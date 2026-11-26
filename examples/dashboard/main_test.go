package main

import (
	"bufio"
	"context"
	"encoding/xml"
	"errors"
	"html"
	"html/template"
	"io"
	"log"
	"math"
	"net/http"
	"net/http/httptest"
	"net/url"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"
	_ "time/tzdata" // zones with clock changes for TestRequestWindowsZones, even without system zoneinfo

	"github.com/ironystock/hyperplot/hx"
	"github.com/ironystock/hyperplot/plot"
	"github.com/ironystock/hyperplot/scale"
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

// svgClassRe finds the class attribute of an <svg> start tag.
var svgClassRe = regexp.MustCompile(`\sclass="([^"]*)"`)

// checkSVGs asserts that body holds at least minCount <svg> elements, that
// each one is well-formed XML, and that each svg.hp is a chart: the client
// treats every svg.hp as one (spec/spec.md §6.4), so a decoration such as
// a legend swatch must not carry the class hp.
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
		tag := svg[:strings.Index(svg, ">")+1]
		if m := svgClassRe.FindStringSubmatch(tag); len(m) == 2 && slices.Contains(strings.Fields(m[1]), "hp") &&
			!strings.Contains(tag, ` data-hp="1"`) {
			t.Errorf("%s: svg #%d has the class hp but is not a chart: %.120s", name, n+1, tag)
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
		{"latency zoomed", "/charts/latency?" + q + "&from=2026-03-03T13%3A30%3A00Z&to=2026-03-03T13%3A40%3A00Z", htmxHeader(), "text/html", 5, true, `data-hp-x="time 1772544600000 1772545200000"`},
		{"latency brush", "/charts/latency?" + q + "&hp-chart=latency&hp-x0=2026-03-03T13%3A30%3A00Z&hp-x1=2026-03-03T13%3A40%3A00Z", htmxHeader(), "text/html", 1, false, `<div id="latency-body"`},
		{"latency zoom out", "/charts/latency?" + q + "&zoom=out", htmxHeader(), "text/html", 1, false, `<div id="latency-body"`},
		{"requests detail", "/detail/requests?" + q + "&hp-chart=requests&hp-x=1%3A45%20PM", htmxHeader(), "text/html", 1, true, `id="drill"`},
		{"requests detail closed", "/detail/requests?" + q, htmxHeader(), "text/html", 0, true, `id="requests-detail-status"`},
		{"hx-plot.css", "/static/hx-plot.css", nil, "text/css", 0, false, ".hp-s1{"},
		{"hx-plot.js", "/static/hx-plot.js", nil, "text/javascript", 0, false, "registerExtension"},
		{"hx-plot-brush.js", "/static/hx-plot-brush.js", nil, "text/javascript", 0, false, "brush"},
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

// TestPageOpensNoStream checks that the page renders the throughput chart
// without hx-sse:connect: its container fetches the stream card on load at
// its real width, so only one stream ever opens and the chart is fitted
// before it goes live.
func TestPageOpensNoStream(t *testing.T) {
	h := newTestServer(t).routes()
	for _, header := range []http.Header{nil, htmxHeader()} {
		res := get(t, h, "/?range=15m", header)
		if strings.Contains(res.Body, `hx-sse:connect="`) {
			t.Errorf("page (htmx %v) connects a stream; the container's load trigger should", header != nil)
		}
		if !strings.Contains(res.Body, `<div class="stream">`) {
			t.Errorf("page (htmx %v) lacks the throughput chart's stream element", header != nil)
		}
	}
	page := get(t, h, "/?range=15m", nil).Body
	if !strings.Contains(page, `hx-get="/cards/throughput" hx-include="#filters" hx-trigger="load, `) {
		t.Error("the throughput container does not fetch its stream card on load")
	}
}

// TestFilterFormSync checks that a filter change made while a request is in
// flight replaces it. htmx's default ("queue first") drops every change
// after the queued one, so on a slow link the charts would show an earlier
// filter than the form.
func TestFilterFormSync(t *testing.T) {
	page := get(t, newTestServer(t).routes(), "/", nil).Body
	want := `hx-get="/" hx-trigger="change, submit" hx-target="#charts" hx-swap="innerMorph" hx-indicator="#charts" hx-sync="this:replace"`
	if !strings.Contains(page, want) {
		t.Errorf("the filter form lacks %s", want)
	}
}

// TestFocusReturn checks that the links that remove themselves name where
// keyboard focus goes (app.js moves it there after the swap).
func TestFocusReturn(t *testing.T) {
	h := newTestServer(t).routes()
	q := "range=15m"
	zoomed := get(t, h, "/?"+q+"&from=2026-03-03T13%3A30%3A00Z&to=2026-03-03T13%3A40%3A00Z", nil).Body
	if !strings.Contains(zoomed, `data-focus-return="#latency">Back to live</a>`) {
		t.Error(`"Back to live" lacks data-focus-return="#latency"`)
	}
	detail := get(t, h, "/detail/requests?"+q+"&hp-chart=requests&hp-x=1%3A45%20PM", htmxHeader()).Body
	if strings.Contains(detail, "<article") && !strings.Contains(detail, `data-focus-return="#requests">Close</a>`) {
		t.Error(`Close lacks data-focus-return="#requests"`)
	}
	if js := get(t, h, "/static/app.js", nil).Body; !strings.Contains(js, "data-focus-return") {
		t.Error("app.js does not handle data-focus-return")
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
// expects at least two messages with distinct ids, each a whole chart,
// the first carrying the retry delay.
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

	type message struct{ id, data, retry string }
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
		case strings.HasPrefix(line, "retry: "):
			cur.retry = strings.TrimPrefix(line, "retry: ")
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
	// spec/spec.md §4.5: a server SHOULD send retry:. hx-sse 4.0.2 applies
	// it only on a message that carries data, so the first one must have it.
	if msgs[0].retry != "2000" {
		t.Errorf("first message retry %q, want 2000", msgs[0].retry)
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
	go func() { done <- run(ctx, "127.0.0.1:0", 1, log.New(io.Discard, "", 0)) }()
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

// TestSlidingWindowsOverscan checks that the sliding charts draw one bucket
// before their window, so the line reaches the plot's left edge while it
// slides, and that the bucket is in neither the tooltips nor the table.
func TestSlidingWindowsOverscan(t *testing.T) {
	f := parseFilters(httptest.NewRequest(http.MethodGet, "/?range=15m", nil))
	span, step := f.Range.Span, f.Range.Step
	rows := regexp.MustCompile(`<tr id="[^"]*-r-`)
	plotLeft := regexp.MustCompile(`data-hp-plot="([\d.]+) `)
	firstX := regexp.MustCompile(`class="hp-(?:line|area)" d="M(-?[\d.]+),`)
	for _, tc := range []struct {
		name string
		c    *plot.Chart
		now  time.Time
	}{
		// On a bucket boundary: buckets end at now-span-step (outside the
		// window) and now-span … now.
		{"latency", latencyChart(f, epoch.Truncate(step), 640, time.UTC), epoch.Truncate(step)},
		// Mid-bucket: buckets end at last-span (outside) … last, then the
		// live point at now.
		{"throughput", throughputChart(f, epoch.Truncate(step).Add(3*time.Second), 640, time.UTC), epoch.Truncate(step).Add(3 * time.Second)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			svg, err := tc.c.Render()
			if err != nil {
				t.Fatal(err)
			}
			table, err := tc.c.Table()
			if err != nil {
				t.Fatal(err)
			}
			want := int(span/step) + 1 // the in-window points
			if n := len(rows.FindAllString(string(table), -1)); n != want {
				t.Errorf("table has %d rows, want %d", n, want)
			}
			if n := strings.Count(string(svg), `"k":"`); n != want {
				t.Errorf("payload has %d rows, want %d", n, want)
			}
			l, x := plotLeft.FindSubmatch(svg), firstX.FindSubmatch(svg)
			if l == nil || x == nil {
				t.Fatal("no plot rectangle or no path")
			}
			left, _ := strconv.ParseFloat(string(l[1]), 64)
			if x0, _ := strconv.ParseFloat(string(x[1]), 64); x0 >= left {
				t.Errorf("the path starts at x %v, inside the plot (left %v): no overscan", x0, left)
			}
			outside := tc.now.Add(-span).Truncate(step)
			if !outside.Before(tc.now.Add(-span)) {
				outside = outside.Add(-step)
			}
			if k := `"k":"` + outside.Format(time.RFC3339) + `"`; strings.Contains(string(svg), k) {
				t.Errorf("payload holds the overscan bucket %s", k)
			}
		})
	}
}

// ---- Brush to zoom ---------------------------------------------------------

// at parses an RFC 3339 time for the tests.
func at(t *testing.T, s string) time.Time {
	t.Helper()
	v, err := time.Parse(time.RFC3339, s)
	if err != nil {
		t.Fatal(err)
	}
	return v
}

func TestNewZoom(t *testing.T) {
	now := epoch // 14:01:15
	tests := []struct {
		name     string
		t0, t1   string
		from, to string
	}{
		{"kept", "2026-03-03T13:30:00Z", "2026-03-03T13:40:00Z", "2026-03-03T13:30:00Z", "2026-03-03T13:40:00Z"},
		{"ordered", "2026-03-03T13:40:00Z", "2026-03-03T13:30:00Z", "2026-03-03T13:30:00Z", "2026-03-03T13:40:00Z"},
		{"whole seconds, outwards", "2026-03-03T13:30:00.900Z", "2026-03-03T13:40:00.100Z", "2026-03-03T13:30:00Z", "2026-03-03T13:40:01Z"},
		{"ends at now", "2026-03-03T13:50:00Z", "2026-03-03T15:00:00Z", "2026-03-03T13:50:00Z", "2026-03-03T14:01:15Z"},
		{"widened around its middle", "2026-03-03T13:30:00Z", "2026-03-03T13:30:20Z", "2026-03-03T13:29:40Z", "2026-03-03T13:30:40Z"},
		{"widened, but not past now", "2026-03-03T14:01:00Z", "2026-03-03T14:01:10Z", "2026-03-03T14:00:15Z", "2026-03-03T14:01:15Z"},
		{"wholly in the future", "2026-03-04T00:00:00Z", "2026-03-05T00:00:00Z", "2026-03-03T14:00:15Z", "2026-03-03T14:01:15Z"},
		{"at most a day", "2026-02-20T00:00:00Z", "2026-03-03T12:00:00Z", "2026-03-02T12:00:00Z", "2026-03-03T12:00:00Z"},
		{"at most a week back", "2026-02-01T00:00:00Z", "2026-02-01T00:10:00Z", "2026-02-24T14:01:15Z", "2026-02-24T14:02:15Z"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			z := newZoom(at(t, tt.t0), at(t, tt.t1), now)
			if got, want := z.query(), (zoom{at(t, tt.from), at(t, tt.to)}).query(); got != want {
				t.Fatalf("newZoom = %s, want %s", got, want)
			}
			// Polls read the zoom back from the URL with a later clock: it
			// must stay the same window. (A window at the week's edge
			// moves with it.)
			later := now.Add(time.Hour)
			if z.From.Equal(now.Add(-maxBack)) {
				later = now
			}
			again := zoomFromQuery(mustQuery(t, z.query()), later)
			if again != z {
				t.Errorf("read back an hour later: %s, want %s", again.query(), z.query())
			}
		})
	}
}

func mustQuery(t *testing.T, q string) url.Values {
	t.Helper()
	v, err := url.ParseQuery(q)
	if err != nil {
		t.Fatal(err)
	}
	return v
}

func TestZoomStep(t *testing.T) {
	for _, tt := range []struct {
		span, step time.Duration
	}{
		{time.Minute, time.Second},
		{2 * time.Minute, time.Second},
		{10 * time.Minute, 5 * time.Second},
		{time.Hour, 30 * time.Second},
		{6 * time.Hour, 3 * time.Minute},
		{24 * time.Hour, 15 * time.Minute},
	} {
		z := zoom{From: epoch, To: epoch.Add(tt.span)}
		if got := z.step(); got != tt.step {
			t.Errorf("span %v: step %v, want %v", tt.span, got, tt.step)
		}
	}
}

func TestZoomFromQuery(t *testing.T) {
	for _, tt := range []struct {
		query string
		on    bool
	}{
		{"", false},
		{"from=2026-03-03T13:30:00Z", false},
		{"from=2026-03-03T13:30:00Z&to=bogus", false},
		{"from=2026-03-03T13:30:00Z&to=2026-03-03T13:40:00Z", true},
		{"from=2026-03-03T14:30:00%2B01:00&to=2026-03-03T13:40:00Z", true}, // any offset
	} {
		if z := zoomFromQuery(mustQuery(t, tt.query), epoch); z.on() != tt.on {
			t.Errorf("%q: zoomed %v, want %v", tt.query, z.on(), tt.on)
		}
	}
}

// TestBrushZoom covers the brush parameters the latency handler accepts:
// the extension's RFC 3339 values, and the datetime-local values of the
// zoom form, which are wall times in the chart's zone.
func TestBrushZoom(t *testing.T) {
	berlin := time.FixedZone("CET", 3600)
	tests := []struct {
		name     string
		query    string
		ok       bool
		from, to string
	}{
		{"extension", "hp-chart=latency&hp-x0=2026-03-03T13%3A40%3A00.250Z&hp-x1=2026-03-03T13%3A30%3A00Z", true, "2026-03-03T13:30:00Z", "2026-03-03T13:40:01Z"},
		{"no chart named", "hp-x0=2026-03-03T13%3A30%3A00Z&hp-x1=2026-03-03T13%3A40%3A00Z", true, "2026-03-03T13:30:00Z", "2026-03-03T13:40:00Z"},
		{"datetime-local in the chart's zone", "hp-chart=latency&hp-x0=2026-03-03T14%3A30&hp-x1=2026-03-03T14%3A40%3A30", true, "2026-03-03T13:30:00Z", "2026-03-03T13:40:30Z"},
		{"another chart", "hp-chart=scatter&hp-x0=2026-03-03T13%3A30%3A00Z&hp-x1=2026-03-03T13%3A40%3A00Z", false, "", ""},
		{"one end only", "hp-chart=latency&hp-x0=2026-03-03T13%3A30%3A00Z", false, "", ""},
		{"unparsable", "hp-chart=latency&hp-x0=yesterday&hp-x1=2026-03-03T13%3A40%3A00Z", false, "", ""},
		{"a number", "hp-chart=latency&hp-x0=1&hp-x1=2", false, "", ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/charts/latency?"+tt.query, nil)
			z, ok := brushZoom(r, epoch, berlin)
			if ok != tt.ok {
				t.Fatalf("ok = %v, want %v", ok, tt.ok)
			}
			if ok && (!z.From.Equal(at(t, tt.from)) || !z.To.Equal(at(t, tt.to))) {
				t.Errorf("zoom %s, want %s to %s", z.query(), tt.from, tt.to)
			}
		})
	}
}

var (
	hxGetAttr  = regexp.MustCompile(`<div id="latency-body"[^>]*\shx-get="([^"]*)"`)
	hxPTagAttr = regexp.MustCompile(`<div id="latency-body"[^>]*\shx-ptag="([^"]*)"`)
	domainAttr = regexp.MustCompile(`id="latency"[^>]*\sdata-hp-x="([^"]*)"`)
)

// attr returns the first group of re's first match in body, or "".
func attr(re *regexp.Regexp, body string) string {
	if m := re.FindStringSubmatch(body); len(m) > 1 {
		return m[1]
	}
	return ""
}

// TestBrushZoomsLatency plays the whole exchange of spec/spec.md §5.4: a
// brush is answered with the polling element, whose hx-get carries the
// zoom, plus the toolbar as a partial and HX-Push-Url; a poll of that
// hx-get stays zoomed and its first answer matches the seeded tag; zoom=out
// returns to the live window; and the pushed page URL shows the same zoom.
func TestBrushZoomsLatency(t *testing.T) {
	h := newTestServer(t).routes()
	const filters = "range=1h&region=eu-west"
	res := get(t, h, "/charts/latency?"+filters+"&bars=stacked&hp-w=900&hp-chart=latency"+
		"&hp-x0=2026-03-03T13%3A30%3A00Z&hp-x1=2026-03-03T13%3A40%3A00Z", htmxHeader())
	if res.Status != http.StatusOK {
		t.Fatalf("brush: status %d", res.Status)
	}
	if got := res.Header.Get(hx.HeaderPTag); got != "" {
		t.Errorf("a zoom answer carries HX-PTag %q; only polls should", got)
	}
	wantPush := "/?" + filters + "&from=2026-03-03T13%3A30%3A00Z&to=2026-03-03T13%3A40%3A00Z&bars=stacked"
	if got := res.Header.Get("HX-Push-Url"); got != wantPush {
		t.Errorf("HX-Push-Url %q, want %q", got, wantPush)
	}
	for _, want := range []string{
		`hx-sync="closest .zoom:drop"`,
		`<hx-partial hx-target="#latency-zoom" hx-swap="innerMorph">`,
		`Zoomed to 1:30 PM to 1:40 PM (10 minutes, 5-second buckets).`,
		`name="hp-x0" step="1" value="2026-03-03T13:30:00"`,
		`>Back to live</a>`,
		`viewBox="0 0 900 300"`,
	} {
		if !strings.Contains(res.Body, want) {
			t.Errorf("brush answer lacks %s", want)
		}
	}
	m := hxGetAttr.FindStringSubmatch(res.Body)
	if m == nil {
		t.Fatal("no polling element with an hx-get")
	}
	pollURL := html.UnescapeString(m[1])
	if !strings.Contains(pollURL, "from=2026-03-03T13%3A30%3A00Z&to=2026-03-03T13%3A40%3A00Z") {
		t.Errorf("the polling element's hx-get %q does not carry the zoom", pollURL)
	}
	d := attr(domainAttr, res.Body)
	if d != "time 1772544600000 1772545200000" {
		t.Errorf("zoomed chart's x domain %v, want 13:30 to 13:40", d)
	}

	// A poll of the new hx-get, at the width the brush reported, is the
	// zoomed chart, and the seeded tag already matches it.
	poll := get(t, h, pollURL+"&hp-w=900", htmxHeader())
	if dd := attr(domainAttr, poll.Body); dd != d {
		t.Errorf("a poll of the zoomed URL shows %v, want %s", dd, d)
	}
	seed := hxPTagAttr.FindStringSubmatch(res.Body)
	if seed == nil {
		t.Fatal("the polling element has no hx-ptag")
	}
	if got := strings.ReplaceAll(seed[1], "&#34;", `"`); got != poll.Header.Get(hx.HeaderPTag) {
		t.Errorf("seeded tag %s, but the first poll gets %s", got, poll.Header.Get(hx.HeaderPTag))
	}

	// The page at the pushed URL shows the same zoom.
	page := get(t, h, wantPush, nil)
	if dd := attr(domainAttr, page.Body); dd != d {
		t.Errorf("the pushed page URL shows %v, want %s", dd, d)
	}
	if !strings.Contains(page.Body, `class="zoom-mark zoom-mark-on"`) {
		t.Error("the zoomed page's toolbar does not say it is zoomed")
	}

	// Back to live.
	out := get(t, h, "/charts/latency?"+filters+"&zoom=out", htmxHeader())
	if got, want := out.Header.Get("HX-Push-Url"), "/?"+filters; got != want {
		t.Errorf("zoom out: HX-Push-Url %q, want %q", got, want)
	}
	if m := attr(hxGetAttr, out.Body); m == "" || strings.Contains(m, "from=") {
		t.Errorf("zoom out: polling element %v still carries a zoom", m)
	}
	if !strings.Contains(out.Body, "Live: last hour.") || strings.Contains(out.Body, "Back to live") {
		t.Error("zoom out: the toolbar still shows the zoom")
	}
}

// TestBrushIgnored checks that a brush the server cannot read changes
// nothing: 204, which htmx does not swap (spec/spec.md §5.3).
func TestBrushIgnored(t *testing.T) {
	h := newTestServer(t).routes()
	for _, q := range []string{
		"hp-chart=latency&hp-x0=soon&hp-x1=later",
		"hp-chart=latency&hp-x0=NaN&hp-x1=1",
		"hp-chart=requests&hp-x0=2026-03-03T13%3A30%3A00Z&hp-x1=2026-03-03T13%3A40%3A00Z",
	} {
		if res := get(t, h, "/charts/latency?"+q, htmxHeader()); res.Status != http.StatusNoContent || res.Body != "" {
			t.Errorf("%s: status %d with %d bytes, want an empty 204", q, res.Status, len(res.Body))
		}
	}
}

// TestZoomFormWithoutScript submits the zoom form as a browser does without
// script: a full page request to / with the datetime-local values.
func TestZoomFormWithoutScript(t *testing.T) {
	h := newTestServer(t).routes()
	res := get(t, h, "/?range=1h&region=us-east&hp-chart=latency&hp-x0=2026-03-03T13%3A30&hp-x1=2026-03-03T13%3A45", nil)
	if res.Status != http.StatusOK {
		t.Fatalf("status %d", res.Status)
	}
	if d := attr(domainAttr, res.Body); d != "time 1772544600000 1772545500000" {
		t.Errorf("x domain %v, want 13:30 to 13:45", d)
	}
}

// TestFilterChangeEndsZoom checks that the filter row, which sends no zoom,
// returns the latency chart to the live window.
func TestFilterChangeEndsZoom(t *testing.T) {
	res := get(t, newTestServer(t).routes(), "/?range=1h&region=us-east", htmxHeader())
	if m := attr(hxGetAttr, res.Body); m == "" || strings.Contains(m, "from=") {
		t.Errorf("polling element %v carries a zoom", m)
	}
	if got := res.Header.Get("HX-Replace-Url"); strings.Contains(got, "from=") {
		t.Errorf("HX-Replace-Url %q carries a zoom", got)
	}
}

// ---- Select to drill down --------------------------------------------------

func TestSelectedWindow(t *testing.T) {
	f := parseFilters(httptest.NewRequest(http.MethodGet, "/?range=1h&region=us-east&region=ap-south", nil))
	ws := requestWindows(f, epoch, time.UTC) // 1:00 PM … 1:50 PM
	tests := []struct {
		name    string
		query   string
		ok      bool
		start   string
		regions string
		notice  bool
	}{
		{"bar with a series", "hp-chart=requests&hp-x=1%3A20%20PM&hp-series=ap-south", true, "13:20", "ap-south", false},
		{"Enter names no series", "hp-chart=requests&hp-x=1%3A20%20PM", true, "13:20", "us-east,ap-south", false},
		{"no chart named", "hp-x=1%3A50%20PM", true, "13:50", "us-east,ap-south", false},
		{"a region not selected", "hp-chart=requests&hp-x=1%3A00%20PM&hp-series=eu-west", true, "13:00", "us-east,ap-south", false},
		{"a window no longer shown", "hp-chart=requests&hp-x=12%3A50%20PM", false, "", "", true},
		{"another chart", "hp-chart=latency&hp-x=1%3A20%20PM", false, "", "", false},
		{"nothing", "", false, "", "", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/detail/requests?"+tt.query, nil)
			w, rs, notice, ok := selectedWindow(r, f, ws)
			if ok != tt.ok || (notice != "") != tt.notice {
				t.Fatalf("ok %v, notice %q; want ok %v, notice %v", ok, notice, tt.ok, tt.notice)
			}
			if !ok {
				return
			}
			var keys []string
			for _, g := range rs {
				keys = append(keys, g.Key)
			}
			if got := w.Start.Format("15:04"); got != tt.start || strings.Join(keys, ",") != tt.regions {
				t.Errorf("window %s for %v, want %s for %s", got, keys, tt.start, tt.regions)
			}
		})
	}
}

// TestRequestWindowsZones checks the window labels, which are the requests
// chart's categories and the select key, in zones whose wall clock is not
// a whole number of hours from UTC or repeats an hour: the labels are
// unique and name each window's real start, and the page renders.
func TestRequestWindowsZones(t *testing.T) {
	zone := func(name string) *time.Location {
		t.Helper()
		loc, err := time.LoadLocation(name)
		if err != nil {
			t.Fatal(err)
		}
		return loc
	}
	ny, kolkata := zone("America/New_York"), zone("Asia/Kolkata")
	hours := parseFilters(httptest.NewRequest(http.MethodGet, "/?range=6h", nil))
	minutes := parseFilters(httptest.NewRequest(http.MethodGet, "/?range=1h", nil))
	tests := []struct {
		name string
		f    filters
		now  time.Time
		loc  *time.Location
		want string
	}{
		{"UTC", hours, epoch, time.UTC, "8 AM,9 AM,10 AM,11 AM,12 PM,1 PM"},
		// 1 AM happens twice on 2026-11-01 in New York.
		{
			"fall back", hours, time.Date(2026, 11, 1, 8, 30, 0, 0, time.UTC), ny,
			"10 PM,11 PM,12 AM,1 AM EDT,1 AM EST,2 AM",
		},
		// 2 AM never happens on 2026-03-08 in New York.
		{
			"spring forward", hours, time.Date(2026, 3, 8, 9, 30, 0, 0, time.UTC), ny,
			"10 PM,11 PM,12 AM,1 AM,3 AM,4 AM",
		},
		{
			"fall back, 10-minute windows", minutes, time.Date(2026, 11, 1, 6, 15, 0, 0, time.UTC), ny,
			"1:10 AM,1:20 AM,1:30 AM,1:40 AM,1:50 AM,1:00 AM",
		},
		{
			"half-hour offset", hours, time.Date(2026, 3, 3, 8, 15, 0, 0, time.UTC), kolkata,
			"7:30 AM,8:30 AM,9:30 AM,10:30 AM,11:30 AM,12:30 PM",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ws := requestWindows(tt.f, tt.now, tt.loc)
			labels := plot.Map(ws, func(w window) string { return w.Label })
			if got := strings.Join(labels, ","); got != tt.want {
				t.Errorf("labels %s, want %s", got, tt.want)
			}
			for i, w := range ws {
				if want := tt.now.Truncate(tt.f.Range.Window).Add(-time.Duration(len(ws)-i) * tt.f.Range.Window); !w.Start.Equal(want) {
					t.Errorf("window %d starts at %v, want %v", i, w.Start, want)
				}
			}
			if _, err := requestsChart(tt.f, tt.now, 640, tt.loc).Render(); err != nil {
				t.Errorf("requests chart: %v", err)
			}

			s := newTestServer(t)
			s.now = func() time.Time { return tt.now }
			s.loc = tt.loc
			h := s.routes()
			q := tt.f.query()
			for _, target := range []string{"/?" + q, "/charts/requests?" + q} {
				if res := get(t, h, target, nil); res.Status != http.StatusOK {
					t.Errorf("GET %s: status %d", target, res.Status)
				}
			}
			// Each label selects its own window.
			for _, w := range ws {
				sel := url.Values{hx.ParamChart: {"requests"}, hx.ParamX: {w.Label}}.Encode()
				r := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/detail/requests?"+q+"&"+sel, nil)
				got, _, _, ok := selectedWindow(r, tt.f, ws)
				if !ok || !got.Start.Equal(w.Start) {
					t.Errorf("select %q: window %v (ok %v), want %v", w.Label, got.Start, ok, w.Start)
				}
			}
		})
	}
}

// TestDrillDown requests a bar's detail the way the extension does after a
// click, and checks the detail, its live region, the width it was sent and
// that its request count repeats the bar's value.
func TestDrillDown(t *testing.T) {
	h := newTestServer(t).routes()
	res := get(t, h, "/detail/requests?range=1h&region=us-east&region=eu-west&hp-w=560&hp-chart=requests&hp-x=1%3A20%20PM&hp-series=eu-west", htmxHeader())
	if res.Status != http.StatusOK {
		t.Fatalf("status %d", res.Status)
	}
	f := parseFilters(httptest.NewRequest(http.MethodGet, "/?range=1h", nil))
	start := time.Date(2026, 3, 3, 13, 20, 0, 0, time.UTC)
	bar := math.Round(regions[1].rate(start, f.Range.Window) * f.Range.Window.Seconds())
	for _, want := range []string{
		`role="status">Showing requests for eu-west from 1:20 PM to 1:30 PM.</p>`,
		`<h3 id="requests-detail-heading">Window from 1:20 PM</h3>`,
		`id="drill" class="hp hp-anim" viewBox="0 0 560 220"`,
		`class="hp-series hp-s2" data-hp-series="eu-west"`, // the region keeps its slot
		`<dt>Requests</dt><dd>` + scale.FormatNumber(bar, 0) + `</dd>`,
		`href="/?range=1h&amp;region=us-east&amp;region=eu-west"`, // Close, without script
	} {
		if !strings.Contains(res.Body, want) {
			t.Errorf("detail lacks %s", want)
		}
	}
	if strings.Contains(res.Body, `data-hp-series="us-east"`) {
		t.Error("the detail of an eu-west bar shows us-east")
	}

	closed := get(t, h, "/detail/requests?range=1h&region=us-east", htmxHeader())
	if strings.Contains(closed.Body, "<svg") || !strings.Contains(closed.Body, `id="requests-detail-status"`) {
		t.Errorf("Close: want only the empty live region, got %s", closed.Body)
	}
	gone := get(t, h, "/detail/requests?range=1h&hp-chart=requests&hp-x=9%3A00%20AM", htmxHeader())
	if !strings.Contains(gone.Body, "no longer on the chart") {
		t.Errorf("a window that slid out: want a notice, got %s", gone.Body)
	}
}

// TestWindowLinks checks the plain-HTML equivalent of the select gesture:
// one link per bar group under the requests chart, each opening that
// window's detail, through htmx and as a page without script.
func TestWindowLinks(t *testing.T) {
	h := newTestServer(t).routes()
	res := get(t, h, "/charts/requests?range=15m&region=ap-south", htmxHeader())
	links := windowLinkRE.FindAllStringSubmatch(res.Body, -1)
	labels := []string{"1:45 PM", "1:48 PM", "1:51 PM", "1:54 PM", "1:57 PM"}
	if len(links) != len(labels) {
		t.Fatalf("found %d window links, want %d", len(links), len(labels))
	}
	for i, m := range links {
		m = m[1:] // the id is checked by TestWindowLinksKeepTheirWindow
		if m[3] != labels[i] {
			t.Errorf("link %d is %q, want %q", i, m[3], labels[i])
		}
		frag := get(t, h, html.UnescapeString(m[2]), htmxHeader())
		if !strings.Contains(frag.Body, "Window from "+labels[i]) {
			t.Errorf("link %d: the detail does not open", i)
		}
		href, anchor, _ := strings.Cut(html.UnescapeString(m[1]), "#")
		if anchor != "requests-detail" {
			t.Errorf("link %d: href %q does not scroll to the detail", i, m[1])
		}
		page := get(t, h, href, nil)
		if !strings.Contains(page.Body, `<div id="requests-detail" class="detail" hx-plot:autosize><p id="requests-detail-status" class="sr-only" role="status">Showing requests for ap-south`) {
			t.Errorf("link %d: the page without script does not open the detail", i)
		}
	}
}

// windowLinkRE matches a window link: its id, href, hx-get and label.
var (
	windowLinkRE = regexp.MustCompile(`<a id="([^"]*)" href="([^"]*)" hx-get="([^"]*)" hx-target="#requests-detail" hx-swap="innerMorph">([^<]*)</a>`)
	windowIDRE   = regexp.MustCompile(`^window-[0-9a-f]{8}$`)
)

// TestWindowLinksKeepTheirWindow checks that each window link has an id
// that names its window, so the innerMorph poll of #requests-body keeps a
// link node with its window as the windows slide. Without ids the morph
// paired the links by position: when a window closed, a focused link kept
// focus but took the next window's text and URL, and Enter opened a window
// the reader never chose.
func TestWindowLinksKeepTheirWindow(t *testing.T) {
	s := newTestServer(t)
	h := s.routes()
	// links returns label -> id of the window links at now.
	links := func(now time.Time) (map[string]string, []string) {
		t.Helper()
		s.now = func() time.Time { return now }
		res := get(t, h, "/charts/requests?range=15m", htmxHeader())
		byLabel := map[string]string{}
		var order []string
		seen := map[string]bool{}
		for _, m := range windowLinkRE.FindAllStringSubmatch(res.Body, -1) {
			id, label := m[1], m[4]
			if !windowIDRE.MatchString(id) {
				t.Errorf("link %q has id %q, want window-{8 hex digits}", label, id)
			}
			if seen[id] {
				t.Errorf("id %q appears twice", id)
			}
			seen[id] = true
			byLabel[label] = id
			order = append(order, label)
		}
		if len(order) != 5 {
			t.Fatalf("found %d window links at %v, want 5", len(order), now)
		}
		return byLabel, order
	}
	before, labels0 := links(epoch)
	after, labels1 := links(epoch.Add(3 * time.Minute)) // one 15m window later
	if labels0[0] != "1:45 PM" || labels1[0] != "1:48 PM" || labels1[4] != "2:00 PM" {
		t.Fatalf("windows did not slide by one: %v, then %v", labels0, labels1)
	}
	for label, id := range before {
		if got, ok := after[label]; ok && got != id {
			t.Errorf("window %q: id %q, then %q after the slide", label, id, got)
		}
	}
	if _, ok := after["1:45 PM"]; ok {
		t.Error("the window that slid out still has a link")
	}
	for _, id := range after {
		if id == before["1:45 PM"] {
			t.Errorf("a new window reuses the id %q of the window that slid out", id)
		}
	}
}

func TestSpanText(t *testing.T) {
	for d, want := range map[time.Duration]string{
		0:                                     "0 seconds",
		time.Second:                           "1 second",
		90 * time.Second:                      "1 minute 30 seconds",
		2 * time.Hour:                         "2 hours",
		time.Hour + time.Minute + 1e9:         "1 hour 1 minute 1 second",
		24*time.Hour + 30*time.Second:         "24 hours 30 seconds",
		10*time.Minute + 500*time.Millisecond: "10 minutes",
	} {
		if got := spanText(d); got != want {
			t.Errorf("spanText(%v) = %q, want %q", d, got, want)
		}
	}
}

func TestRangeText(t *testing.T) {
	a := time.Date(2026, 3, 3, 13, 5, 0, 0, time.UTC)
	if got, want := rangeText(a, a.Add(10*time.Minute+30*time.Second), time.UTC), "1:05 PM to 1:15:30 PM"; got != want {
		t.Errorf("same day: %q, want %q", got, want)
	}
	if got, want := rangeText(a, a.Add(12*time.Hour), time.UTC), "Mar 3, 1:05 PM to Mar 4, 1:05 AM"; got != want {
		t.Errorf("two days: %q, want %q", got, want)
	}
}

func TestFastClock(t *testing.T) {
	now := epoch
	clock := fastClock(func() time.Time { return now }, 5)
	now = epoch.Add(2 * time.Second)
	if got, want := clock(), epoch.Add(10*time.Second); !got.Equal(want) {
		t.Errorf("after 2s at speed 5: %v, want %v", got, want)
	}
}
