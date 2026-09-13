package worker

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/yakshgandhi/callstorm/internal/audio"
	"github.com/yakshgandhi/callstorm/internal/metrics"
)

// awaitGreeting consumes the agent's opening line. Its audio must be fully
// drained before the caller speaks, otherwise turn 1 would measure the tail of
// the greeting as its own response.
func (w *worker) awaitGreeting(ctx context.Context) error {
	deadline := time.NewTimer(w.cfg.TurnTimeout)
	defer deadline.Stop()

	var text string
	for {
		select {
		case ev, ok := <-w.events:
			if !ok {
				return errors.New("connection closed before the agent greeted")
			}
			switch ev.kind {
			case metrics.AgentTranscript:
				text = joinText(text, ev.text)
			case metrics.AgentAudioDone:
				// The greeting counts as something the agent said, so turn 1
				// can branch on it like any other reply.
				w.lastAgentText = text
				w.printf("\n  agent: %s\n", text)
				w.waitPlayout(ctx, ev.playout)
				return nil
			case metrics.ServerError:
				return fmt.Errorf("agent error during greeting: %s", ev.text)
			}
		case err := <-w.pump.err():
			return fmt.Errorf("audio pump: %w", err)
		case <-deadline.C:
			return errors.New("agent never delivered its greeting")
		case <-ctx.Done():
			return ctx.Err()
		}
	}
}

// runTurn speaks one caller line and measures the agent's response.
//
// Speaking and listening share one event loop rather than running in sequence,
// because the agent can start replying before the caller has finished. When
// that happens the caller yields, exactly as a person would; the previous
// design kept talking and spilled the rest of the utterance into the next
// turn's transcript, silently corrupting it.
//
// The turn is bounded by two derived instants: callerEnd, the moment the
// caller fell silent, and playoutEnd, the moment the agent's audio finishes
// playing. Everything on the report card is a difference between instants
// inside that window.
//
// interruptAfter cuts this turn's collection short once the agent has been
// speaking that long, so the next caller line can interrupt it. barging marks
// this turn as the interrupting one, which changes how it starts: it keeps the
// previous reply's pending events, because the instant that reply stops is the
// measurement.
func (w *worker) runTurn(ctx context.Context, idx int, pcm []byte, say string,
	interruptAfter time.Duration, barging bool) metrics.TurnMetric {

	m := metrics.TurnMetric{Turn: idx, CallerText: say, BargedIn: barging}

	// Anything still queued belongs to the previous turn -- except when this
	// turn is interrupting one, where the previous reply is still in flight and
	// the event announcing it stopped is the whole point of the turn.
	if !barging {
		w.drain()
	}
	w.curTurn.Store(int64(idx))

	w.printf("\n  caller: %s\n", say)
	callerStart := w.log.Mark(metrics.CallerSpeechStart, idx, say)
	utterance := audio.Duration(pcm, w.cfg.SampleRate)
	m.CallerSpeech = utterance
	speakDone := w.pump.speak(pcm)

	// Parked until the caller stops talking, then reset to the response
	// timeout. The initial value is a backstop in case the pump never reports.
	deadline := time.NewTimer(utterance + w.cfg.TurnTimeout + 10*time.Second)
	defer deadline.Stop()

	var callerEnd, firstAudio, userTranscript, playoutEnd, audible time.Duration
	speaking := true

	// interrupt fires once the agent has been talking long enough for the next
	// caller line to cut in. It is armed when the agent's audio starts, not
	// when the turn starts, so the offset means "into the reply" rather than
	// "into the wait for one".
	interrupt := time.NewTimer(time.Hour)
	interrupt.Stop()
	defer interrupt.Stop()
	armed := false

	// natural distinguishes an utterance the pump played to its end from one
	// that was cut short. Only the former says anything about harness pacing:
	// measuring drift on a truncated utterance reports a large negative number
	// that reads as a harness fault when nothing went wrong.
	fellSilent := func(at time.Duration, natural bool) {
		if !speaking {
			return
		}
		speaking = false
		callerEnd = at
		w.log.MarkAt(callerEnd, metrics.CallerSpeechEnd, idx, "")
		if natural {
			m.PacingDrift = (callerEnd - callerStart) - utterance
		}
		deadline.Reset(w.cfg.TurnTimeout)
	}

collect:
	for {
		select {
		case at := <-speakDone:
			fellSilent(at, true)

		case ev, ok := <-w.events:
			if !ok {
				m.Failed, m.FailReason = true, "connection closed mid-turn"
				break collect
			}
			switch ev.kind {
			case metrics.UserTranscript:
				// STT finalizes fragments as the caller speaks, so the turn
				// ends at the last transcript before the agent replies, not
				// the first. Taking the first would measure a pause mid-
				// sentence instead of the agent's endpointing decision.
				if firstAudio == 0 {
					userTranscript = ev.at
				}
				m.HeardText = joinText(m.HeardText, ev.text)

			case metrics.AgentFirstAudio:
				if firstAudio == 0 {
					firstAudio = ev.at
					if interruptAfter > 0 && !armed {
						armed = true
						interrupt.Reset(interruptAfter)
					}
				}
				if speaking {
					m.CallerYielded = true
					m.FailReason = "agent talked over the caller"
					w.log.Mark(metrics.CallerYielded, idx, "")
					fellSilent(w.pump.clear(), false)
				}

			case metrics.AgentAudible:
				// The first sound a caller could hear in this reply. One that
				// comes before this turn's own audio belongs to the reply before.
				if firstAudio != 0 && audible == 0 {
					audible = ev.at
				}

			case metrics.AgentTranscript:
				m.AgentText = joinText(m.AgentText, ev.text)

			case metrics.AgentAudioDone:
				if firstAudio != 0 {
					playoutEnd = ev.playout
					break collect
				}
				// No audio of our own yet. On an interrupting turn this is the
				// previous reply falling silent, which is exactly what was
				// being measured: how long the agent kept going after being
				// cut off. On any other turn it is a stray, and ignored.
				if barging && m.BargeInYield == 0 {
					m.BargeInYield = ev.at - callerStart
					w.log.MarkAt(ev.at, metrics.AgentYielded, idx, "")
				}

			case metrics.ServerError:
				m.Failed, m.FailReason = true, ev.text
				break collect
			}

		case <-interrupt.C:
			// The next caller line is about to cut in. Stop collecting so it
			// can start on time. This turn's reply never finished, so its
			// playout and speech length are deliberately left unmeasured
			// rather than guessed at.
			break collect

		case err := <-w.pump.err():
			m.Failed, m.FailReason = true, fmt.Sprintf("audio pump: %v", err)
			break collect

		case <-deadline.C:
			m.Failed, m.FailReason = true, "turn timeout"
			w.log.Mark(metrics.TurnTimeout, idx, w.cfg.TurnTimeout.String())
			break collect

		case <-ctx.Done():
			m.Failed, m.FailReason = true, "cancelled"
			break collect
		}
	}

	// A turn that ended while the caller still had audio queued must not leave
	// it to play into the next turn.
	if speaking {
		fellSilent(w.pump.clear(), false)
	}

	if firstAudio != 0 {
		m.TTFA = firstAudio - callerEnd
		if userTranscript != 0 {
			m.Endpointing = userTranscript - callerEnd
			m.ThinkSpeak = firstAudio - userTranscript
		}
	}
	if audible != 0 {
		m.LeadingSilence, m.LeadingSilenceMeasured = audible-firstAudio, true
	}
	if playoutEnd != 0 {
		m.AgentSpeech = playoutEnd - firstAudio
		m.TurnLatency = playoutEnd - callerEnd
	}

	// The round trip measured while this turn ran, from the caller starting to
	// speak to the agent's first audio. It is what lets the report separate the
	// agent's share of the wait from the network's; see pingLoop.
	end := firstAudio
	if end == 0 {
		end = w.log.Since()
	}
	m.TransportRTT, m.TransportRTTMeasured = w.rtt.median(callerStart, end)

	// The raw instants behind every duration on this turn, so the turn can be
	// placed on a timeline and recomputed from the log.
	m.StartedAt = w.log.Start().Add(callerStart)
	m.CallerStartMs = metrics.Millis(callerStart)
	m.CallerEndMs = metrics.Millis(callerEnd)
	m.HeardMs = metrics.Millis(userTranscript)
	m.FirstAudioMs = metrics.Millis(firstAudio)
	m.PlayoutEndMs = metrics.Millis(playoutEnd)

	if m.AgentText != "" {
		w.lastAgentText = m.AgentText
	}

	w.reportTurn(m)

	// Let the agent finish speaking before the caller's next line. A real
	// caller waits for the audio to stop coming out of the speaker, not for
	// the last packet to land.
	w.waitPlayout(ctx, playoutEnd)
	return m
}

// waitPlayout blocks until the instant the agent's audio finishes playing.
func (w *worker) waitPlayout(ctx context.Context, playoutEnd time.Duration) {
	remaining := playoutEnd - w.log.Since()
	if remaining <= 0 {
		return
	}
	t := time.NewTimer(remaining)
	defer t.Stop()
	select {
	case <-t.C:
	case <-ctx.Done():
	}
}

func (w *worker) reportTurn(m metrics.TurnMetric) {
	if m.HeardText != "" {
		w.printf("  heard: %s\n", m.HeardText)
	}
	if m.AgentText != "" {
		w.printf("  agent: %s\n", m.AgentText)
	}
	if m.Failed && m.TTFA == 0 {
		w.printf("  FAILED: %s\n", m.FailReason)
		return
	}
	line := fmt.Sprintf("  ttfa %-8s (endpointing %-8s + think/speak %-8s)  agent spoke %s",
		ms(m.TTFA), ms(m.Endpointing), ms(m.ThinkSpeak), ms(m.AgentSpeech))
	if m.BargedIn {
		if m.BargeInYield > 0 {
			line += fmt.Sprintf("  <- barged in, agent yielded in %s", ms(m.BargeInYield))
		} else {
			line += "  <- barged in, agent NEVER yielded"
		}
	}
	if m.FailReason != "" {
		line += "  <- " + m.FailReason
	}
	w.printf("%s\n", line)
}

// drain discards events left over from the previous turn.
func (w *worker) drain() {
	for {
		select {
		case _, ok := <-w.events:
			if !ok {
				return
			}
		default:
			return
		}
	}
}

func joinText(a, b string) string {
	switch {
	case b == "":
		return a
	case a == "":
		return b
	default:
		return a + " " + b
	}
}

// ms renders a duration, keeping the sign. Only an exact zero means the
// instant was never observed.
func ms(d time.Duration) string {
	if d == 0 {
		return "-"
	}
	return strings.TrimSuffix(fmt.Sprintf("%.0fms", float64(d.Microseconds())/1000), ".0")
}
