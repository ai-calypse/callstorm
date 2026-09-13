package audio

import (
	"encoding/binary"
	"math"
	"testing"
)

func sine(samples, amplitude, sampleRate int) []byte {
	out := make([]byte, samples*2)
	for i := 0; i < samples; i++ {
		v := int16(float64(amplitude) * math.Sin(2*math.Pi*220*float64(i)/float64(sampleRate)))
		binary.LittleEndian.PutUint16(out[i*2:], uint16(v))
	}
	return out
}

func constant(samples, amplitude int) []byte {
	out := make([]byte, samples*2)
	for i := 0; i < samples; i++ {
		binary.LittleEndian.PutUint16(out[i*2:], uint16(int16(amplitude)))
	}
	return out
}

// 225ms of silence before a tone, the median Coval measured from one provider.
// The first window counted as sound overlaps the tone by a few samples, so the
// onset lands within one window before the tone's true start -- and in the same
// place however the audio was split into chunks, including across a sample.
func TestOnsetFindsTheSilenceBeforeTheFirstSound(t *testing.T) {
	const rate = 24000
	lead := rate * 225 / 1000
	pcm := append(make([]byte, lead*2), sine(rate/2, 6000, rate)...)

	var first int
	for i, chunk := range []int{len(pcm), 4800, 333} {
		o := NewOnset(rate)
		for off := 0; off < len(pcm); off += chunk {
			o.Write(pcm[off:min(off+chunk, len(pcm))])
		}
		start, ok := o.Start()
		if !ok {
			t.Fatalf("chunks of %d bytes: no onset found in audio that plainly has sound", chunk)
		}
		if start > lead || start < lead-rate/100 {
			t.Errorf("chunks of %d bytes: onset at sample %d, want within 10ms before %d", chunk, start, lead)
		}
		if i == 0 {
			first = start
		} else if start != first {
			t.Errorf("chunks of %d bytes: onset at sample %d, but %d when fed whole", chunk, start, first)
		}
	}
}

func TestOnsetAtTheStartOfAReplyIsZero(t *testing.T) {
	o := NewOnset(24000)
	if !o.Write(sine(2400, 6000, 24000)) {
		t.Fatal("no onset in a reply that starts with sound")
	}
	if start, _ := o.Start(); start != 0 {
		t.Errorf("onset at sample %d, want 0", start)
	}
}

// The line is RMS 0.01 of full scale, about 328 on 16-bit samples: a constant
// hum just under it is silence, and just over it is sound.
func TestOnsetHoldsTheLineAtOnePercent(t *testing.T) {
	for _, c := range []struct {
		amplitude int
		want      bool
	}{{0, false}, {300, false}, {340, true}} {
		o := NewOnset(24000)
		if got := o.Write(constant(24000, c.amplitude)); got != c.want {
			t.Errorf("amplitude %d: sound found %t, want %t", c.amplitude, got, c.want)
		}
	}
}
