package main

import (
	"fmt"
	"hash/fnv"
	"html/template"
	"math"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/ironystock/hyperplot"
	"github.com/ironystock/hyperplot/hx"
	"github.com/ironystock/hyperplot/scale"
)

// The two gesture demos of docs/spec.md §5. Both are ordinary requests:
// the extension turns a brush or a select into an htmx request with hp-*
// parameters, and the server answers with HTML.
//
//   - Brush to zoom (latency): dragging across the chart, or the from/to
//     form beside it, fixes the chart's x window to the range. The zoom is
//     page state: its canonical form is from=…&to=… in the page URL and in
//     the polling element's hx-get, so polls stay zoomed, the address bar
//     can be bookmarked and Back undoes it.
//   - Select to drill down (requests by region): clicking a bar, Enter on
//     a focused bar, or one of the window links under the chart opens that
//     window in detail, for the clicked region or for every selected one.

// ---- Zoom ----------------------------------------------------------------

const (
	minZoom    = time.Minute        // a smaller range is widened around its middle
	maxZoom    = 24 * time.Hour     // a larger one keeps its newest end
	maxBack    = 7 * 24 * time.Hour // how far into the past a zoom may reach
	zoomPoints = 120                // at most this many buckets in a zoomed window
)

// zoomSteps are the bucket sizes of a zoomed latency chart. A zoom uses the
// smallest that keeps the window at or under zoomPoints buckets, so zooming
// in reveals finer buckets.
var zoomSteps = []time.Duration{
	time.Second, 2 * time.Second, 5 * time.Second, 10 * time.Second, 15 * time.Second, 30 * time.Second,
	time.Minute, 2 * time.Minute, 3 * time.Minute, 5 * time.Minute, 10 * time.Minute, 15 * time.Minute,
	30 * time.Minute, time.Hour,
}

// zoom is a fixed window of the latency chart. The zero zoom is the live,
// sliding window of the range preset.
type zoom struct{ From, To time.Time }

func (z zoom) on() bool { return !z.To.IsZero() }

// step is the zoomed chart's bucket size.
func (z zoom) step() time.Duration {
	span := z.To.Sub(z.From)
	for _, s := range zoomSteps {
		if span/s <= zoomPoints {
			return s
		}
	}
	return zoomSteps[len(zoomSteps)-1]
}

// query is the zoom's canonical query, "from=…&to=…" in RFC 3339 UTC, or
// "" for the live window.
func (z zoom) query() string {
	if !z.on() {
		return ""
	}
	return url.Values{
		"from": {z.From.UTC().Format(time.RFC3339)},
		"to":   {z.To.UTC().Format(time.RFC3339)},
	}.Encode()
}

// newZoom turns a requested range into a window the dashboard can show:
// ordered, whole seconds, ending no later than now, at least minZoom and at
// most maxZoom long, and no more than maxBack in the past. It is
// idempotent for a later now, so a zoom read back from a URL on every poll
// stays the same window.
func newZoom(t0, t1, now time.Time) zoom {
	if t1.Before(t0) {
		t0, t1 = t1, t0
	}
	t0 = t0.Truncate(time.Second)
	if r := t1.Truncate(time.Second); r.Before(t1) {
		t1 = r.Add(time.Second)
	}
	live := now.Truncate(time.Second)
	if t1.After(live) {
		t1 = live
	}
	if t0.After(t1) {
		t0 = t1
	}
	if t1.Sub(t0) > maxZoom {
		t0 = t1.Add(-maxZoom)
	}
	if t1.Sub(t0) < minZoom {
		t0 = t0.Add(t1.Sub(t0) / 2).Add(-minZoom / 2).Truncate(time.Second)
		t1 = t0.Add(minZoom)
		if t1.After(live) {
			t0, t1 = live.Add(-minZoom), live
		}
	}
	if oldest := live.Add(-maxBack); t0.Before(oldest) {
		t0 = oldest
		if t1.Sub(t0) < minZoom {
			t1 = t0.Add(minZoom)
		}
	}
	return zoom{From: t0.UTC(), To: t1.UTC()}
}

// zoomFromQuery reads the canonical from/to parameters. Anything missing or
// unparsable is the live window.
func zoomFromQuery(q url.Values, now time.Time) zoom {
	from, err := time.Parse(time.RFC3339, q.Get("from"))
	if err != nil {
		return zoom{}
	}
	to, err := time.Parse(time.RFC3339, q.Get("to"))
	if err != nil {
		return zoom{}
	}
	return newZoom(from, to, now)
}

// brushZoom reads a brush of the latency chart (hp-x0, hp-x1). It reports
// false when the request carries no brush, names another chart, or has
// values that do not parse, which the spec says to ignore (§5.3). A plain
// datetime-local form sends wall times without an offset; they are read in
// loc, the chart's time zone.
func brushZoom(r *http.Request, now time.Time, loc *time.Location) (zoom, bool) {
	b, ok := hx.Brushed(r)
	if !ok || (b.Chart != "" && b.Chart != "latency") {
		return zoom{}, false
	}
	t0, t1, err := b.TimesIn(loc)
	if err != nil {
		return zoom{}, false
	}
	return newZoom(t0, t1, now), true
}

// zoomedLatencyChart is the latency chart over z at z's bucket size. Like
// the live window it draws one bucket beyond each edge, clipped, so the
// lines reach both sides; buckets that have not closed yet are left out
// and fill in on later polls.
func zoomedLatencyChart(f filters, z zoom, now time.Time, w float64, loc *time.Location) *hyperplot.Chart {
	step := z.step()
	first := z.From.Truncate(step)
	if !first.Before(z.From) {
		first = first.Add(-step)
	}
	last := z.To.Truncate(step)
	if last.Before(z.To) {
		last = last.Add(step)
	}
	if closed := now.Truncate(step); last.After(closed) {
		last = closed
	}
	var hist []sample
	for e := first; !e.After(last); e = e.Add(step) {
		hist = append(hist, bucket(f, e, step))
	}
	return latencyFrame(hist, z.From, z.To, w, loc)
}

// zoomBar is the toolbar above the latency chart: what the chart shows (a
// polite live region, so a zoom is announced) and the from/to form that
// zooms without a pointer (docs/spec.md §5.5).
type zoomBar struct {
	Zoomed   bool
	Status   string
	From, To string // datetime-local values in the chart's time zone
	Hidden   []field
	ResetURL string // the page URL of the live window, for the no-script link
	ResetGet string // the same through htmx
}

// field is a hidden form input.
type field struct{ Name, Value string }

// datetimeLocal is the value format of <input type="datetime-local">.
const datetimeLocal = "2006-01-02T15:04:05"

func newZoomBar(f filters, z zoom, now time.Time, loc *time.Location) zoomBar {
	q := f.query()
	b := zoomBar{
		Zoomed:   z.on(),
		ResetURL: "/?" + q,
		ResetGet: "/charts/latency?" + q + "&zoom=out",
		Hidden:   []field{{"range", f.Range.Key}},
	}
	if f.NoneSelected {
		b.Hidden = append(b.Hidden, field{"region", ""})
	} else {
		for _, g := range f.Regions {
			b.Hidden = append(b.Hidden, field{"region", g.Key})
		}
	}
	from, to := z.From, z.To
	if z.on() {
		b.Status = "Zoomed to " + rangeText(from, to, loc) + " (" + spanText(to.Sub(from)) +
			", " + bucketText(z.step()) + " buckets)."
	} else {
		to = latencyEnd(f, now)
		from = to.Add(-f.Range.Span)
		b.Status = "Live: " + strings.ToLower(f.Range.Title[:1]) + f.Range.Title[1:] + "."
	}
	b.From, b.To = from.In(loc).Format(datetimeLocal), to.In(loc).Format(datetimeLocal)
	return b
}

// rangeText writes a time range for people: "2:05 PM to 2:15:30 PM", with
// the dates when it does not lie within one day.
func rangeText(from, to time.Time, loc *time.Location) string {
	from, to = from.In(loc), to.In(loc)
	if from.YearDay() == to.YearDay() && from.Year() == to.Year() {
		return clockText(from) + " to " + clockText(to)
	}
	return from.Format("Jan 2, ") + clockText(from) + " to " + to.Format("Jan 2, ") + clockText(to)
}

// clockText writes a time of day, with seconds only when it has some.
func clockText(t time.Time) string {
	if t.Second() == 0 {
		return t.Format("3:04 PM")
	}
	return t.Format("3:04:05 PM")
}

// spanText writes a duration in words: "1 minute", "90 seconds" becomes
// "1 minute 30 seconds", "2 hours".
func spanText(d time.Duration) string {
	units := []struct {
		d    time.Duration
		name string
	}{{time.Hour, "hour"}, {time.Minute, "minute"}, {time.Second, "second"}}
	var parts []string
	for _, u := range units {
		if n := int64(d / u.d); n > 0 {
			d -= time.Duration(n) * u.d
			part := strconv.FormatInt(n, 10) + " " + u.name
			if n != 1 {
				part += "s"
			}
			parts = append(parts, part)
		}
	}
	if len(parts) == 0 {
		return "0 seconds"
	}
	return strings.Join(parts, " ")
}

// bucketText names a bucket size as an adjective: "5-second", "3-minute".
func bucketText(d time.Duration) string {
	s := spanText(d)
	if n, unit, ok := strings.Cut(s, " "); ok && !strings.Contains(unit, " ") {
		return n + "-" + strings.TrimSuffix(unit, "s")
	}
	return s
}

// pageURL is the address of the dashboard showing f and z, which a zoom
// pushes into the browser history (HX-Push-Url).
func pageURL(f filters, z zoom) string {
	u := "/?" + f.query()
	if z.on() {
		u += "&" + z.query()
	}
	if f.Stacked {
		u += "&bars=stacked"
	}
	return u
}

// ---- Drill-down ----------------------------------------------------------

// detailBuckets is the number of buckets a window detail splits its
// window into.
const detailBuckets = 20

// window is one bar group of the requests chart: a closed period of the
// range's window size, labeled by its start. The label is also the bar's
// category, which a select sends back as hp-x, so the labels of the
// windows shown at one time are unique.
type window struct {
	Start time.Time
	Label string
}

// requestWindows lists the closed windows the requests chart shows at now,
// oldest first.
//
// Windows are aligned in absolute time, not on loc's wall clock, so in a
// zone with a half-hour offset an hourly window starts at :30; its label
// then keeps the minutes ("7:30 AM", not "7 AM"). On the day a zone falls
// back, two windows can start at the same wall-clock time ("1 AM" twice);
// those labels gain the zone's abbreviation ("1 AM EDT", "1 AM EST"), or
// its offset when the abbreviations match too, since a wall-clock time and
// an offset name one instant.
func requestWindows(f filters, now time.Time, loc *time.Location) []window {
	win := f.Range.Window
	end := now.Truncate(win)
	starts := make([]time.Time, f.Range.Windows)
	layout := "3 PM"
	for i := range starts {
		starts[i] = end.Add(-time.Duration(f.Range.Windows-i) * win).In(loc)
		if starts[i].Minute() != 0 || win%time.Hour != 0 {
			layout = "3:04 PM"
		}
	}
	labels := make([]string, len(starts))
	for i, t := range starts {
		labels[i] = t.Format(layout)
	}
	for _, suffix := range []string{" MST", " UTC-07:00"} {
		dup := repeated(labels)
		if dup == nil {
			break
		}
		for i, t := range starts {
			if dup[labels[i]] {
				labels[i] = t.Format(layout + suffix)
			}
		}
	}
	out := make([]window, len(starts))
	for i, t := range starts {
		out[i] = window{Start: t.In(now.Location()), Label: labels[i]}
	}
	return out
}

// repeated returns the labels that appear more than once in labels, or
// nil when they are all unique.
func repeated(labels []string) map[string]bool {
	seen := make(map[string]int, len(labels))
	for _, l := range labels {
		seen[l]++
	}
	var dup map[string]bool
	for l, n := range seen {
		if n > 1 {
			if dup == nil {
				dup = make(map[string]bool)
			}
			dup[l] = true
		}
	}
	return dup
}

// windowLink is one "details" link under the requests chart: the select
// gesture as a plain link (docs/spec.md §5.5).
type windowLink struct {
	ID    string // stable while the window is shown, so a poll's morph keeps the node with its window
	Label string
	Href  string // the page with that window's detail open, without script
	Get   string // the detail fragment, through htmx
}

// windowLinkID returns the id of w's link: "window-" and the 8-hex FNV-1a
// hash of its label, the bar group's category (DESIGN.md "Stable keys").
// The links sit in #requests-body, which polls with innerMorph; without
// ids the morph pairs them by position, so when a window closed, every
// link node took the next window's text and URL, and a focused link
// silently pointed at a window the reader never chose. With an id, a node
// stays with its window, and a link whose window slid out is removed. The
// labels shown at one time are unique (requestWindows), so are the ids.
func windowLinkID(w window) string {
	h := fnv.New32a()
	h.Write([]byte(w.Label)) // never fails
	return fmt.Sprintf("window-%08x", h.Sum32())
}

func windowLinks(f filters, ws []window) []windowLink {
	q := f.query()
	out := make([]windowLink, len(ws))
	for i, w := range ws {
		sel := url.Values{hx.ParamChart: {"requests"}, hx.ParamX: {w.Label}}.Encode()
		out[i] = windowLink{
			ID:    windowLinkID(w),
			Label: w.Label,
			Href:  "/?" + q + "&" + sel + "#requests-detail",
			Get:   "/detail/requests?" + q + "&" + sel,
		}
	}
	return out
}

// stat is one figure of a window detail.
type stat struct{ Label, Value, Unit string }

// detailData is the content of #requests-detail.
type detailData struct {
	Open    bool
	Status  string // the polite live region's text
	Notice  string // shown when nothing is open
	Heading string
	Sub     string
	Stats   []stat
	Title   string // the chart's title, which names its table
	SVG     template.HTML
	Table   template.HTML
	BackURL string // the page without the detail, for the no-script link
	BackGet string // the empty detail, through htmx
}

// selectedWindow reads a select of the requests chart. A select that names
// another chart, a window no longer shown or nothing at all is no
// selection (docs/spec.md §5.2). The region is the clicked bar's series
// when it is one of the selected regions, and otherwise every selected
// region: Enter on a band chart names no series.
func selectedWindow(r *http.Request, f filters, ws []window) (w window, rs []region, notice string, ok bool) {
	sel, ok := hx.Selected(r)
	if !ok || (sel.Chart != "" && sel.Chart != "requests") {
		return window{}, nil, "", false
	}
	found := false
	for _, c := range ws {
		if c.Label == sel.X {
			w, found = c, true
		}
	}
	if !found {
		return window{}, nil, "That window is no longer on the chart. Pick another one.", false
	}
	rs = f.Regions
	for _, g := range f.Regions {
		if g.Key == sel.Series {
			rs = []region{g}
		}
	}
	return w, rs, "", true
}

// detailChart plots the request rate of regions rs across the window that
// starts at start, in detailBuckets buckets, stacked. Its first point is
// the bucket that ends where the window starts, so the areas begin at the
// left edge.
func detailChart(f filters, rs []region, start time.Time, w float64, loc *time.Location) *hyperplot.Chart {
	step := f.Range.Window / detailBuckets
	ends := make([]time.Time, detailBuckets+1)
	for i := range ends {
		ends[i] = start.Add(time.Duration(i) * step)
	}
	marks := make([]hyperplot.Mark, len(rs))
	for i, g := range rs {
		marks[i] = hyperplot.Area{
			Name: g.Key,
			X:    hyperplot.Times(ends),
			Y:    hyperplot.Map(ends, func(e time.Time) float64 { return math.Round(g.rate(e.Add(-step), step)) }),
			Slot: g.Slot,
		}
	}
	return &hyperplot.Chart{
		ID:       "drill",
		Title:    "Requests per second, " + regionNames(rs) + ", " + rangeText(start, start.Add(f.Range.Window), loc),
		Width:    w,
		Height:   chartHeight(w, false) - 40,
		Animate:  true,
		Location: loc,
		Stack:    true,
		X:        hyperplot.Axis{TimeDomain: [2]time.Time{start, start.Add(f.Range.Window)}},
		Y:        hyperplot.Axis{Label: "Requests per second", Unit: " req/s"},
		Marks:    marks,
	}
}

// detailStats sums up a window for regions rs. The request count uses the
// same formula as the bar, so the detail repeats the number that was
// clicked.
func detailStats(f filters, rs []region, start time.Time) []stat {
	win := f.Range.Window
	step := win / detailBuckets
	var total, peak, p95, errs, weight float64
	for _, g := range rs {
		total += math.Round(g.rate(start, win) * win.Seconds())
	}
	sub := filters{Range: f.Range, Regions: rs}
	for i := 1; i <= detailBuckets; i++ {
		s := bucket(sub, start.Add(time.Duration(i)*step), step)
		peak = math.Max(peak, s.Rate)
		p95 += s.Rate * s.P95
		errs += s.Rate * s.Errors
		weight += s.Rate
	}
	if weight > 0 {
		p95 /= weight
		errs /= weight
	}
	return []stat{
		{"Requests", scale.FormatNumber(total, 0), ""},
		{"Peak rate", scale.FormatNumber(peak, 0), "req/s"},
		{"p95 latency", scale.FormatNumber(p95, 0), "ms"},
		{"Error rate", scale.FormatNumber(100*errs, 2), "%"},
	}
}

// regionNames joins region keys: "us-east", "us-east and eu-west",
// "us-east, eu-west and ap-south".
func regionNames(rs []region) string {
	names := make([]string, len(rs))
	for i, g := range rs {
		names[i] = g.Key
	}
	if len(names) < 2 {
		return strings.Join(names, "")
	}
	return strings.Join(names[:len(names)-1], ", ") + " and " + names[len(names)-1]
}
