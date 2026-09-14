// Package insights writes a plain-language analysis of a run, or of a suite of
// runs, and keeps only what the data supports.
//
// The report card and the rule-based answers say what each number is. What they
// cannot do is notice that one turn of the conversation is four times slower
// than the rest, that a slowdown came and went regardless of load, or that one
// scenario reaches its limit long before the others. A language model reading
// the whole run can. It can also invent a number that reads exactly like a
// measured one, so every number it writes is checked against the data it was
// given, and a claim carrying a number the data does not hold is dropped and
// recorded as dropped.
package insights

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"
	"unicode"
)

// Insight is one finding, with the evidence it rests on.
type Insight struct {
	Title        string   `json:"title"`
	Category     string   `json:"category"`
	Severity     string   `json:"severity"`
	Evidence     []string `json:"evidence"`
	Finding      string   `json:"finding"`
	WhyItMatters string   `json:"why_it_matters"`
	NextStep     string   `json:"next_step"`
}

// Rejected is a claim that did not survive the number check, kept so a reader
// can see that something was dropped and why.
type Rejected struct {
	Title  string `json:"title"`
	Reason string `json:"reason"`
}

// Report is the written analysis of one run or one suite, as stored beside it.
type Report struct {
	Kind        string    `json:"kind"`
	Subject     string    `json:"subject"`
	Runs        []string  `json:"runs"`
	GeneratedAt time.Time `json:"generated_at"`
	Model       string    `json:"model"`

	// PromptHash and DataHash say which instructions and which data produced
	// this analysis, so a stale one can be told from a current one.
	PromptHash string `json:"prompt_hash"`
	DataHash   string `json:"data_hash"`

	Headline string     `json:"headline"`
	Insights []Insight  `json:"insights"`
	Rejected []Rejected `json:"rejected,omitempty"`
}

// Model is anything that can answer a prompt with JSON matching a schema.
type Model interface {
	Generate(ctx context.Context, system, user string, schema json.RawMessage) (string, error)
}

// schema constrains the reply. Properties are generated in the order given:
// the evidence before the finding it supports, and the headline after the
// insights it summarises.
var schema = json.RawMessage(`{
  "type": "OBJECT",
  "properties": {
    "insights": {
      "type": "ARRAY",
      "items": {
        "type": "OBJECT",
        "properties": {
          "title":          { "type": "STRING" },
          "category":       { "type": "STRING", "enum": ["latency", "capacity", "quality", "reliability", "test", "cost"] },
          "severity":       { "type": "STRING", "enum": ["high", "medium", "low", "info"] },
          "evidence":       { "type": "ARRAY", "items": { "type": "STRING" } },
          "finding":        { "type": "STRING" },
          "why_it_matters": { "type": "STRING" },
          "next_step":      { "type": "STRING" }
        },
        "required": ["title", "category", "severity", "evidence", "finding", "why_it_matters", "next_step"],
        "propertyOrdering": ["title", "category", "severity", "evidence", "finding", "why_it_matters", "next_step"]
      }
    },
    "headline": { "type": "STRING" }
  },
  "required": ["insights", "headline"],
  "propertyOrdering": ["insights", "headline"]
}`)

// Generate asks the model for an analysis of d and keeps what checks out.
func Generate(ctx context.Context, m Model, modelName string, d Digest) (*Report, error) {
	data, err := json.Marshal(d)
	if err != nil {
		return nil, err
	}
	out, err := m.Generate(ctx, systemPrompt, "DATA\n"+string(data), schema)
	if err != nil {
		return nil, err
	}
	var parsed struct {
		Headline string    `json:"headline"`
		Insights []Insight `json:"insights"`
	}
	if err := json.Unmarshal([]byte(out), &parsed); err != nil {
		return nil, fmt.Errorf("model returned unparseable output: %w", err)
	}

	r := &Report{
		Kind:        d.Kind,
		Subject:     d.Subject,
		Runs:        d.IDs(),
		GeneratedAt: time.Now().UTC(),
		Model:       modelName,
		PromptHash:  shortHash(systemPrompt),
		DataHash:    shortHash(string(data)),
		Insights:    []Insight{},
	}

	facts := factsOf(data)
	if bad := unchecked(parsed.Headline, facts); len(bad) > 0 {
		r.Rejected = append(r.Rejected, Rejected{Title: "headline", Reason: notInData(bad)})
	} else {
		r.Headline = parsed.Headline
	}
	// seen maps each evidence item and title already kept to the insight that
	// kept it. A later insight citing one of them says again what an earlier
	// one said, and the earlier one is the one ordered as more important.
	seen := map[string]string{}
	for _, in := range parsed.Insights {
		if len(in.Evidence) == 0 {
			r.Rejected = append(r.Rejected, Rejected{Title: in.Title, Reason: "cites no evidence"})
			continue
		}
		text := strings.Join(append([]string{in.Title, in.Finding, in.WhyItMatters, in.NextStep}, in.Evidence...), "\n")
		if bad := unchecked(text, facts); len(bad) > 0 {
			r.Rejected = append(r.Rejected, Rejected{Title: in.Title, Reason: notInData(bad)})
			continue
		}
		keys := []string{"title " + plainKey(in.Title)}
		for _, e := range in.Evidence {
			keys = append(keys, plainKey(e))
		}
		if first := repeatOf(keys, seen); first != "" {
			r.Rejected = append(r.Rejected, Rejected{Title: in.Title, Reason: fmt.Sprintf("repeats %q", first)})
			continue
		}
		for _, k := range keys {
			seen[k] = in.Title
		}
		r.Insights = append(r.Insights, in)
	}
	return r, nil
}

func repeatOf(keys []string, seen map[string]string) string {
	for _, k := range keys {
		if first, ok := seen[k]; ok {
			return first
		}
	}
	return ""
}

// runIDRE matches a run id, which the model writes in front of some evidence
// and leaves off other evidence citing the same figure.
var runIDRE = regexp.MustCompile(`\b\d{8}-\d{6}-[a-z0-9-]+`)

// plainKey reduces evidence or a title to what it says, so the same figure
// cited as "step c40: ttfa p95_ms 3355.2" and "step c40, ttfa_p95_ms 3355.2"
// counts as the same citation.
func plainKey(s string) string {
	s = runIDRE.ReplaceAllString(strings.ToLower(s), "")
	s = strings.NewReplacer("turns turn", "turn").Replace(s)
	return strings.Join(strings.FieldsFunc(s, func(r rune) bool {
		return !unicode.IsLetter(r) && !unicode.IsDigit(r) && r != '.' && r != '%'
	}), " ")
}

// Unchanged reports whether the analysis stored at path was written by model
// from exactly these instructions and this data. Writing it again would ask
// the same question twice and put a second, differently worded answer to it on
// the dashboard, so it is not written again.
func Unchanged(path, model string, d Digest) bool {
	b, err := os.ReadFile(path)
	if err != nil {
		return false
	}
	var old Report
	if json.Unmarshal(b, &old) != nil {
		return false
	}
	data, err := json.Marshal(d)
	if err != nil {
		return false
	}
	return old.Model == model && old.PromptHash == shortHash(systemPrompt) && old.DataHash == shortHash(string(data))
}

func notInData(bad []string) string {
	return fmt.Sprintf("cites %s, which the data does not contain", strings.Join(bad, ", "))
}

func shortHash(s string) string {
	sum := sha256.Sum256([]byte(s))
	return hex.EncodeToString(sum[:6])
}

// Write stores the analysis as indented JSON.
func (r *Report) Write(path string) error {
	b, err := json.MarshalIndent(r, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, b, 0o644)
}

// PathForRun is where a run's analysis is stored: beside its report.
func PathForRun(reportPath string) string {
	return strings.TrimSuffix(reportPath, ".json") + "-insights.json"
}

// PathForSuite is where a suite's analysis is stored: beside its parts.
func PathForSuite(dir, suite string) string {
	return filepath.Join(dir, suite+"-insights.json")
}

// systemPrompt tells the model what to write and what it may not do.
const systemPrompt = `You write the analysis section of a load-test report on a voice AI agent: a phone agent that listens to a caller, thinks, and speaks back. Many readers have never tested a voice agent, so write in plain words and explain a term the first time you use it.

DATA is JSON. kind is "run" for one load test, or "suite" for several runs of different conversations under one test. Each run has its report: steps at rising numbers of calls at once, each with percentiles of the wait before the agent starts speaking (ttfa), split into the network round trip (transport_rtt), noticing the caller stopped (endpointing) and thinking up the reply (think_speak), plus quality figures. calls.by_turn gives figures per turn of the conversation from the calls log.

Rules:
1. Use only numbers that appear in DATA, as they appear there, rounded to whole milliseconds or one decimal place. Never compute a new number: no ratios, differences, sums or averages. A claim with a number that is not in DATA is deleted.
2. Each evidence item names the run or scenario, the step or turn, the field and its value, e.g. "refund-escalation, step c5: ttfa p95_ms 2544".
3. A step with harness_degraded true measured the test machine, not the agent: draw no conclusion from it, and say so inside the insight it affects, never as an insight of its own. A p95 from fewer than 30 calls is fragile.
4. Prefer what a reader cannot see in one table: patterns across steps, turns or scenarios, contradictions and surprises. Do not restate every number.
5. Order insights by what a team should act on first, at most 5. Fewer is better than repeated, and none is allowed. next_step is one concrete action with no invented numbers.
6. headline is one sentence a manager could repeat.
7. Say what the data shows, and offer a cause only as a possibility unless DATA shows it: "the closing turn is the slowest", not "a longer history makes it slower". Published reference lines are other people's targets, not this team's service levels.
8. Write fractions such as wer or setup_success as percentages with one decimal place: 0.0356 is 3.6%. Do not call anything perfect, always or never unless every value in DATA supports it.
9. Every insight is a different finding. A turn, a step's result, a cause or a figure belongs to one insight only: if two insights would share one, write a single insight covering both. An insight citing evidence an earlier insight already cited is deleted.
10. The page already answers these in plain words, right beside this analysis: where the wait goes, which turn is slowest and what the caller said on it, how often waits pass 1.2 seconds, whether quality or speed gives way first, words misheard, interruptions, repeated replies, whether slow calls did worse, whether the test machine kept up, and how many calls stand behind a percentile. Do not restate any of them. Use one only to connect it to something those answers cannot show: a cause across fields, a contradiction, or a pattern across scenarios.
11. When kind is "suite", each run's own analysis is shown beneath this one. Write only about the scenarios together: what holds in every scenario, stated once with the scenarios named, and how scenarios differ. Leave a finding about one scenario to that run's analysis.

Look for these, writing about one only where it adds to the plain-language answers (rule 10):
- Where the wait goes: network, noticing the caller stopped, or thinking.
- Whether latency follows load. A slowdown that rises and falls back as calls increase was not caused by load.
- The slowest callers against the typical caller (p95 against p50): a wide gap means some calls queue or stall.
- A turn much slower than the others, and what the caller said on it.
- In a suite, how scenarios differ and which reaches its limit first.
- Quality under load: words misheard (wer), task success and how many calls the judge graded, interruptions, dead air, talk ratio, how long the agent kept talking when interrupted.
- The run's reference lines: which published line a number is above or below, on the clock it was defined on.
- Failed calls, failed turns and errors.
- Whether the test can be trusted: harness drift, sample sizes, a judge that passes everything on easy criteria.
- The tail breaking while the median holds: p95 or p99 rising while p50 stays flat.
- Latency holding while quality slips, or the reverse: task success, wer or interruptions at busy steps against the baseline step.
- Later turns slower than earlier ones, as the conversation grows.
- Thinking growing while endpointing stays flat: the reply is the bottleneck, not hearing the caller.
- Too few calls or judged calls behind a percentile or a pass rate to trust it.

Newer fields, absent on runs that predate them:
- steps[].turns is the wait at each turn of the conversation within one step, with the caller's line. Compare turns within a step, not across loads.
- steps[].waits counts a step's turns by the caller's wait: up to 800ms, Hamming's target; to 1200ms, past which Coval says callers repeat themselves; to 2000ms; and over, which is dead air.
- steps[].quality is the step's verdict on doing the job, apart from its latency verdict, with what it compared and why it warned or failed. A step can pass on speed and fail on quality.
- steps[].leading_silence is silence at the start of replies before any sound, and audible_ttfa the wait until a caller could hear something.
- conversation.repeated_replies counts replies that repeated an earlier reply in the same call.
- calls.by_turn[].heard_cut_short counts turns where the agent's transcript of the caller held under half the words the caller said; ttfa_p50_ms_cut_short and ttfa_p50_ms_heard_in_full are that turn's median wait on those turns and on the rest. A turn slow mostly when cut short is a hearing problem, not a thinking one.
- judge.waits compares how often judged calls with a wait past line_ms did the job against calls without one. Read it only when conclusive is true.`
