// Command callstorm places synthetic calls against a voice agent and reports
// what the caller experienced.
//
// With -profile it runs a load profile and reports per-step percentiles;
// without one it places a single call and records it.
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"time"

	"github.com/yakshgandhi/callstorm/internal/audio"
	"github.com/yakshgandhi/callstorm/internal/bus"
	"github.com/yakshgandhi/callstorm/internal/chart"
	"github.com/yakshgandhi/callstorm/internal/config"
	"github.com/yakshgandhi/callstorm/internal/judge"
	"github.com/yakshgandhi/callstorm/internal/loadgen"
	"github.com/yakshgandhi/callstorm/internal/metrics"
	"github.com/yakshgandhi/callstorm/internal/scenario"
	"github.com/yakshgandhi/callstorm/internal/telemetry"
	"github.com/yakshgandhi/callstorm/internal/tts"
	"github.com/yakshgandhi/callstorm/internal/worker"
)

// deepgramConcurrencyCap is Deepgram's Voice Agent limit on pay-as-you-go
// (45 connections; 60 on Growth, 100+ on Enterprise). A profile that exceeds it
// produces connection refusals that look exactly like agent failures, so the
// preflight blocks rather than letting a run generate misleading data.
const deepgramConcurrencyCap = 40

type opts struct {
	scenarioPath   string
	profilePath    string
	outDir         string
	cacheDir       string
	sampleRate     int
	turnTimeout    time.Duration
	targetURL      string
	maxConcurrency int
	envPath        string
	metricsAddr    string
	metricsLinger  time.Duration
	kafkaBrokers   string
	kafkaTopic     string
	judge          bool
	judgeCalls     int
	judgeBackend   string
	judgeModel     string
}

func main() {
	var o opts
	flag.StringVar(&o.scenarioPath, "scenario", "scenarios/refund.json", "scenario file to run")
	flag.StringVar(&o.profilePath, "profile", "", "load profile file; omit to place a single call")
	flag.StringVar(&o.outDir, "out", "runs", "directory for run artifacts")
	flag.StringVar(&o.cacheDir, "cache", ".audiocache", "directory for synthesized caller audio")
	flag.IntVar(&o.sampleRate, "rate", 24000, "audio sample rate in Hz")
	flag.DurationVar(&o.turnTimeout, "turn-timeout", 20*time.Second, "how long to wait for an agent response")
	flag.StringVar(&o.targetURL, "target", "", "agent under test (default: Deepgram Voice Agent)")
	flag.IntVar(&o.maxConcurrency, "max-concurrency", 0, "refuse profiles above this peak (0 = auto)")
	flag.StringVar(&o.envPath, "env", ".env", "file of KEY=VALUE credentials; overrides the environment")
	flag.StringVar(&o.metricsAddr, "metrics", "", "expose Prometheus metrics on this address, e.g. :9090")
	flag.DurationVar(&o.metricsLinger, "metrics-linger", 5*time.Second,
		"keep /metrics up this long after the run, so the last step can be scraped")
	flag.StringVar(&o.kafkaBrokers, "kafka", "", "comma-separated Kafka brokers to publish turn events to")
	flag.StringVar(&o.kafkaTopic, "kafka-topic", bus.DefaultTopic, "topic for per-turn events")
	flag.BoolVar(&o.judge, "judge", false,
		"score sampled conversations against the scenario's success_criteria")
	flag.IntVar(&o.judgeCalls, "judge-calls", 1,
		"conversations to judge per step (0 = every call)")
	flag.StringVar(&o.judgeBackend, "judge-backend", "auto",
		"who judges: groq, claude-code, or auto (groq when GROQ_API_KEY is set)")
	flag.StringVar(&o.judgeModel, "judge-model", "", "model to judge with (default: the backend's own)")
	flag.Parse()

	if err := run(o); err != nil {
		fmt.Fprintf(os.Stderr, "\ncallstorm: %v\n", err)
		os.Exit(1)
	}
}

func run(o opts) error {
	// .env wins over the inherited environment on purpose; see config.LoadDotEnv.
	loaded, err := config.LoadDotEnv(o.envPath)
	if err != nil {
		return fmt.Errorf("read %s: %w", o.envPath, err)
	}

	apiKey := os.Getenv("DEEPGRAM_API_KEY")
	if apiKey == "" {
		return fmt.Errorf("DEEPGRAM_API_KEY is not set (put it in %s or the environment)", o.envPath)
	}
	source := "environment"
	if len(loaded) > 0 {
		source = o.envPath
	}
	fmt.Printf("credentials %s  key %s\n", source, config.Fingerprint(apiKey))

	sc, err := scenario.Load(o.scenarioPath)
	if err != nil {
		return err
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()

	fmt.Printf("callstorm  scenario=%s\n", sc.Name)
	if o.targetURL != "" {
		fmt.Printf("target     %s\n", o.targetURL)
	} else {
		fmt.Printf("target     deepgram voice agent  %s / %s / %s\n",
			sc.Target.ListenModel, sc.Target.ThinkModel, sc.Target.Voice)
	}

	if o.profilePath != "" {
		return runLoad(ctx, o, sc, apiKey)
	}
	return runSingle(ctx, o, sc, apiKey)
}

func runSingle(ctx context.Context, o opts, sc *scenario.Scenario, apiKey string) error {
	fmt.Println()
	res, err := worker.Run(ctx, worker.Config{
		APIKey:      apiKey,
		Scenario:    sc,
		TTS:         tts.New(apiKey, o.sampleRate, o.cacheDir),
		SampleRate:  o.sampleRate,
		TurnTimeout: o.turnTimeout,
		TargetURL:   o.targetURL,
		Out:         os.Stdout,
		// A single call always records: being able to listen to it is the
		// cheapest check that the clock is honest.
		Record: true,
	})
	if err != nil {
		return err
	}

	runID := fmt.Sprintf("%s-%s", res.StartedAt.Format("20060102-150405"), sc.Name)
	jsonPath, wavPath, err := writeArtifacts(o.outDir, runID, res)
	if err != nil {
		return err
	}
	printReport(res, jsonPath, wavPath)
	return nil
}

func runLoad(ctx context.Context, o opts, sc *scenario.Scenario, apiKey string) error {
	profile, err := loadgen.LoadProfile(o.profilePath)
	if err != nil {
		return err
	}
	if err := preflight(o, profile); err != nil {
		return err
	}

	fmt.Printf("profile    %s  %d steps, %d calls, peak concurrency %d\n",
		profile.Name, len(profile.Steps), profile.TotalCalls(), profile.PeakConcurrency())

	var mx *telemetry.Metrics
	if o.metricsAddr != "" {
		mx = telemetry.New()
		go func() {
			if err := mx.Serve(ctx, o.metricsAddr); err != nil {
				fmt.Fprintf(os.Stderr, "metrics server: %v\n", err)
			}
		}()
		fmt.Printf("metrics    http://localhost%s/metrics\n", o.metricsAddr)
	}

	runID := fmt.Sprintf("%s-%s", time.Now().Format("20060102-150405"), profile.Name)

	var producer *bus.Producer
	if o.kafkaBrokers != "" {
		producer, err = bus.NewProducer(strings.Split(o.kafkaBrokers, ","), o.kafkaTopic)
		if err != nil {
			return err
		}
		defer producer.Close()
		fmt.Printf("kafka      %s topic=%s\n", o.kafkaBrokers, o.kafkaTopic)
	}

	rep, err := loadgen.Run(ctx, loadgen.Config{
		APIKey:      apiKey,
		Scenario:    sc,
		Profile:     profile,
		TTS:         tts.New(apiKey, o.sampleRate, o.cacheDir),
		SampleRate:  o.sampleRate,
		TurnTimeout: o.turnTimeout,
		TargetURL:   o.targetURL,
		Out:         os.Stdout,
		Metrics:     mx,
		Bus:         producer,
		RunID:       runID,
	})
	if err != nil {
		return err
	}

	if producer != nil {
		produced, dropped := producer.Flush(ctx)
		fmt.Printf("kafka      published %d turn events, dropped %d", produced, dropped)
		if err := producer.LastError(); err != nil {
			fmt.Printf(" (%v)", err)
		}
		fmt.Println()
	}

	if err := os.MkdirAll(o.outDir, 0o755); err != nil {
		return err
	}
	jsonPath := filepath.Join(o.outDir, runID+".json")
	b, err := json.MarshalIndent(rep, "", "  ")
	if err != nil {
		return err
	}
	if err := os.WriteFile(jsonPath, b, 0o644); err != nil {
		return err
	}
	csvPath := filepath.Join(o.outDir, runID+".csv")
	if err := writeCSV(csvPath, rep); err != nil {
		return err
	}
	callsPath := filepath.Join(o.outDir, runID+"-calls.jsonl")
	if err := writeCalls(callsPath, rep.Calls); err != nil {
		return err
	}

	svgPath := filepath.Join(o.outDir, runID+".svg")
	if err := chart.RenderSweep(svgPath, rep); err != nil {
		fmt.Fprintf(os.Stderr, "chart: %v\n", err)
		svgPath = ""
	}

	printLoadReport(rep, jsonPath, csvPath, svgPath, callsPath)

	if o.judge {
		if err := judgeRun(ctx, o, sc, rep, runID); err != nil {
			// A judge that could not run is not a failed load test. The
			// latency numbers above stand on their own.
			fmt.Fprintf(os.Stderr, "judge: %v\n", err)
		}
	}

	// A step's turns land in the registry as that step ends, and a scrape that
	// arrives after the process has exited gets nothing at all. The breakpoint
	// is by definition the last step, so exiting immediately drops exactly the
	// numbers the run exists to produce -- and leaves active_calls stuck at
	// its last non-zero value rather than back at rest.
	if mx != nil && o.metricsLinger > 0 {
		fmt.Printf("metrics    holding /metrics open %s so the last step can be scraped\n",
			o.metricsLinger)
		select {
		case <-time.After(o.metricsLinger):
		case <-ctx.Done():
		}
	}
	return nil
}

// preflight refuses runs that would produce misleading data.
func preflight(o opts, p *loadgen.Profile) error {
	limit := o.maxConcurrency
	if limit == 0 && o.targetURL == "" {
		// Only Deepgram has a published connection ceiling. A local reference
		// agent has none, so its cap is the operator's to set.
		limit = deepgramConcurrencyCap
	}
	if limit > 0 && p.PeakConcurrency() > limit {
		return fmt.Errorf(
			"profile peaks at %d concurrent calls but the cap is %d.\n"+
				"Deepgram allows 45 concurrent Voice Agent connections on pay-as-you-go, so a\n"+
				"higher peak would report connection refusals as agent failures. Point -target\n"+
				"at a reference agent for larger runs, or raise -max-concurrency deliberately",
			p.PeakConcurrency(), limit)
	}
	if o.targetURL == "" {
		fmt.Printf("cost       %d calls billed by Deepgram; caller audio is cached and free\n",
			p.TotalCalls())
	}
	return nil
}

func writeCSV(path string, rep *loadgen.Report) error {
	var b strings.Builder
	b.WriteString("step,concurrency,calls,setup_success,ttfa_p50_ms,ttfa_p95_ms,ttfa_p99_ms," +
		"turn_latency_p95_ms,turns,turns_failed,p95_ratio,verdict\n")
	for _, s := range rep.Steps {
		fmt.Fprintf(&b, "%s,%d,%d,%.4f,%.1f,%.1f,%.1f,%.1f,%d,%d,%.2f,%s\n",
			s.Name, s.Concurrency, s.Calls, s.SetupSuccess,
			s.TTFA.P50Ms, s.TTFA.P95Ms, s.TTFA.P99Ms, s.TurnLatency.P95Ms,
			s.TurnsTotal, s.TurnsFailed, s.P95Ratio, s.Verdict)
	}
	return os.WriteFile(path, []byte(b.String()), 0o644)
}

func printLoadReport(rep *loadgen.Report, jsonPath, csvPath, svgPath, callsPath string) {
	fmt.Printf("\n%s\n", strings.Repeat("-", 92))
	fmt.Printf("LOAD REPORT  %s   scenario=%s\n", rep.Profile, rep.Scenario)
	fmt.Printf("%s\n\n", strings.Repeat("-", 92))

	fmt.Printf("%-12s %-6s %-7s %-11s %-11s %-11s %-9s %s\n",
		"step", "conc", "setup", "ttfa p50", "ttfa p95", "ttfa p99", "vs base", "verdict")
	for _, s := range rep.Steps {
		ratio := "-"
		if s.P95Ratio > 0 {
			ratio = fmt.Sprintf("%.2fx", s.P95Ratio)
		}
		marker := ""
		if s.Name == rep.Baseline {
			marker = "   <- baseline"
		}
		fmt.Printf("%-12s %-6d %-7s %-11s %-11s %-11s %-9s %s%s\n",
			s.Name, s.Concurrency,
			fmt.Sprintf("%.0f%%", s.SetupSuccess*100),
			msf(s.TTFA.P50Ms), msf(s.TTFA.P95Ms), msf(s.TTFA.P99Ms),
			ratio, s.Verdict, marker)
	}

	fmt.Println()
	for _, s := range rep.Steps {
		if s.TurnsYielded > 0 || s.TurnsFailed > 0 {
			fmt.Printf("%-12s %d/%d turns failed, %d talked over\n",
				s.Name, s.TurnsFailed, s.TurnsTotal, s.TurnsYielded)
		}
		for reason, n := range s.Errors {
			fmt.Printf("%-12s   %dx %s\n", "", n, reason)
		}
	}

	anyBarge := false
	for _, s := range rep.Steps {
		if s.BargeIn.Turns > 0 {
			anyBarge = true
		}
	}
	if anyBarge {
		fmt.Printf("\n%-12s %-9s %-9s %-9s %s\n", "step", "barge-ins", "yielded", "yield p50", "yield p95")
		for _, s := range rep.Steps {
			b := s.BargeIn
			if b.Turns == 0 {
				continue
			}
			note := ""
			if b.Yielded < b.Turns {
				// The headline failure: the agent was interrupted and carried
				// on regardless.
				note = fmt.Sprintf("   <- %d never yielded", b.Turns-b.Yielded)
			}
			fmt.Printf("%-12s %-9d %-9d %-9s %s%s\n",
				s.Name, b.Turns, b.Yielded, msf(b.YieldP50Ms), msf(b.YieldP95Ms), note)
		}
	}

	anyWER := false
	for _, s := range rep.Steps {
		if s.WER.Turns > 0 {
			anyWER = true
		}
	}
	if anyWER {
		fmt.Printf("\n%-12s %-9s %-9s %s\n", "step", "wer mean", "wer worst", "errors / words heard")
		for _, s := range rep.Steps {
			if s.WER.Turns == 0 {
				fmt.Printf("%-12s %-9s %-9s %s\n", s.Name, "-", "-", "target returned no transcript")
				continue
			}
			w := s.WER
			fmt.Printf("%-12s %-9s %-9s %d of %d  (%dS %dD %dI over %d turns)\n",
				s.Name,
				fmt.Sprintf("%.1f%%", w.Mean*100),
				fmt.Sprintf("%.1f%%", w.Worst*100),
				w.Substitutions+w.Deletions+w.Insertions, w.RefWords,
				w.Substitutions, w.Deletions, w.Insertions, w.Turns)
		}
	}

	if bp := rep.Breakpoint(); bp != nil {
		fmt.Printf("\nBREAKPOINT   %s at %d concurrent: p95 TTFA %s is %.2fx baseline\n",
			bp.Name, bp.Concurrency, msf(bp.TTFA.P95Ms), bp.P95Ratio)
	} else {
		fmt.Printf("\nBREAKPOINT   none reached; every step stayed inside 2x baseline\n")
	}

	worst := 0.0
	for _, s := range rep.Steps {
		if abs(s.WorstDriftMs) > abs(worst) {
			worst = s.WorstDriftMs
		}
	}
	fmt.Printf("harness      %.0fms worst-case pacing drift across the run\n", worst)

	// A degraded step is already marked in the report, but the report is a
	// file and this is what a person actually reads. A step whose harness fell
	// behind realtime can still say "pass", because the verdict grades the
	// agent against the baseline -- and that verdict is exactly what should
	// not be trusted when the load generator, not the agent, was the limit.
	for _, s := range rep.Steps {
		if s.HarnessDegraded {
			fmt.Printf("             SUSPECT: step %s drifted %.0fms, past the %dms limit.\n",
				s.Name, s.WorstDriftMs, loadgen.MaxHealthyDriftMs)
			fmt.Printf("             This machine could not hold realtime pacing, so that step\n")
			fmt.Printf("             measures the harness rather than the agent. Re-run it on a\n")
			fmt.Printf("             quieter machine or at lower concurrency before believing it.\n")
		}
	}
	fmt.Printf("duration     %.0fs\n", rep.Duration)
	fmt.Printf("\nreport       %s\n", jsonPath)
	fmt.Printf("csv          %s\n", csvPath)
	if svgPath != "" {
		fmt.Printf("chart        %s\n", svgPath)
	}
	if callsPath != "" {
		fmt.Printf("calls        %s\n", callsPath)
	}
}

// judgeRun scores a sample of the run's conversations against the scenario's
// success criteria.
//
// It runs after the load, never during it: judging competes for nothing while
// calls are in flight, and a slow grader must not become the thing the run is
// measuring.
//
// It samples rather than judging everything. Each judgement is a model call, so
// grading all 76 calls of a sweep costs 76 of them for what is almost always
// the same verdict repeated. The default is one conversation per step, which is
// enough to catch an agent that behaves differently under load; -judge-calls 0
// grades all of them when that is the question being asked.
func judgeRun(ctx context.Context, o opts, sc *scenario.Scenario, rep *loadgen.Report, runID string) error {
	if len(sc.SuccessCriteria) == 0 {
		return fmt.Errorf("scenario %s defines no success_criteria, so there is nothing to judge", sc.Name)
	}

	sampled := sampleCalls(rep.Calls, o.judgeCalls)
	if len(sampled) == 0 {
		return fmt.Errorf("no completed conversations to judge")
	}

	model, describe, err := judgeModel(o)
	if err != nil {
		return err
	}

	fmt.Printf("\njudging      %d of %d conversations against %d criteria (%s)\n",
		len(sampled), len(rep.Calls), len(sc.SuccessCriteria), describe)

	j := judge.Judge{Model: model}

	judgements := make([]judge.Judgement, 0, len(sampled))
	for _, c := range sampled {
		var ex []judge.Exchange
		for _, t := range c.Turns {
			ex = append(ex, judge.Exchange{Turn: t.Turn, Caller: t.CallerText, Agent: t.AgentText})
		}
		g := j.Score(ctx, ex, sc.SuccessCriteria)
		g.Step, g.RequestID = c.Step, c.RequestID
		judgements = append(judgements, g)
	}

	path := filepath.Join(o.outDir, runID+"-judgements.json")
	b, err := json.MarshalIndent(judgements, "", "  ")
	if err != nil {
		return err
	}
	if err := os.WriteFile(path, b, 0o644); err != nil {
		return err
	}

	printJudgements(judgements, path)
	return nil
}

// judgeModel picks the grader and reports which one, because a verdict is only
// as readable as the thing that produced it.
//
// Groq is preferred when a key is present: it is an HTTP call that works from
// CI or a worker, and it constrains the reply to a schema. Claude Code runs on
// a Claude subscription with no API credits, but needs the CLI installed and
// logged in, so it is a laptop-only answer.
func judgeModel(o opts) (judge.Model, string, error) {
	backend := o.judgeBackend
	groqKey := os.Getenv("GROQ_API_KEY")

	if backend == "" || backend == "auto" {
		backend = "claude-code"
		if groqKey != "" {
			backend = "groq"
		}
	}

	switch backend {
	case "groq":
		if groqKey == "" {
			return nil, "", fmt.Errorf("judge-backend groq needs GROQ_API_KEY in %s or the environment", o.envPath)
		}
		model := o.judgeModel
		if model == "" {
			model = judge.DefaultGroqModel
		}
		return judge.Groq{APIKey: groqKey, Model: model}, "groq " + model, nil

	case "claude-code":
		model := o.judgeModel
		if model == "" {
			model = "claude-sonnet-5"
		}
		return judge.ClaudeCode{Model: model}, "claude code " + model, nil

	default:
		return nil, "", fmt.Errorf("unknown judge-backend %q: want groq, claude-code or auto", backend)
	}
}

// sampleCalls takes the first n conversations of each step. n <= 0 takes all.
func sampleCalls(calls []loadgen.CallRecord, n int) []loadgen.CallRecord {
	if n <= 0 {
		return calls
	}
	seen := map[string]int{}
	var out []loadgen.CallRecord
	for _, c := range calls {
		if seen[c.Step] >= n {
			continue
		}
		seen[c.Step]++
		out = append(out, c)
	}
	return out
}

func printJudgements(js []judge.Judgement, path string) {
	// Criteria are reported separately rather than as one score. "Passed 3 of
	// 4" says nothing useful; which one failed is the whole finding.
	type tally struct{ met, total, unjudged int }
	byCriterion := map[string]*tally{}
	var order []string

	failed, unjudged := 0, 0
	for _, g := range js {
		if g.Err != "" {
			unjudged++
			continue
		}
		if !g.Met() {
			failed++
		}
		for _, o := range g.Outcomes {
			t, ok := byCriterion[o.Criterion]
			if !ok {
				t = &tally{}
				byCriterion[o.Criterion] = t
				order = append(order, o.Criterion)
			}
			t.total++
			if o.Met {
				t.met++
			}
		}
	}

	fmt.Println()
	for _, c := range order {
		t := byCriterion[c]
		mark := "ok  "
		if t.met < t.total {
			mark = "FAIL"
		}
		fmt.Printf("%s  %d/%d  %s\n", mark, t.met, t.total, c)
	}

	judged := len(js) - unjudged
	fmt.Printf("\ntask success %d of %d conversations met every criterion\n", judged-failed, judged)
	if unjudged > 0 {
		// Kept apart from a failure: not knowing is not the same as the agent
		// getting it wrong.
		fmt.Printf("unjudged     %d conversations the judge could not score\n", unjudged)
		for _, g := range js {
			if g.Err != "" {
				fmt.Printf("             %s: %s\n", g.Step, g.Err)
				break
			}
		}
	}
	fmt.Printf("judgements   %s\n", path)
}

// writeCalls records every conversation the run produced, one JSON object per
// line.
//
// It is kept apart from the report card because the two are read for different
// reasons and grow at very different rates: the card stays a page whatever the
// concurrency, while this grows with every call placed. JSON Lines rather than
// one array so a judge, or a later analytics store, can stream a run instead of
// loading it whole.
func writeCalls(path string, calls []loadgen.CallRecord) error {
	if len(calls) == 0 {
		return nil
	}
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	defer f.Close()

	enc := json.NewEncoder(f)
	for _, c := range calls {
		if err := enc.Encode(c); err != nil {
			return err
		}
	}
	return f.Close()
}

func writeArtifacts(outDir, runID string, res *worker.Result) (jsonPath, wavPath string, err error) {
	if err = os.MkdirAll(outDir, 0o755); err != nil {
		return "", "", err
	}

	jsonPath = filepath.Join(outDir, runID+".json")
	b, err := json.MarshalIndent(res, "", "  ")
	if err != nil {
		return "", "", err
	}
	if err = os.WriteFile(jsonPath, b, 0o644); err != nil {
		return "", "", err
	}

	if len(res.Recording) == 0 {
		return jsonPath, "", nil
	}
	wavPath = filepath.Join(outDir, runID+".wav")
	if err = audio.WriteWAV(wavPath, res.Recording, res.SampleRate); err != nil {
		return "", "", err
	}
	return jsonPath, wavPath, nil
}

func printReport(res *worker.Result, jsonPath, wavPath string) {
	fmt.Printf("\n%s\n", strings.Repeat("-", 78))
	fmt.Printf("REPORT CARD  %s\n", res.Scenario)
	fmt.Printf("%s\n\n", strings.Repeat("-", 78))

	fmt.Printf("%-5s %-10s %-13s %-13s %-13s %s\n",
		"turn", "ttfa", "endpointing", "think+speak", "agent spoke", "turn latency")

	var ttfas, latencies []time.Duration
	completed, bargedOver := 0, 0
	var worstDrift time.Duration
	for _, t := range res.Turns {
		note := ""
		if t.FailReason != "" {
			note = "  " + t.FailReason
		}
		fmt.Printf("%-5d %-10s %-13s %-13s %-13s %s%s\n",
			t.Turn, ms(t.TTFA), ms(t.Endpointing), ms(t.ThinkSpeak),
			ms(t.AgentSpeech), ms(t.TurnLatency), note)

		if !t.Failed {
			completed++
		}
		if t.CallerYielded {
			bargedOver++
		}
		if abs(float64(t.PacingDrift)) > abs(float64(worstDrift)) {
			worstDrift = t.PacingDrift
		}
		if t.TTFA > 0 && !t.CallerYielded {
			ttfas = append(ttfas, t.TTFA)
		}
		if t.TurnLatency > 0 {
			latencies = append(latencies, t.TurnLatency)
		}
	}

	fmt.Println()
	printPercentiles("time to first audio", ttfas)
	printPercentiles("turn latency", latencies)

	fmt.Printf("\nturns completed     %d/%d\n", completed, len(res.Turns))
	if bargedOver > 0 {
		fmt.Printf("agent talked over   %d/%d turns\n", bargedOver, len(res.Turns))
	}
	fmt.Printf("harness drift       %s worst case (caller audio vs realtime)\n", ms(worstDrift))
	fmt.Printf("call duration       %.1fs\n", res.CallDurationMs/1000)
	fmt.Printf("agent audio         %.0f KB\n", res.AgentAudioKB)
	// Lines, not turns: a scenario with branches renders more lines than it
	// ever speaks, and reporting "7/5 turns" was nonsense on its face.
	fmt.Printf("caller audio cached %d/%d lines\n", res.SynthCacheHits, res.SynthLines)
	fmt.Printf("request id          %s\n", res.RequestID)
	fmt.Printf("\nevent log           %s  (%d events)\n", jsonPath, len(res.Events))
	if wavPath != "" {
		fmt.Printf("call recording      %s\n", wavPath)
	}
}

func printPercentiles(label string, ds []time.Duration) {
	if len(ds) == 0 {
		fmt.Printf("%-20s no samples\n", label)
		return
	}
	fmt.Printf("%-20s p50 %-9s p95 %-9s p99 %-9s max %s  (n=%d)\n",
		label,
		ms(metrics.Percentile(ds, 50)),
		ms(metrics.Percentile(ds, 95)),
		ms(metrics.Percentile(ds, 99)),
		ms(metrics.Percentile(ds, 100)),
		len(ds))
}

func abs(f float64) float64 {
	if f < 0 {
		return -f
	}
	return f
}

// ms renders a duration, keeping the sign. Only an exact zero means the
// instant was never observed.
func ms(d time.Duration) string {
	if d == 0 {
		return "-"
	}
	return fmt.Sprintf("%dms", d.Milliseconds())
}

func msf(f float64) string {
	if f == 0 {
		return "-"
	}
	return fmt.Sprintf("%.0fms", f)
}
