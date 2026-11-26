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
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/ironystock/hyperplot"
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
	body := w.Body.String()
	// The page runs no script at all, and its policy allows none.
	if !strings.Contains(csp, "script-src 'none';") || strings.Contains(body, "<script") {
		t.Errorf("CSP %q, or the page loads a script", csp)
	}
	checkMarkup(t, body, 2*len(services))
	if n := strings.Count(body, `<tr>`); n != len(services)+1 {
		t.Errorf("%d rows, want a header and %d services", n, len(services))
	}
	for _, want := range []string{
		`<svg xmlns="http://www.w3.org/2000/svg" class="hp-spark"`,
		`aria-label="checkout requests per second, last 30 minutes`,
		` req/s`,
		`<th scope="row">inventory</th>`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("page lacks %s", want)
		}
	}
	// No script: a sparkline is not a chart, and nothing on the page moves.
	if strings.Contains(body, "<script") {
		t.Error("page loads a script")
	}
}

func TestChange(t *testing.T) {
	rows, err := buildRows(epoch)
	if err != nil {
		t.Fatal(err)
	}
	for _, r := range rows {
		traffic, _ := series(r.Service, epoch)
		up := traffic[minutes-1] > traffic[0]
		if up != (r.Dir == "up") || up != strings.HasPrefix(r.Change, "+") {
			t.Errorf("%s: %v → %v gives %s %q", r.Service, traffic[0], traffic[minutes-1], r.Dir, r.Change)
		}
	}
}

func TestAssets(t *testing.T) {
	h := newServer(time.Now, time.UTC).routes()
	for _, path := range []string{"/static/hx-plot.css", "/static/app.css"} {
		w := get(t, h, path)
		if w.Code != http.StatusOK || w.Header().Get("Content-Type") != "text/css; charset=utf-8" {
			t.Errorf("%s: %d %q", path, w.Code, w.Header().Get("Content-Type"))
		}
	}
	if w := get(t, h, "/nope"); w.Code != http.StatusNotFound {
		t.Errorf("/nope: %d", w.Code)
	}
}

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

// TestPreview keeps preview/sparklines.svg, the picture in the README, in
// step with the code: the traffic sparklines of every row with their
// names, in one file with the stylesheet embedded (a sparkline has no
// standalone mode of its own). Regenerate it with:
//
//	go test ./examples/recipes/sparkline-table -update
func TestPreview(t *testing.T) {
	rows, err := buildRows(epoch.Truncate(time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	const rowH, width = 36, 300
	height := strconv.Itoa(rowH*len(rows) + 8)
	var b bytes.Buffer
	b.WriteString(`<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 ` + strconv.Itoa(width) + ` ` + height +
		`" width="` + strconv.Itoa(width) + `" height="` + height + `" font-family="system-ui, sans-serif" font-size="13">`)
	b.WriteString(`<style>.bg{fill:#fff}.name,.now{fill:#191919}@media (prefers-color-scheme:dark){.bg{fill:#191919}.name,.now{fill:#efefef}}` +
		hyperplot.Stylesheet() + `</style><rect class="bg" width="100%" height="100%" rx="6"/>`)
	for i, r := range rows {
		y := strconv.Itoa(8 + rowH*i)
		b.WriteString(`<g transform="translate(0 ` + y + `)"><text class="name" x="12" y="19">`)
		_ = xml.EscapeText(&b, []byte(r.Service))
		b.WriteString(`</text><g transform="translate(100 0)">` + string(r.Traffic) + `</g>`)
		b.WriteString(`<text class="now" x="288" y="19" text-anchor="end">`)
		_ = xml.EscapeText(&b, []byte(r.TrafficNow))
		b.WriteString(`</text></g>`)
	}
	b.WriteString(`</svg>` + "\n")
	checkPreview(t, "sparklines.svg", b.Bytes())
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
		t.Errorf("%s is out of date; run go test ./examples/recipes/sparkline-table -update "+
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
