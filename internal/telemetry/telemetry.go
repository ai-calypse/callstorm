// Package telemetry exposes a live view of a run over Prometheus.
//
// The CLI report card only exists once a run finishes, which is no use during
// a thirty-minute soak or when you want to watch the moment an agent starts to
// buckle. These are the same measurements, published as they happen.
package telemetry

import (
	"context"
	"errors"
	"net/http"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/collectors"
	"github.com/prometheus/client_golang/prometheus/promhttp"

	"github.com/yakshgandhi/callstorm/internal/metrics"
)

// latencyBuckets are chosen for conversation, not for HTTP. The published
// guidance treats sub-second as good and anything past two seconds as
// noticeably broken, so the resolution is concentrated where that judgement
// gets made rather than spread evenly.
//
// The 350ms-1s band is deliberately fine. A quantile read back off a histogram
// can only be interpolated within whichever bucket it falls in, so a coarse
// bucket there reports a healthy agent as a visibly slower one: across a
// 0.5-0.75 gap, a run whose true p95 was 504ms was drawn at 738ms. 50ms steps
// bound that error to the width of one bucket. The exact percentiles still
// come from the run report, which sorts the real durations; these are for
// watching a run happen.
var latencyBuckets = []float64{
	0.1, 0.2, 0.3,
	0.35, 0.4, 0.45, 0.5, 0.55, 0.6, 0.65, 0.7, 0.75, 0.8, 0.85, 0.9, 0.95, 1,
	1.25, 1.5, 2, 3, 5, 10,
}

// driftBuckets cover the harness's own pacing error. One 20ms frame is normal;
// past ~100ms the load generator, not the agent, is what is being measured.
var driftBuckets = []float64{0.005, 0.01, 0.02, 0.05, 0.1, 0.25, 0.5, 1}

type Metrics struct {
	TTFA        *prometheus.HistogramVec
	Endpointing *prometheus.HistogramVec
	ThinkSpeak  *prometheus.HistogramVec
	TurnLatency *prometheus.HistogramVec
	Drift       *prometheus.HistogramVec

	ActiveCalls prometheus.Gauge
	Calls       *prometheus.CounterVec
	Turns       *prometheus.CounterVec

	registry *prometheus.Registry
}

func New() *Metrics {
	reg := prometheus.NewRegistry()
	// Go runtime collectors earn their place here: a soak test that leaks
	// goroutines or heap shows up on the same dashboard as the latency it
	// was supposed to be measuring.
	reg.MustRegister(collectors.NewGoCollector(), collectors.NewProcessCollector(collectors.ProcessCollectorOpts{}))

	hist := func(name, help string, buckets []float64) *prometheus.HistogramVec {
		h := prometheus.NewHistogramVec(prometheus.HistogramOpts{
			Namespace: "callstorm", Name: name, Help: help, Buckets: buckets,

			// Native histogram buckets grow by a fixed ratio instead of
			// sitting at fixed boundaries, so a quantile resolves to about 1%
			// of its own value rather than to the width of whichever bucket it
			// happened to land in. Only populated buckets are stored, and a
			// run's latencies cluster tightly, so the resolution is close to
			// free. The classic buckets above stay exposed alongside them:
			// they cost little and keep the heatmap and any Prometheus that
			// is not scraping native histograms working.
			NativeHistogramBucketFactor:     1.01,
			NativeHistogramMaxBucketNumber:  160,
			NativeHistogramMinResetDuration: time.Hour,
		}, []string{"step"})
		reg.MustRegister(h)
		return h
	}

	m := &Metrics{
		registry:    reg,
		TTFA:        hist("ttfa_seconds", "Caller stopped speaking to agent's first audio.", latencyBuckets),
		Endpointing: hist("endpointing_seconds", "Caller stopped speaking to the agent finalizing their turn.", latencyBuckets),
		ThinkSpeak:  hist("think_speak_seconds", "Agent's turn finalized to its first audio.", latencyBuckets),
		TurnLatency: hist("turn_latency_seconds", "Caller stopped speaking to the agent finishing playout.", latencyBuckets),
		Drift:       hist("harness_drift_seconds", "How far the caller's audio departed from realtime. Measures the harness, not the agent.", driftBuckets),
		ActiveCalls: prometheus.NewGauge(prometheus.GaugeOpts{
			Namespace: "callstorm", Name: "active_calls", Help: "Calls currently in progress.",
		}),
		Calls: prometheus.NewCounterVec(prometheus.CounterOpts{
			Namespace: "callstorm", Name: "calls_total", Help: "Calls placed, by outcome.",
		}, []string{"step", "outcome"}),
		Turns: prometheus.NewCounterVec(prometheus.CounterOpts{
			Namespace: "callstorm", Name: "turns_total", Help: "Turns taken, by outcome.",
		}, []string{"step", "outcome"}),
	}
	reg.MustRegister(m.ActiveCalls, m.Calls, m.Turns)
	return m
}

// CallStarted and CallEnded bracket one call so active_calls is accurate even
// when a call dies mid-conversation.
func (m *Metrics) CallStarted() {
	if m != nil {
		m.ActiveCalls.Inc()
	}
}

func (m *Metrics) CallEnded(step string, err error) {
	if m == nil {
		return
	}
	m.ActiveCalls.Dec()
	outcome := "ok"
	if err != nil {
		outcome = "error"
	}
	m.Calls.WithLabelValues(step, outcome).Inc()
}

// ObserveTurn records one finished turn.
func (m *Metrics) ObserveTurn(step string, t metrics.TurnMetric) {
	if m == nil {
		return
	}

	switch {
	case t.Failed:
		m.Turns.WithLabelValues(step, "failed").Inc()
	case t.CallerYielded:
		m.Turns.WithLabelValues(step, "talked_over").Inc()
	default:
		m.Turns.WithLabelValues(step, "ok").Inc()
	}

	// Drift is recorded for every turn that produced one, because harness
	// health is worth watching even on turns whose latency is discarded.
	if t.PacingDrift != 0 {
		m.Drift.WithLabelValues(step).Observe(abs(t.PacingDrift).Seconds())
	}

	// A turn the agent talked over has no meaningful response latency.
	if t.CallerYielded {
		return
	}
	observe(m.TTFA, step, t.TTFA)
	observe(m.Endpointing, step, t.Endpointing)
	observe(m.ThinkSpeak, step, t.ThinkSpeak)
	observe(m.TurnLatency, step, t.TurnLatency)
}

func observe(h *prometheus.HistogramVec, step string, d time.Duration) {
	if d > 0 {
		h.WithLabelValues(step).Observe(d.Seconds())
	}
}

// Serve exposes /metrics until ctx is cancelled.
func (m *Metrics) Serve(ctx context.Context, addr string) error {
	mux := http.NewServeMux()
	mux.Handle("/metrics", promhttp.HandlerFor(m.registry, promhttp.HandlerOpts{}))
	srv := &http.Server{Addr: addr, Handler: mux}

	go func() {
		<-ctx.Done()
		shutdown, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		_ = srv.Shutdown(shutdown)
	}()

	if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		return err
	}
	return nil
}

func abs(d time.Duration) time.Duration {
	if d < 0 {
		return -d
	}
	return d
}
