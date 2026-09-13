package loadgen

import (
	"math"
	"testing"
	"time"

	"github.com/yakshgandhi/callstorm/internal/metrics"
)

func spoken(caller, agent string, callerSec, agentSec float64, ttfa time.Duration, yielded bool) metrics.TurnMetric {
	return metrics.TurnMetric{
		CallerText:    caller,
		AgentText:     agent,
		CallerSpeech:  time.Duration(callerSec * float64(time.Second)),
		AgentSpeech:   time.Duration(agentSec * float64(time.Second)),
		TTFA:          ttfa,
		TurnLatency:   ttfa + time.Duration(agentSec*float64(time.Second)),
		CallerYielded: yielded,
	}
}

func TestConversationTalkRatioAndPace(t *testing.T) {
	// Six agent words over 3s is 120wpm; four caller words over 2s is 120wpm.
	// The agent speaks 3 of every 5 seconds, so it holds 60% of the floor.
	out := []callOutcome{{turns: []metrics.TurnMetric{
		spoken("one two three four", "a b c d e f", 2, 3, 500*time.Millisecond, false),
	}}}
	c := summarizeConversation(out)

	if c.TalkRatio != 0.6 {
		t.Errorf("TalkRatio = %.3f, want 0.600", c.TalkRatio)
	}
	if c.AgentWPM != 120 {
		t.Errorf("AgentWPM = %.1f, want 120", c.AgentWPM)
	}
	if c.CallerWPM != 120 {
		t.Errorf("CallerWPM = %.1f, want 120", c.CallerWPM)
	}
}

// The interruption score is a published formula, so it is pinned rather than
// left to drift: 5 x (1 - interruptions/turns).
func TestInterruptionScoreMatchesPublishedFormula(t *testing.T) {
	for _, tc := range []struct {
		name      string
		yielded   int
		turns     int
		wantScore float64
		wantRate  float64
	}{
		{"never interrupts", 0, 10, 5.0, 0.0},
		{"one turn in ten", 1, 10, 4.5, 0.1},
		{"one turn in five", 4, 20, 4.0, 0.2},
		{"interrupts everything", 5, 5, 0.0, 1.0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var turns []metrics.TurnMetric
			for i := 0; i < tc.turns; i++ {
				turns = append(turns, spoken("a b", "c d", 1, 1, 300*time.Millisecond, i < tc.yielded))
			}
			c := summarizeConversation([]callOutcome{{turns: turns}})

			if math.Abs(c.InterruptionScore-tc.wantScore) > 1e-9 {
				t.Errorf("InterruptionScore = %.2f, want %.2f", c.InterruptionScore, tc.wantScore)
			}
			if math.Abs(c.InterruptionRate-tc.wantRate) > 1e-9 {
				t.Errorf("InterruptionRate = %.3f, want %.3f", c.InterruptionRate, tc.wantRate)
			}
		})
	}
}

func TestDeadAirCountsOnlyFinishedTurns(t *testing.T) {
	out := []callOutcome{{turns: []metrics.TurnMetric{
		spoken("a", "b", 1, 1, 900*time.Millisecond, false),  // quick: not dead air
		spoken("a", "b", 1, 1, 2500*time.Millisecond, false), // 2.5s of silence
		spoken("a", "b", 1, 1, 3000*time.Millisecond, false), // 3.0s of silence
		// Talked over, so the caller never got a silence to sit in. Counting it
		// would charge the agent twice for one behaviour.
		spoken("a", "b", 1, 1, 5000*time.Millisecond, true),
	}}}
	c := summarizeConversation(out)

	if c.DeadAirTurns != 2 {
		t.Errorf("DeadAirTurns = %d, want 2", c.DeadAirTurns)
	}
	if math.Abs(c.DeadAirSeconds-5.5) > 0.05 {
		t.Errorf("DeadAirSeconds = %.1f, want 5.5", c.DeadAirSeconds)
	}
}

func TestCostUnpricedWithoutARate(t *testing.T) {
	out := []callOutcome{{turns: []metrics.TurnMetric{
		spoken("a", "b", 10, 20, time.Second, false),
	}}}

	// Minutes are always counted; dollars only appear when a rate is supplied,
	// so a reader can tell "free" from "nobody priced it".
	unpriced := summarizeCost(out, 0)
	if unpriced.AgentMinutes <= 0 {
		t.Errorf("AgentMinutes = %.3f, want the usage counted anyway", unpriced.AgentMinutes)
	}
	if unpriced.USD != 0 {
		t.Errorf("USD = %.4f, want 0 with no rate", unpriced.USD)
	}

	// 10s of caller speech plus 21s of turn latency is 31s, or 0.5167 minutes.
	priced := summarizeCost(out, 0.06)
	if math.Abs(priced.AgentMinutes-0.517) > 0.002 {
		t.Errorf("AgentMinutes = %.3f, want ~0.517", priced.AgentMinutes)
	}
	if want := priced.AgentMinutes * 0.06; math.Abs(priced.USD-want) > 1e-4 {
		t.Errorf("USD = %.4f, want %.4f", priced.USD, want)
	}
	if priced.USDPerCall != priced.USD {
		t.Errorf("USDPerCall = %.4f, want %.4f for a single call", priced.USDPerCall, priced.USD)
	}
}

func TestFailedCallsAreNotPriced(t *testing.T) {
	out := []callOutcome{
		{err: errTest},
		{turns: []metrics.TurnMetric{spoken("a", "b", 6, 6, time.Second, false)}},
	}
	c := summarizeCost(out, 1)
	// Only the connected call is billable: 6s speech + 7s latency = 13s.
	if math.Abs(c.AgentMinutes-13.0/60) > 0.002 {
		t.Errorf("AgentMinutes = %.3f, want %.3f", c.AgentMinutes, 13.0/60)
	}
	if c.USDPerCall != c.USD {
		t.Errorf("per-call cost divided by the wrong number of calls")
	}
}

var errTest = &testErr{}

type testErr struct{}

func (e *testErr) Error() string { return "dial failed" }
