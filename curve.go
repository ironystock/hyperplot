package hyperplot

import (
	"math"

	"github.com/ironystock/hyperplot/internal/svg"
)

// pt is a point in pixel space.
type pt struct{ x, y float64 }

// piece is one segment of a curve, drawn from the previous piece's end
// (or the run's first point) to to. A cubic piece has control points c1
// and c2; any other piece is a straight line.
type piece struct {
	cubic  bool
	c1, c2 pt
	to     pt
}

// curvePieces returns the pieces that join pts with the given curve.
func curvePieces(pts []pt, c Curve) []piece {
	if len(pts) < 2 {
		return nil
	}
	switch c {
	case CurveStep:
		out := make([]piece, 0, 2*(len(pts)-1))
		for i := 1; i < len(pts); i++ {
			out = append(out, piece{to: pt{pts[i].x, pts[i-1].y}}, piece{to: pts[i]})
		}
		return out
	case CurveMonotone:
		if len(pts) > 2 {
			return monotonePieces(pts)
		}
	}
	out := make([]piece, 0, len(pts)-1)
	for _, q := range pts[1:] {
		out = append(out, piece{to: q})
	}
	return out
}

// monotonePieces implements the monotone cubic interpolation of
// Fritsch and Carlson ("Monotone Piecewise Cubic Interpolation", 1980),
// in the form of d3-shape's curveMonotoneX, in pixel space. The curve
// passes through every point and is monotonic between adjacent points,
// so it never overshoots the data. It needs at least three points.
func monotonePieces(pts []pt) []piece {
	n := len(pts)
	t := make([]float64, n) // tangent (dy/dx) at each point
	for i := 1; i < n-1; i++ {
		t[i] = slope3(pts[i-1], pts[i], pts[i+1])
	}
	t[0] = slope2(pts[0], pts[1], t[1])
	t[n-1] = slope2(pts[n-2], pts[n-1], t[n-2])
	out := make([]piece, 0, n-1)
	for i := range n - 1 {
		a, b := pts[i], pts[i+1]
		dx := (b.x - a.x) / 3
		out = append(out, piece{
			cubic: true,
			c1:    pt{a.x + dx, a.y + dx*t[i]},
			c2:    pt{b.x - dx, b.y - dx*t[i+1]},
			to:    b,
		})
	}
	return out
}

// slope3 returns the tangent at b given its neighbors a and c: the
// harmonic-style blend of the two secant slopes, limited so the curve
// stays monotonic, and zero at a local extremum.
func slope3(a, b, c pt) float64 {
	h0, h1 := b.x-a.x, c.x-b.x
	if h0 == 0 || h1 == 0 || h0+h1 == 0 {
		return 0
	}
	s0, s1 := (b.y-a.y)/h0, (c.y-b.y)/h1
	p := (s0*h1 + s1*h0) / (h0 + h1)
	v := (sign(s0) + sign(s1)) * math.Min(math.Min(math.Abs(s0), math.Abs(s1)), 0.5*math.Abs(p))
	if math.IsNaN(v) || math.IsInf(v, 0) {
		return 0
	}
	return v
}

// slope2 returns the one-sided tangent at an end point of the segment a–b
// given the tangent t at its other end.
func slope2(a, b pt, t float64) float64 {
	h := b.x - a.x
	if h == 0 {
		return t
	}
	v := (3*(b.y-a.y)/h - t) / 2
	if math.IsNaN(v) || math.IsInf(v, 0) {
		return t
	}
	return v
}

func sign(v float64) float64 {
	switch {
	case v > 0:
		return 1
	case v < 0:
		return -1
	}
	return 0
}

// reversePieces returns the pieces that trace the same curve backward,
// starting from the end of ps. start is the forward curve's first point.
func reversePieces(start pt, ps []piece) []piece {
	out := make([]piece, len(ps))
	for i := range ps {
		q := ps[len(ps)-1-i]
		from := start
		if j := len(ps) - 2 - i; j >= 0 {
			from = ps[j].to
		}
		out[i] = piece{cubic: q.cubic, c1: q.c2, c2: q.c1, to: from}
	}
	return out
}

func writePieces(p *svg.Path, ps []piece) {
	for _, q := range ps {
		if q.cubic {
			p.C(q.c1.x, q.c1.y, q.c2.x, q.c2.y, q.to.x, q.to.y)
		} else {
			p.L(q.to.x, q.to.y)
		}
	}
}

// runs splits indices 0..n-1 into maximal runs where ok(i) holds.
func runs(n int, ok func(int) bool) [][2]int {
	var out [][2]int
	start := -1
	for i := 0; i <= n; i++ {
		if i < n && ok(i) {
			if start < 0 {
				start = i
			}
			continue
		}
		if start >= 0 {
			out = append(out, [2]int{start, i})
			start = -1
		}
	}
	return out
}

// linePath returns the path of a line through (xs[i], ys[i]), broken at
// missing (NaN) points. An isolated point becomes a zero-length segment,
// which the round line cap draws as a dot.
func linePath(xs, ys []float64, c Curve) string {
	var p svg.Path
	for _, r := range runs(len(xs), func(i int) bool { return !math.IsNaN(xs[i]) && !math.IsNaN(ys[i]) }) {
		pts := make([]pt, 0, r[1]-r[0])
		for i := r[0]; i < r[1]; i++ {
			pts = append(pts, pt{xs[i], ys[i]})
		}
		p.M(pts[0].x, pts[0].y)
		if len(pts) == 1 {
			p.L(pts[0].x, pts[0].y)
			continue
		}
		writePieces(&p, curvePieces(pts, c))
	}
	return p.String()
}

// areaPath returns the closed path between an upper edge (xs, hi) and a
// lower edge (xs, lo), broken at missing points. Runs of a single point
// enclose nothing and are skipped.
func areaPath(xs, lo, hi []float64, c Curve) string {
	var p svg.Path
	ok := func(i int) bool { return !math.IsNaN(xs[i]) && !math.IsNaN(hi[i]) && !math.IsNaN(lo[i]) }
	for _, r := range runs(len(xs), ok) {
		if r[1]-r[0] < 2 {
			continue
		}
		top := make([]pt, 0, r[1]-r[0])
		bot := make([]pt, 0, r[1]-r[0])
		for i := r[0]; i < r[1]; i++ {
			top = append(top, pt{xs[i], hi[i]})
			bot = append(bot, pt{xs[i], lo[i]})
		}
		p.M(top[0].x, top[0].y)
		writePieces(&p, curvePieces(top, c))
		last := bot[len(bot)-1]
		p.L(last.x, last.y)
		writePieces(&p, reversePieces(bot[0], curvePieces(bot, c)))
		p.Z()
	}
	return p.String()
}
