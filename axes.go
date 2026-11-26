package hyperplot

import (
	"math"

	"github.com/ironystock/hyperplot/internal/svg"
	"github.com/ironystock/hyperplot/internal/textwidth"
)

// crisp places a 1px horizontal line on a half pixel so that it covers
// exactly one device pixel row at 1x.
func crisp(y float64) float64 { return math.Round(y) + 0.5 }

// baselineY returns the y of the baseline: the zero line when the chart
// has bars or areas and 0 is in the domain, and the plot bottom otherwise.
func (p *plan) baselineY() float64 {
	if p.hasBar || p.hasArea {
		lo, hi := math.Min(p.y.Domain[0], p.y.Domain[1]), math.Max(p.y.Domain[0], p.y.Domain[1])
		if lo <= 0 && hi >= 0 {
			return crisp(p.y.Map(0))
		}
	}
	return crisp(p.b)
}

func (p *plan) writeGrid(w *svg.Writer) {
	w.Open("g", svg.A("class", "hp-grid"), svg.A("aria-hidden", "true"),
		svg.A("stroke", "currentColor"), svg.A("stroke-opacity", "0.12"))
	if !p.c.Y.NoGrid {
		base := p.baselineY()
		for _, t := range p.yTicks {
			y := crisp(t.v)
			if y == base {
				continue // the baseline is drawn on top, one step darker
			}
			w.Void("line", svg.N("x1", p.l), svg.N("x2", p.r), svg.N("y1", y), svg.N("y2", y))
		}
	}
	w.Close("g")
}

// mutedText returns the attributes of secondary-ink text.
func mutedText(class string, x, y float64, extra ...svg.Attr) []svg.Attr {
	a := []svg.Attr{svg.A("class", class), svg.N("x", x), svg.N("y", y)}
	a = append(a, extra...)
	return append(a, svg.A("fill", "currentColor"), svg.A("fill-opacity", "0.7"))
}

func (p *plan) writeXAxis(w *svg.Writer) {
	w.Open("g", svg.A("class", "hp-axis hp-axis-x"), svg.A("aria-hidden", "true"))
	y := p.baselineY()
	w.Void("line", svg.A("class", "hp-baseline"), svg.N("x1", p.l), svg.N("x2", p.r), svg.N("y1", y), svg.N("y2", y),
		svg.A("stroke", "currentColor"), svg.A("stroke-opacity", "0.3"))
	for _, t := range p.xTicks {
		w.Elem("text", t.text, mutedText("hp-tick", t.v, p.xTickY, svg.A("text-anchor", t.anchor))...)
	}
	if p.xLabel != "" {
		w.Elem("text", p.xLabel, mutedText("hp-axis-label hp-xlabel", p.r, p.xLabelY, svg.A("text-anchor", "end"))...)
	}
	w.Close("g")
}

func (p *plan) writeYAxis(w *svg.Writer) {
	w.Open("g", svg.A("class", "hp-axis hp-axis-y"), svg.A("aria-hidden", "true"))
	if !p.c.Y.Hide {
		for _, t := range p.yTicks {
			w.Elem("text", t.text, mutedText("hp-tick", p.l-tickGap, t.v, svg.A("dy", "0.32em"), svg.A("text-anchor", "end"))...)
		}
	}
	w.Close("g")
}

func (p *plan) writeYLabel(w *svg.Writer) {
	if p.yLabel == "" {
		return
	}
	w.Elem("text", p.yLabel, mutedText("hp-axis-label hp-ylabel", p.cl, p.yLabelY, svg.A("dy", "0.32em"))...)
}

// visibleRules returns the rules inside the Y domain, with their y.
func (p *plan) visibleRules() ([]RuleY, []float64) {
	lo, hi := math.Min(p.y.Domain[0], p.y.Domain[1]), math.Max(p.y.Domain[0], p.y.Domain[1])
	var rules []RuleY
	var ys []float64
	for _, r := range p.rules {
		if valid(r.Y) && r.Y >= lo && r.Y <= hi {
			rules = append(rules, r)
			ys = append(ys, crisp(p.y.Map(r.Y)))
		}
	}
	return rules, ys
}

func (p *plan) writeRules(w *svg.Writer) {
	rules, ys := p.visibleRules()
	if len(rules) == 0 {
		return
	}
	w.Open("g", svg.A("class", "hp-rules"), svg.A("aria-hidden", "true"))
	for i, r := range rules {
		y := ys[i]
		w.Void("line", svg.A("class", "hp-rule"), svg.N("x1", p.l), svg.N("x2", p.r), svg.N("y1", y), svg.N("y2", y),
			svg.A("stroke", "currentColor"), svg.A("stroke-opacity", "0.5"))
		if r.Label == "" {
			continue
		}
		text := textwidth.Truncate(r.Label, p.f, math.Max(40, (p.r-p.l)/2))
		ly := y - 5 // above the line, at its right end
		if ly-p.f < p.ct {
			ly = y + p.f + 3 // no room above: hang below
		}
		w.Elem("text", text, mutedText("hp-rule-label", p.r, ly, svg.A("text-anchor", "end"))...)
	}
	w.Close("g")
}
