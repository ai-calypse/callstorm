// Package bus carries per-turn events from the workers placing calls to
// whatever is aggregating them.
//
// The volume is small -- a few thousand events per run -- so Kafka is not here
// for throughput. It is here for two properties that matter to a load test:
//
// Workers and the metrics sink scale independently. A worker fleet can grow
// without the collector growing with it, and a slow collector cannot push back
// on the callers and distort the very latencies being measured.
//
// Consumer lag is the backpressure signal. If the collector falls behind, lag
// grows, and that is the evidence that the harness -- not the agent under test
// -- has become the bottleneck. A load generator that cannot prove it was not
// the bottleneck is not measuring anything.
package bus

import (
	"time"

	"github.com/yakshgandhi/callstorm/internal/metrics"
)

// DefaultTopic carries per-turn events.
const DefaultTopic = "callstorm.turns"

// TurnEvent is one measured turn, flattened for transport. Durations travel as
// milliseconds so any consumer can read them without importing Go types.
type TurnEvent struct {
	Run  string    `json:"run"`
	Step string    `json:"step"`
	Call string    `json:"call"`
	Turn int       `json:"turn"`
	At   time.Time `json:"at"`

	TTFAMs        float64 `json:"ttfa_ms"`
	EndpointingMs float64 `json:"endpointing_ms"`
	ThinkSpeakMs  float64 `json:"think_speak_ms"`
	TurnLatencyMs float64 `json:"turn_latency_ms"`
	PacingDriftMs float64 `json:"pacing_drift_ms"`

	Failed  bool `json:"failed"`
	Yielded bool `json:"yielded"`
}

// NewTurnEvent flattens a measured turn for the wire.
func NewTurnEvent(run, step, call string, t metrics.TurnMetric) TurnEvent {
	return TurnEvent{
		Run: run, Step: step, Call: call, Turn: t.Turn, At: time.Now().UTC(),
		TTFAMs:        t.TTFAMs,
		EndpointingMs: t.EndpointingMs,
		ThinkSpeakMs:  t.ThinkSpeakMs,
		TurnLatencyMs: t.TurnLatencyMs,
		PacingDriftMs: t.PacingDriftMs,
		Failed:        t.Failed,
		Yielded:       t.CallerYielded,
	}
}
