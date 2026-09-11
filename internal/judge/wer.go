// Package judge scores what was said, as opposed to how fast it was said.
//
// Every other measurement in Callstorm is a duration. These are the ones that
// ask whether the conversation was any good: whether the agent heard the
// caller correctly, and whether it did what the scenario required.
package judge

import (
	"strings"
	"unicode"
)

// WER is the word error rate between what the caller actually said and what
// the agent's speech-to-text reported hearing.
//
// It matters because it is the one failure no latency metric can see. An agent
// can answer in 300ms, confidently, and be answering a different question: on a
// real run the caller read out order "four four eight one two" and the agent
// heard "four four eight two", thanked them for the order number, and carried
// on toward refunding an order that was never named.
//
// The caller's line is ground truth rather than an estimate, which is what
// makes this honest. Callstorm generated that audio from known text, so the
// reference is exactly what was spoken -- not a second transcription being
// compared against a first.
type WER struct {
	// Rate is (substitutions + deletions + insertions) / reference words. It
	// is not capped at 1: a transcript can invent more words than were said.
	Rate float64 `json:"rate"`

	Substitutions int `json:"substitutions"`
	Deletions     int `json:"deletions"`
	Insertions    int `json:"insertions"`

	// RefWords is the number of words actually spoken. A rate computed over a
	// handful of words is noisy, so the denominator travels with the result.
	RefWords int `json:"ref_words"`
}

// Score computes the word error rate of heard against said.
//
// Both sides are lowercased and stripped of punctuation first, because an STT
// that writes "four four eight one two" where the caller's script said
// "Four four eight one two." has made no error worth reporting.
func Score(said, heard string) WER {
	ref := normalize(said)
	hyp := normalize(heard)

	s, d, i := align(ref, hyp)

	w := WER{
		Substitutions: s,
		Deletions:     d,
		Insertions:    i,
		RefWords:      len(ref),
	}
	if len(ref) > 0 {
		w.Rate = float64(s+d+i) / float64(len(ref))
	} else if len(hyp) > 0 {
		// Nothing was said but something was heard. Reporting 0 would read as
		// a perfect transcript of silence.
		w.Rate = 1
		w.Insertions = len(hyp)
	}
	return w
}

// normalize reduces a line to comparable words: lowercase, apostrophes removed,
// and everything else that is not a letter or digit treated as a separator.
//
// Apostrophes are deleted rather than split on. Splitting turns "it's" into
// two words and "its" into one, so a transcript that merely dropped the
// apostrophe would score a substitution and a deletion for an error the agent
// never made.
func normalize(s string) []string {
	s = strings.Map(func(r rune) rune {
		if r == '\'' || r == '’' {
			return -1
		}
		return r
	}, strings.ToLower(s))

	return strings.FieldsFunc(s, func(r rune) bool {
		return !unicode.IsLetter(r) && !unicode.IsDigit(r)
	})
}

// align runs Levenshtein over the two word sequences and walks the table back
// to separate the three kinds of error. The counts matter more than the total:
// deletions mean the agent missed words the caller said, insertions mean it
// heard words nobody spoke, and those are different problems.
func align(ref, hyp []string) (subs, dels, ins int) {
	n, m := len(ref), len(hyp)

	// d[i][j] is the edit distance between ref[:i] and hyp[:j].
	d := make([][]int, n+1)
	for i := range d {
		d[i] = make([]int, m+1)
		d[i][0] = i
	}
	for j := 0; j <= m; j++ {
		d[0][j] = j
	}
	for i := 1; i <= n; i++ {
		for j := 1; j <= m; j++ {
			if ref[i-1] == hyp[j-1] {
				d[i][j] = d[i-1][j-1]
				continue
			}
			d[i][j] = 1 + min3(
				d[i-1][j-1], // substitution
				d[i-1][j],   // deletion
				d[i][j-1],   // insertion
			)
		}
	}

	// Walk back from the corner, preferring the move that produced this cell.
	for i, j := n, m; i > 0 || j > 0; {
		switch {
		case i > 0 && j > 0 && ref[i-1] == hyp[j-1] && d[i][j] == d[i-1][j-1]:
			i, j = i-1, j-1
		case i > 0 && j > 0 && d[i][j] == d[i-1][j-1]+1:
			subs++
			i, j = i-1, j-1
		case i > 0 && d[i][j] == d[i-1][j]+1:
			dels++
			i--
		default:
			ins++
			j--
		}
	}
	return subs, dels, ins
}

func min3(a, b, c int) int {
	if b < a {
		a = b
	}
	if c < a {
		a = c
	}
	return a
}
