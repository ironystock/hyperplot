package plot

import (
	"math"
	"slices"
	"time"

	"github.com/ironystock/hyperplot/internal/textwidth"
	"github.com/ironystock/hyperplot/scale"
)

const (
	standalonePad = 16  // margin around a standalone chart
	plotPadTop    = 8   // space between the header rows and the plot
	tickGap       = 8   // space between the plot and Y tick labels
	minLeft       = 24  // minimum left margin when Y labels show
	hiddenLeft    = 8   // left margin when Y labels are hidden
	minRight      = 12  // minimum right margin
	minLabelGap   = 6   // minimum space between X tick labels (at least f)
	directOffset  = 8   // direct label distance from the last point
	clipPad       = 6   // the clip rect overhangs the plot so edge dots show
	maxPixel      = 1e6 // pixel coordinates are clamped to ±maxPixel
	keyGap        = 6   // space between a legend key and its name
	itemGap       = 16  // space between legend items
)

// tickLabel is one positioned tick label.
type tickLabel struct {
	v      float64 // position along the axis, in pixels
	text   string
	w      float64 // estimated width
	anchor string  // "start", "middle" or "end" (x labels only)
}

// extent returns the horizontal extent of an x tick label.
func (t tickLabel) extent() (float64, float64) {
	switch t.anchor {
	case "start":
		return t.v, t.v + t.w
	case "end":
		return t.v - t.w, t.v
	}
	return t.v - t.w/2, t.v + t.w/2
}

// directLabel is a series name drawn at the series' last point.
type directLabel struct {
	s    *series
	text string
	i    int // index of the last valid point
}

func clampPx(v float64) float64 { return math.Max(-maxPixel, math.Min(maxPixel, v)) }

// niceSafe returns s.Nice(count), or s itself if rounding the domain
// failed to produce finite bounds.
func niceSafe(s scale.Linear, count int) scale.Linear {
	n := s.Nice(count)
	if !valid(n.Domain[0]) || !valid(n.Domain[1]) || n.Domain[0] == n.Domain[1] {
		return s
	}
	return n
}

func tickCount(n int, length, per float64) int {
	if n <= 0 {
		n = max(2, int(math.Round(length/per)))
	}
	return min(n, maxTickCount)
}

func (p *plan) showTitle() bool { return p.c.Standalone || p.c.ShowTitle }

func (p *plan) layout() {
	c, f := p.c, p.f
	if c.Standalone {
		p.pad = standalonePad
	}
	p.cl, p.cr, p.ct, p.cb = p.pad, p.w-p.pad, p.pad, p.h-p.pad
	cw := p.cr - p.cl

	// Bottom: X tick labels, X label.
	bottom := p.cb
	if c.X.Label != "" {
		bottom -= f + 8
	}
	if !c.X.Hide {
		bottom -= f + 10
	} else {
		bottom -= 4
	}

	// Top: title row, legend rows, Y label row, padding.
	p.t = p.layoutHeader((bottom-p.ct)/2-plotPadTop) + plotPadTop
	p.b = math.Max(bottom, p.t+1)
	p.xTickY = p.b + f + 4
	if c.X.Label != "" {
		p.xLabelY = p.b + f + 2
		if !c.X.Hide {
			p.xLabelY += f + 10
		}
	}

	// Y scale, which fixes the left margin.
	count := tickCount(c.Y.Ticks, p.b-p.t, 50)
	lo, hi, fixed := p.yDomain()
	p.y = scale.NewLinear(lo, hi, p.b, p.t)
	if !fixed {
		p.y = niceSafe(p.y, count)
	}
	ticks := p.y.Ticks(count)
	format := c.Y.Format
	if format == nil {
		format = scale.TickFormat(ticks)
	}
	left := float64(hiddenLeft)
	if !c.Y.Hide {
		var widest float64
		p.yTicks = make([]tickLabel, 0, len(ticks))
		for _, t := range ticks {
			s := format(t)
			w := textwidth.Width(s, f)
			widest = math.Max(widest, w)
			p.yTicks = append(p.yTicks, tickLabel{v: p.y.Map(t), text: s, w: w})
		}
		left = math.Min(math.Max(widest+tickGap, minLeft), cw/2)
	} else {
		for _, t := range ticks {
			p.yTicks = append(p.yTicks, tickLabel{v: p.y.Map(t)})
		}
	}
	p.l = p.cl + left

	// Direct labels fix the right margin; they depend only on Y.
	right := float64(minRight)
	if w := p.planDirectLabels(); w > 0 {
		right = math.Max(right, math.Min(w+14, cw/3))
	}

	// X scale. A first pass measures how far the last tick label spills
	// past the plot; the second reserves room for it.
	for pass := range 2 {
		p.r = math.Max(p.cr-right, p.l+1)
		p.setXScale()
		p.xTicks = p.xTickLabels()
		if pass > 0 || p.xk == xBand || len(p.xTicks) == 0 {
			break
		}
		last := p.xTicks[len(p.xTicks)-1]
		need := math.Min(last.v+last.w/2-p.r, cw/4)
		if need <= right {
			break
		}
		right = need
	}
	switch {
	case p.xk == xTime && c.X.TimeValueFormat != nil:
		p.x.timeFmt = c.X.TimeValueFormat
	case p.xk == xTime:
		// Full labels for tooltips and tables: at the ticks' precision,
		// or finer where the data needs it (30-second buckets under
		// minute ticks), so that no two rows read alike.
		p.x.timeFmt = p.x.tim.FormatterFor(p.x.count, p.dataTimes)
	case p.xk == xLinear && c.X.Format == nil:
		// Likewise for numbers: x values head tooltip and table rows.
		p.xNum = distinctFormat(p.distinctXs())
	}
	p.xTicks = thin(p.anchorTicks(p.xTicks), math.Max(minLabelGap, f))
	if c.X.Hide {
		p.xTicks = nil
	}
	if c.X.Label != "" {
		p.xLabel = textwidth.Truncate(c.X.Label, f, p.r-p.cl)
	}
	p.positions()
}

// layoutHeader lays out the rows above the plot (the visible title, the
// legend and the Y label) and returns their bottom edge. The rows may
// take at most budget px, so that a short or narrow chart with many
// series still has a plot (half of the height left after the X axis
// rows). Rows that do not fit are dropped whole, in this order: the
// legend (after trying shorter names), the Y label, the title. The
// tooltip and the data table still name every series.
func (p *plan) layoutHeader(budget float64) float64 {
	c, f := p.c, p.f
	cw := p.cr - p.cl
	rowH := f + 6
	var titleH, yLabelH float64
	if p.showTitle() && c.Title != "" {
		titleH = math.Round(f*1.25) + 10
	}
	if c.Y.Label != "" {
		yLabelH = rowH
	}
	switch c.Legend {
	case LegendShow:
		p.showLegend = len(p.series) > 0
	case LegendAuto:
		p.showLegend = len(p.series) >= 2
	}
	maxName := math.Max(40, math.Min(200, cw-24))
	if p.showLegend {
		fits := false
		for _, mn := range []float64{maxName, math.Min(maxName, 60)} {
			if titleH+yLabelH+float64(p.layoutLegend(0, mn))*rowH <= budget {
				fits, maxName = true, mn
				break
			}
		}
		p.showLegend = fits
		p.legend = nil
	}
	if titleH+yLabelH > budget {
		yLabelH = 0
	}
	if titleH > budget {
		titleH = 0
	}

	top := p.ct
	if titleH > 0 {
		p.titleSize = math.Round(f * 1.25)
		p.title = textwidth.Truncate(c.Title, p.titleSize, cw)
		p.titleY = top + p.titleSize
		top += titleH
	}
	if p.showLegend {
		top += float64(p.layoutLegend(top, maxName)) * rowH
	}
	if yLabelH > 0 {
		p.yLabel = textwidth.Truncate(c.Y.Label, f, cw)
		p.yLabelY = top + rowH/2
		top += rowH
	}
	return top
}

// dataTimes yields every time x value of every series.
func (p *plan) dataTimes(yield func(time.Time) bool) {
	for _, s := range p.series {
		ts, _ := s.x.(Times)
		for _, t := range ts {
			if !yield(t) {
				return
			}
		}
	}
}

// distinctXs returns every valid numeric x value of every series, sorted
// and without duplicates.
func (p *plan) distinctXs() []float64 {
	var xs []float64
	for _, s := range p.series {
		fs, _ := s.x.(Floats)
		for _, v := range fs {
			if v == 0 {
				v = 0 // fold −0 into 0
			}
			if valid(v) {
				xs = append(xs, v)
			}
		}
	}
	slices.Sort(xs)
	return slices.Compact(xs)
}

// setXScale builds the x scale for the plot's current horizontal extent.
func (p *plan) setXScale() {
	c := p.c
	count := tickCount(c.X.Ticks, p.r-p.l, 90)
	p.x = xScale{kind: p.xk, count: count}
	switch p.xk {
	case xTime:
		t0, t1 := p.timeDomain()
		p.x.tim = scale.Time{Domain: [2]time.Time{t0, t1}, Range: [2]float64{p.l, p.r}, Location: c.Location}
		p.x.ms = timeWire(t0, t1)
	case xBand:
		p.x.band = scale.NewBand(p.cats, p.l, p.r)
	default:
		lo, hi, fixed := p.xDomain()
		p.x.lin = scale.NewLinear(lo, hi, p.l, p.r)
		if !fixed {
			p.x.lin = niceSafe(p.x.lin, count)
		}
	}
}

// xTickLabels returns the x tick labels, centered on their ticks.
func (p *plan) xTickLabels() []tickLabel {
	c, f := p.c, p.f
	var out []tickLabel
	add := func(x float64, s string) {
		out = append(out, tickLabel{v: x, text: s, w: textwidth.Width(s, f), anchor: "middle"})
	}
	switch p.xk {
	case xTime:
		format := c.X.TimeFormat
		if format == nil {
			format = p.x.tim.TickFormatFor(p.x.count)
		}
		for _, t := range p.x.tim.Ticks(p.x.count) {
			add(p.timeX(t), format(t))
		}
	case xBand:
		half := p.x.band.Bandwidth() / 2
		labels := func(maxW float64) []tickLabel {
			out = out[:0:0]
			for i, cat := range p.cats {
				add(p.x.band.MapIndex(i)+half, textwidth.Truncate(cat, f, maxW))
			}
			return out
		}
		// Labels up to 160 px, thinned if they collide. But when
		// truncating each label to its band step (less the label gap)
		// lets every band keep a label, do that instead: one long
		// category then cannot hide its neighbors. Truncating and still
		// thinning would be the worst of both ("10:3…" on every other
		// band), so that never happens.
		gap := math.Max(minLabelGap, f)
		full := labels(160)
		if len(thin(p.anchorTicks(slices.Clone(full)), gap)) == len(full) {
			return full
		}
		if maxW := p.x.band.Step() - gap; maxW >= 40 {
			short := labels(maxW)
			if len(thin(p.anchorTicks(slices.Clone(short)), gap)) == len(short) {
				return short
			}
		}
		return full
	case xLinear:
		ticks := p.x.lin.Ticks(p.x.count)
		format := c.X.Format
		if format == nil {
			format = scale.TickFormat(ticks)
		}
		for _, t := range ticks {
			add(p.x.lin.Map(t), format(t))
		}
	}
	return out
}

// anchorTicks anchors labels that would overflow the content box at
// their outer edge instead of their center.
func (p *plan) anchorTicks(ts []tickLabel) []tickLabel {
	for i := range ts {
		t := &ts[i]
		switch {
		case t.v-t.w/2 < p.cl:
			t.anchor = "start"
		case t.v+t.w/2 > p.cr:
			t.anchor = "end"
		}
	}
	return ts
}

// thin keeps every k-th label, for the smallest k at which every two kept
// labels are at least gap apart.
func thin(ts []tickLabel, gap float64) []tickLabel {
	n := len(ts)
	for k := 1; k < n; k++ {
		ok := true
		for i := k; i < n && ok; i += k {
			_, prevEnd := ts[i-k].extent()
			start, _ := ts[i].extent()
			ok = start-prevEnd >= gap
		}
		if ok {
			if k == 1 {
				return ts
			}
			out := make([]tickLabel, 0, n/k+1)
			for i := 0; i < n; i += k {
				out = append(out, ts[i])
			}
			return out
		}
	}
	if n > 0 {
		return ts[:1]
	}
	return ts
}

// planDirectLabels decides whether to label series at their last points
// and returns the width of the widest label, or 0 for none. Labels are
// drawn for 2–4 line and area series, all or nothing: if any two would
// sit within f+2 px of each other vertically, none are drawn.
func (p *plan) planDirectLabels() float64 {
	n := len(p.series)
	if n < 2 || n > 4 {
		return 0
	}
	maxW := math.Max(40, (p.cr-p.cl)*0.25)
	ys := make([]float64, 0, n)
	labels := make([]directLabel, 0, n)
	var widest float64
	for _, s := range p.series {
		if s.kind != kindLine && s.kind != kindArea {
			return 0
		}
		last := lastPlotted(s)
		if last < 0 {
			return 0
		}
		y := p.y.Map(s.hi[last])
		if y < p.t-1 || y > p.b+1 || !p.xInDomain(s, last) {
			return 0 // outside a fixed domain
		}
		ys = append(ys, y)
		text := textwidth.Truncate(s.label, p.f, maxW)
		widest = math.Max(widest, textwidth.Width(text, p.f))
		labels = append(labels, directLabel{s: s, text: text, i: last})
	}
	slices.Sort(ys)
	for i := 1; i < len(ys); i++ {
		if ys[i]-ys[i-1] < p.f+2 {
			return 0
		}
	}
	p.direct = labels
	return widest
}

// lastPlotted returns the index of the last point of s that has both a
// valid x and a valid y, or -1.
func lastPlotted(s *series) int {
	for i := len(s.hi) - 1; i >= 0; i-- {
		if math.IsNaN(s.hi[i]) {
			continue
		}
		if x, ok := s.x.(Floats); ok && !valid(x[i]) {
			continue
		}
		return i
	}
	return -1
}

// xInDomain reports whether the i-th x value of s lies inside the X
// domain. It needs no x scale, so the right margin can be chosen before
// the scale is built: an automatic domain holds every x by construction
// (rounding it outward only widens it), so only a fixed Domain or
// TimeDomain can leave a point outside.
func (p *plan) xInDomain(s *series, i int) bool {
	switch x := s.x.(type) {
	case Floats:
		lo, hi, _ := p.xDomain()
		return x[i] >= lo && x[i] <= hi
	case Times:
		t0, t1 := p.timeDomain()
		return !x[i].Before(t0) && !x[i].After(t1)
	}
	return true
}

func lastValid(v []float64) int {
	for i := len(v) - 1; i >= 0; i-- {
		if !math.IsNaN(v[i]) {
			return i
		}
	}
	return -1
}

// xPixel maps the i-th x value of s to a pixel, or NaN if it is missing.
func (p *plan) xPixel(s *series, i int) float64 {
	switch x := s.x.(type) {
	case Floats:
		if !valid(x[i]) {
			return math.NaN()
		}
		return clampPx(p.x.lin.Map(x[i]))
	case Times:
		return clampPx(p.timeX(x[i]))
	case Categories:
		return p.bandCenter(p.catIdx[x[i]])
	}
	return math.NaN()
}

func (p *plan) bandCenter(i int) float64 {
	return p.x.band.MapIndex(i) + p.x.band.Bandwidth()/2
}

// yPixel maps a y value to a pixel, keeping NaN.
func (p *plan) yPixel(v float64) float64 {
	if math.IsNaN(v) {
		return v
	}
	return clampPx(p.y.Map(v))
}

// positions computes every point's pixel position.
func (p *plan) positions() {
	for _, s := range p.series {
		n := len(s.y)
		s.px = make([]float64, n)
		s.pyLo = make([]float64, n)
		s.pyHi = make([]float64, n)
		for i := range n {
			s.px[i] = p.xPixel(s, i)
			s.pyHi[i] = p.yPixel(s.hi[i])
			s.pyLo[i] = p.yPixel(s.lo[i])
			if math.IsNaN(s.px[i]) {
				s.pyHi[i], s.pyLo[i] = math.NaN(), math.NaN()
			}
		}
	}
}
