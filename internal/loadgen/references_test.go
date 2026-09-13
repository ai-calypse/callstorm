package loadgen

import (
	"regexp"
	"strings"
	"testing"
)

func TestReferencesAreCited(t *testing.T) {
	refs := References()
	if len(refs) == 0 {
		t.Fatal("no reference lines")
	}
	date := regexp.MustCompile(`^\d{4}-\d{2}(-\d{2})?$`)
	seen := map[string]bool{}
	for _, r := range refs {
		if r.ID == "" || seen[r.ID] {
			t.Errorf("reference id %q is empty or repeated", r.ID)
		}
		seen[r.ID] = true

		switch r.Kind {
		case KindObserved, KindTarget, KindPerceptual:
		default:
			t.Errorf("%s: unknown kind %q", r.ID, r.Kind)
		}
		if r.Axis != AxisEndOfSpeech && r.Axis != AxisDetection {
			t.Errorf("%s: unknown axis %q", r.ID, r.Axis)
		}
		switch r.Percentile {
		case 0, 50, 90, 95, 99:
		default:
			t.Errorf("%s: percentile %d is not one a step reports", r.ID, r.Percentile)
		}
		if r.Ms <= 0 || (r.ToMs != 0 && r.ToMs <= r.Ms) {
			t.Errorf("%s: %v to %v is not a line or a range", r.ID, r.Ms, r.ToMs)
		}

		// A number without its clock is exactly what these lines replaced.
		if strings.TrimSpace(r.Boundary) == "" || strings.TrimSpace(r.Placement) == "" {
			t.Errorf("%s: needs the source's own boundary and why it sits on %s", r.ID, r.Axis)
		}
		if len(r.Cites) == 0 {
			t.Errorf("%s: uncited", r.ID)
		}
		for _, c := range r.Cites {
			if c.Source == "" || c.Title == "" || !date.MatchString(c.Published) {
				t.Errorf("%s: citation %+v needs a source, a title and a published date", r.ID, c)
			}
			if c.URL != "" && !strings.HasPrefix(c.URL, "https://") {
				t.Errorf("%s: citation URL %q is not https", r.ID, c.URL)
			}
		}
	}
}

func TestPlaceReadsTheLineOnItsOwnClock(t *testing.T) {
	s := StepReport{
		TTFA:       Summary{N: 20, P50Ms: 500, P95Ms: 900},
		ThinkSpeak: Summary{N: 20, P50Ms: 300, P95Ms: 700},
	}

	// No percentile stated, detection clock: read at p50 and p95 of think/speak.
	marks := Place(Reference{Ms: 800, Axis: AxisDetection}, s)
	if len(marks) != 2 || marks[0].ValueMs != 300 || marks[1].ValueMs != 700 || marks[0].Over || marks[1].Over {
		t.Errorf("detection marks = %+v, want p50 300 and p95 700, both under 800", marks)
	}

	// The same 800ms read at p95 from true end of speech is crossed. Which clock
	// a line sits on changes the answer, which is the reason for having both.
	if m := Place(Reference{Ms: 800, Percentile: 95, Axis: AxisEndOfSpeech}, s); len(m) != 1 || !m[0].Over || m[0].ValueMs != 900 {
		t.Errorf("end-of-speech mark = %+v, want p95 900 over", m)
	}

	// A range is crossed only past its top.
	if m := Place(Reference{Ms: 400, ToMs: 1700, Percentile: 50, Axis: AxisEndOfSpeech}, s); len(m) != 1 || m[0].Over {
		t.Errorf("500ms read as past a 400ms-1700ms range: %+v", m)
	}

	// A target that sends no transcript has no detection clock to read.
	if m := Place(Reference{Ms: 800, Axis: AxisDetection}, StepReport{TTFA: s.TTFA}); m != nil {
		t.Errorf("placed %+v on a clock with no samples", m)
	}
}
