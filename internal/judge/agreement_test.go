package judge

import "testing"

// Agreement is measured only where both sides reached a verdict on the same
// call and criterion, and every disagreement is listed with the judge's quote.
func TestAgreeComparesOnlyVerdictsBothSidesReached(t *testing.T) {
	const c1, c2 = "asks for the order number", "confirms a refund"
	judged := []Judgement{
		{Step: "baseline", RequestID: "a", Outcomes: []Outcome{{Criterion: c1, Met: true}, {Criterion: c2, Met: false, Evidence: "turn 3 assistant: \"I can send a replacement.\""}}},
		{Step: "baseline", RequestID: "b", Outcomes: []Outcome{{Criterion: c1, Met: true}, {Criterion: c2, Met: true}}},
		{Step: "c5", RequestID: "c", Err: "groq rate limit"},
	}
	labels := []Judgement{
		{Step: "baseline", RequestID: "a", Outcomes: []Outcome{{Criterion: c1, Met: true}, {Criterion: c2, Met: true}}},
		{Step: "baseline", RequestID: "b", Outcomes: []Outcome{{Criterion: c1, Met: true}, {Criterion: c2, Met: true}}},
		{Step: "c5", RequestID: "c", Outcomes: []Outcome{{Criterion: c1, Met: false}}},
		{Step: "c5", RequestID: "unjudged", Outcomes: []Outcome{{Criterion: c1, Met: true}}},
	}

	a := Agree(judged, labels)
	if a.Compared != 4 || a.Agreed != 3 {
		t.Fatalf("compared %d agreed %d, want 4 and 3", a.Compared, a.Agreed)
	}
	if r := a.Rate(); r != 0.75 {
		t.Errorf("rate %.2f, want 0.75", r)
	}
	if len(a.Criteria) != 2 || a.Criteria[0].Agreed != 2 || a.Criteria[1].Compared != 2 || a.Criteria[1].Agreed != 1 {
		t.Errorf("per criterion %+v, want %s 2/2 and %s 1/2", a.Criteria, c1, c2)
	}
	if len(a.Disagreements) != 1 {
		t.Fatalf("%d disagreements, want 1", len(a.Disagreements))
	}
	if d := a.Disagreements[0]; d.RequestID != "a" || d.Criterion != c2 || d.Judge || !d.Human || d.Evidence == "" {
		t.Errorf("disagreement %+v", d)
	}
	if (Agreement{}).Rate() != 0 {
		t.Error("nothing compared should not read as agreement")
	}
}
