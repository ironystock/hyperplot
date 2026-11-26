package svg

// Path builds an SVG path "d" string using absolute commands. Absolute
// commands keep the command structure of a path stable when only its
// values change, which lets browsers interpolate "d" in CSS transitions.
type Path struct {
	buf []byte
}

func (p *Path) cmd(c byte, vals ...float64) *Path {
	p.buf = append(p.buf, c)
	for i, v := range vals {
		if i > 0 {
			p.buf = append(p.buf, ',')
		}
		p.buf = AppendNum(p.buf, v)
	}
	return p
}

// M moves to (x, y).
func (p *Path) M(x, y float64) *Path { return p.cmd('M', x, y) }

// L draws a line to (x, y).
func (p *Path) L(x, y float64) *Path { return p.cmd('L', x, y) }

// H draws a horizontal line to x.
func (p *Path) H(x float64) *Path { return p.cmd('H', x) }

// V draws a vertical line to y.
func (p *Path) V(y float64) *Path { return p.cmd('V', y) }

// C draws a cubic Bézier curve to (x, y).
func (p *Path) C(x1, y1, x2, y2, x, y float64) *Path {
	return p.cmd('C', x1, y1, x2, y2, x, y)
}

// Q draws a quadratic Bézier curve to (x, y).
func (p *Path) Q(x1, y1, x, y float64) *Path { return p.cmd('Q', x1, y1, x, y) }

// A draws an elliptical arc to (x, y) with radii rx and ry, the x axis
// rotated by rotation degrees. largeArc picks the longer of the two
// candidate arcs and sweep picks the clockwise one (in screen
// coordinates, where y grows downward). A zero radius draws a straight
// line, as the SVG specification requires, so callers can keep a path's
// command structure fixed while a corner radius shrinks to nothing.
func (p *Path) A(rx, ry, rotation float64, largeArc, sweep bool, x, y float64) *Path {
	p.cmd('A', rx, ry, rotation)
	p.buf = append(p.buf, ',', flag(largeArc), ',', flag(sweep), ',')
	p.buf = AppendNum(p.buf, x)
	p.buf = append(p.buf, ',')
	p.buf = AppendNum(p.buf, y)
	return p
}

func flag(b bool) byte {
	if b {
		return '1'
	}
	return '0'
}

// Z closes the current subpath.
func (p *Path) Z() *Path {
	p.buf = append(p.buf, 'Z')
	return p
}

// Empty reports whether no commands have been written.
func (p *Path) Empty() bool { return len(p.buf) == 0 }

// String returns the path data.
func (p *Path) String() string { return string(p.buf) }
