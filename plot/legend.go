package plot

import (
	"strconv"

	"github.com/ironystock/hyperplot/internal/svg"
	"github.com/ironystock/hyperplot/internal/textwidth"
)

// legendItem is one positioned legend entry.
type legendItem struct {
	s    *series
	text string // possibly truncated name
	x, y float64
}

// keyWidth is the width of the legend key for a series kind.
func keyWidth(k seriesKind) float64 {
	switch k {
	case kindLine:
		return 16
	case kindDot:
		return 8
	}
	return 10
}

// layoutLegend positions legend items in rows that wrap to the content
// width, starting at top, with names truncated to maxName px, and returns
// the number of rows.
func (p *plan) layoutLegend(top, maxName float64) int {
	f := p.f
	x, row := p.cl, 0
	p.legend = make([]legendItem, 0, len(p.series))
	for _, s := range p.series {
		text := textwidth.Truncate(s.label, f, maxName)
		w := keyWidth(s.kind) + keyGap + textwidth.Width(text, f)
		if x > p.cl && x+w > p.cr {
			row++
			x = p.cl
		}
		p.legend = append(p.legend, legendItem{s: s, text: text, x: x, y: top + float64(row)*(f+6) + (f+6)/2})
		x += w + itemGap
	}
	return row + 1
}

func (p *plan) writeLegend(w *svg.Writer) {
	if !p.showLegend {
		return
	}
	w.Open("g", svg.A("class", "hp-legend"), svg.A("aria-hidden", "true"))
	for _, it := range p.legend {
		s := it.s
		cls := "hp-key hp-s" + strconv.Itoa(s.slot)
		w.Open("g", svg.A("class", "hp-legend-item"))
		if it.text != s.label {
			w.Elem("title", s.label)
		}
		kw := keyWidth(s.kind)
		switch s.kind {
		case kindLine:
			w.Void("line", svg.A("class", cls), svg.N("x1", it.x+1), svg.N("y1", it.y), svg.N("x2", it.x+kw-1), svg.N("y2", it.y),
				svg.A("stroke", "currentColor"), svg.A("stroke-width", "2"), svg.A("stroke-linecap", "round"))
		case kindDot:
			w.Void("circle", svg.A("class", cls), svg.N("cx", it.x+4), svg.N("cy", it.y), svg.A("r", "4"), svg.A("fill", "currentColor"))
		default:
			w.Void("rect", svg.A("class", cls), svg.N("x", it.x), svg.N("y", it.y-5), svg.A("width", "10"), svg.A("height", "10"),
				svg.A("rx", "2"), svg.A("fill", "currentColor"))
		}
		w.Elem("text", it.text, svg.A("class", "hp-legend-label"), svg.N("x", it.x+kw+keyGap), svg.N("y", it.y), svg.A("dy", "0.32em"),
			svg.A("fill", "currentColor"), svg.A("fill-opacity", "0.7"))
		w.Close("g")
	}
	w.Close("g")
}
