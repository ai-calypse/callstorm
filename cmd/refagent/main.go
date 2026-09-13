// Command refagent is a voice agent with no brain and a stopwatch.
//
// It speaks the Deepgram Voice Agent wire protocol, so Callstorm can point at
// it with nothing but a different -target URL, but it does no STT, no LLM and
// no TTS. It waits a configured number of milliseconds and then emits a tone.
//
// That makes it two things at once:
//
//  1. A calibration instrument. Because the injected latency is known exactly,
//     a measured TTFA that does not match it means Callstorm is wrong, and
//     nothing it reports about a real agent can be trusted. Everything else in
//     the project rests on this check.
//  2. A load target with no bill and no rate limit. Deepgram caps Voice Agent
//     connections at 45 on pay-as-you-go, so the concurrency curve has to be
//     drawn against something local.
//
// The timing anchor is the important detail: the response is scheduled from
// the caller's last non-silent audio frame, not from the moment silence was
// detected. Detection needs a hangover window to avoid firing on pauses
// between words, and anchoring to detection would fold that window into the
// number being calibrated.
package main

import (
	"context"
	"encoding/binary"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"math"
	"math/rand"
	"net/http"
	"os"
	"os/signal"
	"sync"
	"sync/atomic"
	"time"

	"github.com/coder/websocket"

	"github.com/yakshgandhi/callstorm/internal/audio"
)

type config struct {
	addr        string
	ttfa        time.Duration
	endpointing time.Duration
	jitter      time.Duration
	speech      time.Duration
	leadSilence time.Duration
	streamRate  float64
	hangover    time.Duration
	capacity    int
	degrade     time.Duration
	failRate    float64
	bargeIn     time.Duration
	yield       time.Duration
	logPath     string
}

var (
	cfg    config
	active atomic.Int64

	timingMu  sync.Mutex
	timingLog []timing
)

// timing is refagent's own record of what it did, in its own clock domain.
// The calibration test compares these durations against Callstorm's, which is
// a genuine two-clock comparison rather than one program grading itself.
type timing struct {
	Conn            int     `json:"conn"`
	Turn            int     `json:"turn"`
	Concurrent      int     `json:"concurrent_at_response"`
	ScheduledTTFAMs float64 `json:"scheduled_ttfa_ms"`
	ActualTTFAMs    float64 `json:"actual_ttfa_ms"`
	SpeechMs        float64 `json:"speech_ms"`
	BargedIn        bool    `json:"barged_in,omitempty"`
}

func main() {
	flag.StringVar(&cfg.addr, "addr", ":8080", "listen address")
	flag.DurationVar(&cfg.ttfa, "ttfa", 800*time.Millisecond,
		"delay from the caller's last non-silent audio to the agent's first audio")
	flag.DurationVar(&cfg.endpointing, "endpointing", 300*time.Millisecond,
		"delay from the same anchor to the user transcript; must be < -ttfa")
	flag.DurationVar(&cfg.jitter, "jitter", 0, "uniform +/- jitter applied to -ttfa")
	flag.DurationVar(&cfg.speech, "speech", 3*time.Second, "length of the agent's spoken reply")
	flag.DurationVar(&cfg.leadSilence, "lead-silence", 0,
		"silence at the start of every reply, before its tone: a known answer for leading silence")
	flag.Float64Var(&cfg.streamRate, "stream-rate", 1.5,
		"how fast to stream reply audio relative to realtime, as Deepgram does")
	flag.DurationVar(&cfg.hangover, "hangover", 150*time.Millisecond,
		"continuous silence needed to call the caller's turn over")
	flag.IntVar(&cfg.capacity, "capacity", 0, "concurrent calls before latency degrades (0 = unlimited)")
	flag.DurationVar(&cfg.degrade, "degrade", 0, "latency added per concurrent call past -capacity")
	flag.Float64Var(&cfg.failRate, "fail-rate", 0, "probability [0,1] that a turn errors instead of replying")
	flag.DurationVar(&cfg.bargeIn, "barge-in", 0,
		"if set, reply this long after the caller STARTS talking, deliberately talking over them")
	flag.DurationVar(&cfg.yield, "yield", 0,
		"stop speaking this long after the caller starts talking over us (0 = never yield)")
	flag.StringVar(&cfg.logPath, "log", "", "write per-turn timing JSON here on shutdown")
	flag.Parse()

	if cfg.endpointing >= cfg.ttfa && cfg.bargeIn == 0 {
		log.Fatalf("-endpointing (%s) must be less than -ttfa (%s)", cfg.endpointing, cfg.ttfa)
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/v1/agent/converse", handle)
	mux.HandleFunc("/timings", func(w http.ResponseWriter, _ *http.Request) {
		// Exposed over HTTP as well as on shutdown so a calibration run can
		// read refagent's own clock without having to signal the process.
		timingMu.Lock()
		defer timingMu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(timingLog)
	})
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, _ *http.Request) {
		fmt.Fprintf(w, "ok active=%d\n", active.Load())
	})

	srv := &http.Server{Addr: cfg.addr, Handler: mux}
	log.Printf("refagent listening on %s  ttfa=%s endpointing=%s speech=%s capacity=%d degrade=%s",
		cfg.addr, cfg.ttfa, cfg.endpointing, cfg.speech, cfg.capacity, cfg.degrade)

	go func() {
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatal(err)
		}
	}()

	waitForShutdown()
	_ = srv.Shutdown(context.Background())
	writeTimingLog()
}

func waitForShutdown() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	<-ctx.Done()
}

var connSeq atomic.Int64

func handle(w http.ResponseWriter, r *http.Request) {
	conn, err := websocket.Accept(w, r, &websocket.AcceptOptions{InsecureSkipVerify: true})
	if err != nil {
		return
	}
	conn.SetReadLimit(1 << 20)
	defer conn.CloseNow()

	id := int(connSeq.Add(1))
	active.Add(1)
	defer active.Add(-1)

	if err := serve(r.Context(), conn, id); err != nil {
		log.Printf("conn %d: %v", id, err)
	}
}

// session is one caller's connection.
type session struct {
	conn *websocket.Conn
	id   int

	writeMu sync.Mutex // replies are written from timers, so writes must serialize

	sampleRate int
	turn       atomic.Int64

	// speakingNow and stopSpeak implement yielding the floor. A caller talking
	// over this agent sets stopSpeak after the configured delay, and the
	// speech loop checks it between chunks. That makes the yield an injected,
	// known quantity, which is the only reason a measured barge-in yield can
	// be trusted.
	speakingNow atomic.Bool
	stopSpeak   atomic.Bool
}

func serve(ctx context.Context, conn *websocket.Conn, id int) error {
	s := &session{conn: conn, id: id, sampleRate: 24000}

	if err := s.sendJSON(ctx, map[string]string{
		"type":       "Welcome",
		"request_id": fmt.Sprintf("refagent-%08d", id),
	}); err != nil {
		return err
	}

	greeting, err := s.awaitSettings(ctx)
	if err != nil {
		return err
	}
	if err := s.sendJSON(ctx, map[string]string{"type": "SettingsApplied"}); err != nil {
		return err
	}

	if greeting != "" {
		s.reply(ctx, greeting, time.Now(), false)
	}

	return s.listen(ctx)
}

// awaitSettings reads the Settings message and returns the configured greeting.
// Only the fields refagent honours are decoded.
func (s *session) awaitSettings(ctx context.Context) (string, error) {
	for {
		typ, data, err := s.conn.Read(ctx)
		if err != nil {
			return "", err
		}
		if typ != websocket.MessageText {
			continue
		}
		var msg struct {
			Type  string `json:"type"`
			Audio struct {
				Input struct {
					SampleRate int `json:"sample_rate"`
				} `json:"input"`
			} `json:"audio"`
			Agent struct {
				Greeting string `json:"greeting"`
			} `json:"agent"`
		}
		if err := json.Unmarshal(data, &msg); err != nil {
			return "", err
		}
		if msg.Type != "Settings" {
			continue
		}
		if msg.Audio.Input.SampleRate > 0 {
			s.sampleRate = msg.Audio.Input.SampleRate
		}
		return msg.Agent.Greeting, nil
	}
}

// listen consumes caller audio and decides when a turn ended.
//
// Callstorm sends exact-zero frames as silence and real samples as speech, so
// the split is unambiguous. lastVoice is the instant the caller's final
// non-silent frame finished playing, and it is the anchor every reply is
// scheduled from.
func (s *session) listen(ctx context.Context) error {
	var (
		speaking    bool
		replied     bool // this utterance has already been answered
		lastVoice   time.Time
		voiceStart  time.Time
		bargeArmed  bool
		frameLength = audio.FrameDuration
	)

	for {
		typ, data, err := s.conn.Read(ctx)
		if err != nil {
			return nil // caller hung up
		}
		if typ != websocket.MessageBinary {
			continue
		}

		now := time.Now()
		if hasVoice(data) {
			if !speaking {
				speaking = true
				replied = false
				voiceStart = now
				bargeArmed = cfg.bargeIn > 0

				// The caller has cut in while this agent was mid-reply.
				if cfg.yield > 0 && s.speakingNow.Load() {
					go func() {
						sleepFor(ctx, cfg.yield)
						s.stopSpeak.Store(true)
					}()
				}
			}
			lastVoice = now.Add(frameLength)

			// Deliberate talk-over: reply while the caller is still going.
			if bargeArmed && now.Sub(voiceStart) >= cfg.bargeIn {
				bargeArmed = false
				replied = true
				go s.reply(ctx, "", now, true)
			}
			continue
		}

		// Silence. The turn is over once the hangover has elapsed.
		// An utterance we already cut into does not get a second answer.
		if speaking && now.Sub(lastVoice) >= cfg.hangover {
			speaking = false
			if !replied {
				go s.reply(ctx, "", lastVoice, false)
			}
		}
	}
}

func hasVoice(frame []byte) bool {
	for _, b := range frame {
		if b != 0 {
			return true
		}
	}
	return false
}

// reply schedules and sends one agent turn, anchored at the given instant.
func (s *session) reply(ctx context.Context, text string, anchor time.Time, barged bool) {
	turn := int(s.turn.Add(1))
	concurrent := int(active.Load())

	if cfg.failRate > 0 && rand.Float64() < cfg.failRate {
		_ = s.sendJSON(ctx, map[string]string{
			"type": "Error", "code": "REFAGENT_INJECTED", "description": "injected failure",
		})
		return
	}

	ttfa := cfg.ttfa
	if barged {
		ttfa = 0 // the anchor is already the moment we chose to cut in
	}
	if cfg.jitter > 0 {
		ttfa += time.Duration(rand.Int63n(int64(2*cfg.jitter))) - cfg.jitter
	}
	// Latency grows once the box is past its configured capacity. This is what
	// puts a knee in the concurrency curve for a sweep to find.
	if cfg.capacity > 0 && concurrent > cfg.capacity && cfg.degrade > 0 {
		ttfa += time.Duration(concurrent-cfg.capacity) * cfg.degrade
	}
	if ttfa < 0 {
		ttfa = 0
	}

	if anchor.IsZero() {
		anchor = time.Now()
	}

	// The user transcript lands before the audio does, which is what lets
	// Callstorm split TTFA into endpointing and think/speak. The content is
	// deliberately empty: this agent runs no STT, and inventing a transcript
	// would give the word error rate something to score that was never heard.
	if !barged && cfg.endpointing < ttfa {
		sleepUntil(ctx, anchor.Add(cfg.endpointing))
		_ = s.sendJSON(ctx, map[string]string{
			"type": "ConversationText", "role": "user", "content": "",
		})
	}

	sleepUntil(ctx, anchor.Add(ttfa))

	if text == "" {
		text = fmt.Sprintf("Reference reply for turn %d.", turn)
	}
	_ = s.sendJSON(ctx, map[string]string{
		"type": "ConversationText", "role": "assistant", "content": text,
	})

	firstAudioAt := time.Now()
	_ = s.sendJSON(ctx, map[string]string{"type": "AgentStartedSpeaking"})
	s.stopSpeak.Store(false)
	s.speakingNow.Store(true)
	s.streamSpeech(ctx)
	s.speakingNow.Store(false)
	_ = s.sendJSON(ctx, map[string]string{"type": "AgentAudioDone"})

	timingMu.Lock()
	timingLog = append(timingLog, timing{
		Conn: s.id, Turn: turn, Concurrent: concurrent,
		ScheduledTTFAMs: float64(ttfa.Microseconds()) / 1000,
		ActualTTFAMs:    float64(firstAudioAt.Sub(anchor).Microseconds()) / 1000,
		SpeechMs:        float64(cfg.speech.Microseconds()) / 1000,
		BargedIn:        barged,
	})
	timingMu.Unlock()
}

// streamSpeech sends the reply tone faster than realtime, the way Deepgram
// delivers TTS, so Callstorm's playout tracking is actually exercised.
func (s *session) streamSpeech(ctx context.Context) {
	const chunk = 100 * time.Millisecond

	// Leading silence goes in front of the tone as exact zeros, the way a voice
	// that opens its reply with a pause sends it.
	lead := make([]byte, 2*int(cfg.leadSilence.Seconds()*float64(s.sampleRate)))
	pcm := append(lead, tone(cfg.speech, s.sampleRate)...)
	chunkBytes := int(float64(s.sampleRate) * chunk.Seconds() * 2)
	interval := time.Duration(float64(chunk) / cfg.streamRate)

	for off := 0; off < len(pcm); off += chunkBytes {
		end := off + chunkBytes
		if end > len(pcm) {
			end = len(pcm)
		}
		s.writeMu.Lock()
		err := s.conn.Write(ctx, websocket.MessageBinary, pcm[off:end])
		s.writeMu.Unlock()
		if err != nil {
			return
		}
		// Yielding the floor: stop mid-reply because the caller cut in.
		if s.stopSpeak.Load() {
			return
		}
		if end < len(pcm) {
			sleepFor(ctx, interval)
		}
	}
}

// tone renders a quiet 220Hz sine so a recorded call is audible and the agent's
// side is obviously distinguishable from the caller's.
func tone(d time.Duration, sampleRate int) []byte {
	n := int(d.Seconds() * float64(sampleRate))
	out := make([]byte, n*2)
	for i := 0; i < n; i++ {
		v := int16(6000 * math.Sin(2*math.Pi*220*float64(i)/float64(sampleRate)))
		binary.LittleEndian.PutUint16(out[i*2:], uint16(v))
	}
	return out
}

func (s *session) sendJSON(ctx context.Context, v any) error {
	b, err := json.Marshal(v)
	if err != nil {
		return err
	}
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	return s.conn.Write(ctx, websocket.MessageText, b)
}

func sleepUntil(ctx context.Context, t time.Time) { sleepFor(ctx, time.Until(t)) }

func sleepFor(ctx context.Context, d time.Duration) {
	if d <= 0 {
		return
	}
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-t.C:
	case <-ctx.Done():
	}
}

func writeTimingLog() {
	if cfg.logPath == "" {
		return
	}
	timingMu.Lock()
	defer timingMu.Unlock()
	b, err := json.MarshalIndent(timingLog, "", "  ")
	if err != nil {
		return
	}
	if err := os.WriteFile(cfg.logPath, b, 0o644); err != nil {
		log.Printf("write timing log: %v", err)
		return
	}
	log.Printf("wrote %d turn timings to %s", len(timingLog), cfg.logPath)
}
