package main

import (
	"context"
	"encoding/xml"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"
)

// epoch is a fixed instant, so every test sees the same data.
var epoch = time.Date(2026, 3, 3, 14, 20, 0, 0, time.UTC)

// zone is the chart's time zone in the tests: an hour ahead of UTC, so a
// datetime-local value read in the wrong zone shows.
var zone = time.FixedZone("UTC+1", 3600)

func testServer() http.Handler {
	return newServer(func() time.Time { return epoch }, zone).routes()
}

func get(t *testing.T, h http.Handler, target string) *httptest.ResponseRecorder {
	t.Helper()
	r := httptest.NewRequestWithContext(t.Context(), http.MethodGet, target, nil)
	r.Header.Set("HX-Request", "true")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	return w
}

// brush is the query the extension sends for a brush from x0 to x1.
func brush(x0, x1 string) string {
	return "/charts/latency?" + url.Values{"hp-chart": {"latency"}, "hp-x0": {x0}, "hp-x1": {x1}}.Encode()
}

var domainAttr = regexp.MustCompile(`data-hp-x="time (\d+) (\d+)"`)

// domain returns the x domain the chart in body was drawn with.
func domain(t *testing.T, body string) (time.Time, time.Time) {
	t.Helper()
	m := domainAttr.FindStringSubmatch(body)
	if m == nil {
		t.Fatal("no time domain in the chart")
	}
	ms := func(s string) time.Time {
		n, _ := strconv.ParseInt(s, 10, 64)
		return time.UnixMilli(n).UTC()
	}
	return ms(m[1]), ms(m[2])
}

func at(hhmm string) time.Time {
	t, _ := time.Parse(time.RFC3339, "2026-03-03T"+hhmm+":00Z")
	return t
}

func TestPage(t *testing.T) {
	w := get(t, testServer(), "/")
	if w.Code != http.StatusOK {
		t.Fatalf("status %d", w.Code)
	}
	if got := w.Header().Get("Content-Security-Policy"); got != contentSecurityPolicy {
		t.Errorf("CSP = %q", got)
	}
	body := w.Body.String()
	checkMarkup(t, body, 1)
	for _, want := range []string{
		// htmx 4.0.2, pinned with SRI exactly as in examples/dashboard, not deferred.
		`<script src="https://cdn.jsdelivr.net/npm/htmx.org@4.0.2/dist/htmx.min.js" integrity="sha384-6NzMyCdh08UZ4b4M17SJJKQLdDijx4z2IqqKLKD0Q/PzCFQLpFuk3konY0WgqbTy" crossorigin="anonymous"></script>`,
		`<script defer src="/static/hx-plot.js"></script>`,
		`<script defer src="/static/hx-plot-brush.js"></script>`,
		`hx-plot:brush hx-get="/charts/latency" hx-trigger="htmx:plot:brush"`,
		`hx-target="#latency-body" hx-swap="outerMorph" hx-sync="replace"`,
		`<div id="latency-body" class="chart" hx-get="/charts/latency" hx-trigger="every 5s"`,
		`hx-sync="closest .zoom:drop"`,
		`hx-sync="closest .zoom:replace"`,
		`name="hp-x0"`, `name="hp-x1"`, `role="status"`,
		`value="2026-03-03T14:50:00"`, // the live window's start, 13:50 UTC, in the chart's zone
	} {
		if !strings.Contains(body, want) {
			t.Errorf("page lacks %s", want)
		}
	}
	for _, unused := range []string{"hx-sse", "hx-ptag"} {
		if strings.Contains(body, unused) {
			t.Errorf("page loads %s", unused)
		}
	}
	if from, to := domain(t, body); !from.Equal(epoch.Add(-live)) || !to.Equal(epoch) {
		t.Errorf("live domain %v to %v", from, to)
	}
}

// TestBrushZooms is the recipe's point: a brush answers with the whole
// polling element, zoomed, whose hx-get keeps the range; HX-Push-Url names
// the page URL of the view; the status line rides along as a partial.
func TestBrushZooms(t *testing.T) {
	h := testServer()
	ordered := get(t, h, brush("2026-03-03T14:05:00Z", "2026-03-03T14:15:00Z"))
	if ordered.Code != http.StatusOK {
		t.Fatalf("status %d", ordered.Code)
	}
	body := ordered.Body.String()
	checkMarkup(t, body, 1)
	const q = "from=2026-03-03T14%3A05%3A00Z&to=2026-03-03T14%3A15%3A00Z"
	if got := ordered.Header().Get("HX-Push-Url"); got != "/?"+q {
		t.Errorf("HX-Push-Url %q", got)
	}
	if want := `<div id="latency-body" class="chart" hx-get="/charts/latency?` + strings.ReplaceAll(q, "&", "&amp;") + `"`; !strings.HasPrefix(body, want) {
		t.Errorf("answer starts %.160q, want the polling element %q", body, want)
	}
	if from, to := domain(t, body); !from.Equal(at("14:05")) || !to.Equal(at("14:15")) {
		t.Errorf("zoomed domain %v to %v", from, to)
	}
	for _, want := range []string{
		`<hx-partial hx-target="#zoom-bar" hx-swap="innerMorph">`,
		"Zoomed to 3:05:00 PM to 3:15:00 PM.", // in the chart's zone
		`value="2026-03-03T15:05:00"`,
		"Back to live",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("answer lacks %s", want)
		}
	}

	// spec/spec.md §5.3: the ends may arrive in either order.
	inverted := get(t, h, brush("2026-03-03T14:15:00Z", "2026-03-03T14:05:00Z"))
	if inverted.Body.String() != body || inverted.Header().Get("HX-Push-Url") != ordered.Header().Get("HX-Push-Url") {
		t.Error("an inverted brush answers differently from the ordered one")
	}
}

// TestBrushRanges covers the values a server must accept and how they are
// read: offsets, datetime-local in the chart's zone, a range larger than
// the data, which is clipped to it, and a range under a minute, which is
// widened around its middle as in the dashboard and the Python and Node
// examples, so a small drag inside a zoomed chart still zooms.
func TestBrushRanges(t *testing.T) {
	h := testServer()
	for _, tt := range []struct {
		name, x0, x1 string
		from, to     time.Time
	}{
		{"offsets", "2026-03-03T09:05:00-05:00", "2026-03-03T15:15:00+01:00", at("14:05"), at("14:15")},
		{"datetime-local, chart zone", "2026-03-03T15:05", "2026-03-03T15:15:30", at("14:05"), at("14:15").Add(30 * time.Second)},
		{"fractions dropped", "2026-03-03T14:05:00.75Z", "2026-03-03T14:15:00.2Z", at("14:05"), at("14:15")},
		{"huge range clipped to the data", "0001-01-01T00:00:00Z", "9999-12-31T23:59:59Z", epoch.Add(-history), epoch},
		{"runs into the future", "2026-03-03T14:10:00Z", "2026-03-04T00:00:00Z", at("14:10"), epoch},
		{"under a minute, widened", "2026-03-03T14:05:00Z", "2026-03-03T14:05:30Z", at("14:04").Add(45 * time.Second), at("14:05").Add(45 * time.Second)},
		{"one instant, widened", "2026-03-03T14:05:00Z", "2026-03-03T14:05:00Z", at("14:04").Add(30 * time.Second), at("14:05").Add(30 * time.Second)},
		{"short, at the newest end", "2026-03-03T14:19:50Z", "2026-03-03T14:25:00Z", at("14:19"), epoch},
		{"short, at the oldest end", "2026-03-03T08:19:00Z", "2026-03-03T08:20:10Z", epoch.Add(-history), epoch.Add(-history + minZoom)},
	} {
		w := get(t, h, brush(tt.x0, tt.x1))
		if w.Code != http.StatusOK {
			t.Errorf("%s: status %d", tt.name, w.Code)
			continue
		}
		if from, to := domain(t, w.Body.String()); !from.Equal(tt.from) || !to.Equal(tt.to) {
			t.Errorf("%s: domain %v to %v, want %v to %v", tt.name, from, to, tt.from, tt.to)
		}
	}
}

// TestBrushIgnored: a brush whose values do not parse, or that lies wholly
// outside the data, is ignored (spec/spec.md §5.3 SHOULD): a 204, which
// htmx does not swap, no pushed URL, and never a 5xx.
func TestBrushIgnored(t *testing.T) {
	h := testServer()
	for _, tt := range []struct{ name, x0, x1 string }{
		{"not a time", "abc", "2026-03-03T14:15:00Z"},
		{"a number", "1772546700000", "1772547300000"},
		{"NaN", "NaN", "NaN"},
		{"overflow", "1e999", "2026-03-03T14:15:00Z"},
		{"markup", "<script>alert(1)</script>", "2026-03-03T14:15:00Z"},
		{"huge value", strings.Repeat("9", 100_000), "2026-03-03T14:15:00Z"},
		{"bad month", "2026-13-03T14:05:00Z", "2026-03-03T14:15:00Z"},
		{"all in the future", "2026-03-04T00:00:00Z", "2026-03-05T00:00:00Z"},
		{"older than the data", "2026-03-01T00:00:00Z", "2026-03-01T01:00:00Z"},
	} {
		w := get(t, h, brush(tt.x0, tt.x1))
		if w.Code != http.StatusNoContent || w.Body.Len() != 0 || w.Header().Get("HX-Push-Url") != "" {
			t.Errorf("%s: status %d, %d-byte body, push %q; want an empty 204",
				tt.name, w.Code, w.Body.Len(), w.Header().Get("HX-Push-Url"))
		}
	}
	// A brush of another chart is not ours to zoom.
	other := "/charts/latency?hp-chart=other&hp-x0=2026-03-03T14:05:00Z&hp-x1=2026-03-03T14:15:00Z"
	if w := get(t, h, other); w.Code != http.StatusNoContent {
		t.Errorf("another chart's brush: status %d, want 204", w.Code)
	}
}

// TestPolls: a poll answers with the chart alone (innerMorph); the URL of
// a zoomed element keeps its range. A from/to the server cannot draw as
// it is gets an empty 204, never the live chart under a "Zoomed" status.
func TestPolls(t *testing.T) {
	h := testServer()
	for _, tt := range []struct {
		name, query string
		from, to    time.Time // zero: an empty 204
	}{
		{"live", "", epoch.Add(-live), epoch},
		{"zoomed", "?from=2026-03-03T14:05:00Z&to=2026-03-03T14:15:00Z", at("14:05"), at("14:15")},
		{"one end only", "?hp-x0=2026-03-03T14:05:00Z", epoch.Add(-live), epoch},
		{"bad from", "?from=yesterday&to=2026-03-03T14:15:00Z", time.Time{}, time.Time{}},
		{"under a minute", "?from=2026-03-03T14:05:00Z&to=2026-03-03T14:05:30Z", time.Time{}, time.Time{}},
		{"not canonical", "?from=2026-03-03T14:05:00.5Z&to=2026-03-03T14:15:00Z", time.Time{}, time.Time{}},
	} {
		w := get(t, h, "/charts/latency"+tt.query)
		if tt.from.IsZero() {
			if w.Code != http.StatusNoContent || w.Body.Len() != 0 {
				t.Errorf("%s: status %d, %d-byte body; want an empty 204", tt.name, w.Code, w.Body.Len())
			}
			continue
		}
		if w.Code != http.StatusOK {
			t.Errorf("%s: status %d", tt.name, w.Code)
			continue
		}
		body := w.Body.String()
		if !strings.HasPrefix(body, "<svg ") || w.Header().Get("HX-Push-Url") != "" {
			t.Errorf("%s: a poll should get the chart alone", tt.name)
		}
		checkMarkup(t, body, 1)
		if from, to := domain(t, body); !from.Equal(tt.from) || !to.Equal(tt.to) {
			t.Errorf("%s: domain %v to %v", tt.name, from, to)
		}
	}
}

// TestZoomAgesOut: the polls of a zoom keep drawing exactly its range as
// the clock moves on; they never clip it again. Once its start leaves the
// data we keep, a poll gets an empty 204, so the zoomed chart, the status
// line and the address bar still agree, and a reload of the URL shows the
// live view and says so.
func TestZoomAgesOut(t *testing.T) {
	clock := epoch
	h := newServer(func() time.Time { return clock }, zone).routes()
	zoomed := get(t, h, brush("2026-03-03T14:00:00Z", "2026-03-03T14:19:00Z"))
	push := zoomed.Header().Get("HX-Push-Url")
	if zoomed.Code != http.StatusOK || !strings.HasPrefix(push, "/?") {
		t.Fatalf("brush: status %d, push %q", zoomed.Code, push)
	}
	poll := "/charts/latency" + push[1:]
	for _, after := range []time.Duration{0, time.Hour, 5*time.Hour + 40*time.Minute} {
		clock = epoch.Add(after)
		w := get(t, h, poll)
		if w.Code != http.StatusOK {
			t.Errorf("+%v: status %d", after, w.Code)
			continue
		}
		if from, to := domain(t, w.Body.String()); !from.Equal(at("14:00")) || !to.Equal(at("14:19")) {
			t.Errorf("+%v: domain %v to %v, want the zoom unchanged", after, from, to)
		}
	}
	for _, after := range []time.Duration{5*time.Hour + 50*time.Minute, 7 * time.Hour} {
		clock = epoch.Add(after)
		if w := get(t, h, poll); w.Code != http.StatusNoContent || w.Body.Len() != 0 {
			t.Errorf("+%v: status %d, %d-byte body; want an empty 204", after, w.Code, w.Body.Len())
		}
	}
	reload := get(t, h, push).Body.String()
	if !strings.Contains(reload, "Live: the last 30 minutes.") || strings.Contains(reload, "Zoomed to") {
		t.Error("a reload of an aged-out zoom should show and announce the live view")
	}
}

func TestBackToLive(t *testing.T) {
	w := get(t, testServer(), "/charts/latency?zoom=out")
	body := w.Body.String()
	if w.Code != http.StatusOK || w.Header().Get("HX-Push-Url") != "/" {
		t.Fatalf("status %d, push %q", w.Code, w.Header().Get("HX-Push-Url"))
	}
	if !strings.HasPrefix(body, `<div id="latency-body" class="chart" hx-get="/charts/latency" `) {
		t.Errorf("answer starts %.120q", body)
	}
	if strings.Contains(body, "Back to live") {
		t.Error("the live view offers Back to live")
	}
}

// TestPageViews: the pushed URL, the no-script form and junk all render
// the page, so a bookmark, Back and a plain form submit work.
func TestPageViews(t *testing.T) {
	h := testServer()
	for _, tt := range []struct {
		name, query string
		from, to    time.Time
	}{
		{"pushed URL", "?from=2026-03-03T14:05:00Z&to=2026-03-03T14:15:00Z", at("14:05"), at("14:15")},
		{"form without script", "?hp-chart=latency&hp-x0=2026-03-03T15:15:00&hp-x1=2026-03-03T15:05:00", at("14:05"), at("14:15")},
		{"junk", "?from=%3Cb%3E&hp-x0=x&hp-x1=y", epoch.Add(-live), epoch},
	} {
		w := get(t, h, "/"+tt.query)
		if w.Code != http.StatusOK {
			t.Errorf("%s: status %d", tt.name, w.Code)
			continue
		}
		body := w.Body.String()
		checkMarkup(t, body, 1)
		if strings.Contains(body, "<b>") {
			t.Errorf("%s: echoes markup", tt.name)
		}
		if from, to := domain(t, body); !from.Equal(tt.from) || !to.Equal(tt.to) {
			t.Errorf("%s: domain %v to %v", tt.name, from, to)
		}
	}
}

func TestAssets(t *testing.T) {
	h := testServer()
	for path, ct := range map[string]string{
		"/static/hx-plot.js":       "text/javascript; charset=utf-8",
		"/static/hx-plot-brush.js": "text/javascript; charset=utf-8",
		"/static/hx-plot.css":      "text/css; charset=utf-8",
		"/static/app.css":          "text/css; charset=utf-8",
	} {
		if w := get(t, h, path); w.Code != http.StatusOK || w.Header().Get("Content-Type") != ct {
			t.Errorf("%s: %d %q", path, w.Code, w.Header().Get("Content-Type"))
		}
	}
}

func TestRunStopsOnCancel(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if err := run(ctx, "127.0.0.1:0", http.NotFoundHandler()); err != nil {
		t.Fatal(err)
	}
}

// forbidden matches a style attribute or an inline event handler in a tag.
var forbidden = regexp.MustCompile(`(?i)<[^>]*\s(style|on[a-z]+)\s*=`)

// checkMarkup asserts that body has at least minSVGs well-formed <svg>
// elements and no style or on* attributes.
func checkMarkup(t *testing.T, body string, minSVGs int) {
	t.Helper()
	n := 0
	for rest := body; ; n++ {
		i := strings.Index(rest, "<svg")
		if i < 0 {
			break
		}
		j := strings.Index(rest[i:], "</svg>")
		if j < 0 {
			t.Fatal("unterminated <svg>")
		}
		if err := wellFormed(rest[i : i+j+len("</svg>")]); err != nil {
			t.Errorf("svg #%d: %v", n+1, err)
		}
		rest = rest[i+j:]
	}
	if n < minSVGs {
		t.Errorf("%d <svg> elements, want at least %d", n, minSVGs)
	}
	if m := forbidden.FindString(body); m != "" {
		t.Errorf("forbidden attribute in %q", m)
	}
}

func wellFormed(s string) error {
	d := xml.NewDecoder(strings.NewReader(s))
	for {
		_, err := d.Token()
		if errors.Is(err, io.EOF) {
			return nil
		}
		if err != nil {
			return err
		}
	}
}
