package plot

import (
	"bytes"
	"flag"
	"math"
	"os"
	"path/filepath"
	"testing"
	"time"
)

var update = flag.Bool("update", false, "rewrite the golden files in testdata")

var t0 = time.Date(2026, time.March, 3, 14, 0, 0, 0, time.UTC)

// minutes returns n times, step minutes apart, starting at t0.
func minutes(n, step int) Times {
	out := make(Times, n)
	for i := range out {
		out[i] = t0.Add(time.Duration(i*step) * time.Minute)
	}
	return out
}

// days returns n daily times starting at t0's date.
func days(n int) Times {
	out := make(Times, n)
	for i := range out {
		out[i] = time.Date(2026, time.March, 1+i, 0, 0, 0, 0, time.UTC)
	}
	return out
}

var nan = math.NaN()

type goldenCase struct {
	name    string
	preview bool // also write a standalone copy to testdata/preview
	chart   func() *Chart
}

var goldenCases = []goldenCase{
	{"line_time", false, func() *Chart {
		return &Chart{
			ID: "latency", Title: "Latency",
			Y:     Axis{Label: "Latency (ms)", Unit: " ms"},
			Marks: []Mark{Line{Name: "p50", X: minutes(13, 5), Y: []float64{42, 45, 41, 48, 52, 61, 58, 55, 50, 47, 49, 46, 44}}},
		}
	}},
	{"lines_direct_labels", true, func() *Chart {
		x := minutes(13, 5)
		return &Chart{
			ID: "percentiles", Title: "Request latency",
			Y: Axis{Label: "ms", Unit: " ms"},
			Marks: []Mark{
				Line{Name: "p50", X: x, Y: []float64{42, 45, 41, 48, 52, 61, 58, 55, 50, 47, 49, 46, 44}},
				Line{Name: "p90", X: x, Y: []float64{71, 76, 70, 82, 88, 97, 93, 90, 84, 80, 83, 79, 77}},
				Line{Name: "p99", X: x, Y: []float64{98, 110, 104, 121, 133, 152, 140, 131, 122, 117, 119, 115, 112}},
			},
		}
	}},
	{"lines_overlap_no_labels", false, func() *Chart {
		x := minutes(13, 5)
		return &Chart{
			ID: "overlap", Title: "Overlapping ends",
			Marks: []Mark{
				Line{Name: "eu-west", X: x, Y: []float64{20, 25, 30, 35, 40, 45, 50, 55, 60, 62, 61, 60, 60}},
				Line{Name: "us-east", X: x, Y: []float64{60, 58, 55, 52, 50, 49, 50, 52, 55, 57, 59, 60, 61}},
				Line{Name: "ap-south", X: x, Y: []float64{40, 41, 42, 43, 44, 45, 46, 47, 48, 49, 50, 51, 70}},
			},
		}
	}},
	{"area_stacked", true, func() *Chart {
		x := days(14)
		return &Chart{
			ID: "traffic", Title: "Traffic by source", Stack: true,
			Y: Axis{Label: "Visits"},
			Marks: []Mark{
				Area{Name: "Search", X: x, Y: []float64{1200, 1350, 1280, 1400, 1520, 900, 850, 1300, 1420, 1380, 1500, 1610, 950, 870}},
				Area{Name: "Direct", X: x, Y: []float64{600, 640, 620, 700, 720, 400, 380, 650, 690, 700, 710, 760, 420, 390}},
				Area{Name: "Social", X: x, Y: []float64{300, 280, 350, 420, 380, 500, 520, 330, 310, 360, 400, 390, 560, 540}},
			},
		}
	}},
	{"bars_grouped", true, func() *Chart {
		q := Categories{"Q1", "Q2", "Q3", "Q4"}
		return &Chart{
			ID: "revenue", Title: "Revenue by quarter",
			Y: Axis{Label: "Revenue ($K)", Unit: "K"},
			Marks: []Mark{
				Bar{Name: "2024", X: q, Y: []float64{120, 135, 150, 170}},
				Bar{Name: "2025", X: q, Y: []float64{140, 150, 165, 190}},
				Bar{Name: "2026", X: q, Y: []float64{160, 172, nan, 210}},
			},
		}
	}},
	{"bars_stacked_negative", true, func() *Chart {
		m := Categories{"Jan", "Feb", "Mar", "Apr", "May", "Jun"}
		return &Chart{
			ID: "cashflow", Title: "Cash flow", Stack: true,
			Marks: []Mark{
				Bar{Name: "Sales", X: m, Y: []float64{50, 62, 48, 70, 75, 80}},
				Bar{Name: "Services", X: m, Y: []float64{20, 18, 25, 22, 0, 30}},
				Bar{Name: "Costs", X: m, Y: []float64{-40, -45, -60, -38, -50, -42}},
				Bar{Name: "Refunds", X: m, Y: []float64{-5, -8, -3, -12, -6, -4}},
			},
		}
	}},
	{"scatter_labels", true, func() *Chart {
		return &Chart{
			ID: "scatter", Title: "Size vs. speed",
			X: Axis{Label: "Bundle size (KB)", Unit: " KB"},
			Y: Axis{Label: "Load time (s)", Unit: " s"},
			Marks: []Mark{
				Dot{
					Name: "Frameworks", X: Floats{42, 130, 88, 310, 65, 210}, Y: []float64{1.2, 2.1, 1.6, 3.4, 1.3, 2.6},
					Labels: []string{"htmx", "React", "Vue", "Angular", "Preact", "Ember"},
				},
				Dot{Name: "No build", X: Floats{14, 20, 31}, Y: []float64{0.8, 0.9, 1.05}, Labels: []string{"hyperplot", "alpine", "petite"}},
			},
		}
	}},
	{"line_categorical", true, func() *Chart {
		return &Chart{
			ID: "weekly", Title: "Signups this week",
			Marks: []Mark{Line{
				Name: "Signups", Curve: CurveMonotone, Points: true,
				X: Categories{"Mon", "Tue", "Wed", "Thu", "Fri", "Sat", "Sun"},
				Y: []float64{120, 132, 101, 134, 190, 230, 210},
			}},
		}
	}},
	{"line_gaps", false, func() *Chart {
		return &Chart{
			ID: "gaps", Title: "Sensor with dropouts",
			Marks: []Mark{
				Line{
					Name: "temp", Curve: CurveMonotone, X: minutes(16, 1),
					Y: []float64{21, 21.4, 21.9, nan, nan, 22.8, 23.1, 22.6, nan, 21.8, nan, 21.2, 21.5, 21.1, math.Inf(1), 20.6},
				},
			},
		}
	}},
	{"rule_y", false, func() *Chart {
		return &Chart{
			ID: "slo", Title: "Error rate",
			Y: Axis{Unit: "%"},
			Marks: []Mark{
				Area{Name: "errors", X: minutes(13, 5), Y: []float64{0.4, 0.5, 0.45, 0.6, 0.9, 1.4, 1.1, 0.8, 0.7, 0.65, 0.6, 0.5, 0.48}},
				RuleY{Y: 1, Label: "SLO 1%"},
			},
		}
	}},
	{"fixed_domain", false, func() *Chart {
		return &Chart{
			ID: "cpu", Title: "CPU",
			X: Axis{Domain: [2]float64{0, 60}, Label: "seconds"},
			Y: Axis{Domain: [2]float64{0, 100}, Unit: "%", Ticks: 5},
			Marks: []Mark{
				Line{Name: "cpu0", Curve: CurveStep, X: Floats{0, 10, 20, 30, 40, 50, 60, 70}, Y: []float64{20, 35, 90, 120, 60, 40, 30, 25}},
				RuleY{Y: 150, Label: "off-scale rule is not drawn"},
			},
		}
	}},
	{"standalone_dark", false, func() *Chart {
		x := days(10)
		return &Chart{
			ID: "standalone", Title: "Deploys and incidents", Standalone: true, Animate: true,
			Y: Axis{Label: "Count"},
			Marks: []Mark{
				Line{Name: "Deploys", X: x, Y: []float64{4, 6, 5, 8, 7, 3, 2, 9, 10, 8}},
				Line{Name: "Incidents", X: x, Y: []float64{1, 0, 2, 1, 0, 0, 1, 3, 1, 0}, Slot: 8},
			},
		}
	}},
	{"empty", false, func() *Chart { return &Chart{ID: "empty", Title: "Nothing yet"} }},
	{"band_area_negative", false, func() *Chart {
		return &Chart{
			ID: "band", Title: "Temperature range",
			Y: Axis{Unit: "°C", Label: "°C"},
			Marks: []Mark{
				Area{Name: "min–max", X: days(7), Y: []float64{-2, 1, 3, -1, -4, 0, 2}, Base: []float64{-9, -7, -5, -8, -12, -9, -6}},
				Line{Name: "mean", X: days(7), Y: []float64{-5.5, -3, -1, -4.5, -8, -4.5, -2}},
			},
		}
	}},
	{"long_names", false, func() *Chart {
		x := Categories{"A very long category name that goes on", "B", "C"}
		return &Chart{
			ID: "long", Title: "A title that is far too long to fit on a single line of a small chart, so it is truncated",
			Width: 320, Height: 200, ShowTitle: true,
			Marks: []Mark{
				Bar{Name: "A series name so long that the legend has to truncate it with an ellipsis", X: x, Y: []float64{1, 2, 3}},
				Bar{Name: "<b>markup & \"quotes\"</b>", X: x, Y: []float64{3, 2, 1}},
			},
		}
	}},
}

func TestGolden(t *testing.T) {
	for _, tc := range goldenCases {
		t.Run(tc.name, func(t *testing.T) {
			got := mustRender(t, tc.chart())
			checkSVG(t, got)
			compareGolden(t, filepath.Join("testdata", tc.name+".svg"), got)
		})
	}
}

// TestGoldenPreview renders standalone copies of some golden charts, for
// opening directly in a browser.
func TestGoldenPreview(t *testing.T) {
	for _, tc := range goldenCases {
		if !tc.preview {
			continue
		}
		t.Run(tc.name, func(t *testing.T) {
			c := tc.chart()
			c.Standalone = true
			got := mustRender(t, c)
			checkSVG(t, got)
			compareGolden(t, filepath.Join("testdata", "preview", tc.name+".svg"), got)
		})
	}
}

func compareGolden(t *testing.T, path string, got []byte) {
	t.Helper()
	if *update {
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, got, 0o644); err != nil {
			t.Fatal(err)
		}
		return
	}
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("%v (run go test -run Golden -update to create it)", err)
	}
	if !bytes.Equal(got, want) {
		t.Errorf("%s differs from the golden file; run go test -run Golden -update and review the diff\ngot:\n%s", path, got)
	}
}

// TestGoldenFilesWellFormed parses every SVG in testdata with
// encoding/xml, and fails on any file that no golden case writes (one
// left over from a renamed or removed case), so testdata always matches
// the cases. Delete such files rather than keeping them.
func TestGoldenFilesWellFormed(t *testing.T) {
	want := map[string]bool{}
	for _, tc := range goldenCases {
		want[filepath.Join("testdata", tc.name+".svg")] = true
		if tc.preview {
			want[filepath.Join("testdata", "preview", tc.name+".svg")] = true
		}
	}
	for _, pattern := range []string{"testdata/*.svg", "testdata/preview/*.svg"} {
		files, err := filepath.Glob(pattern)
		if err != nil {
			t.Fatal(err)
		}
		for _, f := range files {
			if !want[filepath.Clean(f)] {
				t.Errorf("%s matches no golden case; delete it or add the case", f)
			}
			b, err := os.ReadFile(f)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := parseSVG(b); err != nil {
				t.Errorf("%s: %v", f, err)
			}
		}
	}
}
