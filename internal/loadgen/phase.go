package loadgen

import (
	"sort"
	"time"
)

const (
	// driftThreshold is how far a step's median may move across its own
	// duration before that counts as drift rather than noise.
	driftThreshold = 0.15

	// minHalfTurns is the fewest turns each half of a step needs before the
	// two are worth comparing. Below this a single unlucky call moves the
	// median far enough to invent a trend.
	minHalfTurns = 10

	// recoveredRatio is how close to baseline a recovery step must land to
	// count as recovered. Exactly 1.0 would be a coin flip; 1.2 is inside the
	// warn line, so a step that recovered to "as good as baseline, roughly"
	// is not reported as a failure to recover.
	recoveredRatio = 1.2
)

// Phase is how one step behaved over its own duration.
//
// Every other number in a step report is a single figure for the whole step,
// which silently assumes the step was the same thing throughout. That
// assumption is what hides the two failures this exists to catch: an agent
// that degrades slowly under unchanging load, and an agent that absorbs a
// sudden jump but takes a while to do it. Both look identical to a single p95
// -- one step, one number, no shape.
//
// The shape comes from splitting the step's calls in half by start time and
// summarizing each half separately. The split is by call count rather than by
// the clock so that both halves rest on the same number of samples, which is
// what makes their percentiles comparable at all.
type Phase struct {
	Kind string `json:"kind"`

	// HeldSeconds is the step's real wall-clock span, first call placed to
	// last call finished.
	HeldSeconds float64 `json:"held_s"`

	FirstHalf  Summary `json:"first_half"`
	SecondHalf Summary `json:"second_half"`

	// DriftPct is how far the median moved from the first half to the second,
	// as a fraction. Positive means it got slower while nothing about the load
	// changed.
	//
	// It is measured on p50 rather than p95 because a half-step carries far
	// fewer samples than a step, and a p95 over a dozen turns is one unlucky
	// call rather than a trend. Both percentiles are reported above; only the
	// stable one is used to decide.
	DriftPct float64 `json:"drift_pct"`

	// Drift is one of steady, drifting, settling, or unknown. Unknown is not a
	// synonym for steady: it means the step was too short to tell, which is a
	// different thing to report and a different thing to act on.
	Drift string `json:"drift"`

	// Recovered is meaningful only on a recovery step: it says whether
	// returning to the baseline's concurrency returned the baseline's latency.
	Recovered bool `json:"recovered,omitempty"`

	// BackToNormal and BackToNormalAfterS are meaningful only on a recovery
	// step with a timeline: whether the agent got back to normal and stayed
	// there, and how many seconds into the step that began. Normal is every
	// later call's median TTFA within recoveredRatio of the baseline's median.
	BackToNormal       bool    `json:"back_to_normal,omitempty"`
	BackToNormalAfterS float64 `json:"back_to_normal_after_s,omitempty"`
}

const (
	DriftUnknown  = "unknown"
	DriftSteady   = "steady"
	DriftDrifting = "drifting"
	DriftSettling = "settling"
)

// summarizePhase splits one step's calls into the half that ran first and the
// half that ran last, and compares them.
func summarizePhase(step Step, outcomes []callOutcome) Phase {
	p := Phase{Kind: step.Phase(), Drift: DriftUnknown}

	ord := make([]callOutcome, 0, len(outcomes))
	for _, o := range outcomes {
		// A call that never connected has no turns to compare and no honest
		// place in either half. It is already counted as a setup failure.
		if o.err != nil || len(o.turns) == 0 || o.startedAt.IsZero() {
			continue
		}
		ord = append(ord, o)
	}
	if len(ord) == 0 {
		return p
	}
	sort.SliceStable(ord, func(i, j int) bool { return ord[i].startedAt.Before(ord[j].startedAt) })

	first, last := ord[0].startedAt, ord[0].endedAt
	for _, o := range ord {
		if o.endedAt.After(last) {
			last = o.endedAt
		}
	}
	p.HeldSeconds = round1(last.Sub(first).Seconds())

	if len(ord) < 2 {
		return p
	}
	mid := len(ord) / 2
	p.FirstHalf = summarize(phaseTTFA(ord[:mid]))
	p.SecondHalf = summarize(phaseTTFA(ord[mid:]))

	if p.FirstHalf.N < minHalfTurns || p.SecondHalf.N < minHalfTurns || p.FirstHalf.P50Ms <= 0 {
		return p
	}
	p.DriftPct = round3((p.SecondHalf.P50Ms - p.FirstHalf.P50Ms) / p.FirstHalf.P50Ms)
	switch {
	case p.DriftPct >= driftThreshold:
		p.Drift = DriftDrifting
	case p.DriftPct <= -driftThreshold:
		p.Drift = DriftSettling
	default:
		p.Drift = DriftSteady
	}
	return p
}

// phaseTTFA collects the response times of every turn worth timing, matching
// the filters the step-level summary uses so the two cannot disagree.
func phaseTTFA(outcomes []callOutcome) []time.Duration {
	var ds []time.Duration
	for _, o := range outcomes {
		for _, t := range o.turns {
			if t.CallerYielded || t.TTFA <= 0 {
				continue
			}
			ds = append(ds, t.TTFA)
		}
	}
	return ds
}

// Drifted reports whether any step of the run got slower over its own
// duration. It is the finding a soak exists to produce, and it is invisible in
// every per-step percentile on the page.
func (rep *Report) Drifted() []StepReport {
	var out []StepReport
	for _, s := range rep.Steps {
		if s.Phase.Drift == DriftDrifting {
			out = append(out, s)
		}
	}
	return out
}

// HasPhases reports whether this run used the phase model at all. Runs
// recorded before it, and plain ramps that never named a kind, have nothing to
// show and say so rather than rendering an empty card.
func (rep *Report) HasPhases() bool {
	for _, s := range rep.Steps {
		if s.Phase.Kind != "" && s.Phase.Kind != KindRamp {
			return true
		}
		if s.Phase.Drift != "" && s.Phase.Drift != DriftUnknown {
			return true
		}
	}
	return false
}
