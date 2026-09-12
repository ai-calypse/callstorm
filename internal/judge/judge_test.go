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
	"The agent asks for the order number before discussing the order",
	"The agent offers a replacement before offering a refund",
}

func TestScoreParsesVerdicts(t *testing.T) {
	m := &stub{reply: `{"outcomes":[
		{"criterion":"The agent asks for the order number before discussing the order","met":true,"evidence":"turn 1: \"Can I get your order number?\""},
		{"criterion":"The agent offers a replacement before offering a refund","met":true,"evidence":"turn 2: \"I can send a replacement.\""}
	]}`}

	j := Judge{Model: m}
	got := j.Score(context.Background(), conversation, criteria)

	if got.Err != "" {
		t.Fatalf("unexpected error: %s", got.Err)
	}
	if len(got.Outcomes) != 2 {
		t.Fatalf("got %d outcomes, want 2", len(got.Outcomes))
	}
	if !got.Met() {
		t.Error("Met() = false, want true")
	}
	if got.Outcomes[0].Evidence == "" {
		t.Error("evidence is empty; a verdict with no quote cannot be checked")
	}

	// The transcript and the criteria both have to reach the model.
	if !strings.Contains(m.gotUs, "four four eight one two") {
		t.Error("prompt is missing the conversation")
	}
	if !strings.Contains(m.gotUs, criteria[1]) {
		t.Error("prompt is missing the criteria")
	}
}

func TestScoreUnmetCriterion(t *testing.T) {
	m := &stub{reply: `{"outcomes":[{"criterion":"c","met":false,"evidence":"turn 3: agent refunded without offering a replacement"}]}`}
	got := Judge{Model: m}.Score(context.Background(), conversation, criteria)

	if got.Err != "" {
		t.Fatalf("unexpected error: %s", got.Err)
	}
	if got.Met() {
		t.Error("Met() = true with an unmet criterion")
	}
}

func TestScoreToleratesCodeFences(t *testing.T) {
	m := &stub{reply: "```json\n{\"outcomes\":[{\"criterion\":\"c\",\"met\":true,\"evidence\":\"turn 1\"}]}\n```"}
	got := Judge{Model: m}.Score(context.Background(), conversation, criteria)

	if got.Err != "" {
		t.Fatalf("fenced JSON should still parse, got error: %s", got.Err)
	}
	if !got.Met() {
		t.Error("Met() = false, want true")
	}
}

// A judge that could not run must not read as an agent that failed.
func TestScoreSeparatesJudgeFailureFromAgentFailure(t *testing.T) {
	for _, tc := range []struct {
		name string
		m    *stub
	}{
		{"model errored", &stub{err: errors.New("claude not found")}},
		{"unparseable reply", &stub{reply: "I think the agent did fine, honestly."}},
		{"no verdicts", &stub{reply: `{"outcomes":[]}`}},
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
