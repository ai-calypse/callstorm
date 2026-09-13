// Package impair degrades the network calls leave through, the way a bad
// caller-side connection would, using Linux netem.
//
// It shapes egress only: packets this machine sends are delayed, jittered and
// dropped, and packets arriving are left alone. Over a WebSocket that is TCP,
// so loss does not damage audio -- the kernel retransmits -- and every profile
// shows up as latency. That is the honest reading of an impairment run against
// a WebSocket target, and the report says so rather than implying otherwise.
//
// Every profile is applied by running tc, and the exact command is returned so
// the run records what was done to the network in a form anyone can paste.
package impair

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"
)

// Clean is the unimpaired control every matrix runs first.
const Clean = "clean"

// Profile is one network condition.
type Profile struct {
	Name string `json:"name"`

	// LossPct drops this share of outgoing packets, 0 to 100.
	LossPct float64 `json:"loss_pct,omitempty"`

	// DelayMs holds every outgoing packet this long. Egress only, so it adds
	// the same to the round trip.
	DelayMs float64 `json:"delay_ms,omitempty"`

	// JitterMs varies that delay by up to this much either way. netem draws
	// each packet's delay independently, so jitter reorders packets, and TCP
	// pays for reordering with retransmits -- part of what jitter costs here.
	JitterMs float64 `json:"jitter_ms,omitempty"`
}

// Defaults is the brief's matrix: clean, light, moderate and severe.
func Defaults() []Profile {
	return []Profile{
		{Name: Clean},
		{Name: "light", LossPct: 1, JitterMs: 20, DelayMs: 100},
		{Name: "moderate", LossPct: 3, JitterMs: 50, DelayMs: 100},
		{Name: "severe", LossPct: 5, JitterMs: 100, DelayMs: 200},
	}
}

// Load reads a list of profiles and returns them as cohorts, clean first.
func Load(path string) ([]Profile, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var file struct {
		Profiles []Profile `json:"profiles"`
	}
	if err := json.Unmarshal(b, &file); err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	cs, err := Cohorts(file.Profiles)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	return cs, nil
}

// Cohorts validates profiles and puts the clean control first.
//
// Clean is not optional. An impaired result is only readable against a control
// run in the same session, on the same target, minutes earlier; a historical
// baseline would fold whatever changed since into the impairment's cost. So a
// list without a clean profile gets one, and a list with one gets it moved to
// the front.
func Cohorts(ps []Profile) ([]Profile, error) {
	out := []Profile{{Name: Clean}}
	seen := map[string]bool{Clean: true}
	for i, p := range ps {
		if p.Name == "" {
			return nil, fmt.Errorf("profile %d has no name", i+1)
		}
		if p.LossPct < 0 || p.LossPct > 100 || p.DelayMs < 0 || p.JitterMs < 0 {
			return nil, fmt.Errorf("profile %q: loss must be 0-100 and delay and jitter non-negative", p.Name)
		}
		if p.Name == Clean {
			if p.LossPct != 0 || p.DelayMs != 0 || p.JitterMs != 0 {
				// A "clean" control that is quietly impaired would make every
				// other cohort look better than it is.
				return nil, fmt.Errorf("profile %q is the control and must set no loss, delay or jitter", Clean)
			}
			continue
		}
		if seen[p.Name] {
			return nil, fmt.Errorf("duplicate profile %q", p.Name)
		}
		seen[p.Name] = true
		out = append(out, p)
	}
	return out, nil
}

// Args is the tc invocation that puts dev into profile p.
func Args(dev string, p Profile) []string {
	if p.Name == Clean {
		return []string{"qdisc", "del", "dev", dev, "root"}
	}
	args := []string{"qdisc", "replace", "dev", dev, "root", "netem"}
	if p.DelayMs > 0 || p.JitterMs > 0 {
		args = append(args, "delay", num(p.DelayMs)+"ms")
		if p.JitterMs > 0 {
			args = append(args, num(p.JitterMs)+"ms")
		}
	}
	if p.LossPct > 0 {
		args = append(args, "loss", num(p.LossPct)+"%")
	}
	return args
}

func num(v float64) string { return strconv.FormatFloat(v, 'f', -1, 64) }

// Netem applies profiles to one interface.
type Netem struct {
	Device string

	// Exec runs a command and returns its combined output. Nil runs it for
	// real; tests substitute a fake.
	Exec func(ctx context.Context, name string, args ...string) (string, error)
}

func (n Netem) exec(ctx context.Context, args ...string) (string, error) {
	if n.Exec != nil {
		return n.Exec(ctx, "tc", args...)
	}
	out, err := exec.CommandContext(ctx, "tc", args...).CombinedOutput()
	return string(out), err
}

// Apply puts the interface into profile p and proves it.
//
// It returns the command it ran and what tc then reports for the interface.
// The second is the evidence: a run that says it was impaired should carry the
// kernel's own account of the queue discipline it ran under, not only the
// intention.
func (n Netem) Apply(ctx context.Context, p Profile) (command, qdisc string, err error) {
	args := Args(n.Device, p)
	command = "tc " + strings.Join(args, " ")

	out, runErr := n.exec(ctx, args...)
	if runErr != nil && p.Name != Clean {
		return command, "", fmt.Errorf("%s: %v: %s", command, runErr, strings.TrimSpace(out))
	}
	// Deleting a root qdisc that was never added fails, and that is the state
	// clean wants. Whether clean was reached is decided by reading it back.

	qdisc, err = n.exec(ctx, "qdisc", "show", "dev", n.Device)
	if err != nil {
		return command, "", fmt.Errorf("tc qdisc show dev %s: %v: %s", n.Device, err, strings.TrimSpace(qdisc))
	}
	qdisc = strings.TrimSpace(qdisc)
	hasNetem := strings.Contains(qdisc, "netem")
	switch {
	case p.Name == Clean && hasNetem:
		return command, qdisc, fmt.Errorf("could not clear netem from %s: %s", n.Device, qdisc)
	case p.Name != Clean && !hasNetem:
		return command, qdisc, fmt.Errorf("%s ran but %s shows no netem: %s", command, n.Device, qdisc)
	}
	return command, qdisc, nil
}
