package plot

import (
	"math"
	"strings"
	"testing"
	"time"
)

// tipRows returns the headings of a rendered chart's tooltip rows.
func tipRows(t *testing.T, c *Chart) []string {
	t.Helper()
	var heads []string
	for _, r := range checkSVG(t, mustRender(t, c)).tips["rows"].([]any) {
		heads = append(heads, r.(map[string]any)["t"].(string))
	}
	return heads
}

// tableRowHeads returns the row headers of a chart's data table.
func tableRowHeads(t *testing.T, c *Chart) []string {
	t.Helper()
	tbl, err := c.Table()
	if err != nil {
		t.Fatal(err)
	}
	var heads []string
	for _, part := range strings.Split(string(tbl), `<th scope="row">`)[1:] {
		head, _, _ := strings.Cut(part, "</th>")
		heads = append(heads, head)
	}
	return heads
}

func description(t *testing.T, c *Chart) string {
	t.Helper()
	p, err := newPlan(c)
	if err != nil {
		t.Fatal(err)
	}
	return p.description()
}

// TestDistinctXValuesReadApart checks that distinct numeric x values head
// distinct tooltip and table rows. Four significant digits suit y values,
// but made four rows of "100K" here.
func TestDistinctXValuesReadApart(t *testing.T) {
	tenth := 0.1 // a variable, so 0.1 + 0.2 is float64 arithmetic, not an exact constant
	tests := []struct {
		name  string
		x     Floats
		heads []string
		desc  string
	}{
		{"sample indices", Floats{100000, 100001, 100002, 100003}, []string{"100,000", "100,001", "100,002", "100,003"}, "X axis 100,000 to 100,003."},
		{"fine steps", Floats{1001.2, 1001.4, 1001.6}, []string{"1,001.2", "1,001.4", "1,001.6"}, "X axis 1,001.2 to 1,001.6."},
		{"usual values keep the short form", Floats{0, 2.5, 5, 7.5}, []string{"0", "2.5", "5", "7.5"}, "X axis 0 to 8."},
		{"float noise", Floats{0.3, tenth + 0.2}, []string{"0.3", "0.30000000000000004"}, ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c := &Chart{Marks: []Mark{Line{X: tt.x, Y: make([]float64, len(tt.x))}}}
			if got := tipRows(t, c); strings.Join(got, "|") != strings.Join(tt.heads, "|") {
				t.Errorf("tooltip rows %q, want %q", got, tt.heads)
			}
			if got := tableRowHeads(t, c); strings.Join(got, "|") != strings.Join(tt.heads, "|") {
				t.Errorf("table rows %q, want %q", got, tt.heads)
			}
			if d := description(t, c); tt.desc != "" && !strings.Contains(d, tt.desc) {
				t.Errorf("description %q lacks %q", d, tt.desc)
			}
		})
	}
	// Y end points in the description differ when the domain does.
	c := &Chart{Marks: []Mark{Line{X: Floats{0, 1}, Y: []float64{100000, 100003}}}}
	if d := description(t, c); !strings.Contains(d, "Y axis 100,000 to 100,003.") {
		t.Errorf("description %q: Y end points read alike", d)
	}
}

// TestLongUnitsDoNotMultiplyOutput checks that a long Axis.Unit costs
// about its own length a few times, not once per value.
func TestLongUnitsDoNotMultiplyOutput(t *testing.T) {
	const n = 1000
	xs, ys := make(Floats, n), make([]float64, n)
	for i := range xs {
		xs[i], ys[i] = float64(i), float64(i)
	}
	long := strings.Repeat("u", 64<<10)
	size := func(xu, yu string) (int, int) {
		c := &Chart{ID: "c", X: Axis{Unit: xu}, Y: Axis{Unit: yu}, Marks: []Mark{Dot{X: xs, Y: ys}, Line{X: xs, Y: ys}}}
		tbl, err := c.Table()
		if err != nil {
			t.Fatal(err)
		}
		return len(mustRender(t, c)), len(tbl)
	}
	svg0, tbl0 := size(" ms", " ms")
	svg1, tbl1 := size(long, long)
	// The description names each unit in full once; per-value copies are
	// shortened to maxUnit runes.
	if grow := svg1 - svg0; grow > 4*len(long)+6*n*maxUnit {
		t.Errorf("64 KiB units grew the SVG by %d bytes", grow)
	}
	if grow := tbl1 - tbl0; grow > 4*n*maxUnit {
		t.Errorf("64 KiB units grew the table by %d bytes", grow)
	}
	p, err := newPlan(&Chart{Y: Axis{Unit: long}, Marks: []Mark{Line{X: xs, Y: ys}}})
	if err != nil {
		t.Fatal(err)
	}
	if v := p.yValue(1); len([]rune(v)) != 1+maxUnit || !strings.HasSuffix(v, "…") {
		t.Errorf("yValue kept %d runes of the unit", len([]rune(v))-1)
	}
	if !strings.Contains(p.description(), long) {
		t.Error("the description must carry the unit in full")
	}
}

// TestStackedAreaRepeatedX checks that a stacked area with two points at
// one x (a vertical step) stacks both on the earlier series only, not the
// second on the first, and that a later series stacks on the last value.
func TestStackedAreaRepeatedX(t *testing.T) {
	c := &Chart{Stack: true, Marks: []Mark{
		Area{Name: "a", X: Floats{0, 1, 1, 2}, Y: []float64{1, 1, 5, 5}},
		Area{Name: "b", X: Floats{1}, Y: []float64{1}},
	}}
	p, err := newPlan(c)
	if err != nil {
		t.Fatal(err)
	}
	a, b := p.series[0], p.series[1]
	if want := []float64{1, 1, 5, 5}; !equalFloats(a.hi, want) {
		t.Errorf("a.hi = %v, want %v", a.hi, want)
	}
	if want := []float64{0, 0, 0, 0}; !equalFloats(a.lo, want) {
		t.Errorf("a.lo = %v, want %v", a.lo, want)
	}
	if b.lo[0] != 5 || b.hi[0] != 6 {
		t.Errorf("b stacks on %v up to %v, want 5 to 6", b.lo[0], b.hi[0])
	}
	if p.yHi != 6 {
		t.Errorf("y extent reaches %v, want 6", p.yHi)
	}
}

func equalFloats(a, b []float64) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// TestSinglePointTimeLabels checks the first sample of a live chart: a
// zero-span time domain is labeled at the precision its time needs, and
// described as an instant.
func TestSinglePointTimeLabels(t *testing.T) {
	t0 := time.Date(2026, 3, 3, 14, 0, 0, 0, time.UTC)
	c := &Chart{Marks: []Mark{Line{X: Times{t0}, Y: []float64{1}}}}
	if got := tipRows(t, c); len(got) != 1 || got[0] != "Mar 3, 2026 2:00 PM" {
		t.Errorf("tooltip rows %q, want Mar 3, 2026 2:00 PM", got)
	}
	if d := description(t, c); !strings.Contains(d, "X axis time at Mar 3, 2026 2:00 PM.") {
		t.Errorf("description %q", d)
	}
}

// TestTimeTicksInRepeatedHour renders a two-second line in New York's
// repeated 1 AM hour. Its ticks began an hour before the data, so the
// chart emitted 10,000 tick labels, all off the plot to the left.
func TestTimeTicksInRepeatedHour(t *testing.T) {
	ny := mustZone("America/New_York")
	t0 := time.Date(2026, 11, 1, 6, 40, 0, 0, time.UTC)
	for _, span := range []time.Duration{2 * time.Second, 2 * time.Minute, 20 * time.Minute} {
		c := &Chart{Location: ny, Marks: []Mark{Line{X: Times{t0, t0.Add(span)}, Y: []float64{1, 2}}}}
		p, err := newPlan(c)
		if err != nil {
			t.Fatal(err)
		}
		if n := len(p.xTicks); n < 2 || n > 12 {
			t.Errorf("span %v: %d x tick labels", span, n)
		}
		for _, tk := range p.xTicks {
			if tk.v < p.l || tk.v > p.r {
				t.Errorf("span %v: tick %q at x=%v is outside the plot [%v, %v]", span, tk.text, tk.v, p.l, p.r)
			}
		}
		if out := mustRender(t, c); len(out) > 20_000 {
			t.Errorf("span %v: %d bytes of SVG", span, len(out))
		}
	}
}

// TestSubMillisecondTimes checks microsecond and 100 ns data: it gets X
// ticks and evenly spaced points, where float64 Unix seconds collapsed
// 100 ns data into four columns.
func TestSubMillisecondTimes(t *testing.T) {
	t0 := time.Date(2026, 3, 3, 14, 0, 0, 123_456_000, time.UTC)
	for _, step := range []time.Duration{50 * time.Microsecond, time.Microsecond, 100} {
		xs, ys := make(Times, 10), make([]float64, 10)
		for i := range xs {
			xs[i], ys[i] = t0.Add(time.Duration(i)*step), float64(i%3)
		}
		p, err := newPlan(&Chart{Marks: []Mark{Line{X: xs, Y: ys}}})
		if err != nil {
			t.Fatal(err)
		}
		if len(p.xTicks) < 2 {
			t.Errorf("step %v: %d x ticks", step, len(p.xTicks))
		}
		// The domain's ends are epoch-ms numbers data-hp-x can write
		// exactly (timeWire): rounded outward by up to a float64 step
		// (about 0.24 µs), so 100 ns data no longer fills the whole plot,
		// but it stays evenly spaced and spans most of it.
		s := p.series[0]
		want := (s.px[len(s.px)-1] - s.px[0]) / 9
		if want*9 < (p.r-p.l)*2/3 {
			t.Errorf("step %v: the data spans %v of the plot's %v px", step, want*9, p.r-p.l)
		}
		for i := 1; i < len(s.px); i++ {
			if gap := s.px[i] - s.px[i-1]; math.Abs(gap-want) > 1e-6 {
				t.Errorf("step %v: gap %d is %v px, want %v", step, i, gap, want)
			}
		}
		heads := tipRows(t, &Chart{Marks: []Mark{Line{X: xs, Y: ys}}})
		seen := map[string]bool{}
		for _, h := range heads {
			if seen[h] {
				t.Errorf("step %v: two rows read %q", step, h)
			}
			seen[h] = true
		}
	}
}

// TestTimeValueFormat checks that Axis.TimeValueFormat reaches every full
// time label: tooltips, the table and the description, so 24-hour ticks
// are not read out as 12-hour times.
func TestTimeValueFormat(t *testing.T) {
	t0 := time.Date(2026, 3, 3, 15, 0, 0, 0, time.UTC)
	f24 := func(t time.Time) string { return t.Format("2006-01-02 15:04") }
	c := &Chart{
		X:     Axis{TimeFormat: func(t time.Time) string { return t.Format("15:04") }, TimeValueFormat: f24},
		Marks: []Mark{Line{X: Times{t0, t0.Add(time.Hour), t0.Add(2 * time.Hour)}, Y: []float64{1, 2, 3}}},
	}
	out := string(mustRender(t, c))
	if strings.Contains(out, "PM") {
		t.Errorf("12-hour text in the output:\n%s", out)
	}
	if got := tipRows(t, c); got[0] != "2026-03-03 15:00" {
		t.Errorf("tooltip rows %q", got)
	}
	if got := tableRowHeads(t, c); got[2] != "2026-03-03 17:00" {
		t.Errorf("table rows %q", got)
	}
	if d := description(t, c); !strings.Contains(d, "time from 2026-03-03 15:00 to 2026-03-03 17:00.") {
		t.Errorf("description %q", d)
	}
}
