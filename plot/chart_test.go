package plot

import (
	"bytes"
	"context"
	"errors"
	"math"
	"reflect"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestRenderErrors(t *testing.T) {
	cats := Categories{"a", "b"}
	nine := make([]Mark, 9)
	for i := range nine {
		nine[i] = Line{X: Floats{1}, Y: []float64{1}}
	}
	tests := []struct {
		name  string
		chart Chart
		want  string // substring of the error
	}{
		{"length mismatch", Chart{Marks: []Mark{Line{Name: "p99", X: Floats{1, 2}, Y: []float64{1}}}}, `mark 0 (Line "p99"): X has 2 values but Y has 1`},
		{"nil X with Y", Chart{Marks: []Mark{Dot{Y: []float64{1}}}}, "mark 0 (Dot): X is nil but Y has 1 values"},
		{"base mismatch", Chart{Marks: []Mark{Area{X: Floats{1, 2}, Y: []float64{1, 2}, Base: []float64{0}}}}, "Base has 1 values"},
		{"labels mismatch", Chart{Marks: []Mark{Dot{X: Floats{1}, Y: []float64{1}, Labels: []string{"a", "b"}}}}, "Labels has 2 values"},
		{
			"mixed kinds",
			Chart{Marks: []Mark{Line{Name: "a", X: Floats{1}, Y: []float64{1}}, Line{Name: "b", X: Times{time.Unix(0, 0)}, Y: []float64{1}}}},
			`mark 1 (Line "b") has Times x values but mark 0 (Line "a") has Floats`,
		},
		{"empty bar", Chart{Marks: []Mark{Bar{Name: "b"}}}, "X must list at least one category"},
		{"duplicate category", Chart{Marks: []Mark{Bar{X: Categories{"a", "a"}, Y: []float64{1, 2}}}}, `category "a" appears more than once`},
		{"slot too high", Chart{Marks: []Mark{Line{X: cats, Y: []float64{1, 2}, Slot: 9}}}, "Slot 9 is outside 1–8"},
		{"slot negative", Chart{Marks: []Mark{Line{X: cats, Y: []float64{1, 2}, Slot: -1}}}, "Slot -1"},
		{"slot pinned twice", Chart{Marks: []Mark{Line{Name: "a", Slot: 3}, Line{Name: "b", Slot: 3}}}, `mark 0 (Line "a") and mark 1 (Line "b") are both pinned to Slot 3`},
		{"unknown curve", Chart{Marks: []Mark{Line{Curve: 7}}}, "unknown Curve 7"},
		{"too many series", Chart{Marks: nine}, "too many series"},
		{"too narrow", Chart{Width: 49}, "Width 49 is out of range"},
		{"negative height", Chart{Height: -10}, "Height -10 is out of range"},
		{"NaN width", Chart{Width: math.NaN()}, "Width NaN"},
		{"huge width", Chart{Width: 1e9}, "out of range"},
		{"font size", Chart{FontSize: 2}, "FontSize 2"},
		{"bar width", Chart{BarWidth: -1}, "BarWidth -1"},
		{"bad id", Chart{ID: `x") onload="`}, "ID"},
		{"nil mark", Chart{Marks: []Mark{nil}}, "mark 0 is nil"},
		{"nil pointer mark", Chart{Marks: []Mark{(*Line)(nil)}}, "mark 0 is nil"},
		{"non-finite domain", Chart{Y: Axis{Domain: [2]float64{0, math.Inf(1)}}}, "Y.Domain"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := tt.chart.Render()
			if err == nil {
				t.Fatalf("Render succeeded, want error containing %q", tt.want)
			}
			if !strings.HasPrefix(err.Error(), "hyperplot: ") {
				t.Errorf("error %q lacks the hyperplot: prefix", err)
			}
			if !strings.Contains(err.Error(), tt.want) {
				t.Errorf("error %q, want it to contain %q", err, tt.want)
			}
		})
	}
}

func TestErrTooManySeries(t *testing.T) {
	var marks []Mark
	for range 9 {
		marks = append(marks, Bar{X: Categories{"a"}, Y: []float64{1}})
	}
	_, err := (&Chart{Marks: marks}).Render()
	if !errors.Is(err, ErrTooManySeries) {
		t.Fatalf("err = %v, want ErrTooManySeries", err)
	}
	// Eight is fine.
	if _, err := (&Chart{Marks: marks[:8]}).Render(); err != nil {
		t.Fatal(err)
	}
}

func TestNilChart(t *testing.T) {
	var c *Chart
	if _, err := c.Render(); err == nil {
		t.Error("Render on nil *Chart succeeded")
	}
	if _, err := c.Table(); err == nil {
		t.Error("Table on nil *Chart succeeded")
	}
}

// TestEdgeCases renders inputs at the edges of the domain; each must give
// well-formed output without error.
func TestEdgeCases(t *testing.T) {
	inf := math.Inf(1)
	tests := []struct {
		name  string
		chart Chart
	}{
		{"zero chart", Chart{}},
		{"only a rule", Chart{Marks: []Mark{RuleY{Y: 5, Label: "x"}}}},
		{"empty line", Chart{Marks: []Mark{Line{Name: "empty"}}}},
		{"all NaN", Chart{Marks: []Mark{Line{X: Floats{1, 2, 3}, Y: []float64{nan, nan, nan}}}}},
		{"all NaN bars", Chart{Marks: []Mark{Bar{X: Categories{"a", "b"}, Y: []float64{nan, inf}}}}},
		{"single point", Chart{Marks: []Mark{Line{X: Floats{3}, Y: []float64{7}}}}},
		{"single time", Chart{Marks: []Mark{Line{X: Times{t0}, Y: []float64{7}}}}},
		{"single category", Chart{Marks: []Mark{Bar{X: Categories{"only"}, Y: []float64{7}}}}},
		{"flat line", Chart{Marks: []Mark{Line{X: Floats{1, 2, 3}, Y: []float64{5, 5, 5}}}}},
		{"all zero bars", Chart{Marks: []Mark{Bar{X: Categories{"a", "b"}, Y: []float64{0, 0}}}}},
		{"all negative", Chart{Marks: []Mark{Bar{X: Categories{"a", "b"}, Y: []float64{-3, -8}}}}},
		{"huge values", Chart{Marks: []Mark{Line{X: Floats{-1e300, 1e300}, Y: []float64{-1e300, 1e300}}}}},
		{"beyond range", Chart{Marks: []Mark{Line{X: Floats{1, 2, 3}, Y: []float64{math.MaxFloat64, -math.MaxFloat64, 1}}}}},
		{"tiny values", Chart{Marks: []Mark{Line{X: Floats{5e-324, 1e-323}, Y: []float64{5e-324, 1e-323}}}}},
		{"infinities", Chart{Marks: []Mark{Area{X: Floats{1, 2, 3}, Y: []float64{inf, -inf, 2}}}}},
		{"NaN x", Chart{Marks: []Mark{Dot{X: Floats{nan, 1}, Y: []float64{1, 2}}}}},
		{"zero time", Chart{Marks: []Mark{Line{X: Times{{}, t0}, Y: []float64{1, 2}}}}},
		{"duplicate names", Chart{Marks: []Mark{Line{Name: "a", X: Floats{1}, Y: []float64{1}}, Line{Name: "a", X: Floats{1}, Y: []float64{2}}}}},
		{"pointer marks", Chart{Marks: []Mark{&Line{X: Floats{1, 2}, Y: []float64{1, 2}}, &Area{X: Floats{1, 2}, Y: []float64{1, 2}}, &Dot{X: Floats{1}, Y: []float64{1}}, &RuleY{Y: 1}}}},
		{"min size", Chart{
			Width: 50, Height: 50, Title: "Tiny", ShowTitle: true, Y: Axis{Label: "y"}, X: Axis{Label: "x"},
			Marks: []Mark{Line{Name: "a", X: Floats{1, 2}, Y: []float64{1, 2}}, Line{Name: "b", X: Floats{1, 2}, Y: []float64{2, 1}}},
		}},
		{"hidden axes", Chart{X: Axis{Hide: true}, Y: Axis{Hide: true, NoGrid: true}, Marks: []Mark{Line{X: Floats{1, 2}, Y: []float64{1, 2}}}}},
		{"many categories", Chart{Marks: []Mark{Bar{X: manyCategories(300), Y: make([]float64, 300)}}}},
		{"unsorted x", Chart{Marks: []Mark{Line{Curve: CurveMonotone, X: Floats{3, 1, 2, 2}, Y: []float64{1, 2, 3, 4}}}}},
		{"reversed domain", Chart{Y: Axis{Domain: [2]float64{10, 0}}, Marks: []Mark{Bar{X: Categories{"a"}, Y: []float64{5}}}}},
		{"time domain", Chart{X: Axis{TimeDomain: [2]time.Time{t0.Add(time.Hour), t0}}, Marks: []Mark{Line{X: Times{t0}, Y: []float64{1}}}}},
		{"legend show one", Chart{Legend: LegendShow, Marks: []Mark{Dot{Name: "d", X: Floats{1}, Y: []float64{1}}}}},
		{"huge tick counts", Chart{X: Axis{Ticks: math.MaxInt32}, Y: Axis{Ticks: math.MaxInt32}, Marks: []Mark{Line{X: Floats{0, 1}, Y: []float64{0, 1}}}}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			b := mustRender(t, &tt.chart)
			p := checkSVG(t, b)
			if p.tips == nil {
				t.Error("no tooltip payload")
			}
			if bytes.Contains(b, []byte("NaN")) || bytes.Contains(b, []byte("Inf")) {
				t.Errorf("output contains a non-finite number:\n%s", b)
			}
			if _, err := tt.chart.Table(); err != nil {
				t.Errorf("Table: %v", err)
			}
		})
	}
}

func manyCategories(n int) Categories {
	out := make(Categories, n)
	for i := range out {
		out[i] = "category " + strconv.Itoa(i)
	}
	return out
}

func TestDuplicateNamesGetUniqueIDs(t *testing.T) {
	c := &Chart{ID: "c", Marks: []Mark{
		Bar{Name: "a", X: Categories{"x", "y"}, Y: []float64{1, 2}},
		Bar{Name: "a", X: Categories{"x", "y"}, Y: []float64{3, 4}},
		Bar{X: Categories{"x"}, Y: []float64{5}},
	}}
	p := checkSVG(t, mustRender(t, c)) // checkSVG fails on duplicate ids
	if n := p.classes["hp-bar"]; n != 5 {
		t.Errorf("got %d bars, want 5", n)
	}
}

func TestDefaultsAndRoot(t *testing.T) {
	c := &Chart{Title: "Latency", Marks: []Mark{Line{X: Floats{1, 2}, Y: []float64{1, 2}}}}
	b := string(mustRender(t, c))
	// Title, Description, X.Label, Y.Label, then kind:name per series.
	id := defaultID("Latency\x00\x00\x00\x00Line:\x00")
	for _, want := range []string{
		`<svg xmlns="http://www.w3.org/2000/svg" id="` + id + `" class="hp" viewBox="0 0 640 320" width="640" height="320" role="img"`,
		`aria-labelledby="` + id + `-title" aria-describedby="` + id + `-desc" tabindex="0" data-hp="1" data-hp-plot="`,
		`font-size="12"`,
		`<title id="` + id + `-title">Latency</title>`,
		`<clipPath id="` + id + `-clip">`,
		`clip-path="url(#` + id + `-clip)"`,
	} {
		if !strings.Contains(b, want) {
			t.Errorf("output lacks %s", want)
		}
	}
	if strings.Contains(b, "hp-legend") {
		t.Error("a single series should have no legend")
	}
	if strings.Contains(b, "hp-title") {
		t.Error("the title should be drawn only with ShowTitle or Standalone")
	}
	if hash8("") != "811c9dc5" {
		t.Errorf("hash8 is not FNV-1a: %s", hash8(""))
	}
}

func TestAnimateAndStandalone(t *testing.T) {
	c := &Chart{ID: "x", Title: "T", Animate: true, Standalone: true}
	b := string(mustRender(t, c))
	for _, want := range []string{`class="hp hp-anim"`, "<style>", `class="hp-bg"`, `class="hp-title"`, "--hp-series-1"} {
		if !strings.Contains(b, want) {
			t.Errorf("output lacks %s", want)
		}
	}
	custom := DefaultTheme()
	custom.Light.Series[0] = "#123456"
	c.Theme = &custom
	if b := string(mustRender(t, c)); !strings.Contains(b, "--hp-series-1:#123456") {
		t.Error("Standalone ignores Chart.Theme")
	}
}

func TestDOMOrder(t *testing.T) {
	c := &Chart{ID: "o", Title: "T", ShowTitle: true, Y: Axis{Label: "y"}, Marks: []Mark{
		Bar{Name: "a", X: Categories{"x"}, Y: []float64{1}},
		Bar{Name: "b", X: Categories{"x"}, Y: []float64{2}},
		RuleY{Y: 1, Label: "r"},
	}}
	b := string(mustRender(t, c))
	order := []string{
		`<title id=`, `<desc id=`, `class="hp-title"`, `<defs>`, `class="hp-legend"`, `hp-ylabel`,
		`class="hp-grid"`, `class="hp-band-hl"`, `class="hp-marks"`, `class="hp-axis hp-axis-x"`,
		`class="hp-axis hp-axis-y"`, `class="hp-rules"`, `class="hp-labels"`, `class="hp-hover"`,
		`class="hp-crosshair"`, `<metadata class="hp-tips">`,
	}
	last := -1
	for _, s := range order {
		i := strings.Index(b, s)
		if i < 0 {
			t.Fatalf("output lacks %s", s)
		}
		if i < last {
			t.Errorf("%s is out of order", s)
		}
		last = i
	}
	if !strings.Contains(b, `id="o-b-`+hash8("a")+`-`+hash8("x")+`"`) {
		t.Error("bar id is not {ID}-b-{key}-{catkey}")
	}
	if !strings.Contains(b, `id="o-s-`+hash8("b")+`" class="hp-series hp-s2" data-hp-series="b"`) {
		t.Error("series group is not {ID}-s-{key} with slot class and name")
	}
}

func TestEscaping(t *testing.T) {
	evil := `<script>alert("x")</script>&`
	c := &Chart{
		Title: evil, Description: evil, Standalone: true,
		X: Axis{Label: evil}, Y: Axis{Label: evil, Unit: evil},
		Marks: []Mark{
			Bar{Name: evil, X: Categories{evil, "b"}, Y: []float64{1, 2}},
			Line{Name: "\x00\xef\xbf\xbe" + evil, X: Categories{evil, "b"}, Y: []float64{1, 2}},
			RuleY{Y: 1, Label: evil},
		},
	}
	b := mustRender(t, c)
	checkSVG(t, b)
	if bytes.Contains(b, []byte("<script")) {
		t.Error("unescaped markup in output")
	}
	tbl, err := c.Table()
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(tbl), "<script") {
		t.Error("unescaped markup in table")
	}
}

func TestNoMutation(t *testing.T) {
	x := Floats{1, 2, 3}
	y := []float64{3, 1, 2}
	base := []float64{0, 0.5, 1}
	cats := Categories{"b", "a"}
	c := &Chart{
		Stack: true,
		Marks: []Mark{
			Area{Name: "a", X: x, Y: y, Base: base},
			Area{Name: "b", X: x, Y: y},
			Line{Name: "c", X: x, Y: y, Curve: CurveMonotone},
		},
	}
	bars := &Chart{Stack: true, Marks: []Mark{Bar{X: cats, Y: []float64{1, -1}}, Bar{X: cats, Y: []float64{2, -2}}}}
	for _, ch := range []*Chart{c, bars} {
		before := *ch
		marks := append([]Mark(nil), ch.Marks...)
		snapshot := func() []any {
			var out []any
			for _, m := range ch.Marks {
				out = append(out, deepCopyMark(m))
			}
			return out
		}
		want := snapshot()
		mustRender(t, ch)
		if _, err := ch.Table(); err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(snapshot(), want) || !reflect.DeepEqual(ch.Marks, marks) || ch.Width != before.Width || ch.ID != before.ID {
			t.Error("Render mutated the chart")
		}
	}
}

func deepCopyMark(m Mark) any {
	cp := func(v []float64) []float64 { return append([]float64(nil), v...) }
	switch m := m.(type) {
	case Area:
		m.Y, m.Base = cp(m.Y), cp(m.Base)
		m.X = append(Floats(nil), m.X.(Floats)...)
		return m
	case Line:
		m.Y = cp(m.Y)
		return m
	case Bar:
		m.Y = cp(m.Y)
		m.X = append(Categories(nil), m.X...)
		return m
	}
	return m
}

func TestDeterministicAndConcurrent(t *testing.T) {
	c := goldenCases[1].chart()
	want := mustRender(t, c)
	var wg sync.WaitGroup
	for range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for range 10 {
				got, err := c.Render()
				if err != nil || !bytes.Equal(got, want) {
					t.Error("concurrent renders differ")
					return
				}
			}
		}()
	}
	wg.Wait()
}

func TestSlotAssignment(t *testing.T) {
	c := &Chart{Marks: []Mark{
		Line{Name: "a"},
		Line{Name: "b", Slot: 1},
		Line{Name: "c"},
		Line{Name: "d", Slot: 3},
		Line{Name: "e"},
	}}
	p, err := newPlan(c)
	if err != nil {
		t.Fatal(err)
	}
	want := []int{2, 1, 4, 3, 5}
	for i, s := range p.series {
		if s.slot != want[i] {
			t.Errorf("series %s: slot %d, want %d", s.name, s.slot, want[i])
		}
	}
}

func TestStacking(t *testing.T) {
	c := &Chart{Stack: true, Marks: []Mark{
		Bar{Name: "a", X: Categories{"x", "y"}, Y: []float64{2, -1}},
		Bar{Name: "b", X: Categories{"y", "x"}, Y: []float64{-3, 5}},
		Bar{Name: "c", X: Categories{"x"}, Y: []float64{nan}},
		Area{Name: "d", X: Categories{"x", "y"}, Y: []float64{1, 1}},
		Area{Name: "e", X: Categories{"x", "y"}, Y: []float64{2, nan}},
	}}
	p, err := newPlan(c)
	if err != nil {
		t.Fatal(err)
	}
	check := func(si int, lo, hi []float64) {
		t.Helper()
		s := p.series[si]
		for i := range lo {
			if !same(s.lo[i], lo[i]) || !same(s.hi[i], hi[i]) {
				t.Errorf("series %s point %d: [%v, %v], want [%v, %v]", s.name, i, s.lo[i], s.hi[i], lo[i], hi[i])
			}
		}
	}
	check(0, []float64{0, 0}, []float64{2, -1})
	check(1, []float64{-1, 2}, []float64{-4, 7})
	check(2, []float64{nan}, []float64{nan})
	check(3, []float64{0, 0}, []float64{1, 1})
	check(4, []float64{1, nan}, []float64{3, nan})
	// Only the outermost segment in each direction is rounded.
	if r := p.series[0].round; r[0] || r[1] {
		t.Errorf("series a round = %v, want [false false]", r)
	}
	if r := p.series[1].round; !r[0] || !r[1] {
		t.Errorf("series b round = %v, want [true true]", r)
	}
}

func same(a, b float64) bool { return a == b || math.IsNaN(a) && math.IsNaN(b) }

func TestYDomain(t *testing.T) {
	tests := []struct {
		name   string
		chart  Chart
		lo, hi float64
	}{
		{"line fits data", Chart{Marks: []Mark{Line{X: Floats{1, 2}, Y: []float64{42, 61}}}}, 40, 65},
		{"bars include zero", Chart{Marks: []Mark{Bar{X: Categories{"a", "b"}, Y: []float64{42, 61}}}}, 0, 70},
		{"Zero option", Chart{Y: Axis{Zero: true}, Marks: []Mark{Line{X: Floats{1, 2}, Y: []float64{42, 61}}}}, 0, 70},
		{"negative bars", Chart{Marks: []Mark{Bar{X: Categories{"a", "b"}, Y: []float64{-3, -8}}}}, -8, 0},
		{"rule extends", Chart{Marks: []Mark{Line{X: Floats{1, 2}, Y: []float64{1, 2}}, RuleY{Y: 10}}}, 0, 10},
		{"no data", Chart{}, 0, 1},
		{"flat", Chart{Marks: []Mark{Line{X: Floats{1, 2}, Y: []float64{5, 5}}}}, 4.4, 5.6},
		{"flat zero", Chart{Marks: []Mark{Line{X: Floats{1, 2}, Y: []float64{0, 0}}}}, -1, 1},
		{"fixed", Chart{Y: Axis{Domain: [2]float64{0, 33}}, Marks: []Mark{Line{X: Floats{1, 2}, Y: []float64{5, 500}}}}, 0, 33},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p, err := newPlan(&tt.chart)
			if err != nil {
				t.Fatal(err)
			}
			if d := p.y.Domain; d[0] != tt.lo || d[1] != tt.hi {
				t.Errorf("domain %v, want [%v %v]", d, tt.lo, tt.hi)
			}
		})
	}
}

func TestDirectLabels(t *testing.T) {
	x := Floats{1, 2}
	lines := func(ends ...float64) []Mark {
		var m []Mark
		for i, e := range ends {
			m = append(m, Line{Name: string(rune('a' + i)), X: x, Y: []float64{0, e}})
		}
		return m
	}
	tests := []struct {
		name  string
		marks []Mark
		want  int
	}{
		{"one series", lines(10), 0},
		{"two apart", lines(10, 90), 2},
		{"four apart", lines(10, 40, 70, 100), 4},
		{"five series", lines(10, 30, 50, 70, 90), 0},
		{"collision", lines(10, 90, 91), 0},
		{"with bars", append(lines(10, 90), Bar{X: Categories{}, Y: nil}), 0},
		{"missing last", append(lines(10), Line{Name: "z", X: x, Y: []float64{nan, nan}}), 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c := &Chart{Marks: tt.marks}
			p, err := newPlan(c)
			if err != nil {
				if tt.want == 0 {
					return // the bars case is invalid input; fine either way
				}
				t.Fatal(err)
			}
			if len(p.direct) != tt.want {
				t.Errorf("%d direct labels, want %d", len(p.direct), tt.want)
			}
			b := mustRender(t, c)
			if got := checkSVG(t, b).classes["hp-direct"]; got != tt.want {
				t.Errorf("%d hp-direct elements, want %d", got, tt.want)
			}
		})
	}
}

func TestTips(t *testing.T) {
	t.Run("x", func(t *testing.T) {
		c := &Chart{Marks: []Mark{
			Line{Name: "a", X: Floats{1, 2, 3}, Y: []float64{1, nan, 3}},
			Line{Name: "b", X: Floats{3, 4}, Y: []float64{5, 6}},
		}, Y: Axis{Unit: " ms"}}
		tips := checkSVG(t, mustRender(t, c)).tips
		if tips["mode"] != "x" || tips["bw"] != 0.0 {
			t.Fatalf("tips = %v", tips)
		}
		xs := tips["xs"].([]any)
		rows := tips["rows"].([]any)
		if len(xs) != 3 || len(rows) != 3 {
			t.Fatalf("xs %v rows %v, want 3 each (x=2 has no value)", xs, rows)
		}
		for i := 1; i < len(xs); i++ {
			if xs[i].(float64) <= xs[i-1].(float64) {
				t.Errorf("xs not ascending: %v", xs)
			}
		}
		row := rows[1].(map[string]any) // x = 3
		if row["t"] != "3" {
			t.Errorf("row t = %v, want 3", row["t"])
		}
		v := row["v"].([]any)
		if len(v) != 2 || v[0].([]any)[2] != "3 ms" || v[1].([]any)[2] != "5 ms" || v[1].([]any)[0] != 2.0 || v[1].([]any)[1] != "b" {
			t.Errorf("row v = %v", v)
		}
		if got := rows[0].(map[string]any)["v"].([]any)[1].([]any)[2]; got != missing {
			t.Errorf("missing value shown as %v, want %s", got, missing)
		}
	})
	t.Run("band", func(t *testing.T) {
		c := &Chart{Marks: []Mark{
			Bar{Name: "a", X: Categories{"p", "q"}, Y: []float64{1, 2}},
			Bar{Name: "b", X: Categories{"q", "r"}, Y: []float64{3, 4}},
		}}
		tips := checkSVG(t, mustRender(t, c)).tips
		if tips["mode"] != "band" || tips["bw"].(float64) <= 0 {
			t.Fatalf("tips = %v", tips)
		}
		if n := len(tips["xs"].([]any)); n != 3 {
			t.Errorf("%d xs, want 3", n)
		}
		r := tips["rows"].([]any)[2].(map[string]any)
		if r["t"] != "r" || r["v"].([]any)[0].([]any)[2] != missing || r["v"].([]any)[1].([]any)[2] != "4" {
			t.Errorf("row = %v", r)
		}
	})
	t.Run("xy", func(t *testing.T) {
		c := &Chart{Marks: []Mark{Dot{Name: "d", X: Floats{1, 2}, Y: []float64{3, 4}, Labels: []string{"first", ""}}}}
		tips := checkSVG(t, mustRender(t, c)).tips
		if tips["mode"] != "xy" || len(tips["pts"].([]any)) != 2 {
			t.Fatalf("tips = %v", tips)
		}
		rows := tips["rows"].([]any)
		// A label heads the row, but keeps the x value beside it.
		if rows[0].(map[string]any)["t"] != "first · 1" || rows[1].(map[string]any)["t"] != "2" {
			t.Errorf("rows = %v", rows)
		}
	})
	// A labeled Dot beside a line (mode x) or bars (mode band) carries its
	// label after the series name, so keyboard reading announces it.
	t.Run("labels in mixed charts", func(t *testing.T) {
		for _, tt := range []struct {
			name, mode string
			marks      []Mark
		}{
			{"x", "x", []Mark{
				Line{Name: "latency", X: Floats{1, 2, 3, 4, 5}, Y: []float64{10, 20, 30, 20, 10}},
				Dot{Name: "deploys", X: Floats{3, 3}, Y: []float64{30, 31}, Labels: []string{"v2.1 rollout", "hotfix"}},
			}},
			{"band", "band", []Mark{
				Bar{Name: "reqs", X: Categories{"a", "b"}, Y: []float64{1, 2}},
				Dot{Name: "evt", X: Categories{"b"}, Y: []float64{2}, Labels: []string{"incident"}},
			}},
		} {
			tips := checkSVG(t, mustRender(t, &Chart{Marks: tt.marks})).tips
			if tips["mode"] != tt.mode {
				t.Fatalf("%s: mode %v", tt.name, tips["mode"])
			}
			rows := tips["rows"].([]any)
			got := rows[len(rows)/2].(map[string]any)["v"].([]any)[1].([]any)
			want := map[string][2]string{"x": {"deploys · v2.1 rollout, hotfix", "30, 31"}, "band": {"evt · incident", "2"}}[tt.name]
			if got[1] != want[0] || got[2] != want[1] {
				t.Errorf("%s: entry %v, want name %q and value %q", tt.name, got, want[0], want[1])
			}
		}
	})
}

func TestXLabelsStayInBounds(t *testing.T) {
	cats := make(Categories, 60)
	for i := range cats {
		cats[i] = "Category number " + string(rune('A'+i%26)) + string(rune('a'+i/26))
	}
	c := &Chart{Width: 400, Marks: []Mark{Bar{X: cats, Y: make([]float64, len(cats))}}}
	p, err := newPlan(c)
	if err != nil {
		t.Fatal(err)
	}
	if len(p.xTicks) >= len(cats) || len(p.xTicks) == 0 {
		t.Fatalf("%d labels for %d categories: not thinned", len(p.xTicks), len(cats))
	}
	for i, tk := range p.xTicks {
		x0, x1 := tk.extent()
		if x0 < p.cl-0.01 || x1 > p.cr+0.01 {
			t.Errorf("label %q extends to [%v, %v], outside [%v, %v]", tk.text, x0, x1, p.cl, p.cr)
		}
		if i > 0 {
			_, prev := p.xTicks[i-1].extent()
			if x0-prev < p.f {
				t.Errorf("labels %d and %d are %v apart", i-1, i, x0-prev)
			}
		}
	}
}

func TestLegendWraps(t *testing.T) {
	var marks []Mark
	for i := range 8 {
		marks = append(marks, Line{Name: strings.Repeat("series ", i+1), X: Floats{1}, Y: []float64{1}})
	}
	p, err := newPlan(&Chart{Width: 300, Marks: marks})
	if err != nil {
		t.Fatal(err)
	}
	rows := map[float64]bool{}
	for _, it := range p.legend {
		rows[it.y] = true
		if it.x < p.cl || it.x > p.cr {
			t.Errorf("legend item %q at x=%v outside the chart", it.text, it.x)
		}
	}
	if len(rows) < 2 {
		t.Errorf("legend did not wrap: %d row(s)", len(rows))
	}
	if p.t <= float64(len(rows))*(p.f+6) {
		t.Errorf("plot top %v overlaps %d legend rows", p.t, len(rows))
	}
}

func TestLongNameTruncated(t *testing.T) {
	long := strings.Repeat("very long name ", 40)
	c := &Chart{Marks: []Mark{
		Line{Name: long, X: Floats{1, 2}, Y: []float64{1, 2}},
		Line{Name: "b", X: Floats{1, 2}, Y: []float64{9, 1}},
	}}
	p := checkSVG(t, mustRender(t, c))
	for _, s := range p.texts {
		if s == long {
			t.Error("long name drawn untruncated")
		}
	}
}

func TestWriteToHTMLComponent(t *testing.T) {
	c := goldenCases[0].chart()
	want := mustRender(t, c)
	var buf bytes.Buffer
	n, err := c.WriteTo(&buf)
	if err != nil || n != int64(len(want)) || !bytes.Equal(buf.Bytes(), want) {
		t.Errorf("WriteTo = %d, %v", n, err)
	}
	h, err := c.HTML()
	if err != nil || string(h) != string(want) {
		t.Errorf("HTML differs: %v", err)
	}
	buf.Reset()
	if err := c.Component().Render(context.Background(), &buf); err != nil || !bytes.Equal(buf.Bytes(), want) {
		t.Errorf("Component.Render: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := c.Component().Render(ctx, &buf); !errors.Is(err, context.Canceled) {
		t.Errorf("canceled context: err = %v", err)
	}
	bad := &Chart{Width: 1}
	if n, err := bad.WriteTo(&buf); err == nil || n != 0 {
		t.Errorf("WriteTo with invalid chart = %d, %v", n, err)
	}
	if _, err := bad.HTML(); err == nil {
		t.Error("HTML with invalid chart succeeded")
	}
}

func TestDescription(t *testing.T) {
	c := &Chart{
		Y: Axis{Label: "Latency", Unit: " ms"},
		Marks: []Mark{
			Line{Name: "p50", X: Times{t0, t0.Add(time.Hour)}, Y: []float64{10, 100}},
			Line{Name: "p99", X: Times{t0, t0.Add(time.Hour)}, Y: []float64{20, 110}},
			RuleY{Y: 50, Label: "SLO"},
		},
	}
	p, err := newPlan(c)
	if err != nil {
		t.Fatal(err)
	}
	want := "Line chart with 2 series: p50, p99. X axis time from Mar 3, 2026 2:00 PM to Mar 3, 2026 3:00 PM. Y axis Latency, 0 to 120 ms. Reference line SLO at 50 ms."
	if got := p.description(); got != want {
		t.Errorf("description:\n got %s\nwant %s", got, want)
	}
	for _, tt := range []struct {
		marks []Mark
		stack bool
		want  string
	}{
		{nil, false, "Empty chart."},
		{[]Mark{Bar{X: Categories{"a", "b"}, Y: []float64{1, 2}}}, true, "Stacked bar chart of Series 1. X axis 2 categories from a to b."},
		{[]Mark{Dot{Name: "d", X: Floats{1, 2}, Y: []float64{1, 2}}}, false, "Scatter plot of d. X axis 1 to 2."},
		{[]Mark{Bar{X: Categories{"a"}, Y: []float64{1}}, Line{X: Categories{"a"}, Y: []float64{1}}}, false, "Bar and line chart with 2 series"},
	} {
		p, err := newPlan(&Chart{Marks: tt.marks, Stack: tt.stack})
		if err != nil {
			t.Fatal(err)
		}
		if got := p.description(); !strings.HasPrefix(got, tt.want) {
			t.Errorf("description %q, want prefix %q", got, tt.want)
		}
	}
	custom := &Chart{Description: "Custom."}
	if b := mustRender(t, custom); !bytes.Contains(b, []byte(">Custom.</desc>")) {
		t.Error("Description not used")
	}
}
