package loadgen

import (
	"sort"

	"github.com/yakshgandhi/callstorm/internal/judge"
)

// TaskSuccess is what the judge made of the run's conversations, folded into
// the report rather than left in a file beside it.
//
// It lives on the report because the question it exists to answer -- did
// mishearing the caller cost the agent the task -- needs both halves at once:
// the transcripts the load generator collected and the verdicts the judge
// returned. While the verdicts lived only in their own artifact, the two
// numbers that mean nothing apart were never in the same place, and anything
// reading the report and nothing else could not show the join at all.
type TaskSuccess struct {
	// Backend names the grader. A verdict is only as readable as the thing
	// that produced it, and a run graded by a small fast model and one graded
	// by a large one are not the same evidence.
	Backend  string   `json:"backend"`
	Criteria []string `json:"criteria"`

	// Judged counts conversations the judge returned a verdict on; Errored
	// counts the ones it could not grade at all. They are separate because
	// "the agent got it wrong" and "we could not tell" are different results,
	// and collapsing them lets a broken judge read as a failing agent.
	Judged  int `json:"judged"`
	Passed  int `json:"passed"`
	Errored int `json:"errored"`

	// Misses counts, per criterion, how many judged conversations failed it.
	// One criterion failing everywhere is a different problem from a different
	// criterion failing each time, and a single "3 of 4 passed" hides which.
	Misses []CriterionMiss `json:"misses,omitempty"`

	// Calls is every judged conversation reduced to the two things being
	// compared, so a reader can see the points the correlation is drawn from
	// instead of taking the summary on faith.
	Calls []judge.CallScore `json:"calls"`

	Correlation judge.Correlation `json:"correlation"`

	// Judgements keeps the judge's own words, quote and all. It is what makes
	// a verdict auditable: a failed criterion with the line that failed it
	// beside it can be argued with, and one without it cannot.
	Judgements []judge.Judgement `json:"judgements,omitempty"`
}

// CriterionMiss is one success criterion and how often the agent missed it.
type CriterionMiss struct {
	Criterion string `json:"criterion"`
	Missed    int    `json:"missed"`
	Judged    int    `json:"judged"`

	// Evidence is the judge's quote from one conversation that missed it. A
	// count says how often; this says what it looked like.
	Evidence string `json:"evidence,omitempty"`
}

// SummarizeJudgements joins the judge's verdicts back onto the calls they
// graded and computes the correlation between the two.
//
// The join is on step plus request ID because neither is unique alone: a
// request ID identifies a call, but a run places calls under several steps and
// nothing stops a target reusing one.
func SummarizeJudgements(calls []CallRecord, js []judge.Judgement, backend string, criteria []string) *TaskSuccess {
	if len(js) == 0 {
		return nil
	}

	t := &TaskSuccess{
		Backend:    backend,
		Criteria:   criteria,
		Judgements: js,
	}

	verdict := make(map[string]judge.Judgement, len(js))
	for _, g := range js {
		verdict[g.Step+"/"+g.RequestID] = g
		if g.Err != "" {
			t.Errored++
			continue
		}
		t.Judged++
		if g.Met() {
			t.Passed++
		}
	}

	// Per-criterion tallies, kept in the order the scenario listed them rather
	// than sorted by failure count: a reader comparing two runs wants the same
	// row in the same place.
	idx := map[string]int{}
	for _, c := range criteria {
		idx[c] = len(t.Misses)
		t.Misses = append(t.Misses, CriterionMiss{Criterion: c})
	}
	for _, g := range js {
		if g.Err != "" {
			continue
		}
		for _, o := range g.Outcomes {
			i, ok := idx[o.Criterion]
			if !ok {
				// A judge that reworded the criterion still graded something,
				// so it is counted rather than dropped.
				idx[o.Criterion] = len(t.Misses)
				t.Misses = append(t.Misses, CriterionMiss{Criterion: o.Criterion})
				i = idx[o.Criterion]
			}
			m := &t.Misses[i]
			m.Judged++
			if !o.Met {
				m.Missed++
				if m.Evidence == "" {
					m.Evidence = o.Evidence
				}
			}
		}
	}

	for _, c := range calls {
		g, ok := verdict[c.Step+"/"+c.RequestID]
		if !ok {
			continue
		}
		// Per call, errors are totalled over words rather than averaged over
		// turns, matching the step-level rate: averaging per-turn rates would
		// let a three-word turn weigh as heavily as a thirty-word one.
		var errs, words int
		for _, turn := range c.Turns {
			if turn.HeardText == "" {
				continue
			}
			w := judge.Score(turn.CallerText, turn.HeardText)
			errs += w.Substitutions + w.Deletions + w.Insertions
			words += w.RefWords
		}
		s := judge.CallScore{
			Step: c.Step, RequestID: c.RequestID,
			RefWords: words, Met: g.Met(), Judged: g.Err == "",
		}
		if words > 0 {
			s.WER = float64(errs) / float64(words)
		}
		t.Calls = append(t.Calls, s)
	}

	// Sorted by transcript accuracy because that is the axis the correlation
	// is drawn on: a reader scanning the list sees the cleanly-heard calls
	// together and the misheard ones together, which is the comparison.
	sort.SliceStable(t.Calls, func(i, j int) bool { return t.Calls[i].WER < t.Calls[j].WER })

	t.Correlation = judge.Correlate(t.Calls)
	return t
}
