package judge

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
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

	// Reasoning is the judge's step from the quote to the verdict. It is
	// written before the verdict, so the verdict follows from it rather than
	// being justified after the fact.
	Reasoning string `json:"reasoning,omitempty"`
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

// systemPrompt follows Coval's guidance on judge prompts
// (docs.coval.ai/concepts/metrics/writing-judge-prompts): the parties are
// named one way throughout, each criterion goes through ordered gates with a
// conservative default, the reasoning comes before the verdict, a few examples
// anchor the edge cases, and the whole prompt stays under 2,000 characters.
const systemPrompt = `You grade transcripts of phone calls between the user, a customer, and the assistant, an automated voice agent.

You get the conversation and numbered criteria. Decide each criterion on its own, from the transcript alone.

For each criterion, in order:
1. Can words decide it? A transcript has no timing or audio, so speed, pauses and whether the assistant stopped when interrupted cannot be judged. If the criterion needs those, met is false and evidence is "not decidable from a transcript".
2. Find the turns that settle it and quote them, naming each turn and who spoke.
3. Apply its logic literally: AND needs every part, OR needs any part, "before" and "after" compare turn order.
4. met is true only if the quotes show every required part. Missing, unclear or partial evidence is not met.

Only the assistant's own words show what the assistant did: the user asking for a refund is not the assistant offering one. Ignore wording, tone and politeness unless a criterion asks about them.

Examples:
- "The assistant offers a replacement, AND does so before the assistant mentions a refund". Turn 1 user: "Can I get a refund?" Turn 1 assistant: "I can send a replacement." Met: the user's mention does not count.
- "The assistant states how long the refund takes". The assistant only says "Refund issued." Not met: saying nothing is not met.
- "The assistant waits for the user to finish speaking". Not met: not decidable from a transcript.

Reply with JSON only, one outcome per criterion, fields in this order:
{"outcomes":[{"id":"C1","evidence":"turn 2 assistant: \"...\"","reasoning":"...","met":true}]}`

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
		Outcomes []struct {
			ID        string `json:"id"`
			Evidence  string `json:"evidence"`
			Reasoning string `json:"reasoning"`
			Met       bool   `json:"met"`
		} `json:"outcomes"`
	}
	if err := json.Unmarshal([]byte(stripFences(out)), &parsed); err != nil {
		return Judgement{Err: fmt.Sprintf("judge returned unparseable output: %v", err)}
	}
	if len(parsed.Outcomes) == 0 {
		return Judgement{Err: "judge returned no verdicts"}
	}

	// The judge answers by id rather than copying each criterion back, so a
	// paraphrase cannot turn one criterion into two rows. Every criterion must
	// be answered exactly once: Met is every outcome met, so a skipped
	// criterion would pass a call on the criteria the judge happened to answer.
	outcomes := make([]Outcome, len(criteria))
	answered := make([]bool, len(criteria))
	for _, o := range parsed.Outcomes {
		i, err := strconv.Atoi(strings.TrimPrefix(strings.TrimSpace(o.ID), "C"))
		if err != nil || i < 1 || i > len(criteria) {
			return Judgement{Err: fmt.Sprintf("judge answered criterion %q, which was never asked", o.ID)}
		}
		if answered[i-1] {
			return Judgement{Err: fmt.Sprintf("judge answered %s twice", o.ID)}
		}
		answered[i-1] = true
		outcomes[i-1] = Outcome{Criterion: criteria[i-1], Met: o.Met, Evidence: o.Evidence, Reasoning: o.Reasoning}
	}
	if n := len(parsed.Outcomes); n != len(criteria) {
		return Judgement{Err: fmt.Sprintf("judge answered %d of %d criteria", n, len(criteria))}
	}
	return Judgement{Outcomes: outcomes}
}

// buildPrompt lays out the conversation in the roles the system prompt names,
// and numbers the criteria so the judge can answer each by id.
func buildPrompt(exchanges []Exchange, criteria []string) string {
	var b strings.Builder
	b.WriteString("CONVERSATION\n\n")
	for _, e := range exchanges {
		fmt.Fprintf(&b, "turn %d\n  user: %s\n  assistant: %s\n\n", e.Turn, e.Caller, e.Agent)
	}
	b.WriteString("CRITERIA\n\n")
	for i, c := range criteria {
		fmt.Fprintf(&b, "C%d: %s\n", i+1, c)
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
