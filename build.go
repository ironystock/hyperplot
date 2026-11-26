package hyperplot

import (
	"errors"
	"fmt"
	"maps"
	"math"
	"strconv"
	"strings"
	"time"

	"github.com/ironystock/hyperplot/scale"
)

const (
	defaultWidth    = 640
	defaultHeight   = 320
	minSize         = 50
	maxSize         = 32768
	defaultFontSize = 12
	minFontSize     = 4
	maxFontSize     = 96
	defaultBarWidth = 24
	maxSeries       = 8
	maxTickCount    = 200
)

// plan holds everything computed from a Chart before any markup is
// written. Building it validates the chart; writing it cannot fail.
type plan struct {
	c    *Chart
	id   string
	w, h float64
	f    float64
	barW float64
	pad  float64

	series []*series
	rules  []RuleY
	xk     xKind

	hasLine, hasArea, hasBar, hasDot bool

	// Categories of a band x axis, in order of first appearance.
	cats   []string
	catIdx map[string]int
	catKey []string

	// Raw data extents.
	xMin, xMax float64   // linear x
	tMin, tMax time.Time // time x
	hasX       bool
	yLo, yHi   float64

	// Units as appended to each value, shortened (see maxUnit).
	xUnit, yUnit string
	// xNum formats linear x values when X.Format is nil; see
	// distinctFormat. nil means formatValue.
	xNum func(float64) string

	// Scales and ticks.
	x      xScale
	y      scale.Linear
	yTicks []tickLabel
	xTicks []tickLabel

	// Layout: content box (inside the standalone margin) and plot box.
	cl, cr, ct, cb float64
	l, r, t, b     float64

	title      string
	titleSize  float64
	titleY     float64
	legend     []legendItem
	showLegend bool
	yLabel     string
	yLabelY    float64
	xLabel     string
	xLabelY    float64
	xTickY     float64
	direct     []directLabel
}

// xScale is the chart's x scale, of whichever kind the data needs.
type xScale struct {
	kind  xKind
	lin   scale.Linear
	tim   scale.Time
	band  scale.Band
	count int
	// timeFmt formats a time in full, for tooltips and tables.
	timeFmt func(time.Time) string
}

func newPlan(c *Chart) (*plan, error) {
	p := &plan{c: c, xUnit: shorten(c.X.Unit, maxUnit), yUnit: shorten(c.Y.Unit, maxUnit)}
	var err error
	if p.w, err = dimension("Width", c.Width, defaultWidth); err != nil {
		return nil, err
	}
	if p.h, err = dimension("Height", c.Height, defaultHeight); err != nil {
		return nil, err
	}
	switch f := c.FontSize; {
	case f == 0:
		p.f = defaultFontSize
	case f >= minFontSize && f <= maxFontSize:
		p.f = f
	default:
		return nil, fmt.Errorf("hyperplot: FontSize %v is out of range [%d, %d]", f, minFontSize, maxFontSize)
	}
	switch bw := c.BarWidth; {
	case bw == 0:
		p.barW = defaultBarWidth
	case bw > 0 && bw <= maxSize:
		p.barW = bw
	default:
		return nil, fmt.Errorf("hyperplot: BarWidth %v is out of range (0, %d]", bw, maxSize)
	}
	if p.id, err = chartID(c); err != nil {
		return nil, err
	}
	if err := checkDomain("X", c.X); err != nil {
		return nil, err
	}
	if err := checkDomain("Y", c.Y); err != nil {
		return nil, err
	}
	if err := p.buildSeries(); err != nil {
		return nil, err
	}
	if p.id == "" {
		p.id = "hp-" + hash8(p.defaultIDKey())
	}
	p.computeEdges()
	p.extents()
	p.layout()
	return p, nil
}

func dimension(name string, v, def float64) (float64, error) {
	if v == 0 {
		return def, nil
	}
	if !(v >= minSize && v <= maxSize) { // also rejects NaN
		return 0, fmt.Errorf("hyperplot: %s %v is out of range [%d, %d]", name, v, minSize, maxSize)
	}
	return v, nil
}

// chartID validates and returns ID, or "" when it is empty: the default
// id needs the series (see defaultIDKey).
func chartID(c *Chart) (string, error) {
	if c.ID == "" {
		return "", nil
	}
	for _, r := range c.ID {
		ok := r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' ||
			r == '-' || r == '_' || r == ':' || r == '.'
		if !ok {
			return "", fmt.Errorf("hyperplot: ID %q may contain only ASCII letters, digits, '-', '_', ':' and '.'", c.ID)
		}
	}
	return c.ID, nil
}

// defaultIDKey returns the text the default chart id is hashed from: the
// title, description, axis labels and the kind and name of every series.
// These identify a chart on a page but stay the same as its data, size
// or styling change, so live updates of a chart keep its id. Two charts
// that agree on all of them (untitled charts of unnamed series, or one
// chart rendered for two filters) still share an id, which is why
// Chart.ID tells callers to set an ID for those.
func (p *plan) defaultIDKey() string {
	c := p.c
	var b strings.Builder
	for _, s := range []string{c.Title, c.Description, c.X.Label, c.Y.Label} {
		b.WriteString(s)
		b.WriteByte(0)
	}
	for _, s := range p.series {
		b.WriteString(s.kind.String())
		b.WriteByte(':')
		b.WriteString(s.name)
		b.WriteByte(0)
	}
	return b.String()
}

func checkDomain(name string, a Axis) error {
	d := a.Domain
	if d[0] != d[1] && !(valid(d[0]) && valid(d[1])) {
		return fmt.Errorf("hyperplot: %s.Domain %v must be finite", name, d)
	}
	return nil
}

// hash8 returns the 8-hex-digit FNV-1a hash of s, the stable key used in
// element ids.
func hash8(s string) string {
	v := uint32(2166136261) // FNV-1a offset basis
	for i := range len(s) {
		v ^= uint32(s[i])
		v *= 16777619 // FNV prime
	}
	var b [8]byte
	const hex = "0123456789abcdef"
	for i := 7; i >= 0; i-- {
		b[i] = hex[v&0xf]
		v >>= 4
	}
	return string(b[:])
}

// uniqueKey returns hash8(s), rehashing with a counter on collision with
// a key in seen, and records the result in seen. Ids must be unique even
// when names repeat.
func uniqueKey(s string, seen map[string]bool) string {
	k := hash8(s)
	for n := 1; seen[k]; n++ {
		k = hash8(s + "#" + strconv.Itoa(n))
	}
	seen[k] = true
	return k
}

func markDesc(i int, kind string, name string) string {
	if name == "" {
		return fmt.Sprintf("mark %d (%s)", i, kind)
	}
	return fmt.Sprintf("mark %d (%s %q)", i, kind, name)
}

func (p *plan) buildSeries() error {
	var firstX int // mark index that set p.xk
	for i, m := range p.c.Marks {
		var s *series
		switch m := m.(type) {
		case Line:
			s = &series{kind: kindLine, name: m.Name, x: m.X, y: m.Y, curve: m.Curve, slot: m.Slot, points: m.Points}
		case *Line:
			if m != nil {
				s = &series{kind: kindLine, name: m.Name, x: m.X, y: m.Y, curve: m.Curve, slot: m.Slot, points: m.Points}
			}
		case Area:
			s = &series{kind: kindArea, name: m.Name, x: m.X, y: m.Y, curve: m.Curve, slot: m.Slot, base: m.Base}
		case *Area:
			if m != nil {
				s = &series{kind: kindArea, name: m.Name, x: m.X, y: m.Y, curve: m.Curve, slot: m.Slot, base: m.Base}
			}
		case Bar:
			s = &series{kind: kindBar, name: m.Name, x: m.X, y: m.Y, slot: m.Slot}
		case *Bar:
			if m != nil {
				s = &series{kind: kindBar, name: m.Name, x: m.X, y: m.Y, slot: m.Slot}
			}
		case Dot:
			s = &series{kind: kindDot, name: m.Name, x: m.X, y: m.Y, slot: m.Slot, labels: m.Labels}
		case *Dot:
			if m != nil {
				s = &series{kind: kindDot, name: m.Name, x: m.X, y: m.Y, slot: m.Slot, labels: m.Labels}
			}
		case RuleY:
			p.rules = append(p.rules, m)
			continue
		case *RuleY:
			if m != nil {
				p.rules = append(p.rules, *m)
				continue
			}
		}
		if s == nil {
			return fmt.Errorf("hyperplot: mark %d is nil", i)
		}
		s.mark, s.index = i, len(p.series)
		if err := p.checkSeries(s); err != nil {
			return err
		}
		if s.x != nil && s.x.Len() > 0 {
			if p.xk == xNone {
				p.xk, firstX = s.x.kind(), i
			} else if k := s.x.kind(); k != p.xk {
				first := p.c.Marks[firstX]
				return fmt.Errorf("hyperplot: %s has %s x values but %s has %s; all marks in a chart must use one kind",
					markDesc(i, s.kind.String(), s.name), k, markDesc(firstX, kindOf(first), nameOf(first)), p.xk)
			}
		}
		p.series = append(p.series, s)
		switch s.kind {
		case kindLine:
			p.hasLine = true
		case kindArea:
			p.hasArea = true
		case kindBar:
			p.hasBar = true
		case kindDot:
			p.hasDot = true
		}
	}
	if n := len(p.series); n > maxSeries {
		return fmt.Errorf("%w (the chart has %d)", ErrTooManySeries, n)
	}
	if err := p.assignSlots(); err != nil {
		return err
	}
	seen := make(map[string]bool, len(p.series))
	for _, s := range p.series {
		if s.name == "" {
			s.key = uniqueKey("#"+strconv.Itoa(s.index), seen)
			s.label = "Series " + strconv.Itoa(s.index+1)
		} else {
			s.key = uniqueKey(s.name, seen)
			s.label = s.name
		}
	}
	if p.xk == xBand {
		p.catIdx = make(map[string]int)
		seenCat := make(map[string]bool)
		for _, s := range p.series {
			cats, _ := s.x.(Categories)
			for _, c := range cats {
				if _, ok := p.catIdx[c]; !ok {
					p.catIdx[c] = len(p.cats)
					p.cats = append(p.cats, c)
					p.catKey = append(p.catKey, uniqueKey(c, seenCat))
				}
			}
		}
	}
	return nil
}

func kindOf(m Mark) string {
	switch m.(type) {
	case Line, *Line:
		return "Line"
	case Area, *Area:
		return "Area"
	case Bar, *Bar:
		return "Bar"
	case Dot, *Dot:
		return "Dot"
	}
	return "RuleY"
}

func nameOf(m Mark) string {
	switch m := m.(type) {
	case Line:
		return m.Name
	case *Line:
		return m.Name
	case Area:
		return m.Name
	case *Area:
		return m.Name
	case Bar:
		return m.Name
	case *Bar:
		return m.Name
	case Dot:
		return m.Name
	case *Dot:
		return m.Name
	}
	return ""
}

func (p *plan) checkSeries(s *series) error {
	desc := func() string { return markDesc(s.mark, s.kind.String(), s.name) }
	if s.curve < CurveLinear || s.curve > CurveStep {
		return fmt.Errorf("hyperplot: %s: unknown Curve %d", desc(), s.curve)
	}
	if s.slot < 0 || s.slot > maxSeries {
		return fmt.Errorf("hyperplot: %s: Slot %d is outside 1–8 (0 picks one)", desc(), s.slot)
	}
	n := 0
	if s.x != nil {
		n = s.x.Len()
	}
	if s.x == nil && len(s.y) > 0 {
		return fmt.Errorf("hyperplot: %s: X is nil but Y has %d values", desc(), len(s.y))
	}
	if n != len(s.y) {
		return fmt.Errorf("hyperplot: %s: X has %d values but Y has %d", desc(), n, len(s.y))
	}
	if s.base != nil && len(s.base) != len(s.y) {
		return fmt.Errorf("hyperplot: %s: Base has %d values but Y has %d", desc(), len(s.base), len(s.y))
	}
	if s.labels != nil && len(s.labels) != len(s.y) {
		return fmt.Errorf("hyperplot: %s: Labels has %d values but Y has %d", desc(), len(s.labels), len(s.y))
	}
	if s.kind == kindBar {
		cats, _ := s.x.(Categories)
		if len(cats) == 0 {
			return fmt.Errorf("hyperplot: %s: X must list at least one category", desc())
		}
		seen := make(map[string]bool, len(cats))
		for _, c := range cats {
			if seen[c] {
				return fmt.Errorf("hyperplot: %s: category %q appears more than once", desc(), c)
			}
			seen[c] = true
		}
	}
	return nil
}

// assignSlots validates pinned slots and gives every unpinned series the
// lowest free slot, in mark order.
func (p *plan) assignSlots() error {
	var owner [maxSeries + 1]*series
	for _, s := range p.series {
		if s.slot == 0 {
			continue
		}
		if o := owner[s.slot]; o != nil {
			return fmt.Errorf("hyperplot: %s and %s are both pinned to Slot %d",
				markDesc(o.mark, o.kind.String(), o.name), markDesc(s.mark, s.kind.String(), s.name), s.slot)
		}
		owner[s.slot] = s
		s.pinned = true
	}
	next := 1
	for _, s := range p.series {
		if s.slot != 0 {
			continue
		}
		for owner[next] != nil {
			next++
		}
		if next > maxSeries {
			return errors.New("hyperplot: no free color slot") // unreachable: len(series) <= 8
		}
		s.slot = next
		owner[next] = s
	}
	return nil
}

// xkey identifies an x value, for matching points across series.
type xkey struct {
	f float64 // Floats value
	t int64   // Times: Unix seconds
	n int32   // Times: nanoseconds
	s string  // Categories value
}

// keyOf returns the key of the i-th x value and whether it is plottable.
func keyOf(x Values, i int) (xkey, bool) {
	switch x := x.(type) {
	case Floats:
		v := x[i]
		if !valid(v) {
			return xkey{}, false
		}
		if v == 0 {
			v = 0 // fold -0 into 0
		}
		return xkey{f: v}, true
	case Times:
		return xkey{t: x[i].Unix(), n: int32(x[i].Nanosecond())}, true
	case Categories:
		return xkey{s: x[i]}, true
	}
	return xkey{}, false
}

// computeEdges fills each series' lo and hi edges in data units,
// stacking bars and areas when the chart stacks.
func (p *plan) computeEdges() {
	stack := p.c.Stack
	var areaCum map[xkey]float64
	var pos, neg map[string]float64
	if stack {
		areaCum = make(map[xkey]float64)
		pos = make(map[string]float64)
		neg = make(map[string]float64)
	}
	nan := math.NaN()
	for _, s := range p.series {
		n := len(s.y)
		s.lo = make([]float64, n)
		s.hi = make([]float64, n)
		// A stacked area stacks on the earlier series only. Its own
		// totals are merged after its loop, so a repeated x within the
		// series (a vertical step, a duplicate timestamp) does not stack
		// on the series' own earlier point; the last value at an x wins.
		var own map[xkey]float64
		if stack && s.kind == kindArea {
			own = make(map[xkey]float64)
		}
		for i, y := range s.y {
			k, ok := keyOf(s.x, i)
			lo, hi := nan, nan
			if ok && valid(y) {
				switch s.kind {
				case kindLine, kindDot:
					hi = y
				case kindArea:
					switch {
					case stack:
						lo = areaCum[k]
						hi = lo + y
						if valid(hi) {
							own[k] = hi
						}
					case s.base != nil:
						if valid(s.base[i]) {
							lo, hi = s.base[i], y
						}
					default:
						lo, hi = 0, y
					}
				case kindBar:
					switch {
					case stack && y >= 0:
						lo = pos[k.s]
						hi = lo + y
						if valid(hi) {
							pos[k.s] = hi
						}
					case stack:
						lo = neg[k.s]
						hi = lo + y
						if valid(hi) {
							neg[k.s] = hi
						}
					default:
						lo, hi = 0, y
					}
				}
			}
			if !valid(hi) || (s.kind == kindArea || s.kind == kindBar) && !valid(lo) {
				lo, hi = nan, nan
			}
			s.lo[i], s.hi[i] = lo, hi
		}
		maps.Copy(areaCum, own) // order-independent: the keys are distinct
	}
	// Round the data end of every bar, except inner stacked segments: only
	// the outermost segment in each direction keeps its rounded end.
	donePos := make(map[string]bool)
	doneNeg := make(map[string]bool)
	for si := len(p.series) - 1; si >= 0; si-- {
		s := p.series[si]
		if s.kind != kindBar {
			continue
		}
		cats := s.x.(Categories)
		s.round = make([]bool, len(s.y))
		for i, c := range cats {
			if math.IsNaN(s.hi[i]) {
				continue
			}
			if !stack {
				s.round[i] = true
				continue
			}
			switch v := s.y[i]; {
			case v > 0 && !donePos[c]:
				s.round[i], donePos[c] = true, true
			case v < 0 && !doneNeg[c]:
				s.round[i], doneNeg[c] = true, true
			}
		}
	}
}

// extents computes the raw x and y data extents.
func (p *plan) extents() {
	p.xMin, p.xMax = math.Inf(1), math.Inf(-1)
	p.yLo, p.yHi = math.Inf(1), math.Inf(-1)
	for _, s := range p.series {
		for i := range s.y {
			switch x := s.x.(type) {
			case Floats:
				if valid(x[i]) {
					p.xMin, p.xMax = math.Min(p.xMin, x[i]), math.Max(p.xMax, x[i])
					p.hasX = true
				}
			case Times:
				t := x[i]
				if !p.hasX || t.Before(p.tMin) {
					p.tMin = t
				}
				if !p.hasX || t.After(p.tMax) {
					p.tMax = t
				}
				p.hasX = true
			}
			if v := s.hi[i]; !math.IsNaN(v) {
				p.yLo, p.yHi = math.Min(p.yLo, v), math.Max(p.yHi, v)
			}
			if v := s.lo[i]; !math.IsNaN(v) {
				p.yLo, p.yHi = math.Min(p.yLo, v), math.Max(p.yHi, v)
			}
		}
	}
	for _, r := range p.rules {
		if valid(r.Y) {
			p.yLo, p.yHi = math.Min(p.yLo, r.Y), math.Max(p.yHi, r.Y)
		}
	}
}

// yDomain returns the Y domain before Nice, and whether it is fixed.
func (p *plan) yDomain() (lo, hi float64, fixed bool) {
	if d := p.c.Y.Domain; d[0] != d[1] {
		return math.Min(d[0], d[1]), math.Max(d[0], d[1]), true
	}
	lo, hi = p.yLo, p.yHi
	if lo > hi { // no data
		lo, hi = 0, 1
	}
	if p.hasBar || p.hasArea || p.c.Y.Zero {
		lo, hi = math.Min(lo, 0), math.Max(hi, 0)
		if lo == 0 && hi == 0 {
			return 0, 1, false // all zeros: keep the baseline at the bottom
		}
	}
	lo, hi = padDegenerate(lo, hi)
	return lo, hi, false
}

// xDomain returns the linear X domain before Nice, and whether it is fixed.
func (p *plan) xDomain() (lo, hi float64, fixed bool) {
	if d := p.c.X.Domain; d[0] != d[1] {
		return math.Min(d[0], d[1]), math.Max(d[0], d[1]), true
	}
	lo, hi = p.xMin, p.xMax
	if lo > hi {
		lo, hi = 0, 1
	}
	if p.c.X.Zero {
		lo, hi = math.Min(lo, 0), math.Max(hi, 0)
	}
	lo, hi = padDegenerate(lo, hi)
	return lo, hi, false
}

// padDegenerate widens an empty interval around its single value, so
// that a flat series is drawn mid-plot rather than on an edge.
// Spans narrower than 1e-12 of the values' magnitude (or than 1e-290
// absolutely) count as flat: they are float noise, and tick arithmetic on
// them underflows.
func padDegenerate(lo, hi float64) (float64, float64) {
	if span := hi - lo; span > 1e-290 && span > 1e-12*math.Max(math.Abs(lo), math.Abs(hi)) {
		return lo, hi
	}
	if pad := math.Abs(lo) * 0.1; pad > 0 && lo-pad != lo {
		return lo - pad, hi + pad
	}
	return lo - 1, hi + 1
}

// timeDomain returns the time X domain.
func (p *plan) timeDomain() (time.Time, time.Time) {
	if d := p.c.X.TimeDomain; !d[0].IsZero() && !d[1].IsZero() {
		if d[1].Before(d[0]) {
			return d[1], d[0]
		}
		return d[0], d[1]
	}
	if !p.hasX {
		t := time.Unix(0, 0)
		return t, t.Add(time.Hour)
	}
	return p.tMin, p.tMax
}
