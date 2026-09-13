package loadgen

import (
	"context"
	"io"
	"testing"
	"time"

	"github.com/yakshgandhi/callstorm/internal/bus"
	"github.com/yakshgandhi/callstorm/internal/metrics"
	"github.com/yakshgandhi/callstorm/internal/scenario"
)

// stepAt builds a step whose every turn took ttfa.
func stepAt(name string, ttfa time.Duration) StepReport {
	var turns []metrics.TurnMetric
	for i := 0; i < 20; i++ {
		turns = append(turns, metrics.TurnMetric{TTFA: ttfa})
	}
	return buildStepReport(Step{Name: name, Concurrency: 2, Calls: 2},
		[]callOutcome{{step: name, turns: turns}})
}

func TestImpairedCohortIsScoredAgainstTheCleanBaseline(t *testing.T) {
	clean := &Report{Baseline: "c2", Steps: []StepReport{
		stepAt("c2", 500*time.Millisecond), stepAt("c8", 520*time.Millisecond)}}
	clean.score()

	// Against its own baseline this cohort degrades by only 1.1x and would
	// pass. Against the clean control it is 2.2x at c8, and fails there.
	severe := &Report{Baseline: "c2", Steps: []StepReport{
		stepAt("c2", 1000*time.Millisecond), stepAt("c8", 1100*time.Millisecond)}}
	severe.score()
	if severe.Breakpoint() != nil {
		t.Fatalf("precondition: severe should pass against its own baseline")
	}

	severe.compareToClean(clean)
	clean.markVsClean(severe)
	clean.markVsClean(clean)

	bp := severe.Breakpoint()
	if bp == nil || bp.Name != "c8" {
		t.Fatalf("breakpoint = %v, want c8 (2.0x the clean baseline is not past 2x, 2.2x is)", bp)
	}
	if got := severe.Steps[1].P95Ratio; got != 2.2 {
		t.Errorf("c8 ratio = %v, want 2.2 against the clean baseline", got)
	}
	if got := severe.Steps[0].VsClean; got != 2 {
		t.Errorf("c2 vs clean = %v, want 2", got)
	}
	if got := clean.Steps[1].VsClean; got != 1 {
		t.Errorf("clean c8 vs itself = %v, want 1", got)
	}
}

func TestSummarizeNodes(t *testing.T) {
	visit := func(node string, checked, met bool, miss string) metrics.TurnMetric {
		return metrics.TurnMetric{Node: node, ExpectChecked: checked, ExpectMet: met, ExpectMiss: miss}
	}
	outcomes := []callOutcome{
		{turns: []metrics.TurnMetric{
			visit("open", false, false, ""),
			visit("number", true, true, ""),
			visit("number", true, false, `said none of ["replacement"]`),
		}},
		{turns: []metrics.TurnMetric{visit("open", false, false, ""), visit("number", true, true, "")}},
		// A call that never connected says nothing about any node.
		{err: context.DeadlineExceeded},
	}
	got := summarizeNodes(outcomes, []string{"open", "number", "refund"})

	if len(got) != 3 {
		t.Fatalf("nodes = %+v, want three rows in scenario order", got)
	}
	if got[0].Node != "open" || got[0].Visits != 2 || got[0].Checked != 0 || got[0].PassRate != 0 {
		t.Errorf("open = %+v: a node with no assertion has visits and no rate", got[0])
	}
	if n := got[1]; n.Visits != 3 || n.Checked != 3 || n.Passed != 2 || n.PassRate != 0.667 {
		t.Errorf("number = %+v, want 2 of 3", n)
	}
	if got[1].Miss == "" {
		t.Error("a failing node carries no evidence of why")
	}
	if got[2].Node != "refund" || got[2].Visits != 0 {
		t.Errorf("refund = %+v: an unreached node must be kept, with zero visits", got[2])
	}
}

// fakeFleet answers every assignment at once, then adds whatever extra
// results a test wants delivered alongside.
type fakeFleet struct {
	assigned []bus.Assignment
	served   int
	extra    []bus.CallResult
}

func (f *fakeFleet) Assign(_ context.Context, a bus.Assignment) error {
	f.assigned = append(f.assigned, a)
	return nil
}

func (f *fakeFleet) Results(context.Context) ([]bus.CallResult, error) {
	var out []bus.CallResult
	for ; f.served < len(f.assigned); f.served++ {
		a := f.assigned[f.served]
		out = append(out, bus.CallResult{Run: a.Run, Step: a.Step, Seq: a.Seq, Worker: "w1",
			Turns: []metrics.TurnMetric{{Turn: 1, TTFA: 500 * time.Millisecond}}})
	}
	out = append(out, f.extra...)
	f.extra = nil
	return out, nil
}

func TestDispatchCountsDuplicatesOnceAndStragglersApart(t *testing.T) {
	f := &fakeFleet{extra: []bus.CallResult{
		{Run: "r", Step: "c3", Seq: 1, Worker: "w2"}, // redelivered: a second worker placed it again
		{Run: "r", Step: "c1", Seq: 0, Worker: "w1"}, // an earlier step's call, arriving late
	}}
	cfg := Config{
		Scenario:    &scenario.Scenario{Turns: make([]scenario.Turn, 1)},
		TurnTimeout: time.Second,
		Out:         io.Discard,
	}

	outcomes, _, sd, err := dispatchStep(context.Background(), cfg, f, "r", Step{Name: "c3", Concurrency: 3, Calls: 3})
	if err != nil {
		t.Fatal(err)
	}
	if len(outcomes) != 3 {
		t.Errorf("outcomes = %d, want 3: a duplicate must not be scored as a fourth call", len(outcomes))
	}
	want := StepDispatch{Step: "c3", Dispatched: 3, Received: 3, Duplicates: 1, Late: 1}
	if sd != want {
		t.Errorf("integrity = %+v, want %+v", sd, want)
	}
}
