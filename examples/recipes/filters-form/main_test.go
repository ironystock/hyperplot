package main

import (
	"context"
	"encoding/xml"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"
)

// epoch is a fixed instant, so every test sees the same data.
var epoch = time.Date(2026, 3, 3, 14, 20, 0, 0, time.UTC)

func testServer() http.Handler {
	return newServer(func() time.Time { return epoch }, time.UTC).routes()
}

// get requests target; partial makes it a request like the form's, which
// htmx sends with HX-Request-Type: partial because it targets #charts.
func get(t *testing.T, h http.Handler, target string, partial bool) *httptest.ResponseRecorder {
	t.Helper()
	r := httptest.NewRequestWithContext(t.Context(), http.MethodGet, target, nil)
	if partial {
		r.Header.Set("HX-Request", "true")
		r.Header.Set("HX-Request-Type", "partial")
	}
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	return w
}

func TestPage(t *testing.T) {
	w := get(t, testServer(), "/", false)
	if w.Code != http.StatusOK {
		t.Fatalf("status %d", w.Code)
	}
	if got := w.Header().Get("Content-Security-Policy"); got != contentSecurityPolicy {
		t.Errorf("CSP = %q", got)
	}
	if got := w.Header().Get("Vary"); got != "HX-Request, HX-Request-Type" {
		t.Errorf("Vary = %q", got)
	}
	body := w.Body.String()
	checkMarkup(t, body, 3)
	for _, want := range []string{
		// htmx 4.0.2, pinned with SRI exactly as in examples/dashboard, not deferred.
		`<script src="https://cdn.jsdelivr.net/npm/htmx.org@4.0.2/dist/htmx.min.js" integrity="sha384-6NzMyCdh08UZ4b4M17SJJKQLdDijx4z2IqqKLKD0Q/PzCFQLpFuk3konY0WgqbTy" crossorigin="anonymous"></script>`,
		`<script defer src="/static/hx-plot.js"></script>`,
		// hx-sync="this:replace": the last change wins. htmx's default
		// queues one request and drops later ones, which leaves the
		// charts behind the form on a slow link.
		`hx-get="/" hx-trigger="change" hx-target="#charts" hx-swap="innerMorph" hx-push-url="true" hx-sync="this:replace"`,
		`<input type="hidden" name="region" value="">`,
		`value="6h" checked`, `value="eu-west" checked`, `value="ap-south" checked`,
		`<p id="summary" class="summary" role="status">Showing eu-west, us-east, ap-south over the last 6 hours.</p>`,
		`id="latency"`, `id="error-rate"`, `id="requests"`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("page lacks %s", want)
		}
	}
	for _, unused := range []string{"hx-sse", "hx-ptag", "hx-plot-brush"} {
		if strings.Contains(body, unused) {
			t.Errorf("page loads %s", unused)
		}
	}
}

var seriesAttr = regexp.MustCompile(`class="hp-series (hp-s\d)" data-hp-series="([^"]*)"`)

// series lists each series group as "name:slot", per chart, in order.
func series(body string) []string {
	var out []string
	for _, m := range seriesAttr.FindAllStringSubmatch(body, -1) {
		out = append(out, m[2]+":"+m[1])
	}
	return out
}

// TestFilterResponse is the recipe's point: one request answers with all
// three charts (only the contents of #charts) and the summary partial, and
// a region keeps its slot whichever others are shown.
func TestFilterResponse(t *testing.T) {
	h := testServer()
	w := get(t, h, "/?range=1h&region=&region=us-east", true)
	if w.Code != http.StatusOK {
		t.Fatalf("status %d", w.Code)
	}
	if got := w.Header().Get("Vary"); got != "HX-Request, HX-Request-Type" {
		t.Errorf("Vary = %q", got)
	}
	body := w.Body.String()
	if strings.Contains(body, "<html") || strings.Contains(body, "<form") {
		t.Error("a filter request got the whole page")
	}
	checkMarkup(t, body, 3)
	for _, id := range []string{`id="latency"`, `id="error-rate"`, `id="requests"`} {
		if !strings.Contains(body, id) {
			t.Errorf("filter response lacks %s", id)
		}
	}
	const partial = `<hx-partial hx-target="#summary" hx-swap="innerHTML">Showing us-east over the last hour.</hx-partial>`
	if !strings.HasSuffix(body, partial) {
		t.Errorf("filter response ends %.200q, want the summary partial", body[max(0, len(body)-200):])
	}
	// us-east alone keeps slot 2 in all three charts.
	if got, want := strings.Join(series(body), " "), "us-east:hp-s2 us-east:hp-s2 us-east:hp-s2"; got != want {
		t.Errorf("series %s, want %s", got, want)
	}
	// The range sets the x domain: one hour.
	m := regexp.MustCompile(`id="latency"[^>]*data-hp-x="time (\d+) (\d+)"`).FindStringSubmatch(body)
	if m == nil {
		t.Fatal("no time domain on the latency chart")
	}
	d0, _ := strconv.ParseInt(m[1], 10, 64)
	d1, _ := strconv.ParseInt(m[2], 10, 64)
	if d1-d0 != time.Hour.Milliseconds() {
		t.Errorf("domain spans %d ms, want an hour", d1-d0)
	}
}

// TestFilters covers the choices, the empty one and junk: never an error,
// unknown values ignored, nothing echoed unescaped.
func TestFilters(t *testing.T) {
	h := testServer()
	for _, tt := range []struct {
		name, query, summary string
		series               int // series groups across the three charts
	}{
		{"first visit", "", "Showing eu-west, us-east, ap-south over the last 6 hours.", 9},
		{"two regions", "?range=24h&region=&region=eu-west&region=ap-south", "Showing eu-west, ap-south over the last 24 hours.", 6},
		{"nothing checked", "?range=6h&region=", "No region selected.", 0},
		{"junk", "?range=%3Cscript%3E&region=%3Cb%3E&region=eu-west", "Showing eu-west over the last 6 hours.", 3},
	} {
		w := get(t, h, "/"+tt.query, true)
		if w.Code != http.StatusOK {
			t.Errorf("%s: status %d", tt.name, w.Code)
			continue
		}
		body := w.Body.String()
		checkMarkup(t, body, 3)
		if !strings.Contains(body, ">"+tt.summary+"</hx-partial>") {
			t.Errorf("%s: summary is not %q", tt.name, tt.summary)
		}
		if n := len(series(body)); n != tt.series {
			t.Errorf("%s: %d series, want %d", tt.name, n, tt.series)
		}
		if strings.Contains(body, "<script>") || strings.Contains(body, "<b>") {
			t.Errorf("%s: echoes markup", tt.name)
		}
	}
}

// TestNoScriptSubmit: without script the form loads the whole page, with
// the choices it sent checked.
func TestNoScriptSubmit(t *testing.T) {
	body := get(t, testServer(), "/?range=24h&region=&region=us-east", false).Body.String()
	checkMarkup(t, body, 3)
	for _, want := range []string{`value="24h" checked`, `value="us-east" checked`, `value="eu-west">`} {
		if !strings.Contains(body, want) {
			t.Errorf("page lacks %s", want)
		}
	}
}

// TestDeterministic: the same filters give the same bytes, so the morph
// changes only what the data changed.
func TestDeterministic(t *testing.T) {
	h := testServer()
	if a, b := get(t, h, "/?range=1h", true).Body.String(), get(t, h, "/?range=1h", true).Body.String(); a != b {
		t.Error("two renders of the same filters differ")
	}
}

func TestAssets(t *testing.T) {
	h := testServer()
	for path, ct := range map[string]string{
		"/static/hx-plot.js":  "text/javascript; charset=utf-8",
		"/static/hx-plot.css": "text/css; charset=utf-8",
		"/static/app.css":     "text/css; charset=utf-8",
	} {
		if w := get(t, h, path, false); w.Code != http.StatusOK || w.Header().Get("Content-Type") != ct {
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
