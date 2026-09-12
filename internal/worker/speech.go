package worker

import (
	"strings"

	"github.com/yakshgandhi/callstorm/internal/audio"
	"github.com/yakshgandhi/callstorm/internal/scenario"
	"github.com/yakshgandhi/callstorm/internal/tts"
)

// Synthesize renders one caller line as PCM, inserting the scenario's pacing
// pauses between its sentences.
//
// Each sentence is synthesized and cached on its own, so two personas that
// differ only in how long they hesitate share the same audio and pay for it
// once. Only the silence between changes, and silence is free.
//
// The result is still one utterance and one turn. A pause inside a line is the
// caller thinking, not the caller finishing, and an agent that cannot tell
// those apart is exactly what this is for.
func Synthesize(client *tts.Client, sc *scenario.Scenario, say string, sampleRate int) ([]byte, bool, error) {
	pause := sc.Pacing.Pause()
	parts := splitSentences(say)
	if pause <= 0 || len(parts) < 2 {
		return client.Synthesize(sc.CallerVoice, say)
	}

	gap := audio.Silence(pause, sampleRate)
	var out []byte
	cachedAll := true

	for i, p := range parts {
		pcm, cached, err := client.Synthesize(sc.CallerVoice, p)
		if err != nil {
			return nil, false, err
		}
		if !cached {
			cachedAll = false
		}
		if i > 0 {
			out = append(out, gap...)
		}
		out = append(out, pcm...)
	}
	return out, cachedAll, nil
}

// splitSentences breaks a line after ., ! or ?, keeping the punctuation with
// the sentence it ends. A decimal point or an abbreviation would split wrongly,
// which is why caller scripts are written as plain spoken sentences -- an order
// number is spelled out as words for the same reason.
func splitSentences(s string) []string {
	var parts []string
	start := 0
	runes := []rune(s)

	for i, r := range runes {
		if r != '.' && r != '!' && r != '?' {
			continue
		}
		// Only a terminator if the next character ends the line or opens a new
		// sentence, so "4.5" and "e.g." stay whole.
		if i+1 < len(runes) && runes[i+1] != ' ' {
			continue
		}
		if p := strings.TrimSpace(string(runes[start : i+1])); p != "" {
			parts = append(parts, p)
		}
		start = i + 1
	}
	if p := strings.TrimSpace(string(runes[start:])); p != "" {
		parts = append(parts, p)
	}
	return parts
}
