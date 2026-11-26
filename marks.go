package hyperplot

// Mark is one layer of a chart: a [Line], [Area], [Bar], [Dot] or [RuleY].
// Marks share the chart's scales, so several marks can be combined in a
// single plot. Pointers to marks (*Line and so on) are accepted too.
//
// The set of marks is closed for now; new kinds will be added in later
// releases without breaking existing code.
type Mark interface {
	isMark()
}

// Curve selects how a [Line] or [Area] connects its points.
type Curve int

const (
	// CurveLinear joins points with straight segments.
	CurveLinear Curve = iota
	// CurveMonotone draws a smooth curve that passes through every point
	// and never overshoots it: the curve is monotonic between any two
	// adjacent points, so it cannot invent peaks or dips that are not in
	// the data.
	CurveMonotone
	// CurveStep draws a step function that holds each value until the
	// next point (a "step-after" curve), suited to counts and states that
	// change at discrete moments.
	CurveStep
)

// Line draws a series as a 2px line. A missing value in Y (NaN, ±Inf or a
// magnitude beyond 1e300) breaks the line rather than being interpolated.
// The last valid point gets an end dot.
type Line struct {
	// Name identifies the series in the legend, tooltips and data table.
	Name string
	// X holds the x values: Floats, Times or Categories.
	X Values
	// Y holds one y value per x value.
	Y []float64
	// Curve selects straight, smooth or stepped interpolation.
	Curve Curve
	// Slot pins the series to a categorical color, 1 through 8. With 0
	// the series takes the lowest unused slot in mark order. Pin slots
	// when filters can hide series, so that a series keeps its color
	// whichever others are shown.
	Slot int
	// Points draws a dot at every data point, not just the last one.
	Points bool
}

// Area draws a series as a filled area with a 2px line on its upper edge.
// The fill is a light wash of the series color. When the chart stacks
// (Chart.Stack), areas stack on one another in mark order, matching
// points by x value.
type Area struct {
	// Name identifies the series in the legend, tooltips and data table.
	Name string
	// X holds the x values: Floats, Times or Categories.
	X Values
	// Y holds the upper edge, one value per x value.
	Y []float64
	// Curve selects straight, smooth or stepped interpolation.
	Curve Curve
	// Slot pins the series to a categorical color; see [Line].
	Slot int
	// Base is the lower edge of the area, one value per point, for bands
	// such as a min–max range. nil fills down to zero. Base is ignored
	// when the chart stacks.
	Base []float64
}

// Bar draws one column per category. Several Bar marks over the same
// categories are drawn side by side as a grouped column chart, or stacked
// when Chart.Stack is set (positive values stack upward and negative
// values downward). Each category may appear at most once in X.
type Bar struct {
	// Name identifies the series in the legend, tooltips and data table.
	Name string
	// X holds the categories. It must not be empty.
	X Categories
	// Y holds one value per category.
	Y []float64
	// Slot pins the series to a categorical color; see [Line].
	Slot int
}

// Dot draws one 8px point per value, for scatter plots or to emphasize
// individual observations.
//
// Color alone separates at most three scatter series reliably for readers
// with color-vision deficiencies; beyond that, use small multiples or
// label the points.
type Dot struct {
	// Name identifies the series in the legend, tooltips and data table.
	Name string
	// X holds the x values: Floats, Times or Categories.
	X Values
	// Y holds one y value per x value.
	Y []float64
	// Slot pins the series to a categorical color; see [Line].
	Slot int
	// Labels optionally names each point. Labels are drawn beside the
	// points where they fit without overlapping and fill the first column
	// of the data table. In tooltips, native titles and keyboard reading
	// a label heads the point's row, ahead of its x value, in a chart of
	// only dots, and follows the series name in charts with lines, areas
	// or bars. If set, it must have one entry per point.
	Labels []string
}

// RuleY draws a horizontal reference line across the plot, such as a
// target, threshold or average. It is an annotation, not a series: it
// takes no color slot and does not appear in the legend. A rule outside a
// fixed Y domain is not drawn.
type RuleY struct {
	// Y is the value at which the rule is drawn.
	Y float64
	// Label is drawn above the right end of the rule.
	Label string
}

func (Line) isMark()  {}
func (Area) isMark()  {}
func (Bar) isMark()   {}
func (Dot) isMark()   {}
func (RuleY) isMark() {}

// series is the renderer's uniform view of a data-carrying mark. Slices
// alias the caller's data and are never written to.
type series struct {
	mark   int // index in Chart.Marks
	index  int // position among data marks, in mark order
	name   string
	label  string // display name: name, or "Series N"
	slot   int
	pinned bool
	x      Values
	y      []float64
	base   []float64 // area lower edge, or nil
	curve  Curve
	labels []string
	kind   seriesKind
	points bool
	key    string // stable id fragment

	// lo and hi are the lower and upper edges in data units, owned by
	// the renderer. Missing points are NaN.
	lo, hi []float64
	// px, pyLo and pyHi are pixel positions, NaN when missing.
	px, pyLo, pyHi []float64
	round          []bool // kindBar: whether a bar's data end is rounded
}

type seriesKind int

const (
	kindLine seriesKind = iota
	kindArea
	kindBar
	kindDot
)

func (k seriesKind) String() string {
	switch k {
	case kindLine:
		return "Line"
	case kindArea:
		return "Area"
	case kindBar:
		return "Bar"
	default:
		return "Dot"
	}
}
