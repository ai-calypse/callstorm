// Package loadgen places many synthetic calls at once and reports what
// happened at each level of concurrency.
//
// The load shape is described as a list of steps rather than a smooth ramp,
// because a percentile is only meaningful over a set of turns that all ran
// under the same conditions. Aggregating p95 across a rising ramp averages the
// easy start together with the hard finish and hides exactly the degradation
// the run exists to find.
package loadgen

import (
	"encoding/json"
	"fmt"
	"os"
)

// Step holds a fixed concurrency until it has placed Calls calls.
//
// Steps are sized in calls rather than wall-clock duration so that every step
// yields an exact, comparable sample count, and so a step boundary never lands
// in the middle of a call and truncates it into a false failure. A soak is the
// exception, and says so with hold_s: what a soak measures is drift over time,
// which a call budget cannot express without already knowing how long a call
// takes.
type Step struct {
	Name        string `json:"name"`
	Concurrency int    `json:"concurrency"`
	Calls       int    `json:"calls"`

	// Kind says what this step is for, which changes how it is read rather
	// than how it is run. A step at 40 concurrent placed straight after one at
	// 1 and a step at 40 reached by a ramp execute identically; only the
	// question being asked differs, and nothing in the numbers reveals which
	// one was intended.
	//
	// One of: smoke, ramp, stress, spike, soak, recovery. Empty means ramp.
	Kind string `json:"kind,omitempty"`

	// HoldSeconds runs the step for a wall-clock duration instead of a fixed
	// number of calls. It exists for soaks: the failure a soak looks for is
	// drift over time at unchanging load, and a call budget cannot express
	// "hold this for twenty minutes" without first knowing how long a call
	// takes -- which is the thing being measured.
	HoldSeconds float64 `json:"hold_s,omitempty"`
}

// Kinds are the six phases a load test is made of. Callstorm ran only two of
// them for most of its life -- a smoke call and a stepped ramp -- which left
// three questions unasked: what a sudden jump does, what an hour at steady
// load does, and whether the agent comes back afterwards.
const (
	KindSmoke    = "smoke"    // does it work at all
	KindRamp     = "ramp"     // expected load, stepped, to find the shape
	KindStress   = "stress"   // past expected load, to find the breakpoint
	KindSpike    = "spike"    // straight to peak with no warm-up
	KindSoak     = "soak"     // steady load held long enough for drift to show
	KindRecovery = "recovery" // back to baseline, to see whether it returns
)

var kinds = map[string]bool{
	KindSmoke: true, KindRamp: true, KindStress: true,
	KindSpike: true, KindSoak: true, KindRecovery: true,
}

// Phase names the kind of step this is, defaulting to ramp.
func (s Step) Phase() string {
	if s.Kind == "" {
		return KindRamp
	}
	return s.Kind
}

// Profile is an ordered set of steps. The step named in Baseline is the
// reference every other step is scored against.
type Profile struct {
	Name     string `json:"name"`
	Baseline string `json:"baseline"`
	Steps    []Step `json:"steps"`
}

func LoadProfile(path string) (*Profile, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var p Profile
	if err := json.Unmarshal(b, &p); err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	if err := p.validate(); err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	return &p, nil
}

func (p *Profile) validate() error {
	if len(p.Steps) == 0 {
		return fmt.Errorf("profile has no steps")
	}
	seen := map[string]bool{}
	for i := range p.Steps {
		s := &p.Steps[i]
		if s.Name == "" {
			s.Name = fmt.Sprintf("step-%d", i+1)
		}
		if seen[s.Name] {
			return fmt.Errorf("duplicate step name %q", s.Name)
		}
		seen[s.Name] = true

		if s.Concurrency < 1 {
			return fmt.Errorf("step %q: concurrency must be at least 1", s.Name)
		}
		if s.Kind != "" && !kinds[s.Kind] {
			return fmt.Errorf("step %q: unknown kind %q (want smoke, ramp, stress, spike, soak or recovery)",
				s.Name, s.Kind)
		}
		if s.HoldSeconds < 0 {
			return fmt.Errorf("step %q: hold_s cannot be negative", s.Name)
		}
		if s.HoldSeconds > 0 && s.Calls > 0 {
			// Both would mean two different stopping conditions racing, and
			// which one won would be invisible in the report.
			return fmt.Errorf("step %q: set calls or hold_s, not both", s.Name)
		}
		if s.HoldSeconds == 0 {
			if s.Calls < 1 {
				// One call per slot is the useful default: every slot places
				// exactly one call, so the step runs at full concurrency
				// throughout instead of trailing off.
				s.Calls = s.Concurrency
			}
			if s.Calls < s.Concurrency {
				return fmt.Errorf("step %q: %d calls cannot fill %d slots",
					s.Name, s.Calls, s.Concurrency)
			}
		}
	}
	if p.Baseline == "" {
		p.Baseline = p.Steps[0].Name
	}
	if !seen[p.Baseline] {
		return fmt.Errorf("baseline %q is not a step in this profile", p.Baseline)
	}
	return p.validateRecovery()
}

// validateRecovery refuses a recovery step that cannot answer the question it
// exists to ask.
//
// Recovery is scored against the baseline, so it has to run at the baseline's
// concurrency: a step at half the load returning to baseline latency proves
// nothing, and a step at twice the load failing to is not a recovery failure.
// And a recovery placed before anything has stressed the agent has nothing to
// recover from.
func (p *Profile) validateRecovery() error {
	var baseConc int
	for _, s := range p.Steps {
		if s.Name == p.Baseline {
			baseConc = s.Concurrency
		}
	}
	stressed := false
	for i, s := range p.Steps {
		if s.Phase() != KindRecovery {
			if s.Concurrency > baseConc {
				stressed = true
			}
			continue
		}
		if s.Concurrency != baseConc {
			return fmt.Errorf(
				"step %q is a recovery at %d concurrent but the baseline %q runs at %d; "+
					"a recovery is only readable at the same load it is compared against",
				s.Name, s.Concurrency, p.Baseline, baseConc)
		}
		if !stressed {
			return fmt.Errorf(
				"step %q is a recovery but step %d of the profile; nothing before it "+
					"exceeded the baseline, so there is nothing to recover from",
				s.Name, i+1)
		}
	}
	return nil
}

// PeakConcurrency is the highest concurrency any step reaches. The preflight
// check uses it to refuse a run that would exceed the target's connection cap.
func (p *Profile) PeakConcurrency() int {
	peak := 0
	for _, s := range p.Steps {
		if s.Concurrency > peak {
			peak = s.Concurrency
		}
	}
	return peak
}

// TotalCalls is how many calls the whole profile will place. A step held for a
// duration contributes nothing here: how many calls fit in twenty minutes is
// not known until the run has placed them.
func (p *Profile) TotalCalls() int {
	n := 0
	for _, s := range p.Steps {
		n += s.Calls
	}
	return n
}

// HeldSeconds totals the wall-clock time the profile's duration-based steps
// will run for. It is what the call count cannot say, and a cost estimate that
// ignores it would understate a soak by the whole soak.
func (p *Profile) HeldSeconds() float64 {
	var t float64
	for _, s := range p.Steps {
		t += s.HoldSeconds
	}
	return t
}
