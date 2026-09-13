package loadgen

import (
	"math"
	"time"

	"github.com/yakshgandhi/callstorm/internal/judge"
	"github.com/yakshgandhi/callstorm/internal/metrics"
)

// The fail line follows the degradation rule in Coval's load-testing
// methodology (March 2026), p95 within 2x the baseline at peak; the warn line
// at 1.5x is Callstorm's own. A step is scored by how far it has degraded
// relative to the baseline step, not against an absolute latency target,
// because published targets disagree and a target that is generous for one
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
	N     int     `json:"n"`
	P50Ms float64 `json:"p50_ms"`

	// P90Ms sits between the usual turn and the tail, and is offered on the
	// impairment heatmap beside p50 and p95.
	P90Ms  float64 `json:"p90_ms"`
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
		P90Ms:  ms(metrics.Percentile(ds, 90)),
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

	// TransportRTT is the round trip to the agent's edge measured during this
	// step's turns. AgentTTFA and AgentEndpointing are TTFA and endpointing with
	// each turn's own round trip subtracted: an estimate of the share of the
	// wait the agent owns. The observed figures above are what the caller
	// experienced, and stay the ones the verdict is scored on. A turn with no
	// pong counts in the observed figures and not here, so AgentTTFA.N below
	// TTFA.N says how much of the step could be corrected.
	TransportRTT     Summary `json:"transport_rtt"`
	AgentTTFA        Summary `json:"agent_ttfa"`
	AgentEndpointing Summary `json:"agent_endpointing"`

	WorstDriftMs float64 `json:"worst_harness_drift_ms"`

	// Conversation is how the calls sounded: pace, share of the talking,
	// interruptions and dead air. Latency says how fast; this says how it felt.
	Conversation Conversation `json:"conversation"`

	// Cost is what this step cost to run, priced only when a rate was given.
	Cost Cost `json:"cost"`

	// BargeIn is how the agent behaved when the caller cut in on it.
	BargeIn BargeInStats `json:"barge_in"`

	// WER is how accurately the agent heard this step's callers. It is the one
	// failure no latency number can show: an agent can answer fast, fluently,
	// and to a question nobody asked.
	WER WERStats `json:"wer"`

	// Phase is how the step behaved across its own duration: the shape a
	// single percentile flattens away.
	Phase Phase `json:"phase"`

	// HarnessDegraded marks a step where the load generator could not hold
	// realtime pacing. Its latency numbers still describe the agent, but the
	// call timing stopped being realistic, so the step is suspect.
	HarnessDegraded bool `json:"harness_degraded,omitempty"`

	// P95Ratio is this step's p95 TTFA over the baseline step's. Verdict is
	// pass, warn or fail against that ratio and the setup success rate.
	P95Ratio float64 `json:"p95_ratio"`
	Verdict  string  `json:"verdict"`

	// VsClean is this step's p95 TTFA over the same step in the clean cohort
	// of an impairment matrix: what the network cost, with the load held equal.
	// Zero outside a matrix.
	VsClean float64 `json:"vs_clean,omitempty"`

	// Nodes is the pass rate of each scenario node's assertion over this step.
	Nodes []NodeStats `json:"nodes,omitempty"`

	Errors map[string]int `json:"errors,omitempty"`
}

// BargeInStats is how an agent handled being interrupted, over one step.
type BargeInStats struct {
	// Turns is how many caller lines deliberately cut in. Zero means the
	// scenario never tried, not that the agent behaved well.
	Turns int `json:"turns"`

	// Yielded is how many of those the agent actually stopped for. The gap
	// between the two is the finding: an agent that keeps talking has taken
	// the floor from the person paying for the call, and no latency number
	// reports it.
	Yielded int `json:"yielded"`

	YieldP50Ms float64 `json:"yield_p50_ms"`
	YieldP95Ms float64 `json:"yield_p95_ms"`
	WorstMs    float64 `json:"worst_ms"`
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

	// Judge is what an LLM grader made of the conversations, present only when
	// the run was placed with -judge. It is on the report rather than beside
	// it because the finding it carries -- whether the calls the agent
	// misheard are the calls it failed -- needs the transcripts and the
	// verdicts in the same document.
	Judge *TaskSuccess `json:"judge,omitempty"`

	// Matrix is present when the run crossed its profile with network
	// impairment. Steps above are then the clean cohort, so every card that
	// reads a plain run reads the same-session control.
	Matrix *Matrix `json:"matrix,omitempty"`

	// Integrity is whether the harness's own event pipeline delivered what it
	// sent. Absent when the run used no pipeline to check.
	Integrity *Integrity `json:"integrity,omitempty"`

	// References are the published lines this run was read against, copied in
	// at the time it ran. The list will change as vendors publish, and a
	// report should still say what it was compared with.
	References []Reference `json:"references,omitempty"`

	// Calls is every conversation the run produced, kept out of the report
	// card and written alongside it. The report card answers how fast the
	// agent was; these are what it actually said, which is what a judge -- or
	// a person wondering why a step failed -- has to read.
	Calls []CallRecord `json:"-"`
}

// CallRecord is one call's conversation, tagged with the step that placed it.
type CallRecord struct {
	Step      string `json:"step"`
	RequestID string `json:"request_id,omitempty"`

	// Worker names the pod that placed the call, empty when the run was not
	// distributed. It is what lets a chaos run show which calls moved.
	Worker string `json:"worker,omitempty"`

	// Cohort names the impairment profile the call was placed under, empty
	// outside a matrix. Step names repeat across cohorts, so without it a call
	// cannot be traced back to the network it ran on.
	Cohort string `json:"cohort,omitempty"`

	Turns []metrics.TurnMetric `json:"turns"`
}

// callOutcome is one completed (or failed) call, tagged with its step.
type callOutcome struct {
	step      string
	requestID string
	worker    string
	turns     []metrics.TurnMetric
	err       error

	// startedAt and endedAt are wall-clock, and exist so a step can be split
	// into the calls that ran first and the calls that ran last. Nothing else
	// in a step report carries an ordering, so without them a step is a bag of
	// calls and drift across it cannot be seen at all.
	startedAt time.Time
	endedAt   time.Time
}

// buildStepReport folds every call placed during one step into its report.
func buildStepReport(step Step, outcomes []callOutcome) StepReport {
	return buildStepReportAt(step, outcomes, 0)
}

// buildStepReportAt is buildStepReport with a per-minute rate for pricing.
func buildStepReportAt(step Step, outcomes []callOutcome, ratePerMinute float64) StepReport {
	r := StepReport{Step: step, Errors: map[string]int{}}

	var ttfa, endpointing, thinkSpeak, turnLatency []time.Duration
	var agentTTFA, agentEndpointing, rtts []time.Duration
	var worstDrift time.Duration

	var (
		werTurns, werSubs, werDels, werIns, werRefWords int
		werWorst                                        float64

		bargeTurns int
		yields     []time.Duration
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
			if t.BargedIn {
				bargeTurns++
				// A zero yield is an agent never seen to stop. Recording it as
				// a duration would average it in as if it were instant, which
				// is the opposite of what happened.
				if t.BargeInYield > 0 {
					yields = append(yields, t.BargeInYield)
				}
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

			// Only a turn with a measured round trip is corrected. A turn with
			// no pong is still what the caller waited through, so it stays in
			// the observed figures and is simply not estimated here.
			if t.TransportRTTMeasured {
				rtts = append(rtts, t.TransportRTT)
				if t.TTFA > 0 {
					agentTTFA = append(agentTTFA, t.TTFA-t.TransportRTT)
				}
				if t.Endpointing > 0 {
					agentEndpointing = append(agentEndpointing, t.Endpointing-t.TransportRTT)
				}
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
	r.TransportRTT = summarize(rtts)
	r.AgentTTFA = summarize(agentTTFA)
	r.AgentEndpointing = summarize(agentEndpointing)
	r.WorstDriftMs = math.Round(float64(worstDrift.Microseconds())/1000*10) / 10
	r.HarnessDegraded = math.Abs(r.WorstDriftMs) > MaxHealthyDriftMs

	yieldSummary := summarize(yields)
	r.BargeIn = BargeInStats{
		Turns:      bargeTurns,
		Yielded:    len(yields),
		YieldP50Ms: yieldSummary.P50Ms,
		YieldP95Ms: yieldSummary.P95Ms,
		WorstMs:    yieldSummary.MaxMs,
	}

	r.Conversation = summarizeConversation(outcomes)
	r.Cost = summarizeCost(outcomes, ratePerMinute)
	r.Phase = summarizePhase(step, outcomes)

	// A step held for a duration was configured with no call count, so the
	// report supplies the one it actually placed. Otherwise every consumer
	// downstream -- the CSV, the dashboard, the cost estimate -- reads a soak
	// as a step that placed no calls.
	if r.Calls == 0 {
		r.Calls = r.CallsAttempted
	}

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
	rep.scoreAgainst(rep.baselineP95())
}

// baselineP95 is the p95 TTFA of the run's own baseline step.
func (rep *Report) baselineP95() time.Duration {
	for _, s := range rep.Steps {
		if s.Name == rep.Baseline {
			return s.TTFA.p95Raw
		}
	}
	return 0
}

// scoreAgainst fills in each step's ratio and verdict against a given p95. An
// impaired cohort is scored against the clean cohort's baseline rather than its
// own, so its verdicts say what the network cost and not merely what load did
// on an already degraded line.
func (rep *Report) scoreAgainst(base time.Duration) {
	for i := range rep.Steps {
		s := &rep.Steps[i]
		s.P95Ratio = 0
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

		// A recovery step is the one place the ratio answers a different
		// question: not "did it degrade" but "did it come back". It runs at
		// the baseline's own concurrency, so anything close to the baseline's
		// latency means the agent returned rather than stayed broken.
		if s.Phase.Kind == KindRecovery {
			s.Phase.Recovered = s.P95Ratio > 0 && s.P95Ratio <= recoveredRatio
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
