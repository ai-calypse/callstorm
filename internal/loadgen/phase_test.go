package loadgen

import (
	"testing"
	"time"

	"github.com/yakshgandhi/callstorm/internal/metrics"
)

// held builds one call's worth of outcomes: n turns, each answering in ttfa,
// started at the given offset from an arbitrary epoch.
func held(step string, offset time.Duration, ttfa time.Duration, turns int) callOutcome {
	epoch := time.Date(2026, 9, 13, 12, 0, 0, 0, time.UTC)
	o := callOutcome{
		step:      step,
		startedAt: epoch.Add(offset),
		endedAt:   epoch.Add(offset + 10*time.Second),
	}
	for i := 0; i < turns; i++ {
		o.turns = append(o.turns, metrics.TurnMetric{Turn: i + 1, TTFA: ttfa})
	}
	return o
}

func TestPhaseFindsDriftUnderUnchangingLoad(t *testing.T) {
	// Twelve calls at a fixed concurrency: the first six answer in 400ms, the
	// last six in 900ms. Nothing about the load changed, so a single p95 for
	// the step reports one number and hides the whole finding.
	var outcomes []callOutcome
	for i := 0; i < 6; i++ {
		outcomes = append(outcomes, held("soak", time.Duration(i)*time.Minute, 400*time.Millisecond, 4))
	}
	for i := 6; i < 12; i++ {
		outcomes = append(outcomes, held("soak", time.Duration(i)*time.Minute, 900*time.Millisecond, 4))
	}

	p := summarizePhase(Step{Name: "soak", Kind: KindSoak, Concurrency: 4}, outcomes)
	if p.Kind != KindSoak {
		t.Fatalf("kind %q, want soak", p.Kind)
	}
	if p.Drift != DriftDrifting {
		t.Fatalf("drift %q (%.2f), want drifting", p.Drift, p.DriftPct)
	}
	if p.FirstHalf.P50Ms != 400 || p.SecondHalf.P50Ms != 900 {
		t.Errorf("halves %v / %v, want 400 / 900", p.FirstHalf.P50Ms, p.SecondHalf.P50Ms)
	}
	if p.DriftPct != 1.25 {
		t.Errorf("drift %v, want 1.25", p.DriftPct)
	}
	if p.HeldSeconds < 600 {
		t.Errorf("held %vs, want at least the 11 minutes between first and last call", p.HeldSeconds)
	}
}

func TestPhaseOrdersByStartNotByArrival(t *testing.T) {
	// The slow calls were placed first and finished last. Sorting by when a
	// call came back would put them in the second half and report drift in a
	// step that never drifted.
	var outcomes []callOutcome
	for i := 0; i < 6; i++ {
		o := held("spike", time.Duration(i)*time.Second, 900*time.Millisecond, 4)
		o.endedAt = o.endedAt.Add(5 * time.Minute)
		outcomes = append(outcomes, o)
	}
	for i := 6; i < 12; i++ {
		outcomes = append(outcomes, held("spike", time.Duration(i)*time.Second, 400*time.Millisecond, 4))
	}
	p := summarizePhase(Step{Name: "spike", Kind: KindSpike}, outcomes)
	if p.Drift != DriftSettling {
		t.Fatalf("drift %q (%.2f), want settling: the slow calls were the early ones", p.Drift, p.DriftPct)
	}
}

func TestPhaseWithholdsAVerdictOnTooFewTurns(t *testing.T) {
	// Two calls of one turn each. The medians differ wildly, and the honest
	// answer is that the step was too short to tell -- not "steady".
	outcomes := []callOutcome{
		held("c1", 0, 200*time.Millisecond, 1),
		held("c1", time.Second, 2000*time.Millisecond, 1),
	}
	p := summarizePhase(Step{Name: "c1"}, outcomes)
	if p.Drift != DriftUnknown {
		t.Fatalf("drift %q, want unknown on two turns", p.Drift)
	}
	if p.DriftPct != 0 {
		t.Errorf("drift %v reported without enough samples to support it", p.DriftPct)
	}
}

func TestPhaseIgnoresFailedCalls(t *testing.T) {
	var outcomes []callOutcome
	for i := 0; i < 12; i++ {
		outcomes = append(outcomes, held("c1", time.Duration(i)*time.Second, 400*time.Millisecond, 4))
	}
	// A call that never connected has no turns; it must not shift the split.
	outcomes = append(outcomes, callOutcome{step: "c1", err: errTest})
	p := summarizePhase(Step{Name: "c1"}, outcomes)
	if p.FirstHalf.N != p.SecondHalf.N {
		t.Fatalf("halves %d / %d turns: a failed call landed in the split", p.FirstHalf.N, p.SecondHalf.N)
	}
	if p.Drift != DriftSteady {
		t.Errorf("drift %q, want steady", p.Drift)
	}
}

func TestRecoveryScoredAgainstBaseline(t *testing.T) {
	mk := func(name, kind string, ttfa time.Duration) StepReport {
		var outcomes []callOutcome
		for i := 0; i < 8; i++ {
			outcomes = append(outcomes, held(name, time.Duration(i)*time.Second, ttfa, 4))
		}
		return buildStepReport(Step{Name: name, Kind: kind, Concurrency: 2}, outcomes)
	}
	rep := &Report{Baseline: "base", Steps: []StepReport{
		mk("base", KindRamp, 400*time.Millisecond),
		mk("stress", KindStress, 1200*time.Millisecond),
		mk("recover", KindRecovery, 420*time.Millisecond),
	}}
	rep.score()

	if !rep.Steps[2].Phase.Recovered {
		t.Errorf("recovery at %vms vs baseline %vms not marked recovered",
			rep.Steps[2].TTFA.P95Ms, rep.Steps[0].TTFA.P95Ms)
	}
	// A step that is not a recovery never claims to have recovered, however
	// close to baseline it lands.
	if rep.Steps[0].Phase.Recovered {
		t.Error("the baseline step claims to be a recovery")
	}

	rep.Steps[2] = mk("recover", KindRecovery, 1100*time.Millisecond)
	rep.score()
	if rep.Steps[2].Phase.Recovered {
		t.Error("a step still at stress latency was marked recovered")
	}
}

func TestHasPhasesIsFalseForAPlainRamp(t *testing.T) {
	rep := &Report{Steps: []StepReport{
		buildStepReport(Step{Name: "c1"}, []callOutcome{held("c1", 0, 400*time.Millisecond, 2)}),
	}}
	if rep.HasPhases() {
		t.Error("a one-step ramp with no drift verdict claims to have phases to show")
	}
}
