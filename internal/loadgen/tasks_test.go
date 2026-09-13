package loadgen

import (
	"testing"

	"github.com/yakshgandhi/callstorm/internal/judge"
	"github.com/yakshgandhi/callstorm/internal/metrics"
)

// call builds one conversation whose transcript matches or mangles the script,
// so a known word error rate comes out the other end.
func call(step, id string, said, heard []string) CallRecord {
	c := CallRecord{Step: step, RequestID: id}
	for i := range said {
		c.Turns = append(c.Turns, metrics.TurnMetric{
			Turn: i + 1, CallerText: said[i], HeardText: heard[i],
		})
	}
	return c
}

func met(step, id string, ok bool) judge.Judgement {
	return judge.Judgement{
		Step: step, RequestID: id,
		Outcomes: []judge.Outcome{{Criterion: "confirmed the order", Met: ok, Evidence: "turn 2: quoted"}},
	}
}

func TestSummarizeJudgementsJoinsOnStepAndRequest(t *testing.T) {
	calls := []CallRecord{
		call("c1", "a", []string{"one two three four"}, []string{"one two three four"}),
		call("c1", "b", []string{"one two three four"}, []string{"one two three five"}),
		// Same request ID under a different step: the join must not confuse
		// the two, which is the reason it is keyed on both.
		call("c2", "a", []string{"one two three four"}, []string{"one two three four"}),
	}
	js := []judge.Judgement{met("c1", "a", true), met("c1", "b", false), met("c2", "a", false)}

	ts := SummarizeJudgements(calls, js, "test", []string{"confirmed the order"})
	if ts == nil {
		t.Fatal("no summary")
	}
	if ts.Judged != 3 || ts.Passed != 1 {
		t.Fatalf("judged %d passed %d, want 3 and 1", ts.Judged, ts.Passed)
	}
	if len(ts.Calls) != 3 {
		t.Fatalf("joined %d calls, want 3", len(ts.Calls))
	}

	byKey := map[string]judge.CallScore{}
	for _, s := range ts.Calls {
		byKey[s.Step+"/"+s.RequestID] = s
	}
	if got := byKey["c2/a"]; got.Met {
		t.Errorf("c2/a took c1/a's verdict: the join ignored the step")
	}
	if got := byKey["c1/b"].WER; got != 0.25 {
		t.Errorf("c1/b WER = %v, want 0.25 (one word in four)", got)
	}
}

func TestSummarizeJudgementsCountsMissesPerCriterion(t *testing.T) {
	criteria := []string{"confirmed the order", "offered a refund"}
	mk := func(id string, a, b bool) judge.Judgement {
		return judge.Judgement{Step: "c1", RequestID: id, Outcomes: []judge.Outcome{
			{Criterion: criteria[0], Met: a, Evidence: "turn 1: order 4481"},
			{Criterion: criteria[1], Met: b, Evidence: "turn 3: no refund offered"},
		}}
	}
	ts := SummarizeJudgements(nil, []judge.Judgement{
		mk("a", true, false), mk("b", true, false), mk("c", true, true),
	}, "test", criteria)

	if len(ts.Misses) != 2 {
		t.Fatalf("%d criteria tallied, want 2", len(ts.Misses))
	}
	if ts.Misses[0].Missed != 0 || ts.Misses[0].Judged != 3 {
		t.Errorf("criterion 0: %d of %d missed, want 0 of 3", ts.Misses[0].Missed, ts.Misses[0].Judged)
	}
	if ts.Misses[1].Missed != 2 {
		t.Errorf("criterion 1: %d missed, want 2", ts.Misses[1].Missed)
	}
	if ts.Misses[1].Evidence == "" {
		t.Error("a missed criterion carries no quote, so nobody can check it")
	}
	// A criterion every conversation met must not borrow the failing one's
	// quote: the evidence is attached to the miss, not to the row.
	if ts.Misses[0].Evidence != "" {
		t.Errorf("criterion 0 carries evidence %q but was never missed", ts.Misses[0].Evidence)
	}
}

func TestSummarizeJudgementsKeepsErroredApartFromFailed(t *testing.T) {
	js := []judge.Judgement{
		met("c1", "a", true),
		{Step: "c1", RequestID: "b", Err: "judge returned no verdicts"},
	}
	ts := SummarizeJudgements(nil, js, "test", []string{"confirmed the order"})
	if ts.Judged != 1 || ts.Passed != 1 || ts.Errored != 1 {
		t.Fatalf("judged %d passed %d errored %d, want 1/1/1", ts.Judged, ts.Passed, ts.Errored)
	}
	// A judgement that never ran contributes nothing to the per-criterion
	// tally either: counting it as a miss would blame the agent for the judge.
	if ts.Misses[0].Judged != 1 {
		t.Errorf("criterion judged %d times, want 1", ts.Misses[0].Judged)
	}
}

func TestSummarizeJudgementsNilWithoutJudgements(t *testing.T) {
	if ts := SummarizeJudgements([]CallRecord{{Step: "c1"}}, nil, "test", []string{"x"}); ts != nil {
		t.Fatal("a run that was never judged must not carry an empty judge block")
	}
}
