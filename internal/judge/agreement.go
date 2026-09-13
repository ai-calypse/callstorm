package judge

// AgreementTarget is how often a judge should reach the verdict a person
// reached, on the same conversations, before its pass rates are trusted. Below
// it, the disagreements are where a criterion or the prompt needs work.
const AgreementTarget = 0.9

// Agreement is how often the judge reached the verdict a person did, on the
// conversations both of them read.
type Agreement struct {
	Compared      int                  `json:"compared"`
	Agreed        int                  `json:"agreed"`
	Criteria      []CriterionAgreement `json:"criteria"`
	Disagreements []Disagreement       `json:"disagreements,omitempty"`
}

// CriterionAgreement is the agreement on one criterion. A judge that agrees
// overall can still be wrong about one criterion every time, and that
// criterion is the one to rewrite.
type CriterionAgreement struct {
	Criterion string `json:"criterion"`
	Compared  int    `json:"compared"`
	Agreed    int    `json:"agreed"`
}

// Disagreement is one verdict the judge and the person reached differently,
// with the judge's quote, so it can be settled by reading the turn.
type Disagreement struct {
	Step      string `json:"step,omitempty"`
	RequestID string `json:"request_id,omitempty"`
	Criterion string `json:"criterion"`
	Judge     bool   `json:"judge"`
	Human     bool   `json:"human"`
	Evidence  string `json:"evidence"`
}

// Rate is the share of compared verdicts the two agreed on.
func (a Agreement) Rate() float64 {
	if a.Compared == 0 {
		return 0
	}
	return float64(a.Agreed) / float64(a.Compared)
}

// Agree compares the judge's verdicts with a person's labels on the same calls.
//
// Labels are judgements in the same shape, so a person makes them by copying a
// run's judgements file and correcting the verdicts they disagree with. A
// verdict that either side could not reach is left out rather than counted
// against the judge, and so is a criterion the labels do not name.
func Agree(judged, labels []Judgement) Agreement {
	human := map[string]bool{}
	for _, l := range labels {
		if l.Err != "" {
			continue
		}
		for _, o := range l.Outcomes {
			human[l.Step+"/"+l.RequestID+"/"+o.Criterion] = o.Met
		}
	}

	var a Agreement
	idx := map[string]int{}
	for _, j := range judged {
		if j.Err != "" {
			continue
		}
		for _, o := range j.Outcomes {
			h, ok := human[j.Step+"/"+j.RequestID+"/"+o.Criterion]
			if !ok {
				continue
			}
			i, seen := idx[o.Criterion]
			if !seen {
				i = len(a.Criteria)
				idx[o.Criterion] = i
				a.Criteria = append(a.Criteria, CriterionAgreement{Criterion: o.Criterion})
			}
			a.Compared++
			a.Criteria[i].Compared++
			if h == o.Met {
				a.Agreed++
				a.Criteria[i].Agreed++
				continue
			}
			a.Disagreements = append(a.Disagreements, Disagreement{
				Step: j.Step, RequestID: j.RequestID, Criterion: o.Criterion,
				Judge: o.Met, Human: h, Evidence: o.Evidence,
			})
		}
	}
	return a
}
