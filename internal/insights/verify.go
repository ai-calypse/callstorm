package insights

import (
	"encoding/json"
	"math"
	"regexp"
	"strconv"
	"strings"
)

// numberRE finds numbers as a person writes them: 2544, 2,544, 3.6.
var numberRE = regexp.MustCompile(`\d{1,3}(?:,\d{3})+(?:\.\d+)?|\d+(?:\.\d+)?`)

// partOfName reports whether the number starting at i follows a letter or an
// underscore: c40, p95, gpt-4o. Those digits name something rather than
// measure it.
func partOfName(s string, i int) bool {
	if i == 0 {
		return false
	}
	prev := s[i-1]
	return prev == '_' || (prev|0x20 >= 'a' && prev|0x20 <= 'z')
}

// numbersIn returns the quantities written in s, leaving out digits that are
// part of a name.
func numbersIn(s string) []float64 {
	var out []float64
	for _, loc := range numberRE.FindAllStringIndex(s, -1) {
		if partOfName(s, loc[0]) {
			continue
		}
		if v, err := strconv.ParseFloat(strings.ReplaceAll(s[loc[0]:loc[1]], ",", ""), 64); err == nil {
			out = append(out, v)
		}
	}
	return out
}

// factsOf collects every number the data holds, including numbers written
// inside its strings.
func factsOf(data []byte) []float64 {
	var v any
	if err := json.Unmarshal(data, &v); err != nil {
		return nil
	}
	var out []float64
	walk(v, &out)
	return out
}

func walk(v any, out *[]float64) {
	switch x := v.(type) {
	case float64:
		*out = append(*out, math.Abs(x))
	case string:
		*out = append(*out, numbersIn(x)...)
	case []any:
		for _, e := range x {
			walk(e, out)
		}
	case map[string]any:
		for _, e := range x {
			walk(e, out)
		}
	}
}

// unit is how a number in prose says what it measures.
type unit int

const (
	bare unit = iota
	percent
	millis
	seconds
	times
)

// unitAfter reads the unit written straight after a number.
func unitAfter(rest string) unit {
	rest = strings.TrimLeft(rest, " ")
	switch {
	case strings.HasPrefix(rest, "%"):
		return percent
	case strings.HasPrefix(rest, "ms"):
		return millis
	case strings.HasPrefix(rest, "×"):
		return times
	}
	word := rest
	if i := strings.IndexFunc(rest, func(r rune) bool { return !(r >= 'a' && r <= 'z') }); i >= 0 {
		word = rest[:i]
	}
	switch word {
	case "s", "sec", "secs", "second", "seconds":
		return seconds
	case "x":
		return times
	}
	return bare
}

// unchecked returns the numbers written in text that the data does not hold.
func unchecked(text string, facts []float64) []string {
	var bad []string
	for _, loc := range numberRE.FindAllStringIndex(text, -1) {
		if partOfName(text, loc[0]) {
			continue
		}
		s := text[loc[0]:loc[1]]
		v, err := strconv.ParseFloat(strings.ReplaceAll(s, ",", ""), 64)
		if err != nil || held(v, unitAfter(text[loc[1]:]), facts) {
			continue
		}
		bad = append(bad, s)
	}
	return bad
}

// held reports whether a number a reader was given is one the data holds, as
// stored or in the units the text gives it in: milliseconds read as seconds,
// a fraction read as a percentage. Rounding is allowed; a different value is
// not, and neither is a conversion the unit does not call for.
//
// A small whole number with no unit is allowed as it stands. It counts
// something -- 3 of 5 parts, the second turn -- far more often than it
// measures anything, and refusing it would drop honest sentences for the sake
// of a few invented ones. With a unit it is a measurement, and is checked.
func held(n float64, u unit, facts []float64) bool {
	if u == bare && n == math.Trunc(n) && n <= 12 {
		return true
	}
	for _, f := range facts {
		var forms []float64
		switch u {
		case percent:
			forms = []float64{f * 100, f}
		case millis, times:
			forms = []float64{f}
		case seconds:
			forms = []float64{f / 1000, f}
		default:
			forms = []float64{f, f * 100, f / 1000}
		}
		for _, c := range forms {
			if math.Abs(n-c) <= 0.02*c+0.05 {
				return true
			}
		}
	}
	return false
}
