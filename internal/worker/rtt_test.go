package worker

import (
	"testing"
	"time"
)

func TestRTTMedianReadsTheTurnsOwnWindow(t *testing.T) {
	ms := time.Millisecond
	var l rttLog
	l.add(0, 80*ms) // before the turn
	l.add(1000*ms, 20*ms)
	l.add(2000*ms, 40*ms)
	l.add(3000*ms, 30*ms)
	l.add(5000*ms, 90*ms) // after the turn

	if got, ok := l.median(900*ms, 3500*ms); got != 30*ms || !ok {
		t.Errorf("median over the turn = %v %v, want 30ms measured: the middle of 20, 40 and 30, ignoring pings outside it", got, ok)
	}

	// A turn shorter than the ping interval has no ping inside it, and the
	// latest one before it stands in.
	if got, ok := l.median(3100*ms, 3200*ms); got != 30*ms || !ok {
		t.Errorf("median over an empty window = %v %v, want the latest earlier sample, 30ms measured", got, ok)
	}

	// A call whose server never answered has no samples: unmeasured.
	var none rttLog
	if got, ok := none.median(0, time.Second); got != 0 || ok {
		t.Errorf("median with no samples = %v %v, want 0 unmeasured", got, ok)
	}
}

func TestRTTMedianKeepsAZeroReading(t *testing.T) {
	// A pong came back faster than the clock resolves. That is a round trip of
	// zero, and it must not be read as a server that never answered.
	var l rttLog
	l.add(time.Second, 0)
	l.add(2*time.Second, 0)
	l.add(3*time.Second, 500*time.Microsecond)
	if got, ok := l.median(0, 4*time.Second); got != 0 || !ok {
		t.Errorf("median = %v %v, want 0 measured", got, ok)
	}
}
