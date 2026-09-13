package loadgen

import (
	"math"
	"strings"
	"time"
)

// deadAirThreshold is how long a line may stay silent after the caller stops
// before it stops feeling like thinking and starts feeling like a dropped call.
//
// Two seconds is where a person says "hello?". It is deliberately far above the
// latency an agent aims for, because this counts breakdowns rather than grades
// speed -- the percentiles already grade speed.
const deadAirThreshold = 2 * time.Second

// Conversation is how the call sounded, as opposed to how fast it was.
//
// Every field here comes from data the run already collected. None of it needs
// the agent's cooperation, an audio model, or a second pass over the recording:
// an agent's pace, its share of the talking and how often it cut the caller off
// are all decided by instants the harness already stamped.
type Conversation struct {
	// TalkRatio is the agent's share of the speaking, 0 to 1. Published
	// benchmarks put half of voice agents at 0.80 or above, which is an agent
	// lecturing rather than holding a conversation.
	TalkRatio float64 `json:"talk_ratio"`

	// Words per minute over each side's own speaking time, not wall clock, so a
	// slow agent is not flattered by the caller's pauses. Half of measured
	// agents pace above 190, which is faster than comfortable listening.
	AgentWPM  float64 `json:"agent_wpm"`
	CallerWPM float64 `json:"caller_wpm"`

	// Interruptions counts turns the agent talked over. InterruptionScore is
	// the published 5 x (1 - interruptions/turns), clamped to 0-5, so a run can
	// be read beside the platforms that report it that way.
	Interruptions     int     `json:"interruptions"`
	InterruptionRate  float64 `json:"interruption_rate"`
	InterruptionScore float64 `json:"interruption_score"`

	// DeadAirTurns counts turns where the line stayed silent past the
	// threshold, and DeadAirSeconds totals that silence. A p95 says how slow
	// the slow turns were; this says how often the call sounded broken.
	DeadAirTurns   int     `json:"dead_air_turns"`
	DeadAirSeconds float64 `json:"dead_air_seconds"`

	// RepeatedReplies counts replies that said again what the agent had already
	// said earlier in the same call, and CallsWithRepeats the calls they were
	// in. A dialogue stuck in a loop shows here before any latency figure moves.
	RepeatedReplies  int `json:"repeated_replies"`
	CallsWithRepeats int `json:"calls_with_repeats"`

	// AgentSpeechSeconds and CallerSpeechSeconds are the raw totals the ratios
	// come from, kept so a reader can check the arithmetic rather than take it.
	AgentSpeechSeconds  float64 `json:"agent_speech_seconds"`
	CallerSpeechSeconds float64 `json:"caller_speech_seconds"`

	Turns int `json:"turns"`
}

// Cost is what a step of the sweep actually cost to run.
//
// Voice agents bill by the minute, so cost is a function of how long calls last
// -- which is a function of how verbose the agent is, not only how fast. An
// agent that answers in 300ms and then talks for fifteen seconds is expensive
// in a way no latency percentile shows.
//
// The rate is supplied by the operator rather than baked in. A hardcoded price
// is wrong the moment a provider changes one, and a run that quietly reports
// last year's rate is worse than one that reports none.
type Cost struct {
	// AgentMinutes is billable time: the whole call, since a voice agent bills
	// for the session rather than for speech.
	AgentMinutes float64 `json:"agent_minutes"`

	// USD fields stay zero when no rate was given, which is how a reader tells
	// "free" from "not priced".
	RatePerMinute float64 `json:"rate_per_minute,omitempty"`
	USD           float64 `json:"usd,omitempty"`
	USDPerCall    float64 `json:"usd_per_call,omitempty"`
	USDPerTurn    float64 `json:"usd_per_turn,omitempty"`
}

// summarizeConversation folds one step's turns into how the calls sounded.
func summarizeConversation(outcomes []callOutcome) Conversation {
	var c Conversation
	var agentSpeech, callerSpeech, deadAir time.Duration
	var agentWords, callerWords int

	for _, o := range outcomes {
		if o.err != nil {
			continue
		}
		for _, t := range o.turns {
			c.Turns++

			if t.CallerYielded {
				c.Interruptions++
			}

			agentSpeech += t.AgentSpeech
			callerSpeech += t.CallerSpeech
			agentWords += len(strings.Fields(t.AgentText))
			callerWords += len(strings.Fields(t.CallerText))

			// Only a turn the caller finished can have dead air: one the agent
			// talked over never had a silence to measure.
			if !t.CallerYielded && t.TTFA > deadAirThreshold {
				c.DeadAirTurns++
				deadAir += t.TTFA
			}
		}
	}

	c.AgentSpeechSeconds = round1(agentSpeech.Seconds())
	c.CallerSpeechSeconds = round1(callerSpeech.Seconds())
	c.DeadAirSeconds = round1(deadAir.Seconds())

	if total := agentSpeech + callerSpeech; total > 0 {
		c.TalkRatio = round3(agentSpeech.Seconds() / total.Seconds())
	}
	c.AgentWPM = wpm(agentWords, agentSpeech)
	c.CallerWPM = wpm(callerWords, callerSpeech)

	if c.Turns > 0 {
		c.InterruptionRate = round3(float64(c.Interruptions) / float64(c.Turns))
		score := 5 * (1 - float64(c.Interruptions)/float64(c.Turns))
		c.InterruptionScore = round2(math.Max(0, math.Min(5, score)))
	}
	return c
}

// wpm is words over that speaker's own speaking time.
func wpm(words int, speech time.Duration) float64 {
	if speech <= 0 || words == 0 {
		return 0
	}
	return round1(float64(words) / speech.Minutes())
}

// summarizeCost prices one step from the calls it actually placed.
func summarizeCost(outcomes []callOutcome, ratePerMinute float64) Cost {
	var seconds float64
	calls, turns := 0, 0

	for _, o := range outcomes {
		if o.err != nil {
			// A call that never connected was not billed for a conversation.
			continue
		}
		calls++
		for _, t := range o.turns {
			turns++
			// The caller's own speech is billed too: the session is live for
			// all of it.
			seconds += t.CallerSpeech.Seconds() + t.TurnLatency.Seconds()
		}
	}

	c := Cost{AgentMinutes: round3(seconds / 60), RatePerMinute: ratePerMinute}
	if ratePerMinute <= 0 {
		return c
	}
	c.USD = round4(c.AgentMinutes * ratePerMinute)
	if calls > 0 {
		c.USDPerCall = round4(c.USD / float64(calls))
	}
	if turns > 0 {
		c.USDPerTurn = round4(c.USD / float64(turns))
	}
	return c
}

func round1(v float64) float64 { return math.Round(v*10) / 10 }
func round2(v float64) float64 { return math.Round(v*100) / 100 }
func round3(v float64) float64 { return math.Round(v*1000) / 1000 }
func round4(v float64) float64 { return math.Round(v*10000) / 10000 }
