package hyperplot

import (
	"math"
	"time"
)

// Values is a column of x-axis values. The concrete type picks the x
// scale: [Floats] for a linear scale, [Times] for a time scale and
// [Categories] for a band scale. All marks in one chart must use the same
// kind of x values.
type Values interface {
	// Len returns the number of values.
	Len() int
	kind() xKind
}

// Floats is a column of numbers, plotted on a linear scale.
type Floats []float64

// Times is a column of instants, plotted on a time scale.
type Times []time.Time

// Categories is a column of labels, plotted on a band scale in order of
// first appearance across the chart's marks.
type Categories []string

// Len returns the number of values.
func (v Floats) Len() int { return len(v) }

// Len returns the number of values.
func (v Times) Len() int { return len(v) }

// Len returns the number of values.
func (v Categories) Len() int { return len(v) }

func (Floats) kind() xKind     { return xLinear }
func (Times) kind() xKind      { return xTime }
func (Categories) kind() xKind { return xBand }

type xKind int

const (
	xNone xKind = iota
	xLinear
	xTime
	xBand
)

func (k xKind) String() string {
	switch k {
	case xLinear:
		return "Floats"
	case xTime:
		return "Times"
	case xBand:
		return "Categories"
	default:
		return "none"
	}
}

// Map builds a column by applying fn to every element of data. It lets
// row-oriented records feed hyperplot's column-oriented marks:
//
//	hyperplot.Line{
//		X: hyperplot.Times(hyperplot.Map(rows, func(r Row) time.Time { return r.At })),
//		Y: hyperplot.Map(rows, func(r Row) float64 { return r.Latency }),
//	}
func Map[T, V any](data []T, fn func(T) V) []V {
	out := make([]V, len(data))
	for i, d := range data {
		out[i] = fn(d)
	}
	return out
}

// maxAbs is the largest magnitude hyperplot plots. Values beyond it are
// treated as missing: scale arithmetic on spans near ±MaxFloat64
// overflows, and no real chart needs them.
const maxAbs = 1e300

// valid reports whether a value should be drawn. NaN, ±Inf and values
// beyond ±1e300 mark a missing value: lines and areas break into separate
// segments around it.
func valid(v float64) bool { return !math.IsNaN(v) && v >= -maxAbs && v <= maxAbs }
