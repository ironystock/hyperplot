package scale

// Band maps a list of discrete categories onto evenly spaced bands of a
// continuous range, the layout used by bar and column charts.
//
// The range is divided into one step per category. Each step holds a band
// of width Bandwidth() plus inner padding; outer padding is added before
// the first and after the last band.
type Band struct {
	// Domain lists the categories in display order. Categories should be
	// unique: a repeated category gets a band of its own, but Map and
	// Index find only its first position.
	Domain []string
	// Range is the output interval [r0, r1] in pixels. r0 may be greater
	// than r1 (a vertical axis is usually [bottom, top]); the first
	// category is always placed at the r0 end.
	Range [2]float64
	// PaddingInner is the fraction of each step left empty between
	// bands, in [0, 1].
	PaddingInner float64
	// PaddingOuter is the empty space before the first and after the
	// last band, in multiples of the step.
	PaddingOuter float64
	// Align positions the bands within any leftover outer space: 0 is
	// start, 0.5 (the default via NewBand) is centered, 1 is end.
	Align float64
}

// NewBand returns a Band scale with 20% inner padding, 10% outer padding
// and centered alignment.
func NewBand(domain []string, r0, r1 float64) Band {
	return Band{Domain: domain, Range: [2]float64{r0, r1}, PaddingInner: 0.2, PaddingOuter: 0.1, Align: 0.5}
}

func (s Band) layout() (start, step, bandwidth float64) {
	n := float64(len(s.Domain))
	r0, r1 := s.Range[0], s.Range[1]
	reverse := r1 < r0
	if reverse {
		r0, r1 = r1, r0
	}
	pi := clamp01(s.PaddingInner)
	po := max(0, s.PaddingOuter)
	denom := max(1, n-pi+po*2)
	step = (r1 - r0) / denom
	start = r0 + (r1-r0-step*(n-pi))*clamp01(s.Align)
	bandwidth = step * (1 - pi)
	if reverse {
		// Walk from the far end so the first category sits at r0.
		start = r1 - (start - r0) - bandwidth
		step = -step
	}
	return start, step, bandwidth
}

// Map returns the start coordinate of the band for category key, and
// false if key is not in the domain.
func (s Band) Map(key string) (float64, bool) {
	i := s.Index(key)
	if i < 0 {
		return 0, false
	}
	return s.MapIndex(i), true
}

// MapIndex returns the start coordinate of the i-th band.
func (s Band) MapIndex(i int) float64 {
	start, step, _ := s.layout()
	return start + step*float64(i)
}

// Index returns the position of key in the domain, or -1.
func (s Band) Index(key string) int {
	for i, k := range s.Domain {
		if k == key {
			return i
		}
	}
	return -1
}

// Bandwidth returns the width of each band.
func (s Band) Bandwidth() float64 {
	_, _, bw := s.layout()
	return bw
}

// Step returns the distance between the starts of adjacent bands.
func (s Band) Step() float64 {
	_, step, _ := s.layout()
	if step < 0 {
		return -step
	}
	return step
}

func clamp01(v float64) float64 { return max(0, min(1, v)) }
