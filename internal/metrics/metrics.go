// Package metrics is the clock.
//
// Every interesting instant in a call becomes an Event stamped relative to
// call start. TurnMetrics are derived from those instants, never measured
// separately, so the report card and the raw event log can never disagree.
package metrics

import (
	"math"
	"sort"
	"sync"
	"time"
)

type Kind string

const (
	CallStart         Kind = "call_start"
	Welcome           Kind = "welcome"
	SettingsApplied   Kind = "settings_applied"
	CallerSpeechStart Kind = "caller_speech_start"
	CallerSpeechEnd   Kind = "caller_speech_end"
	CallerYielded     Kind = "caller_yielded"
	AgentYielded      Kind = "agent_yielded"
	UserStartedSpeak  Kind = "user_started_speaking"
	UserTranscript    Kind = "user_transcript"
	AgentThinking     Kind = "agent_thinking"
	AgentFirstAudio   Kind = "agent_first_audio"
	AgentStartedSpeak Kind = "agent_started_speaking"
	AgentAudioDone    Kind = "agent_audio_done"
	AgentTranscript   Kind = "agent_transcript"
	TurnTimeout       Kind = "turn_timeout"
	ServerError       Kind = "server_error"
	ServerWarning     Kind = "server_warning"
	CallEnd           Kind = "call_end"
)

// Event is one timestamped thing that happened during a call.
type Event struct {
	TMs   float64 `json:"t_ms"`
	Kind  Kind    `json:"kind"`
	Turn  int     `json:"turn"`
	Text  string  `json:"text,omitempty"`
	Bytes int     `json:"bytes,omitempty"`
}

// Log is a concurrency-safe, monotonically stamped event log for one call.
type Log struct {
	mu     sync.Mutex
	t0     time.Time
	events []Event
}

func NewLog() *Log {
	l := &Log{t0: time.Now()}
	l.Mark(CallStart, 0, "")
	return l
}

// Since is the time elapsed since call start.
func (l *Log) Since() time.Duration { return time.Since(l.t0) }

// Mark stamps an event at the current instant and returns that instant.
func (l *Log) Mark(k Kind, turn int, text string) time.Duration {
	d := time.Since(l.t0)
	l.MarkAt(d, k, turn, text)
	return d
}

// MarkAt records an event at a caller-computed instant. Used where the true
// instant is derived rather than observed -- the moment the caller's last
// audio frame finishes playing, for example, which is what "the caller
// stopped speaking" actually means.
func (l *Log) MarkAt(at time.Duration, k Kind, turn int, text string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.events = append(l.events, Event{
		TMs:  math.Round(float64(at.Microseconds())/1000*100) / 100,
		Kind: k,
		Turn: turn,
		Text: text,
	})
}

// MarkBytes stamps an event carrying an audio payload size.
func (l *Log) MarkBytes(k Kind, turn int, n int) time.Duration {
	d := time.Since(l.t0)
	l.mu.Lock()
	defer l.mu.Unlock()
	l.events = append(l.events, Event{
		TMs:   math.Round(float64(d.Microseconds())/1000*100) / 100,
		Kind:  k,
		Turn:  turn,
		Bytes: n,
	})
	return d
}

// Events returns a sorted copy of the log.
func (l *Log) Events() []Event {
	l.mu.Lock()
	defer l.mu.Unlock()
	out := make([]Event, len(l.events))
	copy(out, l.events)
	sort.SliceStable(out, func(i, j int) bool { return out[i].TMs < out[j].TMs })
	return out
}

// TurnMetric is the report card for a single caller turn. Every field is a
// difference between two instants in the event log.
type TurnMetric struct {
	Turn       int    `json:"turn"`
	CallerText string `json:"caller_text"`
	AgentText  string `json:"agent_text"`

	// HeardText is what the agent's STT actually transcribed the caller as
	// saying. Where it diverges from CallerText the agent was answering a
	// different question than the one asked, which no latency number reveals.
	HeardText string `json:"heard_text"`

	// TTFA is the headline number: the caller stopped speaking, and this is
	// how long the line stayed dead before the agent's first audio arrived.
	// It goes negative when the agent started talking over the caller.
	TTFA time.Duration `json:"-"`

	// Endpointing and ThinkSpeak decompose TTFA. Endpointing is how long the
	// agent took to notice the caller had finished; ThinkSpeak is the LLM plus
	// TTS time after that. They tell an agent builder which half to fix.
	// Endpointing goes negative when the agent decided the caller was done
	// while the caller was still speaking.
	Endpointing time.Duration `json:"-"`
	ThinkSpeak  time.Duration `json:"-"`

	// AgentSpeech is how long the agent's reply takes to play, derived from
	// the audio received rather than from how long it took to arrive --
	// Deepgram streams TTS faster than realtime, so arrival time would
	// understate it badly.
	AgentSpeech time.Duration `json:"-"`

	// TurnLatency runs from the caller falling silent to the agent's audio
	// finishing playout: the caller's whole wait.
	TurnLatency time.Duration `json:"-"`

	// PacingDrift is how far the caller's audio departed from realtime. It
	// measures the harness, not the agent: every other number here is only
	// meaningful if this stays near zero, so the run reports it rather than
	// asking anyone to take realtime pacing on faith.
	PacingDrift time.Duration `json:"-"`

	// CallerYielded records that the caller stopped mid-sentence because the
	// agent started talking over them, which is what a real caller does.
	CallerYielded bool `json:"caller_yielded,omitempty"`

	// BargeInYield is how long the agent kept talking after this turn's caller
	// cut in on it. It is only measured on a turn that deliberately
	// interrupted, and it is the number that says whether an agent can be
	// stopped: one that keeps going has taken the floor away from the person
	// paying for the call.
	//
	// Zero means the agent was never observed to stop, which is worse than a
	// large value and is reported as a failure rather than a good score.
	BargeInYield time.Duration `json:"-"`

	// BargedIn marks a turn that interrupted the agent, so a zero yield can be
	// told apart from a turn that never tried.
	BargedIn bool `json:"barged_in,omitempty"`

	Failed     bool   `json:"failed"`
	FailReason string `json:"fail_reason,omitempty"`

	// Millisecond mirrors of the durations above, for the JSON artifact.
	TTFAMs        float64 `json:"ttfa_ms"`
	EndpointingMs float64 `json:"endpointing_ms"`
	ThinkSpeakMs  float64 `json:"think_speak_ms"`
	AgentSpeechMs float64 `json:"agent_speech_ms"`
	TurnLatencyMs float64 `json:"turn_latency_ms"`
	PacingDriftMs float64 `json:"pacing_drift_ms"`

	// BargeInYieldMs is the millisecond mirror of BargeInYield.
	BargeInYieldMs float64 `json:"barge_in_yield_ms,omitempty"`
}

// Finalize populates the millisecond mirrors from the duration fields.
func (t *TurnMetric) Finalize() {
	// Negatives are preserved: an agent that answers before the caller has
	// finished is a finding, not a missing measurement. Only an exact zero
	// means "never observed".
	ms := func(d time.Duration) float64 {
		return math.Round(float64(d.Microseconds())/1000*100) / 100
	}
	t.TTFAMs = ms(t.TTFA)
	t.EndpointingMs = ms(t.Endpointing)
	t.ThinkSpeakMs = ms(t.ThinkSpeak)
	t.AgentSpeechMs = ms(t.AgentSpeech)
	t.TurnLatencyMs = ms(t.TurnLatency)
	t.PacingDriftMs = ms(t.PacingDrift)
	t.BargeInYieldMs = ms(t.BargeInYield)
}

// Percentile returns the nearest-rank pth percentile of ds.
func Percentile(ds []time.Duration, p float64) time.Duration {
	if len(ds) == 0 {
		return 0
	}
	s := make([]time.Duration, len(ds))
	copy(s, ds)
	sort.Slice(s, func(i, j int) bool { return s[i] < s[j] })
	rank := int(math.Ceil(p/100*float64(len(s)))) - 1
	if rank < 0 {
		rank = 0
	}
	if rank >= len(s) {
		rank = len(s) - 1
	}
	return s[rank]
}
