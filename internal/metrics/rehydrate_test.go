package metrics

import (
	"encoding/json"
	"testing"
	"time"
)

// A turn that crosses a wire must measure the same as one that did not. The
// durations are not serialized, so without rehydration a distributed run builds
// its report from zeroes and reports a sweep in which nothing was measured.
func TestRoundTripThroughJSON(t *testing.T) {
	orig := TurnMetric{
		Turn:         2,
		CallerText:   "my order never arrived",
		TTFA:         502 * time.Millisecond,
		Endpointing:  200*time.Millisecond + 500*time.Microsecond,
		ThinkSpeak:   301 * time.Millisecond,
		AgentSpeech:  2600 * time.Millisecond,
		TurnLatency:  3102 * time.Millisecond,
		PacingDrift:  -17 * time.Millisecond,
		BargeInYield: 487 * time.Millisecond,
	}
	orig.Finalize()

	b, err := json.Marshal(orig)
	if err != nil {
		t.Fatal(err)
	}

	var got TurnMetric
	if err := json.Unmarshal(b, &got); err != nil {
		t.Fatal(err)
	}

	// Without rehydration the durations are gone, which is the bug this guards.
	if got.TTFA != 0 {
		t.Fatalf("durations unexpectedly survived JSON; this test no longer guards anything")
	}

	got.Rehydrate()

	for _, c := range []struct {
		name      string
		want, got time.Duration
	}{
		{"TTFA", orig.TTFA, got.TTFA},
		{"Endpointing", orig.Endpointing, got.Endpointing},
		{"ThinkSpeak", orig.ThinkSpeak, got.ThinkSpeak},
		{"AgentSpeech", orig.AgentSpeech, got.AgentSpeech},
		{"TurnLatency", orig.TurnLatency, got.TurnLatency},
		{"PacingDrift", orig.PacingDrift, got.PacingDrift},
		{"BargeInYield", orig.BargeInYield, got.BargeInYield},
	} {
		// Finalize rounds to two decimal places of a millisecond, so the trip
		// is lossy below a microsecond and exact above it.
		if diff := c.want - c.got; diff > time.Microsecond || diff < -time.Microsecond {
			t.Errorf("%s = %s, want %s", c.name, c.got, c.want)
		}
	}

	// A negative drift is a real measurement and must not be clamped in transit.
	if got.PacingDrift >= 0 {
		t.Errorf("PacingDrift = %s, want it to stay negative", got.PacingDrift)
	}
}
