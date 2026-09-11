// Command fakekafka runs an in-process Kafka broker for local development.
//
// It speaks the real Kafka wire protocol (it is the same implementation the
// bus tests run against), so the whole pipeline -- workers producing turn
// events, a collector consuming them, consumer lag as the backpressure signal
// -- can be exercised end to end without Docker or a JVM on the machine.
//
// It is not a production broker: everything lives in memory and dies with the
// process. Point -kafka at a real cluster for anything that matters.
package main

import (
	"context"
	"flag"
	"log"
	"os"
	"os/signal"
	"strings"

	"github.com/twmb/franz-go/pkg/kfake"

	"github.com/yakshgandhi/callstorm/internal/bus"
)

func main() {
	port := flag.Int("port", 9092, "port to listen on")
	topics := flag.String("topics", bus.DefaultTopic, "comma-separated topics to create at startup")
	partitions := flag.Int("partitions", 3, "partitions per topic")
	flag.Parse()

	names := strings.Split(*topics, ",")
	cluster, err := kfake.NewCluster(
		kfake.NumBrokers(1),
		kfake.Ports(*port),
		kfake.SeedTopics(int32(*partitions), names...),
	)
	if err != nil {
		log.Fatalf("start broker: %v", err)
	}
	defer cluster.Close()

	log.Printf("fakekafka listening on %s  topics=%s partitions=%d",
		strings.Join(cluster.ListenAddrs(), ","), *topics, *partitions)
	log.Printf("in-memory only: everything published here is lost on exit")

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	<-ctx.Done()
	log.Println("shutting down")
}
