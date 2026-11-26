package plot

import (
	"github.com/ironystock/hyperplot/internal/svg"
)

// fontFamily is the system UI font stack. Charts never use display or
// serif faces.
const fontFamily = "system-ui, -apple-system, 'Segoe UI', sans-serif"

// specVersion is the wire spec version (spec/spec.md §2.3) written to the
// root's data-hp attribute.
const specVersion = "1"

// render writes the chart. The element order is the DOM contract in
// docs/DESIGN.md; the client script and the stylesheet rely on it.
func (p *plan) render() []byte {
	var w svg.Writer
	c := p.c
	points := 0
	for _, s := range p.series {
		points += len(s.y)
	}
	w.Grow(4096 + 160*points) // path data plus tooltip row per point
	if c.Standalone {
		w.Grow(8192)
	}
	class := "hp"
	if c.Animate {
		class += " hp-anim"
	}
	root := []svg.Attr{
		svg.A("xmlns", "http://www.w3.org/2000/svg"),
		svg.A("id", p.id),
		svg.A("class", class),
		svg.A("viewBox", "0 0 "+svg.Num(p.w)+" "+svg.Num(p.h)),
		svg.N("width", p.w),
		svg.N("height", p.h),
		svg.A("role", "img"),
		// Named by the title and described by the desc. Naming it by both
		// made browsers read the desc twice: once in the name and again
		// as the description, which a <desc> child supplies anyway.
		svg.A("aria-labelledby", p.id+"-title"),
		svg.A("aria-describedby", p.id+"-desc"),
		svg.A("tabindex", "0"),
		svg.A("data-hp", specVersion),
	}
	root = append(root, p.geometryAttrs()...)
	root = append(root, svg.A("font-family", fontFamily), svg.N("font-size", p.f))
	w.Open("svg", root...)
	w.Elem("title", c.Title, svg.A("id", p.id+"-title"))
	w.Elem("desc", p.description(), svg.A("id", p.id+"-desc"))
	if c.Standalone {
		theme := DefaultTheme()
		if c.Theme != nil {
			theme = *c.Theme
		}
		w.Elem("style", theme.CSS())
		w.Void("rect", svg.A("class", "hp-bg"), svg.N("width", p.w), svg.N("height", p.h), svg.A("fill", "#fff"))
	}
	if p.title != "" {
		w.Elem("text", p.title, svg.A("class", "hp-title"), svg.N("x", p.cl), svg.N("y", p.titleY),
			svg.N("font-size", p.titleSize), svg.A("font-weight", "600"), svg.A("fill", "currentColor"))
	}
	w.Open("defs")
	w.Open("clipPath", svg.A("id", p.id+"-clip"))
	w.Void("rect", svg.N("x", p.l-clipPad), svg.N("y", p.t-clipPad), svg.N("width", p.r-p.l+2*clipPad), svg.N("height", p.b-p.t+2*clipPad))
	w.Close("clipPath")
	w.Close("defs")
	p.writeLegend(&w)
	p.writeYLabel(&w)
	p.writeGrid(&w)
	if p.hasBar {
		w.Void("rect", svg.A("class", "hp-band-hl"), svg.A("visibility", "hidden"),
			svg.N("x", p.l), svg.N("y", p.t), svg.N("width", p.x.band.Step()), svg.N("height", p.b-p.t),
			svg.A("fill", "currentColor"), svg.A("fill-opacity", "0.06"))
	}
	p.writeMarks(&w)
	p.writeXAxis(&w)
	p.writeYAxis(&w)
	p.writeRules(&w)
	p.writeLabels(&w)
	p.writeHover(&w)
	w.Close("svg")
	return w.Bytes()
}
