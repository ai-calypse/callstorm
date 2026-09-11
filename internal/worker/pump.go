package worker

import (
	"context"
	"sync"
	"time"

	"github.com/coder/websocket"

	"github.com/yakshgandhi/callstorm/internal/audio"
	"github.com/yakshgandhi/callstorm/internal/metrics"
)

// pump is the caller's mouth. It writes exactly one audio frame per
// FrameDuration of wall clock, forever, sending silence whenever the caller
// has nothing to say.
//
// Two things here are load-bearing:
//
// Realtime pacing. Pushing the caller's audio at the socket as fast as it will
// take it would produce a TTFA number that measures nothing -- the agent's
// endpointing runs on audio duration, not on how fast bytes arrive. Frame N is
// scheduled against call start rather than against frame N-1, so a slow write
// cannot accumulate into drift.
//
// Continuous silence. A real phone line does not stop carrying audio when the
// caller stops talking, and the agent's turn detection needs to hear that
// silence to decide the turn ended. Going quiet at the socket level would
// leave the agent waiting for audio that never comes.
type pump struct {
	conn       *websocket.Conn
	log        *metrics.Log
	rec        *audio.Recorder
	frameBytes int

	mu    sync.Mutex
	queue []byte
	done  chan time.Duration

	errOnce sync.Once
	errCh   chan error
}

func newPump(conn *websocket.Conn, log *metrics.Log, rec *audio.Recorder, sampleRate int) *pump {
	return &pump{
		conn:       conn,
		log:        log,
		rec:        rec,
		frameBytes: audio.FrameBytes(sampleRate),
		errCh:      make(chan error, 1),
	}
}

// speak queues an utterance and returns a channel that yields the instant the
// caller stopped speaking -- the moment the final frame's audio finishes
// playing, not the moment it was handed to the socket.
func (p *pump) speak(pcm []byte) <-chan time.Duration {
	ch := make(chan time.Duration, 1)
	p.mu.Lock()
	defer p.mu.Unlock()
	p.queue = pcm
	p.done = ch
	return ch
}

// clear drops the queued utterance, cutting the caller off mid-sentence, and
// returns the instant they fell silent. Used when the agent starts talking over
// the caller: a real caller stops, and carrying on would spill the rest of this
// utterance into the next turn's transcript.
func (p *pump) clear() time.Duration {
	at := p.log.Since()
	p.mu.Lock()
	defer p.mu.Unlock()
	p.queue = nil
	if p.done != nil {
		// Buffered, so this never blocks.
		p.done <- at
		p.done = nil
	}
	return at
}

func (p *pump) err() <-chan error { return p.errCh }

func (p *pump) run(ctx context.Context) {
	silence := make([]byte, p.frameBytes)
	scratch := make([]byte, p.frameBytes)

	start := time.Now()
	timer := time.NewTimer(time.Hour)
	defer timer.Stop()

	for i := 0; ; i++ {
		wait := time.Until(start.Add(time.Duration(i) * audio.FrameDuration))
		switch {
		case wait > 0:
			timer.Reset(wait)
			select {
			case <-ctx.Done():
				return
			case <-timer.C:
			}
		case -wait > audio.FrameDuration:
			// The pump fell behind, which happens under load when hundreds of
			// callers all want the scheduler at once. Replaying the missed
			// deadlines back to back would push caller audio out FASTER than
			// realtime, which is exactly what this pacing exists to prevent
			// and would corrupt the agent's endpointing. Re-anchor the whole
			// schedule to now instead: the utterance shifts later, never
			// compresses, and the lateness shows up honestly as pacing drift.
			start = start.Add(-wait)
			fallthrough
		default:
			if ctx.Err() != nil {
				return
			}
		}

		frame, isCaller, finished := p.nextFrame(silence, scratch)

		at := p.log.Since()
		if err := p.conn.Write(ctx, websocket.MessageBinary, frame); err != nil {
			if ctx.Err() == nil {
				p.errOnce.Do(func() { p.errCh <- err })
			}
			return
		}
		if isCaller {
			p.rec.Add(at, frame)
		}
		if finished != nil {
			finished <- at + audio.FrameDuration
		}
	}
}

// nextFrame pulls one frame off the queue, padding a short final frame with
// silence. It reports whether the frame carried caller speech and, when the
// utterance just ended, the channel waiting to hear about it.
func (p *pump) nextFrame(silence, scratch []byte) (frame []byte, isCaller bool, finished chan time.Duration) {
	p.mu.Lock()
	defer p.mu.Unlock()

	if len(p.queue) == 0 {
		return silence, false, nil
	}

	n := copy(scratch, p.queue)
	p.queue = p.queue[n:]
	for i := n; i < len(scratch); i++ {
		scratch[i] = 0
	}
	if len(p.queue) == 0 {
		finished, p.done = p.done, nil
	}
	return scratch, true, finished
}
