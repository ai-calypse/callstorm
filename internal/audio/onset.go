package audio

import (
	"encoding/binary"
	"time"
)

// Perceived onset, as Coval defines it in its time-to-first-audio benchmark
// (September 2026): the start of the first 10ms window, stepped 1ms at a time,
// whose RMS on a [-1, 1] scale is above 0.01. Everything before it is leading
// silence. A provider can deliver its first audio byte quickly and still keep
// the caller waiting, if that audio opens with silence; Coval measured one
// sending a median of 225ms of it.
const (
	onsetWindow = 10 * time.Millisecond
	onsetHop    = time.Millisecond
	onsetRMS    = 0.01
)

// Onset finds where sound starts in a stream of 16-bit little-endian PCM fed
// to it in chunks of any size.
type Onset struct {
	win, hop int

	// limit is the sum of squared samples a window has to exceed: onsetRMS
	// scaled to 16-bit samples, squared, times the window length. Sums are kept
	// in integers so a long reply cannot drift.
	limit   int64
	squares []int64
	sum     int64
	n       int

	// odd holds the first byte of a sample split across two chunks.
	odd []byte

	start int
	found bool
}

// NewOnset returns a detector for PCM at sampleRate.
func NewOnset(sampleRate int) *Onset {
	win := max(sampleRate*int(onsetWindow/time.Millisecond)/1000, 1)
	hop := max(sampleRate*int(onsetHop/time.Millisecond)/1000, 1)
	amp := onsetRMS * 32768
	return &Onset{win: win, hop: hop, limit: int64(amp * amp * float64(win)), squares: make([]int64, win)}
}

// Write feeds the next chunk and reports whether sound has started by the end
// of it. Once it has, later chunks change nothing.
func (o *Onset) Write(pcm []byte) bool {
	if o.found {
		return true
	}
	if len(o.odd) == 1 {
		pcm = append([]byte{o.odd[0]}, pcm...)
		o.odd = nil
	}
	i := 0
	for ; i+1 < len(pcm); i += 2 {
		v := int64(int16(binary.LittleEndian.Uint16(pcm[i:])))
		k := o.n % o.win
		o.sum += v*v - o.squares[k]
		o.squares[k] = v * v
		o.n++
		if o.n >= o.win && (o.n-o.win)%o.hop == 0 && o.sum > o.limit {
			o.start, o.found = o.n-o.win, true
			return true
		}
	}
	if i < len(pcm) {
		o.odd = []byte{pcm[i]}
	}
	return false
}

// Start is the sample the first audible window begins at, and false while no
// window has been loud enough.
func (o *Onset) Start() (int, bool) {
	return o.start, o.found
}
