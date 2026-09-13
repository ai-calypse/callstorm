// Package chart renders a sweep report as a standalone SVG.
//
// p50/p95/p99 are the same measurement at rising severity, not three unrelated
// series, so they use one hue stepped light to dark rather than three
// categorical colors. Reading the darkest line as "the worst case" then needs
// no legend lookup.
package chart

import (
	"fmt"
	"math"
	"os"
	"sort"
	"strings"

	"github.com/yakshgandhi/callstorm/internal/loadgen"
)

// Ordinal ramp, single hue, light to dark. Validated against the light surface:
// monotone lightness, adjacent dL >= 0.06, light end 2.06:1 on #fcfcfb.
const (
	colorP50 = "#86b6ef"
	colorP95 = "#2a78d6"
	colorP99 = "#104281"

	surface = "#fcfcfb"
)

const (
	inkPrimary   = "#0b0b0b"
	inkSecondary = "#52514e"
	inkMuted     = "#8a8880"
	gridLine     = "#e6e5e0"

	statusCritical = "#d03b3b"
	statusWarning  = "#fab219"
	statusGood     = "#0ca30c"

	width   = 900
	height  = 500
	padL    = 78
	padR    = 132 // room for direct labels at the line ends
	padT    = 96
	padB    = 96
	fontStk = "ui-sans-serif,-apple-system,BlinkMacSystemFont,'Segoe UI',Roboto,Helvetica,Arial,sans-serif"
)

type series struct {
	name  string
	color string
	vals  []float64 // NaN where the step produced no samples
}

// RenderSweep writes the concurrency-versus-latency chart for a load run.
func RenderSweep(path string, rep *loadgen.Report) error {
	if len(rep.Steps) < 2 {
		return fmt.Errorf("need at least 2 steps to plot a curve, got %d", len(rep.Steps))
	}

	n := len(rep.Steps)
	all := []series{
		{name: "p50", color: colorP50, vals: make([]float64, n)},
		{name: "p95", color: colorP95, vals: make([]float64, n)},
		{name: "p99", color: colorP99, vals: make([]float64, n)},
	}
	for i, s := range rep.Steps {
		if s.TTFA.N == 0 {
			all[0].vals[i], all[1].vals[i], all[2].vals[i] = math.NaN(), math.NaN(), math.NaN()
			continue
		}
		all[0].vals[i] = s.TTFA.P50Ms
		all[1].vals[i] = s.TTFA.P95Ms
		all[2].vals[i] = s.TTFA.P99Ms
	}

	// The fail line is 2x the baseline p95: the threshold the verdicts use, so
	// the chart and the report card cannot disagree.
	var baseP95 float64
	for _, s := range rep.Steps {
		if s.Name == rep.Baseline {
			baseP95 = s.TTFA.P95Ms
		}
	}
	failAt := baseP95 * 2

	yMax := niceCeil(math.Max(maxOf(all), failAt) * 1.12)
	plotW := float64(width - padL - padR)
	plotH := float64(height - padT - padB)

	x := func(i int) float64 {
		if n == 1 {
			return float64(padL) + plotW/2
		}
		return float64(padL) + plotW*float64(i)/float64(n-1)
	}
	y := func(v float64) float64 {
		return float64(padT) + plotH*(1-v/yMax)
	}

	var b strings.Builder
	fmt.Fprintf(&b, `<svg xmlns="http://www.w3.org/2000/svg" width="%d" height="%d" viewBox="0 0 %d %d" font-family="%s">`,
		width, height, width, height, fontStk)
	fmt.Fprintf(&b, `<rect width="%d" height="%d" fill="%s"/>`, width, height, surface)

	// Title block.
	fmt.Fprintf(&b, `<text x="%d" y="34" font-size="17" font-weight="600" fill="%s">Time to first audio under load</text>`,
		padL, inkPrimary)
	sub := fmt.Sprintf("%s &#183; %s", esc(rep.Scenario), esc(shortTarget(rep.Target)))
	fmt.Fprintf(&b, `<text x="%d" y="55" font-size="12.5" fill="%s">%s</text>`, padL, inkSecondary, sub)

	// Legend: identity is never color-alone, so a swatch sits beside ink text.
	lx := float64(padL)
	for _, s := range all {
		fmt.Fprintf(&b, `<rect x="%.1f" y="66" width="10" height="10" rx="2" fill="%s"/>`, lx, s.color)
		fmt.Fprintf(&b, `<text x="%.1f" y="75" font-size="12" fill="%s">%s</text>`, lx+15, inkSecondary, s.name)
		lx += 56
	}

	// Y grid and ticks.
	for _, t := range ticks(yMax) {
		yy := y(t)
		fmt.Fprintf(&b, `<line x1="%d" y1="%.1f" x2="%.1f" y2="%.1f" stroke="%s" stroke-width="1"/>`,
			padL, yy, float64(width-padR), yy, gridLine)
		fmt.Fprintf(&b, `<text x="%d" y="%.1f" font-size="11" fill="%s" text-anchor="end">%s</text>`,
			padL-10, yy+4, inkMuted, fmtMs(t))
	}
	fmt.Fprintf(&b, `<text transform="translate(20,%.1f) rotate(-90)" font-size="11.5" fill="%s" text-anchor="middle">time to first audio (ms)</text>`,
		float64(padT)+plotH/2, inkSecondary)

	// Fail threshold. Dashed, labelled, status color plus text so the rule is
	// never carried by hue alone.
	if failAt > 0 && failAt <= yMax {
		fy := y(failAt)
		fmt.Fprintf(&b, `<line x1="%d" y1="%.1f" x2="%.1f" y2="%.1f" stroke="%s" stroke-width="1.5" stroke-dasharray="5 4"/>`,
			padL, fy, float64(width-padR), fy, statusCritical)
		fmt.Fprintf(&b, `<text x="%.1f" y="%.1f" font-size="11" fill="%s" text-anchor="end">2&#215; baseline &#183; fail</text>`,
			float64(width-padR)-4, fy-6, statusCritical)
	}

	// Published reference lines defined on this chart's clock, each labelled
	// with its source so none of them reads as the fail rule above. Drawn only
	// where they fall inside the data's own range: stretching the axis to reach
	// a line would flatten the curve the chart exists to show.
	for _, r := range rep.References {
		if r.Axis != loadgen.AxisEndOfSpeech || r.Ms > yMax {
			continue
		}
		label := esc(r.Label)
		if len(r.Cites) > 0 {
			label += " &#183; " + esc(r.Cites[0].Source)
		}
		if r.ToMs > r.Ms {
			top := y(math.Min(r.ToMs, yMax))
			fmt.Fprintf(&b, `<rect x="%d" y="%.1f" width="%.1f" height="%.1f" fill="%s" opacity="0.14"/>`,
				padL, top, plotW, y(r.Ms)-top, inkMuted)
		} else {
			fmt.Fprintf(&b, `<line x1="%d" y1="%.1f" x2="%.1f" y2="%.1f" stroke="%s" stroke-width="1" stroke-dasharray="1 3"/>`,
				padL, y(r.Ms), float64(width-padR), y(r.Ms), inkMuted)
		}
		fmt.Fprintf(&b, `<text x="%.1f" y="%.1f" font-size="10.5" fill="%s">%s</text>`,
			float64(padL)+6, y(r.Ms)-5, inkSecondary, label)
	}

	// X axis: one tick per step, labelled with concurrency and verdict.
	axisY := float64(padT) + plotH
	fmt.Fprintf(&b, `<line x1="%d" y1="%.1f" x2="%.1f" y2="%.1f" stroke="%s" stroke-width="1"/>`,
		padL, axisY, float64(width-padR), axisY, "#cfcec8")
	for i, s := range rep.Steps {
		xx := x(i)
		fmt.Fprintf(&b, `<text x="%.1f" y="%.1f" font-size="12" fill="%s" text-anchor="middle">%d</text>`,
			xx, axisY+20, inkPrimary, s.Concurrency)
		col, label := verdictStyle(s.Verdict)
		fmt.Fprintf(&b, `<text x="%.1f" y="%.1f" font-size="10.5" fill="%s" text-anchor="middle">%s</text>`,
			xx, axisY+36, col, label)
	}
	fmt.Fprintf(&b, `<text x="%.1f" y="%.1f" font-size="11.5" fill="%s" text-anchor="middle">concurrent callers</text>`,
		float64(padL)+plotW/2, axisY+58, inkSecondary)

	// Series. 2px lines, markers ringed in the surface color so overlaps stay
	// readable where the percentiles converge.
	for _, s := range all {
		fmt.Fprintf(&b, `<path d="%s" fill="none" stroke="%s" stroke-width="2" stroke-linejoin="round" stroke-linecap="round"/>`,
			pathOf(s.vals, x, y), s.color)
		for i, v := range s.vals {
			if math.IsNaN(v) {
				continue
			}
			fmt.Fprintf(&b, `<circle cx="%.1f" cy="%.1f" r="4.5" fill="%s" stroke="%s" stroke-width="2"/>`,
				x(i), y(v), s.color, surface)
		}
	}

	// Direct labels at the right end, nudged apart when the lines converge.
	type lbl struct {
		text string
		y    float64
	}
	var labels []lbl
	for _, s := range all {
		if v := lastValid(s.vals); !math.IsNaN(v) {
			labels = append(labels, lbl{fmt.Sprintf("%s &#183; %s", s.name, fmtMs(v)), y(v)})
		}
	}
	sort.Slice(labels, func(i, j int) bool { return labels[i].y < labels[j].y })
	for i := 1; i < len(labels); i++ {
		if gap := labels[i].y - labels[i-1].y; gap < 15 {
			labels[i].y = labels[i-1].y + 15
		}
	}
	for _, l := range labels {
		fmt.Fprintf(&b, `<text x="%.1f" y="%.1f" font-size="11.5" fill="%s">%s</text>`,
			float64(width-padR)+10, l.y+4, inkSecondary, l.text)
	}

	// Breakpoint callout: the number the run exists to produce.
	if bp := rep.Breakpoint(); bp != nil {
		for i, s := range rep.Steps {
			if s.Name != bp.Name {
				continue
			}
			xx := x(i)
			fmt.Fprintf(&b, `<line x1="%.1f" y1="%d" x2="%.1f" y2="%.1f" stroke="%s" stroke-width="1" stroke-dasharray="3 3" opacity="0.7"/>`,
				xx, padT, xx, axisY, statusCritical)
			fmt.Fprintf(&b, `<text x="%.1f" y="%d" font-size="11.5" font-weight="600" fill="%s" text-anchor="%s">breaks at %d concurrent (%.2f&#215;)</text>`,
				xx+labelNudge(i, n), padT-8, statusCritical, anchorFor(i, n), bp.Concurrency, bp.P95Ratio)
		}
	}

	b.WriteString(`</svg>`)
	return os.WriteFile(path, []byte(b.String()), 0o644)
}

func pathOf(vals []float64, x func(int) float64, y func(float64) float64) string {
	var sb strings.Builder
	pen := "M"
	for i, v := range vals {
		if math.IsNaN(v) {
			pen = "M"
			continue
		}
		fmt.Fprintf(&sb, "%s%.1f %.1f ", pen, x(i), y(v))
		pen = "L"
	}
	return strings.TrimSpace(sb.String())
}

func verdictStyle(v string) (color, label string) {
	switch v {
	case "pass":
		return statusGood, "pass"
	case "warn":
		return "#8a6200", "warn" // darkened for contrast on the light surface
	case "fail":
		return statusCritical, "fail"
	default:
		return inkMuted, "&#183;"
	}
}

func shortTarget(t string) string {
	if t == "" {
		return "deepgram voice agent"
	}
	return t
}

func anchorFor(i, n int) string {
	if i >= n-1 {
		return "end"
	}
	return "middle"
}

func labelNudge(i, n int) float64 {
	if i >= n-1 {
		return 4
	}
	return 0
}

func lastValid(vals []float64) float64 {
	for i := len(vals) - 1; i >= 0; i-- {
		if !math.IsNaN(vals[i]) {
			return vals[i]
		}
	}
	return math.NaN()
}

func maxOf(all []series) float64 {
	m := 0.0
	for _, s := range all {
		for _, v := range s.vals {
			if !math.IsNaN(v) && v > m {
				m = v
			}
		}
	}
	return m
}

func niceCeil(v float64) float64 {
	if v <= 0 {
		return 1
	}
	mag := math.Pow(10, math.Floor(math.Log10(v)))
	for _, s := range []float64{1, 1.5, 2, 2.5, 5, 10} {
		if v <= s*mag {
			return s * mag
		}
	}
	return 10 * mag
}

func ticks(max float64) []float64 {
	const want = 5
	step := niceCeil(max / want)
	var out []float64
	for t := 0.0; t <= max+step/2; t += step {
		out = append(out, t)
	}
	return out
}

func fmtMs(v float64) string {
	if v >= 1000 {
		return fmt.Sprintf("%.2fs", v/1000)
	}
	return fmt.Sprintf("%.0fms", v)
}

func esc(s string) string {
	r := strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;", `"`, "&quot;")
	return r.Replace(s)
}
