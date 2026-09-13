package loadgen

import (
	"encoding/json"
	"fmt"
	"testing"
	"time"

	"github.com/yakshgandhi/callstorm/internal/judge"
	"github.com/yakshgandhi/callstorm/internal/metrics"
)

const msec = time.Millisecond

func timed(turn int, caller, agent string, ttfa, think time.Duration) metrics.TurnMetric {
	m := metrics.TurnMetric{Turn: turn, CallerText: caller, AgentText: agent,
		TTFA: ttfa, ThinkSpeak: think, Endpointing: ttfa - think}
	m.Finalize()
	return m
}

// calls whose closing turn is slow in every one of them, the shape the Deepgram
// suite found: about 3.1s to answer "thanks" against about 700ms for the rest.
func closingSlow(step string, calls int) []callOutcome {
	var out []callOutcome
	for i := 0; i < calls; i++ {
		out = append(out, callOutcome{step: step, requestID: fmt.Sprintf("%s-%d", step, i), turns: []metrics.TurnMetric{
			timed(1, "hi", "Can I have your order number?", 700*msec, 400*msec),
			timed(2, "it's 44812", "I will refund that now.", 750*msec, 450*msec),
			timed(3, "thanks", "You're welcome.", 3100*msec+time.Duration(i)*msec, 2800*msec),
		}})
	}
	return out
}

// Pooled, the closing turn is one slow turn in three. By position it is the
// slowest, with the line that set it off.
func TestTurnWaitsFindTheTurnThatIsSlowInEveryCall(t *testing.T) {
	outcomes := append(closingSlow("c1", 4), callOutcome{step: "c1", err: errTest})
	s := buildStepReport(Step{Name: "c1"}, outcomes)

	if len(s.Turns) != 3 {
		t.Fatalf("got %d turns, want 3", len(s.Turns))
	}
	last := s.Turns[2]
	if last.Turn != 3 || last.CallerLine != "thanks" || last.TTFA.N != 4 {
		t.Errorf("closing turn = %+v", last)
	}
	if last.TTFA.P95Ms != 3103 || last.ThinkSpeakP50Ms != 2800 || last.EndpointingP50Ms != 301 {
		t.Errorf("closing turn p95 %.1f think p50 %.1f endpointing p50 %.1f, want 3103, 2800, 301",
			last.TTFA.P95Ms, last.ThinkSpeakP50Ms, last.EndpointingP50Ms)
	}
	if s.Turns[0].TTFA.P95Ms != 700 {
		t.Errorf("first turn p95 %.1f, want 700", s.Turns[0].TTFA.P95Ms)
	}
	if want := (WaitBands{Turns: 12, UpTo800: 8, Over2000: 4}); s.Waits != want {
		t.Errorf("waits %+v, want %+v", s.Waits, want)
	}
}

// Each line belongs to the band below it, so a wait of exactly 800ms meets
// the target. Turns the agent talked over, and turns never answered, have no
// wait to sort.
func TestWaitBandsSortEachTimedTurnOnce(t *testing.T) {
	over := timed(6, "", "", 3000*msec, 0)
	over.CallerYielded = true
	s := buildStepReport(Step{Name: "c1"}, []callOutcome{{step: "c1", turns: []metrics.TurnMetric{
		timed(1, "", "", 800*msec, 0), timed(2, "", "", 801*msec, 0), timed(3, "", "", 1200*msec, 0),
		timed(4, "", "", 2000*msec, 0), timed(5, "", "", 2001*msec, 0), over, timed(7, "", "", 0, 0),
	}}})
	if want := (WaitBands{Turns: 5, UpTo800: 1, To1200: 2, To2000: 1, Over2000: 1}); s.Waits != want {
		t.Errorf("waits %+v, want %+v", s.Waits, want)
	}
}

func TestRepeatedRepliesCountWhatTheAgentSaidAgain(t *testing.T) {
	looped := []metrics.TurnMetric{
		timed(1, "", "Can you give me your order number?", 700*msec, 0),
		timed(2, "", "Sorry, can you give me your order number", 700*msec, 0),
		timed(3, "", "Okay.", 700*msec, 0),
		timed(4, "", "Can you give me the order number please?", 700*msec, 0),
		timed(5, "", "okay", 700*msec, 0),
	}
	clean := []metrics.TurnMetric{
		timed(1, "", "Thank you.", 700*msec, 0),
		timed(2, "", "Thank you, I will refund that.", 700*msec, 0),
	}
	s := buildStepReport(Step{Name: "c1"}, []callOutcome{{step: "c1", turns: looped}, {step: "c1", turns: clean}})
	// The reworded order-number request shares 7 of 8 words with the first;
	// the third wording shares 6 of 9 and is left apart. "okay" repeats "Okay."
	if c := s.Conversation; c.RepeatedReplies != 2 || c.CallsWithRepeats != 1 {
		t.Errorf("repeated %d in %d calls, want 2 in 1", c.RepeatedReplies, c.CallsWithRepeats)
	}
}

// A report written before these figures existed gains them from its calls log,
// matching what the run would have produced.
func TestRefreshRebuildsTheFiguresFromTheCallsLog(t *testing.T) {
	outcomes := closingSlow("c1", 4)
	want := buildStepReport(Step{Name: "c1"}, outcomes)

	b, err := json.Marshal(callRecords(outcomes))
	if err != nil {
		t.Fatal(err)
	}
	var calls []CallRecord
	if err := json.Unmarshal(b, &calls); err != nil {
		t.Fatal(err)
	}
	stale := want
	stale.Turns, stale.Waits, stale.Quality = nil, WaitBands{}, Quality{}
	rep := &Report{Baseline: "c1", Steps: []StepReport{stale}, Calls: calls}

	Refresh(rep)
	got := rep.Steps[0]
	if len(got.Turns) != 3 || got.Turns[2].TTFA.P95Ms != want.Turns[2].TTFA.P95Ms || got.Waits != want.Waits {
		t.Errorf("refreshed turns %+v waits %+v, want %+v and %+v", got.Turns, got.Waits, want.Turns, want.Waits)
	}
	if got.Quality.Verdict != "pass" {
		t.Errorf("quality %+v, want pass", got.Quality)
	}
}

func TestWaitOutcomeSplitsJudgedCallsAtTheLine(t *testing.T) {
	var calls []CallRecord
	var js []judge.Judgement
	for i := 0; i < 10; i++ {
		id := fmt.Sprintf("r%d", i)
		wait := 700.0
		if i < 5 {
			wait = 1500
		}
		calls = append(calls, CallRecord{Step: "c1", RequestID: id, Turns: []metrics.TurnMetric{{Turn: 1, TTFAMs: wait}}})
		// Two of the five slow calls pass; every quick call does.
		js = append(js, met("c1", id, i >= 3))
	}
	w := waitOutcome(calls, js)
	if w.LineMs != 1200 || w.SlowCalls != 5 || w.SlowPassed != 2 || w.QuickCalls != 5 || w.QuickPassed != 5 {
		t.Errorf("split %+v", w)
	}
	if !w.Conclusive || w.SlowPassRate != 0.4 || w.QuickPassRate != 1 {
		t.Errorf("rates %+v, want conclusive 0.4 against 1", w)
	}
	if few := waitOutcome(calls[:3], js[:3]); few.Conclusive || few.Note == "" {
		t.Errorf("three calls read as conclusive: %+v", few)
	}
}
