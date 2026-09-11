// Package audio holds the PCM frame math and a minimal WAV writer.
//
// Everything in Callstorm is signed 16-bit little-endian mono PCM. Frames are
// the unit the worker paces on: one frame every FrameDuration of wall clock is
// exactly realtime, which is the only rate at which a latency measurement
// against a live voice agent means anything.
package audio

import (
	"encoding/binary"
	"os"
	"time"
)

const (
	// FrameDuration is the audio quantum written to the socket per tick.
	// 20ms is the telephony convention and small enough that it does not
	// meaningfully quantize a TTFA measurement.
	FrameDuration = 20 * time.Millisecond

	bytesPerSample = 2
)

// FrameBytes is the byte length of one FrameDuration frame at sampleRate.
func FrameBytes(sampleRate int) int {
	return int(float64(sampleRate) * FrameDuration.Seconds() * bytesPerSample)
}

// Duration is how long pcm takes to play at sampleRate.
func Duration(pcm []byte, sampleRate int) time.Duration {
	samples := len(pcm) / bytesPerSample
	return time.Duration(float64(samples) / float64(sampleRate) * float64(time.Second))
}

// WriteWAV wraps raw mono PCM in a RIFF header so it can be played back.
func WriteWAV(path string, pcm []byte, sampleRate int) error {
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	defer f.Close()

	var (
		numChannels = uint16(1)
		byteRate    = uint32(sampleRate * int(numChannels) * bytesPerSample)
		blockAlign  = numChannels * bytesPerSample
		dataLen     = uint32(len(pcm))
	)

	h := make([]byte, 0, 44)
	h = append(h, "RIFF"...)
	h = binary.LittleEndian.AppendUint32(h, 36+dataLen)
	h = append(h, "WAVEfmt "...)
	h = binary.LittleEndian.AppendUint32(h, 16)          // fmt chunk size
	h = binary.LittleEndian.AppendUint16(h, 1)           // PCM
	h = binary.LittleEndian.AppendUint16(h, numChannels) //
	h = binary.LittleEndian.AppendUint32(h, uint32(sampleRate))
	h = binary.LittleEndian.AppendUint32(h, byteRate)
	h = binary.LittleEndian.AppendUint16(h, blockAlign)
	h = binary.LittleEndian.AppendUint16(h, 8*bytesPerSample)
	h = append(h, "data"...)
	h = binary.LittleEndian.AppendUint32(h, dataLen)

	if _, err := f.Write(h); err != nil {
		return err
	}
	_, err = f.Write(pcm)
	return err
}
