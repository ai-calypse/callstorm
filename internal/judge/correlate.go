package judge

import "sort"

// cleanWER is the line between an agent that heard the caller and one that did
// not. Five percent is the usual published bar for usable speech recognition,
// and it is a threshold rather than a target: below it the occasional wrong
// word is noise, above it the agent is regularly answering a question nobody
// asked.
const cleanWER = 0.05

// minCorrelationCalls is the fewest judged conversations worth splitting into
// two groups. Below this a single call flips a pass rate by twenty points or
// more, and a number that unstable invites a conclusion the data cannot carry.
const minCorrelationCalls = 8

// CallScore is one judged conversation reduced to the two things being
// compared: how badly the agent misheard the caller, and whether it did the
// job it was called to do.
type CallScore struct {
	Step      string  `json:"step"`
	RequestID string  `json:"request_id,omitempty"`
	WER       float64 `json:"wer"`

	// RefWords is how many words the rate was computed over. A call of two
	// words is not evidence about anything, and the denominator is what lets a
	// reader see that.
	RefWords int `json:"ref_words"`

	// Met is the judge's verdict. Judged is false when the judge errored,
	// which is kept apart from a failing agent: "it got it wrong" and "we
	// could not tell" are different results.
	Met    bool `json:"met"`
	Judged bool `json:"judged"`
}

// Correlation answers one question: does mishearing the caller actually cost
// the agent the task?
//
// It is the join that makes word error rate worth measuring. On its own a WER
// figure is an accuracy statistic nobody has to act on; set against task
// outcomes it either shows that the calls the agent misheard are the calls it
// failed, which makes the transcript the thing to fix, or it shows no such
// thing, which means the failures are somewhere else and the WER number is a
// distraction.
//
// This reports an association and nothing stronger. The calls were not
// assigned to be misheard; the same load that degrades recognition degrades
// everything else at the same time.
type Correlation struct {
	// Calls counts conversations that were both judged and transcribed.
	Calls     int     `json:"calls"`
	Threshold float64 `json:"threshold"`

	CleanCalls  int `json:"clean_calls"`
	CleanPassed int `json:"clean_passed"`

	MisheardCalls  int `json:"misheard_calls"`
	MisheardPassed int `json:"misheard_passed"`

	CleanPassRate    float64 `json:"clean_pass_rate"`
	MisheardPassRate float64 `json:"misheard_pass_rate"`

	// Gap is the clean pass rate minus the misheard one, in the same 0-1
	// units. Positive means the calls the agent heard correctly are the calls
	// it completed.
	Gap float64 `json:"gap"`

	// MedianWERPassed and MedianWERFailed come at the same question from the
	// other side, and need no threshold to be chosen. If the failures were
	// caused by mishearing, the failed calls are the worse-heard ones.
	MedianWERPassed float64 `json:"median_wer_passed"`
	MedianWERFailed float64 `json:"median_wer_failed"`

	// Conclusive is false when there were too few judged calls for the split
	// to mean anything, or when every call landed on one side of the
	// threshold. The numbers are still reported; what is withheld is the
	// invitation to read a finding into them.
	Conclusive bool   `json:"conclusive"`
	Note       string `json:"note,omitempty"`
}

// Correlate splits judged calls by transcript accuracy and compares how often
// each group completed the task.
func Correlate(scores []CallScore) Correlation {
	c := Correlation{Threshold: cleanWER}

	var passedWER, failedWER []float64
	for _, s := range scores {
		// A call the judge could not grade, or the target never transcribed,
		// belongs in neither group. Counting it as a failure would blame the
		// agent for the harness's missing data.
		if !s.Judged || s.RefWords == 0 {
			continue
		}
		c.Calls++

		if s.WER <= cleanWER {
			c.CleanCalls++
			if s.Met {
				c.CleanPassed++
			}
		} else {
			c.MisheardCalls++
			if s.Met {
				c.MisheardPassed++
			}
		}

		if s.Met {
			passedWER = append(passedWER, s.WER)
		} else {
			failedWER = append(failedWER, s.WER)
		}
	}

	if c.CleanCalls > 0 {
		c.CleanPassRate = float64(c.CleanPassed) / float64(c.CleanCalls)
	}
	if c.MisheardCalls > 0 {
		c.MisheardPassRate = float64(c.MisheardPassed) / float64(c.MisheardCalls)
	}
	if c.CleanCalls > 0 && c.MisheardCalls > 0 {
		c.Gap = c.CleanPassRate - c.MisheardPassRate
	}
	c.MedianWERPassed = median(passedWER)
	c.MedianWERFailed = median(failedWER)

	switch {
	case c.Calls < minCorrelationCalls:
		c.Note = "too few judged calls to compare groups"
	case c.MisheardCalls == 0:
		c.Note = "every judged call was heard cleanly, so there is nothing to compare it against"
	case c.CleanCalls == 0:
		c.Note = "every judged call was misheard, so there is no clean group to compare against"
	default:
		c.Conclusive = true
	}
	return c
}

func median(vs []float64) float64 {
	if len(vs) == 0 {
		return 0
	}
	s := append([]float64(nil), vs...)
	sort.Float64s(s)
	mid := len(s) / 2
	if len(s)%2 == 1 {
		return s[mid]
	}
	return (s[mid-1] + s[mid]) / 2
}
