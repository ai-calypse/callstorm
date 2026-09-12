// Package scenario describes who calls, what they say, and what they are
// calling. Phase 1 scripts are linear; branching on what the agent says
// arrives with the scenario engine.
package scenario

import (
	"encoding/json"
	"fmt"
	"os"
	"time"
)

// Scenario is one synthetic caller placing one call.
type Scenario struct {
	Name    string `json:"name"`
	Persona string `json:"persona"`

	// CallerVoice is the Aura model the synthetic caller speaks with.
	CallerVoice string `json:"caller_voice"`

	Target Target `json:"target"`
	Turns  []Turn `json:"turns"`

	// SuccessCriteria is what the agent had to actually do, in plain language.
	// Latency says how fast the agent was; these say whether it was any use.
	// Each is judged separately and has to cite the turn it was decided on, so
	// a verdict can be checked rather than taken on trust.
	SuccessCriteria []string `json:"success_criteria,omitempty"`
}

// Target configures the agent under test. Phase 1 stands up a Deepgram Voice
// Agent from this config so there is always something to measure; pointing at
// someone else's agent is a later phase.
type Target struct {
	Prompt      string `json:"prompt"`
	Greeting    string `json:"greeting"`
	Voice       string `json:"voice"`
	ThinkModel  string `json:"think_model"`
	ListenModel string `json:"listen_model"`
}

// Turn is one thing the caller says.
type Turn struct {
	Say string `json:"say"`

	// BargeInAfter makes this line interrupt the agent, starting the given
	// duration after the agent began replying to the previous turn instead of
	// waiting for that reply to finish. "800ms" cuts in most of the way
	// through an opening sentence.
	//
	// It is how the caller behaves when the agent is being long-winded, and it
	// is the only way to measure how fast an agent yields the floor -- an
	// agent that keeps talking over a customer is broken in a way no latency
	// number reports.
	BargeInAfter string `json:"barge_in_after,omitempty"`

	bargeIn time.Duration
}

// BargeIn is the parsed BargeInAfter, or zero when this turn waits its proper
// turn to speak.
func (t Turn) BargeIn() time.Duration { return t.bargeIn }

// Load reads and validates a scenario file.
func Load(path string) (*Scenario, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var s Scenario
	dec := json.NewDecoder(newTrimReader(b))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&s); err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	if err := s.validate(); err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	return &s, nil
}

func (s *Scenario) validate() error {
	if s.Name == "" {
		return fmt.Errorf("name is required")
	}
	if s.CallerVoice == "" {
		return fmt.Errorf("caller_voice is required")
	}
	if len(s.Turns) == 0 {
		return fmt.Errorf("scenario has no turns")
	}
	for i := range s.Turns {
		t := &s.Turns[i]
		if t.Say == "" {
			return fmt.Errorf("turn %d has empty say", i+1)
		}
		if t.BargeInAfter != "" {
			d, err := time.ParseDuration(t.BargeInAfter)
			if err != nil {
				return fmt.Errorf("turn %d: barge_in_after %q: %w", i+1, t.BargeInAfter, err)
			}
			if d <= 0 {
				return fmt.Errorf("turn %d: barge_in_after must be positive, got %s", i+1, t.BargeInAfter)
			}
			if i == 0 {
				return fmt.Errorf("turn 1 cannot barge in: there is no reply in progress to interrupt")
			}
			t.bargeIn = d
		}
	}
	if s.Target.Voice == "" {
		s.Target.Voice = "aura-2-apollo-en"
	}
	if s.Target.ThinkModel == "" {
		s.Target.ThinkModel = "gpt-4o-mini"
	}
	if s.Target.ListenModel == "" {
		s.Target.ListenModel = "nova-3"
	}
	return nil
}
