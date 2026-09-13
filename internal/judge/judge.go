package judge

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

// Exchange is one turn of a conversation as the judge sees it: what the caller
// said, and what the agent said back.
type Exchange struct {
	Turn   int
	Caller string
	Agent  string
}

// Outcome is the verdict on a single success criterion.
type Outcome struct {
	Criterion string `json:"criterion"`
	Met       bool   `json:"met"`

	// Evidence must quote the conversation. A judge that can assert without
	// citing is an oracle, and an oracle cannot be checked -- which would make
	// this the one number in the run nobody can audit. With a quote attached,
	// a disagreement is settled by reading the transcript.
	Evidence string `json:"evidence"`
}

// Judgement is one conversation scored against every criterion.
type Judgement struct {
	Step      string `json:"step,omitempty"`
	RequestID string `json:"request_id,omitempty"`

	// JudgedAt is when the verdict came back.
	JudgedAt time.Time `json:"judged_at"`
	Outcomes []Outcome `json:"outcomes"`

	// Err records a judge that failed to run or answer. It is kept separate
	// from an unmet criterion on purpose: "the agent got it wrong" and "we
	// could not tell" are different results, and collapsing them would let a
	// broken judge read as a failing agent.
	Err string `json:"error,omitempty"`
}

// Met reports whether every criterion was satisfied. A judgement that failed
// to run is not a pass.
func (j Judgement) Met() bool {
	if j.Err != "" {
		return false
	}
	for _, o := range j.Outcomes {
		if !o.Met {
			return false
		}
	}
	return len(j.Outcomes) > 0
}

// Model is anything that can answer a prompt with text. The judge does not
// care whether that is Claude Code on a subscription, an API client, or a
// stub in a test.
type Model interface {
	Complete(ctx context.Context, system, user string) (string, error)
}

// Judge scores conversations against a scenario's success criteria.
type Judge struct {
	Model Model
}

const systemPrompt = `You grade transcripts of phone calls handled by an automated voice agent.

You are given a conversation and a list of criteria the agent was required to meet. Decide each criterion independently, on the evidence of the transcript alone.

Rules:
- Judge only what the transcript shows. Do not assume an agent did something off-transcript.
- You are reading words, not listening to a call. A transcript carries no timing and no audio behaviour, so nothing about how fast the agent replied, whether it paused, or whether it stopped when interrupted can be decided here. If a criterion asks for one of those, say so in the evidence and mark it not met: those are measured directly elsewhere, and guessing at them from text produces a confident wrong answer.
- Quote the conversation as evidence for every verdict, naming the turn.
- A criterion is met only if the transcript positively shows it. Absence of evidence is not met.
- Ignore speed, wording and politeness unless a criterion asks about them.

Reply with JSON only, no prose and no code fences:
{"outcomes":[{"criterion":"<the criterion, copied exactly>","met":true,"evidence":"turn 2: \"...\""}]}`

// Score judges one conversation against the criteria.
func (j Judge) Score(ctx context.Context, exchanges []Exchange, criteria []string) Judgement {
	if len(criteria) == 0 {
		return Judgement{}
	}
	if j.Model == nil {
		return Judgement{Err: "no model configured"}
	}

	out, err := j.Model.Complete(ctx, systemPrompt, buildPrompt(exchanges, criteria))
	if err != nil {
		return Judgement{Err: err.Error()}
	}

	var parsed struct {
		Outcomes []Outcome `json:"outcomes"`
	}
	if err := json.Unmarshal([]byte(stripFences(out)), &parsed); err != nil {
		return Judgement{Err: fmt.Sprintf("judge returned unparseable output: %v", err)}
	}
	if len(parsed.Outcomes) == 0 {
		return Judgement{Err: "judge returned no verdicts"}
	}
	return Judgement{Outcomes: parsed.Outcomes}
}

func buildPrompt(exchanges []Exchange, criteria []string) string {
	var b strings.Builder
	b.WriteString("CONVERSATION\n\n")
	for _, e := range exchanges {
		fmt.Fprintf(&b, "turn %d\n  caller: %s\n  agent:  %s\n\n", e.Turn, e.Caller, e.Agent)
	}
	b.WriteString("CRITERIA\n\n")
	for _, c := range criteria {
		fmt.Fprintf(&b, "- %s\n", c)
	}
	return b.String()
}

// stripFences tolerates a model that wraps its JSON in a code fence despite
// being asked not to. Cheaper than failing the run over punctuation.
func stripFences(s string) string {
	s = strings.TrimSpace(s)
	if !strings.HasPrefix(s, "```") {
		return s
	}
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		s = s[i+1:]
	}
	if i := strings.LastIndex(s, "```"); i >= 0 {
		s = s[:i]
	}
	return strings.TrimSpace(s)
}
