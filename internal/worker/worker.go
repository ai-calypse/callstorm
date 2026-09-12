// Package worker is one synthetic caller: a single goroutine owning a
// WebSocket to the agent under test, a turn-taking state machine, and a clock
// that stamps every event on both sides of the conversation.
package worker

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"sync"
	"sync/atomic"
	"time"

	"github.com/coder/websocket"

	"github.com/yakshgandhi/callstorm/internal/audio"
	"github.com/yakshgandhi/callstorm/internal/metrics"
	"github.com/yakshgandhi/callstorm/internal/scenario"
	"github.com/yakshgandhi/callstorm/internal/tts"
)

const readLimit = 1 << 20

type Config struct {
	APIKey      string
	Scenario    *scenario.Scenario
	TTS         *tts.Client
	SampleRate  int
	TurnTimeout time.Duration
	Out         io.Writer

	// TargetURL is the agent under test. Defaults to Deepgram's Voice Agent.
	// The reference agent in cmd/refagent speaks the same wire protocol, so
	// pointing at it needs nothing more than a different URL.
	TargetURL string

	// Record keeps the mixed two-sided call audio. Worth it for a single call,
	// far too expensive at load: the track costs ~2.9MB per minute per caller.
	Record bool

	// OnTurn, when set, is called as each turn finishes rather than after the
	// whole call. A call holds its turns for tens of seconds, and under load
	// every call in a step finishes at roughly the same moment, so reporting
	// them at call end makes a step's measurements arrive in one burst once
	// the step is already over -- too late for anything watching live.
	OnTurn func(metrics.TurnMetric)
}

// Result is everything one call produced.
type Result struct {
	Scenario  string    `json:"scenario"`
	Persona   string    `json:"persona,omitempty"`
	RequestID string    `json:"request_id"`
	StartedAt time.Time `json:"started_at"`

	Turns  []metrics.TurnMetric `json:"turns"`
	Events []metrics.Event      `json:"events"`

	CallDurationMs float64 `json:"call_duration_ms"`
	AgentAudioKB   float64 `json:"agent_audio_kb"`
	SynthCacheHits int     `json:"synth_cache_hits"`

	// Recording is the mixed two-sided call audio.
	Recording  []byte `json:"-"`
	SampleRate int    `json:"sample_rate"`
}

// srvEvent is a server message reduced to the fields the turn loop cares about.
type srvEvent struct {
	kind metrics.Kind
	at   time.Duration
	text string

	// playout is set on AgentAudioDone: the instant the agent's audio would
	// finish coming out of a speaker, which is later than the instant the last
	// byte arrived.
	playout time.Duration
}

type worker struct {
	cfg  Config
	log  *metrics.Log
	rec  *audio.Recorder
	conn *websocket.Conn
	pump *pump

	events chan srvEvent

	outMu sync.Mutex

	agentBytes atomic.Int64
	requestID  string

	// curTurn lets readLoop attribute each server event to the turn in
	// progress, so the saved event log is queryable per turn.
	curTurn atomic.Int64
}

// Run places one call and returns its measurements.
func Run(ctx context.Context, cfg Config) (*Result, error) {
	if cfg.SampleRate == 0 {
		cfg.SampleRate = 24000
	}
	if cfg.TurnTimeout == 0 {
		cfg.TurnTimeout = 20 * time.Second
	}
	if cfg.Out == nil {
		cfg.Out = io.Discard
	}

	w := &worker{cfg: cfg, events: make(chan srvEvent, 256)}

	// Synthesize every caller line before the call starts. Synthesizing
	// mid-call would put Aura's latency inside the window we are attributing
	// to the agent under test.
	utterances := make([][]byte, len(cfg.Scenario.Turns))
	cacheHits := 0
	for i, t := range cfg.Scenario.Turns {
		pcm, cached, err := cfg.TTS.Synthesize(cfg.Scenario.CallerVoice, t.Say)
		if err != nil {
			return nil, fmt.Errorf("synthesize turn %d: %w", i+1, err)
		}
		utterances[i] = pcm
		if cached {
			cacheHits++
		}
	}
	w.printf("synthesized %d caller turns (%d from cache)\n", len(utterances), cacheHits)

	startedAt := time.Now()
	if err := w.connect(ctx); err != nil {
		return nil, err
	}
	defer w.conn.CloseNow()

	if cfg.Record {
		w.rec = audio.NewRecorder(cfg.SampleRate)
	}
	w.pump = newPump(w.conn, w.log, w.rec, cfg.SampleRate)

	pumpCtx, stopPump := context.WithCancel(ctx)
	defer stopPump()
	go w.pump.run(pumpCtx)
	go w.readLoop(ctx)

	var turns []metrics.TurnMetric

	if cfg.Scenario.Target.Greeting != "" {
		if err := w.awaitGreeting(ctx); err != nil {
			return nil, err
		}
	}

	for i, pcm := range utterances {
		// A turn that barges in changes the turn before it: that reply has to
		// be cut short so the interruption lands while the agent is talking.
		var interruptAfter time.Duration
		if i+1 < len(cfg.Scenario.Turns) {
			interruptAfter = cfg.Scenario.Turns[i+1].BargeIn()
		}
		barging := cfg.Scenario.Turns[i].BargeIn() > 0

		m := w.runTurn(ctx, i+1, pcm, cfg.Scenario.Turns[i].Say, interruptAfter, barging)
		m.Finalize()
		turns = append(turns, m)
		if cfg.OnTurn != nil {
			cfg.OnTurn(m)
		}
		if m.Failed && m.FailReason != "turn timeout" {
			break
		}
	}

	w.log.Mark(metrics.CallEnd, 0, "")
	total := w.log.Since()
	stopPump()
	_ = w.conn.Close(websocket.StatusNormalClosure, "done")

	return &Result{
		Scenario:       cfg.Scenario.Name,
		Persona:        cfg.Scenario.Persona,
		RequestID:      w.requestID,
		StartedAt:      startedAt,
		Turns:          turns,
		Events:         w.log.Events(),
		CallDurationMs: float64(total.Milliseconds()),
		AgentAudioKB:   float64(w.agentBytes.Load()) / 1024,
		SynthCacheHits: cacheHits,
		Recording:      w.rec.PCM(),
		SampleRate:     cfg.SampleRate,
	}, nil
}

// connect dials the agent, completes the Welcome/Settings handshake, and
// starts the clock. The handshake is read synchronously because ordering is
// strict: nothing may be sent before Welcome, and no audio before Settings is
// acknowledged.
func (w *worker) connect(ctx context.Context) error {
	dialCtx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()

	url := w.cfg.TargetURL
	if url == "" {
		url = deepgramAgentURL
	}
	conn, _, err := websocket.Dial(dialCtx, url, &websocket.DialOptions{
		HTTPHeader: http.Header{"Authorization": []string{"Token " + w.cfg.APIKey}},
	})
	if err != nil {
		return fmt.Errorf("dial %s: %w", url, err)
	}
	conn.SetReadLimit(readLimit)
	w.conn = conn
	w.log = metrics.NewLog()

	msg, err := w.readJSON(ctx)
	if err != nil {
		return fmt.Errorf("waiting for Welcome: %w", err)
	}
	if msg.Type != "Welcome" {
		return fmt.Errorf("expected Welcome, got %s", msg.Type)
	}
	w.requestID = msg.RequestID
	w.log.Mark(metrics.Welcome, 0, msg.RequestID)
	w.printf("connected  request_id=%s\n", msg.RequestID)

	body, err := json.Marshal(buildSettings(w.cfg.Scenario, w.cfg.SampleRate))
	if err != nil {
		return err
	}
	if err := w.conn.Write(ctx, websocket.MessageText, body); err != nil {
		return fmt.Errorf("send Settings: %w", err)
	}

	for {
		msg, err := w.readJSON(ctx)
		if err != nil {
			return fmt.Errorf("waiting for SettingsApplied: %w", err)
		}
		switch msg.Type {
		case "SettingsApplied":
			w.log.Mark(metrics.SettingsApplied, 0, "")
			return nil
		case "Error":
			return fmt.Errorf("agent rejected Settings: %s %s",
				msg.Code, firstNonEmpty(msg.Description, msg.Message))
		}
	}
}

func (w *worker) readJSON(ctx context.Context) (serverMessage, error) {
	for {
		typ, data, err := w.conn.Read(ctx)
		if err != nil {
			return serverMessage{}, err
		}
		if typ != websocket.MessageText {
			continue
		}
		var m serverMessage
		if err := json.Unmarshal(data, &m); err != nil {
			return serverMessage{}, err
		}
		return m, nil
	}
}

// readLoop stamps everything arriving from the agent. It is the only reader of
// the socket once the call is live.
func (w *worker) readLoop(ctx context.Context) {
	defer close(w.events)

	// speaking is true between the agent's first audio frame of a turn and its
	// AgentAudioDone, so that "first audio" fires once per agent turn rather
	// than once per chunk.
	speaking := false

	// playoutEnd tracks when the agent's audio would finish playing. Deepgram
	// delivers TTS faster than realtime, so the last byte arriving is not the
	// agent falling silent -- treating it that way would have the caller talk
	// over the tail of every reply.
	var playoutEnd time.Duration

	for {
		typ, data, err := w.conn.Read(ctx)
		if err != nil {
			// A normal close is this caller hanging up at the end of the
			// scenario, not a fault on the agent's side. Recording it as a
			// server error put one fabricated agent error in the event log of
			// every call that completed cleanly.
			normalClose := websocket.CloseStatus(err) == websocket.StatusNormalClosure
			if ctx.Err() == nil && !errors.Is(err, io.EOF) && !normalClose {
				w.mark(metrics.ServerError, err.Error())
			}
			return
		}

		if typ == websocket.MessageBinary {
			at := w.log.Since()
			w.rec.Add(at, data)
			w.agentBytes.Add(int64(len(data)))
			if !speaking {
				speaking = true
				playoutEnd = at
				w.markBytes(metrics.AgentFirstAudio, len(data))
				w.emit(srvEvent{kind: metrics.AgentFirstAudio, at: at})
			}
			// A chunk starts playing when it arrives, or when the previous
			// chunk finishes -- whichever is later.
			if playoutEnd < at {
				playoutEnd = at
			}
			playoutEnd += audio.Duration(data, w.cfg.SampleRate)
			continue
		}

		var m serverMessage
		if err := json.Unmarshal(data, &m); err != nil {
			continue
		}

		switch m.Type {
		case "UserStartedSpeaking":
			at := w.mark(metrics.UserStartedSpeak, "")
			w.emit(srvEvent{kind: metrics.UserStartedSpeak, at: at})

		case "ConversationText":
			kind := metrics.AgentTranscript
			if m.Role == "user" {
				kind = metrics.UserTranscript
			}
			at := w.mark(kind, m.Content)
			w.emit(srvEvent{kind: kind, at: at, text: m.Content})

		case "AgentThinking":
			at := w.mark(metrics.AgentThinking, m.Content)
			w.emit(srvEvent{kind: metrics.AgentThinking, at: at})

		case "AgentStartedSpeaking":
			at := w.mark(metrics.AgentStartedSpeak, "")
			w.emit(srvEvent{kind: metrics.AgentStartedSpeak, at: at})

		case "AgentAudioDone":
			speaking = false
			at := w.mark(metrics.AgentAudioDone, "")
			w.emit(srvEvent{kind: metrics.AgentAudioDone, at: at, playout: playoutEnd})

		case "Error":
			text := firstNonEmpty(m.Description, m.Message, m.Code)
			at := w.mark(metrics.ServerError, text)
			w.emit(srvEvent{kind: metrics.ServerError, at: at, text: text})

		case "Warning":
			w.mark(metrics.ServerWarning, firstNonEmpty(m.Description, m.Message))
		}
	}
}

// emit hands an event to the turn loop. The log is the record of what
// happened; this channel only drives synchronization, so a full channel means
// the turn loop is busy and the event can be dropped without losing data.
func (w *worker) emit(ev srvEvent) {
	select {
	case w.events <- ev:
	default:
	}
}

func (w *worker) printf(format string, args ...any) {
	w.outMu.Lock()
	defer w.outMu.Unlock()
	fmt.Fprintf(w.cfg.Out, format, args...)
}

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if v != "" {
			return v
		}
	}
	return ""
}

// mark stamps an event against whichever turn is currently in flight.
func (w *worker) mark(k metrics.Kind, text string) time.Duration {
	return w.log.Mark(k, int(w.curTurn.Load()), text)
}

func (w *worker) markBytes(k metrics.Kind, n int) time.Duration {
	return w.log.MarkBytes(k, int(w.curTurn.Load()), n)
}
