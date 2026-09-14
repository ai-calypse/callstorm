package bus

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/twmb/franz-go/pkg/kgo"
)

// The later offset finishing first must not acknowledge the earlier call.
// A replacement consumer must receive BOTH records from the unfinished batch.
func TestUnfinishedBatchRedeliversAfterWorkerExit(t *testing.T) {
	brokers := newBroker(t, AssignmentTopic, ResultTopic)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	producer, err := kgo.NewClient(kgo.SeedBrokers(brokers...))
	if err != nil {
		t.Fatal(err)
	}
	defer producer.Close()
	for seq := 0; seq < 2; seq++ {
		b, _ := json.Marshal(Assignment{Run: "recovery", Step: "load", Seq: seq})
		if err := producer.ProduceSync(ctx, &kgo.Record{Topic: AssignmentTopic, Value: b}).FirstErr(); err != nil {
			t.Fatal(err)
		}
	}
	w, err := NewWorker(brokers, "recovery-test", "original")
	if err != nil {
		t.Fatal(err)
	}
	defer w.Close()
	jobs, err := w.Next(ctx, 2)
	if err != nil || len(jobs) != 2 {
		t.Fatalf("jobs=%d err=%v", len(jobs), err)
	}
	if err := w.Complete(ctx, CallResult{Run: "recovery", Step: "load", Seq: 1}, jobs[1]); err != nil {
		t.Fatal(err)
	}
	if err := w.CommitBatch(ctx); err == nil {
		t.Fatal("committed across unfinished offset")
	}
	w.Close()
	replacement, err := NewWorker(brokers, "recovery-test", "replacement")
	if err != nil {
		t.Fatal(err)
	}
	defer replacement.Close()
	replay, err := replacement.Next(ctx, 2)
	if err != nil || len(replay) != 2 {
		t.Fatalf("replayed=%d err=%v; want both assignments", len(replay), err)
	}
	for _, job := range replay {
		if err := replacement.Complete(ctx, CallResult{Run: "recovery", Seq: job.Assignment.Seq}, job); err != nil {
			t.Fatal(err)
		}
	}
	if err := replacement.CommitBatch(ctx); err != nil {
		t.Fatal(err)
	}
	replacement.Close()
	b, _ := json.Marshal(Assignment{Run: "recovery", Seq: 2})
	if err := producer.ProduceSync(ctx, &kgo.Record{Topic: AssignmentTopic, Value: b}).FirstErr(); err != nil {
		t.Fatal(err)
	}
	third, err := NewWorker(brokers, "recovery-test", "third")
	if err != nil {
		t.Fatal(err)
	}
	defer third.Close()
	got, err := third.Next(ctx, 3)
	if err != nil || len(got) != 1 || got[0].Assignment.Seq != 2 {
		t.Fatalf("committed batch replayed: %+v err=%v", got, err)
	}
}

func TestDispatcherReceivesResultProducedBeforeFirstPoll(t *testing.T) {
	brokers := newBroker(t)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	d, err := NewDispatcher(ctx, brokers, "fast")
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	b, _ := json.Marshal(CallResult{Run: "fast", Seq: 7})
	if err := d.client.ProduceSync(ctx, &kgo.Record{Topic: ResultTopic, Value: b}).FirstErr(); err != nil {
		t.Fatal(err)
	}
	got, err := d.Results(ctx)
	if err != nil || len(got) != 1 || got[0].Seq != 7 {
		t.Fatalf("fast result lost: %+v err=%v", got, err)
	}
}

func TestMalformedAssignmentDoesNotCommitPastValidCall(t *testing.T) {
	brokers := newBroker(t, AssignmentTopic, ResultTopic)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	p, err := kgo.NewClient(kgo.SeedBrokers(brokers...))
	if err != nil {
		t.Fatal(err)
	}
	defer p.Close()
	for _, value := range []string{`{"run":"test","seq":0}`, `invalid-json`} {
		if err := p.ProduceSync(ctx, &kgo.Record{Topic: AssignmentTopic, Value: []byte(value)}).FirstErr(); err != nil {
			t.Fatal(err)
		}
	}
	w, err := NewWorker(brokers, "poison-test", "worker")
	if err != nil {
		t.Fatal(err)
	}
	defer w.Close()
	if _, err := w.Next(ctx, 2); err == nil {
		t.Fatal("malformed assignment was silently skipped")
	}
	if err := w.CommitBatch(ctx); err == nil {
		t.Fatal("committed past malformed assignment")
	}
}
