package hyperplot

import (
	"cmp"
	"math"
	"slices"
	"strings"

	"github.com/ironystock/hyperplot/internal/svg"
)

// The tooltip payload is JSON with the shapes in docs/DESIGN.md. It is
// encoded by hand (see tipsjson.go) rather than with encoding/json, which
// spends most of its time in reflection on these small, fixed shapes;
// tests check the encoding against encoding/json byte for byte.

// tipEntry is one series' [slot, name, value] entry in a tooltip row.
type tipEntry struct {
	slot        int
	name, value string
}

// tipRow is one tooltip: a heading, the key of its x value
// (docs/spec.md §4.3) and one entry per series.
type tipRow struct {
	t, k string
	v    []tipEntry
}

// tipsAxis is the payload for modes "x" and "band".
type tipsAxis struct {
	mode        string
	xs          []float64
	bw          float64
	top, bottom float64
	rows        []tipRow
}

// tipsXY is the payload for mode "xy".
type tipsXY struct {
	pts  [][2]float64
	rows []tipRow
}

// r2 rounds a coordinate to two decimals, like svg.Num.
func r2(v float64) float64 {
	v = math.Round(v*100) / 100
	if v == 0 || math.IsNaN(v) || math.IsInf(v, 0) {
		return 0
	}
	return v
}

// tipMode returns the hover mode: "band" for charts with bars, "xy" for
// charts with only dots, and "x" otherwise.
func (p *plan) tipMode() string {
	if p.hasBar {
		return "band"
	}
	if len(p.series) > 0 && !p.hasLine && !p.hasArea {
		return "xy"
	}
	return "x"
}

func (p *plan) writeHover(w *svg.Writer) {
	w.Open("g", svg.A("class", "hp-hover"), svg.A("aria-hidden", "true"))
	w.Void("line", svg.A("class", "hp-crosshair"), svg.A("visibility", "hidden"),
		svg.N("x1", p.l), svg.N("x2", p.l), svg.N("y1", p.t), svg.N("y2", p.b),
		svg.A("stroke", "currentColor"), svg.A("stroke-opacity", "0.4"))
	w.Open("metadata", svg.A("class", "hp-tips"))
	// The JSON escapes <, > and &, and holds no other characters that
	// XML text must escape, so it can be written raw.
	w.RawBytes(p.tipsJSON(nil))
	w.Close("metadata")
	w.Close("g")
}

// tipsJSON appends the tooltip payload as JSON.
func (p *plan) tipsJSON(dst []byte) []byte {
	switch p.tipMode() {
	case "band":
		return p.tipsBand().appendJSON(dst)
	case "xy":
		return p.tipsXY().appendJSON(dst)
	}
	return p.tipsX().appendJSON(dst)
}

// entries returns one entry per series, with the value of its point at x
// key k, or "–" when a series has none there. A series with several
// points at k (a scatter of repeated observations) lists every value, in
// data order, joined by ", ", so tooltips and the table show all the data
// the chart draws. A labeled Dot's labels at k follow its series name
// ("deploys · v2.1 rollout"), so the tooltip and keyboard reading carry
// them in charts where the heading is the x value.
func (p *plan) entries(idx []pointIdx, k xkey) []tipEntry {
	v := make([]tipEntry, len(p.series))
	for si, s := range p.series {
		val, name := missing, tipName(s.label)
		if i := idx[s.index].at(k); i >= 0 {
			val = p.yValue(s.y[i])
			labels := s.pointLabels(i, nil)
			if j := idx[s.index].after(i); j >= 0 {
				vals := []string{val}
				for ; j >= 0; j = idx[s.index].after(j) {
					vals = append(vals, p.yValue(s.y[j]))
					labels = s.pointLabels(j, labels)
				}
				val = strings.Join(vals, ", ")
			}
			if len(labels) > 0 {
				name += " · " + strings.Join(labels, ", ")
			}
		}
		v[si] = tipEntry{s.slot, name, val}
	}
	return v
}

// pointLabels appends point i's Dot label to dst when it has one.
func (s *series) pointLabels(i int, dst []string) []string {
	if i < len(s.labels) && s.labels[i] != "" {
		dst = append(dst, s.labels[i])
	}
	return dst
}

// maxTipName is the length in runes past which a series name is
// shortened where it repeats once per point (tooltip rows and the <title>
// of each bar and dot), so output grows with the number of points, not
// with points × name length. The legend, the table header and the
// description still carry the full name once.
const maxTipName = 100

// tipName returns name, shortened with an ellipsis to maxTipName runes.
func tipName(name string) string { return shorten(name, maxTipName) }

// pointIdx locates a series' plotted points by x key.
type pointIdx struct {
	first map[xkey]int // the first plotted point at each x key
	// next[i] is the next plotted point after i with the same x key, or
	// -1. It is nil when no key repeats, the usual case.
	next []int
}

// at returns the index of the first plotted point at k, or -1.
func (pi pointIdx) at(k xkey) int {
	if i, ok := pi.first[k]; ok {
		return i
	}
	return -1
}

// after returns the index of the next plotted point with the same x key
// as point i, or -1.
func (pi pointIdx) after(i int) int {
	if pi.next == nil {
		return -1
	}
	return pi.next[i]
}

// pointIndex indexes each series' plotted points by x key.
func (p *plan) pointIndex() []pointIdx {
	out := make([]pointIdx, len(p.series))
	for si, s := range p.series {
		m := make(map[xkey]int, len(s.y))
		var next []int
		var tail map[xkey]int // the last point seen at each repeated key
		for i := range s.y {
			if math.IsNaN(s.hi[i]) {
				continue
			}
			k, ok := keyOf(s.x, i)
			if !ok {
				continue
			}
			first, dup := m[k]
			if !dup {
				m[k] = i
				continue
			}
			if next == nil {
				next = make([]int, len(s.y))
				for j := range next {
					next[j] = -1
				}
				tail = make(map[xkey]int)
			}
			prev, ok := tail[k]
			if !ok {
				prev = first
			}
			next[prev] = i
			tail[k] = i
		}
		out[si] = pointIdx{m, next}
	}
	return out
}

func (p *plan) tipsBand() tipsAxis {
	t := tipsAxis{
		mode: "band", xs: make([]float64, len(p.cats)), bw: r2(p.x.band.Step()),
		top: r2(p.t), bottom: r2(p.b), rows: make([]tipRow, len(p.cats)),
	}
	idx := p.pointIndex()
	for ci, cat := range p.cats {
		t.xs[ci] = r2(p.bandCenter(ci))
		t.rows[ci] = tipRow{cat, cat, p.entries(idx, xkey{s: cat})}
	}
	return t
}

// xcol is one distinct x value that has at least one plotted point.
type xcol struct {
	k  xkey
	s  *series // first series with a point here, for formatting x
	i  int     // that point's index in s
	px float64
}

// xColumns returns the distinct plotted x values in axis order. Points
// outside the x domain (the overscan point of a sliding window) are drawn,
// clipped, but have no column: the pointer, which has no horizontal bound
// in mode x, could otherwise land on data that is not visible
// (docs/spec.md §3.5).
func (p *plan) xColumns(idx []pointIdx) []xcol {
	n := 0
	for _, s := range p.series {
		n = max(n, len(s.y)) // series usually share x values
	}
	cols := make([]xcol, 0, n)
	seen := make(map[xkey]bool, n)
	for si, s := range p.series {
		for i := range s.y {
			if math.IsNaN(s.hi[i]) || !p.xInDomain(s, i) {
				continue
			}
			k, _ := keyOf(s.x, i)
			if !seen[k] && idx[si].at(k) == i {
				seen[k] = true
				cols = append(cols, xcol{k, s, i, s.px[i]})
			}
		}
	}
	slices.SortStableFunc(cols, func(a, b xcol) int {
		switch p.xk {
		case xTime:
			return cmp.Or(cmp.Compare(a.k.t, b.k.t), cmp.Compare(a.k.n, b.k.n))
		case xBand:
			return cmp.Compare(p.catIdx[a.k.s], p.catIdx[b.k.s])
		}
		return cmp.Compare(a.k.f, b.k.f)
	})
	return cols
}

func (p *plan) tipsX() tipsAxis {
	idx := p.pointIndex()
	cols := p.xColumns(idx)
	t := tipsAxis{mode: "x", xs: make([]float64, len(cols)), top: r2(p.t), bottom: r2(p.b), rows: make([]tipRow, len(cols))}
	for ci, c := range cols {
		t.xs[ci] = r2(c.px)
		t.rows[ci] = tipRow{p.xValue(c.s, c.i), c.k.rowKey(p.xk), p.entries(idx, c.k)}
	}
	return t
}

func (p *plan) tipsXY() tipsXY {
	var t tipsXY
	for _, s := range p.series {
		for i := range s.y {
			k, ok := keyOf(s.x, i)
			if math.IsNaN(s.pyHi[i]) || !ok || !p.xInDomain(s, i) {
				continue
			}
			// A label heads the row, followed by the x value, which is
			// half of what a scatter point says.
			head := p.xValue(s, i)
			if i < len(s.labels) && s.labels[i] != "" {
				head = s.labels[i] + " · " + head
			}
			t.pts = append(t.pts, [2]float64{r2(s.px[i]), r2(s.pyHi[i])})
			t.rows = append(t.rows, tipRow{head, k.rowKey(p.xk), []tipEntry{{s.slot, tipName(s.label), p.yValue(s.y[i])}}})
		}
	}
	return t
}
