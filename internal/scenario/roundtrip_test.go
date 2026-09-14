package scenario

import (
	"encoding/json"
	"testing"
	"time"
)

// A distributed run hands each worker its scenario as JSON, decoded without
// Load. Barge-ins and pauses have to survive that trip: when they did not, the
// Deepgram suite's barge-in scenario never interrupted and its hesitant caller
// never paused, with nothing on the report to say so.
func TestBargeInAndPauseSurviveAnAssignment(t *testing.T) {
	decoded := func(path string) (*Scenario, Scenario) {
		loaded, err := Load(path)
		if err != nil {
			t.Fatal(err)
		}
		b, err := json.Marshal(loaded)
		if err != nil {
			t.Fatal(err)
		}
		var got Scenario
		if err := json.Unmarshal(b, &got); err != nil {
			t.Fatal(err)
		}
		return loaded, got
	}

	loaded, got := decoded("../../scenarios/barge-in.json")
	for i, want := range []time.Duration{0, 0, 800 * time.Millisecond, 600 * time.Millisecond, 0} {
		if loaded.Turns[i].BargeIn() != want || got.Turns[i].BargeIn() != want {
			t.Errorf("turn %d barges in after %v loaded and %v decoded, want %v", i+1, loaded.Turns[i].BargeIn(), got.Turns[i].BargeIn(), want)
		}
	}

	hesitant, gotHesitant := decoded("../../scenarios/hesitant.json")
	if hesitant.Pacing.Pause() <= 0 || gotHesitant.Pacing.Pause() != hesitant.Pacing.Pause() {
		t.Errorf("hesitant pause %v loaded and %v decoded, want the same positive pause", hesitant.Pacing.Pause(), gotHesitant.Pacing.Pause())
	}
}
