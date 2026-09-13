package insights

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
)

func repeatDigest() Digest {
	return Digest{Kind: "run", Subject: "r1", Runs: []RunDigest{{
		ID: "20260913-191409-sweep-deepgram-30",
		Report: map[string]any{"steps": []any{map[string]any{
			"name": "c40", "ttfa": map[string]any{"p95_ms": 3355.2, "p50_ms": 747.8, "max_ms": 5526.1},
		}}},
	}}}
}

// The Deepgram suite's graph-reference analysis cited step c40's p95 in two
// insights, written two ways. The second, and one reusing a title, are dropped
// and listed as repeats of the insight they repeat.
func TestGenerateDropsAnInsightRepeatingAnEarlierOne(t *testing.T) {
	m := &stubModel{reply: `{"insights": [
		{"title": "Closing turn is slow", "category": "latency", "severity": "high",
		 "evidence": ["20260913-191409-sweep-deepgram-30, step c40: ttfa p95_ms 3355.2"],
		 "finding": "f", "why_it_matters": "w", "next_step": "n"},
		{"title": "Tail is skewed", "category": "latency", "severity": "high",
		 "evidence": ["step c40, ttfa_p95_ms 3355.2"],
		 "finding": "f", "why_it_matters": "w", "next_step": "n"},
		{"title": "Median holds", "category": "latency", "severity": "low",
		 "evidence": ["step c40: ttfa p50_ms 747.8"],
		 "finding": "f", "why_it_matters": "w", "next_step": "n"},
		{"title": "Closing turn is slow!", "category": "latency", "severity": "low",
		 "evidence": ["step c40: ttfa max_ms 5526.1"],
		 "finding": "f", "why_it_matters": "w", "next_step": "n"}
	], "headline": "h"}`}

	r, err := Generate(context.Background(), m, "test-model", repeatDigest())
	if err != nil {
		t.Fatal(err)
	}
	if len(r.Insights) != 2 || r.Insights[0].Title != "Closing turn is slow" || r.Insights[1].Title != "Median holds" {
		t.Fatalf("kept %+v, want the first and the median", r.Insights)
	}
	if len(r.Rejected) != 2 {
		t.Fatalf("rejected %+v, want both repeats", r.Rejected)
	}
	for _, rj := range r.Rejected {
		if !strings.HasPrefix(rj.Reason, `repeats "Closing turn is slow"`) {
			t.Errorf("%s: reason %q, want it named as a repeat of the first", rj.Title, rj.Reason)
		}
	}
}

// An analysis already written by the same model, from the same instructions and
// data, is not asked for again.
func TestUnchangedKnowsAnAnalysisAlreadyWritten(t *testing.T) {
	d := repeatDigest()
	r, err := Generate(context.Background(), &stubModel{reply: `{"insights": [], "headline": "h"}`}, "m1", d)
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	path := filepath.Join(dir, "r1-insights.json")
	if err := r.Write(path); err != nil {
		t.Fatal(err)
	}
	if !Unchanged(path, "m1", d) {
		t.Error("the same model, instructions and data read as changed")
	}
	if Unchanged(path, "m2", d) {
		t.Error("a different model read as unchanged")
	}
	changed := repeatDigest()
	changed.Subject = "r2"
	if Unchanged(path, "m1", changed) {
		t.Error("different data read as unchanged")
	}
	if Unchanged(filepath.Join(dir, "missing-insights.json"), "m1", d) {
		t.Error("an analysis never written read as unchanged")
	}
}
