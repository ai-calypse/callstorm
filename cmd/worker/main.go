// Command worker places calls handed to it over Kafka.
//
// It is the horizontally scaled half of Callstorm. The dispatcher decides what
// the run looks like and how many calls may be in flight; a worker is pure
// capacity, and knows nothing beyond the assignment in front of it.
//
// That split is what makes the fleet disposable. The scenario travels in the
// assignment, so a pod that starts mid-run is useful immediately, and an
// assignment is only committed once its result is published, so a pod killed
// mid-call hands that call back to the group rather than taking it to the
// grave.
package main

import (
	"context"
	"flag"
	"io"
	"log"
	"os"
	"os/signal"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/yakshgandhi/callstorm/internal/bus"
	"github.com/yakshgandhi/callstorm/internal/config"
	"github.com/yakshgandhi/callstorm/internal/metrics"
	"github.com/yakshgandhi/callstorm/internal/telemetry"
	"github.com/yakshgandhi/callstorm/internal/tts"
	"github.com/yakshgandhi/callstorm/internal/worker"
)

func main() {
	var (
		brokers     = flag.String("kafka", "localhost:9092", "comma-separated Kafka brokers")
		group       = flag.String("group", "callstorm-workers", "consumer group sharing a run's calls")
		name        = flag.String("name", "", "worker name (defaults to hostname, so a pod names itself)")
		cacheDir    = flag.String("cache", "/cache", "directory for synthesized caller audio")
		sampleRate  = flag.Int("rate", 24000, "audio sample rate in Hz")
		slots       = flag.Int("slots", 8, "calls this worker will place at once")
		metricsAddr = flag.String("metrics", ":9464", "expose Prometheus metrics here")
		envPath     = flag.String("env", "", "optional KEY=VALUE credentials file")
	)
	flag.Parse()

	if *envPath != "" {
		if _, err := config.LoadDotEnv(*envPath); err != nil {
			log.Fatalf("read %s: %v", *envPath, err)
		}
	}
	apiKey := os.Getenv("DEEPGRAM_API_KEY")
	if apiKey == "" {
		log.Fatal("DEEPGRAM_API_KEY is not set")
	}

	if *name == "" {
		if h, err := os.Hostname(); err == nil {
			*name = h
		} else {
			*name = "worker"
		}
	}

	// SIGTERM is how Kubernetes asks a pod to stop, and how a chaos run kills
	// one. Catching it lets calls in flight finish and report before the
	// process goes, which is the difference between a drained pod and a lost
	// call.
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	mx := telemetry.New()
	go func() {
		if err := mx.Serve(ctx, *metricsAddr); err != nil {
			log.Printf("metrics: %v", err)
		}
	}()

	w, err := bus.NewWorker(strings.Split(*brokers, ","), *group, *name)
	if err != nil {
		log.Fatal(err)
	}
	defer w.Close()

	synth := tts.New(apiKey, *sampleRate, *cacheDir)

	log.Printf("worker %s up: brokers=%s group=%s slots=%d cache=%s",
		*name, *brokers, *group, *slots, *cacheDir)

	var (
		wg        sync.WaitGroup
		sem       = make(chan struct{}, *slots)
		placed    atomic.Int64
		failures  atomic.Int64
		abandoned atomic.Int64
	)

	for ctx.Err() == nil {
		jobs, err := w.Next(ctx)
		if err != nil {
			log.Printf("poll: %v", err)
			time.Sleep(time.Second)
			continue
		}
		for _, job := range jobs {
			wg.Add(1)
			sem <- struct{}{}
			go func(job bus.Job) {
				defer wg.Done()
				defer func() { <-sem }()

				res := place(ctx, job.Assignment, apiKey, synth, mx)

				// A call that died because this worker is shutting down did
				// not fail: nothing was learned about the agent. Reporting it
				// as a failure would turn a pod being killed into a step that
				// scored 69% setup and blamed the target for it.
				//
				// So say nothing and commit nothing. The assignment stays
				// uncommitted and the group hands it to a surviving pod, which
				// is the whole reason commits are manual.
				if ctx.Err() != nil && !complete(res) {
					abandoned.Add(1)
					log.Printf("abandoning %s/%d for redelivery: worker is shutting down",
						res.Step, res.Seq)
					return
				}

				if res.Err != "" {
					failures.Add(1)
				}
				placed.Add(1)

				// Completing uses a background context on purpose: a worker
				// being shut down still owes the dispatcher an answer for the
				// call it actually finished, and a cancelled context here
				// would lose it.
				done, cancel := context.WithTimeout(context.WithoutCancel(ctx), 15*time.Second)
				defer cancel()
				if err := w.Complete(done, res, job); err != nil {
					log.Printf("complete %s/%d: %v", res.Step, res.Seq, err)
				}
			}(job)
		}
	}

	log.Printf("worker %s draining %d in flight", *name, len(sem))
	wg.Wait()
	log.Printf("worker %s done: %d calls placed, %d failed, %d abandoned for redelivery",
		*name, placed.Load(), failures.Load(), abandoned.Load())
}

// complete reports whether a call actually ran to the end.
//
// A cancelled call usually comes back without an error at all: the call itself
// succeeded, and the cancellation shows up as failed turns inside it. Checking
// only the error missed that, and a killed pod still contributed half-finished
// conversations to the run -- five turns marked "cancelled" that said nothing
// about the agent and everything about the pod.
func complete(res bus.CallResult) bool {
	if res.Err != "" {
		return false
	}
	for _, t := range res.Turns {
		if t.Failed {
			return false
		}
	}
	return len(res.Turns) > 0
}

// place runs one assignment and shapes whatever happened into a result.
func place(ctx context.Context, a bus.Assignment, apiKey string, synth *tts.Client, mx *telemetry.Metrics) bus.CallResult {
	res := bus.CallResult{Run: a.Run, Step: a.Step, Seq: a.Seq}

	mx.CallStarted()
	out, err := worker.Run(ctx, worker.Config{
		APIKey:      apiKey,
		Scenario:    a.Scenario,
		TTS:         synth,
		SampleRate:  a.SampleRate,
		TurnTimeout: a.TurnTimeout(),
		TargetURL:   a.TargetURL,
		Record:      false,
		Out:         io.Discard,
		OnTurn: func(t metrics.TurnMetric) {
			mx.ObserveTurn(a.Step, t)
		},
	})
	mx.CallEnded(a.Step, err)

	if err != nil {
		res.Err = err.Error()
		return res
	}
	res.RequestID = out.RequestID
	res.Turns = out.Turns
	res.ClockZero, res.EndedAt, res.Events = out.ClockZero, out.EndedAt, out.Events
	return res
}
