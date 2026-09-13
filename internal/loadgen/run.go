package loadgen

import (
	"context"
	"fmt"
	"io"
	"sync"
	"sync/atomic"
	"time"

	"github.com/yakshgandhi/callstorm/internal/bus"
	"github.com/yakshgandhi/callstorm/internal/metrics"
	"github.com/yakshgandhi/callstorm/internal/scenario"
	"github.com/yakshgandhi/callstorm/internal/telemetry"
	"github.com/yakshgandhi/callstorm/internal/tts"
	"github.com/yakshgandhi/callstorm/internal/worker"
)

type Config struct {
	APIKey      string
	Scenario    *scenario.Scenario
	Profile     *Profile
	TTS         *tts.Client
	SampleRate  int
	TurnTimeout time.Duration
	TargetURL   string
	Out         io.Writer

	// Metrics publishes the run live over Prometheus. Nil disables it.
	Metrics *telemetry.Metrics

	// Bus publishes per-turn events to Kafka. Nil disables it.
	Bus   *bus.Producer
	RunID string

	// RatePerMinute prices a run. Zero leaves cost unpriced rather than
	// guessed: a stale hardcoded rate is worse than no figure at all.
	RatePerMinute float64
}

// Run executes every step of the profile in order and returns the report.
//
// Steps run one at a time, never overlapping, so that a step's percentiles
// describe that concurrency and nothing else.
func Run(ctx context.Context, cfg Config) (*Report, error) {
	if cfg.Out == nil {
		cfg.Out = io.Discard
	}

	// Warm the utterance cache once, before any load, so that the first calls
	// of the run do not pay a synthesis cost the later ones avoid. Without
	// this the baseline step measures Deepgram's TTS, not the target agent.
	if err := warmCache(cfg); err != nil {
		return nil, err
	}

	rep := &Report{
		Profile:   cfg.Profile.Name,
		Scenario:  cfg.Scenario.Name,
		Target:    cfg.TargetURL,
		Baseline:  cfg.Profile.Baseline,
		StartedAt: time.Now(),
	}

	for _, step := range cfg.Profile.Steps {
		fmt.Fprintf(cfg.Out, "\n%-12s %-9s concurrency %-4d %-10s ",
			step.Name, step.Phase(), step.Concurrency, budgetOf(step))

		outcomes := runStep(ctx, cfg, step)
		sr := buildStepReportAt(step, outcomes, cfg.RatePerMinute)
		rep.Steps = append(rep.Steps, sr)

		for _, o := range outcomes {
			if o.err == nil && len(o.turns) > 0 {
				rep.Calls = append(rep.Calls, CallRecord{
					Step:      o.step,
					RequestID: o.requestID,
					Turns:     o.turns,
				})
			}
		}

		fmt.Fprintf(cfg.Out, " p95 %.0fms  setup %.0f%%  turns %d",
			sr.TTFA.P95Ms, sr.SetupSuccess*100, sr.TurnsTotal)

		if ctx.Err() != nil {
			break
		}
	}

	rep.Duration = time.Since(rep.StartedAt).Seconds()
	rep.score()
	fmt.Fprintln(cfg.Out)
	return rep, nil
}

// budgetOf describes what stops a step, for the progress line.
func budgetOf(step Step) string {
	if step.HoldSeconds > 0 {
		return fmt.Sprintf("hold %.0fs", step.HoldSeconds)
	}
	return fmt.Sprintf("calls %d", step.Calls)
}

// runStep holds Concurrency callers busy until the step's budget runs out --
// either Calls calls placed, or HoldSeconds elapsed.
//
// Each slot pulls from a shared budget rather than being handed a fixed share,
// so a slow call does not leave its slot idle while others finish early.
//
// A held step lets a call that would run past the deadline finish rather than
// cutting it short. Truncating the last wave would fill the end of a soak --
// the half the whole phase exists to look at -- with calls that failed because
// the harness stopped, not because the agent did.
func runStep(ctx context.Context, cfg Config, step Step) []callOutcome {
	var (
		remaining atomic.Int64
		mu        sync.Mutex
		outcomes  []callOutcome
		wg        sync.WaitGroup
	)
	remaining.Store(int64(step.Calls))

	var deadline time.Time
	if step.HoldSeconds > 0 {
		deadline = time.Now().Add(time.Duration(step.HoldSeconds * float64(time.Second)))
	}
	// Called exactly once per iteration: the call-budget branch has a side
	// effect, so asking twice would spend two calls to place one.
	more := func() bool {
		if ctx.Err() != nil {
			return false
		}
		if !deadline.IsZero() {
			return time.Now().Before(deadline)
		}
		return remaining.Add(-1) >= 0
	}

	for i := 0; i < step.Concurrency; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				if !more() {
					return
				}
				started := time.Now()
				cfg.Metrics.CallStarted()
				res, err := worker.Run(ctx, worker.Config{
					APIKey:      cfg.APIKey,
					Scenario:    cfg.Scenario,
					TTS:         cfg.TTS,
					SampleRate:  cfg.SampleRate,
					TurnTimeout: cfg.TurnTimeout,
					TargetURL:   cfg.TargetURL,
					// No recording and no per-call chatter under load: a mixed
					// track costs ~2.9MB per minute per caller.
					Record: false,
					Out:    io.Discard,

					// Observed per turn rather than per call so a step's
					// latency is visible while the step is still running.
					OnTurn: func(t metrics.TurnMetric) {
						cfg.Metrics.ObserveTurn(step.Name, t)
					},
				})

				cfg.Metrics.CallEnded(step.Name, err)

				o := callOutcome{step: step.Name, err: err, startedAt: started, endedAt: time.Now()}
				if err == nil {
					o.requestID = res.RequestID
					o.turns = res.Turns
					for _, t := range res.Turns {
						cfg.Bus.Publish(ctx, bus.NewTurnEvent(cfg.RunID, step.Name, res.RequestID, t))
					}
				}
				mu.Lock()
				outcomes = append(outcomes, o)
				mu.Unlock()

				fmt.Fprint(cfg.Out, ".")
			}
		}()
	}
	wg.Wait()
	return outcomes
}

// warmCache synthesizes every caller line once up front. Synthesis is
// process-wide cached, so every subsequent caller shares the same audio.
func warmCache(cfg Config) error {
	for i, t := range cfg.Scenario.Turns {
		if _, _, err := worker.Synthesize(cfg.TTS, cfg.Scenario, t.Say, cfg.SampleRate); err != nil {
			return fmt.Errorf("warm cache, turn %d: %w", i+1, err)
		}
	}
	return nil
}
