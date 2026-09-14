package loadgen

import (
	"slices"
	"strings"
	"testing"

	"github.com/yakshgandhi/callstorm/internal/judge"
)

func stepOf(name string, turns, failed int) StepReport {
	return StepReport{Step: Step{Name: name}, TurnsTotal: turns, TurnsFailed: failed}
}

// Hamming's gate for task completion under load, read as points: within 5
// passes, 5 to 10 warns, more fails. Below 30 checks a rate is not compared at all.
func TestQualityHoldsTaskChecksToTheGate(t *testing.T) {
	base := stepOf("c1", 200, 0)
	base.Nodes = []NodeStats{{Node: "refund", Checked: 50, PassRate: 0.96}}
	for _, c := range []struct {
		name    string
		rate    float64
		checked int
		want    string
	}{
		{"a 3 point drop passes", 0.93, 50, "pass"},
		{"a 6 point drop warns", 0.90, 50, "warn"},
		{"a 12 point drop fails", 0.84, 50, "fail"},
		{"too few checks is not compared", 0.50, 20, "pass"},
	} {
		s := stepOf("c10", 200, 0)
		s.Nodes = []NodeStats{{Node: "refund", Checked: c.checked, PassRate: c.rate}}
		q := qualityOf(s, base, nil)
		if q.Verdict != c.want {
			t.Errorf("%s: verdict %s, want %s (%v)", c.name, q.Verdict, c.want, q.Reasons)
		}
		if compared := slices.Contains(q.Compared, "the refund check"); compared != (c.checked >= minQualitySample) {
			t.Errorf("%s: refund check compared = %t with %d checks", c.name, compared, c.checked)
		}
	}
}

// A rise past 2 points of words misheard warns; it fails only when it also
// leaves the step above 15%, where recognition is poor.
func TestQualityHearingWarnsOnARiseAndFailsWhenPoor(t *testing.T) {
	for _, c := range []struct {
		base, step float64
		want       string
	}{{0.03, 0.045, "pass"}, {0.03, 0.055, "warn"}, {0.13, 0.16, "fail"}} {
		base, s := stepOf("c1", 200, 0), stepOf("c10", 200, 0)
		base.WER, s.WER = WERStats{Turns: 100, Mean: c.base}, WERStats{Turns: 100, Mean: c.step}
		if q := qualityOf(s, base, nil); q.Verdict != c.want {
			t.Errorf("wer %.3f to %.3f: verdict %s, want %s", c.base, c.step, q.Verdict, c.want)
		}
	}
}

func TestQualityInterruptionsWarnOnARiseAndFailPastTenPercent(t *testing.T) {
	for _, c := range []struct {
		step float64
		want string
	}{{0.05, "pass"}, {0.08, "warn"}, {0.12, "fail"}} {
		base, s := stepOf("c1", 200, 0), stepOf("c10", 200, 0)
		base.Conversation = Conversation{Turns: 100, InterruptionRate: 0.02}
		s.Conversation = Conversation{Turns: 100, InterruptionRate: c.step}
		if q := qualityOf(s, base, nil); q.Verdict != c.want {
			t.Errorf("interruptions 2%% to %.0f%%: verdict %s, want %s", c.step*100, q.Verdict, c.want)
		}
	}
}

// Failed turns are held to Hamming's error-rate bands at any load.
func TestQualityFailedTurnsUseTheBands(t *testing.T) {
	for _, c := range []struct {
		turns, failed int
		want          string
	}{{150, 0, "pass"}, {300, 1, "pass"}, {150, 1, "warn"}, {150, 2, "fail"}, {0, 0, "n/a"}} {
		if q := qualityOf(stepOf("c10", c.turns, c.failed), StepReport{}, nil); q.Verdict != c.want {
			t.Errorf("%d of %d failed: verdict %s, want %s", c.failed, c.turns, q.Verdict, c.want)
		}
	}
}

// The judge's pass rate counts once 30 calls a step are graded, and the quality
// breakpoint is the first step to fail, whatever its latency did. A failing
// reason is listed before a warning.
func TestQualityBreakpointIsTheFirstStepThatStoppedDoingTheJob(t *testing.T) {
	var calls []judge.CallScore
	grade := func(step string, passed, of int) {
		for i := 0; i < of; i++ {
			calls = append(calls, judge.CallScore{Step: step, Judged: true, Met: i < passed})
		}
	}
	grade("c1", 30, 30)
	grade("c5", 29, 30)
	grade("c10", 24, 30)

	rep := &Report{Baseline: "c1", Judge: &TaskSuccess{Calls: calls},
		Steps: []StepReport{stepOf("c1", 150, 0), stepOf("c5", 150, 0), stepOf("c10", 150, 1)}}
	rep.ScoreQuality()

	if v := rep.Steps[1].Quality.Verdict; v != "pass" {
		t.Errorf("c5, 97%% against 100%%: verdict %s, want pass", v)
	}
	bp := rep.QualityBreakpoint()
	if bp == nil || bp.Name != "c10" {
		t.Fatalf("quality breakpoint %v, want c10", bp)
	}
	r := bp.Quality.Reasons
	if len(r) != 2 || !strings.HasPrefix(r[0], "the judge passed 80% of 30 calls, against 100% of 30") ||
		!strings.HasPrefix(r[1], "1 of 150 turns failed") {
		t.Errorf("reasons %q, want the judge's failure first, then the failed turn", r)
	}
}
