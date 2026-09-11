package bus

import (
	"context"
	"encoding/json"
	"fmt"
	"sync/atomic"

	"github.com/twmb/franz-go/pkg/kadm"
	"github.com/twmb/franz-go/pkg/kgo"
)

// Producer publishes turn events.
//
// Produces are fire-and-forget on purpose. A caller goroutine is holding a live
// WebSocket on a realtime audio deadline; blocking it to await a broker ack
// would delay the next audio frame and corrupt the measurement this event is
// reporting. Telemetry must never be able to slow down the thing it measures,
// so failures are counted and surfaced at the end of the run instead.
type Producer struct {
	client *kgo.Client
	topic  string

	produced atomic.Int64
	dropped  atomic.Int64
	lastErr  atomic.Value // error; a drop count with no reason is not diagnosable
}

func NewProducer(brokers []string, topic string) (*Producer, error) {
	if topic == "" {
		topic = DefaultTopic
	}
	cl, err := kgo.NewClient(
		kgo.SeedBrokers(brokers...),
		kgo.DefaultProduceTopic(topic),
		kgo.AllowAutoTopicCreation(),
	)
	if err != nil {
		return nil, fmt.Errorf("kafka producer: %w", err)
	}
	return &Producer{client: cl, topic: topic}, nil
}

// Publish enqueues one turn event. A nil Producer is a no-op, so the load
// generator needs no branch on whether Kafka is configured.
func (p *Producer) Publish(ctx context.Context, ev TurnEvent) {
	if p == nil {
		return
	}
	b, err := json.Marshal(ev)
	if err != nil {
		p.fail(err)
		return
	}
	// Keyed by step so every event for one concurrency level lands on one
	// partition and stays in order there.
	rec := &kgo.Record{Topic: p.topic, Key: []byte(ev.Step), Value: b}
	p.client.Produce(ctx, rec, func(_ *kgo.Record, err error) {
		if err != nil {
			p.fail(err)
			return
		}
		p.produced.Add(1)
	})
}

func (p *Producer) fail(err error) {
	p.dropped.Add(1)
	p.lastErr.Store(err)
}

// LastError is why events were dropped, if any were.
func (p *Producer) LastError() error {
	if p == nil {
		return nil
	}
	if v, ok := p.lastErr.Load().(error); ok {
		return v
	}
	return nil
}

// Flush waits for in-flight produces and reports what got through.
func (p *Producer) Flush(ctx context.Context) (produced, dropped int64) {
	if p == nil {
		return 0, 0
	}
	_ = p.client.Flush(ctx)
	return p.produced.Load(), p.dropped.Load()
}

func (p *Producer) Close() {
	if p != nil {
		p.client.Close()
	}
}

// Consumer reads turn events and can report how far behind it is.
type Consumer struct {
	client *kgo.Client
	admin  *kadm.Client
	topic  string
	group  string
}

func NewConsumer(brokers []string, topic, group string) (*Consumer, error) {
	if topic == "" {
		topic = DefaultTopic
	}
	if group == "" {
		group = "callstorm-collector"
	}
	cl, err := kgo.NewClient(
		kgo.SeedBrokers(brokers...),
		kgo.ConsumeTopics(topic),
		kgo.ConsumerGroup(group),
		kgo.ConsumeResetOffset(kgo.NewOffset().AtStart()),
	)
	if err != nil {
		return nil, fmt.Errorf("kafka consumer: %w", err)
	}
	return &Consumer{client: cl, admin: kadm.NewClient(cl), topic: topic, group: group}, nil
}

// Poll returns the next batch of turn events, blocking until ctx is done.
func (c *Consumer) Poll(ctx context.Context) ([]TurnEvent, error) {
	fetches := c.client.PollFetches(ctx)
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if errs := fetches.Errors(); len(errs) > 0 {
		return nil, fmt.Errorf("kafka fetch: %v", errs[0].Err)
	}

	var out []TurnEvent
	fetches.EachRecord(func(r *kgo.Record) {
		var ev TurnEvent
		if err := json.Unmarshal(r.Value, &ev); err == nil {
			out = append(out, ev)
		}
	})
	return out, nil
}

// Lag is how many events the collector has yet to read, summed over partitions.
// This is the backpressure signal: if it climbs during a run, the harness is
// falling behind and its numbers deserve suspicion.
func (c *Consumer) Lag(ctx context.Context) (int64, error) {
	lags, err := c.admin.Lag(ctx, c.group)
	if err != nil {
		return 0, err
	}
	var total int64
	for _, gl := range lags {
		for _, topics := range gl.Lag {
			for _, partition := range topics {
				if partition.Lag > 0 {
					total += partition.Lag
				}
			}
		}
	}
	return total, nil
}

func (c *Consumer) Close() {
	if c != nil {
		c.client.Close()
	}
}
