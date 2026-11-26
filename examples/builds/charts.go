package main

import (
	"math"
	"slices"
	"time"

	"github.com/ironystock/hyperplot"
	"github.com/ironystock/hyperplot/scale"
)

// Chart sizes. The extension reports each container's width (hp-w) and
// the handlers render to it, so the default is only the first paint's.
// Heights are fixed per chart: a container also holds a "View as table"
// disclosure, so its height is not the chart's (docs/htmx.md#height).
const (
	defaultWidth = 960
	narrowWidth  = 560 // below this, charts get shorter
	trendGap     = 16  // the gap between small multiples, as in app.css
	minTrendCol  = 300 // narrowest small multiple before the grid drops a column
)

// Color slots. Pipelines use slots 1–3 (pinned in data.go), the build
// results 6 and 8, the percentiles 5 and 7, so no color means two things
// on the page. Results are never told apart by color alone: the legend,
// the stack order (failed on top), the tooltips and the table name them.
const (
	slotPassed = 6
	slotFailed = 8
	slotP50    = 7
	slotP95    = 5
)

// height returns a chart's height at width w: tall below narrowWidth too,
// minus 60 px, so a phone does not get a sliver.
func height(w, h float64) float64 {
	if w < narrowWidth {
		return h - 60
	}
	return h
}

// durationsChart is the scatter of build durations over [from, to]: one
// Dot series per pipeline, at each build's finish time. Builds slower than
// twice their pipeline's typical time get a label with their number, which
// the renderer draws beside the point where it fits (and always puts in the
// tooltip and the table).
func durationsChart(f filters, bs []build, from, to time.Time, w float64, loc *time.Location) *hyperplot.Chart {
	marks := make([]hyperplot.Mark, len(f.Pipelines))
	for i, p := range f.Pipelines {
		var xs []time.Time
		var ys []float64
		var labels []string
		for j := range bs {
			b := &bs[j]
			if b.Pipeline.Key != p.Key || b.End.Before(from) || b.End.After(to) {
				continue
			}
			xs = append(xs, b.End)
			ys = append(ys, b.Minutes)
			label := ""
			if b.Slow() {
				label = b.Name()
			}
			labels = append(labels, label)
		}
		marks[i] = hyperplot.Dot{Name: p.Key, X: hyperplot.Times(xs), Y: ys, Slot: p.Slot, Labels: labels}
	}
	return &hyperplot.Chart{
		ID:       "durations", // explicit and stable: morphs and selects match on it
		Title:    "Build durations",
		Width:    w,
		Height:   height(w, 320),
		Animate:  true,
		Location: loc,
		X:        hyperplot.Axis{TimeDomain: [2]time.Time{from, to}},
		Y:        hyperplot.Axis{Label: "Duration (min)", Unit: " min", Zero: true},
		Marks:    marks,
	}
}

// daysChart stacks passed and failed builds per day. Its categories are
// the day labels, which a select sends back as hp-x.
func daysChart(f filters, ds []day, bs []build, w float64) *hyperplot.Chart {
	cats := hyperplot.Categories(hyperplot.Map(ds, func(d day) string { return d.Label }))
	passed := make([]float64, len(ds))
	failed := make([]float64, len(ds))
	for j := range bs {
		b := &bs[j]
		i := dayIndex(ds, b.End)
		switch {
		case i < 0:
		case b.Passed:
			passed[i]++
		default:
			failed[i]++
		}
	}
	return &hyperplot.Chart{
		ID:       "days",
		Title:    "Builds per day, " + f.Range.Title,
		Width:    w,
		Height:   height(w, 260),
		Animate:  true,
		Stack:    true,
		BarWidth: 48, // the default 24 px looks thin with a week of days
		X:        hyperplot.Axis{Label: "Day"},
		Y:        hyperplot.Axis{Label: "Builds", Format: func(v float64) string { return scale.FormatNumber(v, 0) }},
		Marks: []hyperplot.Mark{
			hyperplot.Bar{Name: "passed", X: cats, Y: passed, Slot: slotPassed},
			hyperplot.Bar{Name: "failed", X: cats, Y: failed, Slot: slotFailed},
		},
	}
}

// trendLayout is how the small multiples share the trends container: as
// many columns as fit at minTrendCol each (at most one per chart), and the
// width of each chart.
func trendLayout(w float64, charts int) (cols int, chartW float64) {
	cols = max(1, min(charts, int((w+trendGap)/(minTrendCol+trendGap))))
	return cols, math.Floor((w - float64(cols-1)*trendGap) / float64(cols))
}

// trendCharts are the small multiples, one per selected pipeline: the
// daily p50 and p95 duration of its builds, each day plotted at its start
// (a day without builds is a gap in the lines). One chart per pipeline,
// rather than six lines in one, keeps the percentile colors the same in
// every chart. They share a y domain, so the pipelines compare at a glance,
// and their width comes from the container's (trendLayout).
func trendCharts(f filters, ds []day, bs []build, w float64) (charts []*hyperplot.Chart, cols int) {
	type series struct{ p50, p95 []float64 }
	all := make([]series, len(f.Pipelines))
	yMax := 10.0
	for j, p := range f.Pipelines {
		s := series{make([]float64, len(ds)), make([]float64, len(ds))}
		for i, d := range ds {
			var ms []float64
			for k := range bs {
				b := &bs[k]
				if b.Pipeline.Key == p.Key && !b.End.Before(d.Start) && b.End.Before(d.End) {
					ms = append(ms, b.Minutes)
				}
			}
			slices.Sort(ms)
			s.p50[i], s.p95[i] = percentile(ms, 50), percentile(ms, 95)
			if !math.IsNaN(s.p95[i]) {
				yMax = math.Max(yMax, s.p95[i])
			}
		}
		all[j] = s
	}
	yMax = math.Ceil(yMax/10) * 10
	xs := hyperplot.Times(hyperplot.Map(ds, func(d day) time.Time { return d.Start }))
	cols, chartW := trendLayout(w, len(f.Pipelines))
	for j, p := range f.Pipelines {
		charts = append(charts, &hyperplot.Chart{
			ID:       "trend-" + p.Key,
			Title:    p.Key + " build duration, daily p50 and p95",
			Width:    chartW,
			Height:   220,
			Animate:  true,
			Location: ds[0].Start.Location(),
			X:        hyperplot.Axis{TimeDomain: [2]time.Time{ds[0].Start, ds[len(ds)-1].Start}},
			Y:        hyperplot.Axis{Label: p.Key + " (min)", Unit: " min", Domain: [2]float64{0, yMax}},
			Marks: []hyperplot.Mark{
				hyperplot.Line{Name: "p50", X: xs, Y: all[j].p50, Slot: slotP50, Points: true},
				hyperplot.Line{Name: "p95", X: xs, Y: all[j].p95, Slot: slotP95, Points: true},
			},
		})
	}
	return charts, cols
}

// dayIndex returns the index of the day in ds that holds t, or -1.
func dayIndex(ds []day, t time.Time) int {
	for i, d := range ds {
		if !t.Before(d.Start) && t.Before(d.End) {
			return i
		}
	}
	return -1
}
