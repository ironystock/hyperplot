package hyperplot

import (
	"math"
	"strconv"

	"github.com/ironystock/hyperplot/internal/svg"
)

const (
	barRadius = 4 // corner radius of a bar's data end
	barGap    = 2 // gap between grouped bars and between stacked segments
	dotRadius = 4
)

func (s *series) class() string { return "hp-series hp-s" + strconv.Itoa(s.slot) }

func (p *plan) writeMarks(w *svg.Writer) {
	w.Open("g", svg.A("class", "hp-marks"), svg.A("clip-path", "url(#"+p.id+"-clip)"))
	barIndex, bars := 0, 0
	for _, s := range p.series {
		if s.kind == kindBar {
			bars++
		}
	}
	for _, s := range p.series {
		w.Open("g", svg.A("id", p.id+"-s-"+s.key), svg.A("class", s.class()), svg.A("data-hp-series", s.label))
		switch s.kind {
		case kindLine:
			p.writeLine(w, s)
			if s.points {
				p.writeDots(w, s)
			}
		case kindArea:
			w.Void("path", svg.A("class", "hp-area"), svg.A("d", areaPath(s.px, s.pyLo, s.pyHi, s.curve)),
				svg.A("fill", "currentColor"), svg.A("fill-opacity", "0.1"))
			p.writeLine(w, s)
		case kindBar:
			p.writeBars(w, s, barIndex, bars)
			barIndex++
		case kindDot:
			p.writeDots(w, s)
		}
		w.Close("g")
	}
	w.Close("g")
}

func (p *plan) writeLine(w *svg.Writer, s *series) {
	w.Void("path", svg.A("class", "hp-line"), svg.A("d", linePath(s.px, s.pyHi, s.curve)),
		svg.A("fill", "none"), svg.A("stroke", "currentColor"), svg.A("stroke-width", "2"),
		svg.A("stroke-linejoin", "round"), svg.A("stroke-linecap", "round"))
}

// pointTitle is the native-tooltip text of a point: "{series} · {x}: {y}",
// or "{series} · {label} · {x}: {y}" for a labeled Dot, with a long
// series name shortened as in tooltips (see tipName).
func (p *plan) pointTitle(s *series, i int) string {
	x := p.xValue(s, i)
	if i < len(s.labels) && s.labels[i] != "" {
		x = s.labels[i] + " · " + x
	}
	return tipName(s.label) + " · " + x + ": " + p.yValue(s.y[i])
}

func dotAttrs(class string, x, y float64) []svg.Attr {
	return []svg.Attr{
		svg.A("class", class), svg.N("cx", x), svg.N("cy", y), svg.A("r", strconv.Itoa(dotRadius)),
		svg.A("fill", "currentColor"), svg.A("stroke", "#fff"), svg.A("stroke-width", "2"),
	}
}

func (p *plan) writeDots(w *svg.Writer, s *series) {
	for i := range s.y {
		if math.IsNaN(s.pyHi[i]) {
			continue
		}
		w.Open("circle", dotAttrs("hp-dot", s.px[i], s.pyHi[i])...)
		w.Elem("title", p.pointTitle(s, i))
		w.Close("circle")
	}
}

// barGeometry returns the left edge and thickness of the bars of the
// series at position gi among n bar series, relative to a band's start.
func (p *plan) barGeometry(gi, n int) (offset, thick float64) {
	bw := p.x.band.Bandwidth()
	if p.c.Stack || n <= 1 {
		thick = math.Min(p.barW, bw)
		return (bw - thick) / 2, thick
	}
	gap := float64(barGap)
	thick = (bw - gap*float64(n-1)) / float64(n)
	if thick < 1 {
		gap, thick = 0, bw/float64(n)
	}
	thick = math.Min(thick, p.barW)
	group := thick*float64(n) + gap*float64(n-1)
	return (bw-group)/2 + float64(gi)*(thick+gap), thick
}

func (p *plan) writeBars(w *svg.Writer, s *series, gi, n int) {
	offset, thick := p.barGeometry(gi, n)
	cats := s.x.(Categories)
	for i, cat := range cats {
		if math.IsNaN(s.pyHi[i]) {
			continue
		}
		ci := p.catIdx[cat]
		x := p.x.band.MapIndex(ci) + offset
		base, end := s.pyLo[i], s.pyHi[i]
		if p.c.Stack && s.lo[i] != 0 {
			// Leave a surface-colored gap above the segment below.
			if end <= base {
				base = math.Max(base-barGap, end)
			} else {
				base = math.Min(base+barGap, end)
			}
		}
		r := 0.0
		if s.round[i] {
			r = barRadius
		}
		w.Open("path", svg.A("id", p.id+"-b-"+s.key+"-"+p.catKey[ci]), svg.A("class", "hp-bar"),
			svg.A("d", barPath(x, thick, base, end, r)), svg.A("fill", "currentColor"))
		w.Elem("title", p.pointTitle(s, i))
		w.Close("path")
	}
}

// barPath returns a bar from the baseline y0 to the data end y1, with
// square corners at the baseline and corners of radius r at the data end.
// The command structure is the same for every bar (a zero radius draws
// square corners), so CSS can interpolate "d" between updates.
func barPath(x, width, y0, y1, r float64) string {
	up := y1 <= y0
	r = math.Max(0, math.Min(r, math.Min(width/2, math.Abs(y1-y0))))
	var p svg.Path
	p.M(x, y0)
	if up {
		p.V(y1+r).A(r, r, 0, false, true, x+r, y1).H(x+width-r).A(r, r, 0, false, true, x+width, y1+r)
	} else {
		p.V(y1-r).A(r, r, 0, false, false, x+r, y1).H(x+width-r).A(r, r, 0, false, false, x+width, y1-r)
	}
	p.V(y0).Z()
	return p.String()
}
