package plot

import (
	"math"
	"strconv"
	"strings"

	"github.com/ironystock/hyperplot/scale"
)

// missing is shown in tooltips and tables for a value that is absent.
const missing = "–"

// formatValue formats a single data value precisely but compactly, for
// tooltips, tables and descriptions, where a value is read on its own
// rather than against neighboring ticks. It keeps about four significant
// digits: 0.01234, 1.234, 12.34, 123.4, 1,234, 12,345, 123.5K, 1.235M.
// From 100,000 up every value is compact at four significant digits, so
// that values of different sizes read alike side by side in a tooltip
// ("1.102M", "575.4K") instead of mixing "1.1M" with "575,359". Values
// from 1e15 up and under 1e-4 use scientific notation like the tick
// labels ("3.142×10¹⁸", "1.23×10⁻⁵"), so they stay short.
func formatValue(v float64) string {
	a := math.Abs(v)
	switch {
	case math.IsNaN(v) || math.IsInf(v, 0):
		return scale.FormatNumber(v, 0)
	case a == 0:
		return "0"
	case a >= 1e15:
		return trimMantissa(scale.FormatExponent(v, 3))
	case a < 1e-4:
		return trimMantissa(scale.FormatExponent(v, 2))
	case a >= 1e5:
		// Round to four significant digits first, so 999,960 becomes
		// "1M" rather than "1,000K".
		p := math.Pow10(int(math.Floor(math.Log10(a))) - 3)
		r := math.Round(a/p) * p
		decimals := 3 - int(math.Floor(math.Log10(r)))%3
		s := scale.FormatCompact(math.Copysign(r, v), decimals)
		i := strings.LastIndexAny(s, "0123456789")
		return trimFraction(s[:i+1]) + s[i+1:]
	case a >= 1000:
		return scale.FormatNumber(math.Round(v), 0)
	}
	d := max(0, min(3-int(math.Floor(math.Log10(a))), 8))
	// Rounding first (half away from zero) keeps FormatNumber on its fast
	// path; a·10^d < 1e12 here, so the rounding is exact.
	p := math.Pow10(d)
	return trimFraction(scale.FormatNumber(math.Copysign(math.Round(a*p)/p, v), d))
}

// trimMantissa applies trimFraction to the mantissa of a number in
// scale.FormatExponent's notation ("1.500×10¹⁸" becomes "1.5×10¹⁸").
func trimMantissa(s string) string {
	m, exp, ok := strings.Cut(s, "×")
	if !ok {
		return s
	}
	return trimFraction(m) + "×" + exp
}

// trimFraction drops trailing zeros after a decimal point, and the point
// itself if nothing follows it.
func trimFraction(s string) string {
	if !strings.Contains(s, ".") {
		return s
	}
	s = strings.TrimRight(s, "0")
	return strings.TrimSuffix(s, ".")
}

// maxUnit is the length in runes past which Axis.Unit is shortened with
// an ellipsis where it repeats once per value (tooltips, titles and table
// cells), so output grows with the number of points, not with points ×
// unit length. The description carries the unit whole.
const maxUnit = 32

// shorten returns s, shortened with an ellipsis to n runes.
func shorten(s string, n int) string {
	if len(s) <= n {
		return s // fewer bytes than the limit means fewer runes too
	}
	k := 0
	for i := range s {
		if k == n-1 {
			return s[:i] + "…"
		}
		k++
	}
	return s
}

// yNumber formats a y value with the Y formatter, without a unit.
func (p *plan) yNumber(v float64) string {
	if !valid(v) {
		return missing
	}
	if f := p.c.Y.Format; f != nil {
		return f(v)
	}
	return formatValue(v)
}

// yValue formats a y value with the Y formatter and unit.
func (p *plan) yValue(v float64) string {
	if !valid(v) {
		return missing
	}
	return p.yNumber(v) + p.yUnit
}

// xValue formats the i-th x value of s in full, for tooltips and tables.
func (p *plan) xValue(s *series, i int) string {
	switch x := s.x.(type) {
	case Floats:
		return p.xNumber(x[i])
	case Times:
		return p.x.timeFmt(x[i])
	case Categories:
		return x[i]
	}
	return ""
}

// xNumber formats a linear x value with the X formatter and unit.
func (p *plan) xNumber(v float64) string {
	if !valid(v) {
		return missing
	}
	return p.xPlain(v) + p.xUnit
}

// xPlain formats a linear x value with the X formatter, without a unit.
// With no formatter it uses p.xNum, which layout chooses so that distinct
// x values never print alike.
func (p *plan) xPlain(v float64) string {
	switch {
	case p.c.X.Format != nil:
		return p.c.X.Format(v)
	case p.xNum != nil:
		return p.xNum(v)
	}
	return formatValue(v)
}

// distinctFormat returns a formatter that labels every value in sorted
// (ascending, without duplicates) differently from its neighbors.
// formatValue's four significant digits suit y values, which are read
// against an axis, but x values head tooltip and table rows, and two rows
// reading "100K" cannot be told apart. So when formatValue merges two
// neighbors, values are written in full with enough decimals to separate
// the closest pair ("100,001", "1,001.2"), and failing that (float noise
// below 12 decimals) in the shortest form that round-trips.
func distinctFormat(sorted []float64) func(float64) string {
	if distinctUnder(sorted, formatValue) {
		return formatValue
	}
	gap := math.Inf(1)
	for i := 1; i < len(sorted); i++ {
		gap = math.Min(gap, sorted[i]-sorted[i-1])
	}
	for d := max(0, int(math.Ceil(-math.Log10(gap)))); d <= 12; d++ {
		f := func(v float64) string { return scale.FormatNumber(v, d) }
		if distinctUnder(sorted, f) {
			return f
		}
	}
	return shortestNumber
}

// distinctUnder reports whether f labels neighbors in sorted apart.
func distinctUnder(sorted []float64, f func(float64) string) bool {
	prev := ""
	for i, v := range sorted {
		s := f(v)
		if i > 0 && s == prev {
			return false
		}
		prev = s
	}
	return true
}

// shortestNumber formats v in the shortest form that parses back to v,
// with the typographic minus.
func shortestNumber(v float64) string {
	if v < 0 {
		return scale.Minus + strconv.FormatFloat(-v, 'g', -1, 64)
	}
	return strconv.FormatFloat(v, 'g', -1, 64)
}

// distinctPair formats lo and hi (lo < hi) so that they read differently,
// for the end points of an axis range in the description.
func distinctPair(lo, hi float64) (string, string) {
	f := distinctFormat([]float64{lo, hi})
	return f(lo), f(hi)
}
