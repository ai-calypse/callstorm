// Package scenario describes who calls, what they say, and what they are
// calling. Phase 1 scripts are linear; branching on what the agent says
// arrives with the scenario engine.
package scenario

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"
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

	// Pacing shapes how the caller speaks. Voice and script make a persona
	// recognisable; pacing is what the agent's turn detection actually reacts
	// to, which is why it lives in the harness rather than in the prompt.
	Pacing Pacing `json:"pacing,omitempty"`

	// SuccessCriteria is what the agent had to actually do, in plain language.
	// Latency says how fast the agent was; these say whether it was any use.
	// Each is judged separately and has to cite the turn it was decided on, so
	// a verdict can be checked rather than taken on trust.
	SuccessCriteria []string `json:"success_criteria,omitempty"`

	// MaxTurns bounds how many turns one call may take. It is required once any
	// branch jumps with goto, because a graph that can loop has no natural end,
	// and a call that never ends is a hung worker rather than a finding.
	MaxTurns int `json:"max_turns,omitempty"`
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

// Pacing is how the caller delivers a line, as distinct from what it says.
type Pacing struct {
	// SentencePause is silence inserted between the sentences of one caller
	// line, for a caller who stops to think mid-thought.
	//
	// It is the standard way to catch an agent whose endpointing is tuned too
	// aggressively: it hears the gap, decides the caller has finished, and
	// starts talking over the rest of the sentence. The harness already
	// records that as the caller yielding, so a persona that pauses turns a
	// tuning mistake into a measurement.
	//
	// The line is still one utterance and one turn. The pause is inside it.
	SentencePause string `json:"sentence_pause,omitempty"`

	sentencePause time.Duration
}

// Pause is the parsed SentencePause, or zero for a caller who does not pause.
func (p Pacing) Pause() time.Duration { return p.sentencePause }

// Turn is one thing the caller says, and a node of the scenario graph.
type Turn struct {
	// ID names this node so a branch can jump to it and a report can score it.
	// Empty defaults to turn-N, its position in the file.
	ID string `json:"id,omitempty"`

	Say string `json:"say"`

	// Expect is what the agent's reply to this line has to contain. It is what
	// turns a call-level pass rate into a per-node one: overall completion can
	// hold at 70% while one node of the conversation has collapsed, and only a
	// score kept per node says which.
	Expect *Expect `json:"expect,omitempty"`

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

	// Branch replaces Say when the agent's previous reply matches. The first
	// matching branch wins, and Say is the fallback when none do.
	//
	// A scripted caller who ignores what the agent just said produces the kind
	// of nonsense that reads as an agent failure but is really a test failure:
	// declining a replacement that was never offered, thanking someone for a
	// refund they refused. Branching keeps the caller responsive without
	// giving up a fixed script, which is what makes runs comparable.
	Branch []Branch `json:"branch,omitempty"`

	bargeIn time.Duration
}

// Branch is an alternative line, chosen by what the agent last said.
type Branch struct {
	// IfAgentSaid matches case-insensitively anywhere in the agent's previous
	// reply. Substring rather than regex on purpose: a scenario is read by
	// people deciding whether a run was fair, and a wrong answer has to be
	// explainable by pointing at the transcript.
	IfAgentSaid string `json:"if_agent_said"`
	Say         string `json:"say"`

	// Goto names the node the call continues at after this line, instead of the
	// next one in the file. It is what makes a scenario a graph rather than a
	// script with alternatives: a caller asked for their order number again can
	// be sent back to the node that gives it.
	Goto string `json:"goto,omitempty"`
}

// Expect is an assertion on the agent's reply, matched the same way branches
// are: case-insensitive substrings, so a failed node can be explained by
// pointing at the transcript.
type Expect struct {
	// SaidAny passes when the reply contains at least one of these.
	SaidAny []string `json:"said_any,omitempty"`

	// NotSaid fails the node when the reply contains any of these. It is how a
	// scenario says "never offer a refund before the caller asks".
	NotSaid []string `json:"not_said,omitempty"`
}

// Check reports whether a reply satisfies the assertion, and if not, why.
func (e *Expect) Check(reply string) (met bool, miss string) {
	said := strings.ToLower(reply)
	for _, p := range e.NotSaid {
		if strings.Contains(said, strings.ToLower(p)) {
			return false, fmt.Sprintf("said %q", p)
		}
	}
	if len(e.SaidAny) == 0 {
		return true, ""
	}
	for _, p := range e.SaidAny {
		if strings.Contains(said, strings.ToLower(p)) {
			return true, ""
		}
	}
	return false, fmt.Sprintf("said none of %q", e.SaidAny)
}

// BargeIn is the parsed BargeInAfter, or zero when this turn waits its proper
// turn to speak.
func (t Turn) BargeIn() time.Duration { return t.bargeIn }

// Lines returns every line the caller could possibly speak, defaults and all
// branches alike.
//
// Every one of them is synthesized before the call starts. Synthesizing a
// branch at the moment it is chosen would put the text-to-speech round trip
// inside the window being attributed to the agent, so the cost of a branch
// never taken is paid once and gladly.
func (s *Scenario) Lines() []string {
	var out []string
	for _, t := range s.Turns {
		out = append(out, t.Say)
		for _, b := range t.Branch {
			out = append(out, b.Say)
		}
	}
	return out
}

// Choose picks the line for a turn given what the agent last said, and the
// node to continue at afterwards when the chosen branch jumps. An empty goto
// means the next node in the file.
func (t Turn) Choose(agentSaid string) (say, matched, next string) {
	said := strings.ToLower(agentSaid)
	for _, b := range t.Branch {
		if strings.Contains(said, strings.ToLower(b.IfAgentSaid)) {
			return b.Say, b.IfAgentSaid, b.Goto
		}
	}
	return t.Say, "", ""
}

// NodeIDs lists the scenario's nodes in file order.
func (s *Scenario) NodeIDs() []string {
	ids := make([]string, len(s.Turns))
	for i, t := range s.Turns {
		ids[i] = t.ID
	}
	return ids
}

// Index is the position of a node, or -1 when no node has that id.
func (s *Scenario) Index(id string) int {
	for i, t := range s.Turns {
		if t.ID == id {
			return i
		}
	}
	return -1
}

// TurnLimit is how many turns one call may take: max_turns when set, and
// otherwise one per node, which is a linear script's own length.
func (s *Scenario) TurnLimit() int {
	if s.MaxTurns > 0 {
		return s.MaxTurns
	}
	return len(s.Turns)
}

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
	ids := map[string]bool{}
	for i := range s.Turns {
		t := &s.Turns[i]
		if t.ID == "" {
			t.ID = fmt.Sprintf("turn-%d", i+1)
		}
		if ids[t.ID] {
			return fmt.Errorf("turn %d: duplicate id %q", i+1, t.ID)
		}
		ids[t.ID] = true
	}
	jumps := false
	for i := range s.Turns {
		t := &s.Turns[i]
		if t.Say == "" {
			return fmt.Errorf("turn %d has empty say", i+1)
		}
		if t.Expect != nil {
			if len(t.Expect.SaidAny) == 0 && len(t.Expect.NotSaid) == 0 {
				return fmt.Errorf("node %q: expect needs said_any or not_said", t.ID)
			}
			for _, p := range append(append([]string{}, t.Expect.SaidAny...), t.Expect.NotSaid...) {
				if strings.TrimSpace(p) == "" {
					// An empty phrase is contained in every reply, so the
					// assertion would pass or fail regardless of the agent.
					return fmt.Errorf("node %q: expect has an empty phrase", t.ID)
				}
			}
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
		for j, b := range t.Branch {
			if b.IfAgentSaid == "" {
				return fmt.Errorf("turn %d branch %d: if_agent_said is required", i+1, j+1)
			}
			if b.Say == "" {
				return fmt.Errorf("turn %d branch %d: say is required", i+1, j+1)
			}
			if b.Goto != "" {
				if !ids[b.Goto] {
					return fmt.Errorf("turn %d branch %d: goto %q is not a node in this scenario", i+1, j+1, b.Goto)
				}
				jumps = true
			}
		}
	}
	if s.MaxTurns < 0 {
		return fmt.Errorf("max_turns must not be negative, got %d", s.MaxTurns)
	}
	if jumps && s.MaxTurns == 0 {
		return fmt.Errorf("a scenario whose branches goto another node can loop, so it needs max_turns")
	}
	if !jumps && s.MaxTurns > 0 && s.MaxTurns < len(s.Turns) {
		return fmt.Errorf("max_turns %d would end the call before its last node (%d nodes and no goto)",
			s.MaxTurns, len(s.Turns))
	}
	if s.Pacing.SentencePause != "" {
		d, err := time.ParseDuration(s.Pacing.SentencePause)
		if err != nil {
			return fmt.Errorf("pacing.sentence_pause %q: %w", s.Pacing.SentencePause, err)
		}
		if d < 0 {
			return fmt.Errorf("pacing.sentence_pause must not be negative, got %s", s.Pacing.SentencePause)
		}
		s.Pacing.sentencePause = d
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
