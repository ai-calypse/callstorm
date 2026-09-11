package audio

import (
	"encoding/binary"
	"sync"
	"time"
)

// Recorder mixes both sides of a call into one mono track, each chunk placed
// at the instant it actually occurred. The silence between the caller
// finishing and the agent replying is therefore audible in the output -- which
// is the point. A recording where you can hear the latency is the cheapest
// possible check that the clock is telling the truth.
type Recorder struct {
	mu         sync.Mutex
	sampleRate int
	samples    []int16
}

func NewRecorder(sampleRate int) *Recorder {
	return &Recorder{sampleRate: sampleRate}
}

// Add mixes pcm into the track starting at offset at from call start.
//
// A nil Recorder is a no-op. Load runs disable recording entirely -- a mixed
// track costs ~2.9MB per minute per caller, which is over a gigabyte at 500
// concurrent callers -- so every call site would otherwise need a nil guard.
func (r *Recorder) Add(at time.Duration, pcm []byte) {
	if r == nil || len(pcm) < 2 {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()

	start := int(at.Seconds() * float64(r.sampleRate))
	if start < 0 {
		start = 0
	}
	n := len(pcm) / 2
	if need := start + n; need > len(r.samples) {
		grown := make([]int16, need)
		copy(grown, r.samples)
		r.samples = grown
	}
	for i := 0; i < n; i++ {
		v := int32(r.samples[start+i]) + int32(int16(binary.LittleEndian.Uint16(pcm[i*2:])))
		switch {
		case v > 32767:
			v = 32767
		case v < -32768:
			v = -32768
		}
		r.samples[start+i] = int16(v)
	}
}

// PCM returns the mixed track as signed 16-bit little-endian bytes. A nil
// Recorder returns nil.
func (r *Recorder) PCM() []byte {
	if r == nil {
		return nil
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]byte, len(r.samples)*2)
	for i, s := range r.samples {
		binary.LittleEndian.PutUint16(out[i*2:], uint16(s))
	}
	return out
}
