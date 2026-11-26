package main

import (
	"net/http"
	"net/url"
	"time"

	"github.com/ironystock/hyperplot/hx"
)

// The two gestures of docs/spec.md §5, as this page uses them. Both are
// ordinary htmx requests: the extension adds hp-* parameters, the server
// answers with HTML.
//
//   - Select (over-limit chart): a click on a sensor's bar, or Enter on a
//     focused one, opens that sensor in the detail. The tile links and the
//     alert links open the same detail without a pointer (§5.5).
//   - Brush (detail chart): dragging across the chart, or the from/to form,
//     fixes the detail's x window to the range. The zoom is page state:
//     from=…&to=… in the page URL and in the polling element's hx-get, so
//     polls stay zoomed, the address bar can be shared and Back undoes it.

// ---- Select ----------------------------------------------------------------

// selectedSensor reads which sensor a /detail request opens: a select of
// the over-limit chart (hp-chart, hp-x), or a plain sensor parameter from
// a tile or alert link. A select of another chart or of an unknown code is
// no selection (docs/spec.md §5.2), and neither is a missing one.
func selectedSensor(r *http.Request) (sensor, bool) {
	if sel, ok := hx.Selected(r); ok {
		if sel.Chart != "" && sel.Chart != overLimitID {
			return sensor{}, false
		}
		// hp-x is the bar's category key, which is the code itself: the
		// server wrote it, so an exact match is enough.
		return sensorByCode(sel.X)
	}
	return sensorByCode(r.URL.Query().Get("sensor"))
}

// pageSensor is the sensor a page or poll URL names, or the default. A
// poll never fails over a bad parameter.
func pageSensor(r *http.Request) sensor {
	if s, ok := sensorByCode(r.URL.Query().Get("sensor")); ok {
		return s
	}
	s, _ := sensorByCode(defaultSensor)
	return s
}

// ---- Zoom ------------------------------------------------------------------

const (
	minZoom = 10 * time.Minute   // a shorter range is widened around its middle
	maxZoom = 6 * time.Hour      // a longer one keeps its newest end
	maxBack = 7 * 24 * time.Hour // how far into the past a zoom may reach
)

// zoom is a fixed window of the detail chart. The zero zoom is the live,
// sliding window.
type zoom struct{ From, To time.Time }

func (z zoom) on() bool { return !z.To.IsZero() }

// newZoom turns a requested range into one the page can show: ordered,
// whole minutes, inside the data (from maxBack before the last closed
// minute to that minute) and between minZoom and maxZoom long. It reports
// false for a range that lies wholly outside the data, such as a brush
// from year 1 or in year 9999: docs/spec.md §7 says to bound such a range
// by the data and otherwise ignore it (§5.3).
//
// Fed its own result with the same now, it returns it unchanged. A later
// now can cut a zoom near the oldest end, so zoomFromQuery compares
// instead of clipping again.
func newZoom(t0, t1, now time.Time) (zoom, bool) {
	if t1.Before(t0) {
		t0, t1 = t1, t0 // docs/spec.md §5.3: never assume hp-x0 ≤ hp-x1
	}
	last := now.Truncate(minute)
	oldest := last.Add(-maxBack)
	if !t1.After(oldest) || t0.After(last) {
		return zoom{}, false
	}
	t0 = t0.Truncate(minute)
	if r := t1.Truncate(minute); r.Before(t1) {
		t1 = r.Add(minute)
	}
	t0, t1 = maxTime(t0, oldest), minTime(t1, last)
	if t1.Sub(t0) > maxZoom {
		t0 = t1.Add(-maxZoom) // keep the newest end
	}
	if t1.Sub(t0) < minZoom {
		mid := t0.Add(t1.Sub(t0) / 2)
		t0 = mid.Add(-minZoom / 2).Truncate(minute)
		t1 = t0.Add(minZoom)
		if t1.After(last) {
			t0, t1 = last.Add(-minZoom), last
		}
		if t0.Before(oldest) {
			t0, t1 = oldest, oldest.Add(minZoom)
		}
	}
	return zoom{From: t0.UTC(), To: t1.UTC()}, true
}

func minTime(a, b time.Time) time.Time {
	if a.Before(b) {
		return a
	}
	return b
}

func maxTime(a, b time.Time) time.Time {
	if a.After(b) {
		return a
	}
	return b
}

// zoomFromQuery reads the canonical from/to parameters that a zoom put in
// the page URL and the polling element's hx-get. They are drawn exactly as
// they are, never clipped again, so every poll of a zoom shows the window
// the toolbar and the address bar name. Without from and to it is the
// live window (ok). ok is false when they do not parse or are no longer
// canonical: edited by hand, or cut by the 7-day history as the clock
// moved on. A poll then answers 204 and keeps its last frame.
func zoomFromQuery(q url.Values, now time.Time) (zoom, bool) {
	if q.Get("from") == "" && q.Get("to") == "" {
		return zoom{}, true
	}
	from, err0 := time.Parse(time.RFC3339, q.Get("from"))
	to, err1 := time.Parse(time.RFC3339, q.Get("to"))
	if err0 != nil || err1 != nil {
		return zoom{}, false
	}
	z, ok := newZoom(from, to, now)
	if !ok || !z.From.Equal(from) || !z.To.Equal(to) {
		return zoom{}, false
	}
	return z, true
}

// addZoom adds z's canonical parameters, from and to in RFC 3339 UTC, to q.
func addZoom(q url.Values, z zoom) {
	if z.on() {
		q.Set("from", z.From.UTC().Format(time.RFC3339))
		q.Set("to", z.To.UTC().Format(time.RFC3339))
	}
}

// brushZoom reads a brush of the detail chart (hp-x0, hp-x1). It reports
// false when the request carries no brush, names another chart, or has
// values that do not parse or lie wholly outside the data, which the spec
// says to ignore (§5.3, §7). The from/to form sends datetime-local wall
// times without an offset; TimesIn reads them in loc, the chart's time
// zone.
func brushZoom(r *http.Request, now time.Time, loc *time.Location) (zoom, bool) {
	b, ok := hx.Brushed(r)
	if !ok || (b.Chart != "" && b.Chart != trendID) {
		return zoom{}, false
	}
	t0, t1, err := b.TimesIn(loc)
	if err != nil {
		return zoom{}, false
	}
	return newZoom(t0, t1, now)
}

// pageURL is the address of the page showing s's detail over z: what a
// select or a zoom pushes into the browser history (HX-Push-Url).
func pageURL(s sensor, z zoom) string {
	q := url.Values{"sensor": {s.Code}}
	addZoom(q, z)
	return "/?" + q.Encode()
}

// ---- Zoom toolbar -------------------------------------------------------------

// zoomBar is the toolbar above the detail chart: what the chart shows (a
// polite live region, so a zoom is announced, docs/spec.md §5.5) and the
// from/to form that zooms without a pointer.
type zoomBar struct {
	Zoomed   bool
	Status   string
	Code     string // the sensor, a hidden field of the form
	From, To string // datetime-local values in the chart's time zone
	LiveURL  string // the page without the zoom, for the no-script link
	LiveGet  string // the live chart through htmx
}

// datetimeLocal is the value format of <input type="datetime-local">.
const datetimeLocal = "2006-01-02T15:04"

func newZoomBar(s sensor, z zoom, now time.Time, loc *time.Location) zoomBar {
	b := zoomBar{
		Zoomed:  z.on(),
		Code:    s.Code,
		LiveURL: pageURL(s, zoom{}) + "#detail",
		LiveGet: "/detail/chart?" + url.Values{"sensor": {s.Code}, "zoom": {"out"}}.Encode(),
	}
	from, to := z.From, z.To
	if z.on() {
		b.Status = "Zoomed to " + rangeText(from, to, now, loc) + "."
	} else {
		to = now.Truncate(minute)
		from = to.Add(-liveSpan)
		b.Status = "Live: the last two hours."
	}
	b.From, b.To = from.In(loc).Format(datetimeLocal), to.In(loc).Format(datetimeLocal)
	return b
}

// rangeText writes a time range for people: "2:05 PM to 2:45 PM" when it
// lies within today, and with the dates otherwise ("Feb 24, 2:05 PM to
// Feb 24, 2:45 PM"), so a screen reader announcing a zoom into last week
// does not make it sound like today.
func rangeText(from, to, now time.Time, loc *time.Location) string {
	from, to = from.In(loc), to.In(loc)
	if sameDay(from, now.In(loc)) && sameDay(to, now.In(loc)) {
		return from.Format("3:04 PM") + " to " + to.Format("3:04 PM")
	}
	return from.Format("Jan 2, 3:04 PM") + " to " + to.Format("Jan 2, 3:04 PM")
}

// sameDay reports whether a and b, in the same location, fall on one
// calendar day.
func sameDay(a, b time.Time) bool {
	ay, am, ad := a.Date()
	by, bm, bd := b.Date()
	return ay == by && am == bm && ad == bd
}

// chartURL is the detail chart's polling URL: the sensor and any zoom.
func chartURL(s sensor, z zoom) string {
	q := url.Values{"sensor": {s.Code}}
	addZoom(q, z)
	return "/detail/chart?" + q.Encode()
}
