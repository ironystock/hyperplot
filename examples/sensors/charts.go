package main

import (
	"html/template"
	"net/url"
	"strings"
	"time"

	"github.com/ironystock/hyperplot"
	"github.com/ironystock/hyperplot/scale"
)

// Chart sizes. The client extension reports each container's width (hp-w)
// and the handlers render to it, so the width is only the first-paint
// size. Heights are fixed: a container also holds its "View as table"
// disclosure, so its height is not the chart's.
const (
	defaultWidth = 640
	narrowWidth  = 520 // below this, charts get shorter
	liveSpan     = 2 * time.Hour
	sparkSpan    = 30 * time.Minute
)

// Chart ids. They are explicit and stable, because live updates pair the
// old and new chart by id (docs/spec.md §4.1). The detail keeps its id
// when it switches sensors, so a select morphs one chart into the next.
const (
	trendID     = "trend"
	overLimitID = "overlimit"
)

func chartHeight(w, h float64) float64 {
	if w < narrowWidth {
		return h - 50
	}
	return h
}

// ---- Detail: one sensor, with its limits as rules -----------------------

// detailChart plots s's one-minute readings over the live window (the two
// hours ending with the last closed minute) or over a zoom. Like the
// dashboard's latency chart it draws one bucket beyond the left edge,
// clipped, so the line reaches the edge while the window slides
// (docs/spec.md §4.4).
func detailChart(s sensor, z zoom, now time.Time, w float64, loc *time.Location) *hyperplot.Chart {
	last := now.Truncate(minute)
	from, to := last.Add(-liveSpan), last
	if z.on() {
		from, to = z.From, z.To
	}
	first := from.Truncate(minute)
	if !first.Before(from) {
		first = first.Add(-minute)
	}
	end := to.Truncate(minute)
	if end.Before(to) {
		end = end.Add(minute) // one bucket past a zoom's right edge, clipped too
	}
	if end.After(last) {
		end = last // buckets that have not closed yet fill in on later polls
	}
	ends, vals := s.series(first, end)
	k := s.Kind
	return &hyperplot.Chart{
		ID:       trendID,
		Title:    s.Name() + " (" + s.Code + ")",
		Width:    w,
		Height:   chartHeight(w, 300),
		Animate:  true,
		Location: loc,
		X:        hyperplot.Axis{TimeDomain: [2]time.Time{from, to}},
		Y:        hyperplot.Axis{Label: k.Axis, Unit: k.axisUnit()},
		Marks: []hyperplot.Mark{
			hyperplot.Line{Name: s.Code, X: hyperplot.Times(ends), Y: vals, Curve: hyperplot.CurveMonotone, Slot: k.Slot},
			// Thresholds are annotations, not series: no color slot, no
			// legend entry, and the y domain always reaches them, so both
			// limits stay in view.
			hyperplot.RuleY{Y: s.Warn, Label: "Warning " + s.format(s.Warn)},
			hyperplot.RuleY{Y: s.Alarm, Label: "Alarm " + s.format(s.Alarm)},
		},
	}
}

// format writes a reading with its unit: "24.0 °C", "1,000 ppm", "55%".
func (s sensor) format(v float64) string {
	return scale.FormatNumber(v, s.Kind.Decimals) + s.Kind.axisUnit()
}

// ---- Over-limit: the chart you select a sensor on -----------------------

// overLimitChart stacks, per sensor, the minutes of the last hour spent at
// the warning and at the alarm level. Its categories are the sensor
// codes, so a click on a bar (or Enter on a focused one) sends that code
// back as hp-x (docs/spec.md §5.2). It changes once a minute at most, so
// nearly all of its polls are answered with 304 Not Modified.
func overLimitChart(now time.Time, w float64) *hyperplot.Chart {
	last := now.Truncate(minute)
	codes := make(hyperplot.Categories, len(sensors))
	warn := make([]float64, len(sensors))
	alarm := make([]float64, len(sensors))
	for i, s := range sensors {
		codes[i] = s.Code
		wm, am := minutesOver(s, last)
		warn[i], alarm[i] = float64(wm), float64(am)
	}
	return &hyperplot.Chart{
		ID:      overLimitID,
		Title:   "Minutes over limit in the last hour",
		Width:   w,
		Height:  chartHeight(w, 260),
		Animate: true,
		Stack:   true,
		X:       hyperplot.Axis{Label: "Sensor"},
		Y:       hyperplot.Axis{Label: "Minutes", Unit: " min"},
		Marks: []hyperplot.Mark{
			hyperplot.Bar{Name: "Warning", X: codes, Y: warn, Slot: 4},
			hyperplot.Bar{Name: "Alarm", X: codes, Y: alarm, Slot: 2},
		},
	}
}

// ---- Tiles: the grid the SSE stream keeps live --------------------------

// tile is one sensor's stat tile. Its id is stable, so each SSE message
// morphs the tile in place.
type tile struct {
	ID    string // "tile-t-srv"
	Code  string
	Name  string
	Value string // the live reading, formatted
	Unit  string
	Level level
	Spark template.HTML
	Href  string // the page with this sensor's detail, without script
	Get   string // the detail fragment, through htmx
}

// tiles renders the tile grid at now: each sensor's live reading and a
// sparkline of its last half hour of closed minutes, ending in the live
// value.
func tiles(now time.Time) ([]tile, error) {
	last := now.Truncate(minute)
	out := make([]tile, len(sensors))
	for i, s := range sensors {
		_, vals := s.series(last.Add(-sparkSpan+minute), last)
		v := s.live(now)
		spark, err := hyperplot.Sparkline{
			Values: append(vals, v),
			Width:  128,
			Height: 32,
			Label:  s.Name() + ", last 30 minutes",
			Unit:   s.Kind.axisUnit(),
			Area:   true,
		}.HTML()
		if err != nil {
			return nil, err
		}
		out[i] = tile{
			ID:    "tile-" + strings.ToLower(s.Code),
			Code:  s.Code,
			Name:  s.Name(),
			Value: scale.FormatNumber(v, s.Kind.Decimals),
			Unit:  s.Kind.Unit,
			Level: s.judge(v),
			Spark: spark,
			Href:  pageURL(s, zoom{}) + "#detail",
			Get:   "/detail?" + url.Values{"sensor": {s.Code}}.Encode(),
		}
	}
	return out, nil
}

// ---- Alerts table ---------------------------------------------------------

// alertRow is one row of the alerts table. Its id names the sensor and
// the minute the alert began, which never change, so a morph keeps a
// reader's row in place while newer alerts push in above it.
type alertRow struct {
	ID      string // "alert-co2-mtg-20260303t1401": the sensor and the start in UTC
	Code    string
	Name    string
	Level   level
	Started string
	State   string // "Active, 6 min" or "Cleared after 4 min"
	Peak    string
	Href    string // the page zoomed to the alert, without script
	Get     string // the same detail through htmx
}

// alertsView is the content of #alerts.
type alertsView struct {
	Summary string
	Rows    []alertRow
}

func newAlertsView(now time.Time, loc *time.Location) alertsView {
	last := now.Truncate(minute)
	as := alerts(last)
	var active, alarms int
	rows := make([]alertRow, len(as))
	for i := range as {
		a := &as[i]
		if a.Active {
			active++
			if a.Level == levelAlarm {
				alarms++
			}
		}
		mins := int(a.End.Sub(a.Start)/minute) + 1
		state := "Cleared after " + plural(mins, "min")
		if a.Active {
			state = "Active, " + plural(mins, "min")
		}
		// The link zooms the detail to the alert with ten minutes either
		// side: the same request a brush across it would send.
		// An alert is always inside the history, so ok is true; if it
		// were not, z stays the live window.
		z, _ := newZoom(a.Start.Add(-10*minute), a.End.Add(10*minute), now)
		q := url.Values{"sensor": {a.Sensor.Code}}
		addZoom(q, z)
		rows[i] = alertRow{
			ID:      "alert-" + strings.ToLower(a.Sensor.Code) + "-" + a.Start.UTC().Format("20060102t1504"),
			Code:    a.Sensor.Code,
			Name:    a.Sensor.Name(),
			Level:   a.Level,
			Started: a.Start.In(loc).Format("3:04 PM"),
			State:   state,
			Peak:    a.Sensor.format(a.Peak),
			Href:    pageURL(a.Sensor, z) + "#detail",
			Get:     "/detail?" + q.Encode(),
		}
	}
	v := alertsView{Rows: rows}
	switch {
	case active == 0:
		v.Summary = "No active alerts."
	case alarms == 0:
		v.Summary = plural(active, "active alert") + ", no alarms."
	default:
		v.Summary = plural(active, "active alert") + ", " + plural(alarms, "alarm") + "."
	}
	return v
}

// plural writes a count and a noun: "1 min", "3 active alerts".
func plural(n int, noun string) string {
	s := scale.FormatNumber(float64(n), 0) + " " + noun
	if n != 1 && !strings.HasSuffix(noun, "min") {
		s += "s"
	}
	return s
}
