package loadgen

import (
	_ "embed"
	"encoding/json"
	"fmt"
)

// Reference lines are thresholds and observations other people have
// published, each placed on the Callstorm clock it was defined against.
//
// They replace a single absolute grade. That grade presented one vendor's
// latency bands as if they were settled science, and applied them on a
// different clock from the one the vendor used. There is no consensus to adopt:
// the same vendor publishes different thresholds for the same acronym measured
// between different instants. So each line carries its source, its date, the
// boundary the source defined, and why it sits on the axis it does here.
//
// None of them is a verdict. The verdict stays relative: each step against the
// run's own baseline.
//
//go:embed references.json
var referencesJSON []byte

// The two clocks every run is reported on.
const (
	// AxisEndOfSpeech runs from the instant the caller's audio actually
	// stopped. Callstorm synthesized that audio, so this is ground truth, and
	// it is the TTFA every other card reports.
	AxisEndOfSpeech = "end_of_speech"

	// AxisDetection runs from the instant the agent's transcript of the
	// caller arrived: think/speak. It is the nearest clock Callstorm has to one
	// that starts when the agent detected the caller had stopped. It starts
	// later than voice activity detection does, by transcript finalization and
	// the trip back, so a line placed on it is read slightly in the agent's
	// favour.
	AxisDetection = "detection"
)

// Kinds of reference line, in the order a reading should meet them: where
// agents actually are, what people say they should be, and what a human does.
const (
	KindObserved   = "observed"
	KindTarget     = "target"
	KindPerceptual = "perceptual"
)

// Citation is one published source. Published is a date, to the day where the
// source gives one and to the month where it does not. URL is optional: a
// citation is its source, title and date, and a link is drawn only when known.
type Citation struct {
	Source    string `json:"source"`
	Title     string `json:"title"`
	Published string `json:"published"`
	URL       string `json:"url,omitempty"`
}

// Reference is one line.
type Reference struct {
	ID    string `json:"id"`
	Label string `json:"label"`
	Kind  string `json:"kind"`

	// Ms is the line. ToMs, when set, makes it a range: an observed spread
	// rather than a threshold.
	Ms   float64 `json:"ms"`
	ToMs float64 `json:"to_ms,omitempty"`

	// Percentile is the one the source states, or 0 when it states none, in
	// which case the line is read against both p50 and p95.
	Percentile int `json:"percentile"`

	Axis string `json:"axis"`

	// Boundary is the source's own definition of what it measured, and
	// Placement is why that definition lands on Axis, including which way the
	// mismatch leans. A line without both is a number with no clock.
	Boundary  string `json:"boundary"`
	Placement string `json:"placement"`

	Cites []Citation `json:"cites"`
}

// References returns the published lines.
func References() []Reference {
	var refs []Reference
	if err := json.Unmarshal(referencesJSON, &refs); err != nil {
		// The file is embedded, so a parse failure is a build defect that
		// TestReferencesAreCited catches before it ships.
		panic(fmt.Sprintf("references.json: %v", err))
	}
	return refs
}

// ReferencesJSON is the embedded file, for the dashboard to serve unchanged.
func ReferencesJSON() []byte { return referencesJSON }

// Mark is one step's reading against one reference line.
type Mark struct {
	Percentile int     `json:"percentile"`
	ValueMs    float64 `json:"value_ms"`

	// Over is true when the step's value is past the line: above a threshold,
	// or above the top of an observed range.
	Over bool `json:"over"`
}

// Place reads a step against a reference line on the line's own axis. A step
// with no samples on that axis -- a target that sends no transcript has no
// detection clock -- places nothing.
func Place(r Reference, s StepReport) []Mark {
	sum := s.TTFA
	if r.Axis == AxisDetection {
		sum = s.ThinkSpeak
	}
	if sum.N == 0 {
		return nil
	}
	percentiles := []int{r.Percentile}
	if r.Percentile == 0 {
		percentiles = []int{50, 95}
	}
	top := r.Ms
	if r.ToMs > 0 {
		top = r.ToMs
	}
	var marks []Mark
	for _, p := range percentiles {
		v := sum.P50Ms
		switch p {
		case 90:
			v = sum.P90Ms
		case 95:
			v = sum.P95Ms
		case 99:
			v = sum.P99Ms
		}
		marks = append(marks, Mark{Percentile: p, ValueMs: v, Over: v > top})
	}
	return marks
}
