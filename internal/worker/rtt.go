package worker

import (
	"context"
	"sort"
	"sync"
	"time"
)

// pingInterval is how often a call measures its round trip. Once a second
// gives several samples per turn for a few bytes on a line already carrying
// 48KB of audio a second.
const pingInterval = time.Second

// rttSample is one ping: when it was sent, on the call's clock, and how long
// its pong took to come back.
type rttSample struct {
	sent time.Duration
	rtt  time.Duration
}

// rttLog is one call's round-trip samples, in the order they were sent.
type rttLog struct {
	mu      sync.Mutex
	samples []rttSample
}

func (l *rttLog) add(sent, rtt time.Duration) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.samples = append(l.samples, rttSample{sent: sent, rtt: rtt})
}

// median is the median round trip of pings sent between from and to. With no
// ping inside that window it falls back to the latest one sent before to.
//
// measured is false only when no pong had come back at all. It is reported
// apart from the value because zero is a real reading: a local run on Windows
// measured loopback round trips of exactly zero, below what the clock resolves,
// and reading those as "no pong" dropped a third of the run's turns from the
// correction.
func (l *rttLog) median(from, to time.Duration) (rtt time.Duration, measured bool) {
	l.mu.Lock()
	defer l.mu.Unlock()
	var in []time.Duration
	for _, s := range l.samples {
		if s.sent > to {
			continue
		}
		rtt, measured = s.rtt, true
		if s.sent >= from {
			in = append(in, s.rtt)
		}
	}
	if len(in) == 0 {
		return rtt, measured
	}
	sort.Slice(in, func(i, j int) bool { return in[i] < in[j] })
	return in[(len(in)-1)/2], true
}

// pingLoop measures the round trip to the agent's edge on the call's own
// connection, once a second for the life of the call.
//
// A WebSocket ping is answered by the server's WebSocket layer, not by the
// agent's logic, so its round trip is transport: the path between wherever this
// test runs and the agent's edge, out and back. It travels the same TCP stream
// as the caller's audio, so it waits in the same queues the audio does. And its
// pong is read by the same goroutine that stamps the agent's audio arriving, so
// the harness's own receive lag is inside it too and cancels when the round trip
// is subtracted. The send side needs nothing: the caller's end of speech is
// already stamped when its last frame was actually written.
//
// Pings go one at a time, each sent only once the last pong is back. A server
// that never answers therefore costs one ping and yields no samples, and its
// turns are reported uncorrected rather than corrected by a guess.
//
// One risk is accepted: the library gives a control frame's write five seconds
// and closes the connection if it blocks longer. A ping's write only blocks that
// long when the call's audio has been unable to leave for as long, and a call in
// that state is already failing.
func (w *worker) pingLoop(ctx context.Context) {
	t := time.NewTicker(pingInterval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
		sent := w.log.Since()
		if err := w.conn.Ping(ctx); err != nil {
			if ctx.Err() != nil {
				return
			}
			continue
		}
		w.rtt.add(sent, w.log.Since()-sent)
	}
}
