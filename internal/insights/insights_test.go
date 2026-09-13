package insights

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
)

// A number a reader is given must be one the data holds -- as stored, rounded,
// or in the units it is read in -- and anything else is flagged.
func TestUncheckedFlagsNumbersTheDataDoesNotHold(t *testing.T) {
	facts := factsOf([]byte(`{"name":"c40","ttfa":{"p95_ms":2544.3},"wer":{"mean":0.0361},
		"transport_rtt":{"p50_ms":81.4},"turn4":{"ttfa_p50_ms":3166.2},"ratio":2.21}`))

	for _, s := range []string{"2544ms", "2,544 ms", "3.6%", "81ms", "3.2 s", "step c40", "p95", "2.21×", "3 of 5 parts"} {
		if bad := unchecked(s, facts); len(bad) > 0 {
			t.Errorf("%q flagged %v, but the data holds it", s, bad)
		}
	}
	for _, s := range []string{"2600ms", "4%", "95ms", "2.9 s", "3.1×"} {
		if bad := unchecked(s, facts); len(bad) == 0 {
			t.Errorf("%q passed, but the data does not hold it", s)
		}
	}
}

type stubModel struct {
	reply   string
	gotUser string
}

func (s *stubModel) Generate(_ context.Context, _, user string, _ json.RawMessage) (string, error) {
	s.gotUser = user
	return s.reply, nil
}

// An insight carrying an invented number, or no evidence, is dropped and
// recorded as dropped; the rest are kept as written.
func TestGenerateKeepsOnlyWhatTheDataSupports(t *testing.T) {
	d := Digest{Kind: "run", Subject: "r1", Runs: []RunDigest{{
		ID:     "r1",
		Report: map[string]any{"scenario": "refund", "steps": []any{map[string]any{"name": "c5", "ttfa": map[string]any{"p95_ms": 2544.0}}}},
	}}}
	m := &stubModel{reply: `{
		"insights": [
			{"title": "Slow at c5", "category": "latency", "severity": "high",
			 "evidence": ["refund, step c5: ttfa p95_ms 2544"], "finding": "The slowest callers waited 2544ms.",
			 "why_it_matters": "Callers notice.", "next_step": "Rerun the step."},
			{"title": "Invented", "category": "latency", "severity": "low",
			 "evidence": ["refund, step c5: ttfa p95_ms 1830"], "finding": "It waited 1830ms.",
			 "why_it_matters": "x", "next_step": "y"},
			{"title": "Unsupported", "category": "quality", "severity": "low",
			 "evidence": [], "finding": "It sounded fine.", "why_it_matters": "x", "next_step": "y"}
		],
		"headline": "It is 7400ms slow."
	}`}

	r, err := Generate(context.Background(), m, "test-model", d)
	if err != nil {
		t.Fatal(err)
	}
	if len(r.Insights) != 1 || r.Insights[0].Title != "Slow at c5" {
		t.Fatalf("kept %+v, want only the supported insight", r.Insights)
	}
	if len(r.Rejected) != 3 {
		t.Fatalf("rejected %+v, want the invented number, the missing evidence and the headline", r.Rejected)
	}
	if r.Headline != "" {
		t.Errorf("headline %q kept with a number the data does not hold", r.Headline)
	}
	if !strings.Contains(r.Rejected[0].Reason, "7400") || !strings.Contains(r.Rejected[1].Reason, "1830") {
		t.Errorf("reasons do not name the numbers: %+v", r.Rejected)
	}
	if r.Model != "test-model" || r.DataHash == "" || r.PromptHash == "" || len(r.Runs) != 1 {
		t.Errorf("provenance missing: %+v", r)
	}
	if !strings.HasPrefix(m.gotUser, "DATA\n") || !strings.Contains(m.gotUser, `"p95_ms":2544`) {
		t.Errorf("the model was not given the data: %.120s", m.gotUser)
	}
}

// The digest trims what a reader does not need, and adds the per-turn view
// from the calls log that no report table has.
func TestDigestTrimsTheReportAndAddsTurnsFromTheCallsLog(t *testing.T) {
	dir := t.TempDir()
	write := func(name, body string) {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("20260913-100000-p.json", `{"profile":"p","scenario":"refund","suite":"s1","scenario_hash":"abc123",
		"steps":[{"name":"c1","timeline":[{"at":"x"}],"ttfa":{"p95_ms":900}}],
		"judge":{"judged":2,"passed":2,"judgements":[{"step":"c1"}],"calls":[{"step":"c1"}]}}`)
	write("20260913-100000-p-calls.jsonl",
		`{"step":"c1","turns":[{"turn":1,"caller_text":"hi","agent_text":"hello","ttfa_ms":700,"endpointing_ms":300,"think_speak_ms":400},{"turn":2,"caller_text":"thanks","agent_text":"bye","ttfa_ms":3100,"endpointing_ms":240,"think_speak_ms":2860}]}
{"step":"c1","turns":[{"turn":1,"caller_text":"hi","agent_text":"hello","ttfa_ms":720,"endpointing_ms":310,"think_speak_ms":410},{"turn":2,"caller_text":"thanks","agent_text":"goodbye","ttfa_ms":3300,"endpointing_ms":250,"think_speak_ms":3050}]}
{"step":"c1","error":"dial: refused","turns":[]}
`)
	write("20260913-090000-q.json", `{"profile":"q","scenario":"other","suite":"s2","steps":[]}`)
	write("20260913-100000-p-judgements.json", `[]`)

	d, err := ForSuite(dir, "s1")
	if err != nil {
		t.Fatal(err)
	}
	if len(d.Runs) != 1 || d.Kind != "suite" || d.Subject != "s1" {
		t.Fatalf("suite digest %+v, want the one run of s1", d)
	}
	rd := d.Runs[0]
	step := rd.Report["steps"].([]any)[0].(map[string]any)
	if _, ok := step["timeline"]; ok {
		t.Error("timeline kept")
	}
	if j := rd.Report["judge"].(map[string]any); j["judgements"] != nil || j["calls"] != nil {
		t.Error("judged transcripts kept")
	}
	if _, ok := rd.Report["scenario_hash"]; ok {
		t.Error("fingerprint kept: its hex digits would count as facts")
	}

	cs := rd.Calls
	if cs == nil || cs.Calls != 3 || cs.Failed != 1 || len(cs.Errors) != 1 || len(cs.ByTurn) != 2 {
		t.Fatalf("call stats %+v", cs)
	}
	t2 := cs.ByTurn[1]
	if t2.Turn != 2 || t2.Count != 2 || t2.TTFAP50Ms != 3100 || t2.TTFAP95Ms != 3300 || t2.ThinkSpeakP50Ms != 2860 || t2.CallerLine != "thanks" {
		t.Errorf("turn 2 %+v", t2)
	}
	// A tie between replies resolves the same way every time.
	if t2.CommonReply != "bye" || t2.CommonReplyCount != 1 {
		t.Errorf("turn 2 reply %q x%d, want bye x1", t2.CommonReply, t2.CommonReplyCount)
	}

	suites, loose := Suites(dir)
	if strings.Join(suites, ",") != "s1,s2" || len(loose) != 0 {
		t.Errorf("suites %v loose %v", suites, loose)
	}
}

// Gemini is asked for JSON matching the schema, a rate limit is waited out,
// and a thought part is not mistaken for the answer.
func TestGeminiWaitsOutARateLimitAndReturnsTheAnswer(t *testing.T) {
	var calls atomic.Int32
	var gotKey, gotPath string
	var gotBody map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if calls.Add(1) == 1 {
			w.WriteHeader(http.StatusTooManyRequests)
			_, _ = w.Write([]byte(`{"error":{"message":"quota","details":[{"@type":"type.googleapis.com/google.rpc.RetryInfo","retryDelay":"0.01s"}]}}`))
			return
		}
		gotKey, gotPath = r.Header.Get("x-goog-api-key"), r.URL.Path
		_ = json.NewDecoder(r.Body).Decode(&gotBody)
		_, _ = w.Write([]byte(`{"candidates":[{"content":{"parts":[{"text":"thinking...","thought":true},{"text":"{\"insights\":[],\"headline\":\"ok\"}"}]},"finishReason":"STOP"}]}`))
	}))
	defer srv.Close()

	got, err := Gemini{APIKey: "k", Model: "gemini-test", BaseURL: srv.URL}.Generate(context.Background(), "sys", "user", schema)
	if err != nil {
		t.Fatal(err)
	}
	if got != `{"insights":[],"headline":"ok"}` {
		t.Errorf("reply %q", got)
	}
	if calls.Load() != 2 || gotKey != "k" || gotPath != "/models/gemini-test:generateContent" {
		t.Errorf("calls %d key %q path %q", calls.Load(), gotKey, gotPath)
	}
	gc, _ := gotBody["generationConfig"].(map[string]any)
	if gc["responseMimeType"] != "application/json" || gc["responseSchema"] == nil || gotBody["systemInstruction"] == nil {
		t.Errorf("request did not ask for schema-constrained JSON: %v", gotBody)
	}
}
