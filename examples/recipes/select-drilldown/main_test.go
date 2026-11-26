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
	"strings"
	"testing"
	"time"
)

// epoch is a fixed instant, so every test sees the same data.
var epoch = time.Date(2026, 3, 3, 14, 20, 0, 0, time.UTC)

func testServer() http.Handler {
	return newServer(func() time.Time { return epoch }, time.UTC).routes()
}

func get(t *testing.T, h http.Handler, target string) *httptest.ResponseRecorder {
	t.Helper()
	r := httptest.NewRequestWithContext(t.Context(), http.MethodGet, target, nil)
	r.Header.Set("HX-Request", "true")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	return w
}

// selectQuery is what the extension adds for a click on a bar.
func selectQuery(chart, x, series string) string {
	v := url.Values{}
	if chart != "" {
		v.Set("hp-chart", chart)
	}
	if x != "" {
		v.Set("hp-x", x)
	}
	if series != "" {
		v.Set("hp-series", series)
	}
	return v.Encode()
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
		`hx-get="/detail" hx-trigger="htmx:plot:select"`,
		`hx-target="#detail" hx-swap="innerMorph"`,
		`<p id="detail-status" class="status" role="status">No service selected.</p>`,
		`id="errors"`,
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
	// spec/spec.md §5.5: one link per bar group, sending the same request.
	for _, svc := range services {
		q := strings.ReplaceAll(selectQuery("errors", svc, ""), "&", "&amp;")
		want := `<a href="/?` + q + `" hx-get="/detail?` + q + `" hx-target="#detail" hx-swap="innerMorph">` + svc + `</a>`
		if !strings.Contains(body, want) {
			t.Errorf("page lacks the link %s", want)
		}
		// The bars carry each service as its row key, so a click sends it as hp-x.
		if !strings.Contains(body, `"k":"`+svc+`"`) {
			t.Errorf("the chart has no row key %q", svc)
		}
	}
}

// TestSelect is the recipe's point: hp-x picks the service, hp-series
// narrows to a class, and anything unknown is no selection, never an error.
func TestSelect(t *testing.T) {
	h := testServer()
	for _, tt := range []struct {
		name, query, status string
		series              []string // data-hp-series in the detail chart; nil: no chart
	}{
		{"bar with series", selectQuery("errors", "auth", "5xx"), "Showing auth, 5xx only.", []string{"5xx"}},
		{"other series", selectQuery("errors", "web", "4xx"), "Showing web, 4xx only.", []string{"4xx"}},
		{"no series (Enter, a link)", selectQuery("errors", "billing", ""), "Showing billing, all errors.", []string{"5xx", "4xx"}},
		{"no hp-chart", selectQuery("", "api", ""), "Showing api, all errors.", []string{"5xx", "4xx"}},
		{"unknown series", selectQuery("errors", "api", "<b>2xx</b>"), "Showing api, all errors.", []string{"5xx", "4xx"}},
		{"nothing selected (Close)", "", "No service selected.", nil},
		{"another chart", selectQuery("latency", "auth", ""), "No service selected.", nil},
		{
			"unknown service", selectQuery("errors", "<script>x</script>", ""),
			"There is no service named “&lt;script&gt;x&lt;/script&gt;”.", nil,
		},
	} {
		w := get(t, h, "/detail?"+tt.query)
		if w.Code != http.StatusOK {
			t.Errorf("%s: status %d", tt.name, w.Code)
			continue
		}
		body := w.Body.String()
		checkMarkup(t, body, 0)
		if !strings.HasPrefix(body, `<p id="detail-status" class="status" role="status">`+tt.status+`</p>`) {
			t.Errorf("%s: detail starts %.160q", tt.name, body)
		}
		if strings.Contains(body, "<script>") || strings.Contains(body, "<b>") {
			t.Errorf("%s: echoes markup unescaped", tt.name)
		}
		got := regexp.MustCompile(`data-hp-series="([^"]*)"`).FindAllStringSubmatch(body, -1)
		if len(got) != len(tt.series) {
			t.Errorf("%s: %d series in the detail, want %v", tt.name, len(got), tt.series)
			continue
		}
		for i, m := range got {
			if m[1] != tt.series[i] {
				t.Errorf("%s: series %d is %q, want %q", tt.name, i, m[1], tt.series[i])
			}
		}
		if tt.series != nil && !strings.Contains(body, `id="service-errors"`) {
			t.Errorf("%s: no detail chart", tt.name)
		}
	}
}

// TestLinkWithoutScript: the link's href opens the page with the detail.
func TestLinkWithoutScript(t *testing.T) {
	body := get(t, testServer(), "/?"+selectQuery("errors", "search", "")).Body.String()
	checkMarkup(t, body, 2)
	if !strings.Contains(body, "Showing search, all errors.") || !strings.Contains(body, `id="service-errors"`) {
		t.Error("the page does not open the linked detail")
	}
}

// TestSlotsKeepColors: a class keeps its color slot when it is shown alone.
func TestSlotsKeepColors(t *testing.T) {
	body := get(t, testServer(), "/detail?"+selectQuery("errors", "auth", "4xx")).Body.String()
	if !strings.Contains(body, `class="hp-series hp-s2" data-hp-series="4xx"`) {
		t.Error("4xx alone lost its slot")
	}
}

func TestAssets(t *testing.T) {
	h := testServer()
	for path, ct := range map[string]string{
		"/static/hx-plot.js":  "text/javascript; charset=utf-8",
		"/static/hx-plot.css": "text/css; charset=utf-8",
		"/static/app.css":     "text/css; charset=utf-8",
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
