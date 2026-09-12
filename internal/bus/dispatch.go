package bus

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/twmb/franz-go/pkg/kadm"
	"github.com/twmb/franz-go/pkg/kerr"
	"github.com/twmb/franz-go/pkg/kgo"

	"github.com/yakshgandhi/callstorm/internal/metrics"
	"github.com/yakshgandhi/callstorm/internal/scenario"
)

// Dispatch topics. Assignments travel one way and results the other, on
// separate topics so a slow reader of one cannot stall the other.
const (
	AssignmentTopic = "callstorm.assignments"
	ResultTopic     = "callstorm.results"

	// assignmentPartitions bounds how many workers can ever share a step's
	// calls. A consumer group hands each partition to exactly one member, so a
	// single-partition topic would pin an entire fleet to one pod no matter
	// what the autoscaler did.
	assignmentPartitions = 12
)

// Assignment is one call for a worker to place.
//
// The scenario travels with it rather than being read from disk, so a worker
// needs no shared volume and no config rollout: a pod that joins mid-run is
// immediately useful. The API key deliberately does not travel here -- it comes
// from the worker's own environment, so credentials never sit in a topic that
// anything with broker access can read.
type Assignment struct {
	Run  string `json:"run"`
	Step string `json:"step"`
	Seq  int    `json:"seq"`

	Scenario      *scenario.Scenario `json:"scenario"`
	TargetURL     string             `json:"target_url,omitempty"`
	SampleRate    int                `json:"sample_rate"`
	TurnTimeoutMs int                `json:"turn_timeout_ms"`
}

// TurnTimeout is the assignment's per-turn deadline.
func (a Assignment) TurnTimeout() time.Duration {
	return time.Duration(a.TurnTimeoutMs) * time.Millisecond
}

// CallResult is what a worker made of one assignment.
type CallResult struct {
	Run  string `json:"run"`
	Step string `json:"step"`
	Seq  int    `json:"seq"`

	// Worker names the pod that placed the call, so a run that went wrong can
	// be traced to the machine that made it.
	Worker    string `json:"worker"`
	RequestID string `json:"request_id,omitempty"`

	Turns []metrics.TurnMetric `json:"turns,omitempty"`
	Err   string               `json:"error,omitempty"`
}

// Dispatcher hands calls out to a worker fleet and gathers what comes back.
//
// Unlike turn telemetry, dispatch cannot be fire-and-forget. A dropped
// assignment is a call that never happens, and a dropped result is a
// dispatcher that waits forever for it, so every produce here is synchronous
// and every failure is returned.
type Dispatcher struct {
	client *kgo.Client
	admin  *kadm.Client
	run    string
}

// NewDispatcher connects and makes sure the assignment topic is wide enough to
// spread across a fleet.
func NewDispatcher(ctx context.Context, brokers []string, run string) (*Dispatcher, error) {
	cl, err := kgo.NewClient(
		kgo.SeedBrokers(brokers...),
		kgo.ConsumeTopics(ResultTopic),
		// Results are consumed by this one dispatcher for this one run, so it
		// reads from the end: an earlier run's leftovers are not this run's
		// calls, and counting them would end a step early.
		kgo.ConsumeResetOffset(kgo.NewOffset().AtEnd()),
		kgo.AllowAutoTopicCreation(),
	)
	if err != nil {
		return nil, fmt.Errorf("kafka dispatcher: %w", err)
	}

	d := &Dispatcher{client: cl, admin: kadm.NewClient(cl), run: run}
	if err := d.ensureTopics(ctx); err != nil {
		cl.Close()
		return nil, err
	}
	return d, nil
}

func (d *Dispatcher) ensureTopics(ctx context.Context) error {
	// Created explicitly rather than left to auto-creation, which would use the
	// broker's default of one partition and quietly cap the fleet at one
	// working pod however far the autoscaler scaled out.
	//
	// A second run finds the topics already there, which is success, not an
	// error worth stopping for.
	if err := createTopic(ctx, d.admin, AssignmentTopic, assignmentPartitions); err != nil {
		return err
	}
	if err := widenTopic(ctx, d.admin, AssignmentTopic, assignmentPartitions); err != nil {
		return err
	}
	return createTopic(ctx, d.admin, ResultTopic, 1)
}

func createTopic(ctx context.Context, admin *kadm.Client, topic string, partitions int32) error {
	resp, err := admin.CreateTopics(ctx, partitions, 1, nil, topic)
	if err != nil {
		return fmt.Errorf("create %s: %w", topic, err)
	}
	for _, t := range resp {
		if t.Err != nil && !errors.Is(t.Err, kerr.TopicAlreadyExists) {
			return fmt.Errorf("create %s: %w", topic, t.Err)
		}
	}
	return nil
}

// widenTopic grows an assignment topic that already exists but is too narrow to
// spread across a fleet.
//
// A cluster where anything created this topic before the dispatcher did is left
// with a single partition, and a single-partition topic silently caps the fleet
// at one working pod. Partitions can only be added, never removed, which is why
// this is safe to run on every start.
func widenTopic(ctx context.Context, admin *kadm.Client, topic string, want int32) error {
	details, err := admin.ListTopics(ctx, topic)
	if err != nil {
		return fmt.Errorf("describe %s: %w", topic, err)
	}
	have := int32(len(details[topic].Partitions))
	if have >= want {
		return nil
	}
	resp, err := admin.CreatePartitions(ctx, int(want-have), topic)
	if err != nil {
		return fmt.Errorf("widen %s from %d to %d: %w", topic, have, want, err)
	}
	for _, r := range resp {
		if r.Err != nil {
			return fmt.Errorf("widen %s: %w", topic, r.Err)
		}
	}
	return nil
}

// Assign hands one call to the fleet, waiting for the broker to acknowledge it.
//
// Keyed by sequence rather than by step, so one step's calls spread across
// every partition. Keying by step would put a whole concurrency level on a
// single partition, and therefore on a single worker.
func (d *Dispatcher) Assign(ctx context.Context, a Assignment) error {
	b, err := json.Marshal(a)
	if err != nil {
		return err
	}
	rec := &kgo.Record{
		Topic: AssignmentTopic,
		Key:   []byte(fmt.Sprintf("%s-%d", a.Step, a.Seq)),
		Value: b,
	}
	return d.client.ProduceSync(ctx, rec).FirstErr()
}

// Results returns whatever finished calls have arrived, for this run only.
func (d *Dispatcher) Results(ctx context.Context) ([]CallResult, error) {
	fetches := d.client.PollFetches(ctx)
	if err := fetches.Err(); err != nil {
		if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
			return nil, nil
		}
		return nil, err
	}

	var out []CallResult
	fetches.EachRecord(func(r *kgo.Record) {
		var res CallResult
		if err := json.Unmarshal(r.Value, &res); err != nil {
			return
		}
		// Another run's results are not this run's calls.
		if res.Run != d.run {
			return
		}
		// Durations do not survive JSON; rebuild them from the millisecond
		// mirrors so a turn that crossed the bus measures the same as one that
		// did not.
		for i := range res.Turns {
			res.Turns[i].Rehydrate()
		}
		out = append(out, res)
	})
	return out, nil
}

func (d *Dispatcher) Close() { d.client.Close() }

// Worker is the other end: it takes assignments and reports what happened.
type Worker struct {
	client *kgo.Client
	name   string
}

// NewWorker joins the consumer group that shares out a run's calls.
//
// Commits are manual on purpose. An assignment is only marked done once its
// call has finished and its result is published, so a pod killed mid-call
// leaves its assignment uncommitted and the group hands that call to a
// surviving pod. Automatic commits would mark the work done the moment it was
// received, and a killed pod would take its calls with it.
func NewWorker(brokers []string, group, name string) (*Worker, error) {
	cl, err := kgo.NewClient(
		kgo.SeedBrokers(brokers...),
		kgo.ConsumeTopics(AssignmentTopic),
		kgo.ConsumerGroup(group),
		kgo.ConsumeResetOffset(kgo.NewOffset().AtStart()),
		kgo.DisableAutoCommit(),
		// Deliberately not AllowAutoTopicCreation. Workers start before the
		// dispatcher, and a consumer that auto-creates its topic gets the
		// broker default of one partition -- which a consumer group hands to
		// exactly one member, pinning the whole fleet to a single pod no
		// matter how far it is scaled. The dispatcher owns topic creation
		// because only it knows how wide the topic has to be.
	)
	if err != nil {
		return nil, fmt.Errorf("kafka worker: %w", err)
	}
	return &Worker{client: cl, name: name}, nil
}

// Job is one assignment together with the broker record that carried it, so
// the right offset is committed when the right call finishes. Keeping them
// paired rather than in two slices removes the chance of committing one call's
// assignment because another one succeeded.
type Job struct {
	Assignment Assignment
	rec        *kgo.Record
}

// Next blocks until assignments arrive or ctx ends.
func (w *Worker) Next(ctx context.Context) ([]Job, error) {
	fetches := w.client.PollFetches(ctx)
	if err := fetches.Err(); err != nil {
		if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
			return nil, nil
		}
		return nil, err
	}

	var jobs []Job
	var undecodable []*kgo.Record
	fetches.EachRecord(func(r *kgo.Record) {
		var a Assignment
		if err := json.Unmarshal(r.Value, &a); err != nil {
			// Work that cannot be read is still work taken off the queue.
			// Committing it stops it being redelivered forever.
			undecodable = append(undecodable, r)
			return
		}
		jobs = append(jobs, Job{Assignment: a, rec: r})
	})
	if len(undecodable) > 0 {
		_ = w.client.CommitRecords(ctx, undecodable...)
	}
	return jobs, nil
}

// Complete publishes a result and only then marks the assignment done.
func (w *Worker) Complete(ctx context.Context, res CallResult, job Job) error {
	res.Worker = w.name
	b, err := json.Marshal(res)
	if err != nil {
		return err
	}
	if err := w.client.ProduceSync(ctx, &kgo.Record{Topic: ResultTopic, Value: b}).FirstErr(); err != nil {
		// Leave the assignment uncommitted: better that the call is placed
		// twice than silently lost from the run.
		return fmt.Errorf("publish result: %w", err)
	}
	if job.rec != nil {
		return w.client.CommitRecords(ctx, job.rec)
	}
	return nil
}

func (w *Worker) Name() string { return w.name }
func (w *Worker) Close()       { w.client.Close() }
