package loadgen

import (
	"errors"
	"testing"
	"time"

	"github.com/yakshgandhi/callstorm/internal/metrics"
)

func TestTimelineStampsEveryCallInStartOrder(t *testing.T) {
	zero := time.Date(2026, 9, 13, 12, 0, 0, 0, time.UTC)
	turns := func(ms ...int) []metrics.TurnMetric {
		var out []metrics.TurnMetric
		for _, v := range ms {
			out = append(out, metrics.TurnMetric{TTFA: time.Duration(v) * time.Millisecond})
		}
		return out
	}
	outcomes := []callOutcome{
		{step: "c2", clockZero: zero.Add(10 * time.Second), callEnded: zero.Add(40 * time.Second), turns: turns(500, 700, 600)},
		{step: "c2", clockZero: zero, callEnded: zero.Add(30 * time.Second), turns: turns(400)},
		// Never connected: still on the timeline and in the log, marked failed,
		// at the time it was tried.
		{step: "c2", startedAt: zero.Add(5 * time.Second), endedAt: zero.Add(6 * time.Second), err: errors.New("dial refused")},
	}

	start, end, pts := summarizeTimeline(outcomes)
	if !start.Equal(zero) || !end.Equal(zero.Add(40*time.Second)) {
		t.Errorf("step ran %v to %v, want %v to %v", start, end, zero, zero.Add(40*time.Second))
	}
	if len(pts) != 3 || !pts[0].At.Equal(zero) || !pts[1].Failed || pts[2].TTFAMs != 600 {
		t.Errorf("timeline = %+v, want three calls in start order: 400ms, the failed dial, then a 600ms median", pts)
	}

	recs := callRecords(outcomes)
	if len(recs) != 3 {
		t.Fatalf("recorded %d calls, want all 3 including the failed one", len(recs))
	}
	if recs[2].Error == "" || !recs[2].StartedAt.Equal(zero.Add(5*time.Second)) {
		t.Errorf("failed call recorded as %+v, want its error and the time it was tried", recs[2])
	}
}

func TestBackToNormalWaitsUntilItStaysNormal(t *testing.T) {
	start := time.Date(2026, 9, 13, 12, 0, 0, 0, time.UTC)
	s := StepReport{StartedAt: start, Timeline: []CallPoint{
		{At: start, TTFAMs: 900},
		// Dips back to normal, then slows again: not yet recovered.
		{At: start.Add(10 * time.Second), TTFAMs: 450},
		{At: start.Add(20 * time.Second), TTFAMs: 700},
		{At: start.Add(30 * time.Second), TTFAMs: 420},
		{At: start.Add(40 * time.Second), TTFAMs: 410},
	}}

	// Normal is within 1.2x of a 400ms baseline median: 480ms or better.
	back, after := backToNormal(s, 400)
	if !back || after != 30 {
		t.Errorf("back to normal = %v after %vs, want true after 30s: the dip at 10s did not last", back, after)
	}

	s.Timeline = append(s.Timeline, CallPoint{At: start.Add(50 * time.Second), TTFAMs: 800})
	if back, _ := backToNormal(s, 400); back {
		t.Error("a step whose last call is slow was read as recovered")
	}
}
