package main

import (
	"html/template"
	"math"
	"time"

	"github.com/ironystock/hyperplot"
	"github.com/ironystock/hyperplot/scale"
)

// Chart sizes. The client extension reports each container's width (hp-w)
// and the handlers render to it, so these are only the first-paint size.
// Heights are fixed per chart rather than read from hp-h: a container also
// holds its "View as table" disclosure, so its height is not the chart's.
const (
	defaultWidth = 640
	narrowWidth  = 520 // below this, charts get shorter
	sloMS        = 250 // the latency objective drawn as a rule
	scatterN     = 40  // simulated requests per region in the scatter plot
	sparkPoints  = 24  // points per KPI sparkline
)

// Latency percentiles use slots 5–7, away from the regions' slots 1–3, so
// that no color means two things on the page. The trio passes the
// palette's all-pairs color-vision checks in light and dark mode.
const (
	slotP50 = 5
	slotP95 = 6
	slotP99 = 7
)

// chartHeight returns the height for a chart rendered at width w.
func chartHeight(w float64, tall bool) float64 {
	h := 260.0
	if tall {
		h = 300
	}
	if w < narrowWidth {
		h -= 60
	}
	return h
}

func round1(v float64) float64 { return math.Round(v*10) / 10 }

// latencyChart plots the traffic-weighted p50, p95 and p99 latency of the
// selected regions over the closed buckets ending at end. The series start
// one bucket before the window (see windowHistory), so the lines reach the
// plot's left edge while the window slides.
func latencyChart(f filters, end time.Time, w float64, loc *time.Location) *hyperplot.Chart {
	start := end.Add(-f.Range.Span)
	return latencyFrame(windowHistory(f, end, start), start, end, w, loc)
}

// latencyFrame plots the buckets in hist over the x window [start, end]:
// the live window of latencyChart or a zoom (zoomedLatencyChart).
func latencyFrame(hist []sample, start, end time.Time, w float64, loc *time.Location) *hyperplot.Chart {
	xs := hyperplot.Times(hyperplot.Map(hist, func(s sample) time.Time { return s.End }))
	line := func(name string, slot int, v func(sample) float64) hyperplot.Line {
		return hyperplot.Line{
			Name:  name,
			X:     xs,
			Y:     hyperplot.Map(hist, func(s sample) float64 { return round1(v(s)) }),
			Curve: hyperplot.CurveMonotone,
			Slot:  slot,
		}
	}
	return &hyperplot.Chart{
		ID:       "latency",
		Title:    "Latency",
		Width:    w,
		Height:   chartHeight(w, true),
		Animate:  true,
		Location: loc,
		X:        hyperplot.Axis{TimeDomain: [2]time.Time{start, end}},
		Y:        hyperplot.Axis{Label: "Latency (ms)", Unit: " ms", Zero: true},
		Marks: []hyperplot.Mark{
			line("p50", slotP50, func(s sample) float64 { return s.P50 }),
			line("p95", slotP95, func(s sample) float64 { return s.P95 }),
			line("p99", slotP99, func(s sample) float64 { return s.P99 }),
			hyperplot.RuleY{Y: sloMS, Label: "SLO 250 ms"},
		},
	}
}

// throughputChart stacks each selected region's request rate. The closed
// buckets, from one before the window on, are followed by one live point for
// the current second, so the chart moves every second while the stream is
// open.
func throughputChart(f filters, now time.Time, w float64, loc *time.Location) *hyperplot.Chart {
	now = now.Truncate(time.Second)
	last := now.Truncate(f.Range.Step)
	hist := windowHistory(f, last, now.Add(-f.Range.Span))
	xs := hyperplot.Map(hist, func(s sample) time.Time { return s.End })
	var live []float64
	if now.After(last) {
		xs = append(xs, now)
		live = liveRates(f, now)
	}
	marks := make([]hyperplot.Mark, len(f.Regions))
	for i, g := range f.Regions {
		ys := hyperplot.Map(hist, func(s sample) float64 { return math.Round(s.ByRegion[i]) })
		if live != nil {
			ys = append(ys, math.Round(live[i]))
		}
		// Straight segments: this chart is re-sent every second, and line
		// paths are about a third the size of smooth ones.
		marks[i] = hyperplot.Area{Name: g.Key, X: hyperplot.Times(xs), Y: ys, Slot: g.Slot}
	}
	return &hyperplot.Chart{
		ID:       "throughput",
		Title:    "Throughput",
		Width:    w,
		Height:   chartHeight(w, false),
		Animate:  true,
		Location: loc,
		Stack:    true,
		X:        hyperplot.Axis{TimeDomain: [2]time.Time{now.Add(-f.Range.Span), now}},
		Y:        hyperplot.Axis{Label: "Requests per second", Unit: " req/s"},
		Marks:    marks,
	}
}

// requestsChart compares the request counts of the selected regions over
// the last few closed windows, grouped or stacked. Windows close rarely
// (every 3 minutes to every hour), so this chart is usually unchanged and
// its polls are answered with 304 Not Modified.
func requestsChart(f filters, now time.Time, w float64, loc *time.Location) *hyperplot.Chart {
	win := f.Range.Window
	ws := requestWindows(f, now, loc)
	cats := hyperplot.Categories(hyperplot.Map(ws, func(w window) string { return w.Label }))
	marks := make([]hyperplot.Mark, len(f.Regions))
	for i, g := range f.Regions {
		marks[i] = hyperplot.Bar{
			Name: g.Key,
			X:    cats,
			Y: hyperplot.Map(ws, func(w window) float64 {
				return math.Round(g.rate(w.Start, win) * win.Seconds())
			}),
			Slot: g.Slot,
		}
	}
	return &hyperplot.Chart{
		ID:      "requests",
		Title:   "Requests by region",
		Width:   w,
		Height:  chartHeight(w, false),
		Animate: true,
		Stack:   f.Stacked,
		X:       hyperplot.Axis{Label: "Window start"},
		Y:       hyperplot.Axis{Label: "Requests"},
		Marks:   marks,
	}
}

// scatterChart plots latency against payload size for a sample of
// requests from each selected region. The sample is drawn per bar window,
// so it changes as rarely as the bar chart.
func scatterChart(f filters, now time.Time, w float64) *hyperplot.Chart {
	at := now.Truncate(f.Range.Window)
	marks := make([]hyperplot.Mark, len(f.Regions))
	for i, g := range f.Regions {
		kb, ms := payloads(g, at, f.Range.Window, scatterN)
		marks[i] = hyperplot.Dot{Name: g.Key, X: hyperplot.Floats(kb), Y: ms, Slot: g.Slot}
	}
	return &hyperplot.Chart{
		ID:      "scatter",
		Title:   "Latency vs payload size",
		Width:   w,
		Height:  chartHeight(w, false),
		Animate: true,
		X:       hyperplot.Axis{Label: "Payload (KB)", Unit: " KB", Zero: true},
		Y:       hyperplot.Axis{Label: "Latency (ms)", Unit: " ms", Zero: true},
		Marks:   marks,
	}
}

// ---- KPI tiles ----------------------------------------------------------

// kpi is one stat tile: a value, its change against the previous period
// and a sparkline of the current period.
type kpi struct {
	ID     string // stable element id, so morphs update tiles in place
	Label  string
	Value  string
	Unit   string
	Delta  string // signed relative change, such as "+4.2%"
	Dir    string // "up", "down" or "flat": picks the arrow icon
	Tone   string // "good", "bad" or "neutral": colors the arrow only
	Judge  string // "better" or "worse", spelled out for screen readers
	Period string // "previous hour"
	Spark  template.HTML
}

// kpis computes the tiles for the closed buckets ending at end and their
// changes against the period before.
func kpis(f filters, end time.Time) ([]kpi, error) {
	cur, prev := history(f, end), history(f, end.Add(-f.Range.Span))
	step := f.Range.Step.Seconds()
	type metric struct {
		id, label, unit string
		upIsGood        int // 1: up is good, -1: up is bad, 0: neither
		total           func([]sample) float64
		point           func(sample) float64
		format          func(float64) string
	}
	metrics := []metric{
		{
			id: "kpi-requests", label: "Requests",
			total: func(h []sample) float64 {
				var n float64
				for _, s := range h {
					n += s.Rate * step
				}
				return n
			},
			point:  func(s sample) float64 { return s.Rate },
			format: func(v float64) string { return scale.FormatCompact(v, 1) },
		},
		{
			id: "kpi-p95", label: "p95 latency", unit: "ms", upIsGood: -1,
			total:  func(h []sample) float64 { return weighted(h, func(s sample) float64 { return s.P95 }) },
			point:  func(s sample) float64 { return s.P95 },
			format: func(v float64) string { return scale.FormatNumber(v, 0) },
		},
		{
			id: "kpi-errors", label: "Error rate", unit: "%", upIsGood: -1,
			total:  func(h []sample) float64 { return 100 * weighted(h, func(s sample) float64 { return s.Errors }) },
			point:  func(s sample) float64 { return s.Errors },
			format: func(v float64) string { return scale.FormatNumber(v, 2) },
		},
		{
			id: "kpi-apdex", label: "Apdex", upIsGood: 1,
			total:  func(h []sample) float64 { return weighted(h, apdex) },
			point:  apdex,
			format: func(v float64) string { return scale.FormatNumber(v, 2) },
		},
	}
	out := make([]kpi, len(metrics))
	for i, m := range metrics {
		now, before := m.total(cur), m.total(prev)
		spark, err := hyperplot.Sparkline{
			Values: downsample(hyperplot.Map(cur, m.point), sparkPoints),
			Width:  120,
			Height: 32,
			Label:  m.label + ", " + f.Range.Title,
			Area:   true,
		}.Render()
		if err != nil {
			return nil, err
		}
		k := kpi{
			ID:     m.id,
			Label:  m.label,
			Value:  m.format(now),
			Unit:   m.unit,
			Period: f.Range.Period,
			Spark:  template.HTML(spark), // rendered by hyperplot, which escapes all text
		}
		k.Delta, k.Dir = change(now, before)
		switch {
		case k.Dir == "flat" || m.upIsGood == 0:
			k.Tone = "neutral"
		case (k.Dir == "up") == (m.upIsGood > 0):
			k.Tone, k.Judge = "good", "better"
		default:
			k.Tone, k.Judge = "bad", "worse"
		}
		out[i] = k
	}
	return out, nil
}

// weighted averages v over the buckets, weighted by request rate.
func weighted(h []sample, v func(sample) float64) float64 {
	var sum, n float64
	for _, s := range h {
		sum += s.Rate * v(s)
		n += s.Rate
	}
	if n == 0 {
		return 0
	}
	return sum / n
}

// apdex is a simulated Apdex score for a bucket: 1 when the tail is fast
// and nothing fails, lower as p95 latency and the error rate grow.
func apdex(s sample) float64 {
	return math.Max(0, math.Min(1, 1.02-0.0009*s.P95-5*s.Errors))
}

// change formats the relative change from before to now, such as "+4.2%",
// and reports its direction. Changes under 0.05% count as flat.
func change(now, before float64) (delta, dir string) {
	if before == 0 {
		return "n/a", "flat"
	}
	rel := 100 * (now - before) / before
	text := scale.FormatNumber(math.Abs(rel), 1) + "%"
	switch {
	case math.Abs(rel) < 0.05:
		return "0.0%", "flat"
	case rel > 0:
		return "+" + text, "up"
	default:
		return scale.Minus + text, "down"
	}
}

// downsample averages v into at most n points, so every sparkline has the
// same density whatever the range.
func downsample(v []float64, n int) []float64 {
	if len(v) <= n {
		return v
	}
	out := make([]float64, n)
	for i := range n {
		lo, hi := i*len(v)/n, (i+1)*len(v)/n
		var sum float64
		for _, x := range v[lo:hi] {
			sum += x
		}
		out[i] = sum / float64(hi-lo)
	}
	return out
}
