// Command callstorm places synthetic calls against a voice agent and reports
// what the caller experienced.
//
// With -profile it runs a load profile and reports per-step percentiles;
// without one it places a single call and records it.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"math"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strings"
	"time"

	"github.com/yakshgandhi/callstorm/internal/audio"
	"github.com/yakshgandhi/callstorm/internal/bus"
	"github.com/yakshgandhi/callstorm/internal/chart"
	"github.com/yakshgandhi/callstorm/internal/config"
	"github.com/yakshgandhi/callstorm/internal/impair"
	"github.com/yakshgandhi/callstorm/internal/insights"
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
	ratePerMinute  float64
	distributed    bool
	judge          bool
	judgeCalls     int
	judgeBackend   string
	judgeModel     string
	impairments    string
	impairDev      string
	suite          string
	rejudge        string
	labels         string
	insightsPath   string
	insightsModel  string
	refresh        string
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
	flag.Float64Var(&o.ratePerMinute, "rate-per-minute", 0,
		"price a run at this cost per agent-minute (0 = report usage without dollars)")
	flag.BoolVar(&o.distributed, "distributed", false,
		"place calls through a worker fleet over Kafka instead of in this process")
	flag.BoolVar(&o.judge, "judge", false,
		"score sampled conversations against the scenario's success_criteria")
	flag.IntVar(&o.judgeCalls, "judge-calls", 1,
		"conversations to judge per step (0 = every call)")
	flag.StringVar(&o.judgeBackend, "judge-backend", "auto",
		"who judges: groq, claude-code, or auto (groq when GROQ_API_KEY is set)")
	flag.StringVar(&o.judgeModel, "judge-model", "", "model to judge with (default: the backend's own)")
	flag.StringVar(&o.impairments, "impairments", "",
		"file of netem profiles; runs the load profile once per profile, clean first (Linux, needs NET_ADMIN)")
	flag.StringVar(&o.impairDev, "impair-dev", "eth0", "interface the impairment is applied to")
	flag.StringVar(&o.suite, "suite", "",
		"name of the load test this run is part of; runs sharing it read as one test")
	flag.StringVar(&o.rejudge, "rejudge", "",
		"judge a run already placed, from its report and calls file, instead of placing calls; needs its -scenario")
	flag.StringVar(&o.labels, "labels", "",
		"with -rejudge: a person's corrected copy of a judgements file; reports how often the judge agrees")
	flag.StringVar(&o.insightsPath, "insights", "",
		"write the analysis of a run report, or of every suite and run in a directory, with Gemini, instead of placing calls")
	flag.StringVar(&o.insightsModel, "insights-model", insights.DefaultModel,
		"Gemini model that writes run analyses; analyses are written after every run when GEMINI_API_KEY is set")
	flag.StringVar(&o.refresh, "refresh", "",
		"recompute turn waits, wait bands, repeated replies and quality from the calls log of a report, or of every report in a directory, instead of placing calls")
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

	// -insights reads runs already on disk and places no calls, so it needs no
	// scenario.
	if o.insightsPath != "" {
		ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
		defer stop()
		return runInsights(ctx, o)
	}
	if o.refresh != "" {
		ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
		defer stop()
		return runRefresh(ctx, o)
	}

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

	if o.rejudge != "" {
		return rejudge(ctx, o, sc)
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

	var cohorts []impair.Profile
	if o.impairments != "" {
		switch {
		case o.distributed:
			// netem shapes this pod's interface. A distributed run's calls
			// leave from worker pods it cannot reach, so they would run on a
			// clean network while the report said otherwise.
			return fmt.Errorf("-impairments cannot run -distributed: the impairment is applied to " +
				"this machine's interface, and a fleet's calls leave from other machines")
		case o.kafkaBrokers != "":
			return fmt.Errorf("-impairments cannot publish -kafka turn events: step names repeat " +
				"in every cohort and the events carry no cohort, so a consumer could not tell them apart")
		case o.judge:
			return fmt.Errorf("-impairments cannot -judge yet: verdicts are joined to calls by step, " +
				"and step names repeat in every cohort")
		}
		if cohorts, err = impair.Load(o.impairments); err != nil {
			return err
		}
		if _, err := exec.LookPath("tc"); err != nil {
			return fmt.Errorf("-impairments needs tc from iproute2 and a Linux kernel with netem: %w", err)
		}
	}

	held := ""
	if h := profile.HeldSeconds(); h > 0 {
		held = fmt.Sprintf(" plus %.0fs held", h)
	}
	fmt.Printf("profile    %s  %d steps, %d calls%s, peak concurrency %d\n",
		profile.Name, len(profile.Steps), profile.TotalCalls(), held, profile.PeakConcurrency())

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

	lg := loadgen.Config{
		APIKey:        apiKey,
		Scenario:      sc,
		Profile:       profile,
		TTS:           tts.New(apiKey, o.sampleRate, o.cacheDir),
		SampleRate:    o.sampleRate,
		TurnTimeout:   o.turnTimeout,
		TargetURL:     o.targetURL,
		Out:           os.Stdout,
		Metrics:       mx,
		Bus:           producer,
		RunID:         runID,
		RatePerMinute: o.ratePerMinute,
	}

	var rep *loadgen.Report
	if len(cohorts) > 0 {
		names := make([]string, len(cohorts))
		for i, c := range cohorts {
			names[i] = c.Name
		}
		fmt.Printf("impair     %d cohorts on %s: %s\n", len(cohorts), o.impairDev, strings.Join(names, ", "))
		netem := impair.Netem{Device: o.impairDev}
		// Leave the interface as it was found, whatever happened to the run. On
		// a pod that exits this is tidiness; on a Linux workstation it is the
		// difference between a test and a broken network until reboot.
		defer func() {
			clearCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			if cmd, _, err := netem.Apply(clearCtx, impair.Profile{Name: impair.Clean}); err != nil {
				fmt.Fprintf(os.Stderr, "impair: could not restore %s with %s: %v\n", o.impairDev, cmd, err)
			}
		}()
		rep, err = loadgen.RunMatrix(ctx, lg, cohorts, o.impairDev, netem)
	} else if o.distributed {
		if o.kafkaBrokers == "" {
			return fmt.Errorf("-distributed needs -kafka: the fleet is reached over the bus")
		}
		d, derr := bus.NewDispatcher(ctx, strings.Split(o.kafkaBrokers, ","), runID)
		if derr != nil {
			return derr
		}
		defer d.Close()
		fmt.Printf("dispatch   %s  run=%s\n", o.kafkaBrokers, runID)
		rep, err = loadgen.RunDistributed(ctx, lg, d, runID)
	} else {
		rep, err = loadgen.Run(ctx, lg)
	}
	if err != nil {
		return err
	}

	rep.References = loadgen.References()
	rep.ScenarioHash = loadgen.Fingerprint(sc)
	rep.ProfileHash = loadgen.Fingerprint(profile)
	rep.Suite = o.suite

	if producer != nil {
		produced, dropped := producer.Flush(ctx)
		fmt.Printf("kafka      published %d turn events, dropped %d", produced, dropped)
		te := &loadgen.TurnEventIntegrity{Published: produced, Dropped: dropped}
		if err := producer.LastError(); err != nil {
			fmt.Printf(" (%v)", err)
			te.LastError = err.Error()
		}
		fmt.Println()
		if rep.Integrity == nil {
			rep.Integrity = &loadgen.Integrity{}
		}
		rep.Integrity.TurnEvents = te
	}

	// Judging runs before the artifacts are written, not after, so its verdicts
	// land inside the report rather than in a file beside it. The two numbers
	// it produces -- how badly the agent misheard, and whether it did the job
	// -- only mean something together, and anything that reads the report and
	// nothing else could not see the join while they lived apart.
	//
	// The directory is made first: the judge writes its own file into it, and
	// a run whose -out did not exist yet lost its verdicts to that write.
	if err := os.MkdirAll(o.outDir, 0o755); err != nil {
		return err
	}
	judgePath := ""
	if o.judge {
		var jerr error
		judgePath, jerr = judgeRun(ctx, o, sc, rep, runID)
		if jerr != nil {
			// A judge that could not run is not a failed load test. The
			// latency numbers stand on their own.
			fmt.Fprintf(os.Stderr, "judge: %v\n", jerr)
		}
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

	if rep.Judge != nil {
		printTaskSuccess(rep.Judge, judgePath)
	}

	// The analysis is written last, from the files just written, so it reads
	// exactly what the dashboard will show. A part of a suite also rewrites the
	// suite's analysis, so the test's reads every part placed so far.
	suites := map[string]string{}
	if o.suite != "" {
		suites[o.suite] = o.outDir
	}
	writeInsights(ctx, o, []string{jsonPath}, suites)

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
		note := ""
		if h := p.HeldSeconds(); h > 0 {
			// A held step places an unknown number of calls, so the count
			// above is a floor rather than the bill. Saying so is cheaper than
			// letting someone plan a spend against it.
			note = fmt.Sprintf(", plus however many fit in %.0fs of held steps", h)
		}
		fmt.Printf("cost       %d calls billed by Deepgram%s; caller audio is cached and free\n",
			p.TotalCalls(), note)
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

	printTransport(rep)
	printAudible(rep)
	printReferences(rep)
	printPhases(rep)
	printConversation(rep)
	printComponentShare(rep)
	printTurns(rep)
	printWaits(rep)
	printCost(rep)
	printNodes(rep)
	printQuality(rep)
	printMatrix(rep)
	printIntegrity(rep)

	if bp := rep.Breakpoint(); bp != nil {
		fmt.Printf("\nBREAKPOINT   %s at %d concurrent: p95 TTFA %s is %.2fx baseline\n",
			bp.Name, bp.Concurrency, msf(bp.TTFA.P95Ms), bp.P95Ratio)
	} else {
		fmt.Printf("\nBREAKPOINT   none reached; every step stayed inside 2x baseline\n")
	}
	if qb := rep.QualityBreakpoint(); qb != nil {
		fmt.Printf("QUALITY      %s at %d concurrent: %s\n", qb.Name, qb.Concurrency, qb.Quality.Reasons[0])
	} else {
		fmt.Printf("QUALITY      no step fell below the baseline's quality on what it compared\n")
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

// printConversation reports how the calls sounded rather than how fast they
// were. The benchmarks in the notes are the published ones, so a reader can
// tell an unusual agent from a normal one without having a fleet to compare
// against.
func printConversation(rep *loadgen.Report) {
	if len(rep.Steps) == 0 || rep.Steps[0].Conversation.Turns == 0 {
		return
	}
	fmt.Printf("\n%-12s %-9s %-10s %-11s %-12s %-7s %s\n",
		"step", "talk", "agent wpm", "caller wpm", "interrupts", "score", "dead air")
	for _, s := range rep.Steps {
		c := s.Conversation
		if c.Turns == 0 {
			continue
		}
		dead := "-"
		if c.DeadAirTurns > 0 {
			dead = fmt.Sprintf("%d turns, %.0fs", c.DeadAirTurns, c.DeadAirSeconds)
		}
		note := ""
		switch {
		case c.TalkRatio >= 0.80:
			note = "   <- agent holds 80%+ of the floor"
		case c.AgentWPM > 190:
			note = "   <- above comfortable listening pace"
		}
		fmt.Printf("%-12s %-9s %-10.0f %-11.0f %-12s %-7.2f %s%s\n",
			s.Name,
			fmt.Sprintf("%.0f%%", c.TalkRatio*100),
			c.AgentWPM, c.CallerWPM,
			fmt.Sprintf("%d/%d", c.Interruptions, c.Turns),
			c.InterruptionScore, dead, note)
	}
	for _, s := range rep.Steps {
		if c := s.Conversation; c.RepeatedReplies > 0 {
			fmt.Printf("%-12s   %d replies repeated an earlier reply in the same call, across %d calls\n",
				s.Name, c.RepeatedReplies, c.CallsWithRepeats)
		}
	}
}

// printAudible reports silence at the start of each reply. TTFA ends when the
// first audio byte arrives; a caller hears nothing until the first sound, and a
// voice that opens its reply with a pause keeps them waiting through it.
func printAudible(rep *loadgen.Report) {
	measured := false
	for _, s := range rep.Steps {
		if s.LeadingSilence.N > 0 {
			measured = true
		}
	}
	if !measured {
		return
	}
	// Zero is the good reading here, so it is printed rather than dashed.
	f := func(v float64) string { return fmt.Sprintf("%.0fms", v) }
	fmt.Printf("\naudible      silence at the start of each reply, and the wait until the first sound a caller could hear\n")
	fmt.Printf("%-12s %-13s %-13s %-12s %s\n", "step", "silence p50", "silence p95", "audible p50", "audible p95")
	for _, s := range rep.Steps {
		fmt.Printf("%-12s %-13s %-13s %-12s %s\n", s.Name,
			f(s.LeadingSilence.P50Ms), f(s.LeadingSilence.P95Ms), f(s.AudibleTTFA.P50Ms), f(s.AudibleTTFA.P95Ms))
	}
}

// printTurns reports the wait at each turn of the conversation, step by step.
// A step's percentiles pool every turn of every call, so a turn slow in every
// call is one slow turn among the rest there; by position it stands out.
func printTurns(rep *loadgen.Report) {
	most := 0
	for _, s := range rep.Steps {
		for _, t := range s.Turns {
			most = max(most, t.Turn)
		}
	}
	if most < 2 {
		return
	}
	most = min(most, 10)
	fmt.Printf("\nturns        p95 wait at each turn of the conversation; * marks the slowest turn in the step\n")
	fmt.Printf("%-12s", "step")
	for n := 1; n <= most; n++ {
		fmt.Printf(" %-9s", fmt.Sprintf("turn %d", n))
	}
	fmt.Println()
	slowest := ""
	for _, s := range rep.Steps {
		if len(s.Turns) == 0 {
			continue
		}
		slow := s.Turns[0]
		for _, t := range s.Turns {
			if t.TTFA.P95Ms > slow.TTFA.P95Ms {
				slow = t
			}
		}
		fmt.Printf("%-12s", s.Name)
		for n := 1; n <= most; n++ {
			cell := "-"
			for _, t := range s.Turns {
				if t.Turn == n {
					cell = fmt.Sprintf("%.0fms", t.TTFA.P95Ms)
					if n == slow.Turn {
						cell += "*"
					}
				}
			}
			fmt.Printf(" %-9s", cell)
		}
		fmt.Println()
		slowest = fmt.Sprintf("turn %d in %s, after the caller said %q", slow.Turn, s.Name, slow.CallerLine)
	}
	fmt.Printf("slowest      %s\n", slowest)
}

// printWaits reports how often a caller's wait crossed a line people notice:
// Hamming's 800ms target, Coval's 1,200ms, past which callers start repeating
// themselves, and two seconds of dead air.
func printWaits(rep *loadgen.Report) {
	counted := false
	for _, s := range rep.Steps {
		if s.Waits.Turns > 0 {
			counted = true
		}
	}
	if !counted {
		return
	}
	share := func(n, of int) string { return fmt.Sprintf("%.0f%%", 100*float64(n)/float64(of)) }
	fmt.Printf("\nwaits        share of turns by the caller's wait: Hamming's 800ms target, Coval's 1,200ms, and 2s of dead air\n")
	fmt.Printf("%-12s %-10s %-11s %-12s %s\n", "step", "<=800ms", "800-1200ms", "1200-2000ms", ">2000ms")
	for _, s := range rep.Steps {
		w := s.Waits
		if w.Turns == 0 {
			continue
		}
		fmt.Printf("%-12s %-10s %-11s %-12s %s\n", s.Name,
			share(w.UpTo800, w.Turns), share(w.To1200, w.Turns), share(w.To2000, w.Turns), share(w.Over2000, w.Turns))
	}
}

// printQuality reports each step's verdict on doing the job, beside the verdict
// on speed. A step can answer as fast as the baseline and still get the task
// wrong, mishear more, or cut callers off, and the latency verdict sees none of
// it. Each step says what it compared, because a pass that compared only failed
// turns says much less than one that compared everything.
func printQuality(rep *loadgen.Report) {
	if len(rep.Steps) == 0 || rep.Steps[0].Quality.Verdict == "" {
		return
	}
	fmt.Printf("\nquality      each step against the baseline: scenario checks, task success, words misheard, interruptions, failed turns\n")
	for _, s := range rep.Steps {
		q := s.Quality
		fmt.Printf("%-12s %-7s compared %s\n", s.Name, q.Verdict, strings.Join(q.Compared, ", "))
		for _, r := range q.Reasons {
			fmt.Printf("%-12s   %s\n", "", r)
		}
	}
}

// printComponentShare splits p95 TTFA into the two halves an agent builder can
// actually act on, and tracks the split across concurrency.
//
// The point is not the absolute numbers, which the percentile table already
// gives: it is which half grows. Endpointing that widens under load is a VAD or
// transport problem; think/speak that widens is the LLM or TTS queueing. A
// single TTFA number cannot tell those apart, and they have different fixes.
func printComponentShare(rep *loadgen.Report) {
	any := false
	for _, s := range rep.Steps {
		if s.Endpointing.N > 0 && s.ThinkSpeak.N > 0 {
			any = true
		}
	}
	if !any {
		return
	}

	fmt.Printf("\n%-12s %-11s %-13s %-13s %s\n",
		"step", "ttfa p95", "endpointing", "think/speak", "which half")
	var base float64
	for i, s := range rep.Steps {
		if s.Endpointing.N == 0 || s.ThinkSpeak.N == 0 {
			continue
		}
		total := s.Endpointing.P95Ms + s.ThinkSpeak.P95Ms
		if total <= 0 {
			continue
		}
		share := s.ThinkSpeak.P95Ms / total
		if i == 0 {
			base = share
		}
		// "Shifted" is the finding: a step whose split moved is a step where
		// one component saturated before the other.
		trend := fmt.Sprintf("%.0f%% think/speak", share*100)
		if d := share - base; math.Abs(d) >= 0.05 {
			trend += fmt.Sprintf("  (%+.0f pts vs baseline)", d*100)
		}
		fmt.Printf("%-12s %-11s %-13s %-13s %s\n",
			s.Name, msf(s.TTFA.P95Ms), msf(s.Endpointing.P95Ms), msf(s.ThinkSpeak.P95Ms), trend)
	}
}

// printCost reports billable minutes, and dollars only when -rate-per-minute
// supplied one. A run that quietly priced itself at a guessed rate would be
// worse than one that reports none.
func printCost(rep *loadgen.Report) {
	var minutes float64
	for _, s := range rep.Steps {
		minutes += s.Cost.AgentMinutes
	}
	if minutes <= 0 {
		return
	}
	rate := rep.Steps[0].Cost.RatePerMinute

	if rate <= 0 {
		fmt.Printf("\ncost         %.1f agent-minutes across the run; pass -rate-per-minute to price it\n",
			minutes)
		return
	}

	fmt.Printf("\n%-12s %-13s %-11s %-11s %s\n",
		"step", "agent-min", "$ / call", "$ / turn", "$ step")
	var total float64
	for _, s := range rep.Steps {
		c := s.Cost
		if c.AgentMinutes <= 0 {
			continue
		}
		total += c.USD
		// Every column carries four places so the step costs visibly sum to
		// the total. Rounding them for looks makes the arithmetic fail to
		// check, which is the one thing a cost table has to survive.
		fmt.Printf("%-12s %-13.2f %-11s %-11s %s\n",
			s.Name, c.AgentMinutes,
			fmt.Sprintf("$%.4f", c.USDPerCall),
			fmt.Sprintf("$%.4f", c.USDPerTurn),
			fmt.Sprintf("$%.4f", c.USD))
	}
	fmt.Printf("%-12s %-13.2f %-11s %-11s $%.4f\n", "total", minutes, "", "", total)
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
//
// The verdicts are attached to rep, so they travel with the report card. The
// returned path is the standalone judgements file, kept for anything already
// reading it.
func judgeRun(ctx context.Context, o opts, sc *scenario.Scenario, rep *loadgen.Report, runID string) (string, error) {
	if len(sc.SuccessCriteria) == 0 {
		return "", fmt.Errorf("scenario %s defines no success_criteria, so there is nothing to judge", sc.Name)
	}

	sampled := sampleCalls(rep.Calls, o.judgeCalls)
	if len(sampled) == 0 {
		return "", fmt.Errorf("no completed conversations to judge")
	}

	model, describe, err := judgeModel(o)
	if err != nil {
		return "", err
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
		g.JudgedAt = time.Now()
		judgements = append(judgements, g)
	}

	// The report carries the verdicts from here on. The standalone file stays
	// because it is what a script that already reads it expects, and because a
	// judge run is expensive enough that losing it to a later write failure
	// would be worth avoiding.
	rep.Judge = loadgen.SummarizeJudgements(sampled, judgements, describe, sc.SuccessCriteria)
	// Task success is one of the quality checks, so the verdicts change it.
	rep.ScoreQuality()

	path := filepath.Join(o.outDir, runID+"-judgements.json")
	b, err := json.MarshalIndent(judgements, "", "  ")
	if err != nil {
		return "", err
	}
	if err := os.WriteFile(path, b, 0o644); err != nil {
		return "", err
	}
	return path, nil
}

// printTaskSuccess reports what the judge found, and then the one question
// that needs both halves of the run at once: are the calls the agent misheard
// the calls it failed?
//
// That join is what makes word error rate worth reporting. Alone it is an
// accuracy statistic with no action attached; set against task outcomes it
// either points at the transcript as the thing to fix, or shows the failures
// are somewhere else and the WER figure is a distraction.
func printTaskSuccess(t *loadgen.TaskSuccess, path string) {
	fmt.Println()
	for _, m := range t.Misses {
		if m.Judged == 0 {
			continue
		}
		mark := "ok  "
		if m.Missed > 0 {
			mark = "FAIL"
		}
		fmt.Printf("%s  %d/%d  %s\n", mark, m.Judged-m.Missed, m.Judged, m.Criterion)
	}

	fmt.Printf("\ntask success %d of %d conversations met every criterion\n", t.Passed, t.Judged)
	if t.Errored > 0 {
		// Kept apart from a failure: not knowing is not the same as the agent
		// getting it wrong.
		fmt.Printf("unjudged     %d conversations the judge could not score\n", t.Errored)
		for _, g := range t.Judgements {
			if g.Err != "" {
				fmt.Printf("             %s: %s\n", g.Step, g.Err)
				break
			}
		}
	}
	if path != "" {
		fmt.Printf("judgements   %s\n", path)
	}

	x := t.Correlation
	if x.Calls == 0 {
		return
	}

	fmt.Printf("\nheard vs done  %d judged calls, split at %.0f%% word error rate\n",
		x.Calls, x.Threshold*100)
	fmt.Printf("  heard cleanly   %d calls, %d completed the task", x.CleanCalls, x.CleanPassed)
	if x.CleanCalls > 0 {
		fmt.Printf("  (%.0f%%)", x.CleanPassRate*100)
	}
	fmt.Println()
	fmt.Printf("  misheard        %d calls, %d completed the task", x.MisheardCalls, x.MisheardPassed)
	if x.MisheardCalls > 0 {
		fmt.Printf("  (%.0f%%)", x.MisheardPassRate*100)
	}
	fmt.Println()
	fmt.Printf("  median WER      %.1f%% on calls that passed, %.1f%% on calls that failed\n",
		x.MedianWERPassed*100, x.MedianWERFailed*100)

	if !x.Conclusive {
		fmt.Printf("  inconclusive: %s\n", x.Note)
		return
	}
	if x.Gap > 0 {
		fmt.Printf("  the calls it heard passed %.0f points more often. Fix the transcript first.\n",
			x.Gap*100)
	} else {
		fmt.Printf("  mishearing did not separate the failures here; they are somewhere else.\n")
	}
	fmt.Printf("  association only: nothing assigned which calls were misheard, and load\n")
	fmt.Printf("  degrades recognition and everything else at the same time.\n")
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
	seen := map[string]int{}
	var out []loadgen.CallRecord
	for _, c := range calls {
		// The calls log now holds failed calls too, and a call with no
		// conversation has nothing to grade.
		if c.Error != "" || len(c.Turns) == 0 {
			continue
		}
		if n > 0 && seen[c.Step] >= n {
			continue
		}
		seen[c.Step]++
		out = append(out, c)
	}
	return out
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

// rejudge judges a run that was already placed, from the report and calls file
// it wrote. A judge that could not run at the time -- an image without
// certificate roots, a provider outage -- should not cost the calls again.
func rejudge(ctx context.Context, o opts, sc *scenario.Scenario) error {
	b, err := os.ReadFile(o.rejudge)
	if err != nil {
		return err
	}
	var rep loadgen.Report
	if err := json.Unmarshal(b, &rep); err != nil {
		return fmt.Errorf("read %s: %w", o.rejudge, err)
	}
	// The script has to be the one the calls were placed with. The criteria may
	// have changed since -- rewording them is what rejudging is for -- so a
	// changed file is reported rather than refused.
	if rep.Scenario != sc.Name {
		return fmt.Errorf("%s was placed with scenario %s, not %s", o.rejudge, rep.Scenario, sc.Name)
	}
	if rep.ScenarioHash != "" && rep.ScenarioHash != loadgen.Fingerprint(sc) {
		fmt.Printf("scenario   %s has changed since this run; judging against its current criteria\n", o.scenarioPath)
	}
	dir := filepath.Dir(o.rejudge)
	runID := strings.TrimSuffix(filepath.Base(o.rejudge), ".json")
	if rep.Calls, err = readCalls(filepath.Join(dir, runID+"-calls.jsonl")); err != nil {
		return err
	}
	o.outDir = dir
	path, err := judgeRun(ctx, o, sc, &rep, runID)
	if err != nil {
		return err
	}
	out, err := json.MarshalIndent(&rep, "", "  ")
	if err != nil {
		return err
	}
	if err := os.WriteFile(o.rejudge, out, 0o644); err != nil {
		return err
	}
	printTaskSuccess(rep.Judge, path)
	// The verdicts changed, so an analysis written from the old ones is stale.
	suites := map[string]string{}
	if rep.Suite != "" {
		suites[rep.Suite] = dir
	}
	writeInsights(ctx, o, []string{o.rejudge}, suites)
	if o.labels != "" {
		return printAgreement(rep.Judge, o.labels)
	}
	return nil
}

// printAgreement compares the judge's verdicts with a person's: the check that
// says whether a pass rate from this judge can be believed at all.
func printAgreement(t *loadgen.TaskSuccess, labelsPath string) error {
	b, err := os.ReadFile(labelsPath)
	if err != nil {
		return err
	}
	var labels []judge.Judgement
	if err := json.Unmarshal(b, &labels); err != nil {
		return fmt.Errorf("read %s: %w", labelsPath, err)
	}
	var judged []judge.Judgement
	if t != nil {
		judged = t.Judgements
	}

	a := judge.Agree(judged, labels)
	fmt.Println()
	if a.Compared == 0 {
		fmt.Printf("agreement    nothing to compare: %s names no call and criterion the judge answered\n", labelsPath)
		return nil
	}
	fmt.Printf("agreement    %d of %d verdicts match the person's (%.0f%%, target %.0f%%)\n",
		a.Agreed, a.Compared, a.Rate()*100, judge.AgreementTarget*100)
	for _, c := range a.Criteria {
		fmt.Printf("  %d/%d  %s\n", c.Agreed, c.Compared, c.Criterion)
	}
	for _, d := range a.Disagreements {
		fmt.Printf("  differs  %s %s: judge %t, person %t -- %s\n           %s\n",
			d.Step, d.RequestID, d.Judge, d.Human, d.Criterion, d.Evidence)
	}
	if a.Rate() < judge.AgreementTarget {
		fmt.Printf("  below target: read the disagreements, reword the criterion or the prompt, and rejudge.\n")
	}
	return nil
}

// geminiModel returns the model that writes analyses, and false when there is
// no key: a run without one is not an error, only a run without an analysis.
func geminiModel(o opts) (insights.Model, bool) {
	key := os.Getenv("GEMINI_API_KEY")
	if key == "" {
		return nil, false
	}
	return insights.Gemini{APIKey: key, Model: o.insightsModel}, true
}

// writeInsights writes the analysis of each report, then of each suite, given
// as name to directory. An analysis that cannot be written is reported and
// does not fail the run: the measurements stand without it.
func writeInsights(ctx context.Context, o opts, reportPaths []string, suites map[string]string) {
	m, ok := geminiModel(o)
	if !ok {
		return
	}
	type job struct {
		d    insights.Digest
		path string
	}
	var jobs []job
	for _, p := range reportPaths {
		d, err := insights.ForRun(p)
		if err != nil {
			fmt.Fprintf(os.Stderr, "insights: %v\n", err)
			continue
		}
		jobs = append(jobs, job{d, insights.PathForRun(p)})
	}
	for suite, dir := range suites {
		d, err := insights.ForSuite(dir, suite)
		if err != nil {
			fmt.Fprintf(os.Stderr, "insights: %v\n", err)
			continue
		}
		jobs = append(jobs, job{d, insights.PathForSuite(dir, suite)})
	}
	for i, j := range jobs {
		if err := saveInsights(ctx, o, m, j.d, j.path); errors.Is(err, insights.ErrLimited) {
			// Analyses already written stay as they were, and a later run
			// skips every one that is up to date, so rerunning costs only the
			// ones left.
			fmt.Fprintf(os.Stderr, "insights: stopped with %d analyses not written; run -insights again once Gemini's limit resets\n", len(jobs)-i)
			return
		}
	}
}

// saveInsights writes one analysis. The error is returned only so a batch can
// stop at a rate limit; it has already been reported.
func saveInsights(ctx context.Context, o opts, m insights.Model, d insights.Digest, path string) error {
	if insights.Unchanged(path, o.insightsModel, d) {
		fmt.Printf("\ninsights     %s %s already analysed from this data with these instructions; kept as it is\n", d.Kind, d.Subject)
		return nil
	}
	fmt.Printf("\ninsights     writing the analysis of %s %s with %s\n", d.Kind, d.Subject, o.insightsModel)
	r, err := insights.Generate(ctx, m, o.insightsModel, d)
	if err != nil {
		fmt.Fprintf(os.Stderr, "insights: %s %s: %v\n", d.Kind, d.Subject, err)
		return err
	}
	if err := r.Write(path); err != nil {
		fmt.Fprintf(os.Stderr, "insights: %v\n", err)
		return err
	}
	fmt.Printf("insights     %d kept, %d dropped as unchecked or repeated  %s\n", len(r.Insights), len(r.Rejected), path)
	if r.Headline != "" {
		fmt.Printf("             %s\n", r.Headline)
	}
	for _, in := range r.Insights {
		fmt.Printf("  %-7s %s\n", in.Severity, in.Title)
	}
	for _, rj := range r.Rejected {
		fmt.Printf("  dropped %s: %s\n", rj.Title, rj.Reason)
	}
	return nil
}

// runInsights is -insights: the analysis of one report and its suite, or of
// every run and suite in a directory. It is how runs placed before analyses
// existed get one, and how every analysis is rewritten when the instructions
// change.
func runInsights(ctx context.Context, o opts) error {
	if _, ok := geminiModel(o); !ok {
		return fmt.Errorf("-insights needs GEMINI_API_KEY in %s or the environment", o.envPath)
	}
	info, err := os.Stat(o.insightsPath)
	if err != nil {
		return err
	}
	suites := map[string]string{}
	if !info.IsDir() {
		if d, err := insights.ForRun(o.insightsPath); err == nil {
			if s, _ := d.Runs[0].Report["suite"].(string); s != "" {
				suites[s] = filepath.Dir(o.insightsPath)
			}
		}
		writeInsights(ctx, o, []string{o.insightsPath}, suites)
		return nil
	}
	names, _ := insights.Suites(o.insightsPath)
	for _, s := range names {
		suites[s] = o.insightsPath
	}
	writeInsights(ctx, o, insights.Reports(o.insightsPath), suites)
	return nil
}

// runRefresh is -refresh: the figures a report gains after the load, recomputed
// from its calls log and written back, for one report or every report in a
// directory. Latency and its verdict stay as written. Analyses are rewritten
// afterwards when a key is set, since they read the figures that just changed.
func runRefresh(ctx context.Context, o opts) error {
	info, err := os.Stat(o.refresh)
	if err != nil {
		return err
	}
	paths := []string{o.refresh}
	if info.IsDir() {
		paths = insights.Reports(o.refresh)
	}
	var done []string
	suites := map[string]string{}
	for _, p := range paths {
		rep, err := refreshReport(p)
		if err != nil {
			fmt.Fprintf(os.Stderr, "refresh: %v\n", err)
			continue
		}
		done = append(done, p)
		if rep.Suite != "" {
			suites[rep.Suite] = filepath.Dir(p)
		}
		quality := "no step fell below the baseline's quality"
		if qb := rep.QualityBreakpoint(); qb != nil {
			quality = "quality fails at " + qb.Name
		}
		fmt.Printf("refresh      %s  %s\n", p, quality)
	}
	if len(done) == 0 {
		return fmt.Errorf("nothing in %s could be refreshed", o.refresh)
	}
	writeInsights(ctx, o, done, suites)
	return nil
}

func refreshReport(path string) (*loadgen.Report, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var rep loadgen.Report
	if err := json.Unmarshal(b, &rep); err != nil {
		return nil, fmt.Errorf("read %s: %w", path, err)
	}
	if rep.Calls, err = readCalls(strings.TrimSuffix(path, ".json") + "-calls.jsonl"); err != nil {
		return nil, fmt.Errorf("%s has no calls log to refresh from: %w", path, err)
	}
	loadgen.Refresh(&rep)
	out, err := json.MarshalIndent(&rep, "", "  ")
	if err != nil {
		return nil, err
	}
	return &rep, os.WriteFile(path, out, 0o644)
}

// readCalls reads a calls file back, one conversation per line.
func readCalls(path string) ([]loadgen.CallRecord, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	var calls []loadgen.CallRecord
	dec := json.NewDecoder(f)
	for dec.More() {
		var c loadgen.CallRecord
		if err := dec.Decode(&c); err != nil {
			return nil, fmt.Errorf("read %s: %w", path, err)
		}
		calls = append(calls, c)
	}
	return calls, nil
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

// printTransport separates what the caller waited through from what the agent
// took. Each call pings the agent over its own connection, and subtracting that
// round trip from TTFA leaves an estimate of the agent's share: the same agent
// tested from two places gets two observed TTFAs and one agent TTFA. Think/speak
// needs no correction, since both of its instants arrive over the same downlink.
func printTransport(rep *loadgen.Report) {
	measured := false
	for _, s := range rep.Steps {
		if s.AgentTTFA.N > 0 {
			measured = true
		}
	}
	if !measured {
		return
	}
	fmt.Printf("\nnetwork      observed TTFA, the round trip measured on each call's connection, and the agent's share\n")
	fmt.Printf("%-12s %-13s %-13s %-10s %-11s %-11s %s\n",
		"step", "observed p50", "observed p95", "rtt p50", "agent p50", "agent p95", "corrected")
	for _, s := range rep.Steps {
		fmt.Printf("%-12s %-13s %-13s %-10s %-11s %-11s %d of %d turns\n", s.Name,
			msf(s.TTFA.P50Ms), msf(s.TTFA.P95Ms), msf(s.TransportRTT.P50Ms),
			msf(s.AgentTTFA.P50Ms), msf(s.AgentTTFA.P95Ms), s.AgentTTFA.N, s.TTFA.N)
	}
}

// printReferences reports every step on two clocks and reads the published
// reference lines against the clock each was defined on.
//
// Vendors publish latency thresholds under the same names measured between
// different instants, so a threshold is only readable on the clock it was
// defined against. Callstorm records two: from the caller's true end of speech,
// and from the agent's transcript of the caller. None of the lines is the
// verdict; the verdict above stays relative to the run's own baseline.
func printReferences(rep *loadgen.Report) {
	fmt.Printf("\ntwo clocks   from the caller's true end of speech (TTFA), and from the agent's transcript of the caller (think/speak)\n")
	fmt.Printf("%-12s %-12s %-12s %-12s %s\n", "step", "speech p50", "speech p95", "detect p50", "detect p95")
	for _, s := range rep.Steps {
		fmt.Printf("%-12s %-12s %-12s %-12s %s\n", s.Name,
			msf(s.TTFA.P50Ms), msf(s.TTFA.P95Ms), msf(s.ThinkSpeak.P50Ms), msf(s.ThinkSpeak.P95Ms))
	}
	if len(rep.References) == 0 {
		return
	}

	fmt.Printf("\nreference    published lines, each read on its own clock; none of them is the verdict\n")
	for _, r := range rep.References {
		line := fmt.Sprintf("%.0fms", r.Ms)
		if r.ToMs > 0 {
			line = fmt.Sprintf("%.0f-%.0fms", r.Ms, r.ToMs)
		}
		clock := "speech"
		if r.Axis == loadgen.AxisDetection {
			clock = "detect"
		}
		cite := ""
		if len(r.Cites) > 0 {
			cite = fmt.Sprintf("%s, %s", r.Cites[0].Source, r.Cites[0].Published)
		}
		fmt.Printf("%-12s %s %s on %s  (%s)\n", r.Kind, r.Label, line, clock, cite)

		var cells []string
		for _, s := range rep.Steps {
			for _, m := range loadgen.Place(r, s) {
				side := "under"
				if m.Over {
					side = "over"
				}
				cells = append(cells, fmt.Sprintf("%s p%d %s %s", s.Name, m.Percentile, msf(m.ValueMs), side))
			}
		}
		if len(cells) == 0 {
			cells = []string{"no samples on this clock"}
		}
		fmt.Printf("%-12s %s\n", "", strings.Join(cells, " · "))
	}
}

// printPhases reports the shape of each step, which every other number on the
// page flattens away.
//
// A step report is one figure per metric for the whole step, and that quietly
// assumes the step was the same thing from start to finish. Two failures hide
// in that assumption: an agent that degrades slowly while the load never
// changes, and an agent that absorbs a sudden jump but takes a while to do it.
// Splitting each step in half by start time and comparing the medians is what
// makes both visible.
func printPhases(rep *loadgen.Report) {
	if !rep.HasPhases() {
		return
	}
	fmt.Printf("\nphase shape   each step split in half by when its calls started, medians compared\n")
	fmt.Printf("%-12s %-10s %-8s %-11s %-11s %-9s %s\n",
		"step", "phase", "held", "first half", "second half", "drift", "reading")
	for _, s := range rep.Steps {
		p := s.Phase
		drift, reading := "-", ""
		switch p.Drift {
		case loadgen.DriftUnknown:
			reading = "too few turns to compare halves"
		default:
			drift = fmt.Sprintf("%+.0f%%", p.DriftPct*100)
			reading = p.Drift
		}
		if p.Kind == loadgen.KindRecovery {
			if p.Recovered {
				reading += "   <- back to baseline"
			} else {
				reading += fmt.Sprintf("   <- still %.2fx baseline, it did not come back", s.P95Ratio)
			}
		}
		fmt.Printf("%-12s %-10s %-8s %-11s %-11s %-9s %s\n",
			s.Name, p.Kind, fmt.Sprintf("%.0fs", p.HeldSeconds),
			msf(p.FirstHalf.P50Ms), msf(p.SecondHalf.P50Ms), drift, reading)
	}

	if drifted := rep.Drifted(); len(drifted) > 0 {
		names := make([]string, 0, len(drifted))
		for _, s := range drifted {
			names = append(names, s.Name)
		}
		fmt.Printf("\nDRIFT        %s got slower over its own duration while the load never changed.\n",
			strings.Join(names, ", "))
		fmt.Printf("             That is the failure a single percentile cannot show: a leak, a\n")
		fmt.Printf("             filling queue or a cache going cold looks fine in any one snapshot.\n")
	}
}

// printNodes reports each scenario node's assertion pass rate per step.
//
// A call-level number says the agent got worse and not where. A node that
// collapses while the rest of the conversation holds is the diagnosis that
// pairs with the latency curve, and it is only visible per node.
func printNodes(rep *loadgen.Report) {
	views := []struct {
		name  string
		steps []loadgen.StepReport
	}{{"", rep.Steps}}
	if rep.Matrix != nil {
		views = views[:0]
		for _, c := range rep.Matrix.Cohorts {
			views = append(views, struct {
				name  string
				steps []loadgen.StepReport
			}{c.Impairment.Name, c.Steps})
		}
	}

	checked := false
	for _, v := range views {
		for _, s := range v.steps {
			for _, n := range s.Nodes {
				if n.Checked > 0 {
					checked = true
				}
			}
		}
	}
	if !checked {
		return
	}

	fmt.Printf("\nnodes        share of visits whose reply met the node's expect, visits in brackets\n")
	for _, v := range views {
		if len(v.steps) == 0 || len(v.steps[0].Nodes) == 0 {
			continue
		}
		label := "node"
		if v.name != "" {
			label = v.name
		}
		fmt.Printf("%-16s", label)
		for _, s := range v.steps {
			fmt.Printf(" %-14s", s.Name)
		}
		fmt.Println()
		for i, n := range v.steps[0].Nodes {
			fmt.Printf("  %-14s", n.Node)
			for _, s := range v.steps {
				if i >= len(s.Nodes) {
					fmt.Printf(" %-14s", "-")
					continue
				}
				sn := s.Nodes[i]
				cell := fmt.Sprintf("(%d)", sn.Visits)
				if sn.Checked > 0 {
					cell = fmt.Sprintf("%.0f%% (%d)", sn.PassRate*100, sn.Visits)
				}
				fmt.Printf(" %-14s", cell)
			}
			fmt.Println()
		}
	}
}

// printMatrix reports every cohort's p95 against the same step on the clean
// network, and the command that made each network what it was.
func printMatrix(rep *loadgen.Report) {
	m := rep.Matrix
	if m == nil || len(m.Cohorts) == 0 {
		return
	}
	fmt.Printf("\nimpairment   p95 TTFA per cohort, and its ratio to the same step on the clean network\n")
	fmt.Printf("%-10s %-24s", "cohort", "network")
	for _, s := range m.Cohorts[0].Steps {
		fmt.Printf(" %-15s", s.Name)
	}
	fmt.Printf(" %s\n", "breaks at")
	for _, c := range m.Cohorts {
		fmt.Printf("%-10s %-24s", c.Impairment.Name, describeNetwork(c.Impairment))
		for _, s := range c.Steps {
			cell := msf(s.TTFA.P95Ms)
			if s.VsClean > 0 {
				cell += fmt.Sprintf(" %.2fx", s.VsClean)
			}
			fmt.Printf(" %-15s", cell)
		}
		fmt.Printf(" %s\n", dash(c.Breakpoint))

		// The same cohort with each turn's round trip taken out. On a network
		// that only adds delay, this row should match the clean one.
		corrected := false
		for _, s := range c.Steps {
			if s.AgentTTFA.N > 0 {
				corrected = true
			}
		}
		if corrected {
			fmt.Printf("%-10s %-24s", "", "  agent p95, rtt removed")
			for _, s := range c.Steps {
				fmt.Printf(" %-15s", msf(s.AgentTTFA.P95Ms))
			}
			fmt.Println()
		}
	}
	fmt.Printf("\n%-10s applied to %s, egress only; verdicts scored against the clean baseline\n", "tc", m.Device)
	for _, c := range m.Cohorts {
		fmt.Printf("%-10s %s\n", c.Impairment.Name, c.Command)
	}
}

func describeNetwork(p impair.Profile) string {
	var parts []string
	if p.DelayMs > 0 || p.JitterMs > 0 {
		d := fmt.Sprintf("+%gms", p.DelayMs)
		if p.JitterMs > 0 {
			d += fmt.Sprintf(" ±%gms", p.JitterMs)
		}
		parts = append(parts, d)
	}
	if p.LossPct > 0 {
		parts = append(parts, fmt.Sprintf("%g%% loss", p.LossPct))
	}
	if len(parts) == 0 {
		return "none"
	}
	return strings.Join(parts, ", ")
}

// printIntegrity reports whether the dispatch pipeline delivered every call it
// sent. It checks the harness, not the target: a lost or doubled result moves
// a step's percentiles as surely as the agent does.
func printIntegrity(rep *loadgen.Report) {
	if rep.Integrity == nil || rep.Integrity.Dispatch == nil {
		return
	}
	d := rep.Integrity.Dispatch
	fmt.Printf("\ndispatch     %d dispatched, %d received, %d duplicates dropped, %d missing, %d late\n",
		d.Dispatched, d.Received, d.Duplicates, d.Missing, d.Late)
	for _, s := range d.Steps {
		if s.Duplicates > 0 || s.Missing > 0 || s.Late > 0 {
			fmt.Printf("%-12s %d duplicates, %d missing, %d late from earlier steps\n",
				s.Step, s.Duplicates, s.Missing, s.Late)
		}
	}
}

// dash renders an empty grade as something rather than as nothing, so a column
// of blanks cannot be mistaken for a column of good news.
func dash(s string) string {
	if s == "" {
		return "-"
	}
	return s
}
