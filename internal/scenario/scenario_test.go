package scenario

import (
	"strings"
	"testing"
)

func valid() Scenario {
	return Scenario{
		Name:        "graph",
		CallerVoice: "aura-2-thalia-en",
		Turns: []Turn{
			{Say: "hello"},
			{ID: "number", Say: "four four eight one two"},
			{Say: "thanks"},
		},
	}
}

func TestValidateNamesUnnamedNodesByPosition(t *testing.T) {
	s := valid()
	if err := s.validate(); err != nil {
		t.Fatal(err)
	}
	got := strings.Join(s.NodeIDs(), ",")
	if want := "turn-1,number,turn-3"; got != want {
		t.Errorf("node ids = %s, want %s", got, want)
	}
}

func TestValidateRejectsDuplicateNodeIDs(t *testing.T) {
	s := valid()
	s.Turns[2].ID = "number"
	if err := s.validate(); err == nil {
		t.Fatal("accepted two nodes with the same id")
	}
}

func TestValidateRejectsGotoToNowhere(t *testing.T) {
	s := valid()
	s.MaxTurns = 6
	s.Turns[1].Branch = []Branch{{IfAgentSaid: "again", Say: "four four eight one two", Goto: "missing"}}
	err := s.validate()
	if err == nil || !strings.Contains(err.Error(), "missing") {
		t.Fatalf("err = %v, want one naming the unknown node", err)
	}
}

func TestValidateRequiresMaxTurnsOnceAGraphCanLoop(t *testing.T) {
	s := valid()
	s.Turns[1].Branch = []Branch{{IfAgentSaid: "again", Say: "four four eight one two", Goto: "number"}}
	if err := s.validate(); err == nil {
		t.Fatal("accepted a looping graph with no bound on its length")
	}
	s.MaxTurns = 6
	if err := s.validate(); err != nil {
		t.Fatalf("rejected a bounded graph: %v", err)
	}
}

func TestValidateRejectsMaxTurnsThatTruncateALinearScript(t *testing.T) {
	s := valid()
	s.MaxTurns = 2
	if err := s.validate(); err == nil {
		t.Fatal("accepted a turn limit that silently drops the last node")
	}
}

func TestValidateRejectsAnEmptyExpectPhrase(t *testing.T) {
	// An empty substring is contained in every reply, so the node would pass
	// whatever the agent said.
	s := valid()
	s.Turns[1].Expect = &Expect{SaidAny: []string{" "}}
	if err := s.validate(); err == nil {
		t.Fatal("accepted an assertion that cannot fail")
	}
}

func TestChooseReturnsTheBranchJump(t *testing.T) {
	turn := Turn{Say: "default", Branch: []Branch{
		{IfAgentSaid: "Order Number", Say: "I gave it already", Goto: "number"},
	}}
	say, matched, next := turn.Choose("What is your order number?")
	if say != "I gave it already" || matched != "Order Number" || next != "number" {
		t.Errorf("Choose = %q %q %q", say, matched, next)
	}
	if _, _, next := turn.Choose("anything else"); next != "" {
		t.Errorf("the default line jumped to %q", next)
	}
}

func TestExpectCheck(t *testing.T) {
	e := &Expect{SaidAny: []string{"replacement", "reship"}, NotSaid: []string{"refund"}}
	for _, tc := range []struct {
		reply string
		met   bool
	}{
		{"I can send a Replacement today.", true},
		{"Shall I reship it?", true},
		{"I can refund that or send a replacement.", false}, // forbidden phrase wins
		{"Let me look that up.", false},
	} {
		met, miss := e.Check(tc.reply)
		if met != tc.met {
			t.Errorf("Check(%q) = %v, want %v", tc.reply, met, tc.met)
		}
		if !met && miss == "" {
			t.Errorf("Check(%q) failed without saying why", tc.reply)
		}
	}
}
