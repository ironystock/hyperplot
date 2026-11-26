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

	"github.com/ironystock/hyperplot"
)

var update = flag.Bool("update", false, "rewrite the files in preview/")

// epoch is a fixed instant, so every test sees the same data.
var epoch = time.Date(2026, 3, 3, 14, 20, 0, 0, time.UTC)

func get(t *testing.T, h http.Handler, target string) *httptest.ResponseRecorder {
	t.Helper()
	w := httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequestWithContext(t.Context(), http.MethodGet, target, nil))
	return w
}

func TestPage(t *testing.T) {
	h := newServer(func() time.Time { return epoch }, time.UTC).routes()
	tests := []struct {
		target, html, stylesheet string
		current                  []string // labels of the links marked current
	}{
		{"/", `<html lang="en">`, "/static/hx-plot.css", []string{"System", "Default"}},
		{"/?theme=dark&palette=brand", `<html lang="en" data-theme="dark">`, "/static/brand.css", []string{"Dark", "Brand"}},
		{"/?theme=light", `<html lang="en" data-theme="light">`, "/static/hx-plot.css", []string{"Light", "Default"}},
		// Untrusted values fall back to the defaults and are never echoed.
		{`/?theme="><script>x</script>&palette=../../etc`, `<html lang="en">`, "/static/hx-plot.css", []string{"System", "Default"}},
	}
	for _, tt := range tests {
		w := get(t, h, tt.target)
		if w.Code != http.StatusOK {
			t.Fatalf("%s: status %d", tt.target, w.Code)
		}
		csp := w.Header().Get("Content-Security-Policy")
		if csp != contentSecurityPolicy {
			t.Errorf("%s: CSP = %q", tt.target, csp)
		}
		// The page loads hx-plot.js from this origin and nothing else.
		if !strings.Contains(csp, "script-src 'self';") || strings.Contains(csp, "jsdelivr") {
			t.Errorf("%s: CSP allows more scripts than the page loads: %q", tt.target, csp)
		}
		body := w.Body.String()
		checkMarkup(t, body, 2)
		for _, want := range []string{tt.html, `<link rel="stylesheet" href="` + tt.stylesheet + `">`} {
			if !strings.Contains(body, want) {
				t.Errorf("%s: page lacks %s", tt.target, want)
			}
		}
		for _, label := range tt.current {
			if !regexp.MustCompile(`aria-current="true">` + label + `<`).MatchString(body) {
				t.Errorf("%s: %s is not marked current", tt.target, label)
			}
		}
		if n := strings.Count(body, `aria-current=`); n != 2 {
			t.Errorf("%s: %d current links, want 2", tt.target, n)
		}
		if strings.Contains(body, "<script>x") || strings.Contains(body, "etc") {
			t.Errorf("%s: echoed an untrusted value", tt.target)
		}
	}
}

// TestAllSlots checks that the page uses all eight series colors, so the
// palette shows in full.
func TestAllSlots(t *testing.T) {
	body := get(t, newServer(func() time.Time { return epoch }, time.UTC).routes(), "/").Body.String()
	for _, slot := range []string{"hp-s1", "hp-s2", "hp-s3", "hp-s4", "hp-s5", "hp-s6", "hp-s7", "hp-s8"} {
		if !strings.Contains(body, slot) {
			t.Errorf("no %s on the page", slot)
		}
	}
}

func TestStylesheets(t *testing.T) {
	h := newServer(time.Now, time.UTC).routes()
	if got := get(t, h, "/static/hx-plot.css").Body.String(); got != hyperplot.Stylesheet() {
		t.Error("hx-plot.css is not the default stylesheet")
	}
	w := get(t, h, "/static/brand.css")
	if w.Code != http.StatusOK || w.Header().Get("Content-Type") != "text/css; charset=utf-8" {
		t.Fatalf("brand.css: %d %q", w.Code, w.Header().Get("Content-Type"))
	}
	brand := w.Body.String()
	for _, want := range []string{
		"--hp-surface:#fbfaf7",                // the light palette
		"--hp-surface:#1b1a17",                // the dark palette
		"--hp-series-1:var(--brand, #0b6bcb)", // slot 1 follows the page's variable
		"--hp-series-2:#f47230",               // the rest keep the validated order
		`:root[data-theme="dark"] .hp`,        // the switch the links set
		"@media (prefers-color-scheme:dark)",  // and the system setting
		"@media (forced-colors:active)",       // contrast themes
	} {
		if !strings.Contains(brand, want) {
			t.Errorf("brand.css lacks %s", want)
		}
	}
	for _, path := range []string{"/static/hx-plot.js", "/static/app.css"} {
		if w := get(t, h, path); w.Code != http.StatusOK {
			t.Errorf("%s: %d", path, w.Code)
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

// TestPreview keeps preview/, the pictures in the README, in step with
// brandTheme: the regions chart as a standalone file in the brand palette,
// light and (with the dark palette in both places) dark. Regenerate with:
//
//	go test ./examples/recipes/theming -update
func TestPreview(t *testing.T) {
	brand := brandTheme()
	dark := hyperplot.Theme{Light: brand.Dark, Dark: brand.Dark}
	for _, p := range []struct {
		name  string
		theme *hyperplot.Theme
	}{{"brand-light.svg", &brand}, {"brand-dark.svg", &dark}} {
		c := regionsChart(epoch.Truncate(time.Hour), time.UTC)
		c.Standalone, c.Theme = true, p.theme
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
		t.Errorf("%s is out of date; run go test ./examples/recipes/theming -update "+
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
