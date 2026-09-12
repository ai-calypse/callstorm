package loadgen

import (
	"math"
	"time"

	"github.com/yakshgandhi/callstorm/internal/judge"
	"github.com/yakshgandhi/callstorm/internal/metrics"
)

// Verdict thresholds follow the published voice-agent load-testing guidance:
// score a step by how far it has degraded relative to the baseline step, not
// against an absolute latency target, because a target that is generous for one
// agent is unreachable for another.
const (
	warnRatio = 1.5 // p95 up to 1.5x baseline passes
	failRatio = 2.0 // beyond 2x baseline fails

	minSetupSuccess = 0.97 // call setup success below this fails outright

	// MaxHealthyDriftMs is how far the caller's audio may drift from realtime
	// before the harness itself, rather than the agent, is the thing being
	// measured. One 20ms frame of slack is normal; five is a warning that this
	// step's numbers describe the load generator's limits.
	MaxHealthyDriftMs = 100
)

// Summary is the percentile spread of one metric over one step.
type Summary struct {
	N      int     `json:"n"`
	P50Ms  float64 `json:"p50_ms"`
	P95Ms  float64 `json:"p95_ms"`
	P99Ms  float64 `json:"p99_ms"`
	MaxMs  float64 `json:"max_ms"`
	p95Raw time.Duration
}

func summarize(ds []time.Duration) Summary {
	if len(ds) == 0 {
		return Summary{}
	}
	ms := func(d time.Duration) float64 {
		return math.Round(float64(d.Microseconds())/1000*10) / 10
	}
	p95 := metrics.Percentile(ds, 95)
	return Summary{
		N:      len(ds),
		P50Ms:  ms(metrics.Percentile(ds, 50)),
		P95Ms:  ms(p95),
		P99Ms:  ms(metrics.Percentile(ds, 99)),
		MaxMs:  ms(metrics.Percentile(ds, 100)),
		p95Raw: p95,
	}
}

// StepReport is everything one concurrency level produced.
type StepReport struct {
	Step

	CallsAttempted int `json:"calls_attempted"`
	CallsConnected int `json:"calls_connected"`
	TurnsTotal     int `json:"turns_total"`
	TurnsFailed    int `json:"turns_failed"`
	TurnsYielded   int `json:"turns_yielded"`

	// SetupSuccess is the share of attempted calls that reached a live
	// conversation. It is tracked apart from turn failures because a target
	// that refuses connections and one that answers slowly fail differently.
	SetupSuccess float64 `json:"setup_success"`

	TTFA        Summary `json:"ttfa"`
	Endpointing Summary `json:"endpointing"`
	ThinkSpeak  Summary `json:"think_speak"`
	TurnLatency Summary `json:"turn_latency"`

	WorstDriftMs float64 `json:"worst_harness_drift_ms"`

	// WER is how accurately the agent heard this step's callers. It is the one
	// failure no latency number can show: an agent can answer fast, fluently,
	// and to a question nobody asked.
	WER WERStats `json:"wer"`

	// HarnessDegraded marks a step where the load generator could not hold
	// realtime pacing. Its latency numbers still describe the agent, but the
	// call timing stopped being realistic, so the step is suspect.
	HarnessDegraded bool `json:"harness_degraded,omitempty"`

	// P95Ratio is this step's p95 TTFA over the baseline step's. Verdict is
	// pass, warn or fail against that ratio and the setup success rate.
	P95Ratio float64 `json:"p95_ratio"`
	Verdict  string  `json:"verdict"`

	Errors map[string]int `json:"errors,omitempty"`
}

// WERStats is transcript accuracy over one step.
type WERStats struct {
	// Turns counts only turns the target actually transcribed. A target that
	// reports no transcript contributes nothing here rather than scoring a
	// perfect zero for having said nothing.
	Turns int `json:"turns"`

	// Mean is total errors over total words spoken across the step, not the
	// average of per-turn rates. Averaging rates would let a three-word turn
	// weigh as heavily as a thirty-word one.
	Mean  float64 `json:"mean"`
	Worst float64 `json:"worst"`

	Substitutions int `json:"substitutions"`
	Deletions     int `json:"deletions"`
	Insertions    int `json:"insertions"`
	RefWords      int `json:"ref_words"`
}

// Report is a whole profile run.
type Report struct {
	Profile   string       `json:"profile"`
	Scenario  string       `json:"scenario"`
	Target    string       `json:"target"`
	Baseline  string       `json:"baseline"`
	StartedAt time.Time    `json:"started_at"`
	Duration  float64      `json:"duration_s"`
	Steps     []StepReport `json:"steps"`

	// Calls is every conversation the run produced, kept out of the report
	// card and written alongside it. The report card answers how fast the
	// agent was; these are what it actually said, which is what a judge -- or
	// a person wondering why a step failed -- has to read.
	Calls []CallRecord `json:"-"`
}

// CallRecord is one call's conversation, tagged with the step that placed it.
type CallRecord struct {
	Step      string               `json:"step"`
	RequestID string               `json:"request_id,omitempty"`
	Turns     []metrics.TurnMetric `json:"turns"`
}

// callOutcome is one completed (or failed) call, tagged with its step.
type callOutcome struct {
	step      string
	requestID string
	turns     []metrics.TurnMetric
	err       error
}

// buildStepReport folds every call placed during one step into its report.
func buildStepReport(step Step, outcomes []callOutcome) StepReport {
	r := StepReport{Step: step, Errors: map[string]int{}}

	var ttfa, endpointing, thinkSpeak, turnLatency []time.Duration
	var worstDrift time.Duration

	var (
		werTurns, werSubs, werDels, werIns, werRefWords int
		werWorst                                        float64
	)

	for _, o := range outcomes {
		r.CallsAttempted++
		if o.err != nil {
			r.Errors[classify(o.err)]++
			continue
		}
		r.CallsConnected++

		for _, t := range o.turns {
			r.TurnsTotal++
			if t.Failed {
				r.TurnsFailed++
				if t.FailReason != "" {
					r.Errors[t.FailReason]++
				}
			}
			if t.CallerYielded {
				r.TurnsYielded++
			}
			if abs(t.PacingDrift) > abs(worstDrift) {
				worstDrift = t.PacingDrift
			}

			// A turn the agent talked over has no meaningful response
			// latency: the clock would run backwards. Count it, exclude it.
			if t.CallerYielded {
				continue
			}

			// Transcript accuracy is scored only on turns the caller finished,
			// and only where the target returned a transcript at all. A turn
			// cut short by the agent never had its script fully spoken, so
			// every unsaid word would be charged to the agent's hearing.
			if t.HeardText != "" {
				w := judge.Score(t.CallerText, t.HeardText)
				werTurns++
				werSubs += w.Substitutions
				werDels += w.Deletions
				werIns += w.Insertions
				werRefWords += w.RefWords
				if w.Rate > werWorst {
					werWorst = w.Rate
				}
			}

			if t.TTFA > 0 {
				ttfa = append(ttfa, t.TTFA)
			}
			if t.Endpointing > 0 {
				endpointing = append(endpointing, t.Endpointing)
			}
			if t.ThinkSpeak > 0 {
				thinkSpeak = append(thinkSpeak, t.ThinkSpeak)
			}
			if t.TurnLatency > 0 {
				turnLatency = append(turnLatency, t.TurnLatency)
			}
		}
	}

	if r.CallsAttempted > 0 {
		r.SetupSuccess = float64(r.CallsConnected) / float64(r.CallsAttempted)
	}
	r.TTFA = summarize(ttfa)
	r.Endpointing = summarize(endpointing)
	r.ThinkSpeak = summarize(thinkSpeak)
	r.TurnLatency = summarize(turnLatency)
	r.WorstDriftMs = math.Round(float64(worstDrift.Microseconds())/1000*10) / 10
	r.HarnessDegraded = math.Abs(r.WorstDriftMs) > MaxHealthyDriftMs

	r.WER = WERStats{
		Turns:         werTurns,
		Worst:         werWorst,
		Substitutions: werSubs,
		Deletions:     werDels,
		Insertions:    werIns,
		RefWords:      werRefWords,
	}
	if werRefWords > 0 {
		r.WER.Mean = float64(werSubs+werDels+werIns) / float64(werRefWords)
	}

	if len(r.Errors) == 0 {
		r.Errors = nil
	}
	return r
}

// score fills in each step's degradation ratio and verdict, relative to the
// baseline step.
func (rep *Report) score() {
	var base time.Duration
	for _, s := range rep.Steps {
		if s.Name == rep.Baseline {
			base = s.TTFA.p95Raw
			break
		}
	}

	for i := range rep.Steps {
		s := &rep.Steps[i]
		switch {
		case s.CallsAttempted > 0 && s.SetupSuccess < minSetupSuccess:
			s.Verdict = "fail"
		case base <= 0 || s.TTFA.N == 0:
			s.Verdict = "n/a"
		default:
			s.P95Ratio = math.Round(float64(s.TTFA.p95Raw)/float64(base)*100) / 100
			switch {
			case s.P95Ratio > failRatio:
				s.Verdict = "fail"
			case s.P95Ratio > warnRatio:
				s.Verdict = "warn"
			default:
				s.Verdict = "pass"
			}
		}
	}
}

// Breakpoint is the first step that failed, or "" if every step passed. This is
// the number the whole run exists to produce: the concurrency at which the
// agent under test stops meeting its baseline.
func (rep *Report) Breakpoint() *StepReport {
	for i := range rep.Steps {
		if rep.Steps[i].Verdict == "fail" {
			return &rep.Steps[i]
		}
	}
	return nil
}

func classify(err error) string {
	if err == nil {
		return ""
	}
	s := err.Error()
	if len(s) > 60 {
		s = s[:60]
	}
	return s
}

func abs(d time.Duration) time.Duration {
	if d < 0 {
		return -d
	}
	return d
}
