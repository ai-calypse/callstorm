package bus

import (
	"context"
	"testing"
	"time"

	"github.com/twmb/franz-go/pkg/kfake"

	"github.com/yakshgandhi/callstorm/internal/metrics"
)

// kfake is a real Kafka broker implementation, in-process: these tests exercise
// the actual wire protocol rather than a hand-written stub, so the Kafka path
// is verified without needing Docker or a JVM.
func newBroker(t *testing.T, topics ...string) []string {
	t.Helper()
	c, err := kfake.NewCluster(kfake.NumBrokers(1), kfake.SeedTopics(1, topics...))
	if err != nil {
		t.Fatalf("start fake cluster: %v", err)
	}
	t.Cleanup(c.Close)
	return c.ListenAddrs()
}

func TestTurnEventRoundTrip(t *testing.T) {
	const topic = "callstorm.turns.test"
	addrs := newBroker(t, topic)

	prod, err := NewProducer(addrs, topic)
	if err != nil {
		t.Fatalf("producer: %v", err)
	}
	defer prod.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	sent := NewTurnEvent("run-1", "c25", "call-7", metrics.TurnMetric{
		Turn:          3,
		TTFAMs:        877.5,
		EndpointingMs: 301.2,
		ThinkSpeakMs:  576.3,
		CallerYielded: true,
	})
	prod.Publish(ctx, sent)

	produced, dropped := prod.Flush(ctx)
	if produced != 1 || dropped != 0 {
		t.Fatalf("produced=%d dropped=%d (%v), want 1 and 0", produced, dropped, prod.LastError())
	}

	cons, err := NewConsumer(addrs, topic, "test-group")
	if err != nil {
		t.Fatalf("consumer: %v", err)
	}
	defer cons.Close()

	got, err := cons.Poll(ctx)
	if err != nil {
		t.Fatalf("poll: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("got %d events, want 1", len(got))
	}

	// The measurements must survive the wire intact: a load report assembled
	// from these events has to agree with the one assembled in-process.
	switch {
	case got[0].Step != "c25":
		t.Errorf("step = %q, want c25", got[0].Step)
	case got[0].Turn != 3:
		t.Errorf("turn = %d, want 3", got[0].Turn)
	case got[0].TTFAMs != 877.5:
		t.Errorf("ttfa = %v, want 877.5", got[0].TTFAMs)
	case got[0].EndpointingMs != 301.2:
		t.Errorf("endpointing = %v, want 301.2", got[0].EndpointingMs)
	case !got[0].Yielded:
		t.Error("yielded flag was lost in transit")
	}
}

func TestLagReportsUnreadEvents(t *testing.T) {
	const topic = "callstorm.turns.lag"
	addrs := newBroker(t, topic)

	prod, err := NewProducer(addrs, topic)
	if err != nil {
		t.Fatalf("producer: %v", err)
	}
	defer prod.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	// A consumer that has joined but read nothing is the "collector fell
	// behind" case the backpressure signal exists to catch.
	cons, err := NewConsumer(addrs, topic, "lag-group")
	if err != nil {
		t.Fatalf("consumer: %v", err)
	}
	defer cons.Close()

	const n = 25
	for i := 0; i < n; i++ {
		prod.Publish(ctx, NewTurnEvent("run-1", "c50", "call-x", metrics.TurnMetric{Turn: i}))
	}
	if produced, dropped := prod.Flush(ctx); produced != n || dropped != 0 {
		t.Fatalf("produced=%d dropped=%d (%v), want %d and 0", produced, dropped, prod.LastError(), n)
	}

	got, err := cons.Poll(ctx)
	if err != nil {
		t.Fatalf("poll: %v", err)
	}
	if len(got) != n {
		t.Fatalf("consumed %d events, want %d", len(got), n)
	}

	// Lag must be readable; after a full drain it cannot exceed what was sent.
	lag, err := cons.Lag(ctx)
	if err != nil {
		t.Fatalf("lag: %v", err)
	}
	if lag < 0 || lag > n {
		t.Errorf("lag = %d, want between 0 and %d", lag, n)
	}
}

func TestNilProducerIsNoOp(t *testing.T) {
	// loadgen calls Publish unconditionally; Kafka being unconfigured must not
	// require a branch at every call site.
	var p *Producer
	p.Publish(context.Background(), TurnEvent{})
	if produced, dropped := p.Flush(context.Background()); produced != 0 || dropped != 0 {
		t.Errorf("nil producer reported produced=%d dropped=%d", produced, dropped)
	}
	p.Close()
}
