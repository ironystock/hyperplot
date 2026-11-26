package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/xml"
	"errors"
	"html"
	"io"
	"log"
	"net/http"
	"net/http/httptest"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/ironystock/hyperplot/hx"
)

// epoch is a fixed instant in the middle of a minute, so small clock steps
// never close a minute by accident.
var epoch = time.Date(2026, 3, 3, 14, 1, 15, 0, time.UTC)

// newTestServer returns a server with a fixed clock in UTC and a fast SSE
// tick.
func newTestServer(t *testing.T) *server {
	t.Helper()
	s, err := newServer(log.New(io.Discard, "", 0))
	if err != nil {
		t.Fatal(err)
	}
	s.now = func() time.Time { return epoch }
	s.loc = time.UTC
	s.tick = 10 * time.Millisecond
	return s
}

// reply is a recorded response.
type reply struct {
	Status int
	Header http.Header
	Body   string
}

// get performs one GET request against h.
func get(t *testing.T, h http.Handler, target string, header http.Header) reply {
	t.Helper()
	r := httptest.NewRequestWithContext(t.Context(), http.MethodGet, target, nil)
	for k, vs := range header {
		for _, v := range vs {
			r.Header.Add(k, v)
		}
	}
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	return reply{Status: w.Code, Header: w.Header(), Body: w.Body.String()}
}

// htmxHeader marks a request as coming from htmx, targeting an element.
func htmxHeader() http.Header {
	h := http.Header{}
	h.Set(hx.HeaderRequest, "true")
	h.Set(hx.HeaderRequestType, "partial")
	return h
}

// checkSVGs asserts that body holds at least minCount <svg> elements and
// that each one is well-formed XML.
func checkSVGs(t *testing.T, name, body string, minCount int) {
	t.Helper()
	n := 0
	for rest := body; ; {
		i := strings.Index(rest, "<svg")
		if i < 0 {
			break
		}
		j := strings.Index(rest[i:], "</svg>")
		if j < 0 {
			t.Fatalf("%s: unterminated <svg>", name)
		}
		if err := wellFormed(rest[i : i+j+len("</svg>")]); err != nil {
			t.Errorf("%s: svg #%d is not well-formed XML: %v", name, n+1, err)
		}
		rest = rest[i+j:]
		n++
	}
	if n < minCount {
		t.Errorf("%s: found %d <svg> elements, want at least %d", name, n, minCount)
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

// Inline styles and event handlers would break the page's CSP and leak
// script into markup. The patterns match attributes, not text.
var (
	styleAttr   = regexp.MustCompile(`<[^>]*\sstyle\s*=`)
	handlerAttr = regexp.MustCompile(`<[^>]*\son[a-z]+\s*=`)
	inlineCode  = regexp.MustCompile(`<script(\s[^>]*)?>\s*[^<\s]|<script>|<style[\s>]`)
)

// checkSafe asserts that body has no style attribute, no on* attribute and
// no inline <script> or <style>.
func checkSafe(t *testing.T, name, body string) {
	t.Helper()
	if m := styleAttr.FindString(body); m != "" {
		t.Errorf("%s: style attribute in %.80q", name, m)
	}
	if m := handlerAttr.FindString(body); m != "" {
		t.Errorf("%s: event handler attribute in %.80q", name, m)
	}
	if m := inlineCode.FindString(body); m != "" {
		t.Errorf("%s: inline script or style: %.80q", name, m)
	}
}

const (
	brushQ = "&hp-chart=trend&hp-x0=2026-03-03T13%3A20%3A00Z&hp-x1=2026-03-03T13%3A50%3A00Z"
	zoomQ  = "&from=2026-03-03T13%3A20%3A00Z&to=2026-03-03T13%3A50%3A00Z"
)

func TestRoutes(t *testing.T) {
	h := newTestServer(t).routes()
	tests := []struct {
		name        string
		target      string
		header      http.Header
		contentType string
		svgs        int    // minimum number of well-formed <svg> elements
		fragment    bool   // the whole body must be well-formed XML too
		contains    string // a marker the body must contain
	}{
		{"page", "/", nil, "text/html", 14, false, `hx-sse:connect="/stream/tiles"`},
		{"page with sensor", "/?sensor=CO2-MTG", nil, "text/html", 14, false, `Meeting room CO₂`},
		{"page zoomed", "/?sensor=T-SRV" + zoomQ, nil, "text/html", 14, false, `data-hp-x="time 1772544000000 1772545800000"`},
		{"page zoom form without script", "/?sensor=T-SRV" + brushQ, nil, "text/html", 14, false, `Zoomed to 1:20 PM to 1:50 PM.`},
		{"over-limit chart", "/charts/overlimit", htmxHeader(), "text/html", 1, true, `id="overlimit"`},
		{"detail chart", "/detail/chart?sensor=KW-HVAC", htmxHeader(), "text/html", 1, true, `id="trend"`},
		{"detail chart zoomed", "/detail/chart?sensor=KW-HVAC" + zoomQ, htmxHeader(), "text/html", 1, true, `data-hp-x="time 1772544000000 1772545800000"`},
		{"detail brush", "/detail/chart?sensor=KW-HVAC" + brushQ, htmxHeader(), "text/html", 1, false, `<div id="detail-body"`},
		{"detail zoom out", "/detail/chart?sensor=KW-HVAC&zoom=out", htmxHeader(), "text/html", 1, false, `<hx-partial hx-target="#detail-zoom"`},
		{"select", "/detail?hp-chart=overlimit&hp-x=RH-SRV&hp-series=Alarm", htmxHeader(), "text/html", 1, false, `Server room humidity`},
		{"tile link", "/detail?sensor=KW-LGT", htmxHeader(), "text/html", 1, false, `Lighting power`},
		{"alert link", "/detail?sensor=T-2E" + zoomQ, htmxHeader(), "text/html", 1, false, `Zoomed to 1:20 PM to 1:50 PM.`},
		{"hx-plot.css", "/static/hx-plot.css", nil, "text/css", 0, false, ".hp-s1{"},
		{"hx-plot.js", "/static/hx-plot.js", nil, "text/javascript", 0, false, "registerExtension"},
		{"hx-plot-brush.js", "/static/hx-plot-brush.js", nil, "text/javascript", 0, false, "brush"},
		{"app.css", "/static/app.css", nil, "text/css", 0, false, ".tile-link::after"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			res := get(t, h, tt.target, tt.header)
			if res.Status != http.StatusOK {
				t.Fatalf("status %d, want 200\n%s", res.Status, res.Body)
			}
			if ct := res.Header.Get("Content-Type"); !strings.HasPrefix(ct, tt.contentType) {
				t.Errorf("Content-Type %q, want %s", ct, tt.contentType)
			}
			if res.Header.Get("Content-Security-Policy") != contentSecurityPolicy {
				t.Error("missing or wrong Content-Security-Policy")
			}
			if !strings.Contains(res.Body, tt.contains) {
				t.Errorf("body lacks %q", tt.contains)
			}
			checkSVGs(t, tt.name, res.Body, tt.svgs)
			if strings.HasPrefix(tt.contentType, "text/html") {
				checkSafe(t, tt.name, res.Body)
			}
			if tt.fragment {
				if err := wellFormed("<root>" + res.Body + "</root>"); err != nil {
					t.Errorf("fragment is not well-formed: %v", err)
				}
			}
		})
	}
}

// TestUniqueIDs checks that every id on the page is unique: morphs, the
// table's aria references and hx-target all rely on it, and a chart id
// that repeats a page id (the detail chart was once "detail", like its
// panel) sends requests to the wrong element.
func TestUniqueIDs(t *testing.T) {
	h := newTestServer(t).routes()
	idAttr := regexp.MustCompile(`\sid="([^"]+)"`)
	for _, target := range []string{"/", "/?sensor=CO2-MTG" + zoomQ} {
		seen := map[string]bool{}
		for _, m := range idAttr.FindAllStringSubmatch(get(t, h, target, nil).Body, -1) {
			if seen[m[1]] {
				t.Errorf("%s: id %q appears twice", target, m[1])
			}
			seen[m[1]] = true
		}
	}
}

func TestUnknownRouteIs404(t *testing.T) {
	if res := get(t, newTestServer(t).routes(), "/nope", nil); res.Status != http.StatusNotFound {
		t.Errorf("status %d, want 404", res.Status)
	}
}

// TestPageHead checks that the page loads the pinned htmx release with its
// integrity hashes, only the extensions it uses, and no deferred htmx.
func TestPageHead(t *testing.T) {
	page := get(t, newTestServer(t).routes(), "/", nil).Body
	for _, want := range []string{
		`<script src="https://cdn.jsdelivr.net/npm/htmx.org@4.0.2/dist/htmx.min.js" integrity="sha384-6NzMyCdh08UZ4b4M17SJJKQLdDijx4z2IqqKLKD0Q/PzCFQLpFuk3konY0WgqbTy" crossorigin="anonymous"></script>`,
		`<script src="https://cdn.jsdelivr.net/npm/htmx.org@4.0.2/dist/ext/hx-sse.min.js" integrity="sha384-5zdqOOgEI+w1bvDhFjUeS1VYpPKkc1DFc4+WnvUe849JkM4u0s7JSeGpLon7/sRU" crossorigin="anonymous"></script>`,
		`<script src="https://cdn.jsdelivr.net/npm/htmx.org@4.0.2/dist/ext/hx-ptag.js" integrity="sha384-R20vL2ppRMMKzN1SVZdVpoeW1yH54eSH8rX3jz9NqpuWNqzwR5ngAqhCcKnK/b1s" crossorigin="anonymous"></script>`,
		`<script defer src="/static/hx-plot.js"></script>`,
		`<script defer src="/static/hx-plot-brush.js"></script>`,
		`<meta name="htmx-config" content='{"noSwap":[204,304,"4xx","5xx"]}'>`,
	} {
		if !strings.Contains(page, want) {
			t.Errorf("page lacks %s", want)
		}
	}
	if strings.Index(page, "htmx.min.js") > strings.Index(page, "hx-plot.js") {
		t.Error("hx-plot.js loads before htmx")
	}
}

// TestNoStyleOrHandlers checks every HTML response the page can get,
// including SSE messages (TestSSE) and error-ish requests (TestBadParams).
func TestNoStyleOrHandlers(t *testing.T) {
	s := newTestServer(t)
	b, err := s.liveFragment(epoch)
	if err != nil {
		t.Fatal(err)
	}
	checkSafe(t, "live fragment", string(b))
}

// TestPTagRoundTrip polls like hx-ptag does: the first poll gets a tag,
// echoing it gets a bodiless 304, and a different width is new content.
func TestPTagRoundTrip(t *testing.T) {
	h := newTestServer(t).routes()
	for _, target := range []string{
		"/charts/overlimit",
		"/detail/chart?sensor=T-SRV",
		"/detail/chart?sensor=CO2-2E" + zoomQ,
	} {
		t.Run(target, func(t *testing.T) {
			first := get(t, h, target, htmxHeader())
			tag := first.Header.Get(hx.HeaderPTag)
			if first.Status != http.StatusOK || tag == "" || first.Body == "" {
				t.Fatalf("first poll: status %d, tag %q, %d bytes", first.Status, tag, len(first.Body))
			}
			for _, echo := range []string{tag, "W/" + tag, " " + tag + " ", strings.Trim(tag, `"`)} {
				header := htmxHeader()
				header.Set(hx.HeaderPTag, echo)
				if again := get(t, h, target, header); again.Status != http.StatusNotModified || again.Body != "" {
					t.Errorf("echo %q: status %d with %d bytes, want a bodiless 304", echo, again.Status, len(again.Body))
				}
			}
			header := htmxHeader()
			header.Set(hx.HeaderPTag, `"something-else"`)
			if res := get(t, h, target, header); res.Status != http.StatusOK {
				t.Errorf("other tag: status %d, want 200", res.Status)
			}
			header.Set(hx.HeaderPTag, tag)
			sep := "?"
			if strings.Contains(target, "?") {
				sep = "&"
			}
			if res := get(t, h, target+sep+"hp-w=500", header); res.Status != http.StatusOK {
				t.Errorf("new width: status %d, want 200", res.Status)
			}
		})
	}
}

// TestPTagFollowsMinutes checks that the detail chart's tag is stable
// inside a minute and changes when the minute closes (the window slides),
// and that the page seeds the tags its first polls will get.
func TestPTagFollowsMinutes(t *testing.T) {
	s := newTestServer(t)
	h := s.routes()
	const target = "/detail/chart?sensor=T-SRV"
	tagAt := func(now time.Time, target string) string {
		s.now = func() time.Time { return now }
		return get(t, h, target, htmxHeader()).Header.Get(hx.HeaderPTag)
	}
	start := epoch.Truncate(time.Minute)
	a, b, c := tagAt(start, target), tagAt(start.Add(59*time.Second), target), tagAt(start.Add(time.Minute), target)
	if a != b {
		t.Errorf("tag changed inside a minute: %s then %s", a, b)
	}
	if b == c {
		t.Errorf("tag did not change when the minute closed: %s", c)
	}

	s.now = func() time.Time { return epoch }
	page := get(t, h, "/?sensor=T-SRV", nil).Body
	for _, target := range []string{target, "/charts/overlimit"} {
		want := `hx-ptag="` + strings.ReplaceAll(tagAt(epoch, target), `"`, "&#34;") + `"`
		if !strings.Contains(page, want) {
			t.Errorf("page does not seed the tag of %s: %s", target, want)
		}
	}
}

// TestWidth checks that hp-w sets the rendered width (spec/spec.md §4.6),
// clamped, and that a bad value falls back to the default.
func TestWidth(t *testing.T) {
	h := newTestServer(t).routes()
	for _, tt := range []struct{ w, want string }{
		{"480", "480"}, {"1000", "1000"}, {"10", "50"}, {"99999", "4096"}, {"abc", "640"}, {"-5", "640"}, {"NaN", "640"},
	} {
		for _, path := range []string{"/charts/overlimit?", "/detail/chart?sensor=T-LOB&", "/detail?sensor=T-LOB&"} {
			res := get(t, h, path+"hp-w="+tt.w, htmxHeader())
			if res.Status != http.StatusOK {
				t.Errorf("%s hp-w=%s: status %d", path, tt.w, res.Status)
				continue
			}
			if !strings.Contains(res.Body, `width="`+tt.want+`"`) {
				t.Errorf("%s hp-w=%s: chart is not %s wide", path, tt.w, tt.want)
			}
		}
	}
}

// TestSelect checks the drill-down: a select of the over-limit chart and
// the tile link both open the sensor and push its page URL; anything else
// selects nothing and swaps nothing (spec/spec.md §5.2).
func TestSelect(t *testing.T) {
	h := newTestServer(t).routes()
	for _, target := range []string{
		"/detail?hp-chart=overlimit&hp-x=CO2-MTG",
		"/detail?hp-x=CO2-MTG&hp-series=Warning", // no hp-chart: a hand-made link
		"/detail?sensor=CO2-MTG",
	} {
		res := get(t, h, target, htmxHeader())
		if res.Status != http.StatusOK {
			t.Fatalf("%s: status %d", target, res.Status)
		}
		if got := res.Header.Get("HX-Push-Url"); got != "/?sensor=CO2-MTG" {
			t.Errorf("%s: HX-Push-Url %q", target, got)
		}
		for _, want := range []string{
			`<p id="detail-status" class="sr-only" role="status">Showing Meeting room CO₂.</p>`,
			`hx-get="/detail/chart?sensor=CO2-MTG"`,
			`data-hp-series="CO2-MTG"`,
		} {
			if !strings.Contains(res.Body, want) {
				t.Errorf("%s: body lacks %s", target, want)
			}
		}
	}
	for _, target := range []string{
		"/detail",
		"/detail?hp-chart=overlimit&hp-x=NOPE",
		"/detail?hp-chart=trend&hp-x=T-SRV", // a select of another chart
		"/detail?hp-chart=overlimit&hp-x=%3Cscript%3E",
		"/detail?sensor=t-srv", // codes match exactly
	} {
		if res := get(t, h, target, htmxHeader()); res.Status != http.StatusNoContent || res.Body != "" {
			t.Errorf("%s: status %d with %d bytes, want 204 and nothing", target, res.Status, len(res.Body))
		}
	}
}

// TestBrush checks brush to zoom: the answer is the whole polling element,
// carrying the zoom in its hx-get, the toolbar as a partial, and the page
// URL of the zoom. The order of hp-x0 and hp-x1 doesn't matter, and the
// zoom form's datetime-local values are read in the chart's time zone.
func TestBrush(t *testing.T) {
	s := newTestServer(t)
	h := s.routes()
	const (
		want   = `/detail/chart?from=2026-03-03T13%3A20%3A00Z&amp;sensor=T-SRV&amp;to=2026-03-03T13%3A50%3A00Z`
		pushed = "/?from=2026-03-03T13%3A20%3A00Z&sensor=T-SRV&to=2026-03-03T13%3A50%3A00Z"
	)
	for _, q := range []string{
		brushQ,
		"&hp-chart=trend&hp-x0=2026-03-03T13%3A50%3A00Z&hp-x1=2026-03-03T13%3A20%3A00Z",   // reversed
		"&hp-x0=2026-03-03T13%3A20%3A00Z&hp-x1=2026-03-03T13%3A50%3A00Z",                  // no hp-chart
		"&hp-chart=trend&hp-x0=2026-03-03T13%3A20&hp-x1=2026-03-03T13%3A50",               // datetime-local
		"&hp-chart=trend&hp-x0=2026-03-03T13%3A20%3A12.5Z&hp-x1=2026-03-03T13%3A49%3A40Z", // rounded out to minutes
	} {
		res := get(t, h, "/detail/chart?sensor=T-SRV"+q, htmxHeader())
		if res.Status != http.StatusOK {
			t.Fatalf("%s: status %d", q, res.Status)
		}
		if !strings.Contains(res.Body, `<div id="detail-body" class="chart" hx-plot:autosize`+"\n"+`     hx-get="`+want+`"`) {
			t.Errorf("%s: the polling element does not carry the zoom\n%.400s", q, res.Body)
		}
		if got := res.Header.Get("HX-Push-Url"); got != pushed {
			t.Errorf("%s: HX-Push-Url %q, want %q", q, got, pushed)
		}
		if !strings.Contains(res.Body, `Zoomed to 1:20 PM to 1:50 PM.`) {
			t.Errorf("%s: the zoom bar does not announce the zoom", q)
		}
	}

	// Brushes that cannot be read are ignored: 204, nothing swapped.
	for _, q := range []string{
		"&hp-chart=trend&hp-x0=yesterday&hp-x1=2026-03-03T13%3A50%3A00Z",
		"&hp-chart=overlimit&hp-x0=2026-03-03T13%3A20%3A00Z&hp-x1=2026-03-03T13%3A50%3A00Z",
		"&hp-chart=trend&hp-x0=1&hp-x1=2",
	} {
		if res := get(t, h, "/detail/chart?sensor=T-SRV"+q, htmxHeader()); res.Status != http.StatusNoContent {
			t.Errorf("%s: status %d, want 204", q, res.Status)
		}
	}

	// Zooming out pushes the live page URL.
	res := get(t, h, "/detail/chart?sensor=T-SRV"+zoomQ+"&zoom=out", htmxHeader())
	if got := res.Header.Get("HX-Push-Url"); got != "/?sensor=T-SRV" {
		t.Errorf("zoom out: HX-Push-Url %q", got)
	}
	if !strings.Contains(res.Body, "Live: the last two hours.") {
		t.Error("zoom out: the zoom bar does not say live")
	}
}

// TestNewZoom checks the zoom limits, that a range wholly outside the data
// is refused (spec/spec.md §7), and that a zoom fed back in at the same
// clock is the same window (so zoomFromQuery can tell it is canonical).
func TestNewZoom(t *testing.T) {
	now := epoch
	last := now.Truncate(time.Minute)
	oldest := last.Add(-maxBack)
	year := func(y int) time.Time { return time.Date(y, 1, 1, 0, 0, 0, 0, time.UTC) }
	tests := []struct {
		name     string
		t0, t1   time.Time
		from, to time.Time
		ok       bool
	}{
		{"plain", last.Add(-time.Hour), last.Add(-30 * time.Minute), last.Add(-time.Hour), last.Add(-30 * time.Minute), true},
		{"too short", last.Add(-60 * time.Minute), last.Add(-59 * time.Minute), last.Add(-65 * time.Minute), last.Add(-55 * time.Minute), true},
		{"future end", last.Add(-20 * time.Minute), last.Add(time.Hour), last.Add(-20 * time.Minute), last, true},
		{"too long", last.Add(-24 * time.Hour), last, last.Add(-6 * time.Hour), last, true},
		{"old start", oldest.Add(-time.Hour), oldest.Add(time.Hour), oldest, oldest.Add(time.Hour), true},
		{"short at the oldest end", oldest.Add(-time.Hour), oldest.Add(time.Minute), oldest, oldest.Add(minZoom), true},
		{"whole history", year(1), year(9999), last.Add(-maxZoom), last, true},
		{"too old", last.Add(-30 * 24 * time.Hour), last.Add(-29 * 24 * time.Hour), time.Time{}, time.Time{}, false},
		{"year 1", year(1), year(1).Add(time.Minute), time.Time{}, time.Time{}, false},
		{"year 9999, zero width", year(9999), year(9999), time.Time{}, time.Time{}, false},
		{"ends at the oldest minute", oldest.Add(-time.Hour), oldest, time.Time{}, time.Time{}, false},
	}
	for _, tt := range tests {
		z, ok := newZoom(tt.t0, tt.t1, now)
		if ok != tt.ok {
			t.Errorf("%s: ok = %v, want %v", tt.name, ok, tt.ok)
			continue
		}
		if !ok {
			continue
		}
		if !z.From.Equal(tt.from) || !z.To.Equal(tt.to) {
			t.Errorf("%s: got %v to %v, want %v to %v", tt.name, z.From, z.To, tt.from, tt.to)
		}
		if again, ok := newZoom(z.From, z.To, now); !ok || again != z {
			t.Errorf("%s: not a fixed point: %v then %v", tt.name, z, again)
		}
	}
}

// TestBrushOutsideData checks that a brush wholly outside the data is
// ignored like any unreadable brush: 204, no history entry (spec §7).
func TestBrushOutsideData(t *testing.T) {
	h := newTestServer(t).routes()
	for _, q := range []string{
		"&hp-chart=trend&hp-x0=0001-01-01T00%3A00%3A00Z&hp-x1=0001-01-01T00%3A01%3A00Z",
		"&hp-x0=9999-01-01T00%3A00%3A00Z&hp-x1=9999-01-01T00%3A00%3A00Z",
		"&hp-chart=trend&hp-x0=2026-02-01T00%3A00&hp-x1=2026-02-01T01%3A00", // the form, a month back
	} {
		res := get(t, h, "/detail/chart?sensor=T-SRV"+q, htmxHeader())
		if res.Status != http.StatusNoContent || res.Header.Get("HX-Push-Url") != "" {
			t.Errorf("%s: status %d, HX-Push-Url %q; want 204 and none", q, res.Status, res.Header.Get("HX-Push-Url"))
		}
	}
}

// TestZoomedPollAgesOut checks that a zoomed poll draws exactly the window
// its URL names while the 7-day history still holds it, and answers 204
// (keep the last frame) once the history has started to cut it, instead
// of drawing another window under a toolbar that still names this one.
func TestZoomedPollAgesOut(t *testing.T) {
	s := newTestServer(t)
	clock := epoch
	s.now = func() time.Time { return clock }
	h := s.routes()
	// The oldest hour of the history: 2026-02-24 14:01 to 15:01.
	q := "&hp-chart=trend&hp-x0=2026-02-24T14%3A01%3A00Z&hp-x1=2026-02-24T15%3A01%3A00Z"
	res := get(t, h, "/detail/chart?sensor=KW-HVAC"+q, htmxHeader())
	if res.Status != http.StatusOK {
		t.Fatalf("zoom: status %d", res.Status)
	}
	// Its date is announced: the window is a week old, not today.
	if !strings.Contains(res.Body, "Zoomed to Feb 24, 2:01 PM to Feb 24, 3:01 PM.") {
		t.Error("the zoom into last week is announced without its date")
	}
	m := regexp.MustCompile(`id="detail-body"[^>]*\shx-get="([^"]*)"`).FindStringSubmatch(res.Body)
	if m == nil {
		t.Fatal("no #detail-body hx-get")
	}
	poll := html.UnescapeString(m[1])
	domain := attr(res.Body, "data-hp-x")
	if domain != "time 1771941660000 1771945260000" {
		t.Fatalf("the zoom draws %q", domain)
	}
	for _, step := range []struct {
		after  time.Duration
		status int
	}{
		{0, http.StatusOK},
		{30 * time.Second, http.StatusOK},   // 14:01:45: the same last minute
		{time.Minute, http.StatusNoContent}, // 14:02: the history starts at 14:02
		{30 * time.Minute, http.StatusNoContent},
		{2 * time.Hour, http.StatusNoContent}, // wholly before the history
	} {
		clock = epoch.Add(step.after)
		got := get(t, h, poll, htmxHeader())
		if got.Status != step.status {
			t.Errorf("+%v: poll status %d, want %d", step.after, got.Status, step.status)
		}
		if got.Status == http.StatusOK && attr(got.Body, "data-hp-x") != domain {
			t.Errorf("+%v: poll draws %q, want %q", step.after, attr(got.Body, "data-hp-x"), domain)
		}
	}
	// A page with that aged-out zoom shows the live window and says so.
	page := get(t, h, "/?sensor=KW-HVAC&from=2026-02-24T14%3A01%3A00Z&to=2026-02-24T15%3A01%3A00Z", nil)
	if page.Status != http.StatusOK || !strings.Contains(page.Body, "Live: the last two hours.") {
		t.Errorf("a page with an aged-out zoom: status %d, not live", page.Status)
	}
}

// attr returns the first value of attribute name in body, unescaped.
func attr(body, name string) string {
	m := regexp.MustCompile(`\s` + name + `="([^"]*)"`).FindStringSubmatch(body)
	if m == nil {
		return ""
	}
	return html.UnescapeString(m[1])
}

// TestBadParams sends hostile and malformed parameters to every route: none
// may fail with a 5xx, and none may echo markup.
func TestBadParams(t *testing.T) {
	h := newTestServer(t).routes()
	evil := url.QueryEscape(`"><script>alert(1)</script>`)
	for _, path := range []string{"/", "/charts/overlimit", "/detail", "/detail/chart"} {
		for _, q := range []string{
			"sensor=" + evil,
			"theme=" + evil,
			"from=" + evil + "&to=2026-03-03T13%3A00%3A00Z",
			"from=2026-03-03T13%3A00%3A00Z&to=9999-12-31T23%3A59%3A59Z",
			"from=0001-01-01T00%3A00%3A00Z&to=0001-01-01T00%3A10%3A00Z",
			"hp-chart=" + evil + "&hp-x=" + evil,
			"hp-chart=trend&hp-x0=" + evil + "&hp-x1=" + evil,
			"hp-x0=1e999&hp-x1=-1e999",
			"hp-w=" + evil,
			"hp-w=1e308&hp-h=-1",
			"zoom=" + evil,
		} {
			for _, header := range []http.Header{nil, htmxHeader()} {
				res := get(t, h, path+"?"+q, header)
				if res.Status >= 500 {
					t.Errorf("%s?%s: status %d", path, q, res.Status)
				}
				if strings.Contains(res.Body, "<script>alert") {
					t.Errorf("%s?%s: echoes markup", path, q)
				}
				if strings.HasPrefix(res.Header.Get("Content-Type"), "text/html") {
					checkSafe(t, path+"?"+q, res.Body)
					checkSVGs(t, path+"?"+q, res.Body, 0)
				}
			}
		}
	}
}

// TestTheme checks the script-free theme switch: a link sets the theme and
// a cookie that remembers it; "system" clears both.
func TestTheme(t *testing.T) {
	h := newTestServer(t).routes()
	res := get(t, h, "/?theme=dark", nil)
	if !strings.Contains(res.Body, `<html lang="en" data-theme="dark">`) {
		t.Error("?theme=dark does not set data-theme")
	}
	if c := res.Header.Get("Set-Cookie"); !strings.HasPrefix(c, "theme=dark;") {
		t.Errorf("Set-Cookie %q", c)
	}
	if !strings.Contains(res.Body, `href="/?sensor=T-SRV&amp;theme=dark" aria-current="true">Dark</a>`) {
		t.Error("the Dark link is not marked current")
	}
	cookie := http.Header{"Cookie": {"theme=light"}}
	if res := get(t, h, "/", cookie); !strings.Contains(res.Body, `data-theme="light"`) {
		t.Error("the theme cookie is not honored")
	}
	res = get(t, h, "/?theme=system", cookie)
	if strings.Contains(res.Body, "data-theme=") {
		t.Error("?theme=system keeps a data-theme")
	}
	if c := res.Header.Get("Set-Cookie"); !strings.Contains(c, "Max-Age=0") {
		t.Errorf("?theme=system does not clear the cookie: %q", c)
	}
	if res := get(t, h, "/", http.Header{"Cookie": {"theme=purple"}}); strings.Contains(res.Body, "data-theme=") {
		t.Error("an unknown theme cookie is honored")
	}
}

// TestRules checks that the detail chart draws the sensor's two limits.
func TestRules(t *testing.T) {
	body := get(t, newTestServer(t).routes(), "/detail/chart?sensor=T-SRV", htmxHeader()).Body
	if n := strings.Count(body, `class="hp-rule"`); n != 2 {
		t.Errorf("%d rules, want 2", n)
	}
	for _, want := range []string{">Warning 24.0 °C<", ">Alarm 27.0 °C<"} {
		if !strings.Contains(body, want) {
			t.Errorf("no rule label %s", want)
		}
	}
}

// TestAlerts checks the alerts table: rows exist at the test epoch, their
// ids are unique, and a row keeps its id while time moves on, so a morph
// keeps the row node with its alert.
func TestAlerts(t *testing.T) {
	ids := func(now time.Time) map[string]alertRow {
		out := map[string]alertRow{}
		for _, r := range newAlertsView(now, time.UTC).Rows {
			if _, dup := out[r.ID]; dup {
				t.Errorf("duplicate row id %s at %v", r.ID, now)
			}
			out[r.ID] = r
		}
		return out
	}
	a, b := ids(epoch), ids(epoch.Add(10*time.Minute))
	if len(a) == 0 {
		t.Fatal("no alerts at the test epoch; pick another")
	}
	kept := 0
	for id, row := range a {
		if other, ok := b[id]; ok {
			kept++
			if other.Code != row.Code || other.Started != row.Started {
				t.Errorf("row %s changed alert: %+v then %+v", id, row, other)
			}
		}
	}
	if kept == 0 {
		t.Error("no row survived ten minutes; ids are not stable")
	}
	for _, as := range [][]alert{alerts(epoch.Truncate(time.Minute)), alerts(epoch.Add(time.Hour).Truncate(time.Minute))} {
		for i, al := range as {
			if i > 0 && al.Start.After(as[i-1].Start) {
				t.Error("alerts are not newest first")
			}
			if al.Peak < al.Sensor.Warn {
				t.Errorf("alert %+v peaks below the warning limit", al)
			}
		}
	}
	html, err := newTestServer(t).liveFragment(epoch)
	if err != nil {
		t.Fatal(err)
	}
	if !regexp.MustCompile(`<tr id="alert-[a-z0-9-]+-\d{8}t\d{4}">`).Match(html) {
		t.Error("the live fragment has no alert row with a stable id")
	}
}

// TestDeterministic checks that a reading depends only on the sensor and
// the bucket, and that the same instant renders the same bytes.
func TestDeterministic(t *testing.T) {
	s := newTestServer(t)
	a, err := s.liveFragment(epoch)
	if err != nil {
		t.Fatal(err)
	}
	b, err := s.liveFragment(epoch.Add(400 * time.Millisecond)) // the same second
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(a, b) {
		t.Error("the same second rendered different bytes")
	}
	for _, sn := range sensors {
		end := epoch.Truncate(time.Minute)
		if sn.at(end) != sn.at(end) || sn.live(epoch) != sn.live(epoch.Add(time.Millisecond)) {
			t.Errorf("%s: readings are not a function of the bucket", sn.Code)
		}
	}
}

// TestSSE reads the tile stream over a real HTTP connection: a retry
// field, then messages with distinct ids, each the tile grid plus the
// alerts as an <hx-partial>.
func TestSSE(t *testing.T) {
	s := newTestServer(t)
	var calls atomic.Int64
	s.now = func() time.Time { // one second per message
		return epoch.Add(time.Duration(calls.Add(1)) * time.Second)
	}
	srv := httptest.NewServer(s.routes())
	defer srv.Close()

	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, srv.URL+"/stream/tiles", nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Accept", "text/event-stream")
	res, err := srv.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	if ct := res.Header.Get("Content-Type"); !strings.HasPrefix(ct, "text/event-stream") {
		t.Fatalf("Content-Type %q, want text/event-stream", ct)
	}

	type message struct{ id, data string }
	var msgs []message
	var cur message
	retry := false
	sc := bufio.NewScanner(res.Body)
	sc.Buffer(make([]byte, 0, 64<<10), 4<<20)
	for len(msgs) < 3 && sc.Scan() {
		line := sc.Text()
		if strings.Contains(line, "\r") {
			t.Fatal("raw CR in the stream")
		}
		switch {
		case line == "":
			if cur.data != "" {
				msgs = append(msgs, cur)
			}
			cur = message{}
		case strings.HasPrefix(line, "retry: "):
			retry = true
		case strings.HasPrefix(line, "id: "):
			cur.id = strings.TrimPrefix(line, "id: ")
		case strings.HasPrefix(line, "data: "):
			if cur.data != "" {
				cur.data += "\n"
			}
			cur.data += strings.TrimPrefix(line, "data: ")
		}
	}
	cancel()
	if !retry {
		t.Error("no retry: field")
	}
	if len(msgs) < 2 {
		t.Fatalf("got %d messages, want at least 2 (scan error: %v)", len(msgs), sc.Err())
	}
	ids := map[string]bool{}
	for i, m := range msgs {
		if m.id == "" || ids[m.id] {
			t.Errorf("message %d: id %q is missing or repeated", i, m.id)
		}
		ids[m.id] = true
		for _, want := range []string{`<article class="tile`, `id="tile-t-srv"`, `<hx-partial hx-target="#alerts" hx-swap="innerMorph">`} {
			if !strings.Contains(m.data, want) {
				t.Errorf("message %d lacks %s", i, want)
			}
		}
		if n := strings.Count(m.data, `class="hp-spark"`); n != len(sensors) {
			t.Errorf("message %d has %d sparklines, want %d", i, n, len(sensors))
		}
		checkSVGs(t, "sse message", m.data, len(sensors))
		checkSafe(t, "sse message", m.data)
		if err := wellFormed("<root>" + m.data + "</root>"); err != nil {
			t.Errorf("message %d is not well-formed: %v", i, err)
		}
	}
}

// TestSSEResume checks Last-Event-ID: a client that reconnects within the
// second it already shows gets the next second, not a duplicate.
func TestSSEResume(t *testing.T) {
	s := newTestServer(t)
	var calls atomic.Int64
	s.now = func() time.Time {
		if calls.Add(1) <= 5 {
			return epoch
		}
		return epoch.Add(time.Second)
	}
	srv := httptest.NewServer(s.routes())
	defer srv.Close()
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, srv.URL+"/stream/tiles", nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set(hx.HeaderLastEventID, strconv.FormatInt(epoch.Unix(), 10))
	res, err := srv.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	sc := bufio.NewScanner(res.Body)
	sc.Buffer(make([]byte, 0, 64<<10), 4<<20)
	for sc.Scan() {
		if id, ok := strings.CutPrefix(sc.Text(), "id: "); ok {
			if want := strconv.FormatInt(epoch.Unix()+1, 10); id != want {
				t.Errorf("first id %s, want %s", id, want)
			}
			return
		}
	}
	t.Fatalf("no message: %v", sc.Err())
}

// TestFastClock checks the -speed clock.
func TestFastClock(t *testing.T) {
	base := epoch
	now := base
	clock := fastClock(func() time.Time { return now }, 30)
	now = base.Add(2 * time.Second)
	if got := clock().Sub(base); got != time.Minute {
		t.Errorf("2 s at speed 30 is %v, want 1m", got)
	}
}
