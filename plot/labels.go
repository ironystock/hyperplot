package plot

import (
	"math"
	"strconv"

	"github.com/ironystock/hyperplot/internal/svg"
	"github.com/ironystock/hyperplot/internal/textwidth"
)

// box is an axis-aligned rectangle used for label collision tests.
type box struct{ x0, y0, x1, y1 float64 }

func (a box) overlaps(b box) bool {
	return a.x0 < b.x1 && b.x0 < a.x1 && a.y0 < b.y1 && b.y0 < a.y1
}

// inPlot reports whether a point lies within the (clip-padded) plot.
func (p *plan) inPlot(x, y float64) bool {
	return x >= p.l-clipPad && x <= p.r+clipPad && y >= p.t-clipPad && y <= p.b+clipPad
}

func (p *plan) writeLabels(w *svg.Writer) {
	w.Open("g", svg.A("class", "hp-labels"), svg.A("aria-hidden", "true"))
	// End dots: every line (without Points) and area, on its last point.
	for _, s := range p.series {
		if s.kind == kindArea || s.kind == kindLine && !s.points {
			i := lastValid(s.pyHi)
			if i >= 0 && p.inPlot(s.px[i], s.pyHi[i]) {
				w.Void("circle", dotAttrs("hp-end hp-s"+strconv.Itoa(s.slot), s.px[i], s.pyHi[i])...)
			}
		}
	}
	for _, d := range p.direct {
		s := d.s
		if math.IsNaN(s.pyHi[d.i]) {
			continue
		}
		w.Elem("text", d.text, mutedText("hp-label hp-direct", s.px[d.i]+directOffset, s.pyHi[d.i], svg.A("dy", "0.32em"))...)
	}
	p.writeDotLabels(w)
	if !p.hasData() {
		// An empty frame looks broken; say why it is empty.
		w.Elem("text", "No data", mutedText("hp-label hp-empty", (p.l+p.r)/2, (p.t+p.b)/2,
			svg.A("dy", "0.32em"), svg.A("text-anchor", "middle"))...)
	}
	w.Close("g")
}

// hasData reports whether any series has at least one valid value.
func (p *plan) hasData() bool {
	for _, s := range p.series {
		for _, y := range s.pyHi {
			if !math.IsNaN(y) {
				return true
			}
		}
	}
	return false
}

// dotLabel is a Dot label placed beside its point.
type dotLabel struct {
	text   string
	x, y   float64
	anchor string
}

// writeDotLabels draws Dot labels beside their points.
func (p *plan) writeDotLabels(w *svg.Writer) {
	labels, _ := p.placeDotLabels()
	for _, l := range labels {
		w.Elem("text", l.text, mutedText("hp-label hp-dot-label", l.x, l.y, svg.A("dy", "0.32em"), svg.A("text-anchor", l.anchor))...)
	}
}

// placeDotLabels places Dot labels beside their points, to the right when
// there is room, then left, above or below, skipping any label that would
// overlap a dot or a label already placed. It also returns the collision
// grid it used (nil when no Dot has labels), so tests can measure the
// work placement did.
func (p *plan) placeDotLabels() ([]dotLabel, *boxGrid) {
	f := p.f
	// Every dot is an obstacle, so that no label covers another point
	// (its surface-colored ring may be touched). A label's own dot is
	// never hit: the gap clears it.
	hasLabels := false
	for _, s := range p.series {
		hasLabels = hasLabels || s.kind == kindDot && s.labels != nil
	}
	if !hasLabels {
		return nil, nil
	}
	// Placed boxes live in a grid over the clip-padded plot, so each
	// collision test checks only nearby boxes: placement stays close to
	// linear in the number of dots, even for tens of thousands of labeled
	// dots on a large chart. Dot obstacles are centered on the nearest
	// whole pixel and grown by half a pixel, so each still covers its dot,
	// and dots within a pixel of each other are one obstacle; a cell also
	// holds a bounded number of boxes (see maxCellBoxes). Clustered dots
	// therefore cannot make each test scan the whole cluster.
	const edge = clipPad + dotRadius
	placed := newBoxGrid(box{p.l - edge, p.t - edge, p.r + edge, p.b + edge}, f)
	seen := make(map[box]struct{})
	for _, s := range p.series {
		if s.kind != kindDot {
			continue
		}
		for i, x := range s.px {
			if y := s.pyHi[i]; !math.IsNaN(y) && p.inPlot(x, y) {
				const r = dotRadius + 0.5
				cx, cy := math.Round(x), math.Round(y)
				b := box{cx - r, cy - r, cx + r, cy + r}
				if _, dup := seen[b]; !dup {
					seen[b] = struct{}{}
					placed.add(b)
				}
			}
		}
	}
	var out []dotLabel
	maxW := math.Max(40, (p.r-p.l)/3)
	for _, s := range p.series {
		if s.kind != kindDot || s.labels == nil {
			continue
		}
		for i, label := range s.labels {
			x, y := s.px[i], s.pyHi[i]
			if label == "" || math.IsNaN(y) || !p.inPlot(x, y) {
				continue
			}
			text := textwidth.Truncate(label, f, maxW)
			tw := textwidth.Width(text, f)
			gap := float64(dotRadius + 3)
			vgap := float64(dotRadius+2) + f/2 // center offset above or below
			// Candidates in order of preference: right, left, above,
			// below. The first that stays inside the plot (so it never
			// runs into the tick labels or the margins) and overlaps no
			// placed label and no dot wins.
			cands := [...]struct {
				b      box
				tx, ty float64
				anchor string
			}{
				{box{x + gap, y - f/2, x + gap + tw, y + f/2}, x + gap, y, "start"},
				{box{x - gap - tw, y - f/2, x - gap, y + f/2}, x - gap, y, "end"},
				{box{x - tw/2, y - vgap - f/2, x + tw/2, y - vgap + f/2}, x, y - vgap, "middle"},
				{box{x - tw/2, y + vgap - f/2, x + tw/2, y + vgap + f/2}, x, y + vgap, "middle"},
			}
			for _, c := range cands {
				b := c.b
				if b.x0 < p.l || b.x1 > p.r || b.y0 < p.t-clipPad || b.y1 > p.b+clipPad || !placed.free(b) {
					continue
				}
				placed.add(b)
				out = append(out, dotLabel{text, c.tx, c.ty, c.anchor})
				break
			}
		}
	}
	return out, placed
}

// boxGrid holds placed boxes in a uniform grid of square cells over an
// area. A box is recorded in every cell it touches, so a query scans only
// the cells its own box touches. Boxes reaching past the area are
// recorded in, and queried against, the edge cells. Answers are exact
// except that a full cell (maxCellBoxes) counts as occupied.
type boxGrid struct {
	area       box
	cell       float64
	cols, rows int
	cells      [][]box
	// checks counts the box comparisons free has made, so tests can
	// assert that placement does bounded work per query.
	checks int
}

// maxGridSide bounds a boxGrid's columns and rows, so that a huge chart
// costs at most 256 × 256 cell headers (1.5 MB).
const maxGridSide = 256

// maxCellBoxes bounds the boxes one cell records. A full cell counts as
// occupied, so a query costs at most maxCellBoxes comparisons per cell
// however densely dots cluster (80,000 dots in one cell made placement
// quadratic). At the default font size and up to 4096 px a cell is at
// most 16 px square, so 64 distinct dots touching it leave no room for a
// label anyway (placed labels never overlap, so they cannot fill a cell).
const maxCellBoxes = 64

// newBoxGrid returns an empty grid over area with cells of at least size
// px, grown as needed to keep within maxGridSide cells per side.
func newBoxGrid(area box, size float64) *boxGrid {
	w, h := math.Max(1, area.x1-area.x0), math.Max(1, area.y1-area.y0)
	cell := math.Max(math.Max(1, size), math.Max(w, h)/maxGridSide)
	g := &boxGrid{area: area, cell: cell}
	g.cols = max(1, min(maxGridSide, int(math.Ceil(w/cell))))
	g.rows = max(1, min(maxGridSide, int(math.Ceil(h/cell))))
	g.cells = make([][]box, g.cols*g.rows)
	return g
}

// index returns the cell index along one axis for coordinate v, clamped
// to [0, n).
func (g *boxGrid) index(v, origin float64, n int) int {
	i := math.Floor((v - origin) / g.cell)
	return int(math.Max(0, math.Min(float64(n-1), i)))
}

// span returns the range of cells that b touches.
func (g *boxGrid) span(b box) (c0, c1, r0, r1 int) {
	return g.index(b.x0, g.area.x0, g.cols), g.index(b.x1, g.area.x0, g.cols),
		g.index(b.y0, g.area.y0, g.rows), g.index(b.y1, g.area.y0, g.rows)
}

func (g *boxGrid) add(b box) {
	c0, c1, r0, r1 := g.span(b)
	for r := r0; r <= r1; r++ {
		for c := c0; c <= c1; c++ {
			// A full cell is occupied whatever else it would hold.
			if i := r*g.cols + c; len(g.cells[i]) < maxCellBoxes {
				g.cells[i] = append(g.cells[i], b)
			}
		}
	}
}

// free reports whether b overlaps no box in the grid and touches no full
// cell.
func (g *boxGrid) free(b box) bool {
	c0, c1, r0, r1 := g.span(b)
	for r := r0; r <= r1; r++ {
		for c := c0; c <= c1; c++ {
			cell := g.cells[r*g.cols+c]
			if len(cell) >= maxCellBoxes {
				return false
			}
			for _, o := range cell {
				g.checks++
				if b.overlaps(o) {
					return false
				}
			}
		}
	}
	return true
}
