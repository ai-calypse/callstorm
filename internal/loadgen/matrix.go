package loadgen

import (
	"context"
	"fmt"
	"io"
	"math"
	"time"

	"github.com/yakshgandhi/callstorm/internal/impair"
)

// Network puts the interface calls leave through into an impairment profile,
// and returns the command it ran and the interface's own account of the result.
type Network interface {
	Apply(ctx context.Context, p impair.Profile) (command, qdisc string, err error)
}

// Matrix is a profile run once per impairment cohort: load on one axis,
// network on the other.
type Matrix struct {
	// Device is the interface the impairment was applied to.
	Device  string   `json:"device"`
	Cohorts []Cohort `json:"cohorts"`
}

// Cohort is the profile's steps under one network condition.
type Cohort struct {
	Impairment impair.Profile `json:"impairment"`

	// Command is the tc invocation exactly as it ran, and Qdisc is what tc
	// reported for the interface afterwards. The first makes the run
	// reproducible; the second is the proof it happened.
	Command string `json:"tc_command"`
	Qdisc   string `json:"qdisc"`

	Steps []StepReport `json:"steps"`

	// Breakpoint is the first step that failed against the clean cohort's
	// baseline, empty when none did.
	Breakpoint string `json:"breakpoint,omitempty"`
}

// RunMatrix runs the profile under each cohort in turn, clean first.
//
// Cohorts run one after another, never side by side: the target would
// otherwise carry every cohort's calls at once, and each cohort's latency would
// include load it did not place.
//
// The clean cohort becomes the report's own steps, so everything that reads a
// plain run reads the control. Every impaired step is then scored against the
// clean cohort's baseline rather than its own: a severe network measured
// against a severe-network baseline would pass, and say nothing about what the
// network cost.
func RunMatrix(ctx context.Context, cfg Config, cohorts []impair.Profile, device string, net Network) (*Report, error) {
	if cfg.Out == nil {
		cfg.Out = io.Discard
	}
	if len(cohorts) == 0 || cohorts[0].Name != impair.Clean {
		return nil, fmt.Errorf("a matrix must run the %q control first", impair.Clean)
	}

	started := time.Now()
	var rep *Report
	m := &Matrix{Device: device}

	for _, p := range cohorts {
		cmd, qdisc, err := net.Apply(ctx, p)
		if err != nil {
			return nil, fmt.Errorf("cohort %s: %w", p.Name, err)
		}
		fmt.Fprintf(cfg.Out, "\ncohort %-10s %s", p.Name, cmd)

		r, err := Run(ctx, cfg)
		if err != nil {
			return nil, fmt.Errorf("cohort %s: %w", p.Name, err)
		}
		for i := range r.Calls {
			r.Calls[i].Cohort = p.Name
		}

		if rep == nil {
			rep = r
		} else {
			r.compareToClean(rep)
			rep.Calls = append(rep.Calls, r.Calls...)
		}
		rep.markVsClean(r)

		c := Cohort{Impairment: p, Command: cmd, Qdisc: qdisc, Steps: r.Steps}
		if bp := r.Breakpoint(); bp != nil {
			c.Breakpoint = bp.Name
		}
		m.Cohorts = append(m.Cohorts, c)

		if ctx.Err() != nil {
			break
		}
	}

	rep.Matrix = m
	rep.StartedAt = started
	rep.Duration = time.Since(started).Seconds()
	return rep, nil
}

// compareToClean rescores an impaired cohort against the clean cohort's
// baseline step.
func (rep *Report) compareToClean(clean *Report) {
	rep.scoreAgainst(clean.baselineP95())
}

// markVsClean sets each step of cohort r to its p95 over the same step of the
// clean run rep. The clean run marks itself, at exactly 1.
func (rep *Report) markVsClean(r *Report) {
	clean := map[string]time.Duration{}
	for _, s := range rep.Steps {
		clean[s.Name] = s.TTFA.p95Raw
	}
	for i := range r.Steps {
		s := &r.Steps[i]
		if base := clean[s.Name]; base > 0 && s.TTFA.p95Raw > 0 {
			s.VsClean = math.Round(float64(s.TTFA.p95Raw)/float64(base)*100) / 100
		}
	}
}
