package judge

import (
	"math"
	"testing"
)

func score(wer float64, met bool) CallScore {
	return CallScore{WER: wer, RefWords: 20, Met: met, Judged: true}
}

func TestCorrelateSplitsCleanFromMisheard(t *testing.T) {
	// Eight clean calls, all passing; four misheard, one passing. If
	// mishearing costs the task, this is what it looks like.
	var scores []CallScore
	for i := 0; i < 8; i++ {
		scores = append(scores, score(0.02, true))
	}
	scores = append(scores, score(0.30, true))
	for i := 0; i < 3; i++ {
		scores = append(scores, score(0.30, false))
	}

	c := Correlate(scores)

	if c.Calls != 12 {
		t.Fatalf("Calls = %d, want 12", c.Calls)
	}
	if c.CleanCalls != 8 || c.MisheardCalls != 4 {
		t.Errorf("split = %d clean / %d misheard, want 8 / 4", c.CleanCalls, c.MisheardCalls)
	}
	if c.CleanPassRate != 1.0 {
		t.Errorf("CleanPassRate = %.2f, want 1.00", c.CleanPassRate)
	}
	if c.MisheardPassRate != 0.25 {
		t.Errorf("MisheardPassRate = %.2f, want 0.25", c.MisheardPassRate)
	}
	if math.Abs(c.Gap-0.75) > 1e-9 {
		t.Errorf("Gap = %.2f, want 0.75", c.Gap)
	}
	if !c.Conclusive {
		t.Errorf("Conclusive = false with both groups populated and %d calls", c.Calls)
	}
	// The median view has to agree with the threshold view, or one of them is
	// measuring something else.
	if c.MedianWERFailed <= c.MedianWERPassed {
		t.Errorf("median WER failed %.2f should exceed passed %.2f",
			c.MedianWERFailed, c.MedianWERPassed)
	}
}

// The dangerous output is a confident gap computed from three calls, so a
// small sample reports its numbers and withholds the conclusion.
func TestCorrelateWithholdsAConclusionOnThinData(t *testing.T) {
	c := Correlate([]CallScore{score(0.01, true), score(0.40, false), score(0.02, true)})

	if c.Conclusive {
		t.Errorf("Conclusive = true on %d calls, below the %d minimum", c.Calls, minCorrelationCalls)
	}
	if c.Note == "" {
		t.Errorf("no note explaining why the comparison was withheld")
	}
	// The measurements still come through; only the invitation to read a
	// finding into them is removed.
	if c.CleanCalls != 2 || c.MisheardCalls != 1 {
		t.Errorf("split = %d / %d, want 2 / 1", c.CleanCalls, c.MisheardCalls)
	}
}

func TestCorrelateWithholdsAConclusionWhenOneGroupIsEmpty(t *testing.T) {
	var scores []CallScore
	for i := 0; i < 10; i++ {
		scores = append(scores, score(0.01, i%2 == 0))
	}
	c := Correlate(scores)

	if c.Conclusive {
		t.Errorf("Conclusive = true with no misheard calls to compare against")
	}
	if c.Gap != 0 {
		t.Errorf("Gap = %.2f, want 0 when one group is empty", c.Gap)
	}
	if c.CleanPassRate != 0.5 {
		t.Errorf("CleanPassRate = %.2f, want 0.50", c.CleanPassRate)
	}
}

// A call the judge could not grade, or that the target never transcribed, is
// missing data. Counting either as a failure would blame the agent for the
// harness.
func TestCorrelateIgnoresUngradedAndUntranscribedCalls(t *testing.T) {
	scores := []CallScore{
		score(0.01, true),
		{WER: 0.9, RefWords: 20, Met: false, Judged: false}, // judge errored
		{WER: 0, RefWords: 0, Met: false, Judged: true},     // no transcript
	}
	c := Correlate(scores)

	if c.Calls != 1 {
		t.Errorf("Calls = %d, want 1: only one call had both a verdict and a transcript", c.Calls)
	}
	if c.CleanPassRate != 1.0 {
		t.Errorf("CleanPassRate = %.2f, want 1.00", c.CleanPassRate)
	}
}
