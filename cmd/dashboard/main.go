// Command dashboard serves run history and report cards.
//
// The terminal shows you one run as it happens and the artifacts keep every run
// forever, but neither answers the question a team actually asks: is this agent
// getting worse? That needs runs side by side, which is what this is for.
//
// It reads the run directory directly rather than a database. At a few hundred
// runs the directory is the index, and standing up a store to answer questions
// nobody has asked yet would be building for a scale that does not exist.
package main

import (
	"embed"
	"encoding/json"
	"flag"
	"fmt"
	"io/fs"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/yakshgandhi/callstorm/internal/loadgen"
)

//go:embed ui/*
var ui embed.FS

func main() {
	var (
		runsDir = flag.String("runs", "runs", "directory of run artifacts")
		addr    = flag.String("addr", ":8090", "listen address")
		export  = flag.String("export", "", "write a static site to this directory and exit")
	)
	flag.Parse()

	if *export != "" {
		if err := exportStatic(*runsDir, *export); err != nil {
			log.Fatal(err)
		}
		return
	}

	mux := http.NewServeMux()
	// Paths carry .json so the very same requests work against a static
	// export, where a file server has nothing but files to offer.
	mux.HandleFunc("GET /api/runs.json", listRuns(*runsDir))
	// A wildcard has to be a whole path segment, so the extension is stripped
	// in the handler rather than written into the pattern.
	mux.HandleFunc("GET /api/runs/{id}", getRun(*runsDir))
	// The calls file beside a report: every turn's words and instants, one
	// call per line. The report summarises it; the transcript view reads it.
	mux.HandleFunc("GET /api/runs/{id}/calls.jsonl", getCalls(*runsDir))
	mux.HandleFunc("GET /api/runs/{id}/archive.tar.gz", getArchive(*runsDir))
	// The written analysis of a run, and of a suite of runs, for runs that have
	// one. Both dashboards read these; neither writes them.
	mux.HandleFunc("GET /api/runs/{id}/insights.json", getInsights(*runsDir, "id"))
	mux.HandleFunc("GET /api/suites/{suite}/insights.json", getInsights(*runsDir, "suite"))
	// The published reference lines, served from the same embedded file the
	// CLI reads, so the page cannot quote a different source from the report.
	mux.HandleFunc("GET /api/references.json", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(loadgen.ReferencesJSON())
	})
	mux.HandleFunc("POST /api/clienterror", clientError)

	pages, err := newFS()
	if err != nil {
		log.Fatal(err)
	}
	mux.Handle("/", http.FileServer(pages))

	log.Printf("dashboard on http://localhost%s  runs=%s", *addr, *runsDir)
	if err := http.ListenAndServe(*addr, revalidate(mux)); err != nil {
		log.Fatal(err)
	}
}

// revalidate has the browser check every /api/ response again before reusing
// it. Run files change on disk after a run -- a re-judge rewrites a report, an
// analysis is written later -- and a file served with only Last-Modified is
// cached on a guess, so a page went on showing an analysis already replaced.
func revalidate(h http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/api/") {
			w.Header().Set("Cache-Control", "no-cache")
		}
		h.ServeHTTP(w, r)
	})
}

func newFS() (http.FileSystem, error) {
	sub, err := fs.Sub(ui, "ui")
	if err != nil {
		return nil, err
	}
	return http.FS(sub), nil
}

// exportStatic writes the dashboard as plain files.
//
// The run artifacts are already in the repository, so the history needs no
// server to read them: the same requests the API answers can be answered by a
// directory. That makes the dashboard publishable anywhere static hosting works,
// while the server stays the way to watch runs arriving live.
func exportStatic(runsDir, dst string) error {
	if err := os.MkdirAll(filepath.Join(dst, "api", "runs"), 0o755); err != nil {
		return err
	}

	// The page and the stylesheets it links beside it: the design kit's
	// theme and components, and the dashboard's own layout on top of them.
	for _, name := range []string{"index.html", "theme.css", "components.css", "dashboard.css", "clarity.css", "studio.css"} {
		b, err := ui.ReadFile("ui/" + name)
		if err != nil {
			return err
		}
		if err := os.WriteFile(filepath.Join(dst, name), b, 0o644); err != nil {
			return err
		}
	}
	// GitHub Pages otherwise runs the output through Jekyll, which drops files
	// and directories whose names begin with an underscore.
	if err := os.WriteFile(filepath.Join(dst, ".nojekyll"), nil, 0o644); err != nil {
		return err
	}

	runs := collect(runsDir)
	index, err := json.Marshal(runs)
	if err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(dst, "api", "runs.json"), index, 0o644); err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(dst, "api", "references.json"), loadgen.ReferencesJSON(), 0o644); err != nil {
		return err
	}

	for _, r := range runs {
		src, err := resolve(runsDir, r.ID, ".json")
		if err != nil {
			continue
		}
		b, err := os.ReadFile(src)
		if err != nil {
			continue
		}
		if err := os.WriteFile(filepath.Join(dst, "api", "runs", r.ID+".json"), b, 0o644); err != nil {
			return err
		}
		// The written analysis, for runs that have one.
		if ins, err := resolve(runsDir, r.ID+"-insights", ".json"); err == nil {
			if err := copyInto(ins, filepath.Join(dst, "api", "runs", r.ID, "insights.json")); err != nil {
				return err
			}
		}
		// The calls file, at the same path the server answers, for runs that kept one.
		calls, err := resolve(runsDir, r.ID+"-calls", ".jsonl")
		if err != nil {
			continue
		}
		c, err := os.ReadFile(calls)
		if err != nil {
			continue
		}
		if err := os.MkdirAll(filepath.Join(dst, "api", "runs", r.ID), 0o755); err != nil {
			return err
		}
		if err := os.WriteFile(filepath.Join(dst, "api", "runs", r.ID, "calls.jsonl"), c, 0o644); err != nil {
			return err
		}
	}
	for _, s := range suitesOf(runs) {
		if ins, err := resolve(runsDir, s+"-insights", ".json"); err == nil {
			if err := copyInto(ins, filepath.Join(dst, "api", "suites", s, "insights.json")); err != nil {
				return err
			}
		}
	}

	log.Printf("exported %d runs to %s", len(runs), dst)
	return nil
}

// collect reads every load report in the run directory.
func collect(dir string) []summary {
	reports, _ := filepath.Glob(filepath.Join(dir, "*.json"))
	nested, _ := filepath.Glob(filepath.Join(dir, "*", "*.json"))
	reports = append(reports, nested...)

	out := []summary{}
	for _, p := range reports {
		if strings.HasSuffix(p, "-judgements.json") || strings.HasSuffix(p, "-insights.json") {
			continue
		}
		rep, err := readReport(p)
		if err != nil || rep.Profile == "" {
			continue
		}
		out = append(out, summarize(p, rep))
	}
	sort.Slice(out, func(i, j int) bool { return out[i].StartedAt.After(out[j].StartedAt) })
	return out
}

// summary is one row of run history: enough to decide which run to open,
// without reading every report in the directory into memory.
type summary struct {
	ID        string    `json:"id"`
	Profile   string    `json:"profile"`
	Scenario  string    `json:"scenario"`
	Target    string    `json:"target"`
	StartedAt time.Time `json:"started_at"`
	Duration  float64   `json:"duration_s"`

	Steps       int     `json:"steps"`
	Verdict     string  `json:"verdict"`
	Breakpoint  string  `json:"breakpoint,omitempty"`
	PeakConc    int     `json:"peak_concurrency"`
	BaselineP95 float64 `json:"baseline_p95_ms"`
	WorstP95    float64 `json:"worst_p95_ms"`
	WERMean     float64 `json:"wer_mean"`
	Degraded    bool    `json:"harness_degraded"`

	// Judged and Passed say whether this run answers the second question at
	// all. Without them, finding the runs that were graded means opening every
	// one of them.
	Judged int `json:"judged,omitempty"`
	Passed int `json:"passed,omitempty"`

	// Drifted counts steps that got slower across their own duration. It is
	// the one finding that can be true while every verdict in the run passes.
	Drifted int `json:"drifted,omitempty"`

	// Cohorts counts the network conditions an impairment matrix ran under,
	// zero for a run on an unimpaired network.
	Cohorts int `json:"cohorts,omitempty"`

	// NetworkBreaks names each impaired cohort that failed, and where. The
	// verdict and percentiles above describe the clean control, so without
	// this a matrix whose severe network broke at the first step reads as a
	// pass in the history.
	NetworkBreaks []string `json:"network_breaks,omitempty"`

	// ScenarioHash and ProfileHash let a run be compared with earlier runs
	// that asked exactly the same question.
	ScenarioHash string `json:"scenario_hash,omitempty"`
	ProfileHash  string `json:"profile_hash,omitempty"`

	// Suite names the load test this run was one part of, so the history can
	// show a test that spans several runs as one entry.
	Suite string `json:"suite,omitempty"`
}

func listRuns(dir string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, collect(dir))
	}
}

func summarize(path string, rep *loadgen.Report) summary {
	s := summary{
		ID:        strings.TrimSuffix(filepath.Base(path), ".json"),
		Profile:   rep.Profile,
		Scenario:  rep.Scenario,
		Target:    rep.Target,
		StartedAt: rep.StartedAt,
		Duration:  rep.Duration,
		Steps:     len(rep.Steps),
		Verdict:   "pass",
	}
	for _, st := range rep.Steps {
		if st.Concurrency > s.PeakConc {
			s.PeakConc = st.Concurrency
		}
		if st.TTFA.P95Ms > s.WorstP95 {
			s.WorstP95 = st.TTFA.P95Ms
		}
		if st.Name == rep.Baseline {
			s.BaselineP95 = st.TTFA.P95Ms
		}
		if st.WER.Mean > s.WERMean {
			s.WERMean = st.WER.Mean
		}
		if st.HarnessDegraded {
			s.Degraded = true
		}
		// The worst verdict in the run is the run's verdict: a sweep that
		// failed anywhere did not pass.
		switch st.Verdict {
		case "fail":
			s.Verdict = "fail"
		case "warn":
			if s.Verdict != "fail" {
				s.Verdict = "warn"
			}
		}
	}
	if bp := rep.Breakpoint(); bp != nil {
		s.Breakpoint = fmt.Sprintf("%s at %d concurrent", bp.Name, bp.Concurrency)
	}
	if rep.Judge != nil {
		s.Judged, s.Passed = rep.Judge.Judged, rep.Judge.Passed
	}
	s.Drifted = len(rep.Drifted())
	s.ScenarioHash, s.ProfileHash = rep.ScenarioHash, rep.ProfileHash
	s.Suite = rep.Suite
	if rep.Matrix != nil {
		s.Cohorts = len(rep.Matrix.Cohorts)
		for _, c := range rep.Matrix.Cohorts[1:] {
			if c.Breakpoint != "" {
				s.NetworkBreaks = append(s.NetworkBreaks, fmt.Sprintf("%s at %s", c.Impairment.Name, c.Breakpoint))
			}
		}
	}
	return s
}

func getRun(dir string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id := strings.TrimSuffix(r.PathValue("id"), ".json")
		p, err := resolve(dir, id, ".json")
		if err != nil {
			http.NotFound(w, r)
			return
		}
		b, err := os.ReadFile(p)
		if err != nil {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(b)
	}
}

// getCalls serves the calls file recorded beside a report, for runs that kept one.
func getCalls(dir string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		p, err := resolve(dir, r.PathValue("id")+"-calls", ".jsonl")
		if err != nil {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/x-ndjson")
		http.ServeFile(w, r, p)
	}
}

// getInsights serves the written analysis of a run, or of a suite, named by
// the path value param, for those that have one.
func getInsights(dir, param string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		p, err := resolve(dir, r.PathValue(param)+"-insights", ".json")
		if err != nil {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		http.ServeFile(w, r, p)
	}
}

// copyInto copies one file into the export, making its directory.
func copyInto(src, dst string) error {
	b, err := os.ReadFile(src)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return err
	}
	return os.WriteFile(dst, b, 0o644)
}

// suitesOf lists the suites the runs belong to, once each.
func suitesOf(runs []summary) []string {
	seen := map[string]bool{}
	var out []string
	for _, r := range runs {
		if r.Suite != "" && !seen[r.Suite] {
			seen[r.Suite] = true
			out = append(out, r.Suite)
		}
	}
	return out
}

// resolve turns a run id into a path inside dir, refusing anything that tries
// to climb out of it.
func resolve(dir, id, ext string) (string, error) {
	if id == "" || strings.ContainsAny(id, `/\*?[]:`) || strings.Contains(id, "..") {
		return "", fmt.Errorf("bad id")
	}
	candidates, _ := filepath.Glob(filepath.Join(dir, id+ext))
	nested, _ := filepath.Glob(filepath.Join(dir, "*", id+ext))
	candidates = append(candidates, nested...)
	if len(candidates) == 0 {
		return "", fmt.Errorf("not found")
	}
	return candidates[0], nil
}

func readReport(path string) (*loadgen.Report, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var rep loadgen.Report
	if err := json.Unmarshal(b, &rep); err != nil {
		return nil, err
	}
	return &rep, nil
}

// clientError lets the page report a crash it could not otherwise show.
//
// A dashboard that renders and then goes blank tells its operator nothing, and
// the one machine that can see the console belongs to whoever is looking at it.
// Sending the failure to the server puts it in the same log as everything else.
func clientError(w http.ResponseWriter, r *http.Request) {
	var e struct {
		Message string `json:"message"`
		Stack   string `json:"stack"`
		Where   string `json:"where"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 64<<10)).Decode(&e); err != nil {
		http.Error(w, "bad report", http.StatusBadRequest)
		return
	}
	log.Printf("ui error in %s: %s\n%s", e.Where, e.Message, e.Stack)
	w.WriteHeader(http.StatusNoContent)
}

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(v)
}

func httpError(w http.ResponseWriter, err error) {
	http.Error(w, err.Error(), http.StatusInternalServerError)
}
