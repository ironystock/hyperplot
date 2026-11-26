package main

import (
	"bufio"
	"context"
	"encoding/xml"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/ironystock/hyperplot/hx"
)

// epoch is a fixed instant, so every test sees the same data.
var epoch = time.Date(2026, 3, 3, 14, 20, 0, 0, time.UTC)

// fixedServer always shows epoch; tickingServer moves one second on per
// clock read, so every stream tick is a new frame.
func fixedServer() *server {
	return newServer(func() time.Time { return epoch }, time.UTC, 5*time.Millisecond)
}

func tickingServer() *server {
	var n atomic.Int64
	return newServer(func() time.Time {
		return epoch.Add(time.Duration(n.Add(1)) * time.Second)
	}, time.UTC, 5*time.Millisecond)
}

func get(t *testing.T, h http.Handler, target string) *httptest.ResponseRecorder {
	t.Helper()
	r := httptest.NewRequestWithContext(t.Context(), http.MethodGet, target, nil)
	r.Header.Set("HX-Request", "true")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	return w
}

func TestPage(t *testing.T) {
	w := get(t, fixedServer().routes(), "/")
	if w.Code != http.StatusOK {
		t.Fatalf("status %d", w.Code)
	}
	if got := w.Header().Get("Content-Security-Policy"); got != contentSecurityPolicy {
		t.Errorf("CSP = %q", got)
	}
	body := w.Body.String()
	checkMarkup(t, body, 1)
	for _, want := range []string{
		// htmx 4.0.2 and hx-sse, pinned with SRI exactly as in examples/dashboard, not deferred.
		`<script src="https://cdn.jsdelivr.net/npm/htmx.org@4.0.2/dist/htmx.min.js" integrity="sha384-6NzMyCdh08UZ4b4M17SJJKQLdDijx4z2IqqKLKD0Q/PzCFQLpFuk3konY0WgqbTy" crossorigin="anonymous"></script>`,
		`<script src="https://cdn.jsdelivr.net/npm/htmx.org@4.0.2/dist/ext/hx-sse.min.js" integrity="sha384-5zdqOOgEI+w1bvDhFjUeS1VYpPKkc1DFc4+WnvUe849JkM4u0s7JSeGpLon7/sRU" crossorigin="anonymous"></script>`,
		`<script defer src="/static/hx-plot.js"></script>`,
		`hx-plot:autosize hx-get="/cards/throughput"`,
		`hx-trigger="load, htmx:plot:resize" hx-swap="innerHTML"`,
		`id="throughput"`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("page lacks %s", want)
		}
	}
	// The page opens no stream itself: "load" fetches the stream element
	// at the real width, so exactly one stream opens.
	if strings.Contains(body, "hx-sse:connect") {
		t.Error("page has an hx-sse:connect element")
	}
	if strings.Contains(body, "hx-ptag") {
		t.Error("page loads hx-ptag, which it does not use")
	}
}

// TestCard checks the stream element: its URL carries the container's
// width, clamped like any hp-w (docs/spec.md §4.6).
func TestCard(t *testing.T) {
	h := fixedServer().routes()
	for _, tt := range []struct{ query, width string }{
		{"?hp-w=480", "480"},
		{"", "640"},
		{"?hp-w=abc", "640"},
		{"?hp-w=99999", "4096"},
	} {
		w := get(t, h, "/cards/throughput"+tt.query)
		if w.Code != http.StatusOK {
			t.Errorf("%s: status %d", tt.query, w.Code)
			continue
		}
		body := w.Body.String()
		checkMarkup(t, body, 1)
		want := `<div class="stream" hx-sse:connect="/stream/throughput?hp-w=` + tt.width + `" hx-swap="innerMorph">`
		if !strings.HasPrefix(body, want) {
			t.Errorf("%s: card starts %.120q, want %q", tt.query, body, want)
		}
		if !strings.Contains(body, `width="`+tt.width+`"`) {
			t.Errorf("%s: chart is not %s wide", tt.query, tt.width)
		}
	}
}

type message struct{ id, data, retry string }

// openStream connects to the stream as hx-sse does and returns the
// response; cancel ends the request.
func openStream(t *testing.T, srv *httptest.Server, path string, header http.Header) (*http.Response, context.CancelFunc) {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, srv.URL+path, nil)
	if err != nil {
		t.Fatal(err)
	}
	for k, v := range header {
		req.Header[k] = v
	}
	req.Header.Set("Accept", "text/event-stream")
	res, err := srv.Client().Do(req)
	if err != nil {
		cancel()
		t.Fatal(err)
	}
	if ct := res.Header.Get("Content-Type"); ct != "text/event-stream" {
		cancel()
		t.Fatalf("Content-Type %q, want text/event-stream", ct)
	}
	return res, cancel
}

// readMessages parses n messages that carry data from an event stream.
func readMessages(t *testing.T, r io.Reader, n int) []message {
	t.Helper()
	var msgs []message
	var cur message
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 0, 64<<10), 4<<20)
	for len(msgs) < n && sc.Scan() {
		line := sc.Text()
		field, value, _ := strings.Cut(line, ": ")
		switch {
		case line == "":
			if cur.data != "" {
				msgs = append(msgs, cur)
			}
			cur = message{}
		case field == "retry":
			cur.retry = value
		case field == "id":
			cur.id = value
		case field == "data":
			if cur.data != "" {
				cur.data += "\n"
			}
			cur.data += value
		}
	}
	if len(msgs) < n {
		t.Fatalf("got %d messages, want %d (scan error: %v)", len(msgs), n, sc.Err())
	}
	return msgs
}

// TestStream reads frames as hx-sse would: each message is one complete
// chart at the stream's width, with an id, and the first carries retry:
// (docs/spec.md §4.5).
func TestStream(t *testing.T) {
	srv := httptest.NewServer(tickingServer().routes())
	defer srv.Close()
	res, cancel := openStream(t, srv, "/stream/throughput?hp-w=500", nil)
	defer cancel()
	defer res.Body.Close()
	if cc := res.Header.Get("Cache-Control"); cc != "no-cache" {
		t.Errorf("Cache-Control %q", cc)
	}
	msgs := readMessages(t, res.Body, 3)
	// hx-sse 4.0.2 applies retry only on a message that carries data.
	if msgs[0].retry != "2000" {
		t.Errorf("first message retry %q, want 2000", msgs[0].retry)
	}
	prev := int64(0)
	for i, m := range msgs {
		id, err := strconv.ParseInt(m.id, 10, 64)
		if err != nil || id <= prev {
			t.Errorf("message %d: id %q, want a second after %d", i, m.id, prev)
		}
		prev = id
		if !strings.HasPrefix(m.data, "<svg ") || !strings.Contains(m.data, ` id="throughput"`) ||
			strings.Count(m.data, "<svg ") != 1 || !strings.HasSuffix(m.data, "</svg>") {
			t.Errorf("message %d is not one chart: %.80q", i, m.data)
		}
		if !strings.Contains(m.data, `width="500"`) {
			t.Errorf("message %d is not 500 wide", i)
		}
		checkMarkup(t, m.data, 1)
	}
}

// TestStreamStopsOnDisconnect proves that the handler returns once the
// client goes away: httptest.Server.Close waits for every running handler,
// so it returns only if the stream loop has ended (no goroutine left behind).
func TestStreamStopsOnDisconnect(t *testing.T) {
	srv := httptest.NewServer(tickingServer().routes())
	defer srv.Close()
	res, cancel := openStream(t, srv, "/stream/throughput", nil)
	readMessages(t, res.Body, 2)
	cancel() // the tab closes
	_ = res.Body.Close()

	done := make(chan struct{})
	go func() {
		srv.Close()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("the stream handler is still running after the client disconnected")
	}
}

// TestStreamResume checks Last-Event-ID: a client that reconnects within
// the second it already shows does not get that second again.
func TestStreamResume(t *testing.T) {
	var n atomic.Int64
	s := newServer(func() time.Time {
		if n.Add(1) <= 3 { // a few ticks within the known second, then the next
			return epoch
		}
		return epoch.Add(time.Second)
	}, time.UTC, 5*time.Millisecond)
	srv := httptest.NewServer(s.routes())
	defer srv.Close()
	known := strconv.FormatInt(epoch.Unix(), 10)
	res, cancel := openStream(t, srv, "/stream/throughput", http.Header{hx.HeaderLastEventID: {known}})
	defer cancel()
	defer res.Body.Close()
	if got, want := readMessages(t, res.Body, 1)[0].id, strconv.FormatInt(epoch.Unix()+1, 10); got != want {
		t.Errorf("first message id %s, want %s (the second after %s)", got, want, known)
	}
}

// noFlush hides the ResponseWriter's Flush, so a stream cannot start.
type noFlush struct{ http.ResponseWriter }

// TestStreamCannotStart checks the failure answer: 503, which makes a
// reconnecting hx-sse back off, with HX-Reswap: none, so htmx swaps no
// error body over the chart (docs/spec.md §4.5).
func TestStreamCannotStart(t *testing.T) {
	r := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/stream/throughput", nil)
	w := httptest.NewRecorder()
	fixedServer().routes().ServeHTTP(noFlush{w}, r)
	if w.Code != http.StatusServiceUnavailable || w.Header().Get("HX-Reswap") != "none" {
		t.Errorf("status %d, HX-Reswap %q; want 503 and none", w.Code, w.Header().Get("HX-Reswap"))
	}
}

func TestAssets(t *testing.T) {
	h := fixedServer().routes()
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

// TestRunEndsStreams checks that Ctrl-C (ctx canceled) shuts the server
// down even while a stream is open.
func TestRunEndsStreams(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0") // find a free port
	if err != nil {
		t.Fatal(err)
	}
	addr := ln.Addr().String()
	_ = ln.Close()
	ctx, cancel := context.WithCancel(t.Context())
	errc := make(chan error, 1)
	go func() { errc <- run(ctx, addr, tickingServer().routes()) }()

	req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, "http://"+addr+"/stream/throughput", nil)
	if err != nil {
		t.Fatal(err)
	}
	var res *http.Response
	for i := 0; ; i++ {
		res, err = http.DefaultClient.Do(req)
		if err == nil {
			break
		}
		if i == 100 {
			cancel()
			t.Fatal(err)
		}
		time.Sleep(20 * time.Millisecond)
	}
	defer res.Body.Close()
	readMessages(t, res.Body, 1)
	cancel()
	select {
	case err := <-errc:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("run did not return with a stream open")
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
