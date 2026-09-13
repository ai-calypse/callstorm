package loadgen

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"sort"
	"time"

	"github.com/yakshgandhi/callstorm/internal/metrics"
)

// CallPoint is one call on a step's timeline.
type CallPoint struct {
	At time.Time `json:"at"`

	// TTFAMs is the median TTFA of the call's turns that count toward the
	// step's percentiles, zero when none did.
	TTFAMs float64 `json:"ttfa_ms,omitempty"`

	// Failed marks a call that did not connect, or had a turn fail.
	Failed bool `json:"failed,omitempty"`
}

// began is when a call started: the worker's own clock where the call ran,
// and the moment it was placed where it never did.
func (o callOutcome) began() time.Time {
	if !o.clockZero.IsZero() {
		return o.clockZero
	}
	return o.startedAt
}

// finished is when a call hung up, by the same rule.
func (o callOutcome) finished() time.Time {
	if !o.callEnded.IsZero() {
		return o.callEnded
	}
	return o.endedAt
}

// callRecords turns a step's outcomes into the calls log. Every call placed is
// recorded, failed ones included, each with its own timestamps and event log.
func callRecords(outcomes []callOutcome) []CallRecord {
	out := make([]CallRecord, 0, len(outcomes))
	for _, o := range outcomes {
		c := CallRecord{
			Step:      o.step,
			RequestID: o.requestID,
			Worker:    o.worker,
			Turns:     o.turns,
			StartedAt: o.began(),
			EndedAt:   o.finished(),
			Events:    o.events,
		}
		if o.err != nil {
			c.Error = o.err.Error()
		}
		// A failed call has no turns. An empty list rather than null, so a
		// reader walking every call's turns needs no special case for it.
		if c.Turns == nil {
			c.Turns = []metrics.TurnMetric{}
		}
		out = append(out, c)
	}
	return out
}

// summarizeTimeline places every call of a step in start order, and returns
// when the step's first call started and its last call hung up.
func summarizeTimeline(outcomes []callOutcome) (start, end time.Time, points []CallPoint) {
	for _, o := range outcomes {
		b, f := o.began(), o.finished()
		if b.IsZero() {
			// A call no worker ever reported has no time to place it at.
			continue
		}
		if start.IsZero() || b.Before(start) {
			start = b
		}
		if f.After(end) {
			end = f
		}
		p := CallPoint{At: b, Failed: o.err != nil}
		var ttfa []time.Duration
		for _, t := range o.turns {
			if t.Failed {
				p.Failed = true
			}
			if !t.CallerYielded && t.TTFA > 0 {
				ttfa = append(ttfa, t.TTFA)
			}
		}
		if len(ttfa) > 0 {
			p.TTFAMs = round1(metrics.Millis(metrics.Percentile(ttfa, 50)))
		}
		points = append(points, p)
	}
	sort.SliceStable(points, func(i, j int) bool { return points[i].At.Before(points[j].At) })
	return start, end, points
}

// baselineP50Ms is the median TTFA of the run's own baseline step.
func (rep *Report) baselineP50Ms() float64 {
	for _, s := range rep.Steps {
		if s.Name == rep.Baseline {
			return s.TTFA.P50Ms
		}
	}
	return 0
}

// backToNormal reads how long a recovery step took to get back to normal and
// stay there.
//
// Normal is a call whose median TTFA sits within recoveredRatio of the
// baseline's median, and "stayed there" means every later call in the step did
// too. A step that dips back to normal and then slows again has not recovered,
// so the walk starts from the end: the answer is the first call of the unbroken
// run of normal calls that finishes the step. A step whose last call is still
// slow never got back, however many normal calls came before.
func backToNormal(s StepReport, baseP50Ms float64) (back bool, afterS float64) {
	if len(s.Timeline) == 0 || baseP50Ms <= 0 {
		return false, 0
	}
	limit := baseP50Ms * recoveredRatio
	from := -1
	for i := len(s.Timeline) - 1; i >= 0; i-- {
		p := s.Timeline[i]
		if p.Failed || p.TTFAMs <= 0 || p.TTFAMs > limit {
			break
		}
		from = i
	}
	if from < 0 {
		return false, 0
	}
	return true, round1(s.Timeline[from].At.Sub(s.StartedAt).Seconds())
}

// Fingerprint is the SHA-256 of v as JSON: the scenario or profile a run
// actually executed, defaults applied, so two runs can be matched exactly.
func Fingerprint(v any) string {
	b, err := json.Marshal(v)
	if err != nil {
		return ""
	}
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}
