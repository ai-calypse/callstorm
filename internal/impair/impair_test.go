package impair

import (
	"context"
	"errors"
	"strings"
	"testing"
)

func TestCohortsPutsTheControlFirst(t *testing.T) {
	cs, err := Cohorts([]Profile{{Name: "severe", DelayMs: 200}, {Name: Clean}})
	if err != nil {
		t.Fatal(err)
	}
	if len(cs) != 2 || cs[0].Name != Clean || cs[1].Name != "severe" {
		t.Errorf("cohorts = %+v, want clean then severe", cs)
	}

	cs, err = Cohorts([]Profile{{Name: "light", DelayMs: 100}})
	if err != nil {
		t.Fatal(err)
	}
	if cs[0].Name != Clean {
		t.Errorf("a list without a control was not given one: %+v", cs)
	}
}

func TestCohortsRejectsAnImpairedControl(t *testing.T) {
	if _, err := Cohorts([]Profile{{Name: Clean, LossPct: 1}}); err == nil {
		t.Fatal("accepted a clean profile that drops packets")
	}
}

func TestCohortsRejectsDuplicates(t *testing.T) {
	if _, err := Cohorts([]Profile{{Name: "x", DelayMs: 1}, {Name: "x", DelayMs: 2}}); err == nil {
		t.Fatal("accepted two profiles with one name")
	}
}

func TestArgs(t *testing.T) {
	for _, tc := range []struct {
		p    Profile
		want string
	}{
		{Profile{Name: Clean}, "qdisc del dev eth0 root"},
		{Profile{Name: "light", LossPct: 1, JitterMs: 20, DelayMs: 100},
			"qdisc replace dev eth0 root netem delay 100ms 20ms loss 1%"},
		{Profile{Name: "delay", DelayMs: 100}, "qdisc replace dev eth0 root netem delay 100ms"},
		{Profile{Name: "lossy", LossPct: 0.5}, "qdisc replace dev eth0 root netem loss 0.5%"},
	} {
		if got := strings.Join(Args("eth0", tc.p), " "); got != tc.want {
			t.Errorf("%s: %q, want %q", tc.p.Name, got, tc.want)
		}
	}
}

// fakeTC answers show with whatever was last applied.
type fakeTC struct{ state string }

func (f *fakeTC) run(_ context.Context, _ string, args ...string) (string, error) {
	switch args[1] {
	case "show":
		return f.state, nil
	case "del":
		if f.state == "" {
			return "Error: Cannot delete qdisc with handle of zero.", errors.New("exit status 2")
		}
		f.state = ""
	case "replace":
		f.state = "qdisc netem 8001: root refcnt 2 limit 1000 " + strings.Join(args[6:], " ")
	}
	return "", nil
}

func TestApplyReadsTheQdiscBack(t *testing.T) {
	tc := &fakeTC{}
	n := Netem{Device: "eth0", Exec: tc.run}

	// Clearing an interface that was never impaired fails in tc and is still
	// exactly the state clean asks for.
	cmd, _, err := n.Apply(context.Background(), Profile{Name: Clean})
	if err != nil {
		t.Fatalf("clean on a fresh interface: %v", err)
	}
	if cmd != "tc qdisc del dev eth0 root" {
		t.Errorf("command = %q", cmd)
	}

	_, qdisc, err := n.Apply(context.Background(), Profile{Name: "light", DelayMs: 100})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(qdisc, "delay 100ms") {
		t.Errorf("qdisc evidence = %q, want the kernel's account of the delay", qdisc)
	}
}

func TestApplyRefusesAnImpairmentThatDidNotTakeEffect(t *testing.T) {
	n := Netem{Device: "eth0", Exec: func(_ context.Context, _ string, args ...string) (string, error) {
		if args[1] == "show" {
			return "qdisc noqueue 0: root refcnt 2", nil
		}
		return "", nil
	}}
	if _, _, err := n.Apply(context.Background(), Profile{Name: "light", DelayMs: 100}); err == nil {
		t.Fatal("reported an impairment the interface does not show")
	}
}
