package main

import (
	"path/filepath"
	"testing"

	"github.com/yakshgandhi/callstorm/internal/loadgen"
	"github.com/yakshgandhi/callstorm/internal/metrics"
)

// A run judged later is judged from its calls file, so reading that file back
// has to return the conversations the judge would have seen at the time --
// including a failed call, which sampling must still be able to skip.
func TestReadCallsReadsWhatWriteCallsWrote(t *testing.T) {
	path := filepath.Join(t.TempDir(), "run-calls.jsonl")
	want := []loadgen.CallRecord{
		{Step: "baseline", RequestID: "a", Turns: []metrics.TurnMetric{
			{Turn: 1, CallerText: "four four eight one two", HeardText: "four four eight two", AgentText: "Thanks."},
		}},
		{Step: "c5", RequestID: "b", Error: "dial: refused"},
	}
	if err := writeCalls(path, want); err != nil {
		t.Fatal(err)
	}

	got, err := readCalls(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != len(want) {
		t.Fatalf("read %d calls, wrote %d", len(got), len(want))
	}
	if g := got[0]; g.Step != "baseline" || g.RequestID != "a" || len(g.Turns) != 1 ||
		g.Turns[0].CallerText != "four four eight one two" || g.Turns[0].HeardText != "four four eight two" ||
		g.Turns[0].AgentText != "Thanks." {
		t.Errorf("first call came back as %+v", g)
	}
	if got[1].Error != "dial: refused" || len(got[1].Turns) != 0 {
		t.Errorf("failed call came back as %+v", got[1])
	}
	if n := len(sampleCalls(got, 0)); n != 1 {
		t.Errorf("sampled %d calls from the file, want only the one that connected", n)
	}
}
