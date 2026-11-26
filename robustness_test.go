package hyperplot

import (
	"math/rand/v2"
	"strconv"
	"strings"
	"testing"
	"time"
)

// Tests for inputs at the edges of what charts are built for: crowded
// headers, fixed domains that exclude data, repeated x values, huge or
// tiny magnitudes, long names, many labeled points and hostile themes.

// TestHeaderLeavesRoomForThePlot checks that the title, legend and Y
// label rows never push the plot off a short or narrow chart.
func TestHeaderLeavesRoomForThePlot(t *testing.T) {
	marks := make([]Mark, 8)
	for i := range marks {
		marks[i] = Line{Name: "eu-central-" + strconv.Itoa(i+1), X: Floats{1, 2}, Y: []float64{1, float64(i)}}
	}
	tests := []struct {
		w, h       float64
		standalone bool
		legend     bool // whether the legend still fits
	}{
		{640, 320, false, true},
		{600, 300, false, true},
		{300, 150, false, false},
		{200, 150, false, false},
		{50, 50, false, false},
		{300, 150, true, false},
	}
	for _, tt := range tests {
		c := &Chart{
			Title: "Requests", ShowTitle: true, Standalone: tt.standalone,
			Width: tt.w, Height: tt.h, Y: Axis{Label: "req/s"}, Marks: marks,
		}
		p, err := newPlan(c)
		if err != nil {
			t.Fatal(err)
		}
		name := strconv.FormatFloat(tt.w, 'f', -1, 64) + "x" + strconv.FormatFloat(tt.h, 'f', -1, 64)
		bottom := p.cb - (p.f + 10) // the X tick row
		if minPlot := (bottom - p.ct) / 2; p.b-p.t < minPlot-1 {
			t.Errorf("%s: plot is %.1f px tall, want at least %.1f", name, p.b-p.t, minPlot)
		}
		if p.t < p.ct || p.b > p.cb {
			t.Errorf("%s: plot [%v, %v] leaves the content box [%v, %v]", name, p.t, p.b, p.ct, p.cb)
		}
		if p.showLegend != tt.legend {
			t.Errorf("%s: legend shown = %v, want %v", name, p.showLegend, tt.legend)
		}
		checkSVG(t, mustRender(t, c))
	}
}

// TestDirectLabelsNeedTheLastPointInsideX checks that direct labels are
// dropped (all of them, with their margin) when a fixed X domain leaves
// a series' last point outside the plot.
func TestDirectLabelsNeedTheLastPointInsideX(t *testing.T) {
	t0 := time.Date(2026, 3, 3, 14, 0, 0, 0, time.UTC)
	tests := []struct {
		name string
		c    *Chart
	}{
		{"fixed Domain", &Chart{
			X: Axis{Domain: [2]float64{0, 5}},
			Marks: []Mark{
				Line{Name: "alpha", X: Floats{1, 2, 10}, Y: []float64{1, 2, 3}},
				Line{Name: "beta", X: Floats{1, 2, 10}, Y: []float64{5, 6, 9}},
			},
		}},
		{"sliding TimeDomain", &Chart{
			X: Axis{TimeDomain: [2]time.Time{t0, t0.Add(2 * time.Minute)}},
			Marks: []Mark{
				Line{Name: "alpha", X: Times{t0, t0.Add(time.Minute), t0.Add(125 * time.Second)}, Y: []float64{1, 2, 3}},
				Line{Name: "beta", X: Times{t0, t0.Add(time.Minute), t0.Add(125 * time.Second)}, Y: []float64{5, 6, 9}},
			},
		}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p := checkSVG(t, mustRender(t, tt.c))
			if n := p.classes["hp-direct"]; n != 0 {
				t.Errorf("drew %d direct labels for points outside the plot", n)
			}
			plan, err := newPlan(tt.c)
			if err != nil {
				t.Fatal(err)
			}
			if right := plan.cr - plan.r; right > 2*minRight {
				t.Errorf("right margin %.1f px is reserved for labels that are not drawn", right)
			}
		})
	}
	// Inside the domain, labels stay.
	c := &Chart{
		X: Axis{Domain: [2]float64{0, 10}},
		Marks: []Mark{
			Line{Name: "alpha", X: Floats{1, 2, 10}, Y: []float64{1, 2, 3}},
			Line{Name: "beta", X: Floats{1, 2, 10}, Y: []float64{5, 6, 9}},
		},
	}
	if n := checkSVG(t, mustRender(t, c)).classes["hp-direct"]; n != 2 {
		t.Errorf("got %d direct labels, want 2", n)
	}
}

// TestRepeatedXValuesAreListed checks that every point a series has at
// one x reaches the tooltip and the table, not only the first.
func TestRepeatedXValuesAreListed(t *testing.T) {
	c := &Chart{Marks: []Mark{
		Line{Name: "mean", X: Floats{1, 2}, Y: []float64{10, 20}},
		Dot{Name: "samples", X: Floats{1, 1, 1, 2, 2}, Y: []float64{8, 10, 12, 18, 22}, Labels: []string{"a", "", "c", "d", "e"}},
	}}
	tbl, err := c.Table()
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		`<tr><td>a, c</td><th scope="row">1</th><td>10</td><td>8, 10, 12</td></tr>`,
		`<tr><td>d, e</td><th scope="row">2</th><td>20</td><td>18, 22</td></tr>`,
	} {
		if !strings.Contains(string(tbl), want) {
			t.Errorf("table lacks %s\n%s", want, tbl)
		}
	}
	p := checkSVG(t, mustRender(t, c))
	rows := p.tips["rows"].([]any)
	v := rows[0].(map[string]any)["v"].([]any)[1].([]any)
	if v[2] != "8, 10, 12" {
		t.Errorf("tooltip value at x=1 = %v, want all three samples", v[2])
	}
	if p.classes["hp-dot"] != 5 {
		t.Errorf("drew %d dots, want 5", p.classes["hp-dot"])
	}
}

// TestHugeAndTinyValuesStayShort checks tick labels and tooltip values
// for magnitudes beyond the K/M/B/T suffixes and below 1e-4.
func TestHugeAndTinyValuesStayShort(t *testing.T) {
	tests := []struct {
		name string
		c    *Chart
	}{
		{"exabytes", &Chart{Marks: []Mark{Line{X: Floats{1, 2}, Y: []float64{1e18, 3e18}}}}},
		{"fixed ±1e300", &Chart{Y: Axis{Domain: [2]float64{-1e300, 1e300}}, Marks: []Mark{Line{X: Floats{1, 2}, Y: []float64{1, 2}}}}},
		{"1e-10", &Chart{Marks: []Mark{Line{X: Floats{1, 2}, Y: []float64{1e-10, 5e-10}}}}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p, err := newPlan(tt.c)
			if err != nil {
				t.Fatal(err)
			}
			seen := map[string]bool{}
			for _, tk := range p.yTicks {
				if seen[tk.text] {
					t.Errorf("two Y ticks read %q", tk.text)
				}
				seen[tk.text] = true
				if tk.w > 80 {
					t.Errorf("Y tick %q is %.0f px wide", tk.text, tk.w)
				}
			}
			if p.l-p.cl > 90 {
				t.Errorf("left margin %.0f px", p.l-p.cl)
			}
		})
	}
}

// TestLongNamesDoNotMultiplyOutput checks that a long series name costs
// about its own length once, not once per point.
func TestLongNamesDoNotMultiplyOutput(t *testing.T) {
	const n = 500
	xs, ys := make(Floats, n), make([]float64, n)
	for i := range xs {
		xs[i], ys[i] = float64(i), float64(i%7)
	}
	size := func(name string, dot bool) int {
		var m Mark = Line{Name: name, X: xs, Y: ys}
		if dot {
			m = Dot{Name: name, X: xs, Y: ys}
		}
		return len(mustRender(t, &Chart{ID: "c", Marks: []Mark{m, Line{Name: "b", X: xs, Y: ys}}}))
	}
	for _, dot := range []bool{false, true} {
		short, long := size("a", dot), size(strings.Repeat("A", 64<<10), dot)
		// The full name appears a few times (data-hp-series, the legend's
		// <title>, the description); per-point copies are capped.
		if grow := long - short; grow > 8*(64<<10)+n*2*maxTipName*3 {
			t.Errorf("dot=%v: a 64 KiB name grew the output by %d bytes", dot, grow)
		}
	}
	if got := tipName(strings.Repeat("é", 300)); len([]rune(got)) != maxTipName || !strings.HasSuffix(got, "…") {
		t.Errorf("tipName kept %d runes", len([]rune(got)))
	}
	if got := tipName("short"); got != "short" {
		t.Errorf("tipName(short) = %q", got)
	}
}

// TestBoxGridMatchesLinearScan checks the label collision index against
// a plain scan over every placed box: it never calls an occupied box
// free, and it agrees exactly unless the box touches a full cell.
func TestBoxGridMatchesLinearScan(t *testing.T) {
	r := rand.New(rand.NewPCG(1, 2))
	for _, area := range []box{{0, 0, 600, 300}, {-10, 5, 4096, 4096}, {0, 0, 1, 1}} {
		g := newBoxGrid(area, 12)
		var all []box
		rnd := func() box {
			x := area.x0 - 50 + r.Float64()*(area.x1-area.x0+100)
			y := area.y0 - 50 + r.Float64()*(area.y1-area.y0+100)
			return box{x, y, x + r.Float64()*80, y + r.Float64()*20}
		}
		for range 2000 {
			b := rnd()
			free := true
			for _, o := range all {
				if b.overlaps(o) {
					free = false
					break
				}
			}
			full := false
			c0, c1, r0, r1 := g.span(b)
			for r := r0; r <= r1; r++ {
				for c := c0; c <= c1; c++ {
					full = full || len(g.cells[r*g.cols+c]) >= maxCellBoxes
				}
			}
			// A full cell may make the grid refuse a free box, never
			// accept an occupied one.
			if got := g.free(b); got != free && (got || !full) {
				t.Fatalf("area %v: free(%v) = %v, want %v", area, b, got, free)
			}
			if r.IntN(2) == 0 {
				g.add(b)
				all = append(all, b)
			}
		}
	}
}

// TestDotLabelPlacementWork guards label placement against quadratic
// time. It runs the placement the renderer uses and counts the box
// comparisons its collision grid makes, so it does not depend on the
// machine's speed: per labeled dot the count must stay small whether the
// dots are spread over a large chart or piled on one spot (80,000 dots at
// one point took 15 s when every label scanned the whole pile).
func TestDotLabelPlacementWork(t *testing.T) {
	piled := func(n int, jitter float64) *Chart {
		xs, ys, labels := make(Floats, n+2), make([]float64, n+2), make([]string, n+2)
		for i := range n {
			xs[i], ys[i], labels[i] = 1+float64(i)*jitter, 1, "a"
		}
		xs[n], ys[n] = 0, 0 // keep the pile mid-plot
		xs[n+1], ys[n+1] = 2, 2
		return &Chart{ID: "pile", Marks: []Mark{Dot{X: xs, Y: ys, Labels: labels}}}
	}
	tests := []struct {
		name string
		c    *Chart
		n    int
	}{
		{"sparse on a large chart", sparseLabeledDots(20_000), 20_000},
		{"piled on one point", piled(80_000, 0), 80_000},
		{"piled within a pixel", piled(80_000, 1e-7), 80_000},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p, err := newPlan(tt.c)
			if err != nil {
				t.Fatal(err)
			}
			labels, g := p.placeDotLabels()
			if g == nil || len(labels) == 0 {
				t.Fatalf("placed %d labels", len(labels))
			}
			// Each label tries at most 4 positions, each touching a few
			// cells of at most maxCellBoxes boxes; a quadratic placement
			// makes tens of thousands of comparisons per label here.
			if avg := float64(g.checks) / float64(tt.n); avg > 200 {
				t.Errorf("placement made %.0f box comparisons per labeled dot", avg)
			}
		})
	}
	checkSVG(t, mustRender(t, sparseLabeledDots(20_000)))
}

func sparseLabeledDots(n int) *Chart {
	xs, ys, labels := make(Floats, n), make([]float64, n), make([]string, n)
	for i := range xs {
		xs[i], ys[i], labels[i] = float64(i%997), float64((i*7919)%1009), "x"
	}
	return &Chart{ID: "dots", Width: 4096, Height: 4096, Marks: []Mark{Dot{X: xs, Y: ys, Labels: labels}}}
}

func BenchmarkDotLabels50kAt4096(b *testing.B) {
	c := sparseLabeledDots(50_000)
	for b.Loop() {
		if _, err := c.Render(); err != nil {
			b.Fatal(err)
		}
	}
}

// TestDefaultIDsDiffer checks that the default id tells apart charts
// that differ in title, labels or series, and stays put as data and size
// change.
func TestDefaultIDsDiffer(t *testing.T) {
	line := func(name string, ys ...float64) Mark { return Line{Name: name, X: Floats{1, 2}, Y: ys} }
	id := func(c *Chart) string {
		p, err := newPlan(c)
		if err != nil {
			t.Fatal(err)
		}
		return p.id
	}
	base := id(&Chart{Title: "Latency", Marks: []Mark{line("p50", 1, 2)}})
	same := []*Chart{
		{Title: "Latency", Width: 800, Marks: []Mark{line("p50", 5, 9)}},
		{Title: "Latency", Height: 200, Animate: true, Marks: []Mark{line("p50", 1, 2)}},
	}
	for _, c := range same {
		if got := id(c); got != base {
			t.Errorf("id changed with data or size: %s vs %s", got, base)
		}
	}
	differ := []*Chart{
		{Title: "Errors", Marks: []Mark{line("p50", 1, 2)}},
		{Title: "Latency", Marks: []Mark{line("p99", 1, 2)}},
		{Title: "Latency", Marks: []Mark{Dot{Name: "p50", X: Floats{1}, Y: []float64{1}}}},
		{Title: "Latency", Y: Axis{Label: "ms"}, Marks: []Mark{line("p50", 1, 2)}},
		{Title: "Latency", Marks: []Mark{line("p50", 1, 2), line("p90", 1, 2)}},
	}
	seen := map[string]bool{base: true}
	for _, c := range differ {
		got := id(c)
		if seen[got] {
			t.Errorf("default id %s repeats for %+v", got, c)
		}
		seen[got] = true
	}
	if got := id(&Chart{ID: "mine", Title: "Latency"}); got != "mine" {
		t.Errorf("explicit ID = %q", got)
	}
}

// TestDescriptionOfEmptyChart checks that a chart with series but no
// values says so instead of describing fallback axis ranges.
func TestDescriptionOfEmptyChart(t *testing.T) {
	t0 := time.Date(2026, 3, 3, 14, 0, 0, 0, time.UTC)
	tests := []struct {
		marks []Mark
		want  string
	}{
		{[]Mark{Line{Name: "p50", X: Times{}, Y: []float64{}}}, "Line chart of p50. No data."},
		{
			[]Mark{Line{Name: "p50", X: Times{t0, t0.Add(time.Hour)}, Y: []float64{nan, nan}}},
			"Line chart of p50. No data. X axis time from Mar 3, 2026 2:00 PM to Mar 3, 2026 3:00 PM.",
		},
		{[]Mark{Bar{Name: "b", X: Categories{"a"}, Y: []float64{nan}}}, "Bar chart of b. No data. X axis 1 category: a."},
	}
	for _, tt := range tests {
		p, err := newPlan(&Chart{Marks: tt.marks})
		if err != nil {
			t.Fatal(err)
		}
		if got := p.description(); got != tt.want {
			t.Errorf("description:\n got %s\nwant %s", got, tt.want)
		}
	}
}

// TestThemeColorsCannotFetch checks that theme values that are not CSS
// colors, notably url() and image-set(), fall back to the default.
func TestThemeColorsCannotFetch(t *testing.T) {
	def := DefaultTheme()
	tests := []struct {
		in, want string
	}{
		{"#abc", "#abc"},
		{"#AABBCCDD", "#AABBCCDD"},
		{"rebeccapurple", "rebeccapurple"},
		{"currentColor", "currentColor"},
		{"rgb(37 125 224 / 50%)", "rgb(37 125 224 / 50%)"},
		{"color-mix(in srgb, var(--brand) 40%, white)", "color-mix(in srgb, var(--brand) 40%, white)"},
		{"oklch(0.7 0.1 calc(120 + 5))", "oklch(0.7 0.1 calc(120 + 5))"},
		// Hex colors are the usual arguments of var() fallbacks,
		// color-mix() and light-dark().
		{"var(--brand, #0b6bcb)", "var(--brand, #0b6bcb)"},
		{"light-dark(#fff, #000)", "light-dark(#fff, #000)"},
		{"color-mix(in srgb, #0b6bcb 80%, white)", "color-mix(in srgb, #0b6bcb 80%, white)"},
		{"rgb(from #0b6bcb r g b / 50%)", "rgb(from #0b6bcb r g b / 50%)"},
		{"var(--brand, #0b6bcb80)", "var(--brand, #0b6bcb80)"},
		{"var(--brand, #0b6bc)", def.Light.Surface},
		{"var(--brand, #0b6bcbz)", def.Light.Surface},
		{"var(--brand, #)", def.Light.Surface},
		{"#fff #000", def.Light.Surface},
		{"red #fff", def.Light.Surface},
		{"var(--a, #fff", def.Light.Surface},
		{"url(//attacker.example/beacon)", def.Light.Surface},
		{"url(/beacon-same-origin)", def.Light.Surface},
		{"rgb(1 2 3), url(/x)", def.Light.Surface},
		{"image-set(x 1x)", def.Light.Surface},
		{"rgb(var(--x), image(x))", def.Light.Surface},
		{"#ggg", def.Light.Surface},
		{"#12345", def.Light.Surface},
		{"red blue", def.Light.Surface},
		{"rgb(1 2 3", def.Light.Surface},
		{"", def.Light.Surface},
	}
	for _, tt := range tests {
		th := def
		th.Light.Surface = tt.in
		th.Dark.Surface = tt.in
		css := th.CSS()
		if !strings.Contains(css, "--hp-surface:"+tt.want+";") {
			t.Errorf("Surface %q: want --hp-surface:%s", tt.in, tt.want)
		}
		if strings.Contains(css, "url(") || strings.Contains(css, "image") {
			t.Errorf("Surface %q reached the stylesheet", tt.in)
		}
	}
}

// TestForcedColorsKeepSeriesAndTooltipText checks the forced-colors
// rules: series keep their colors on marks and keys, and tooltip text
// uses the system text color.
func TestForcedColorsKeepSeriesAndTooltipText(t *testing.T) {
	css := Stylesheet()
	_, forced, ok := strings.Cut(css, "@media (forced-colors:active){")
	if !ok {
		t.Fatal("no forced-colors block")
	}
	for _, want := range []string{
		".hp .hp-marks,.hp .hp-key,.hp .hp-end{forced-color-adjust:none}",
		".hp-tip-t,.hp-tip-v,.hp-tip-n{color:CanvasText}",
	} {
		if !strings.Contains(forced, want) {
			t.Errorf("forced-colors block lacks %s", want)
		}
	}
	if strings.Contains(forced, ".hp-s1") {
		t.Error("forced colors must not flatten the series colors")
	}
	// In band mode the highlight is the only mark of the active category,
	// so it must be clearly visible against Canvas, not a 6% wash.
	i := strings.Index(forced, ".hp .hp-band-hl{")
	if i < 0 {
		t.Fatal("forced-colors block does not restyle the band highlight")
	}
	rule, _, _ := strings.Cut(forced[i:], "}")
	if !strings.Contains(rule, "stroke:Highlight") || !strings.Contains(rule, "fill-opacity:.25") {
		t.Errorf("forced-colors band highlight %s: want a Highlight outline and a visible wash", rule)
	}
}

// TestStatusColorsDifferFromSeries pins the status-color rule in
// docs/DESIGN.md: no series token equals a documented status color,
// except the one documented exception, dark series 6, which is the same
// green as status good.
func TestStatusColorsDifferFromSeries(t *testing.T) {
	status := map[string]string{"#0aac23": "good", "#fbb315": "warning", "#f18a59": "serious", "#dc3c3d": "critical"}
	th := DefaultTheme()
	for mode, pl := range map[string]Palette{"light": th.Light, "dark": th.Dark} {
		for i, c := range pl.Series {
			role, clash := status[strings.ToLower(c)]
			if clash && (mode != "dark" || i != 5 || role != "good") {
				t.Errorf("%s series %d is %s, the status %s color", mode, i+1, c, role)
			}
		}
	}
	if th.Dark.Series[5] != "#0aac23" {
		t.Error("dark series 6 changed: update the status-color note in docs/DESIGN.md")
	}
}

// TestTooltipContentWraps checks the tooltip rules that keep long content
// inside the card: headings and values wrap, and the series name keeps a
// width of its own instead of shrinking to nothing.
func TestTooltipContentWraps(t *testing.T) {
	css := Stylesheet()
	rule := func(sel string) string {
		i := strings.Index(css, "\n"+sel+"{")
		if i < 0 {
			t.Fatalf("no %s rule", sel)
		}
		r, _, _ := strings.Cut(css[i+1:], "}")
		return r
	}
	for sel, wants := range map[string][]string{
		".hp-tip-t":   {"overflow-wrap:anywhere"},
		".hp-tip-row": {"display:flex"},
		".hp-tip-v":   {"min-width:0", "overflow-wrap:anywhere"},
		".hp-tip-n":   {"flex:none", "max-width:60%", "text-overflow:ellipsis"},
	} {
		r := rule(sel)
		for _, want := range wants {
			if !strings.Contains(r, want) {
				t.Errorf("%s lacks %s", r, want)
			}
		}
		if sel != ".hp-tip-n" && strings.Contains(r, "nowrap") {
			t.Errorf("%s must let its text wrap", r)
		}
	}
}

// TestRefetchDimIsDelayed checks that a refetch dims the chart only after
// a delay, so fast polls do not make it pulse.
func TestRefetchDimIsDelayed(t *testing.T) {
	if !strings.Contains(Stylesheet(), ".htmx-request .hp,.hp.htmx-request{opacity:.6;transition:opacity .2s .5s}") {
		t.Error("the refetch dim should wait half a second")
	}
}

// TestSubSecondTimes checks that data sampled faster than once a second
// gets several ticks and distinct tooltip headings.
func TestSubSecondTimes(t *testing.T) {
	t0 := time.Date(2026, 3, 3, 14, 0, 0, 0, time.UTC)
	xs := make(Times, 5)
	for i := range xs {
		xs[i] = t0.Add(time.Duration(i) * 100 * time.Millisecond)
	}
	p := checkSVG(t, mustRender(t, &Chart{Marks: []Mark{Line{X: xs, Y: []float64{1, 2, 3, 2, 1}}}}))
	if n := p.classes["hp-tick"]; n < 3+3 { // Y ticks plus at least 3 X ticks
		t.Errorf("only %d tick labels", n)
	}
	seen := map[string]bool{}
	for _, r := range p.tips["rows"].([]any) {
		head := r.(map[string]any)["t"].(string)
		if seen[head] {
			t.Errorf("two tooltips read %q", head)
		}
		seen[head] = true
	}
	if !seen["Mar 3, 2026 2:00:00.100 PM"] {
		t.Errorf("tooltip headings %v lack milliseconds", seen)
	}
}
