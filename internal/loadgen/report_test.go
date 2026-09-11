package loadgen

import (
	"math"
	"testing"
	"time"

	"github.com/yakshgandhi/callstorm/internal/metrics"
)

func turn(caller, heard string, yielded bool) metrics.TurnMetric {
	return metrics.TurnMetric{
		CallerText:    caller,
		HeardText:     heard,
		CallerYielded: yielded,
		TTFA:          500 * time.Millisecond,
		Endpointing:   200 * time.Millisecond,
		ThinkSpeak:    300 * time.Millisecond,
		TurnLatency:   time.Second,
	}
}

func TestStepReportWER(t *testing.T) {
	step := Step{Name: "c2", Concurrency: 2, Calls: 2}

	outcomes := []callOutcome{
		{step: "c2", turns: []metrics.TurnMetric{
			// 14 reference words, 2 dropped: the live nova-3 failure.
			turn("Sure, it's four four eight one two. It was a pair of wireless headphones.",
				"Sure. It's four four eight two. Was a pair of wireless headphones.", false),
			// 6 reference words, heard perfectly.
			turn("I would like my money back", "I would like my money back", false),
		}},
		{step: "c2", turns: []metrics.TurnMetric{
			// No transcript at all: must not be scored as a perfect turn.
			turn("Are you still there", "", false),
			// Talked over: the caller never finished the line, so scoring it
			// would charge the agent for words that were never spoken.
			turn("I have been waiting three weeks for this order to arrive", "I have been", true),
		}},
	}

	r := buildStepReport(step, outcomes)

	if got, want := r.WER.Turns, 2; got != want {
		t.Errorf("scored turns = %d, want %d (empty transcript and talked-over turn must be excluded)", got, want)
	}
	if got, want := r.WER.RefWords, 20; got != want {
		t.Errorf("RefWords = %d, want %d", got, want)
	}
	if got, want := r.WER.Deletions, 2; got != want {
		t.Errorf("Deletions = %d, want %d", got, want)
	}

	// Micro-averaged: 2 errors over 20 words spoken, not the mean of the two
	// per-turn rates (which would be (0.143 + 0) / 2 = 0.071).
	if want := 2.0 / 20.0; math.Abs(r.WER.Mean-want) > 1e-9 {
		t.Errorf("Mean = %.4f, want %.4f", r.WER.Mean, want)
	}
	if want := 2.0 / 14.0; math.Abs(r.WER.Worst-want) > 1e-9 {
		t.Errorf("Worst = %.4f, want %.4f", r.WER.Worst, want)
	}

	if got, want := r.TurnsYielded, 1; got != want {
		t.Errorf("TurnsYielded = %d, want %d", got, want)
	}
}

func TestStepReportWERNoTranscripts(t *testing.T) {
	// The reference agent runs no speech-to-text. A target that reports
	// nothing must score nothing, rather than a flawless zero.
	outcomes := []callOutcome{
		{step: "c1", turns: []metrics.TurnMetric{turn("hello there", "", false)}},
	}
	r := buildStepReport(Step{Name: "c1", Concurrency: 1, Calls: 1}, outcomes)

	if r.WER.Turns != 0 {
		t.Errorf("scored turns = %d, want 0", r.WER.Turns)
	}
	if r.WER.Mean != 0 || r.WER.RefWords != 0 {
		t.Errorf("WER = %+v, want zero value", r.WER)
	}
}
