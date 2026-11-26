package hyperplot

import (
	"math"
	"strconv"
	"time"

	"github.com/ironystock/hyperplot/internal/svg"
)

// This file writes the values that docs/spec.md §1.1 and §4.2 put on the
// wire for the client and for other servers to compare as text: the root
// geometry attributes and the row keys. They use the spec's number and
// time value forms, not svg.Num, because a sliding window compares domain
// ends exactly and a server reads keys back.

// appendWireNumber appends v in the spec's number form: the shortest
// round-trip decimal, written like ECMAScript's Number.prototype.toString
// and like encoding/json ("1", "2.5", "1e+21", "1e-7"). v must be finite.
func appendWireNumber(dst []byte, v float64) []byte {
	if v == 0 {
		return append(dst, '0') // also folds -0
	}
	if a := math.Abs(v); a >= 1e-6 && a < 1e21 {
		return strconv.AppendFloat(dst, v, 'f', -1, 64)
	}
	start := len(dst)
	dst = strconv.AppendFloat(dst, v, 'e', -1, 64)
	// Go writes e-07 and e+21; the number form drops the exponent's
	// leading zero, as encoding/json and ECMAScript do.
	if n := len(dst); n-start >= 4 && dst[n-4] == 'e' && dst[n-2] == '0' {
		dst[n-2] = dst[n-1]
		dst = dst[:n-1]
	}
	return dst
}

func wireNumber(v float64) string { return string(appendWireNumber(nil, v)) }

// epochMS returns t as milliseconds since the Unix epoch, fractional when
// t is not a whole millisecond.
func epochMS(t time.Time) float64 {
	return float64(t.Unix())*1000 + float64(t.Nanosecond())/1e6
}

// msSince returns t − ms in milliseconds, where ms is in epoch ms. The
// whole milliseconds are subtracted as integers and the fractions
// separately, so the result keeps sub-nanosecond precision for any t in
// years 1 to 9999, however far ms is from 1970.
func msSince(ms float64, t time.Time) float64 {
	whole, frac := math.Modf(ms)
	return float64(t.Unix()*1000-int64(whole)) + (float64(t.Nanosecond())/1e6 - frac)
}

// timeWire returns the drawn time domain for data spanning [t0, t1], in
// epoch ms: the float64 numbers data-hp-x writes, rounded outward until
// they hold [t0, t1]. The chart is drawn through these very numbers
// (timeX), so data-hp-x describes the scale actually used (docs/spec.md
// §4.2) even where epoch ms cannot hold the data's precision: near 2026 a
// float64 resolves about 0.24 µs, and a domain a few microseconds wide
// written as rounded ends misplaced keys by tens of pixels. Ends that are
// whole milliseconds (or any exact float64) are kept as they are. A single
// instant stays one; it is drawn mid-plot.
func timeWire(t0, t1 time.Time) [2]float64 {
	f0, f1 := epochMS(t0), epochMS(t1)
	if !t0.Before(t1) {
		return [2]float64{f0, f0}
	}
	for msSince(f0, t0) < 0 {
		f0 = math.Nextafter(f0, math.Inf(-1))
	}
	for msSince(f1, t1) > 0 {
		f1 = math.Nextafter(f1, math.Inf(1))
	}
	return [2]float64{f0, f1}
}

// timeX maps a time to its x position through the drawn time domain.
func (p *plan) timeX(t time.Time) float64 {
	f0, f1 := p.x.ms[0], p.x.ms[1]
	if f0 == f1 {
		return (p.l + p.r) / 2
	}
	// As scale.Linear.Map: the conversion stops an FMA, so every platform
	// computes the same bits. For whole-second or whole-millisecond data
	// the ratio is the one scale.Time computed, bit for bit.
	return p.l + float64(msSince(f0, t)/(f1-f0)*(p.r-p.l))
}

// timeKey returns the spec's time value form of t: RFC 3339 in UTC, with
// fractional seconds only when they are not zero.
func timeKey(t time.Time) string { return t.UTC().Format(time.RFC3339Nano) }

// rowKey returns the key of an x value (docs/spec.md §1.1): the time
// value, the number or the category, by axis kind.
func (k xkey) rowKey(kind xKind) string {
	switch kind {
	case xTime:
		return timeKey(time.Unix(k.t, int64(k.n)))
	case xBand:
		return k.s
	}
	return wireNumber(k.f)
}

// geometryAttrs returns the root attributes of docs/spec.md §4.2:
// data-hp-plot (the plot rectangle in viewBox units), data-hp-x (the x
// scale's kind and domain, omitted when the chart has no x values) and
// data-hp-y (the y scale's domain). The client compares them across frames
// to tell a sliding window from a change of values.
func (p *plan) geometryAttrs() []svg.Attr {
	plot := svg.Num(p.l) + " " + svg.Num(p.t) + " " + svg.Num(p.r) + " " + svg.Num(p.b)
	attrs := []svg.Attr{svg.A("data-hp-plot", plot)}
	switch p.xk {
	case xTime:
		// A single instant is widened by a second each way: its epoch ms
		// can be off by half a float64 step (up to 16 µs in year 9999),
		// which is negligible against two seconds but not two milliseconds.
		attrs = append(attrs, svg.A("data-hp-x", "time "+wireDomain(p.x.ms[0], p.x.ms[1], 1000)))
	case xBand:
		attrs = append(attrs, svg.A("data-hp-x", "band"))
	case xLinear:
		d := p.x.lin.Domain
		attrs = append(attrs, svg.A("data-hp-x", "linear "+wireDomain(d[0], d[1], 1)))
	}
	return append(attrs, svg.A("data-hp-y", "linear "+wireDomain(p.y.Domain[0], p.y.Domain[1], 1)))
}

// wireDomain writes a domain as "d0 d1" with d0 < d1. The scales draw a
// degenerate domain (one point, or a time axis whose data has a single
// instant) at the middle of the range, so widening it symmetrically, by
// pad or by the smallest power-of-two multiple of pad that changes d0,
// describes the same drawing; the spec requires d0 < d1.
func wireDomain(d0, d1, pad float64) string {
	if d0 > d1 {
		d0, d1 = d1, d0
	}
	if math.IsNaN(d0) || math.IsInf(d0, 0) {
		d0 = 0
	}
	if !(d0 < d1) || math.IsInf(d1, 0) {
		for d0-pad == d0 || d0+pad == d0 {
			pad *= 2
		}
		d0, d1 = d0-pad, d0+pad
	}
	b := appendWireNumber(nil, d0)
	b = append(b, ' ')
	return string(appendWireNumber(b, d1))
}
