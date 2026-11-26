package hyperplot

import (
	"context"
	"fmt"
	"html/template"
	"io"
	"math"

	"github.com/ironystock/hyperplot/internal/svg"
)

const (
	sparkWidth  = 120
	sparkHeight = 28
	sparkMin    = 10
	sparkPad    = 4 // room for the end dot and round line caps
)

// Sparkline is a compact, axis-free line for stat tiles and tables: a 2px
// line, an optional area wash and an accent dot on the last value. Missing
// values (NaN, ±Inf) break the line.
//
// It has no title or tooltip; its accessible name (aria-label) is Label
// followed by the first, minimum, maximum and last values. Its root is
// <svg class="hp-spark">, not a chart (svg.hp), so the client and spec
// validators leave it alone; inside, it uses the same classes as charts
// (hp-s1, hp-line, hp-area, hp-end), so the same stylesheet themes it.
type Sparkline struct {
	// Values are plotted at even horizontal spacing.
	Values []float64
	// Width and Height are the size in pixels, 120 × 28 by default. Each
	// must be at least 10 and at most 32768.
	Width, Height float64
	// Label names the data in the accessible name, such as "Requests per
	// second".
	Label string
	// Area adds a light wash under the line.
	Area bool
	// Unit is appended to the values in the accessible name, such as
	// " ms", so a tile showing "212 ms" is not read out as "212".
	Unit string
	// Format formats the values in the accessible name. nil uses the
	// same precise, compact format as chart tooltips.
	Format func(float64) string
}

// WriteTo renders the sparkline and writes it to w. It implements
// [io.WriterTo]. Nothing is written if rendering fails.
func (s Sparkline) WriteTo(w io.Writer) (int64, error) {
	b, err := s.Render()
	if err != nil {
		return 0, err
	}
	n, err := w.Write(b)
	return int64(n), err
}

// HTML renders the sparkline for use in an html/template, so templates
// need no template.HTML conversion of their own. The output is escaped by
// construction, so it is safe to mark as trusted HTML.
func (s Sparkline) HTML() (template.HTML, error) {
	b, err := s.Render()
	if err != nil {
		return "", err
	}
	return template.HTML(b), nil //nolint:gosec // all text in b is escaped by the SVG writer
}

// Component returns the sparkline as a [Component], for templ templates.
// It renders the sparkline as it is when Component is called.
func (s Sparkline) Component() Component { return sparkComponent{s} }

type sparkComponent struct{ s Sparkline }

// Render renders the sparkline to w, first checking that ctx is not done.
func (sc sparkComponent) Render(ctx context.Context, w io.Writer) error {
	if ctx != nil {
		if err := ctx.Err(); err != nil {
			return err
		}
	}
	_, err := sc.s.WriteTo(w)
	return err
}

// Render renders the sparkline as an <svg> element. It returns an error
// only for an out-of-range size.
func (s Sparkline) Render() ([]byte, error) {
	w, err := sparkDim("Width", s.Width, sparkWidth)
	if err != nil {
		return nil, err
	}
	h, err := sparkDim("Height", s.Height, sparkHeight)
	if err != nil {
		return nil, err
	}
	lo, hi := math.Inf(1), math.Inf(-1)
	first, last := -1, -1
	for i, v := range s.Values {
		if !valid(v) {
			continue
		}
		if first < 0 {
			first = i
		}
		last = i
		lo, hi = math.Min(lo, v), math.Max(hi, v)
	}
	var out svg.Writer
	out.Open("svg",
		svg.A("xmlns", "http://www.w3.org/2000/svg"),
		// Not class hp: a chart is any svg.hp (docs/spec.md §1, §6.4), and
		// a sparkline has none of a chart's contract (§3.2), so clients
		// and validators would take it for a broken chart.
		svg.A("class", "hp-spark"),
		svg.A("viewBox", "0 0 "+svg.Num(w)+" "+svg.Num(h)),
		svg.N("width", w),
		svg.N("height", h),
		svg.A("role", "img"),
		svg.A("aria-label", s.ariaLabel(first, last, lo, hi)),
	)
	if first >= 0 {
		xs := make([]float64, len(s.Values))
		ys := make([]float64, len(s.Values))
		base := make([]float64, len(s.Values))
		lo, hi = padDegenerate(lo, hi)
		n := len(s.Values)
		for i, v := range s.Values {
			if n == 1 {
				xs[i] = w / 2
			} else {
				xs[i] = sparkPad + float64(i)*(w-2*sparkPad)/float64(n-1)
			}
			ys[i], base[i] = math.NaN(), math.NaN()
			if valid(v) {
				ys[i] = h - sparkPad - (v-lo)/(hi-lo)*(h-2*sparkPad)
				base[i] = h - sparkPad
			}
		}
		out.Open("g", svg.A("class", "hp-s1"))
		if s.Area {
			out.Void("path", svg.A("class", "hp-area"), svg.A("d", areaPath(xs, base, ys, CurveLinear)),
				svg.A("fill", "currentColor"), svg.A("fill-opacity", "0.1"))
		}
		out.Void("path", svg.A("class", "hp-line"), svg.A("d", linePath(xs, ys, CurveLinear)),
			svg.A("fill", "none"), svg.A("stroke", "currentColor"), svg.A("stroke-width", "2"),
			svg.A("stroke-linejoin", "round"), svg.A("stroke-linecap", "round"))
		out.Void("circle", svg.A("class", "hp-end"), svg.N("cx", xs[last]), svg.N("cy", ys[last]), svg.A("r", "2.5"),
			svg.A("fill", "currentColor"))
		out.Close("g")
	}
	out.Close("svg")
	return out.Bytes(), nil
}

func (s Sparkline) ariaLabel(first, last int, lo, hi float64) string {
	label := s.Label
	if label == "" {
		label = "Sparkline"
	}
	if first < 0 {
		return label + ": no data"
	}
	unit := shorten(s.Unit, maxUnit)
	value := func(v float64) string {
		if s.Format != nil {
			return s.Format(v) + unit
		}
		return formatValue(v) + unit
	}
	return label + ": first " + value(s.Values[first]) +
		", min " + value(lo) +
		", max " + value(hi) +
		", last " + value(s.Values[last])
}

func sparkDim(name string, v, def float64) (float64, error) {
	if v == 0 {
		return def, nil
	}
	if !(v >= sparkMin && v <= maxSize) {
		return 0, fmt.Errorf("hyperplot: Sparkline %s %v is out of range [%d, %d]", name, v, sparkMin, maxSize)
	}
	return v, nil
}
