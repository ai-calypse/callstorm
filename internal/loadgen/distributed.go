package loadgen

import (
	"context"
	"errors"
	"fmt"
	"io"
	"time"

	"github.com/yakshgandhi/callstorm/internal/bus"
)

// RunDistributed executes a profile across a fleet of workers instead of
// goroutines in this process.
//
// The important property is that nothing about the measurement changes. Steps
// still run one at a time, and a step still holds exactly its concurrency in
// flight -- the dispatcher keeps a sliding window of that many outstanding
// assignments and only releases another when one comes back.
//
// That window is why concurrency stays honest under an autoscaler. The
// alternative, dividing a step's calls among however many replicas happen to
// exist, would make the achieved concurrency a property of the fleet size, and
// a step's percentiles would then describe the cluster rather than the agent.
// Workers are capacity; the dispatcher decides what load is.
func RunDistributed(ctx context.Context, cfg Config, d *bus.Dispatcher, runID string) (*Report, error) {
	if cfg.Out == nil {
		cfg.Out = io.Discard
	}

	rep := &Report{
		Profile:   cfg.Profile.Name,
		Scenario:  cfg.Scenario.Name,
		Target:    cfg.TargetURL,
		Baseline:  cfg.Profile.Baseline,
		StartedAt: time.Now(),
	}

	for _, step := range cfg.Profile.Steps {
		fmt.Fprintf(cfg.Out, "\n%-12s concurrency %-4d calls %-4d ",
			step.Name, step.Concurrency, step.Calls)

		outcomes, workers, err := dispatchStep(ctx, cfg, d, runID, step)
		if err != nil {
			return nil, err
		}

		sr := buildStepReportAt(step, outcomes, cfg.RatePerMinute)
		rep.Steps = append(rep.Steps, sr)

		for _, o := range outcomes {
			if o.err == nil && len(o.turns) > 0 {
				rep.Calls = append(rep.Calls, CallRecord{
					Step:      o.step,
					RequestID: o.requestID,
					Worker:    o.worker,
					Turns:     o.turns,
				})
			}
		}

		fmt.Fprintf(cfg.Out, " p95 %.0fms  setup %.0f%%  turns %d  across %d workers",
			sr.TTFA.P95Ms, sr.SetupSuccess*100, sr.TurnsTotal, len(workers))

		if ctx.Err() != nil {
			break
		}
	}

	rep.Duration = time.Since(rep.StartedAt).Seconds()
	rep.score()
	fmt.Fprintln(cfg.Out)
	return rep, nil
}

// dispatchStep holds step.Concurrency calls in flight until step.Calls have
// been placed and answered for.
func dispatchStep(ctx context.Context, cfg Config, d *bus.Dispatcher, runID string, step Step) ([]callOutcome, map[string]int, error) {
	var (
		outcomes   []callOutcome
		workers    = map[string]int{}
		dispatched int
		received   int
		inFlight   int
	)

	// A worker that dies between taking a call and reporting it would otherwise
	// leave the step waiting forever. The group redelivers that assignment, but
	// only after its session times out, so the budget has to be generous enough
	// to cover a rebalance and still bounded.
	deadline := time.Now().Add(stepBudget(cfg, step))

	for received < step.Calls {
		for inFlight < step.Concurrency && dispatched < step.Calls {
			a := bus.Assignment{
				Run:           runID,
				Step:          step.Name,
				Seq:           dispatched,
				Scenario:      cfg.Scenario,
				TargetURL:     cfg.TargetURL,
				SampleRate:    cfg.SampleRate,
				TurnTimeoutMs: int(cfg.TurnTimeout / time.Millisecond),
			}
			if err := d.Assign(ctx, a); err != nil {
				return nil, nil, fmt.Errorf("assign %s/%d: %w", step.Name, dispatched, err)
			}
			dispatched++
			inFlight++
		}

		poll, cancel := context.WithTimeout(ctx, 2*time.Second)
		results, err := d.Results(poll)
		cancel()
		if err != nil && !errors.Is(err, context.DeadlineExceeded) {
			return nil, nil, err
		}

		for _, r := range results {
			// A result from an earlier step is a straggler, not this step's.
			if r.Step != step.Name {
				continue
			}
			o := callOutcome{step: r.Step, requestID: r.RequestID, turns: r.Turns, worker: r.Worker}
			if r.Err != "" {
				o.err = errors.New(r.Err)
			}
			outcomes = append(outcomes, o)
			workers[r.Worker]++
			received++
			inFlight--
			fmt.Fprint(cfg.Out, ".")
		}

		if ctx.Err() != nil {
			return outcomes, workers, nil
		}
		if time.Now().After(deadline) {
			// Reported rather than retried. A step that could not be completed
			// is a finding about the fleet, and quietly padding it with retries
			// would hide exactly that.
			fmt.Fprintf(cfg.Out, "\n%-12s gave up waiting: %d of %d calls never came back",
				step.Name, step.Calls-received, step.Calls)
			for i := received; i < step.Calls; i++ {
				outcomes = append(outcomes, callOutcome{
					step: step.Name,
					err:  errors.New("no worker reported this call"),
				})
			}
			break
		}
	}
	return outcomes, workers, nil
}

// stepBudget is how long a step may take before the fleet is declared unable to
// finish it: the time the calls themselves need, plus room for one consumer
// group rebalance.
func stepBudget(cfg Config, step Step) time.Duration {
	perCall := cfg.TurnTimeout * time.Duration(len(cfg.Scenario.Turns)+2)
	if perCall < time.Minute {
		perCall = time.Minute
	}
	waves := (step.Calls + step.Concurrency - 1) / maxInt(step.Concurrency, 1)
	return perCall*time.Duration(waves) + 2*time.Minute
}

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}
