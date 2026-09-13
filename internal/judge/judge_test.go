package judge

import (
	"context"
	"errors"
	"strings"
	"testing"
)

type stub struct {
	reply string
	err   error
	gotSy string
	gotUs string
}

func (s *stub) Complete(_ context.Context, system, user string) (string, error) {
	s.gotSy, s.gotUs = system, user
	return s.reply, s.err
}

var conversation = []Exchange{
	{Turn: 1, Caller: "my order never arrived", Agent: "Can I get your order number?"},
	{Turn: 2, Caller: "four four eight one two", Agent: "I can send a replacement."},
	{Turn: 3, Caller: "I'd rather have my money back", Agent: "Refunded."},
}

var criteria = []string{
	"The assistant asks the user for an order number before the assistant discusses the order",
	"The assistant offers a replacement, AND does so before the assistant mentions a refund",
}

func TestScoreParsesVerdicts(t *testing.T) {
	m := &stub{reply: `{"outcomes":[
		{"id":"C1","evidence":"turn 1 assistant: \"Can I get your order number?\"","reasoning":"asked in turn 1, before any order detail","met":true},
		{"id":"C2","evidence":"turn 2 assistant: \"I can send a replacement.\"","reasoning":"replacement in turn 2, refund first in turn 3","met":true}
	]}`}

	got := Judge{Model: m}.Score(context.Background(), conversation, criteria)

	if got.Err != "" {
		t.Fatalf("unexpected error: %s", got.Err)
	}
	if len(got.Outcomes) != 2 {
		t.Fatalf("got %d outcomes, want 2", len(got.Outcomes))
	}
	if !got.Met() {
		t.Error("Met() = false, want true")
	}
	// The model answers by id; the verdict carries the criterion's own words,
	// so a report never shows a judge's paraphrase of what it was asked.
	if got.Outcomes[0].Criterion != criteria[0] || got.Outcomes[1].Criterion != criteria[1] {
		t.Errorf("criteria came back as %q and %q", got.Outcomes[0].Criterion, got.Outcomes[1].Criterion)
	}
	if got.Outcomes[0].Evidence == "" || got.Outcomes[0].Reasoning == "" {
		t.Error("a verdict with no quote or reasoning cannot be checked")
	}

	// The transcript and the criteria both reach the model, in the roles the
	// prompt names: the user and the assistant.
	for _, want := range []string{"user: four four eight one two", "assistant: Can I get your order number?", "C2: " + criteria[1]} {
		if !strings.Contains(m.gotUs, want) {
			t.Errorf("prompt is missing %q", want)
		}
	}
}

func TestScoreUnmetCriterion(t *testing.T) {
	m := &stub{reply: `{"outcomes":[
		{"id":"C1","evidence":"turn 1","reasoning":"asked","met":true},
		{"id":"C2","evidence":"turn 3 assistant: \"Refunded.\"","reasoning":"refunded without offering a replacement first","met":false}
	]}`}
	got := Judge{Model: m}.Score(context.Background(), conversation, criteria)

	if got.Err != "" {
		t.Fatalf("unexpected error: %s", got.Err)
	}
	if got.Met() {
		t.Error("Met() = true with an unmet criterion")
	}
}

func TestScoreToleratesCodeFences(t *testing.T) {
	m := &stub{reply: "```json\n{\"outcomes\":[{\"id\":\"C1\",\"evidence\":\"turn 1\",\"reasoning\":\"r\",\"met\":true},{\"id\":\"C2\",\"evidence\":\"turn 2\",\"reasoning\":\"r\",\"met\":true}]}\n```"}
	got := Judge{Model: m}.Score(context.Background(), conversation, criteria)

	if got.Err != "" {
		t.Fatalf("fenced JSON should still parse, got error: %s", got.Err)
	}
	if !got.Met() {
		t.Error("Met() = false, want true")
	}
}

// A judge that could not run, or did not answer what it was asked, must not
// read as an agent that failed -- or as one that passed.
func TestScoreSeparatesJudgeFailureFromAgentFailure(t *testing.T) {
	for _, tc := range []struct {
		name string
		m    *stub
	}{
		{"model errored", &stub{err: errors.New("claude not found")}},
		{"unparseable reply", &stub{reply: "I think the agent did fine, honestly."}},
		{"no verdicts", &stub{reply: `{"outcomes":[]}`}},
		// Met() is every outcome met, so a skipped criterion would otherwise
		// pass a call on the criteria the judge happened to answer.
		{"a criterion left unanswered", &stub{reply: `{"outcomes":[{"id":"C1","evidence":"turn 1","reasoning":"r","met":true}]}`}},
		{"an id that was never asked", &stub{reply: `{"outcomes":[{"id":"C1","evidence":"e","reasoning":"r","met":true},{"id":"C9","evidence":"e","reasoning":"r","met":true}]}`}},
		{"one criterion answered twice", &stub{reply: `{"outcomes":[{"id":"C1","evidence":"e","reasoning":"r","met":true},{"id":"C1","evidence":"e","reasoning":"r","met":true}]}`}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := Judge{Model: tc.m}.Score(context.Background(), conversation, criteria)
			if got.Err == "" {
				t.Error("want an error recorded")
			}
			if got.Met() {
				t.Error("Met() = true for a judgement that never happened")
			}
			if len(got.Outcomes) != 0 {
				t.Errorf("got %d outcomes, want none", len(got.Outcomes))
			}
		})
	}
}

func TestScoreNoCriteriaIsNotAJudgement(t *testing.T) {
	m := &stub{reply: "should never be called"}
	got := Judge{Model: m}.Score(context.Background(), conversation, nil)

	if got.Err != "" || len(got.Outcomes) != 0 {
		t.Errorf("got %+v, want an empty judgement", got)
	}
	if m.gotUs != "" {
		t.Error("model was called with nothing to judge")
	}
	if got.Met() {
		t.Error("Met() = true with no criteria to meet")
	}
}

// Judge prompts measurably degrade past about 2,000 characters, and roles
// named two ways are read as two parties.
func TestSystemPromptIsShortAndNamesTheRolesOneWay(t *testing.T) {
	if n := len(systemPrompt); n >= 2000 {
		t.Errorf("system prompt is %d characters, keep it under 2000", n)
	}
	for _, role := range []string{"the assistant", "the user"} {
		if !strings.Contains(systemPrompt, role) {
			t.Errorf("system prompt never names %q", role)
		}
	}
	for _, other := range []string{"caller", "the agent "} {
		if strings.Contains(systemPrompt, other) {
			t.Errorf("system prompt also calls a party %q", other)
		}
	}
}
