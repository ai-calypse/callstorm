package metrics

import (
	"testing"
	"time"
)

func TestPercentileNearestRank(t *testing.T) {
	ds := []time.Duration{
		100 * time.Millisecond,
		200 * time.Millisecond,
		300 * time.Millisecond,
		400 * time.Millisecond,
	}
	for _, tc := range []struct {
		p    float64
		want time.Duration
	}{
		{50, 200 * time.Millisecond},
		{75, 300 * time.Millisecond},
		{95, 400 * time.Millisecond},
		{99, 400 * time.Millisecond},
		{100, 400 * time.Millisecond},
	} {
		if got := Percentile(ds, tc.p); got != tc.want {
			t.Errorf("p%v = %v, want %v", tc.p, got, tc.want)
		}
	}
}

func TestPercentileDoesNotReorderCallerSlice(t *testing.T) {
	ds := []time.Duration{3, 1, 2}
	Percentile(ds, 50)
	if ds[0] != 3 {
		t.Errorf("input slice was sorted in place: %v", ds)
	}
}

func TestFinalizeKeepsNegatives(t *testing.T) {
	// A negative TTFA means the agent talked over the caller. Clamping it to
	// zero would hide the single most interesting failure this tool finds.
	m := TurnMetric{TTFA: -1500 * time.Millisecond, Endpointing: -2 * time.Second}
	m.Finalize()

	if m.TTFAMs != -1500 {
		t.Errorf("TTFAMs = %v, want -1500", m.TTFAMs)
	}
	if m.EndpointingMs != -2000 {
		t.Errorf("EndpointingMs = %v, want -2000", m.EndpointingMs)
	}
}

func TestLogOrdersEventsByTimestamp(t *testing.T) {
	l := NewLog()
	l.MarkAt(500*time.Millisecond, AgentFirstAudio, 1, "")
	l.MarkAt(100*time.Millisecond, CallerSpeechEnd, 1, "")

	ev := l.Events()
	if len(ev) != 3 { // call_start is stamped by NewLog
		t.Fatalf("got %d events, want 3", len(ev))
	}
	for i := 1; i < len(ev); i++ {
		if ev[i].TMs < ev[i-1].TMs {
			t.Fatalf("events out of order at %d: %v", i, ev)
		}
	}
	if ev[1].Kind != CallerSpeechEnd {
		t.Errorf("second event is %s, want %s", ev[1].Kind, CallerSpeechEnd)
	}
}
