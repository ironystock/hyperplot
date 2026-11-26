package main

import (
	"bufio"
	"context"
	"encoding/xml"
	"errors"
	"html/template"
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

// epoch is a fixed instant in the middle of a latency bucket for every
// range (buckets are at most 3 minutes long), so small clock steps never
// close a bucket by accident.
var epoch = time.Date(2026, 3, 3, 14, 1, 15, 0, time.UTC)

// newTestServer returns a dashboard server with a fixed clock in UTC and
// a fast SSE tick.
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
		svg := rest[i : i+j+len("</svg>")]
		if err := wellFormed(svg); err != nil {
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

func TestRoutes(t *testing.T) {
	h := newTestServer(t).routes()
	q := "range=15m&region=us-east&region=ap-south"
	tests := []struct {
		name        string
		target      string
		header      http.Header
		contentType string
		svgs        int    // minimum number of well-formed <svg> elements
		fragment    bool   // the whole body must be well-formed XML too
		contains    string // a marker the body must contain
	}{
		{"page", "/", nil, "text/html", 9, false, `id="charts"`},
		{"page with filters", "/?" + q + "&bars=stacked", nil, "text/html", 9, false, `value="stacked" checked`},
		{"charts fragment", "/?" + q, htmxHeader(), "text/html", 9, false, `id="kpis"`},
		{"latency", "/charts/latency?" + q, htmxHeader(), "text/html", 5, true, `<hx-partial hx-target="#kpis"`},
		{"requests grouped", "/charts/requests?" + q, htmxHeader(), "text/html", 1, true, `id="requests"`},
		{"requests stacked", "/charts/requests?" + q + "&bars=stacked", htmxHeader(), "text/html", 1, true, `id="requests"`},
		{"scatter", "/charts/scatter?" + q, htmxHeader(), "text/html", 1, true, `class="hp-dot"`},
		{"throughput card", "/cards/throughput?" + q + "&hp-w=480", htmxHeader(), "text/html", 1, true, `hx-sse:connect="/stream/throughput?`},
		{"hyperplot.css", "/static/hyperplot.css", nil, "text/css", 0, false, ".hp-s1{"},
		{"hyperplot.js", "/static/hyperplot.js", nil, "text/javascript", 0, false, "registerExtension"},
		{"app.css", "/static/app.css", nil, "text/css", 0, false, "--page"},
		{"app.js", "/static/app.js", nil, "text/javascript", 0, false, "data-theme-choice"},
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
			if res.Header.Get("Content-Security-Policy") == "" {
				t.Error("missing Content-Security-Policy")
			}
			if !strings.Contains(res.Body, tt.contains) {
				t.Errorf("body lacks %q", tt.contains)
			}
			checkSVGs(t, tt.name, res.Body, tt.svgs)
			if tt.fragment {
				if err := wellFormed("<root>" + res.Body + "</root>"); err != nil {
					t.Errorf("fragment is not well-formed: %v", err)
				}
			}
		})
	}
}

func TestUnknownRouteIs404(t *testing.T) {
	if res := get(t, newTestServer(t).routes(), "/nope", nil); res.Status != http.StatusNotFound {
		t.Errorf("status %d, want 404", res.Status)
	}
}

// TestPTagRoundTrip polls like hx-ptag does: the first poll gets a tag,
// and echoing it gets 304 with no body.
func TestPTagRoundTrip(t *testing.T) {
	h := newTestServer(t).routes()
	for _, target := range []string{
		"/charts/latency?range=15m",
		"/charts/requests?range=1h&bars=stacked",
		"/charts/scatter?range=6h&region=eu-west",
	} {
		t.Run(target, func(t *testing.T) {
			first := get(t, h, target, htmxHeader())
			tag := first.Header.Get(hx.HeaderPTag)
			if first.Status != http.StatusOK || tag == "" || first.Body == "" {
				t.Fatalf("first poll: status %d, tag %q, %d bytes", first.Status, tag, len(first.Body))
			}
			header := htmxHeader()
			header.Set(hx.HeaderPTag, tag)
			if again := get(t, h, target, header); again.Status != http.StatusNotModified || again.Body != "" {
				t.Fatalf("repeat poll: status %d with %d bytes, want a bodiless 304", again.Status, len(again.Body))
			}
			// The ptag extension echoes the tag verbatim, but a seeded
			// hx-ptag attribute is often unquoted.
			header.Set(hx.HeaderPTag, strings.Trim(tag, `"`))
			if res := get(t, h, target, header); res.Status != http.StatusNotModified {
				t.Errorf("unquoted tag: status %d, want 304", res.Status)
			}
			// A different width is different content.
			if res := get(t, h, target+"&hp-w=500", header); res.Status != http.StatusOK {
				t.Errorf("new width: status %d, want 200", res.Status)
			}
		})
	}
}

// TestPTagFollowsBuckets checks that the tag is stable inside a latency
// bucket and changes when the bucket closes, and that the page seeds the
// tag its first poll will get.
func TestPTagFollowsBuckets(t *testing.T) {
	s := newTestServer(t)
	h := s.routes()
	const target = "/charts/latency?range=15m" // 10-second buckets
	tagAt := func(now time.Time) string {
		s.now = func() time.Time { return now }
		return get(t, h, target, htmxHeader()).Header.Get(hx.HeaderPTag)
	}
	start := epoch.Truncate(10 * time.Second)
	a, b, c := tagAt(start), tagAt(start.Add(9*time.Second)), tagAt(start.Add(10*time.Second))
	if a != b {
		t.Errorf("tag changed inside a bucket: %s then %s", a, b)
	}
	if b == c {
		t.Errorf("tag did not change when the bucket closed: %s", c)
	}

	s.now = func() time.Time { return epoch }
	page := get(t, h, "/?range=15m", nil).Body
	want := `hx-ptag="` + strings.ReplaceAll(tagAt(epoch), `"`, "&#34;") + `"`
	if !strings.Contains(page, want) {
		t.Errorf("page does not seed the latency tag %s", want)
	}
}

// TestSSE reads the throughput stream over a real HTTP connection and
// expects at least two messages with distinct ids, each a whole chart.
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
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, srv.URL+"/stream/throughput?range=15m&hp-w=500", nil)
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
	sc := bufio.NewScanner(res.Body)
	sc.Buffer(make([]byte, 0, 64<<10), 4<<20)
	for len(msgs) < 3 && sc.Scan() {
		line := sc.Text()
		switch {
		case line == "":
			if cur.data != "" {
				msgs = append(msgs, cur)
			}
			cur = message{}
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
	if len(msgs) < 2 {
		t.Fatalf("got %d messages, want at least 2 (scan error: %v)", len(msgs), sc.Err())
	}
	ids := map[string]bool{}
	for i, m := range msgs {
		if m.id == "" || ids[m.id] {
			t.Errorf("message %d: id %q is missing or repeated", i, m.id)
		}
		ids[m.id] = true
		if !strings.Contains(m.data, `id="throughput"`) || !strings.Contains(m.data, `width="500"`) {
			t.Errorf("message %d is not the throughput chart at the requested width", i)
		}
		checkSVGs(t, "sse message", m.data, 1)
		if err := wellFormed("<root>" + m.data + "</root>"); err != nil {
			t.Errorf("message %d is not well-formed: %v", i, err)
		}
	}
}

// firstEvent opens the throughput stream on srv as a reconnecting client
// that last saw lastID, checks that the response really is an event
// stream, and returns the id of the first message that carries data.
func firstEvent(t *testing.T, srv *httptest.Server, lastID string) string {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, srv.URL+"/stream/throughput?range=15m", nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Accept", "text/event-stream")
	req.Header.Set(hx.HeaderLastEventID, lastID)
	res, err := srv.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(io.LimitReader(res.Body, 200))
		t.Fatalf("status %d, want 200: %q", res.StatusCode, b)
	}
	if ct := res.Header.Get("Content-Type"); !strings.HasPrefix(ct, "text/event-stream") {
		t.Fatalf("Content-Type %q, want text/event-stream", ct)
	}
	sc := bufio.NewScanner(res.Body)
	sc.Buffer(make([]byte, 0, 64<<10), 4<<20)
	var id string
	for sc.Scan() {
		line := sc.Text()
		switch {
		case line == "":
			id = ""
		case strings.HasPrefix(line, "id: "):
			id = strings.TrimPrefix(line, "id: ")
		case strings.HasPrefix(line, "data: "):
			if id == "" {
				t.Fatalf("message without an id: %.200s", line)
			}
			return id
		}
	}
	t.Fatalf("stream ended with no message: %v", sc.Err())
	return ""
}

// TestSSEResume checks Last-Event-ID handling. A client that reconnects
// within the second it already shows gets no duplicate: its first message
// is the next second, once the clock reaches it. A client that last saw an
// older second gets the current one at once.
func TestSSEResume(t *testing.T) {
	sec := func(t time.Time) string { return strconv.FormatInt(t.Unix(), 10) }

	t.Run("skips the known second", func(t *testing.T) {
		s := newTestServer(t)
		var calls atomic.Int64
		s.now = func() time.Time {
			// Several ticks within the known second, then the next one.
			if calls.Add(1) <= 5 {
				return epoch
			}
			return epoch.Add(time.Second)
		}
		srv := httptest.NewServer(s.routes())
		defer srv.Close()
		if got, want := firstEvent(t, srv, sec(epoch)), sec(epoch.Add(time.Second)); got != want {
			t.Errorf("first message id %s, want %s (the second after the one the client had)", got, want)
		}
	})

	t.Run("sends a newer second at once", func(t *testing.T) {
		srv := httptest.NewServer(newTestServer(t).routes())
		defer srv.Close()
		if got, want := firstEvent(t, srv, sec(epoch.Add(-time.Second))), sec(epoch); got != want {
			t.Errorf("first message id %s, want %s", got, want)
		}
	})
}

func TestParseFilters(t *testing.T) {
	tests := []struct {
		query    string
		rng      string
		regions  string
		none     bool
		stacked  bool
		canonQry string
	}{
		{"", "1h", "us-east,eu-west,ap-south", false, false, "range=1h&region=us-east&region=eu-west&region=ap-south"},
		{"range=6h&region=ap-south&region=us-east", "6h", "us-east,ap-south", false, false, "range=6h&region=us-east&region=ap-south"},
		{"range=bogus&region=mars", "1h", "us-east,eu-west,ap-south", true, false, "range=1h&region="},
		{"range=15m&region=", "15m", "us-east,eu-west,ap-south", true, false, "range=15m&region="},
		{"range=15m", "15m", "us-east,eu-west,ap-south", false, false, "range=15m&region=us-east&region=eu-west&region=ap-south"},
		{"range=6h&region=&region=eu-west", "6h", "eu-west", false, false, "range=6h&region=eu-west"},
		{"bars=stacked&region=eu-west&region=eu-west", "1h", "eu-west", false, true, "range=1h&region=eu-west"},
	}
	for _, tt := range tests {
		t.Run(tt.query, func(t *testing.T) {
			r := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/?"+tt.query, nil)
			f := parseFilters(r)
			var keys []string
			for _, g := range f.Regions {
				keys = append(keys, g.Key)
			}
			if f.Range.Key != tt.rng || strings.Join(keys, ",") != tt.regions || f.NoneSelected != tt.none || f.Stacked != tt.stacked {
				t.Errorf("got range %s, regions %v, none %v, stacked %v", f.Range.Key, keys, f.NoneSelected, f.Stacked)
			}
			if got := f.query(); got != canon(tt.canonQry) {
				t.Errorf("query() = %s, want %s", got, canon(tt.canonQry))
			}
			// query round-trips.
			r2 := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/?"+f.query(), nil)
			if g := parseFilters(r2); g.query() != f.query() || g.NoneSelected != f.NoneSelected {
				t.Errorf("query %s does not round-trip", f.query())
			}
		})
	}
}

// canon puts a query string in url.Values.Encode order.
func canon(q string) string {
	v, _ := url.ParseQuery(q)
	return v.Encode()
}

// TestSlotsSurviveFilters checks that a region keeps its color when the
// regions before it are filtered out.
func TestSlotsSurviveFilters(t *testing.T) {
	h := newTestServer(t).routes()
	for _, target := range []string{
		"/charts/requests?range=1h&region=ap-south",
		"/cards/throughput?range=1h&region=eu-west&region=ap-south",
		"/charts/scatter?range=1h&region=ap-south",
	} {
		if body := get(t, h, target, htmxHeader()).Body; !strings.Contains(body, `class="hp-series hp-s3" data-hp-series="ap-south"`) {
			t.Errorf("%s: ap-south lost its slot 3", target)
		}
	}
}

// TestDeterministic renders everything twice at the same instant and
// expects identical bytes: the simulation has no hidden state.
func TestDeterministic(t *testing.T) {
	h := newTestServer(t).routes()
	for _, target := range []string{"/?range=6h", "/charts/latency", "/cards/throughput", "/charts/scatter"} {
		a := get(t, h, target, htmxHeader()).Body
		b := get(t, h, target, htmxHeader()).Body
		if a != b {
			t.Errorf("%s: two renders at the same instant differ", target)
		}
	}
}

// TestFilterResponse checks that a filter change renders each chart at the
// width its hidden input reported, gives the address bar a clean URL, and
// tells the throughput card to reconnect.
func TestFilterResponse(t *testing.T) {
	h := newTestServer(t).routes()
	res := get(t, h, "/?range=15m&region=eu-west&bars=stacked&w-latency=1100&w-requests=530&w-scatter=530&w-throughput=99999", htmxHeader())
	for _, want := range []string{
		`id="latency" class="hp hp-anim" viewBox="0 0 1100 300"`,
		`id="requests" class="hp hp-anim" viewBox="0 0 530 260"`,
		`id="throughput" class="hp hp-anim" viewBox="0 0 4096 260"`, // clamped
		`name="w-scatter" value="530"`,
	} {
		if !strings.Contains(res.Body, want) {
			t.Errorf("filter response lacks %s", want)
		}
	}
	if got, want := res.Header.Get("HX-Replace-Url"), "/?range=15m&region=eu-west&bars=stacked"; got != want {
		t.Errorf("HX-Replace-Url = %q, want %q", got, want)
	}
	if got := res.Header.Get("HX-Trigger"); got != filtersApplied {
		t.Errorf("HX-Trigger = %q, want %q (the throughput card reconnects on it)", got, filtersApplied)
	}
}

// TestRenderFailureKeepsLastChart checks the error policy: htmx swaps
// every response except 204 and 304, so a failed fragment is a 204.
func TestRenderFailureKeepsLastChart(t *testing.T) {
	s := newTestServer(t)
	s.tmpl = template.Must(template.New("empty").Parse("")) // no templates to execute
	h := s.routes()
	if res := get(t, h, "/charts/scatter", htmxHeader()); res.Status != http.StatusNoContent {
		t.Errorf("htmx request: status %d, want 204", res.Status)
	}
	if res := get(t, h, "/", nil); res.Status != http.StatusInternalServerError {
		t.Errorf("page: status %d, want 500", res.Status)
	}
}

// noFlush hides the Flush and Unwrap methods of the recorder it wraps, like
// a logging middleware that forgets them.
type noFlush struct{ http.ResponseWriter }

// A stream that cannot start must not answer htmx with an error body:
// the response is not an event stream, so htmx would swap the text over
// the chart. HX-Reswap: none stops the swap, and the status must not be
// 2xx: hx-sse resets its reconnect backoff on any 2xx, so a 204 would
// have every open tab retrying at the base delay for as long as the
// failure lasts.
func TestStreamFailureKeepsLastChart(t *testing.T) {
	s := newTestServer(t)
	r := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/stream/throughput", nil)
	for k, v := range htmxHeader() {
		r.Header[k] = v
	}
	rec := httptest.NewRecorder()
	s.routes().ServeHTTP(noFlush{rec}, r)
	if rec.Code != http.StatusServiceUnavailable || rec.Body.Len() != 0 {
		t.Errorf("status %d with %d-byte body, want 503 and no body", rec.Code, rec.Body.Len())
	}
	if got := rec.Header().Get("HX-Reswap"); got != "none" {
		t.Errorf("HX-Reswap %q, want none", got)
	}
}

func TestRunServesAndShutsDown(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan error, 1)
	go func() { done <- run(ctx, "127.0.0.1:0", log.New(io.Discard, "", 0)) }()
	time.Sleep(50 * time.Millisecond)
	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("run: %v", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("run did not shut down")
	}
}

// TestHTTPServerTimeouts checks that the dashboard's http.Server bounds
// slow clients: one that trickles in its request headers or body, and one
// that stops reading a response.
func TestHTTPServerTimeouts(t *testing.T) {
	srv := newHTTPServer(t.Context(), http.NotFoundHandler(), log.New(io.Discard, "", 0))
	if srv.ReadHeaderTimeout <= 0 {
		t.Error("ReadHeaderTimeout is unset: a client can hold a connection by trickling its headers")
	}
	if srv.ReadTimeout <= 0 {
		t.Error("ReadTimeout is unset: a client can hold a connection by trickling a request body")
	}
	if srv.WriteTimeout <= 0 {
		t.Error("WriteTimeout is unset: a client that never reads blocks its handler in Write forever")
	}
}

// TestStreamOutlivesServerTimeouts serves the dashboard with its own
// http.Server configuration, its ReadTimeout and WriteTimeout cut to a
// fraction of the stream's life, and expects the throughput stream to
// keep delivering well past both: hx.Stream moves the write deadline on
// every message, and net/http clears the read deadline once the request
// has been read.
func TestStreamOutlivesServerTimeouts(t *testing.T) {
	s := newTestServer(t)
	s.tick = 40 * time.Millisecond
	var calls atomic.Int64
	s.now = func() time.Time { // one second per message
		return epoch.Add(time.Duration(calls.Add(1)) * time.Second)
	}
	const timeout = 100 * time.Millisecond
	ts := httptest.NewUnstartedServer(nil)
	ts.Config = newHTTPServer(t.Context(), s.routes(), log.New(io.Discard, "", 0))
	ts.Config.ReadTimeout = timeout
	ts.Config.WriteTimeout = timeout
	ts.Start()
	defer ts.Close()

	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, ts.URL+"/stream/throughput?range=15m", nil)
	if err != nil {
		t.Fatal(err)
	}
	start := time.Now()
	res, err := ts.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	sc := bufio.NewScanner(res.Body)
	sc.Buffer(make([]byte, 0, 64<<10), 4<<20)
	n := 0
	for sc.Scan() {
		if strings.HasPrefix(sc.Text(), "id: ") {
			n++
		}
		if time.Since(start) > 4*timeout && n >= 4 {
			return
		}
	}
	t.Fatalf("stream ended after %v and %d messages (server timeouts %v): %v",
		time.Since(start).Round(time.Millisecond), n, timeout, sc.Err())
}

// idAttr and labelledByAttr match id and aria-labelledby attributes in
// html/template output, which always double-quotes attribute values.
var (
	idAttr         = regexp.MustCompile(`\sid="([^"]*)"`)
	labelledByAttr = regexp.MustCompile(`\saria-labelledby="([^"]*)"`)
)

// TestUniqueIDs checks that no id appears twice on the page or in the
// filter response, and that every aria-labelledby reference names exactly
// one element. A duplicate id breaks more than validity: aria-labelledby
// resolves to the first match, and htmx's morph cannot match nodes by a
// duplicated id, so it replaces them instead of morphing them.
func TestUniqueIDs(t *testing.T) {
	h := newTestServer(t).routes()
	for _, tt := range []struct {
		name, target string
		header       http.Header
	}{
		{"page", "/", nil},
		{"charts fragment", "/?range=1h&region=us-east&bars=stacked", htmxHeader()},
	} {
		t.Run(tt.name, func(t *testing.T) {
			res := get(t, h, tt.target, tt.header)
			if res.Status != http.StatusOK {
				t.Fatalf("status %d, want 200", res.Status)
			}
			count := map[string]int{}
			for _, m := range idAttr.FindAllStringSubmatch(res.Body, -1) {
				count[m[1]]++
			}
			if len(count) == 0 {
				t.Fatal("found no ids")
			}
			for id, n := range count {
				if n > 1 {
					t.Errorf("id %q appears %d times", id, n)
				}
			}
			for _, m := range labelledByAttr.FindAllStringSubmatch(res.Body, -1) {
				for _, id := range strings.Fields(m[1]) {
					if count[id] != 1 {
						t.Errorf("aria-labelledby names %q, which appears %d times", id, count[id])
					}
				}
			}
		})
	}
}

// TestCSPScriptSources checks that script-src trusts no whole third-party
// host, since a public CDN serves anyone's code, and that it still allows
// every script the page loads. A source with a path that ends in "/"
// matches by prefix (CSP Level 2 and later), so pinning the package
// version's directory allows exactly the files the page uses.
func TestCSPScriptSources(t *testing.T) {
	res := get(t, newTestServer(t).routes(), "/", nil)
	var sources []string
	for _, d := range strings.Split(res.Header.Get("Content-Security-Policy"), ";") {
		if f := strings.Fields(d); len(f) > 0 && f[0] == "script-src" {
			sources = f[1:]
		}
	}
	if len(sources) == 0 {
		t.Fatal("the policy has no script-src directive")
	}
	var hosts []string // the non-keyword sources
	for _, src := range sources {
		if strings.HasPrefix(src, "'") {
			continue // a keyword such as 'self'
		}
		u, err := url.Parse(src)
		if err != nil || u.Scheme != "https" || u.Host == "" {
			t.Errorf("script-src %q is not an https URL", src)
			continue
		}
		if !strings.HasSuffix(u.Path, "/") || !strings.Contains(u.Path, "@") {
			t.Errorf("script-src %q trusts more than one pinned package version", src)
		}
		hosts = append(hosts, src)
	}
	scripts := regexp.MustCompile(`<script[^>]*\ssrc="([^"]+)"`).FindAllStringSubmatch(res.Body, -1)
	if len(scripts) == 0 {
		t.Fatal("the page loads no scripts")
	}
	for _, m := range scripts {
		src := m[1]
		if strings.HasPrefix(src, "/") && !strings.HasPrefix(src, "//") {
			continue // same origin: 'self'
		}
		allowed := false
		for _, h := range hosts {
			allowed = allowed || strings.HasPrefix(src, h)
		}
		if !allowed {
			t.Errorf("the policy blocks the page's script %s", src)
		}
	}
}
