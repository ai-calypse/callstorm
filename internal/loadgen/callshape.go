package loadgen

import (
	"sort"
	"strings"
	"time"
	"unicode"

	"github.com/yakshgandhi/callstorm/internal/metrics"
)

// The lines a caller's wait is sorted against. 800ms is Hamming's target for
// the wait from the end of the caller's speech ("Voice AI latency: what's fast,
// what's slow", January 2026). 1,200ms is where Coval says callers start
// repeating themselves, talking over the agent or hanging up ("Voice AI
// latency", March 2026). Past two seconds is dead air.
const (
	waitTarget = 800 * time.Millisecond
	waitRepeat = 1200 * time.Millisecond
)

// WaitBands counts one step's timed turns by how long the caller waited.
//
// A p95 says how long the slow turns were. This says how often a caller
// crossed a line people notice, which is what a team setting a target asks.
type WaitBands struct {
	Turns    int `json:"turns"`
	UpTo800  int `json:"up_to_800ms"`
	To1200   int `json:"800_to_1200ms"`
	To2000   int `json:"1200_to_2000ms"`
	Over2000 int `json:"over_2000ms"`
}

func (w *WaitBands) add(ttfa time.Duration) {
	w.Turns++
	switch {
	case ttfa <= waitTarget:
		w.UpTo800++
	case ttfa <= waitRepeat:
		w.To1200++
	case ttfa <= deadAirThreshold:
		w.To2000++
	default:
		w.Over2000++
	}
}

// TurnWait is one turn of the conversation across a step's calls.
//
// A step's percentiles pool every turn of every call. A turn that is slow in
// every call -- a closing line the agent takes three seconds to answer -- is
// one slow turn in five in that pool and never stands out. Grouped by its
// position in the call, it does.
type TurnWait struct {
	Turn int `json:"turn"`

	// CallerLine is what the caller most often said on this turn. In a
	// branching scenario the same turn can carry different lines.
	CallerLine string `json:"caller_line"`

	TTFA             Summary `json:"ttfa"`
	EndpointingP50Ms float64 `json:"endpointing_p50_ms"`
	ThinkSpeakP50Ms  float64 `json:"think_speak_p50_ms"`
}

// analyzeCalls fills in what a step's calls show beyond its percentiles: the
// wait at each turn of the conversation, how often a wait crossed the lines
// people notice, and replies the agent repeated. calls holds the turns of each
// call that connected. It reads only what the calls log keeps, so a report
// written before these figures existed can gain them; see Refresh.
func analyzeCalls(s *StepReport, calls [][]metrics.TurnMetric) {
	type acc struct {
		ttfa, endpointing, think []time.Duration
		lines                    map[string]int
	}
	byTurn := map[int]*acc{}
	var bands WaitBands
	repeated, callsWith := 0, 0

	for _, turns := range calls {
		if n := repeatedReplies(turns); n > 0 {
			repeated += n
			callsWith++
		}
		for _, t := range turns {
			// Left out as the step's percentiles leave them out: a turn the
			// agent talked over has no wait, and one with no audio was never
			// answered.
			if t.CallerYielded || t.TTFA <= 0 {
				continue
			}
			bands.add(t.TTFA)
			a := byTurn[t.Turn]
			if a == nil {
				a = &acc{lines: map[string]int{}}
				byTurn[t.Turn] = a
			}
			a.ttfa = append(a.ttfa, t.TTFA)
			if t.Endpointing > 0 {
				a.endpointing = append(a.endpointing, t.Endpointing)
			}
			if t.ThinkSpeak > 0 {
				a.think = append(a.think, t.ThinkSpeak)
			}
			a.lines[t.CallerText]++
		}
	}

	order := make([]int, 0, len(byTurn))
	for n := range byTurn {
		order = append(order, n)
	}
	sort.Ints(order)
	s.Turns = nil
	for _, n := range order {
		a := byTurn[n]
		s.Turns = append(s.Turns, TurnWait{
			Turn:             n,
			CallerLine:       commonest(a.lines),
			TTFA:             summarize(a.ttfa),
			EndpointingP50Ms: summarize(a.endpointing).P50Ms,
			ThinkSpeakP50Ms:  summarize(a.think).P50Ms,
		})
	}
	s.Waits = bands
	s.Conversation.RepeatedReplies = repeated
	s.Conversation.CallsWithRepeats = callsWith
}

// repeatedReplies counts replies in one call that say again what the agent
// already said earlier in it. Coval's loop detection looks for exactly this,
// repeated agent utterances as the sign of a stuck dialogue or voice, and
// Hamming puts repeated questions above 3% of turns as a problem. Two replies
// are the same when their words match, ignoring case and punctuation, or when
// both have at least four words and share at least 80% of their distinct
// words. The four words and 80% are Callstorm's own lines: close enough to
// catch a reworded repeat, far enough to leave "Thank you" and "Thank you, I
// will refund that" apart.
func repeatedReplies(turns []metrics.TurnMetric) int {
	var seen [][]string
	n := 0
	for _, t := range turns {
		w := words(t.AgentText)
		if len(w) == 0 {
			continue
		}
		for _, p := range seen {
			if sameReply(p, w) {
				n++
				break
			}
		}
		seen = append(seen, w)
	}
	return n
}

func words(s string) []string {
	return strings.FieldsFunc(strings.ToLower(s), func(r rune) bool {
		return !unicode.IsLetter(r) && !unicode.IsDigit(r) && r != '\''
	})
}

func sameReply(a, b []string) bool {
	if strings.Join(a, " ") == strings.Join(b, " ") {
		return true
	}
	if len(a) < 4 || len(b) < 4 {
		return false
	}
	set := map[string]int{}
	for _, w := range a {
		set[w] |= 1
	}
	for _, w := range b {
		set[w] |= 2
	}
	both := 0
	for _, v := range set {
		if v == 3 {
			both++
		}
	}
	return float64(both) >= 0.8*float64(len(set))
}

// commonest returns the most frequent string, the alphabetically first on a
// tie, so the same calls always give the same report.
func commonest(counts map[string]int) string {
	best, n := "", 0
	for s, c := range counts {
		if c > n || (c == n && s < best) {
			best, n = s, c
		}
	}
	return best
}

// connectedTurns is the turns of every call that connected.
func connectedTurns(outcomes []callOutcome) [][]metrics.TurnMetric {
	var out [][]metrics.TurnMetric
	for _, o := range outcomes {
		if o.err == nil {
			out = append(out, o.turns)
		}
	}
	return out
}

// Refresh recomputes, from the report's own calls, every figure a report gains
// after the load: turn waits, wait bands, repeated replies, the judge's split
// of slow calls against done, and the quality verdict. It is how a run placed
// before those figures existed gets them without placing a call.
//
// Latency and its verdict are left as written, and so is leading silence: the
// calls log keeps words and instants, not the audio that silence is found in.
func Refresh(rep *Report) {
	groups := map[string][][]metrics.TurnMetric{}
	for _, c := range rep.Calls {
		if c.Error != "" {
			continue
		}
		turns := make([]metrics.TurnMetric, len(c.Turns))
		for i, t := range c.Turns {
			t.Rehydrate()
			turns[i] = t
		}
		key := c.Cohort + "/" + c.Step
		groups[key] = append(groups[key], turns)
	}

	clean := ""
	if rep.Matrix != nil && len(rep.Matrix.Cohorts) > 0 {
		clean = rep.Matrix.Cohorts[0].Impairment.Name
	}
	for i := range rep.Steps {
		analyzeCalls(&rep.Steps[i], groups[clean+"/"+rep.Steps[i].Name])
	}
	if rep.Matrix != nil {
		for ci := range rep.Matrix.Cohorts {
			c := &rep.Matrix.Cohorts[ci]
			for i := range c.Steps {
				analyzeCalls(&c.Steps[i], groups[c.Impairment.Name+"/"+c.Steps[i].Name])
			}
		}
	}
	if rep.Judge != nil {
		rep.Judge.Waits = waitOutcome(rep.Calls, rep.Judge.Judgements)
	}
	rep.ScoreQuality()
}
