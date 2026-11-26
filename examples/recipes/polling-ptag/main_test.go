package main

import (
	"context"
	"encoding/xml"
	"errors"
	"html"
	"io"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"testing"
	"time"
)

// epoch is a fixed instant, so every test sees the same data.
var epoch = time.Date(2026, 3, 3, 14, 20, 0, 0, time.UTC)

func testServer(now *time.Time) http.Handler {
	return newServer(func() time.Time { return *now }, time.UTC).routes()
}

func get(t *testing.T, h http.Handler, target string, header http.Header) *httptest.ResponseRecorder {
	t.Helper()
	r := httptest.NewRequestWithContext(t.Context(), http.MethodGet, target, nil)
	for k, v := range header {
		r.Header[k] = v
	}
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	return w
}

// poll is a request as htmx and the ptag extension send it.
func poll(tag string) http.Header {
	h := http.Header{"Hx-Request": {"true"}, "Hx-Request-Type": {"partial"}}
	if tag != "" {
		h.Set("HX-PTag", tag)
	}
	return h
}

var seed = regexp.MustCompile(`hx-ptag="([^"]*)"`)

func TestPage(t *testing.T) {
	now := epoch
	h := testServer(&now)
	w := get(t, h, "/", nil)
	if w.Code != http.StatusOK {
		t.Fatalf("status %d", w.Code)
	}
	if got := w.Header().Get("Content-Security-Policy"); got != contentSecurityPolicy {
		t.Errorf("CSP = %q", got)
	}
	body := w.Body.String()
	checkMarkup(t, body, 1)
	for _, want := range []string{
		// htmx 4.0.2 and hx-ptag, pinned with SRI exactly as in examples/dashboard, not deferred.
		`<script src="https://cdn.jsdelivr.net/npm/htmx.org@4.0.2/dist/htmx.min.js" integrity="sha384-6NzMyCdh08UZ4b4M17SJJKQLdDijx4z2IqqKLKD0Q/PzCFQLpFuk3konY0WgqbTy" crossorigin="anonymous"></script>`,
		`<script src="https://cdn.jsdelivr.net/npm/htmx.org@4.0.2/dist/ext/hx-ptag.js" integrity="sha384-R20vL2ppRMMKzN1SVZdVpoeW1yH54eSH8rX3jz9NqpuWNqzwR5ngAqhCcKnK/b1s" crossorigin="anonymous"></script>`,
		`<script defer src="/static/hx-plot.js"></script>`,
		`hx-get="/charts/connections" hx-trigger="every 2s"`,
		`hx-swap="innerMorph"`,
		`id="connections"`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("page lacks %s", want)
		}
	}
	if strings.Contains(body, "hx-sse") {
		t.Error("page loads hx-sse, which it does not use")
	}

	// The seeded tag is the tag a poll at the same instant gets, so the
	// first poll is already a 304.
	m := seed.FindStringSubmatch(body)
	if m == nil {
		t.Fatal("page has no hx-ptag seed")
	}
	tag := html.UnescapeString(m[1])
	if w := get(t, h, "/charts/connections", poll(tag)); w.Code != http.StatusNotModified {
		t.Errorf("first poll with the seeded tag %s: status %d, want 304", tag, w.Code)
	}
}

// TestPTag is the recipe's point: a poll that echoes the current tag gets
// a bodiless 304, in every form hx-ptag may send it, and a poll after the
// data moved on gets the new chart and a new tag.
func TestPTag(t *testing.T) {
	now := epoch
	h := testServer(&now)

	first := get(t, h, "/charts/connections", poll(""))
	if first.Code != http.StatusOK {
		t.Fatalf("status %d", first.Code)
	}
	if ct := first.Header().Get("Content-Type"); ct != "text/html; charset=utf-8" {
		t.Errorf("Content-Type %q", ct)
	}
	checkMarkup(t, first.Body.String(), 1)
	tag := first.Header().Get("HX-PTag")
	if !regexp.MustCompile(`^"hp-[0-9a-f]{16}"$`).MatchString(tag) {
		t.Fatalf("HX-PTag %q, want a quoted hp- tag", tag)
	}

	// Later in the same 10-second bucket the chart is the same.
	now = epoch.Add(9 * time.Second)
	bare := strings.Trim(tag, `"`)
	for _, sent := range []string{tag, bare, `W/` + tag, "  " + tag + " "} {
		w := get(t, h, "/charts/connections", poll(sent))
		if w.Code != http.StatusNotModified {
			t.Errorf("HX-PTag %q: status %d, want 304", sent, w.Code)
		}
		if w.Body.Len() != 0 {
			t.Errorf("HX-PTag %q: 304 has a %d-byte body", sent, w.Body.Len())
		}
		if got := w.Header().Get("HX-PTag"); got != tag {
			t.Errorf("HX-PTag %q: response tag %q, want %q", sent, got, tag)
		}
	}

	// A stale or foreign tag gets the chart.
	if w := get(t, h, "/charts/connections", poll(`"hp-0000000000000000"`)); w.Code != http.StatusOK {
		t.Errorf("foreign tag: status %d, want 200", w.Code)
	}

	// The next bucket: the old tag no longer matches.
	now = epoch.Add(step)
	w := get(t, h, "/charts/connections", poll(tag))
	if w.Code != http.StatusOK {
		t.Fatalf("after the data changed: status %d, want 200", w.Code)
	}
	if next := w.Header().Get("HX-PTag"); next == tag || next == "" {
		t.Errorf("after the data changed: tag %q, want a new one", next)
	}
	checkMarkup(t, w.Body.String(), 1)
}

// TestDeterministic checks that the same instant gives the same bytes,
// which is what makes the tags meaningful (spec/spec.md §4.4).
func TestDeterministic(t *testing.T) {
	now := epoch
	h := testServer(&now)
	a := get(t, h, "/charts/connections", nil).Body.String()
	b := get(t, h, "/charts/connections", nil).Body.String()
	if a != b {
		t.Error("two renders of the same instant differ")
	}
}

func TestAssets(t *testing.T) {
	now := epoch
	h := testServer(&now)
	for path, ct := range map[string]string{
		"/static/hx-plot.js":  "text/javascript; charset=utf-8",
		"/static/hx-plot.css": "text/css; charset=utf-8",
		"/static/app.css":     "text/css; charset=utf-8",
	} {
		if w := get(t, h, path, nil); w.Code != http.StatusOK || w.Header().Get("Content-Type") != ct {
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
