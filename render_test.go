package hyperplot

import (
	"bytes"
	"context"
	"html/template"
	"math"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"
)

func TestFormatValue(t *testing.T) {
	tests := []struct {
		v    float64
		want string
	}{
		{0, "0"},
		{math.Copysign(0, -1), "0"},
		{1, "1"},
		{1.5, "1.5"},
		{12.345, "12.35"},
		{123.456, "123.5"},
		{-0.5, "−0.5"},
		{0.012345, "0.01235"},
		{999.96, "1,000"},
		{1234.6, "1,235"},
		{-98765, "−98,765"},
		{99_999, "99,999"},
		{100_000, "100K"},
		{262_659, "262.7K"},
		{575_359, "575.4K"},
		{-575_359, "−575.4K"},
		{999_960, "1M"},
		{1_102_345, "1.102M"},
		{1_250_000, "1.25M"},
		{12_345_678, "12.35M"},
		{2_000_000_000, "2B"},
		{0.00001234, "1.23×10⁻⁵"},
		{-0.00005, "−5×10⁻⁵"},
		{999e12, "999T"},
		{1e15, "1×10¹⁵"},
		{3e18, "3×10¹⁸"},
		{-3.14159e18, "−3.142×10¹⁸"},
		{1e300, "1×10³⁰⁰"},
		{math.NaN(), "NaN"},
	}
	for _, tt := range tests {
		if got := formatValue(tt.v); got != tt.want {
			t.Errorf("formatValue(%v) = %q, want %q", tt.v, got, tt.want)
		}
	}
}

func TestCustomFormatters(t *testing.T) {
	pct := func(v float64) string { return strconv.FormatFloat(v*100, 'f', 0, 64) + "%" }
	c := &Chart{
		X:     Axis{Format: func(v float64) string { return "day " + strconv.FormatFloat(v, 'f', 0, 64) }, Unit: "!"},
		Y:     Axis{Format: pct, Unit: " share"},
		Marks: []Mark{Line{Name: "a", X: Floats{1, 2}, Y: []float64{0.25, 0.5}}},
	}
	p := checkSVG(t, mustRender(t, c))
	row := p.tips["rows"].([]any)[1].(map[string]any)
	if row["t"] != "day 2!" || row["v"].([]any)[0].([]any)[2] != "50% share" {
		t.Errorf("row = %v", row)
	}
	if !slices.Contains(p.texts, "day 1") || !slices.Contains(p.texts, "50%") {
		t.Errorf("tick labels do not use the formatters: %v", p.texts)
	}

	tc := &Chart{
		X:     Axis{TimeFormat: func(tm time.Time) string { return "T" + tm.Format("15") }},
		Marks: []Mark{Line{X: Times{t0, t0.Add(3 * time.Hour)}, Y: []float64{1, 2}}},
	}
	if tp := checkSVG(t, mustRender(t, tc)); !slices.Contains(tp.texts, "T14") {
		t.Errorf("time ticks do not use TimeFormat: %v", tp.texts)
	}
}

func TestMixedKindErrorNamesPointerMarks(t *testing.T) {
	_, err := (&Chart{Marks: []Mark{
		&Dot{Name: "first", X: Floats{1}, Y: []float64{1}},
		&Area{Name: "second", X: Categories{"a"}, Y: []float64{1}},
	}}).Render()
	if err == nil || !strings.Contains(err.Error(), `mark 1 (Area "second") has Categories x values but mark 0 (Dot "first") has Floats`) {
		t.Errorf("err = %v", err)
	}
}

func TestBarPath(t *testing.T) {
	tests := []struct {
		name            string
		x, w, y0, y1, r float64
		want            string
	}{
		{"up", 10, 20, 100, 40, 4, "M10,100V44A4,4,0,0,1,14,40H26A4,4,0,0,1,30,44V100Z"},
		{"down", 10, 20, 100, 160, 4, "M10,100V156A4,4,0,0,0,14,160H26A4,4,0,0,0,30,156V100Z"},
		{"square", 10, 20, 100, 40, 0, "M10,100V40A0,0,0,0,1,10,40H30A0,0,0,0,1,30,40V100Z"},
		{"short bar clamps radius", 0, 20, 100, 98, 4, "M0,100V100A2,2,0,0,1,2,98H18A2,2,0,0,1,20,100V100Z"},
		{"thin bar clamps radius", 0, 2, 100, 0, 4, "M0,100V1A1,1,0,0,1,1,0H1A1,1,0,0,1,2,1V100Z"},
		{"zero height", 0, 20, 100, 100, 4, "M0,100V100A0,0,0,0,1,0,100H20A0,0,0,0,1,20,100V100Z"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := barPath(tt.x, tt.w, tt.y0, tt.y1, tt.r); got != tt.want {
				t.Errorf("got  %s\nwant %s", got, tt.want)
			}
		})
	}
}

func TestBarGeometry(t *testing.T) {
	c := &Chart{Width: 640, Marks: []Mark{
		Bar{X: Categories{"a"}, Y: []float64{1}},
		Bar{X: Categories{"a"}, Y: []float64{1}},
		Bar{X: Categories{"a"}, Y: []float64{1}},
	}}
	p, err := newPlan(c)
	if err != nil {
		t.Fatal(err)
	}
	bw := p.x.band.Bandwidth()
	var prevEnd float64
	for gi := range 3 {
		off, thick := p.barGeometry(gi, 3)
		if thick != defaultBarWidth {
			t.Errorf("bar %d thickness %v, want the %d px cap", gi, thick, defaultBarWidth)
		}
		if gi > 0 && off-prevEnd != barGap {
			t.Errorf("gap before bar %d is %v, want %d", gi, off-prevEnd, barGap)
		}
		if off < 0 || off+thick > bw {
			t.Errorf("bar %d [%v, %v] leaves the band [0, %v]", gi, off, off+thick, bw)
		}
		prevEnd = off + thick
	}
	// Narrow bands shrink bars but never fill the band.
	c.Width = 60
	p, _ = newPlan(c)
	off, thick := p.barGeometry(0, 3)
	if thick >= p.x.band.Bandwidth() || off < 0 || thick <= 0 {
		t.Errorf("narrow band: offset %v thickness %v of %v", off, thick, p.x.band.Bandwidth())
	}
}

func TestStackedBarsTouchNothing(t *testing.T) {
	// Stacked segments leave a gap and only the outermost is rounded.
	c := &Chart{ID: "s", Stack: true, Marks: []Mark{
		Bar{Name: "a", X: Categories{"x"}, Y: []float64{10}},
		Bar{Name: "b", X: Categories{"x"}, Y: []float64{10}},
	}}
	p, err := newPlan(c)
	if err != nil {
		t.Fatal(err)
	}
	b := string(mustRender(t, c))
	a, bb := p.series[0], p.series[1]
	if gap := a.pyHi[0] - bb.pyLo[0]; gap != 0 {
		t.Fatalf("data edges should meet before the gap is applied, got %v", gap)
	}
	if !strings.Contains(b, "A0,0,0,0,1") || !strings.Contains(b, "A4,4,0,0,1") {
		t.Errorf("want one square inner segment and one rounded outer segment:\n%s", b)
	}
}

func TestMonotoneNeverOvershoots(t *testing.T) {
	pts := []pt{{0, 10}, {10, 20}, {20, 20}, {30, 0}, {40, 5}, {50, 5.5}, {60, 100}}
	ps := curvePieces(pts, CurveMonotone)
	if len(ps) != len(pts)-1 {
		t.Fatalf("%d pieces for %d points", len(ps), len(pts))
	}
	for i, q := range ps {
		a, b := pts[i], pts[i+1]
		if q.to != b {
			t.Errorf("piece %d ends at %v, want %v", i, q.to, b)
		}
		lo, hi := math.Min(a.y, b.y), math.Max(a.y, b.y)
		for _, c := range []pt{q.c1, q.c2} {
			if c.y < lo-1e-9 || c.y > hi+1e-9 {
				t.Errorf("piece %d control point %v outside [%v, %v]: overshoot", i, c, lo, hi)
			}
		}
	}
	// A flat run stays flat.
	if ps[1].c1.y != 20 || ps[1].c2.y != 20 {
		t.Errorf("flat segment bends: %+v", ps[1])
	}
}

func TestCurvePieces(t *testing.T) {
	pts := []pt{{0, 0}, {10, 5}, {20, 3}}
	step := curvePieces(pts, CurveStep)
	want := []pt{{10, 0}, {10, 5}, {20, 5}, {20, 3}}
	for i, q := range step {
		if q.to != want[i] || q.cubic {
			t.Errorf("step piece %d = %+v, want line to %v", i, q, want[i])
		}
	}
	if got := curvePieces(pts[:2], CurveMonotone); len(got) != 1 || got[0].cubic {
		t.Errorf("two-point monotone should be one straight piece, got %+v", got)
	}
	if got := curvePieces(pts[:1], CurveLinear); got != nil {
		t.Errorf("one point should have no pieces, got %+v", got)
	}
	// Reversing traces the same points backward, with swapped controls.
	for _, c := range []Curve{CurveLinear, CurveMonotone, CurveStep} {
		fwd := curvePieces(pts, c)
		rev := reversePieces(pts[0], fwd)
		if rev[len(rev)-1].to != pts[0] {
			t.Errorf("curve %d: reversed path ends at %v", c, rev[len(rev)-1].to)
		}
		if c == CurveMonotone && (rev[0].c1 != fwd[len(fwd)-1].c2 || rev[0].c2 != fwd[len(fwd)-1].c1) {
			t.Errorf("curve %d: reversed control points not swapped", c)
		}
	}
}

func TestLinePathGaps(t *testing.T) {
	xs := []float64{0, 1, 2, 3, 4, 5}
	ys := []float64{0, 1, nan, 3, nan, 5}
	got := linePath(xs, ys, CurveLinear)
	want := "M0,0L1,1M3,3L3,3M5,5L5,5"
	if got != want {
		t.Errorf("linePath = %q, want %q", got, want)
	}
	if got := linePath(nil, nil, CurveMonotone); got != "" {
		t.Errorf("empty linePath = %q", got)
	}
	area := areaPath([]float64{0, 1, 2, 3}, []float64{10, 10, 10, nan}, []float64{5, 4, 3, 2}, CurveLinear)
	if area != "M0,5L1,4L2,3L2,10L1,10L0,10Z" {
		t.Errorf("areaPath = %q", area)
	}
}

func TestRuns(t *testing.T) {
	ok := []bool{true, true, false, true, false, false, true}
	got := runs(len(ok), func(i int) bool { return ok[i] })
	want := [][2]int{{0, 2}, {3, 4}, {6, 7}}
	if len(got) != len(want) {
		t.Fatalf("runs = %v, want %v", got, want)
	}
	for i := range got {
		if got[i] != want[i] {
			t.Errorf("runs = %v, want %v", got, want)
		}
	}
}

func TestThin(t *testing.T) {
	mk := func(xs ...float64) []tickLabel {
		var out []tickLabel
		for _, x := range xs {
			out = append(out, tickLabel{v: x, w: 20, anchor: "middle"})
		}
		return out
	}
	if got := thin(mk(0, 40, 80), 6); len(got) != 3 {
		t.Errorf("spaced labels thinned to %d", len(got))
	}
	if got := thin(mk(0, 20, 40, 60, 80), 6); len(got) != 3 || got[1].v != 40 {
		t.Errorf("crowded labels thinned to %+v, want every 2nd", got)
	}
	if got := thin(mk(0, 1, 2), 6); len(got) != 1 {
		t.Errorf("overlapping labels thinned to %d, want 1", len(got))
	}
	if got := thin(nil, 6); len(got) != 0 {
		t.Error("thin(nil) not empty")
	}
}

func TestCrisp(t *testing.T) {
	for _, tt := range []struct{ in, want float64 }{{10, 10.5}, {10.4, 10.5}, {10.6, 11.5}} {
		if got := crisp(tt.in); got != tt.want {
			t.Errorf("crisp(%v) = %v, want %v", tt.in, got, tt.want)
		}
	}
	c := &Chart{Marks: []Mark{Line{X: Floats{0, 1}, Y: []float64{0, 7}}}}
	p, _ := newPlan(c)
	b := string(mustRender(t, c))
	for _, tk := range p.yTicks {
		y := crisp(tk.v)
		if y != math.Round(y-0.5)+0.5 {
			t.Errorf("gridline at %v is not on a half pixel", y)
		}
	}
	if !strings.Contains(b, `class="hp-baseline"`) {
		t.Error("no baseline")
	}
}

func TestBaseline(t *testing.T) {
	c := &Chart{Marks: []Mark{Bar{X: Categories{"a", "b"}, Y: []float64{-5, 10}}}}
	p, err := newPlan(c)
	if err != nil {
		t.Fatal(err)
	}
	if got, want := p.baselineY(), crisp(p.y.Map(0)); got != want {
		t.Errorf("baseline %v, want the zero line %v", got, want)
	}
	c = &Chart{Marks: []Mark{Line{X: Floats{1, 2}, Y: []float64{-5, 10}}}}
	p, _ = newPlan(c)
	if got := p.baselineY(); got != crisp(p.b) {
		t.Errorf("line chart baseline %v, want plot bottom %v", got, crisp(p.b))
	}
}

func TestEndDotsAndPoints(t *testing.T) {
	c := &Chart{Marks: []Mark{
		Line{Name: "a", X: Floats{1, 2, 3}, Y: []float64{1, 2, nan}},
		Line{Name: "b", X: Floats{1, 2, 3}, Y: []float64{3, 1, 2}, Points: true},
		Area{Name: "c", X: Floats{1, 2, 3}, Y: []float64{1, 1, 1}},
	}}
	p := checkSVG(t, mustRender(t, c))
	if got := p.classes["hp-end"]; got != 2 {
		t.Errorf("%d end dots, want 2 (line a and area c)", got)
	}
	if got := p.classes["hp-dot"]; got != 3 {
		t.Errorf("%d point dots, want 3 for line b", got)
	}
}

func TestDotLabelsAvoidCollisions(t *testing.T) {
	c := &Chart{Marks: []Mark{Dot{
		X: Floats{0, 25, 25, 25, 25, 25, 50}, Y: []float64{1, 5, 5, 5, 5, 5, 9},
		Labels: []string{"", "first", "second", "third", "fourth", "fifth", ""},
	}}}
	out := mustRender(t, c)
	p := checkSVG(t, out)
	if got := p.classes["hp-dot-label"]; got != 4 {
		t.Errorf("%d labels drawn for 5 coincident points, want 4 (right, left, above, below)", got)
	}
	if strings.Contains(string(out), ">fifth<") {
		t.Error("the fifth coincident label was drawn; it has no free position")
	}
}

func TestDotLabelsStayInPlotAndOffDots(t *testing.T) {
	tests := []struct {
		name string
		dot  Dot
		want int
	}{
		// A point on the left plot edge with no room on its right (a
		// neighbor's dot is there) has nowhere to put its label: the
		// left side would run into the Y tick labels.
		{"left edge", Dot{X: Floats{0, 1.5, 100}, Y: []float64{50, 50, 0}, Labels: []string{"edge label", "", ""}}, 0},
		// A label never covers another point: dots on all four sides
		// leave it nowhere to go.
		{"boxed in", Dot{
			X: Floats{50, 53, 47, 50, 50, 0, 100}, Y: []float64{50, 50, 50, 53, 47, 0, 100},
			Labels: []string{"middle", "", "", "", "", "", ""},
		}, 0},
		{"free", Dot{X: Floats{50, 0, 100}, Y: []float64{50, 0, 100}, Labels: []string{"middle", "", ""}}, 1},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p := checkSVG(t, mustRender(t, &Chart{Marks: []Mark{tt.dot}}))
			if got := p.classes["hp-dot-label"]; got != tt.want {
				t.Errorf("%d labels drawn, want %d", got, tt.want)
			}
		})
	}
}

func TestEmptyChartSaysNoData(t *testing.T) {
	tests := []struct {
		name  string
		chart *Chart
		want  int
	}{
		{"no marks", &Chart{}, 1},
		{"all missing", &Chart{Marks: []Mark{Line{X: Floats{1, 2}, Y: []float64{nan, nan}}}}, 1},
		{"one value", &Chart{Marks: []Mark{Line{X: Floats{1, 2}, Y: []float64{nan, 3}}}}, 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p := checkSVG(t, mustRender(t, tt.chart))
			if got := p.classes["hp-empty"]; got != tt.want {
				t.Errorf("%d hp-empty labels, want %d", got, tt.want)
			}
		})
	}
}

func TestBandLabelsTruncateToTheirBand(t *testing.T) {
	c := &Chart{Width: 320, Height: 200, Marks: []Mark{Bar{
		X: Categories{"A very long category name that goes on", "B", "C"}, Y: []float64{1, 2, 3},
	}}}
	out := string(mustRender(t, c))
	for _, want := range []string{">B</text>", ">C</text>", "…</text>"} {
		if !strings.Contains(out, want) {
			t.Errorf("output lacks %q: every band keeps its label and the long one is truncated", want)
		}
	}

	// When truncating to the band would still need thinning, labels stay
	// whole and are thinned instead: never "10:3…" on every other band.
	c = &Chart{Width: 324, Height: 200, Marks: []Mark{Bar{
		X: Categories{"10:30 PM", "10:40 PM", "10:50 PM", "11:00 PM", "11:10 PM", "11:20 PM"}, Y: []float64{1, 2, 3, 4, 5, 6},
	}}}
	out = string(mustRender(t, c))
	if strings.Contains(out, "…") {
		t.Error("band labels were truncated although thinning was needed anyway")
	}
	if !strings.Contains(out, ">10:30 PM</text>") || strings.Contains(out, ">10:40 PM</text>") {
		t.Error("want whole labels on every other band")
	}
}

func TestTable(t *testing.T) {
	c := &Chart{
		ID:    "t",
		Title: "Latency",
		X:     Axis{Label: "Quarter"},
		Y:     Axis{Unit: " ms"},
		Marks: []Mark{
			Bar{Name: "a", X: Categories{"Q1", "Q2"}, Y: []float64{1.5, 2}},
			Bar{Name: "<b>", X: Categories{"Q2"}, Y: []float64{3}},
		},
	}
	got, err := c.Table()
	if err != nil {
		t.Fatal(err)
	}
	want := `<table class="hp-table" id="t-table"><caption>Latency</caption>` +
		`<thead><tr><th scope="col">Quarter</th><th scope="col">a</th><th scope="col">&lt;b&gt;</th></tr></thead>` +
		// A missing value shows a dash but is read as "no data": screen
		// readers skip a bare en dash at their default punctuation level.
		`<tbody><tr id="t-r-` + hash8("Q1") + `"><th scope="row">Q1</th><td>1.5 ms</td><td><span aria-hidden="true">–</span><span class="hp-sr"> no data</span></td></tr>` +
		`<tr id="t-r-` + hash8("Q2") + `"><th scope="row">Q2</th><td>2 ms</td><td>3 ms</td></tr></tbody></table>`
	if string(got) != want {
		t.Errorf("Table:\n got %s\nwant %s", got, want)
	}

	scatter := &Chart{ID: "s", Marks: []Mark{
		Dot{Name: "a", X: Floats{1}, Y: []float64{2}, Labels: []string{"p"}},
		Dot{Name: "b", X: Floats{3}, Y: []float64{4}},
	}}
	got, err = scatter.Table()
	if err != nil {
		t.Fatal(err)
	}
	for _, s := range []string{
		`<caption>Chart data</caption>`, `<th scope="col">Label</th><th scope="col">Series</th><th scope="col">X</th><th scope="col">Y</th>`,
		`<tr id="s-r-` + hash8("a") + "-" + hash8("1") + `"><td>p</td><td>a</td><th scope="row">1</th><td>2</td></tr>`,
		`<tr id="s-r-` + hash8("b") + "-" + hash8("3") + `"><td></td><td>b</td><th scope="row">3</th><td>4</td></tr>`,
	} {
		if !strings.Contains(string(got), s) {
			t.Errorf("scatter table lacks %s\n%s", s, got)
		}
	}

	mixed := &Chart{ID: "m", Marks: []Mark{
		Line{Name: "l", X: Floats{1, 2}, Y: []float64{1, 2}},
		Dot{Name: "d", X: Floats{2}, Y: []float64{5}, Labels: []string{"peak"}},
	}}
	got, _ = mixed.Table()
	if !strings.Contains(string(got), `<tr id="m-r-`+hash8("2")+`"><td>peak</td><th scope="row">2</th><td>2</td><td>5</td></tr>`) {
		t.Errorf("dot labels should fill the first column:\n%s", got)
	}
	if _, err := (&Chart{Width: 1}).Table(); err == nil {
		t.Error("Table accepted an invalid chart")
	}
}

// Labels in tooltips and tables must tell every data point apart, even
// when the axis ticks are coarser than the data (an hour of 30-second
// buckets has minute ticks).
func TestTimeLabelsAreDistinct(t *testing.T) {
	t0 := time.Date(2026, 3, 3, 14, 0, 0, 0, time.UTC)
	xs := make(Times, 120)
	ys := make([]float64, len(xs))
	for i := range xs {
		xs[i] = t0.Add(time.Duration(i) * 30 * time.Second)
		ys[i] = float64(i)
	}
	c := &Chart{Marks: []Mark{Line{X: xs, Y: ys}}}
	tbl, err := c.Table()
	if err != nil {
		t.Fatal(err)
	}
	seen := map[string]bool{}
	for _, row := range strings.Split(string(tbl), `<th scope="row">`)[1:] {
		label, _, _ := strings.Cut(row, "<")
		if seen[label] {
			t.Fatalf("two rows are labeled %q", label)
		}
		seen[label] = true
	}
	if !strings.Contains(string(tbl), `<th scope="col">Time</th>`) {
		t.Error(`an unlabeled time axis should head its column "Time"`)
	}
	if !seen["Mar 3, 2026 2:00:30 PM"] {
		t.Errorf("30-second buckets should be labeled with seconds; rows: %v", seen)
	}
	if svg := mustRender(t, c); !strings.Contains(string(svg), `"t":"Mar 3, 2026 2:00:30 PM"`) {
		t.Error("tooltip rows should carry seconds too")
	}
}

func TestSparkline(t *testing.T) {
	tests := []struct {
		name  string
		s     Sparkline
		label string
		parts []string
	}{
		{
			"basic",
			Sparkline{Values: []float64{3, 1, 4, 1, 5}, Label: "Load"},
			"Load: first 3, min 1, max 5, last 5",
			[]string{`viewBox="0 0 120 28"`, `<svg xmlns="http://www.w3.org/2000/svg" class="hp-spark" `, `class="hp-s1"`, `class="hp-line"`, `class="hp-end"`},
		},
		{
			"area",
			Sparkline{Values: []float64{1, 2}, Area: true, Width: 60, Height: 20},
			"Sparkline: first 1, min 1, max 2, last 2",
			[]string{`class="hp-area"`, `width="60"`},
		},
		{"gaps", Sparkline{Values: []float64{nan, 2, nan, 4, math.Inf(1)}}, "Sparkline: first 2, min 2, max 4, last 4", []string{"M"}},
		{"flat", Sparkline{Values: []float64{7, 7, 7}}, "Sparkline: first 7, min 7, max 7, last 7", []string{`cy="14"`}},
		{"single", Sparkline{Values: []float64{7}}, "Sparkline: first 7, min 7, max 7, last 7", []string{`cx="60"`}},
		{"empty", Sparkline{Label: "Errors"}, "Errors: no data", nil},
		{"all NaN", Sparkline{Values: []float64{nan}}, "Sparkline: no data", nil},
		{"unit", Sparkline{Values: []float64{198, 180, 240, 212}, Label: "p99 latency", Unit: " ms"}, "p99 latency: first 198 ms, min 180 ms, max 240 ms, last 212 ms", nil},
		{
			"format",
			Sparkline{Values: []float64{0.5, 0.25}, Unit: "%", Format: func(v float64) string { return strconv.FormatFloat(v*100, 'f', 0, 64) }},
			"Sparkline: first 50%, min 25%, max 50%, last 25%", nil,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			b, err := tt.s.Render()
			if err != nil {
				t.Fatal(err)
			}
			checkSVG(t, b)
			s := string(b)
			if !strings.Contains(s, `aria-label="`+tt.label+`"`) {
				t.Errorf("aria-label: want %q in\n%s", tt.label, s)
			}
			for _, part := range tt.parts {
				if !strings.Contains(s, part) {
					t.Errorf("want %s in\n%s", part, s)
				}
			}
		})
	}
	for _, bad := range []Sparkline{{Width: 5}, {Height: math.NaN()}, {Width: -1}} {
		if _, err := bad.Render(); err == nil {
			t.Errorf("Render(%+v) succeeded", bad)
		}
		var buf bytes.Buffer
		if n, err := bad.WriteTo(&buf); err == nil || n != 0 || buf.Len() != 0 {
			t.Errorf("WriteTo(%+v) = %d, %v and wrote %d bytes", bad, n, err, buf.Len())
		}
		if _, err := bad.HTML(); err == nil {
			t.Errorf("HTML(%+v) succeeded", bad)
		}
	}
}

// TestSparklineIsNotAChart checks that a sparkline does not claim to be
// a chart: spec.md §1 and §6.4 make every svg.hp a chart, which must meet
// §3.2 (id, data-hp, tabindex, title and desc), and a sparkline has none
// of that. The stylesheet must still give it the palette and the halo on
// its end dot.
func TestSparklineIsNotAChart(t *testing.T) {
	b, err := Sparkline{Values: []float64{1, 2, 3}, Label: "p50"}.Render()
	if err != nil {
		t.Fatal(err)
	}
	root, err := parseDOM(b)
	if err != nil {
		t.Fatal(err)
	}
	if root.hasClass("hp") || !root.hasClass("hp-spark") {
		t.Errorf("sparkline root class = %q, want hp-spark and not hp", root.get("class"))
	}
	if len(query(root, "svg.hp")) != 0 {
		t.Error("a sparkline matches svg.hp")
	}
	css := DefaultTheme().CSS()
	for _, want := range []string{".hp,.hp-spark,.hp-tip,.hp-table{--hp-surface:", ".hp-spark{", ".hp-spark .hp-end{"} {
		if !strings.Contains(css, want) && !strings.Contains(css, strings.TrimSuffix(want, "{")+",") {
			t.Errorf("the stylesheet lacks %q", want)
		}
	}
}

// TestSparklineOutputs checks that WriteTo, HTML and Component all write
// what Render does, so templates need no conversion of their own.
func TestSparklineOutputs(t *testing.T) {
	s := Sparkline{Values: []float64{1, 3, 2}, Label: `<"rps">`, Unit: " rps"}
	want, err := s.Render()
	if err != nil {
		t.Fatal(err)
	}
	var buf bytes.Buffer
	if n, err := s.WriteTo(&buf); err != nil || n != int64(len(want)) || buf.String() != string(want) {
		t.Errorf("WriteTo = %d, %v, %q", n, err, buf.String())
	}
	h, err := s.HTML()
	if err != nil || string(h) != string(want) {
		t.Errorf("HTML = %q, %v", h, err)
	}
	var page bytes.Buffer
	tpl := template.Must(template.New("").Parse(`<div>{{.}}</div>`))
	if err := tpl.Execute(&page, h); err != nil || page.String() != "<div>"+string(want)+"</div>" {
		t.Errorf("html/template wrote %q, %v", page.String(), err)
	}
	buf.Reset()
	if err := s.Component().Render(context.Background(), &buf); err != nil || buf.String() != string(want) {
		t.Errorf("Component wrote %q, %v", buf.String(), err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := s.Component().Render(ctx, &buf); err == nil {
		t.Error("Component rendered with a canceled context")
	}
}

func TestThemeCSS(t *testing.T) {
	css := Stylesheet()
	for _, want := range []string{
		".hp,.hp-spark,.hp-tip,.hp-table{--hp-surface:#ffffff;",
		"--hp-series-1:#257de0;", "--hp-series-8:#ec4647;",
		`@media (prefers-color-scheme:dark){:root:where(:not([data-theme="light"])) .hp,`,
		`:root[data-theme="dark"] .hp,`, "--hp-series-1:#338ced;",
		".hp-s1{color:var(--hp-series-1)}", ".hp-s8{color:var(--hp-series-8)}",
		"prefers-reduced-motion:no-preference", ".hp-anim path",
		".htmx-request .hp,.hp.htmx-request{opacity:.6",
		".hp-tip{", ".hp-tip-v{", ".hp-tip-n{", ".hp-tip .hp-key{",
		"@media (forced-colors:active)", "CanvasText",
		".hp-table{", "tabular-nums", ".hp-sr{position:absolute;",
		".hp .hp-label,.hp .hp-rule-label{paint-order:stroke;stroke:var(--hp-surface)",
		".hp{color:var(--hp-text);max-width:100%;height:auto;overflow:visible;touch-action:pan-y pinch-zoom}",
		// Spec 1 (docs/spec.md §3.4, §6.6): the touch scrub, the brush.
		".hp .hp-brush{fill:currentColor;fill-opacity:.1;",
		`:is([hx-plot\:brush],[data-hx-plot\:brush]) .hp{cursor:crosshair}`,
		".hp .hp-brush{fill:Highlight;fill-opacity:.25;stroke:Highlight}",
	} {
		if !strings.Contains(css, want) {
			t.Errorf("stylesheet lacks %s", want)
		}
	}
	if strings.Contains(css, "<") || strings.Contains(css, "style=") {
		t.Error("stylesheet contains markup")
	}
	if strings.Count(css, "{") != strings.Count(css, "}") {
		t.Error("unbalanced braces")
	}
	// Theme values cannot escape their declaration.
	th := DefaultTheme()
	th.Light.Surface = "red;}</style><script>x{"
	if css := th.CSS(); strings.Contains(css, "</style>") || strings.Contains(css, "red;}") {
		t.Error("theme value not sanitized")
	}
}
