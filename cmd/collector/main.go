// Command collector consumes per-turn events from Kafka and aggregates them.
//
// It exists to make one claim checkable. A load generator's numbers are only
// worth reading if the harness was not itself the bottleneck, and the evidence
// for that is consumer lag: if the collector keeps up while callers are placing
// calls, the measurement pipeline had headroom. If lag climbs, it did not, and
// the run's latencies deserve suspicion.
//
// It also scales independently of the workers, which is the other reason the
// events go through a broker rather than straight into the report.
package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"os"
	"os/signal"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/yakshgandhi/callstorm/internal/bus"
	"github.com/yakshgandhi/callstorm/internal/metrics"
)

func main() {
	brokers := flag.String("kafka", "localhost:9092", "comma-separated Kafka brokers")
	topic := flag.String("topic", bus.DefaultTopic, "topic to consume")
	group := flag.String("group", "callstorm-collector", "consumer group")
	every := flag.Duration("report-every", 10*time.Second, "how often to print a summary")
	flag.Parse()

	cons, err := bus.NewConsumer(strings.Split(*brokers, ","), *topic, *group)
	if err != nil {
		log.Fatal(err)
	}
	defer cons.Close()

	log.Printf("collector consuming %s from %s", *topic, *brokers)

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()

	agg := &aggregator{byStep: map[string][]time.Duration{}}

	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		ticker := time.NewTicker(*every)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				lag, err := cons.Lag(ctx)
				if err != nil {
					lag = -1
				}
				agg.report(lag)
			}
		}
	}()

	for ctx.Err() == nil {
		events, err := cons.Poll(ctx)
		if err != nil {
			break
		}
		agg.add(events)
	}

	wg.Wait()
	lag, _ := cons.Lag(context.Background())
	fmt.Println("\nfinal:")
	agg.report(lag)
}

type aggregator struct {
	mu     sync.Mutex
	byStep map[string][]time.Duration
	total  int
	failed int
}

func (a *aggregator) add(events []bus.TurnEvent) {
	a.mu.Lock()
	defer a.mu.Unlock()
	for _, e := range events {
		a.total++
		if e.Failed {
			a.failed++
		}
		// A turn the agent talked over has no meaningful response latency.
		if e.Yielded || e.TTFAMs <= 0 {
			continue
		}
		a.byStep[e.Step] = append(a.byStep[e.Step],
			time.Duration(e.TTFAMs*float64(time.Millisecond)))
	}
}

func (a *aggregator) report(lag int64) {
	a.mu.Lock()
	defer a.mu.Unlock()

	steps := make([]string, 0, len(a.byStep))
	for s := range a.byStep {
		steps = append(steps, s)
	}
	sort.Strings(steps)

	lagText := fmt.Sprintf("%d", lag)
	if lag < 0 {
		lagText = "unknown"
	}
	fmt.Printf("  events=%d failed=%d consumer_lag=%s\n", a.total, a.failed, lagText)
	for _, s := range steps {
		ds := a.byStep[s]
		fmt.Printf("    %-10s n=%-4d ttfa p50 %-8s p95 %-8s p99 %s\n", s, len(ds),
			ms(metrics.Percentile(ds, 50)),
			ms(metrics.Percentile(ds, 95)),
			ms(metrics.Percentile(ds, 99)))
	}
}

func ms(d time.Duration) string { return fmt.Sprintf("%dms", d.Milliseconds()) }
