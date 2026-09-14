package loadgen

import (
	"fmt"

	"github.com/yakshgandhi/callstorm/internal/judge"
)

// The lines a step's quality is held to. Each is a published one where a
// published one exists; each page is listed with its link under Sources in the
// README.
const (
	// Hamming's load-testing gate for task completion under load against the
	// baseline: within 5% passes, a drop of 5% to 10% warns, more fails ("Voice
	// Agent Load Testing Guide", May 2026). The page gives percentages; they are
	// read here as points of the pass rate.
	taskDropWarn = 0.05
	taskDropFail = 0.10

	// Hamming's regression tolerance for word error rate is ±2% ("Voice Agent
	// Testing Guide", January 2026), read here as 2 points, and above 15% it
	// calls recognition poor ("Voice Agent Evaluation Metrics", January 2026).
	// A rise past the
	// tolerance warns; a rise that also leaves the step above 15% fails.
	werRiseWarn = 0.02
	werPoor     = 0.15

	// A rise of 5 points in turns the agent talked over is the line the
	// dashboard's question already uses; above 10% of turns Hamming calls
	// interruptions poor ("Voice Agent Analytics & Post-Call Metrics", February
	// 2026).
	interruptRiseWarn = 0.05
	interruptPoor     = 0.10

	// Hamming's error-rate bands: under 0.5% good, under 1% acceptable, above
	// 1% critical ("Testing Voice Agents: Load, Regression, and A/B Evaluation
	// for Production Reliability", December 2025). Failed turns are held to the bands rather than to the baseline: a
	// failed turn is a failure at any load.
	failedTurnsWarn = 0.005
	failedTurnsFail = 0.01

	// minQualitySample is the fewest checks on each side before a rate is
	// compared, and it is Callstorm's own floor: below 30, one call moves a
	// rate by more than the 5-point line being tested. The same Hamming
	// reliability guide asks for 1,000+ calls per variant before an A/B
	// comparison reaches 95% confidence, so a comparison clearing 30 is still
	// rough, and every reason says how many checks it rests on.
	minQualitySample = 30

	// rateEpsilon keeps a rate stored to three places from crossing a line it
	// sits exactly on.
	rateEpsilon = 1e-9
)

// Quality is a step's verdict on doing the job, beside Verdict on speed.
//
// The latency verdict cannot see an agent that answers as fast as ever and
// gets the task wrong, mishears more, or starts cutting callers off. Coval's
// and Hamming's load-testing guides both count a decline in quality as the
// point an agent stops coping, not only a slowdown, so a step carries both
// verdicts and the report names the first step each failed at.
type Quality struct {
	Verdict string `json:"verdict"`

	// Compared names every check that had enough on both sides to compare, and
	// Reasons says, with its numbers, each one that warned or failed -- failures
	// first. A pass that compared only failed turns says much less than one that
	// compared five checks, and a reader has to be able to see which it was.
	Compared []string `json:"compared,omitempty"`
	Reasons  []string `json:"reasons,omitempty"`
}

// qualityOf scores step s against the baseline step. judged is the judge's
// graded calls, nil when the run was not judged.
func qualityOf(s, base StepReport, judged []judge.CallScore) Quality {
	if s.TurnsTotal == 0 {
		return Quality{Verdict: "n/a"}
	}
	q := Quality{Verdict: "pass"}
	flag := func(level, reason string) {
		if level == "fail" {
			q.Verdict = "fail"
			q.Reasons = append([]string{reason}, q.Reasons...)
			return
		}
		if q.Verdict == "pass" {
			q.Verdict = "warn"
		}
		q.Reasons = append(q.Reasons, reason)
	}

	q.Compared = append(q.Compared, "failed turns")
	failed := float64(s.TurnsFailed) / float64(s.TurnsTotal)
	switch {
	case failed > failedTurnsFail:
		flag("fail", fmt.Sprintf("%d of %d turns failed (%s), past the 1%% Hamming calls critical",
			s.TurnsFailed, s.TurnsTotal, pctTenth(failed)))
	case failed > failedTurnsWarn:
		flag("warn", fmt.Sprintf("%d of %d turns failed (%s), past the 0.5%% Hamming calls good",
			s.TurnsFailed, s.TurnsTotal, pctTenth(failed)))
	}

	for _, n := range s.Nodes {
		b, ok := nodeNamed(base.Nodes, n.Node)
		if !ok || n.Checked < minQualitySample || b.Checked < minQualitySample {
			continue
		}
		q.Compared = append(q.Compared, "the "+n.Node+" check")
		if level := dropLevel(b.PassRate - n.PassRate); level != "" {
			flag(level, fmt.Sprintf("the %s check passed %s of %d visits, against %s of %d at the baseline",
				n.Node, pctWhole(n.PassRate), n.Checked, pctWhole(b.PassRate), b.Checked))
		}
	}

	if rate, n := judgedRate(judged, s.Name); n >= minQualitySample {
		if baseRate, baseN := judgedRate(judged, base.Name); baseN >= minQualitySample {
			q.Compared = append(q.Compared, "task success")
			if level := dropLevel(baseRate - rate); level != "" {
				flag(level, fmt.Sprintf("the judge passed %s of %d calls, against %s of %d at the baseline",
					pctWhole(rate), n, pctWhole(baseRate), baseN))
			}
		}
	}

	if s.WER.Turns >= minQualitySample && base.WER.Turns >= minQualitySample {
		q.Compared = append(q.Compared, "words misheard")
		if s.WER.Mean-base.WER.Mean > werRiseWarn+rateEpsilon {
			level := "warn"
			if s.WER.Mean > werPoor {
				level = "fail"
			}
			flag(level, fmt.Sprintf("misheard %s of words over %d turns, against %s at the baseline",
				pctTenth(s.WER.Mean), s.WER.Turns, pctTenth(base.WER.Mean)))
		}
	}

	c, bc := s.Conversation, base.Conversation
	if c.Turns >= minQualitySample && bc.Turns >= minQualitySample {
		q.Compared = append(q.Compared, "interruptions")
		if c.InterruptionRate-bc.InterruptionRate > interruptRiseWarn+rateEpsilon {
			level := "warn"
			if c.InterruptionRate > interruptPoor {
				level = "fail"
			}
			flag(level, fmt.Sprintf("talked over the caller in %s of %d turns, against %s at the baseline",
				pctTenth(c.InterruptionRate), c.Turns, pctTenth(bc.InterruptionRate)))
		}
	}
	return q
}

func dropLevel(drop float64) string {
	switch {
	case drop > taskDropFail+rateEpsilon:
		return "fail"
	case drop > taskDropWarn+rateEpsilon:
		return "warn"
	}
	return ""
}

func nodeNamed(nodes []NodeStats, id string) (NodeStats, bool) {
	for _, n := range nodes {
		if n.Node == id {
			return n, true
		}
	}
	return NodeStats{}, false
}

// judgedRate is the share of a step's graded calls that met every criterion,
// and how many were graded.
func judgedRate(calls []judge.CallScore, step string) (float64, int) {
	passed, n := 0, 0
	for _, c := range calls {
		if c.Step != step || !c.Judged {
			continue
		}
		n++
		if c.Met {
			passed++
		}
	}
	if n == 0 {
		return 0, 0
	}
	return float64(passed) / float64(n), n
}

func pctWhole(v float64) string { return fmt.Sprintf("%.0f%%", v*100) }
func pctTenth(v float64) string { return fmt.Sprintf("%.1f%%", v*100) }

// ScoreQuality sets every step's quality verdict against the run's baseline
// step. It runs when a run is scored, and again whenever the judge's verdicts
// arrive or change, since task success is one of the checks.
func (rep *Report) ScoreQuality() {
	base := rep.baselineStep()
	rep.scoreQualityAgainst(base)
	// Every cohort of a matrix is held to the clean cohort's baseline, as its
	// latency is.
	if rep.Matrix != nil {
		for i := range rep.Matrix.Cohorts {
			steps := rep.Matrix.Cohorts[i].Steps
			for j := range steps {
				steps[j].Quality = qualityOf(steps[j], base, nil)
			}
		}
	}
}

func (rep *Report) scoreQualityAgainst(base StepReport) {
	var judged []judge.CallScore
	if rep.Judge != nil {
		judged = rep.Judge.Calls
	}
	for i := range rep.Steps {
		rep.Steps[i].Quality = qualityOf(rep.Steps[i], base, judged)
	}
}

// baselineStep is the run's baseline step, or an empty one when it has none,
// against which only failed turns can be scored.
func (rep *Report) baselineStep() StepReport {
	for _, s := range rep.Steps {
		if s.Name == rep.Baseline {
			return s
		}
	}
	return StepReport{}
}

// QualityBreakpoint is the first step whose quality verdict is fail, or nil:
// the load at which the agent stopped doing the job as well as it did at the
// baseline, whether or not it had slowed down.
func (rep *Report) QualityBreakpoint() *StepReport {
	for i := range rep.Steps {
		if rep.Steps[i].Quality.Verdict == "fail" {
			return &rep.Steps[i]
		}
	}
	return nil
}
