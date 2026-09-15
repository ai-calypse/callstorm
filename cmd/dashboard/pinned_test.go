package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestCollectMarksPinnedSuitesAndRuns(t *testing.T) {
	dir := t.TempDir()
	for name, body := range map[string]string{
		"a.json":     `{"profile":"p","suite":"copy","started_at":"2026-09-13T16:00:00Z"}`,
		"b.json":     `{"profile":"p","suite":"newer","started_at":"2026-09-14T16:00:00Z"}`,
		"c.json":     `{"profile":"p","started_at":"2026-09-12T16:00:00Z"}`,
		"pinned.txt": "# shown first\ncopy\n\nc  # a lone run\n",
	} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	runs := collect(dir)
	got := map[string]bool{}
	for _, s := range runs {
		got[s.ID] = s.Pinned
	}
	if len(runs) != 3 || !got["a"] || got["b"] || !got["c"] {
		t.Fatalf("pinned = %v, want a and c of 3 runs", got)
	}
	// Pinning marks runs; the order stays newest first by start time.
	if runs[0].ID != "b" {
		t.Fatalf("first run = %s, want b", runs[0].ID)
	}
}
