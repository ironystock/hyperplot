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
var epoch = time.Date(2026, 3, 3, 14, 0, 30, 0, time.UTC)

func get(t *testing.T, h http.Handler, target string) *httptest.ResponseRecorder {
	t.Helper()
	w := httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequestWithContext(t.Context(), http.MethodGet, target, nil))
	return w
}

func TestPage(t *testing.T) {
	h := newServer(func() time.Time { return epoch }, time.UTC).routes()
	w := get(t, h, "/")
	if w.Code != http.StatusOK {
		t.Fatalf("status %d", w.Code)
	}
	csp := w.Header().Get("Content-Security-Policy")
	if csp != contentSecurityPolicy {
		t.Errorf("CSP = %q", csp)
	}
	// The page loads hx-plot.js from this origin and nothing else, so the
	// policy allows no other script source.
	if !strings.Contains(csp, "script-src 'self';") || strings.Contains(csp, "jsdelivr") {
		t.Errorf("CSP allows more scripts than the page loads: %q", csp)
	}
	body := w.Body.String()
	checkMarkup(t, body, 2)
	for _, want := range []string{`<script defer src="/static/hx-plot.js">`, `id="latency"`, `id="regions"`, `id="latency-table"`} {
		if !strings.Contains(body, want) {
			t.Errorf("page lacks %s", want)
		}
	}
	// The point of the recipe: no htmx, no htmx attributes.
	for _, bad := range []string{"htmx.org", "hx-get", "hx-trigger", "htmx-config"} {
		if strings.Contains(body, bad) {
			t.Errorf("page contains %q", bad)
		}
	}
}

func TestAssets(t *testing.T) {
	h := newServer(time.Now, time.UTC).routes()
	tests := []struct {
		path, contentType string
		status            int
	}{
		{"/static/hx-plot.js", "text/javascript; charset=utf-8", http.StatusOK},
		{"/static/hx-plot.css", "text/css; charset=utf-8", http.StatusOK},
		{"/static/app.css", "text/css; charset=utf-8", http.StatusOK},
		{"/nope", "", http.StatusNotFound},
	}
	for _, tt := range tests {
		w := get(t, h, tt.path)
		if w.Code != tt.status {
			t.Errorf("%s: status %d, want %d", tt.path, w.Code, tt.status)
		}
		if tt.contentType != "" && w.Header().Get("Content-Type") != tt.contentType {
			t.Errorf("%s: Content-Type %q", tt.path, w.Header().Get("Content-Type"))
		}
	}
}

// TestDeterministic checks that the data is a function of the minute only.
func TestDeterministic(t *testing.T) {
	at := func(tm time.Time) string {
		return get(t, newServer(func() time.Time { return tm }, time.UTC).routes(), "/").Body.String()
	}
	if at(epoch) != at(epoch.Add(20*time.Second)) {
		t.Error("two requests in the same minute differ")
	}
	if at(epoch) == at(epoch.Add(time.Minute)) {
		t.Error("the next minute renders the same page")
	}
}

func TestRunStopsOnCancel(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if err := run(ctx, "127.0.0.1:0", http.NotFoundHandler()); err != nil {
		t.Fatal(err)
	}
}

// TestExamplePaths checks that every examples/... path the README and the
// source mention exists, so a reader is never sent to a missing example.
func TestExamplePaths(t *testing.T) {
	mention := regexp.MustCompile(`examples/[A-Za-z0-9_./-]*[A-Za-z0-9]`)
	for _, name := range []string{"README.md", "main.go"} {
		b, err := os.ReadFile(name)
		if err != nil {
			t.Fatal(err)
		}
		for _, p := range mention.FindAllString(string(b), -1) {
			if _, err := os.Stat(filepath.Join("..", "..", "..", filepath.FromSlash(p))); err != nil {
				t.Errorf("%s mentions %s, which does not exist", name, p)
			}
		}
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

// TestPreview keeps preview/latency.svg, the picture in the README, in
// step with the code. Regenerate it with:
//
//	go test ./examples/recipes/static-page -update
func TestPreview(t *testing.T) {
	c := latencyChart(epoch.Truncate(time.Minute), time.UTC)
	c.Standalone = true // a self-contained file: embedded stylesheet, background, title
	got, err := c.Render()
	if err != nil {
		t.Fatal(err)
	}
	checkPreview(t, "latency.svg", got)
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
		t.Errorf("%s is out of date; run go test ./examples/recipes/static-page -update "+
			"(or, after a renderer change, make golden) and review it", path)
	}
}

// forbidden matches a style attribute or an inline event handler in a tag.
var forbidden = regexp.MustCompile(`(?i)<[^>]*\s(style|on[a-z]+)\s*=`)

// idAttr matches an id attribute.
var idAttr = regexp.MustCompile(`\sid="([^"]*)"`)

// checkMarkup asserts that body has at least minSVGs well-formed <svg>
// elements, no style or on* attributes and no repeated ids.
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
	seen := map[string]bool{}
	for _, m := range idAttr.FindAllStringSubmatch(body, -1) {
		if seen[m[1]] {
			t.Errorf("id %q repeats", m[1])
		}
		seen[m[1]] = true
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
