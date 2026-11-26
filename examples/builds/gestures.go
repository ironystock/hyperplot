package main

import (
	"net/http"
	"net/url"
	"slices"
	"strconv"
	"time"

	"github.com/ironystock/hyperplot/hx"
	"github.com/ironystock/hyperplot/scale"
)

// The two gestures of docs/spec.md §5, as ordinary requests: the extension
// turns a brush or a select into an htmx request with hp-* parameters, and
// the server answers with HTML, like for any other request.
//
//   - Brush to zoom (durations): dragging across the scatter, or the
//     from/to form above it, fixes the chart's x window to that range. The
//     zoom is page state: from=…&to=… in the page URL and in the polling
//     element's hx-get, so polls stay zoomed and Back undoes it.
//   - Select to drill down (builds per day): clicking a bar segment, Enter
//     on a focused day, or one of the day links under the chart lists that
//     day's builds, the failed or passed ones when a segment was clicked.

// ---- Zoom ----------------------------------------------------------------

// minZoom is the shortest zoom; a shorter brush is widened around its middle.
const minZoom = time.Hour

// zoom is a fixed window of the durations chart. The zero zoom is the
// whole range.
type zoom struct{ From, To time.Time }

func (z zoom) on() bool { return !z.To.IsZero() }

// query is the zoom's canonical query, "from=…&to=…" in RFC 3339 UTC.
func (z zoom) query() string {
	return url.Values{
		"from": {z.From.UTC().Format(time.RFC3339)},
		"to":   {z.To.UTC().Format(time.RFC3339)},
	}.Encode()
}

// newZoom turns a requested range into one the chart can show: whole
// minutes, inside [lo, hi] (the range's days) and at least minZoom long. It
// reports false for a range that misses [lo, hi] entirely. Fed its own
// result with the same lo and hi, it returns it unchanged; zoomFromQuery
// relies on that to tell a zoom that is still drawable from one that lo,
// which moves with the clock, has started to cut.
func newZoom(t0, t1, lo, hi time.Time) (zoom, bool) {
	if t1.Before(t0) {
		t0, t1 = t1, t0
	}
	if !t1.After(lo) || !t0.Before(hi) {
		return zoom{}, false
	}
	t0 = t0.Truncate(time.Minute)
	if r := t1.Truncate(time.Minute); r.Before(t1) {
		t1 = r.Add(time.Minute)
	}
	t0, t1 = later(t0, lo), earlier(t1, hi)
	if t1.Sub(t0) < minZoom {
		mid := t0.Add(t1.Sub(t0) / 2).Truncate(time.Minute)
		t0, t1 = mid.Add(-minZoom/2), mid.Add(minZoom/2)
		if t0.Before(lo) {
			t0, t1 = lo, lo.Add(minZoom)
		}
		if t1.After(hi) {
			t0, t1 = hi.Add(-minZoom), hi
		}
	}
	return zoom{From: t0.UTC(), To: t1.UTC()}, true
}

func later(a, b time.Time) time.Time {
	if a.Before(b) {
		return b
	}
	return a
}

func earlier(a, b time.Time) time.Time {
	if a.After(b) {
		return b
	}
	return a
}

// zoomFromQuery reads the canonical from/to parameters that zoomTo put in
// the page URL and the poller's hx-get. They are drawn exactly as they
// are, never clipped again, so every poll of a zoom shows the same window
// the toolbar and the address bar name. Without from and to it is the
// whole range (ok). ok is false when they do not parse or are no longer
// canonical: edited by hand, or cut by lo after the days moved on (a zoom
// on the oldest day, the next midnight). A poll then answers 204 and keeps
// its last frame until the reader zooms again or shows all.
func zoomFromQuery(q url.Values, lo, hi time.Time) (zoom, bool) {
	if q.Get("from") == "" && q.Get("to") == "" {
		return zoom{}, true
	}
	t0, err0 := time.Parse(time.RFC3339, q.Get("from"))
	t1, err1 := time.Parse(time.RFC3339, q.Get("to"))
	if err0 != nil || err1 != nil {
		return zoom{}, false
	}
	z, ok := newZoom(t0, t1, lo, hi)
	if !ok || !z.From.Equal(t0) || !z.To.Equal(t1) {
		return zoom{}, false
	}
	return z, true
}

// brushZoom reads a brush of the durations chart (hp-x0, hp-x1). It
// reports false when the request carries no brush, names another chart or
// has values that do not parse or miss the range, all of which the spec
// says to ignore (docs/spec.md §5.3). The extension sends RFC 3339 times;
// the from/to form sends datetime-local wall times, which TimesIn reads in
// loc, the chart's time zone (§5.5).
func brushZoom(r *http.Request, lo, hi time.Time, loc *time.Location) (zoom, bool) {
	b, ok := hx.Brushed(r)
	if !ok || (b.Chart != "" && b.Chart != "durations") {
		return zoom{}, false
	}
	t0, t1, err := b.TimesIn(loc)
	if err != nil {
		return zoom{}, false
	}
	return newZoom(t0, t1, lo, hi)
}

// zoomBar is the toolbar above the durations chart: a polite live region
// saying what the chart shows, so a zoom is announced, and the from/to
// form that zooms without a pointer (docs/spec.md §5.5).
type zoomBar struct {
	Zoomed   bool
	Status   string
	From, To string  // datetime-local values in the chart's time zone
	Hidden   []field // the filters, so the form keeps them
	ResetURL string  // the page without the zoom, for the no-script link
	ResetGet string  // the same through htmx
}

// field is a hidden form input.
type field struct{ Name, Value string }

// datetimeLocal is the value format of <input type="datetime-local">.
const datetimeLocal = "2006-01-02T15:04"

func newZoomBar(f filters, z zoom, lo, hi time.Time, loc *time.Location) zoomBar {
	q := f.query()
	b := zoomBar{
		Zoomed:   z.on(),
		ResetURL: "/?" + q,
		ResetGet: "/charts/durations?" + q + "&zoom=out",
		Hidden:   []field{{"range", f.Range.Key}},
	}
	// The same fields as query(): an empty branch or theme is left out,
	// but an empty pipeline is kept, because it says every box was cleared.
	if f.Branch != "" {
		b.Hidden = append(b.Hidden, field{"branch", f.Branch})
	}
	if f.Theme != "" {
		b.Hidden = append(b.Hidden, field{"theme", f.Theme})
	}
	if f.NoneSelected {
		b.Hidden = append(b.Hidden, field{"pipeline", ""})
	} else if len(f.Pipelines) < len(pipelines) {
		for _, p := range f.Pipelines {
			b.Hidden = append(b.Hidden, field{"pipeline", p.Key})
		}
	}
	from, to := lo, hi
	if z.on() {
		from, to = z.From, z.To
		b.Status = "Zoomed to " + when(from, loc) + " to " + when(to, loc) + "."
	} else {
		b.Status = "Showing " + firstLower(f.Range.Title) + "."
	}
	b.From, b.To = from.In(loc).Format(datetimeLocal), to.In(loc).Format(datetimeLocal)
	return b
}

// when writes an instant for people: "Mar 3, 2:05 PM".
func when(t time.Time, loc *time.Location) string { return t.In(loc).Format("Jan 2, 3:04 PM") }

// firstLower lowercases an ASCII first letter: "Last 7 days" → "last 7 days".
func firstLower(s string) string {
	if s == "" {
		return s
	}
	return string(s[0]|0x20) + s[1:]
}

func itoa(n int) string { return strconv.Itoa(n) }

// plural writes a count and a noun: "1 build", "3 failed builds".
func plural(n int, noun string) string {
	if n == 1 {
		return "1 " + noun
	}
	return itoa(n) + " " + noun + "s"
}

// pageURL is the address of the page showing f and z, which a zoom pushes
// into the browser history (HX-Push-Url).
func pageURL(f filters, z zoom) string {
	u := "/?" + f.query()
	if z.on() {
		u += "&" + z.query()
	}
	return u
}

// ---- Drill-down ----------------------------------------------------------

// dayLink is one link under the days chart: the select gesture as a plain
// link (docs/spec.md §5.5). Its id comes from the date, so a poll's morph
// keeps each link with its day as days roll over.
type dayLink struct {
	ID, Label string
	Href      string // the page with the day's builds open, without script
	Get       string // the builds list, through htmx
}

func dayLinks(f filters, ds []day) []dayLink {
	q := f.query()
	out := make([]dayLink, len(ds))
	for i, d := range ds {
		sel := selectQuery(d.Label, "")
		out[i] = dayLink{
			ID:    "day-" + d.Start.Format("2006-01-02"),
			Label: d.Label,
			Href:  "/?" + q + "&" + sel + "#day-detail",
			Get:   "/detail/day?" + q + "&" + sel,
		}
	}
	return out
}

// selectQuery is the query of a select of the days chart: the parameters
// the extension sends for a click (docs/spec.md §5.1).
func selectQuery(label, series string) string {
	v := url.Values{hx.ParamChart: {"days"}, hx.ParamX: {label}}
	if series != "" {
		v.Set(hx.ParamSeries, series)
	}
	return v.Encode()
}

// detailData is the content of #day-detail.
type detailData struct {
	Open    bool
	Status  string // the polite live region's text
	Notice  string // shown when nothing is open
	Heading string
	Sub     string
	Tabs    []tab // all, passed, failed: the series choice without a pointer
	Rows    []buildRow
	Close   dayLink // its Href and Get close the list
}

// tab is a link that narrows the list to one result.
type tab struct {
	Label, Href, Get string
	Current          bool
}

// buildRow is one row of the builds table, ready to print.
type buildRow struct {
	ID, Name, Pipeline, Branch, Result, Duration, Finished string
	Slot                                                   int
	Passed, Slow                                           bool
}

// selectedDay reads a select of the days chart. A select that names
// another chart, a day no longer shown or nothing at all is no selection
// (docs/spec.md §5.2). The series, "passed" or "failed", narrows the list;
// Enter on a focused day names none, and an unknown one is ignored.
func selectedDay(r *http.Request, ds []day) (d day, series, notice string, ok bool) {
	sel, ok := hx.Selected(r)
	if !ok || (sel.Chart != "" && sel.Chart != "days") {
		return day{}, "", "", false
	}
	i := slices.IndexFunc(ds, func(d day) bool { return d.Label == sel.X })
	if i < 0 {
		return day{}, "", "That day is no longer on the chart. Pick another one.", false
	}
	if sel.Series == "passed" || sel.Series == "failed" {
		series = sel.Series
	}
	return ds[i], series, "", true
}

// newDetail lists the builds of the selected day, newest first.
func newDetail(r *http.Request, f filters, ds []day, bs []build, loc *time.Location) detailData {
	q := f.query()
	d := detailData{Close: dayLink{Href: "/?" + q, Get: "/detail/day?" + q}}
	sel, series, notice, ok := selectedDay(r, ds)
	if !ok {
		d.Notice, d.Status = notice, notice
		return d
	}
	var passed, failed int
	for i := len(bs) - 1; i >= 0; i-- {
		b := bs[i]
		if b.End.Before(sel.Start) || !b.End.Before(sel.End) {
			continue
		}
		if b.Passed {
			passed++
		} else {
			failed++
		}
		if series == "passed" && !b.Passed || series == "failed" && b.Passed {
			continue
		}
		row := buildRow{
			ID: b.ID(), Name: b.Name(), Pipeline: b.Pipeline.Key, Branch: b.Branch,
			Duration: scale.FormatNumber(b.Minutes, 1) + " min",
			Finished: b.End.In(loc).Format("3:04 PM"),
			Slot:     b.Pipeline.Slot, Passed: b.Passed, Slow: b.Slow(),
			Result: "Failed",
		}
		if b.Passed {
			row.Result = "Passed"
		}
		d.Rows = append(d.Rows, row)
	}
	noun := "build"
	if series != "" {
		noun = series + " build"
	}
	d.Open = true
	d.Heading = string(noun[0]-0x20) + noun[1:] + "s on " + sel.Start.Format("Monday, Jan 2")
	d.Sub = plural(passed+failed, "build") + " finished: " + itoa(passed) + " passed, " + itoa(failed) + " failed."
	d.Status = "Showing " + plural(len(d.Rows), noun) + " from " + sel.Label + "."
	for _, t := range []struct{ label, series string }{
		{"All " + itoa(passed+failed), ""}, {"Passed " + itoa(passed), "passed"}, {"Failed " + itoa(failed), "failed"},
	} {
		sq := selectQuery(sel.Label, t.series)
		d.Tabs = append(d.Tabs, tab{
			Label:   t.label,
			Href:    "/?" + q + "&" + sq + "#day-detail",
			Get:     "/detail/day?" + q + "&" + sq,
			Current: t.series == series,
		})
	}
	return d
}
