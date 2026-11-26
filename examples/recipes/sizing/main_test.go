package main

import (
	"bytes"
	"context"
	"encoding/xml"
	"errors"
	"flag"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"
)

var update = flag.Bool("update", false, "rewrite the files in preview/")

// epoch is a fixed instant, so every test sees the same data.
var epoch = time.Date(2026, 3, 3, 14, 20, 0, 0, time.UTC)

func testServer() http.Handler {
	return newServer(func() time.Time { return epoch }, time.UTC).routes()
}

func get(t *testing.T, h http.Handler, target string, htmx bool) *httptest.ResponseRecorder {
	t.Helper()
	r := httptest.NewRequestWithContext(t.Context(), http.MethodGet, target, nil)
	if htmx {
		r.Header.Set("HX-Request", "true")
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
	body := w.Body.String()
	checkMarkup(t, body, 3)
	for _, want := range []string{
		// htmx 4.0.2, pinned with SRI exactly as in examples/dashboard, not deferred.
		`<script src="https://cdn.jsdelivr.net/npm/htmx.org@4.0.2/dist/htmx.min.js" integrity="sha384-6NzMyCdh08UZ4b4M17SJJKQLdDijx4z2IqqKLKD0Q/PzCFQLpFuk3konY0WgqbTy" crossorigin="anonymous"></script>`,
		`<script defer src="/static/hx-plot.js"></script>`,
		`hx-plot:autosize hx-get="/charts/width"`,
		`hx-plot:autosize="both" hx-get="/charts/both"`,
		`id="scaled"`, `id="width"`, `id="both"`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("page lacks %s", want)
		}
	}
	// Only the extensions the page uses.
	for _, unused := range []string{"hx-sse", "hx-ptag"} {
		if strings.Contains(body, unused) {
			t.Errorf("page loads %s", unused)
		}
	}
}

// svgSize matches the root size of a chart.
var svgSize = regexp.MustCompile(`<svg [^>]*?width="([^"]+)" height="([^"]+)"`)

// TestSize is the recipe's point: hp-w (and for "both", hp-h) set the size,
// clamped to [50, 4096]; bad values give the server's default, never an error.
func TestSize(t *testing.T) {
	h := testServer()
	tests := []struct {
		target, width, height string
	}{
		{"/charts/width?hp-w=500", "500", "240"},
		{"/charts/width?hp-w=500&hp-h=900", "500", "240"}, // the width-only chart ignores hp-h
		{"/charts/width", "640", "240"},                   // no extension: the default
		{"/charts/width?hp-w=abc", "640", "240"},
		{"/charts/width?hp-w=-3", "640", "240"},
		{"/charts/width?hp-w=NaN", "640", "240"},
		{"/charts/width?hp-w=99999", "4096", "240"},
		{"/charts/width?hp-w=10", "50", "240"},
		{"/charts/both?hp-w=400&hp-h=300", "400", "300"},
		{"/charts/both?hp-w=50&hp-h=50", "50", "50"},
		{"/charts/both?hp-w=400", "400", "320"},
		{"/charts/both", "640", "320"},
	}
	for _, tt := range tests {
		w := get(t, h, tt.target, true)
		if w.Code != http.StatusOK {
			t.Errorf("%s: status %d", tt.target, w.Code)
			continue
		}
		if ct := w.Header().Get("Content-Type"); ct != "text/html; charset=utf-8" {
			t.Errorf("%s: Content-Type %q", tt.target, ct)
		}
		body := w.Body.String()
		if err := wellFormed(body); err != nil { // the whole fragment is one <svg>
			t.Errorf("%s: %v", tt.target, err)
		}
		m := svgSize.FindStringSubmatch(body)
		if len(m) != 3 || m[1] != tt.width || m[2] != tt.height {
			t.Errorf("%s: size %v, want %s × %s", tt.target, m, tt.width, tt.height)
		}
		if !strings.Contains(body, "drawn at "+tt.width+" × "+tt.height+" px") {
			t.Errorf("%s: the title does not state the size", tt.target)
		}
	}
}

// TestStableID checks that every size renders the same chart id, so a
// resize morphs the chart in place.
func TestStableID(t *testing.T) {
	h := testServer()
	for _, target := range []string{"/charts/width?hp-w=300", "/charts/width?hp-w=900"} {
		if body := get(t, h, target, true).Body.String(); !strings.Contains(body, `id="width"`) {
			t.Errorf("%s: id changed", target)
		}
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

// TestDisplayAddr checks that the logged URL opens in a browser: a
// wildcard listen address such as -addr :9000 logs as localhost.
func TestDisplayAddr(t *testing.T) {
	for _, tc := range []struct {
		addr net.Addr
		want string
	}{
		{&net.TCPAddr{IP: net.IPv6unspecified, Port: 9000}, "localhost:9000"},
		{&net.TCPAddr{IP: net.IPv4zero, Port: 9000}, "localhost:9000"},
		{&net.TCPAddr{Port: 9000}, "localhost:9000"},
		{&net.TCPAddr{IP: net.IPv4(127, 0, 0, 1), Port: 8080}, "127.0.0.1:8080"},
		{&net.TCPAddr{IP: net.IPv6loopback, Port: 8080}, "[::1]:8080"},
	} {
		if got := displayAddr(tc.addr); got != tc.want {
			t.Errorf("displayAddr(%v) = %q, want %q", tc.addr, got, tc.want)
		}
	}
}

// TestPreview keeps preview/, the pictures in the README, in step with the
// code: the same chart drawn for a narrow and a wide box. Regenerate with:
//
//	go test ./examples/recipes/sizing -update
func TestPreview(t *testing.T) {
	s := newServer(func() time.Time { return epoch }, time.UTC)
	for _, p := range []struct {
		name          string
		width, height float64
	}{{"narrow.svg", 320, 240}, {"wide.svg", 720, 240}} {
		c := s.chart("width", p.width, p.height)
		c.Standalone = true
		got, err := c.Render()
		if err != nil {
			t.Fatal(err)
		}
		checkPreview(t, p.name, got)
	}
}

func checkPreview(t *testing.T, name string, got []byte) {
	t.Helper()
	path := filepath.Join("preview", name)
	if err := wellFormed(string(got)); err != nil {
		t.Fatalf("%s: %v", name, err)
	}
	if *update {
		if err := os.MkdirAll("preview", 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, got, 0o644); err != nil {
			t.Fatal(err)
		}
		return
	}
	want, err := os.ReadFile(path)
	if err != nil || !bytes.Equal(got, want) {
		t.Errorf("%s is out of date; run go test ./examples/recipes/sizing -update "+
			"(or, after a renderer change, make golden) and review it", path)
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
