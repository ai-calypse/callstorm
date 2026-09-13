package insights

import (
	"bufio"
	"encoding/json"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// Digest is what the model reads: each run's report with the parts that are
// large and say nothing to a reader trimmed away, and figures per turn of the
// conversation from the run's calls log.
type Digest struct {
	Kind    string      `json:"kind"`
	Subject string      `json:"subject"`
	Runs    []RunDigest `json:"runs"`
}

// RunDigest is one run as the model sees it.
type RunDigest struct {
	ID     string         `json:"id"`
	Report map[string]any `json:"report"`
	Calls  *CallStats     `json:"calls,omitempty"`
}

// CallStats summarises the calls log: the per-turn view no report table has.
// A turn that is slow in every call is invisible in a step's percentiles,
// which mix every turn of every call together.
type CallStats struct {
	Calls  int         `json:"calls"`
	Failed int         `json:"failed_calls"`
	Errors []string    `json:"sample_errors,omitempty"`
	ByTurn []TurnStats `json:"by_turn"`
}

// TurnStats is one turn of the conversation, across every call in the run.
type TurnStats struct {
	Turn             int     `json:"turn"`
	Count            int     `json:"count"`
	Failed           int     `json:"failed"`
	TTFAP50Ms        float64 `json:"ttfa_p50_ms"`
	TTFAP95Ms        float64 `json:"ttfa_p95_ms"`
	EndpointingP50Ms float64 `json:"endpointing_p50_ms"`
	ThinkSpeakP50Ms  float64 `json:"think_speak_p50_ms"`
	CallerLine       string  `json:"caller_line"`
	CommonReply      string  `json:"most_common_reply"`
	CommonReplyCount int     `json:"most_common_reply_count"`
}

// IDs lists the runs the digest covers.
func (d Digest) IDs() []string {
	ids := make([]string, len(d.Runs))
	for i, r := range d.Runs {
		ids[i] = r.ID
	}
	return ids
}

// ForRun builds the digest of one run from its report.
func ForRun(reportPath string) (Digest, error) {
	rd, err := loadRun(reportPath)
	if err != nil {
		return Digest{}, err
	}
	return Digest{Kind: "run", Subject: rd.ID, Runs: []RunDigest{rd}}, nil
}

// ForSuite builds the digest of every run of suite found in dir, oldest first.
func ForSuite(dir, suite string) (Digest, error) {
	var runs []RunDigest
	for _, p := range reports(dir) {
		rd, err := loadRun(p)
		if err != nil {
			continue
		}
		if s, _ := rd.Report["suite"].(string); s == suite {
			runs = append(runs, rd)
		}
	}
	if len(runs) == 0 {
		return Digest{}, fmt.Errorf("no runs of suite %s in %s", suite, dir)
	}
	// Run ids begin with the time they started, so they sort in run order.
	sort.Slice(runs, func(i, j int) bool { return runs[i].ID < runs[j].ID })
	return Digest{Kind: "suite", Subject: suite, Runs: runs}, nil
}

// Suites lists the suites that runs in dir belong to, and the reports that
// belong to none.
func Suites(dir string) (suites []string, loose []string) {
	seen := map[string]bool{}
	for _, p := range reports(dir) {
		rd, err := loadRun(p)
		if err != nil {
			continue
		}
		s, _ := rd.Report["suite"].(string)
		if s == "" {
			loose = append(loose, p)
			continue
		}
		if !seen[s] {
			seen[s] = true
			suites = append(suites, s)
		}
	}
	sort.Strings(suites)
	return suites, loose
}

// Reports lists the load reports in dir, oldest first.
func Reports(dir string) []string {
	var out []string
	for _, p := range reports(dir) {
		if _, err := loadRun(p); err == nil {
			out = append(out, p)
		}
	}
	return out
}

// reports lists the JSON files in dir that could be load reports, leaving out
// the files that sit beside them.
func reports(dir string) []string {
	paths, _ := filepath.Glob(filepath.Join(dir, "*.json"))
	var out []string
	for _, p := range paths {
		if strings.HasSuffix(p, "-judgements.json") || strings.HasSuffix(p, "-insights.json") {
			continue
		}
		out = append(out, p)
	}
	return out
}

func loadRun(path string) (RunDigest, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return RunDigest{}, err
	}
	var rep map[string]any
	if err := json.Unmarshal(b, &rep); err != nil {
		return RunDigest{}, fmt.Errorf("read %s: %w", path, err)
	}
	if _, ok := rep["steps"]; !ok {
		return RunDigest{}, fmt.Errorf("%s is not a load report", path)
	}
	prune(rep)

	rd := RunDigest{ID: strings.TrimSuffix(filepath.Base(path), ".json"), Report: rep}
	if cs, err := callStats(strings.TrimSuffix(path, ".json") + "-calls.jsonl"); err == nil {
		rd.Calls = cs
	}
	return rd, nil
}

// prune drops what is large and tells a reader nothing: every call's point on
// a step's timeline, every judged transcript, a matrix cohort's raw qdisc
// output, and fingerprints, whose hex digits would otherwise count as numbers
// the data holds.
func prune(rep map[string]any) {
	pruneSteps(rep["steps"])
	if j, ok := rep["judge"].(map[string]any); ok {
		delete(j, "judgements")
		delete(j, "calls")
	}
	if m, ok := rep["matrix"].(map[string]any); ok {
		if cohorts, ok := m["cohorts"].([]any); ok {
			for _, c := range cohorts {
				if cm, ok := c.(map[string]any); ok {
					pruneSteps(cm["steps"])
					delete(cm, "qdisc")
				}
			}
		}
	}
	delete(rep, "scenario_hash")
	delete(rep, "profile_hash")
}

func pruneSteps(v any) {
	steps, ok := v.([]any)
	if !ok {
		return
	}
	for _, s := range steps {
		if m, ok := s.(map[string]any); ok {
			delete(m, "timeline")
		}
	}
}

type callLine struct {
	Error string `json:"error"`
	Turns []struct {
		Turn          int     `json:"turn"`
		Failed        bool    `json:"failed"`
		CallerText    string  `json:"caller_text"`
		AgentText     string  `json:"agent_text"`
		TTFAMs        float64 `json:"ttfa_ms"`
		EndpointingMs float64 `json:"endpointing_ms"`
		ThinkSpeakMs  float64 `json:"think_speak_ms"`
	} `json:"turns"`
}

func callStats(path string) (*CallStats, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	type acc struct {
		count, failed    int
		ttfa, ep, ts     []float64
		callers, replies map[string]int
	}
	byTurn := map[int]*acc{}
	cs := &CallStats{}
	errs := map[string]bool{}

	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 1<<20), 64<<20)
	for sc.Scan() {
		var c callLine
		if err := json.Unmarshal(sc.Bytes(), &c); err != nil {
			return nil, fmt.Errorf("read %s: %w", path, err)
		}
		cs.Calls++
		if c.Error != "" {
			cs.Failed++
			if !errs[c.Error] && len(cs.Errors) < 3 {
				errs[c.Error] = true
				cs.Errors = append(cs.Errors, c.Error)
			}
		}
		for _, t := range c.Turns {
			a := byTurn[t.Turn]
			if a == nil {
				a = &acc{callers: map[string]int{}, replies: map[string]int{}}
				byTurn[t.Turn] = a
			}
			a.count++
			if t.Failed {
				a.failed++
			}
			if t.TTFAMs > 0 {
				a.ttfa = append(a.ttfa, t.TTFAMs)
			}
			if t.EndpointingMs > 0 {
				a.ep = append(a.ep, t.EndpointingMs)
			}
			if t.ThinkSpeakMs > 0 {
				a.ts = append(a.ts, t.ThinkSpeakMs)
			}
			a.callers[t.CallerText]++
			a.replies[t.AgentText]++
		}
	}
	if err := sc.Err(); err != nil {
		return nil, err
	}

	turns := make([]int, 0, len(byTurn))
	for t := range byTurn {
		turns = append(turns, t)
	}
	sort.Ints(turns)
	for _, t := range turns {
		a := byTurn[t]
		caller, _ := commonest(a.callers)
		reply, n := commonest(a.replies)
		cs.ByTurn = append(cs.ByTurn, TurnStats{
			Turn: t, Count: a.count, Failed: a.failed,
			TTFAP50Ms: nearestRank(a.ttfa, 0.50), TTFAP95Ms: nearestRank(a.ttfa, 0.95),
			EndpointingP50Ms: nearestRank(a.ep, 0.50), ThinkSpeakP50Ms: nearestRank(a.ts, 0.50),
			CallerLine: caller, CommonReply: reply, CommonReplyCount: n,
		})
	}
	return cs, nil
}

// nearestRank is the percentile the rest of Callstorm reports: a value that
// was actually measured, never an interpolation between two.
func nearestRank(v []float64, p float64) float64 {
	if len(v) == 0 {
		return 0
	}
	s := append([]float64(nil), v...)
	sort.Float64s(s)
	i := int(math.Ceil(p*float64(len(s)))) - 1
	if i < 0 {
		i = 0
	}
	return math.Round(s[i]*10) / 10
}

// commonest returns the most frequent string, the alphabetically first on a
// tie, so the same log always gives the same digest.
func commonest(counts map[string]int) (string, int) {
	best, n := "", 0
	for s, c := range counts {
		if c > n || (c == n && s < best) {
			best, n = s, c
		}
	}
	return best, n
}
