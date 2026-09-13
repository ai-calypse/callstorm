package loadgen

import (
	"context"
	"errors"
	"fmt"
	"io"
	"time"

	"github.com/yakshgandhi/callstorm/internal/bus"
)

// fleet is the part of a dispatcher a step needs: hand out a call, and collect
// whatever has come back.
type fleet interface {
	Assign(ctx context.Context, a bus.Assignment) error
	Results(ctx context.Context) ([]bus.CallResult, error)
}

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
	dispatch := &DispatchIntegrity{}

	for _, step := range cfg.Profile.Steps {
		if step.HoldSeconds > 0 {
			// The dispatcher's window is sized in outstanding assignments, so
			// a step that ends on a clock rather than on a call count has no
			// stopping condition it can honour. Refused rather than silently
			// run as something else.
			return nil, fmt.Errorf(
				"step %q is held for %.0fs, and a held step cannot run distributed: "+
					"the dispatcher stops on calls answered, not on the clock",
				step.Name, step.HoldSeconds)
		}
		fmt.Fprintf(cfg.Out, "\n%-12s %-9s concurrency %-4d calls %-4d ",
			step.Name, step.Phase(), step.Concurrency, step.Calls)

		outcomes, workers, sd, err := dispatchStep(ctx, cfg, d, runID, step)
		if err != nil {
			return nil, err
		}
		dispatch.add(sd)

		sr := buildStepReportAt(step, outcomes, cfg.RatePerMinute)
		sr.Nodes = summarizeNodes(outcomes, cfg.Scenario.NodeIDs())
		rep.Steps = append(rep.Steps, sr)

		rep.Calls = append(rep.Calls, callRecords(outcomes)...)

		fmt.Fprintf(cfg.Out, " p95 %.0fms  setup %.0f%%  turns %d  across %d workers",
			sr.TTFA.P95Ms, sr.SetupSuccess*100, sr.TurnsTotal, len(workers))

		if ctx.Err() != nil {
			break
		}
	}

	rep.Duration = time.Since(rep.StartedAt).Seconds()
	rep.EndedAt = time.Now()
	rep.Integrity = &Integrity{Dispatch: dispatch}
	rep.score()
	fmt.Fprintln(cfg.Out)
	return rep, nil
}

// dispatchStep holds step.Concurrency calls in flight until step.Calls have
// been placed and answered for.
func dispatchStep(ctx context.Context, cfg Config, d fleet, runID string, step Step) ([]callOutcome, map[string]int, StepDispatch, error) {
	var (
		outcomes   []callOutcome
		workers    = map[string]int{}
		dispatched int
		received   int
		inFlight   int
		sd         = StepDispatch{Step: step.Name}

		// When each assignment went out, so a result can be placed in the
		// order the run intended rather than the order it came back. Ordering
		// by arrival would sort slow calls into the end of every step, which
		// is exactly the shape drift detection looks for -- and would find it
		// in every run.
		sentAt = map[int]time.Time{}

		// Which calls have already come back. Delivery is at-least-once, so the
		// same call can report twice; counting both would place one call's
		// turns in the step twice and end the step a call early.
		answered = map[int]bool{}
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
				return nil, nil, sd, fmt.Errorf("assign %s/%d: %w", step.Name, dispatched, err)
			}
			sentAt[a.Seq] = time.Now()
			dispatched++
			inFlight++
		}
		sd.Dispatched = dispatched

		poll, cancel := context.WithTimeout(ctx, 2*time.Second)
		results, err := d.Results(poll)
		cancel()
		if err != nil && !errors.Is(err, context.DeadlineExceeded) {
			return nil, nil, sd, err
		}

		for _, r := range results {
			// A result from an earlier step is a straggler, not this step's.
			if r.Step != step.Name {
				sd.Late++
				continue
			}
			if answered[r.Seq] {
				sd.Duplicates++
				continue
			}
			answered[r.Seq] = true
			o := callOutcome{step: r.Step, requestID: r.RequestID, turns: r.Turns, worker: r.Worker,
				startedAt: sentAt[r.Seq], endedAt: time.Now(),
				clockZero: r.ClockZero, callEnded: r.EndedAt, events: r.Events}
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
			break
		}
		if time.Now().After(deadline) {
			// Reported rather than retried. A step that could not be completed
			// is a finding about the fleet, and quietly padding it with retries
			// would hide exactly that.
			fmt.Fprintf(cfg.Out, "\n%-12s gave up waiting: %d of %d calls never came back",
				step.Name, step.Calls-received, step.Calls)
			sd.Missing = step.Calls - received
			for i := received; i < step.Calls; i++ {
				outcomes = append(outcomes, callOutcome{
					step: step.Name,
					err:  errors.New("no worker reported this call"),
				})
			}
			break
		}
	}
	sd.Received = received
	return outcomes, workers, sd, nil
}

// stepBudget is how long a step may take before the fleet is declared unable to
// finish it: the time the calls themselves need, plus room for one consumer
// group rebalance.
func stepBudget(cfg Config, step Step) time.Duration {
	perCall := cfg.TurnTimeout * time.Duration(cfg.Scenario.TurnLimit()+2)
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
