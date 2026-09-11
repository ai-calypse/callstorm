package audio

import (
	"encoding/binary"
	"testing"
	"time"
)

func TestFrameBytesIsExactlyRealtime(t *testing.T) {
	// A frame must hold exactly FrameDuration of audio. If it does not, the
	// pump's one-frame-per-tick loop drifts away from realtime and every
	// latency number in the run is wrong by a growing amount.
	for _, rate := range []int{8000, 16000, 24000, 48000} {
		got := Duration(make([]byte, FrameBytes(rate)), rate)
		if got != FrameDuration {
			t.Errorf("rate %d: frame holds %v, want %v", rate, got, FrameDuration)
		}
	}
}

func TestDuration(t *testing.T) {
	// One second of 24kHz 16-bit mono is 48000 bytes.
	if got := Duration(make([]byte, 48000), 24000); got != time.Second {
		t.Errorf("got %v, want 1s", got)
	}
}

func TestRecorderPlacesAudioAtItsTimestamp(t *testing.T) {
	const rate = 24000
	r := NewRecorder(rate)

	// One sample of silence-free tone dropped in at exactly 1s.
	chunk := make([]byte, 2)
	binary.LittleEndian.PutUint16(chunk, uint16(int16(1000)))
	r.Add(time.Second, chunk)

	pcm := r.PCM()
	if len(pcm) != (rate+1)*2 {
		t.Fatalf("track is %d bytes, want %d", len(pcm), (rate+1)*2)
	}
	if got := int16(binary.LittleEndian.Uint16(pcm[rate*2:])); got != 1000 {
		t.Errorf("sample at 1s = %d, want 1000", got)
	}
	if got := int16(binary.LittleEndian.Uint16(pcm[0:])); got != 0 {
		t.Errorf("sample at 0s = %d, want silence", got)
	}
}

func TestRecorderMixesOverlappingSides(t *testing.T) {
	// Caller and agent talking at once must sum, not overwrite -- that overlap
	// is what a barge-in sounds like in the recording.
	r := NewRecorder(24000)
	a := make([]byte, 2)
	binary.LittleEndian.PutUint16(a, uint16(int16(100)))
	r.Add(0, a)
	r.Add(0, a)

	if got := int16(binary.LittleEndian.Uint16(r.PCM())); got != 200 {
		t.Errorf("mixed sample = %d, want 200", got)
	}
}

func TestRecorderClipsInsteadOfWrapping(t *testing.T) {
	r := NewRecorder(24000)
	loud := make([]byte, 2)
	binary.LittleEndian.PutUint16(loud, uint16(int16(30000)))
	r.Add(0, loud)
	r.Add(0, loud)

	if got := int16(binary.LittleEndian.Uint16(r.PCM())); got != 32767 {
		t.Errorf("clipped sample = %d, want 32767 (wraparound would give a loud negative)", got)
	}
}
