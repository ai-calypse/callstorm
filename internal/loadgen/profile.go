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
// in the middle of a call and truncates it into a false failure.
type Step struct {
	Name        string `json:"name"`
	Concurrency int    `json:"concurrency"`
	Calls       int    `json:"calls"`
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
	if p.Baseline == "" {
		p.Baseline = p.Steps[0].Name
	}
	if !seen[p.Baseline] {
		return fmt.Errorf("baseline %q is not a step in this profile", p.Baseline)
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

// TotalCalls is how many calls the whole profile will place.
func (p *Profile) TotalCalls() int {
	n := 0
	for _, s := range p.Steps {
		n += s.Calls
	}
	return n
}
