// The gallery's charts: one func per card, in page order. The page shows
// each func's source next to the chart it returns, cut out of this very
// file (embedded into the binary, see source.go), so the code on the page
// is always the code that drew the chart. A func's name in kebab case is
// its chart's ID. The data helpers are at the end of the file.
package main

import (
	"hash/fnv"
	"io"
	"math"
	"strconv"
	"time"

	"github.com/ironystock/hyperplot/plot"
)

// ---- Lines ------------------------------------------------------------------

// lineDirectLabels draws three lines over a time axis. With two to four
// lines whose ends are far enough apart, hyperplot writes each name at
// its last point, so nobody has to match colors against the legend.
func lineDirectLabels() *plot.Chart {
	xs := hours(24)
	return &plot.Chart{
		// An explicit, stable ID: the client keys hover state on it.
		ID:    "line-direct-labels",
		Title: "API latency by percentile",
		Y:     plot.Axis{Label: "ms", Unit: " ms", Zero: true},
		Marks: []plot.Mark{
			plot.Line{Name: "p50", X: xs, Y: wave("p50", 24, 90, 15)},
			plot.Line{Name: "p95", X: xs, Y: wave("p95", 24, 180, 30)},
			plot.Line{Name: "p99", X: xs, Y: wave("p99", 24, 290, 45)},
		},
	}
}

// lineLegendOnly draws five lines. Past four series (or when two ends
// would collide) there are no direct labels; the legend, the tooltip and
// the table name the series instead.
func lineLegendOnly() *plot.Chart {
	xs := hours(24)
	regions := []string{"us-east", "us-west", "eu-west", "eu-north", "ap-south"}
	var marks []plot.Mark
	for i, region := range regions {
		ys := wave(region, 24, 1200+150*float64(i), 300)
		marks = append(marks, plot.Line{Name: region, X: xs, Y: ys})
	}
	return &plot.Chart{
		ID:    "line-legend-only",
		Title: "Requests per minute by region",
		Y:     plot.Axis{Unit: " req/min"},
		Marks: marks,
	}
}

// lineGaps draws a line with missing readings. A NaN breaks the line
// rather than bridging the hole, and a lone reading between two gaps
// still shows. Points puts a dot (with a native tooltip) on every reading.
func lineGaps() *plot.Chart {
	ys := wave("sensor", 24, 21, 3)
	for _, i := range []int{6, 7, 8, 10, 11, 17} {
		ys[i] = math.NaN() // the sensor was offline
	}
	return &plot.Chart{
		ID:    "line-gaps",
		Title: "Room temperature, with outages",
		Y:     plot.Axis{Label: "°C", Unit: " °C"},
		Marks: []plot.Mark{
			plot.Line{Name: "temperature", X: hours(24), Y: ys, Points: true},
		},
	}
}

// lineCurves draws the same readings with each Curve. Monotone is smooth
// but never overshoots a point; step holds each value until the next one.
// The direct labels name the lines, so the legend is hidden.
func lineCurves() *plot.Chart {
	xs := hours(12)
	depth := []float64{2, 3, 3, 7, 6, 6, 9, 8, 4, 4, 6, 7}
	// up returns depth moved up by some amount, so the lines don't overlap.
	up := func(by float64) []float64 {
		ys := make([]float64, len(depth))
		for i, v := range depth {
			ys[i] = v + by
		}
		return ys
	}
	return &plot.Chart{
		ID:     "line-curves",
		Title:  "Queue depth, three ways",
		Legend: plot.LegendHide,
		Y:      plot.Axis{Hide: true}, // the offsets make the values meaningless
		Marks: []plot.Mark{
			plot.Line{
				Name: "linear", X: xs, Y: up(20),
				Curve: plot.CurveLinear,
			},
			plot.Line{
				Name: "monotone", X: xs, Y: up(10),
				Curve: plot.CurveMonotone,
			},
			plot.Line{
				Name: "step", X: xs, Y: up(0),
				Curve: plot.CurveStep,
			},
		},
	}
}

// lineCategorical draws lines over Categories: a band scale, in order of
// first appearance, with each point at the middle of its band. A series
// may cover fewer categories than another.
func lineCategorical() *plot.Chart {
	months := plot.Categories{
		"Jan", "Feb", "Mar", "Apr", "May", "Jun",
		"Jul", "Aug", "Sep", "Oct", "Nov", "Dec",
	}
	lastYear := []float64{310, 290, 350, 400, 380, 420, 460, 450, 500, 520, 610, 700}
	thisYear := []float64{520, 540, 600, 580, 640, 700, 690, 760, 820} // to September
	return &plot.Chart{
		ID:    "line-categorical",
		Title: "Sign-ups per month",
		Y:     plot.Axis{Unit: " sign-ups", Zero: true},
		Marks: []plot.Mark{
			plot.Line{Name: "2025", X: months, Y: lastYear, Points: true},
			plot.Line{Name: "2026", X: months[:9], Y: thisYear, Points: true},
		},
	}
}

// lineLinearX draws a line over Floats: a linear x scale, ticked like the
// y axis. The x label sits under the right end of the plot.
func lineLinearX() *plot.Chart {
	clients := plot.Floats{1, 2, 4, 8, 16, 32, 48, 64, 96, 128}
	rps := []float64{410, 800, 1530, 2810, 4620, 6100, 6480, 6550, 6420, 6180}
	return &plot.Chart{
		ID:    "line-linear-x",
		Title: "Throughput under load",
		X:     plot.Axis{Label: "concurrent clients"},
		Y:     plot.Axis{Label: "req/s", Unit: " req/s", Zero: true},
		Marks: []plot.Mark{
			plot.Line{
				Name: "throughput", X: clients, Y: rps,
				Curve: plot.CurveMonotone, Points: true,
			},
		},
	}
}

// ---- Areas ------------------------------------------------------------------

// areaSingle draws one area: a light wash under a 2 px line. Charts with
// areas or bars always include zero on y.
func areaSingle() *plot.Chart {
	return &plot.Chart{
		ID:    "area-single",
		Title: "Egress bandwidth, last two days",
		Y:     plot.Axis{Label: "Mbit/s", Unit: " Mbit/s"},
		Marks: []plot.Mark{
			plot.Area{
				Name:  "egress",
				X:     hours(48),
				Y:     wave("egress", 48, 420, 160),
				Curve: plot.CurveMonotone,
			},
		},
	}
}

// areaStacked stacks areas in mark order (Chart.Stack), matching points
// by x value. The top edge is the total.
func areaStacked() *plot.Chart {
	xs := hours(24)
	return &plot.Chart{
		ID:    "area-stacked",
		Title: "Storage used by tier",
		Stack: true,
		Y:     plot.Axis{Label: "TB", Unit: " TB"},
		Marks: []plot.Mark{
			plot.Area{Name: "hot", X: xs, Y: wave("hot", 24, 12, 2)},
			plot.Area{Name: "warm", X: xs, Y: wave("warm", 24, 20, 3)},
			plot.Area{Name: "cold", X: xs, Y: wave("cold", 24, 35, 2)},
		},
	}
}

// areaBand fills between two edges (Area.Base) for a min–max range that
// crosses zero, with the mean as a line on top.
func areaBand() *plot.Chart {
	xs := days(21)
	mean, spread := wave("mean", 21, 1, 6), wave("spread", 21, 4, 1.5)
	low, high := make([]float64, 21), make([]float64, 21)
	for i := range mean {
		low[i], high[i] = round1(mean[i]-spread[i]), round1(mean[i]+spread[i])
	}
	return &plot.Chart{
		ID:    "area-band",
		Title: "Daily temperature range",
		Y:     plot.Axis{Label: "°C", Unit: " °C"},
		Marks: []plot.Mark{
			plot.Area{Name: "low to high", X: xs, Y: high, Base: low},
			plot.Line{Name: "mean", X: xs, Y: mean},
		},
	}
}

// ---- Bars -------------------------------------------------------------------

// barsSingle draws one column per category. Bars are at most 24 px thick
// (Chart.BarWidth), with a rounded data end and a square base.
func barsSingle() *plot.Chart {
	components := plot.Categories{"parser", "renderer", "client", "docs", "build"}
	open := []float64{14, 23, 9, 31, 6}
	return &plot.Chart{
		ID:    "bars-single",
		Title: "Open issues by component",
		Y:     plot.Axis{Unit: " issues"},
		Marks: []plot.Mark{
			plot.Bar{Name: "open issues", X: components, Y: open},
		},
	}
}

// barsGrouped draws several Bar marks over the same categories side by
// side, one color slot per series in mark order.
func barsGrouped() *plot.Chart {
	quarters := plot.Categories{"Q1", "Q2", "Q3", "Q4"}
	return &plot.Chart{
		ID:    "bars-grouped",
		Title: "Revenue by quarter and market",
		Y:     plot.Axis{Label: "$M", Unit: " $M"},
		Marks: []plot.Mark{
			plot.Bar{Name: "US", X: quarters, Y: []float64{4.2, 4.8, 5.1, 6.0}},
			plot.Bar{Name: "EU", X: quarters, Y: []float64{3.1, 3.3, 3.9, 4.4}},
			plot.Bar{Name: "Asia", X: quarters, Y: []float64{2.0, 2.6, 3.4, 4.1}},
		},
	}
}

// barsGroupedNegative draws grouped bars with negative values. They hang
// down from the zero baseline, with the rounded end pointing down.
func barsGroupedNegative() *plot.Chart {
	regions := plot.Categories{"us-east", "us-west", "eu-west", "ap-south"}
	return &plot.Chart{
		ID:    "bars-grouped-negative",
		Title: "Year-over-year change in traffic",
		Y:     plot.Axis{Label: "change, %", Unit: "%"},
		Marks: []plot.Mark{
			plot.Bar{Name: "2025", X: regions, Y: []float64{12.5, -4, 7.2, -9.8}},
			plot.Bar{Name: "2026", X: regions, Y: []float64{8.1, 3.4, -2.6, 15.3}},
		},
	}
}

// barsStackedNegative stacks bars (Chart.Stack): positive values stack
// upward from zero and negative values downward, so income and costs
// share one column per month.
func barsStackedNegative() *plot.Chart {
	months := plot.Categories{"Jan", "Feb", "Mar", "Apr", "May", "Jun"}
	sales := []float64{42, 38, 51, 47, 55, 61}
	refunds := []float64{-4, -6, -3, -5, -4, -7} // negative: they stack downward
	costs := []float64{-28, -30, -29, -33, -31, -35}
	return &plot.Chart{
		ID:    "bars-stacked-negative",
		Title: "Cash flow by month",
		Stack: true,
		Y:     plot.Axis{Label: "$k", Unit: " $k"},
		Marks: []plot.Mark{
			plot.Bar{Name: "sales", X: months, Y: sales},
			plot.Bar{Name: "refunds", X: months, Y: refunds},
			plot.Bar{Name: "costs", X: months, Y: costs},
		},
	}
}

// ---- Scatter ----------------------------------------------------------------

// scatter draws Dot marks over Floats. Color alone separates about three
// series for readers with color-vision deficiencies; past that, label the
// points or use small multiples.
func scatter() *plot.Chart {
	var marks []plot.Mark
	for _, method := range []struct {
		name  string
		slope float64
	}{{"GET", 1.2}, {"POST", 2.4}} {
		kb, ms := make(plot.Floats, 40), make([]float64, 40)
		for i := range kb {
			kb[i] = round1(1 + 99*noise(method.name+"/kb", i))
			ms[i] = round1(20 + method.slope*kb[i] + 40*noise(method.name+"/ms", i))
		}
		marks = append(marks, plot.Dot{Name: method.name, X: kb, Y: ms})
	}
	return &plot.Chart{
		ID:    "scatter",
		Title: "Latency against payload size",
		X:     plot.Axis{Label: "payload, KB", Zero: true},
		Y:     plot.Axis{Label: "ms", Unit: " ms", Zero: true},
		Marks: marks,
	}
}

// scatterLabels names each point (Dot.Labels). A label is drawn where it
// fits without covering another label or dot, and skipped where it does
// not; the tooltip and the table always carry it.
func scatterLabels() *plot.Chart {
	services := []string{
		"auth", "billing", "search", "cart", "catalog",
		"images", "mail", "pricing", "profile", "reviews",
		"shipping", "stock", "suggest", "spelling", "autocomplete",
	}
	rps := plot.Floats{
		820, 140, 1960, 610, 1500,
		2400, 90, 350, 720, 260,
		480, 1100, 1530, 1480, 1515,
	}
	errorPct := []float64{
		0.4, 2.9, 0.6, 1.1, 0.3,
		0.8, 4.2, 0.5, 0.9, 1.6,
		2.2, 0.7, 0.35, 0.25, 0.4,
	}
	return &plot.Chart{
		ID:    "scatter-labels",
		Title: "Error rate against traffic, by service",
		X:     plot.Axis{Label: "requests per second", Zero: true},
		Y:     plot.Axis{Label: "errors, %", Unit: "%", Zero: true},
		Marks: []plot.Mark{
			plot.Dot{Name: "services", X: rps, Y: errorPct, Labels: services},
		},
	}
}

// ---- Rules, domains and formats ---------------------------------------------

// rules adds RuleY marks: reference lines for targets and thresholds.
// They are annotations, not series: no color slot, no legend entry.
func rules() *plot.Chart {
	return &plot.Chart{
		ID:    "rules",
		Title: "Checkout latency against its objectives",
		Y:     plot.Axis{Label: "ms", Unit: " ms", Zero: true},
		Marks: []plot.Mark{
			plot.Line{Name: "p95", X: hours(24), Y: wave("checkout", 24, 210, 90)},
			plot.RuleY{Y: 250, Label: "250 ms objective"},
			plot.RuleY{Y: 400, Label: "page the on-call"},
		},
	}
}

// fixedDomains fixes both axes instead of fitting the data: a percentage
// keeps its 0–100 scale, and today's chart spans the whole day while the
// data is still arriving.
func fixedDomains() *plot.Chart {
	day := [2]time.Time{start, start.Add(24 * time.Hour)}
	return &plot.Chart{
		ID:    "fixed-domains",
		Title: "CPU use today",
		X:     plot.Axis{TimeDomain: day},
		Y:     plot.Axis{Label: "%", Unit: "%", Domain: [2]float64{0, 100}},
		Marks: []plot.Mark{
			plot.Area{Name: "cpu", X: hours(15), Y: wave("cpu", 15, 45, 30)},
			plot.RuleY{Y: 80, Label: "scale out at 80%"},
		},
	}
}

// customFormats replaces the default labels, which follow US conventions,
// with a 24-hour clock and a "k" for thousands. TimeFormat is for ticks;
// TimeValueFormat is for tooltips, the table and the description.
func customFormats() *plot.Chart {
	clock := func(t time.Time) string { return t.Format("15:04") }
	full := func(t time.Time) string { return t.Format("2 Jan 2006 15:04") }
	thousands := func(v float64) string {
		return strconv.FormatFloat(v/1000, 'f', 1, 64) + "k"
	}
	orders := wave("orders", 24, 5200, 2100)
	return &plot.Chart{
		ID:    "custom-formats",
		Title: "Orders per hour",
		X:     plot.Axis{TimeFormat: clock, TimeValueFormat: full},
		Y:     plot.Axis{Label: "orders", Format: thousands},
		Marks: []plot.Mark{
			plot.Line{Name: "orders", X: hours(24), Y: orders},
		},
	}
}

// ---- Long text, no data, untrusted text -------------------------------------

// longNames has a title, series names and categories too long for their
// space. They are cut with an ellipsis rather than overlapping; the
// tooltip, the table and the accessible name keep the full text.
func longNames() *plot.Chart {
	teams := plot.Categories{
		"Platform infrastructure and developer experience",
		"Payments",
		"Search relevance and ranking quality",
		"Mobile",
	}
	q3 := "Incidents in the third quarter of the fiscal year"
	q4 := "Incidents in the fourth quarter, after the reorganization"
	return &plot.Chart{
		ID: "long-names",
		Title: "Incidents per team in the last two quarters of the fiscal year, " +
			"not counting planned maintenance",
		ShowTitle: true, // draw the title inside the chart, where it has to fit
		Marks: []plot.Mark{
			plot.Bar{Name: q3, X: teams, Y: []float64{7, 3, 5, 2}},
			plot.Bar{Name: q4, X: teams, Y: []float64{4, 5, 2, 3}},
		},
	}
}

// empty has a series with no values yet. The chart still renders, with
// "No data" in the middle of the plot, so a page never breaks on an empty
// query.
func empty() *plot.Chart {
	return &plot.Chart{
		ID:    "empty",
		Title: "Error rate (nothing reported yet)",
		Y:     plot.Axis{Unit: "%"},
		Marks: []plot.Mark{
			plot.Line{Name: "errors", X: plot.Times{}, Y: nil},
		},
	}
}

// untrustedText puts markup in every string. Names and categories often
// come from users, so hyperplot escapes all text: the markup is shown,
// never run (spec/spec.md §3.6).
func untrustedText() *plot.Chart {
	names := plot.Categories{
		`<script>alert(1)</script>`, `Tom & Jerry`, `"quoted"`, `<b>bold?</b>`,
	}
	return &plot.Chart{
		ID:    "untrusted-text",
		Title: `Votes for <em>your</em> favorite`,
		Marks: []plot.Mark{
			plot.Bar{
				Name: `votes <img src=x onerror=alert(1)>`,
				X:    names,
				Y:    []float64{12, 30, 7, 19},
			},
		},
	}
}

// ---- Sparklines and tables --------------------------------------------------

// sparklines are compact, axis-free lines for stat tiles and table rows.
// Label and Unit make the accessible name ("Requests per second: first
// …, min …"); a NaN breaks the line, as in a chart.
func sparklines() []plot.Sparkline {
	errs := wave("errors", 30, 1.2, 0.6)
	errs[12], errs[13] = math.NaN(), math.NaN() // no data for two days
	return []plot.Sparkline{
		{Label: "Requests per second", Values: wave("rps", 30, 820, 140), Area: true},
		{Label: "p95 latency", Unit: " ms", Values: wave("p95", 30, 180, 30)},
		{Label: "Error rate", Unit: "%", Values: errs},
	}
}

// dataTable is an ordinary chart; the gallery shows its Chart.Table open
// below it. The table is the chart's accessible equivalent: a caption,
// one row per x value, one column per series (spec/spec.md §3.8).
func dataTable() *plot.Chart {
	weekdays := plot.Categories{"Mon", "Tue", "Wed", "Thu", "Fri"}
	return &plot.Chart{
		ID:    "data-table",
		Title: "Deploys this week",
		Stack: true,
		X:     plot.Axis{Label: "Day"}, // heads the table's first column
		Y:     plot.Axis{Unit: " deploys"},
		Marks: []plot.Mark{
			plot.Bar{Name: "succeeded", X: weekdays, Y: []float64{5, 8, 6, 9, 3}},
			plot.Bar{Name: "failed", X: weekdays, Y: []float64{1, 0, 2, 1, 0}},
		},
	}
}

// ---- Data helpers -----------------------------------------------------------
//
// The charts draw made-up data. These helpers keep it deterministic:
// every value is a pure function of a series name and an index, so the
// gallery renders the same bytes on every run.

// start is the first instant of every time axis.
var start = time.Date(2026, 3, 2, 0, 0, 0, 0, time.UTC)

// hours returns n hourly instants from start.
func hours(n int) plot.Times {
	ts := make(plot.Times, n)
	for i := range ts {
		ts[i] = start.Add(time.Duration(i) * time.Hour)
	}
	return ts
}

// days returns n daily instants from start.
func days(n int) plot.Times {
	ts := make(plot.Times, n)
	for i := range ts {
		ts[i] = start.AddDate(0, 0, i)
	}
	return ts
}

// wave returns n values around base: a slow swing of ±amp/2 plus up to
// ±amp/2 of noise, rounded to one decimal place.
func wave(series string, n int, base, amp float64) []float64 {
	ys := make([]float64, n)
	for i := range ys {
		swing := math.Sin(float64(i) / 4)
		ys[i] = round1(base + amp/2*swing + amp*(noise(series, i)-0.5))
	}
	return ys
}

// round1 rounds to one decimal place, so tooltips read 21.4, not
// 21.396254.
func round1(v float64) float64 { return math.Round(v*10) / 10 }

// noise is a deterministic number in [0, 1) for a series and an index:
// FNV-1a of both, finished with the splitmix64 mixer. (FNV-1a alone
// barely changes its high bits when only the last digit changes.)
func noise(series string, i int) float64 {
	h := fnv.New64a()
	_, _ = io.WriteString(h, series+"/"+strconv.Itoa(i))
	x := h.Sum64()
	x = (x ^ x>>30) * 0xbf58476d1ce4e5b9
	x = (x ^ x>>27) * 0x94d049bb133111eb
	x ^= x >> 31
	return float64(x>>11) / (1 << 53)
}
