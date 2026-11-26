package scale

import (
	"math"
)

// Linear is a continuous scale that maps a numeric domain onto a numeric
// range with a linear transform. The zero value maps everything to 0;
// construct one with [NewLinear].
type Linear struct {
	// Domain is the input interval [d0, d1]. d0 may be greater than d1.
	Domain [2]float64
	// Range is the output interval [r0, r1], usually pixels. For a
	// vertical axis this is typically [bottom, top] so larger values are
	// drawn higher.
	Range [2]float64
	// Clamp restricts Map's output to the Range.
	Clamp bool
}

// NewLinear returns a Linear scale mapping [d0, d1] onto [r0, r1].
func NewLinear(d0, d1, r0, r1 float64) Linear {
	return Linear{Domain: [2]float64{d0, d1}, Range: [2]float64{r0, r1}}
}

// Map converts a domain value to a range value. A degenerate domain
// (d0 == d1) maps every input to the midpoint of the range.
func (s Linear) Map(v float64) float64 {
	d0, d1 := s.Domain[0], s.Domain[1]
	r0, r1 := s.Range[0], s.Range[1]
	if d0 == d1 {
		return (r0 + r1) / 2
	}
	t := (v - d0) / (d1 - d0)
	if s.Clamp {
		t = math.Max(0, math.Min(1, t))
	}
	return r0 + t*(r1-r0)
}

// Invert converts a range value back to a domain value.
func (s Linear) Invert(px float64) float64 {
	d0, d1 := s.Domain[0], s.Domain[1]
	r0, r1 := s.Range[0], s.Range[1]
	if r0 == r1 {
		return (d0 + d1) / 2
	}
	t := (px - r0) / (r1 - r0)
	if s.Clamp {
		t = math.Max(0, math.Min(1, t))
	}
	return d0 + t*(d1-d0)
}

// Ticks returns roughly count evenly spaced, human-friendly values inside
// the domain. Steps are powers of ten times 1, 2 or 5. The values are
// computed so that they do not accumulate floating-point error (0.3, not
// 0.30000000000000004).
func (s Linear) Ticks(count int) []float64 {
	return Ticks(s.Domain[0], s.Domain[1], count)
}

// TickStep returns the distance between adjacent values returned by
// Ticks(count).
func (s Linear) TickStep(count int) float64 {
	return TickStep(s.Domain[0], s.Domain[1], count)
}

// Nice returns a copy of the scale whose domain is extended outward to
// round values aligned with the tick step for count ticks, so that the
// first and last ticks fall exactly on the domain bounds.
func (s Linear) Nice(count int) Linear {
	d0, d1 := s.Domain[0], s.Domain[1]
	reversed := d1 < d0
	if reversed {
		d0, d1 = d1, d0
	}
	start, stop := niceBounds(d0, d1, count)
	if reversed {
		start, stop = stop, start
	}
	s.Domain = [2]float64{start, stop}
	return s
}

func niceBounds(start, stop float64, count int) (float64, float64) {
	if count <= 0 || !finite(start) || !finite(stop) || start == stop {
		return start, stop
	}
	var prestep float64
	for range 10 {
		step := tickIncrement(start, stop, count)
		if step == prestep {
			break
		}
		switch {
		case step > 0:
			start = math.Floor(start/step) * step
			stop = math.Ceil(stop/step) * step
		case step < 0:
			start = math.Ceil(start*step) / step
			stop = math.Floor(stop*step) / step
		default:
			return start, stop
		}
		prestep = step
	}
	return start, stop
}

// Ticks returns roughly count human-friendly values between start and
// stop inclusive. If start > stop the ticks are returned in descending
// order. It returns nil when count <= 0 or the bounds are not finite.
func Ticks(start, stop float64, count int) []float64 {
	if count <= 0 || !finite(start) || !finite(stop) {
		return nil
	}
	if start == stop {
		return []float64{start}
	}
	reverse := stop < start
	if reverse {
		start, stop = stop, start
	}
	i1, i2, inc := tickSpec(start, stop, float64(count))
	// Spans that overflow (near ±MaxFloat64) or underflow (subnormal
	// steps) give non-finite indices; there are no sensible ticks then.
	if !finite(i1) || !finite(i2) || !finite(inc) || inc == 0 || i2 < i1 || i2-i1 > maxTicks {
		return nil
	}
	n := int(i2-i1) + 1
	ticks := make([]float64, n)
	for i := range n {
		v := i1 + float64(i)
		if inc < 0 {
			ticks[i] = v / -inc
		} else {
			ticks[i] = v * inc
		}
	}
	if reverse {
		for i, j := 0, n-1; i < j; i, j = i+1, j-1 {
			ticks[i], ticks[j] = ticks[j], ticks[i]
		}
	}
	return ticks
}

// TickStep returns the spacing between ticks that [Ticks] would return
// for the same arguments.
func TickStep(start, stop float64, count int) float64 {
	if count <= 0 || !finite(start) || !finite(stop) || start == stop {
		return 0
	}
	if stop < start {
		start, stop = stop, start
	}
	_, _, inc := tickSpec(start, stop, float64(count))
	if inc < 0 {
		return 1 / -inc
	}
	return inc
}

func tickIncrement(start, stop float64, count int) float64 {
	_, _, inc := tickSpec(start, stop, float64(count))
	return inc
}

var (
	e10 = math.Sqrt(50)
	e5  = math.Sqrt(10)
	e2  = math.Sqrt(2)
)

// tickSpec computes the integer tick indices i1..i2 and the increment.
// A negative increment -k means the step is 1/k; dividing by k instead of
// multiplying by 1/k keeps decimal ticks exact.
func tickSpec(start, stop, count float64) (i1, i2, inc float64) {
	step := (stop - start) / math.Max(0, count)
	power := math.Floor(math.Log10(step))
	err := step / math.Pow(10, power)
	factor := 1.0
	switch {
	case err >= e10:
		factor = 10
	case err >= e5:
		factor = 5
	case err >= e2:
		factor = 2
	}
	if power < 0 {
		inc = math.Pow(10, -power) / factor
		i1 = math.Round(start * inc)
		i2 = math.Round(stop * inc)
		if i1/inc < start {
			i1++
		}
		if i2/inc > stop {
			i2--
		}
		inc = -inc
	} else {
		inc = math.Pow(10, power) * factor
		i1 = math.Round(start / inc)
		i2 = math.Round(stop / inc)
		if i1*inc < start {
			i1++
		}
		if i2*inc > stop {
			i2--
		}
	}
	if i2 < i1 && count >= 0.5 && count < 2 {
		return tickSpec(start, stop, count*2)
	}
	return i1, i2, inc
}

func finite(v float64) bool { return !math.IsNaN(v) && !math.IsInf(v, 0) }

// maxTicks bounds the number of ticks Ticks returns, so that a huge count
// cannot exhaust memory.
const maxTicks = 100_000
