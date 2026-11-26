package hyperplot

import (
	"encoding/json"
	"math"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"
	"unicode/utf8"
)

func TestWireNumber(t *testing.T) {
	tenth := 0.1 // a variable, so the sum below is not folded exactly
	for _, tc := range []struct {
		v    float64
		want string
	}{
		{0, "0"},
		{math.Copysign(0, -1), "0"},
		{1, "1"},
		{-2.5, "-2.5"},
		{0.001, "0.001"},
		{1e-6, "0.000001"},
		{1e-7, "1e-7"},
		{1.5e-10, "1.5e-10"},
		{123456789012, "123456789012"},
		{1772546400000, "1772546400000"},
		{1772546400000.5, "1772546400000.5"},
		{1e20, "100000000000000000000"},
		{1e21, "1e+21"},
		{-1.25e22, "-1.25e+22"},
		{1e300, "1e+300"},
		{tenth + 2*tenth, "0.30000000000000004"},
		{1e300, "1e+300"},
	} {
		got := wireNumber(tc.v)
		if got != tc.want {
			t.Errorf("wireNumber(%v) = %q, want %q", tc.v, got, tc.want)
		}
		// encoding/json writes the same form for every finite float64.
		if j, err := json.Marshal(tc.v); err != nil || string(j) != got && tc.v != 0 {
			t.Errorf("wireNumber(%v) = %q, encoding/json %s (%v)", tc.v, got, j, err)
		}
	}
}

func TestWireDomain(t *testing.T) {
	for _, tc := range []struct {
		d0, d1, pad float64
		want        string
	}{
		{0, 100, 1, "0 100"},
		{100, 0, 1, "0 100"},
		{5, 5, 1, "4 6"},
		{1772546400000, 1772546400000, 1, "1772546399999 1772546400001"},
		{1772546400000, 1772546400000, 1000, "1772546399000 1772546401000"},
		{1e300, 1e300, 1, "9.999999999999999e+299 1.0000000000000002e+300"},
	} {
		if got := wireDomain(tc.d0, tc.d1, tc.pad); got != tc.want {
			t.Errorf("wireDomain(%v, %v, %v) = %q, want %q", tc.d0, tc.d1, tc.pad, got, tc.want)
		}
	}
}

var geomRE = regexp.MustCompile(` data-hp="1" data-hp-plot="([^"]*)"(?: data-hp-x="([^"]*)")? data-hp-y="([^"]*)" font-family=`)

// geometry returns the root's data-hp-plot, data-hp-x and data-hp-y.
func geometry(t *testing.T, svg []byte) (plot, x, y string) {
	t.Helper()
	m := geomRE.FindSubmatch(svg)
	if m == nil {
		t.Fatalf("no geometry attributes in %.300s", svg)
	}
	return string(m[1]), string(m[2]), string(m[3])
}

func TestGeometryAttrs(t *testing.T) {
	t0 := time.Date(2026, 3, 3, 14, 0, 0, 0, time.UTC)
	ms0 := strconv.FormatInt(t0.UnixMilli(), 10)
	for _, tc := range []struct {
		name string
		c    *Chart
		x, y string
	}{
		{
			name: "fixed time domain",
			c: &Chart{
				Width: 240, Height: 120,
				X:     Axis{TimeDomain: [2]time.Time{t0, t0.Add(30 * time.Minute)}},
				Y:     Axis{Domain: [2]float64{0, 100}},
				Marks: []Mark{Line{X: Times{t0.Add(5 * time.Minute)}, Y: []float64{3}}},
			},
			x: "time " + ms0 + " " + strconv.FormatInt(t0.Add(30*time.Minute).UnixMilli(), 10),
			y: "linear 0 100",
		},
		{
			name: "fractional milliseconds",
			c: &Chart{
				X:     Axis{TimeDomain: [2]time.Time{t0.Add(250 * time.Microsecond), t0.Add(time.Second)}},
				Marks: []Mark{Line{X: Times{t0.Add(time.Millisecond)}, Y: []float64{3}}},
			},
			x: "time " + ms0 + ".25 " + strconv.FormatInt(t0.UnixMilli()+1000, 10),
			y: "linear 2.7 3.3",
		},
		{
			name: "single instant is widened",
			c:    &Chart{Marks: []Mark{Line{X: Times{t0}, Y: []float64{3}}}},
			x:    "time " + strconv.FormatInt(t0.UnixMilli()-1000, 10) + " " + strconv.FormatInt(t0.UnixMilli()+1000, 10),
			y:    "linear 2.7 3.3",
		},
		{
			name: "linear after nice",
			c:    &Chart{Marks: []Mark{Dot{X: Floats{0.13, 9.7}, Y: []float64{1, 2}}}},
			x:    "linear 0 10",
			y:    "linear 1 2",
		},
		{
			name: "band",
			c:    &Chart{Marks: []Mark{Bar{X: Categories{"a", "b"}, Y: []float64{1, 2}}}},
			x:    "band",
			y:    "linear 0 2",
		},
		{
			name: "no x values",
			c:    &Chart{Title: "empty"},
			x:    "",
			y:    "linear 0 1",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			b := mustRender(t, tc.c)
			plot, x, y := geometry(t, b)
			if x != tc.x || y != tc.y {
				t.Errorf("data-hp-x %q, data-hp-y %q; want %q, %q", x, y, tc.x, tc.y)
			}
			f := strings.Fields(plot)
			if len(f) != 4 {
				t.Fatalf("data-hp-plot %q: want 4 numbers", plot)
			}
			var v [4]float64
			for i, s := range f {
				n, err := strconv.ParseFloat(s, 64)
				if err != nil {
					t.Fatalf("data-hp-plot %q: %v", plot, err)
				}
				v[i] = n
			}
			if !(v[0] < v[2] && v[1] < v[3]) {
				t.Errorf("data-hp-plot %q: want left < right and top < bottom", plot)
			}
			// The plot rectangle is the grid's and the payload's.
			if !strings.Contains(string(b), `"top":`+f[1]+`,"bottom":`+f[3]) && !strings.Contains(string(b), `"mode":"xy"`) {
				t.Errorf("data-hp-plot %q disagrees with the payload's top and bottom", plot)
			}
		})
	}
}

// TestRowKeysAndTableIDs checks the payload's row keys and the table's ids
// (docs/spec.md §4.1, §4.3), including two series with one name and rows
// whose keys repeat in a scatter.
func TestRowKeysAndTableIDs(t *testing.T) {
	t0 := time.Date(2026, 3, 3, 14, 0, 0, 0, time.UTC)
	line := &Chart{ID: "c", Marks: []Mark{
		Line{Name: "p50", X: Times{t0, t0.Add(1500 * time.Millisecond)}, Y: []float64{1, 2}},
		Line{Name: "p50", X: Times{t0}, Y: []float64{3}},
	}}
	payload := tipsOf(t, mustRender(t, line))
	if got := rowKeys(payload); strings.Join(got, " ") != "2026-03-03T14:00:00Z 2026-03-03T14:00:01.5Z" {
		t.Errorf("row keys %q", got)
	}
	table := mustTable(t, line)
	for _, want := range []string{
		`<table class="hp-table" id="c-table">`,
		`<tr id="c-r-` + hash8("2026-03-03T14:00:00Z") + `">`,
		`<tr id="c-r-` + hash8("2026-03-03T14:00:01.5Z") + `">`,
	} {
		if !strings.Contains(table, want) {
			t.Errorf("table lacks %s:\n%s", want, table)
		}
	}

	bars := &Chart{ID: "b", Marks: []Mark{Bar{Name: "s", X: Categories{"Q1149599", "Q1312382"}, Y: []float64{1, 2}}}}
	if got := rowKeys(tipsOf(t, mustRender(t, bars))); strings.Join(got, " ") != "Q1149599 Q1312382" {
		t.Errorf("band row keys %q", got)
	}
	// The two categories' hashes collide; rows share the bars' keys.
	table = mustTable(t, bars)
	for _, want := range []string{`<tr id="b-r-e0864ec2">`, `<tr id="b-r-` + hash8("Q1312382#1") + `">`} {
		if !strings.Contains(table, want) {
			t.Errorf("band table lacks %s:\n%s", want, table)
		}
	}

	dots := &Chart{ID: "d", Marks: []Mark{
		Dot{Name: "GET", X: Floats{2, 1e-7, 2}, Y: []float64{1, 2, 3}},
		Dot{Name: "GET", X: Floats{2}, Y: []float64{4}},
	}}
	if got := rowKeys(tipsOf(t, mustRender(t, dots))); strings.Join(got, " ") != "2 1e-7 2 2" {
		t.Errorf("xy row keys %q", got)
	}
	table = mustTable(t, dots)
	s0, s1 := hash8("GET"), hash8("GET#1")
	ids := regexp.MustCompile(`<tr id="([^"]*)">`).FindAllStringSubmatch(table, -1)
	var got []string
	for _, m := range ids {
		got = append(got, m[1])
	}
	want := []string{
		"d-r-" + s0 + "-" + hash8("2"), "d-r-" + s0 + "-" + hash8("1e-7"),
		"d-r-" + s0 + "-" + hash8("2#1"), "d-r-" + s1 + "-" + hash8("2"),
	}
	if strings.Join(got, " ") != strings.Join(want, " ") {
		t.Errorf("point table row ids\n got %q\nwant %q", got, want)
	}
}

// TestOverscanPoint checks that a point outside a fixed x domain, such as
// the point before a sliding window's start, is drawn but has no column in
// the payload and no row in the table (docs/spec.md §3.5, §3.8).
func TestOverscanPoint(t *testing.T) {
	t0 := time.Date(2026, 3, 3, 14, 0, 0, 0, time.UTC)
	at := func(m int) time.Time { return t0.Add(time.Duration(m) * time.Minute) }
	line := &Chart{
		ID: "o",
		X:  Axis{TimeDomain: [2]time.Time{at(5), at(15)}},
		Marks: []Mark{
			Line{Name: "a", X: Times{at(0), at(5), at(10), at(15)}, Y: []float64{1, 2, 3, 4}},
		},
	}
	b := mustRender(t, line)
	if got := rowKeys(tipsOf(t, b)); strings.Join(got, " ") != "2026-03-03T14:05:00Z 2026-03-03T14:10:00Z 2026-03-03T14:15:00Z" {
		t.Errorf("row keys %q: the overscan point must be left out", got)
	}
	// Drawn: the line still has four vertices, the first left of the plot.
	if d := regexp.MustCompile(`class="hp-line" d="([^"]*)"`).FindSubmatch(b); len(d) < 2 || strings.Count(string(d[1]), "L") != 3 {
		t.Errorf("line path %q: want 4 vertices", d)
	}
	if table := mustTable(t, line); strings.Contains(table, hash8("2026-03-03T14:00:00Z")) || strings.Count(table, "<tr id=") != 3 {
		t.Errorf("table holds the overscan row:\n%s", table)
	}

	dots := &Chart{
		X:     Axis{Domain: [2]float64{0, 10}},
		Marks: []Mark{Dot{X: Floats{-1, 5, 11}, Y: []float64{1, 2, 3}}},
	}
	if got := rowKeys(tipsOf(t, mustRender(t, dots))); strings.Join(got, " ") != "5" {
		t.Errorf("xy row keys %q: points outside the domain must be left out", got)
	}
	if table := mustTable(t, dots); strings.Count(table, "<tr id=") != 1 {
		t.Errorf("point table rows outside the domain:\n%s", table)
	}
}

type wireTips struct {
	Rows []struct {
		K string `json:"k"`
	} `json:"rows"`
}

func tipsOf(t *testing.T, svg []byte) wireTips {
	t.Helper()
	m := regexp.MustCompile(`<metadata class="hp-tips">(.*?)</metadata>`).FindSubmatch(svg)
	if m == nil {
		t.Fatal("no hp-tips payload")
	}
	var w wireTips
	if err := json.Unmarshal(m[1], &w); err != nil {
		t.Fatalf("payload: %v", err)
	}
	return w
}

func rowKeys(w wireTips) []string {
	out := make([]string, len(w.Rows))
	for i, r := range w.Rows {
		out[i] = r.K
	}
	return out
}

func mustTable(t *testing.T, c *Chart) string {
	t.Helper()
	h, err := c.Table()
	if err != nil {
		t.Fatal(err)
	}
	return string(h)
}

// TestTimesOutsideRFC3339Years checks that Render rejects times whose row
// keys could not be RFC 3339 date-times (years 1 to 9999 in UTC), in the
// data and in a fixed TimeDomain, and accepts the edges of that range.
// Before the fix, year -249, 0 and 12000 rendered keys that hx.Selection
// and Python's datetime cannot parse (docs/spec.md §1.1).
func TestTimesOutsideRFC3339Years(t *testing.T) {
	line := func(ts ...time.Time) *Chart {
		return &Chart{Marks: []Mark{Line{Name: "n", X: Times(ts), Y: make([]float64, len(ts))}}}
	}
	ok := time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC)
	for _, tc := range []struct {
		name string
		c    *Chart
		err  string // "" for success
	}{
		{"year -249", line(time.Unix(-7e10, 0)), `mark 0 (Line "n"): X[0] -0249-10-15T19:33:20Z is outside years 1 to 9999`},
		{"year 0", line(ok, time.Date(0, 6, 1, 0, 0, 0, 0, time.UTC)), `X[1] 0000-06-01T00:00:00Z is outside`},
		{"year 12000", line(time.Date(12000, 1, 1, 0, 0, 0, 0, time.UTC)), `X[0] 12000-01-01T00:00:00Z is outside`},
		{"year 1 in UTC only", line(time.Date(1, 1, 1, 0, 30, 0, 0, time.FixedZone("x", 3600))), `X[0] 0000-12-31T23:30:00Z is outside`},
		{"time domain", &Chart{
			X:     Axis{TimeDomain: [2]time.Time{time.Date(9999, 1, 1, 0, 0, 0, 0, time.UTC), time.Date(10000, 1, 1, 0, 0, 0, 0, time.UTC)}},
			Marks: []Mark{Line{X: Times{ok}, Y: []float64{1}}},
		}, `X.TimeDomain [9999-01-01T00:00:00Z, 10000-01-01T00:00:00Z] is outside years 1 to 9999`},
		{"first and last instants", line(time.Time{}, time.Date(9999, 12, 31, 23, 59, 59, 999999999, time.UTC)), ""},
	} {
		out, err := tc.c.Render()
		if tc.err == "" {
			if err != nil {
				t.Errorf("%s: %v", tc.name, err)
			} else if v := checkSpec(out, []byte(mustTable(t, tc.c))); len(v) > 0 {
				t.Errorf("%s: spec violations:\n%s", tc.name, strings.Join(v, "\n"))
			}
			continue
		}
		if err == nil || !strings.HasPrefix(err.Error(), "hyperplot: ") || !strings.Contains(err.Error(), tc.err) {
			t.Errorf("%s: err = %v, want one containing %q", tc.name, err, tc.err)
		}
	}
}

// TestInvalidUTF8CategoriesKeyAsWritten checks that categories differing
// only in bytes that are not UTF-8 are one category, as the metadata and
// the client see them (each such byte reads U+FFFD): keys stay unique
// (docs/spec.md §4.3), a bar chart reports the repeat, and the chart's own
// slices are not modified.
func TestInvalidUTF8CategoriesKeyAsWritten(t *testing.T) {
	repl := string(utf8.RuneError)
	cats := Categories{"a\xff", "a\xfe", "a" + repl, "b\xff\xfe"}
	line := &Chart{Marks: []Mark{Line{Name: "s", X: cats, Y: []float64{1, 2, 3, 4}}}}
	out := mustRender(t, line)
	if v := checkSpec(out, []byte(mustTable(t, line))); len(v) > 0 {
		t.Errorf("line: spec violations:\n%s", strings.Join(v, "\n"))
	}
	if got, want := strings.Join(rowKeys(tipsOf(t, out)), "|"), "a"+repl+"|b"+repl+repl; got != want {
		t.Errorf("line keys %q, want %q", got, want)
	}
	if cats[0] != "a\xff" || cats[1] != "a\xfe" || cats[3] != "b\xff\xfe" {
		t.Errorf("Render modified the categories: %q", cats)
	}

	_, err := (&Chart{Marks: []Mark{Bar{Name: "s", X: Categories{"a\xff", "a\xfe"}, Y: []float64{1, 2}}}}).Render()
	if err == nil || !strings.Contains(err.Error(), `mark 0 (Bar "s"): category "a`+repl+`" appears more than once`) {
		t.Errorf("bars on a\\xff and a\\xfe: err = %v, want the repeated category", err)
	}
	bars := &Chart{Marks: []Mark{Bar{Name: "s", X: Categories{"x\xff", "y"}, Y: []float64{1, 2}}}}
	out = mustRender(t, bars)
	if v := checkSpec(out, []byte(mustTable(t, bars))); len(v) > 0 {
		t.Errorf("bars: spec violations:\n%s", strings.Join(v, "\n"))
	}
	if !strings.Contains(string(out), `"k":"x`+repl+`"`) || strings.Contains(string(out), "\xff") {
		t.Errorf("bars: the key is not the written category:\n%s", out)
	}
}

// TestTimeDomainIsExactOnTheWire checks docs/spec.md §4.2 for time
// domains a few microseconds wide: every row key, mapped through
// data-hp-x and data-hp-plot, lands where the row is drawn. Before the
// fix, a 100 ns domain was written as 2 ms wide and a 3 µs one put the
// middle point 25 px off.
func TestTimeDomainIsExactOnTheWire(t *testing.T) {
	t0 := time.Date(2026, 3, 3, 14, 0, 0, 0, time.UTC)
	far := time.Date(9000, 3, 3, 14, 0, 0, 0, time.UTC) // coarse float64 epoch ms
	for _, offs := range [][]time.Duration{
		{0, 50, 100},
		{123, 1500, 3123},
		{0, 500 * time.Microsecond, time.Millisecond},
		{7, 30*time.Microsecond + 1, 17 * time.Millisecond},
		{0, 1},
		{5}, // a single instant
	} {
		for _, base := range []time.Time{t0, far} {
			ts := make(Times, len(offs))
			for i, d := range offs {
				ts[i] = base.Add(d)
			}
			for _, c := range []*Chart{
				{Marks: []Mark{Line{X: ts, Y: make([]float64, len(ts))}}},
				{Marks: []Mark{Dot{X: ts, Y: make([]float64, len(ts))}}},
				{
					Width: 4000, X: Axis{TimeDomain: [2]time.Time{base.Add(-3), base.Add(4 * time.Microsecond)}},
					Marks: []Mark{Line{X: ts, Y: make([]float64, len(ts))}},
				},
			} {
				out := mustRender(t, c)
				if v := checkSpec(out, []byte(mustTable(t, c))); len(v) > 0 {
					t.Errorf("%v + %v: spec violations:\n%s", base.Year(), offs, strings.Join(v, "\n"))
				}
			}
		}
	}
	// Ends already on the grid (whole milliseconds, 250 µs) are kept.
	_, x, _ := geometry(t, mustRender(t, &Chart{Marks: []Mark{Line{X: Times{t0, t0.Add(250 * time.Microsecond)}, Y: []float64{1, 2}}}}))
	if want := "time 1772546400000 1772546400000.25"; x != want {
		t.Errorf("data-hp-x = %q, want %q", x, want)
	}
}
