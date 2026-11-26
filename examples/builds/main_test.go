package main

import (
	"context"
	"encoding/xml"
	"errors"
	"html"
	"io"
	"log"
	"net/http"
	"net/http/httptest"
	"net/url"
	"regexp"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/ironystock/hyperplot"
	"github.com/ironystock/hyperplot/hx"
)

// epoch is a fixed instant, a Wednesday afternoon, so the 14-day range
// holds weekdays and weekends.
var epoch = time.Date(2026, 3, 4, 15, 20, 30, 0, time.UTC)

// newTestServer returns a server with a fixed clock in UTC.
func newTestServer(t *testing.T) *server {
	t.Helper()
	s, err := newServer(log.New(io.Discard, "", 0))
	if err != nil {
		t.Fatal(err)
	}
	s.now = func() time.Time { return epoch }
	s.loc = time.UTC
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
		if err := wellFormed(rest[i : i+j+len("</svg>")]); err != nil {
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

// A day of the range at epoch, and its URL-encoded label.
const (
	someDay  = "Mar 3"
	someDayQ = "Mar+3"
)

func TestRoutes(t *testing.T) {
	h := newTestServer(t).routes()
	q := "range=7d&pipeline=api&pipeline=mobile"
	zoom := "&from=2026-03-02T00%3A00%3A00Z&to=2026-03-03T00%3A00%3A00Z"
	tests := []struct {
		name, target string
		header       http.Header
		contentType  string
		svgs         int    // minimum number of well-formed <svg> elements
		fragment     bool   // the whole body must be well-formed XML too
		contains     string // a marker the body must contain
	}{
		{"page", "/", nil, "text/html", 5, false, `id="charts"`},
		{"page with filters", "/?" + q + "&branch=main", nil, "text/html", 4, false, `<option value="main" selected>`},
		{"charts fragment", "/?" + q, htmxHeader(), "text/html", 4, false, `id="summary"`},
		{"durations", "/charts/durations?" + q, htmxHeader(), "text/html", 1, true, `id="durations"`},
		{"durations zoomed", "/charts/durations?" + q + zoom, htmxHeader(), "text/html", 1, true, `data-hp-x="time 1772409600000 1772496000000"`},
		{"durations brush", "/charts/durations?" + q + "&hp-chart=durations&hp-x0=2026-03-02T00%3A00%3A00Z&hp-x1=2026-03-03T00%3A00%3A00Z", htmxHeader(), "text/html", 1, false, `<div id="durations-body"`},
		{"durations zoom out", "/charts/durations?" + q + "&zoom=out", htmxHeader(), "text/html", 1, false, `<div id="durations-body"`},
		{"days", "/charts/days?" + q, htmxHeader(), "text/html", 1, true, `<hx-partial hx-target="#summary"`},
		{"trends", "/charts/trends?" + q, htmxHeader(), "text/html", 2, true, `id="trend-mobile"`},
		{"day detail", "/detail/day?" + q + "&hp-chart=days&hp-x=" + someDayQ, htmxHeader(), "text/html", 0, true, `<table class="builds">`},
		{"day detail closed", "/detail/day?" + q, htmxHeader(), "text/html", 0, true, `id="day-detail-status"`},
		{"hx-plot.css", "/static/hx-plot.css", nil, "text/css", 0, false, ".hp-s1{"},
		{"hx-plot.js", "/static/hx-plot.js", nil, "text/javascript", 0, false, "registerExtension"},
		{"hx-plot-brush.js", "/static/hx-plot-brush.js", nil, "text/javascript", 0, false, "brush"},
		{"app.css", "/static/app.css", nil, "text/css", 0, false, "--page"},
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
			if res.Header.Get("Content-Security-Policy") != contentSecurityPolicy {
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

// unsafeMarkup matches what a strict CSP blocks or the spec forbids: style
// attributes, inline event handlers, inline scripts and style elements.
var unsafeMarkup = regexp.MustCompile(`(?i)\sstyle=|\son[a-z]+=|<script(\s[^>]*)?>[^<]|<script>|<style`)

func TestNoInlineStyleOrScript(t *testing.T) {
	h := newTestServer(t).routes()
	q := "range=14d&hp-chart=days&hp-x=" + someDayQ + "&from=2026-03-02T00%3A00%3A00Z&to=2026-03-03T00%3A00%3A00Z"
	for _, target := range []string{
		"/?" + q, "/charts/durations?" + q, "/charts/days?" + q, "/charts/trends?" + q, "/detail/day?" + q,
	} {
		body := get(t, h, target, htmxHeader()).Body
		if m := unsafeMarkup.FindString(body); m != "" {
			t.Errorf("%s: found %q", target, m)
		}
	}
	page := get(t, h, "/?"+q, nil).Body
	if m := unsafeMarkup.FindString(page); m != "" {
		t.Errorf("page: found %q", m)
	}
}

// TestPTagRoundTrip polls like hx-ptag does: the first poll gets a tag,
// and echoing it, in any form CheckPTag accepts, gets 304 with no body.
func TestPTagRoundTrip(t *testing.T) {
	h := newTestServer(t).routes()
	for _, target := range []string{
		"/charts/durations?range=7d",
		"/charts/durations?range=14d&from=2026-03-02T00%3A00%3A00Z&to=2026-03-03T00%3A00%3A00Z",
		"/charts/days?range=14d&branch=feature",
		"/charts/trends?range=7d&pipeline=web",
	} {
		t.Run(target, func(t *testing.T) {
			first := get(t, h, target, htmxHeader())
			tag := first.Header.Get(hx.HeaderPTag)
			if first.Status != http.StatusOK || tag == "" || first.Body == "" {
				t.Fatalf("first poll: status %d, tag %q, %d bytes", first.Status, tag, len(first.Body))
			}
			for _, echo := range []string{tag, strings.Trim(tag, `"`), "W/" + tag, " " + tag + " "} {
				header := htmxHeader()
				header.Set(hx.HeaderPTag, echo)
				if res := get(t, h, target, header); res.Status != http.StatusNotModified || res.Body != "" {
					t.Errorf("echo %q: status %d with %d bytes, want a bodiless 304", echo, res.Status, len(res.Body))
				}
			}
			header := htmxHeader()
			header.Set(hx.HeaderPTag, `"hp-0000000000000000"`)
			if res := get(t, h, target, header); res.Status != http.StatusOK {
				t.Errorf("another tag: status %d, want 200", res.Status)
			}
			// A different width is different content.
			header.Set(hx.HeaderPTag, tag)
			if res := get(t, h, target+"&hp-w=500", header); res.Status != http.StatusOK {
				t.Errorf("new width: status %d, want 200", res.Status)
			}
		})
	}
}

// ptagAttr matches the seeded hx-ptag attributes; html/template writes the
// tag's quotes as &#34;.
var ptagAttr = regexp.MustCompile(`id="([a-z]+)-body"[^>]*\shx-get="([^"]*)"[^>]*\shx-ptag="([^"]*)"`)

// TestPageSeedsTags checks that each poller's hx-ptag is the tag its first
// poll at the same width gets, so that poll is a 304.
func TestPageSeedsTags(t *testing.T) {
	h := newTestServer(t).routes()
	page := get(t, h, "/?range=7d&branch=main", nil).Body
	found := map[string]bool{}
	for _, m := range ptagAttr.FindAllStringSubmatch(page, -1) {
		id, u, seed := m[1], html.UnescapeString(m[2]), html.UnescapeString(m[3])
		found[id] = true
		header := htmxHeader()
		header.Set(hx.HeaderPTag, seed)
		if res := get(t, h, u, header); res.Status != http.StatusNotModified {
			t.Errorf("%s: first poll with the seed got %d, want 304", id, res.Status)
		}
	}
	for _, id := range []string{"durations", "days", "trends"} {
		if !found[id] {
			t.Errorf("no seeded poller #%s-body", id)
		}
	}
}

// nextFinish returns the first instant after t at which a build finishes.
func nextFinish(t time.Time) time.Time {
	var next time.Time
	for h := t.Add(-maxBuildTime).Unix() / 3600; h <= t.Add(6*time.Hour).Unix()/3600; h++ {
		for k := range pipelines {
			p := &pipelines[k]
			for _, b := range buildsStarted(p, h) {
				if b.End.After(t) && (next.IsZero() || b.End.Before(next)) {
					next = b.End
				}
			}
		}
	}
	return next
}

// TestPTagFollowsBuilds checks that the polls keep their tag until a build
// finishes, and get a new one when one does. It looks for two builds that
// finish one after the other within one hour, because the durations
// window also moves on the hour.
func TestPTagFollowsBuilds(t *testing.T) {
	s := newTestServer(t)
	h := s.routes()
	tags := func(at time.Time) []string {
		s.now = func() time.Time { return at }
		var out []string
		for _, u := range []string{"/charts/durations", "/charts/days", "/charts/trends"} {
			out = append(out, get(t, h, u, htmxHeader()).Header.Get(hx.HeaderPTag))
		}
		return out
	}
	first := nextFinish(epoch)
	for range 50 {
		second := nextFinish(first)
		if second.IsZero() {
			break
		}
		if first.Truncate(time.Hour) != second.Truncate(time.Hour) || second.Sub(first) < 2*time.Second {
			first = second
			continue
		}
		a, b, c := tags(first), tags(second.Add(-time.Second)), tags(second)
		for i := range a {
			if a[i] != b[i] {
				t.Errorf("poll %d: tag changed between %v and %v, with no build finishing", i, first, second)
			}
		}
		if a[0] == c[0] || a[1] == c[1] {
			t.Errorf("durations or days kept their tag after a build finished at %v", second)
		}
		return
	}
	t.Fatal("found no two builds finishing within one hour after epoch")
}

// TestDeterministic renders the same page twice, from two servers, and
// expects the same bytes.
func TestDeterministic(t *testing.T) {
	target := "/?range=14d&hp-chart=days&hp-x=" + someDayQ
	a := get(t, newTestServer(t).routes(), target, nil).Body
	b := get(t, newTestServer(t).routes(), target, nil).Body
	if a != b {
		t.Error("two renders of the same page differ")
	}
}

func TestWidth(t *testing.T) {
	h := newTestServer(t).routes()
	tests := []struct {
		target, want string
	}{
		{"/charts/durations?hp-w=480", `id="durations" class="hp hp-anim" viewBox="0 0 480 `},
		{"/charts/days?hp-w=700", `width="700"`},
		{"/charts/durations?hp-w=10", `width="50"`},    // clamped to hx.MinSize
		{"/charts/durations?hp-w=1e9", `width="4096"`}, // clamped to hx.MaxSize
		{"/charts/durations?hp-w=abc", `width="960"`},  // ignored: the default
		{"/charts/durations?hp-w=-5", `width="960"`},   // ignored
		{"/charts/trends?hp-w=1000", `trends-3`},       // three columns of 322
		{"/charts/trends?hp-w=1000", `width="322"`},    // (1000 - 2×16) / 3
		{"/charts/trends?hp-w=640", `trends-2`},        // two of 312
		{"/charts/trends?hp-w=400", `trends-1`},        // one
		{"/charts/trends?hp-w=400&pipeline=api", `width="400"`},
	}
	for _, tt := range tests {
		res := get(t, h, tt.target, htmxHeader())
		if res.Status != http.StatusOK || !strings.Contains(res.Body, tt.want) {
			t.Errorf("%s: status %d, body lacks %q", tt.target, res.Status, tt.want)
		}
	}
}

func TestParseFilters(t *testing.T) {
	tests := []struct {
		query, want string
		none        bool
	}{
		{"", "range=14d", false},
		{"range=7d", "range=7d", false},
		{"range=1y&branch=nope&theme=blue", "range=14d", false},
		{"pipeline=web&pipeline=api", "range=14d&pipeline=api&pipeline=web", false},
		{"pipeline=", "range=14d&pipeline=", true},
		{"range=7d&pipeline=&theme=dark", "range=7d&pipeline=&theme=dark", true},
		{"pipeline=&pipeline=mobile&branch=release&theme=dark", "range=14d&pipeline=mobile&branch=release&theme=dark", false},
		{"pipeline=api&pipeline=web&pipeline=mobile", "range=14d", false},
	}
	for _, tt := range tests {
		q, err := url.ParseQuery(tt.query)
		if err != nil {
			t.Fatal(err)
		}
		f := parseFilters(q)
		if got := f.query(); got != tt.want || f.NoneSelected != tt.none {
			t.Errorf("parseFilters(%q) = %q (none %v), want %q (none %v)", tt.query, got, f.NoneSelected, tt.want, tt.none)
		}
		// The canonical query is a fixed point: reading it back gives
		// the same filters, cleared boxes included.
		back, err := url.ParseQuery(f.query())
		if err != nil {
			t.Fatal(err)
		}
		if g := parseFilters(back); g.query() != f.query() || g.NoneSelected != f.NoneSelected {
			t.Errorf("parseFilters(%q) does not round-trip: %q (none %v)", f.query(), g.query(), g.NoneSelected)
		}
	}
}

// TestClearedPipelinesSurviveReload checks that the address the server
// writes after every box is cleared serves a page with every box still
// cleared, so a reload, a bookmark or Back keeps the form as it was.
func TestClearedPipelinesSurviveReload(t *testing.T) {
	h := newTestServer(t).routes()
	res := get(t, h, "/?range=14d&pipeline=&branch=&w-durations=800&w-days=800&w-trends=800", htmxHeader())
	u := res.Header.Get("HX-Replace-Url")
	if u != "/?range=14d&pipeline=" {
		t.Fatalf("HX-Replace-Url %q, want /?range=14d&pipeline=", u)
	}
	page := get(t, h, u, nil).Body
	for _, p := range pipelines {
		box := `<input type="checkbox" name="pipeline" value="` + p.Key + `"`
		if !strings.Contains(page, box+">") {
			t.Errorf("reloading %s ticks %s", u, p.Key)
		}
	}
	if !strings.Contains(page, `<input type="hidden" name="range" value="14d"><input type="hidden" name="pipeline" value="">`) {
		t.Errorf("the zoom form of %s drops the cleared pipelines", u)
	}
}

// TestSlotsSurviveFilters checks that a pipeline keeps its color slot
// when the others are filtered out.
func TestSlotsSurviveFilters(t *testing.T) {
	h := newTestServer(t).routes()
	for _, p := range pipelines {
		body := get(t, h, "/charts/durations?pipeline="+p.Key, htmxHeader()).Body
		want := `class="hp-series hp-s` + itoa(p.Slot) + `" data-hp-series="` + p.Key + `"`
		if !strings.Contains(body, want) {
			t.Errorf("%s alone: chart lacks %s", p.Key, want)
		}
		if strings.Count(body, `class="hp-series `) != 1 {
			t.Errorf("%s alone: want exactly one series", p.Key)
		}
	}
}

func TestBranchFilter(t *testing.T) {
	f := parseFilters(url.Values{"branch": {"feature"}})
	bs := finishedBuilds(f, epoch.Add(-7*24*time.Hour), epoch, epoch)
	if len(bs) == 0 {
		t.Fatal("no feature builds in a week")
	}
	for _, b := range bs {
		if b.Kind != "feature" || !strings.HasPrefix(b.Branch, "feature/") {
			t.Fatalf("build %s on %s passed the feature filter", b.ID(), b.Branch)
		}
	}
}

// TestBuildsArePure checks the simulation: the same hour gives the same
// builds, numbers are unique, and every build fits maxBuildTime.
func TestBuildsArePure(t *testing.T) {
	seen := map[string]bool{}
	for h := epoch.Unix()/3600 - 24*14; h <= epoch.Unix()/3600; h++ {
		for k := range pipelines {
			p := &pipelines[k]
			a, b := buildsStarted(p, h), buildsStarted(p, h)
			if len(a) != len(b) {
				t.Fatalf("hour %d: %d builds, then %d", h, len(a), len(b))
			}
			for i := range a {
				if a[i] != b[i] {
					t.Fatalf("hour %d: build %d differs between calls", h, i)
				}
				if seen[a[i].Name()] {
					t.Fatalf("duplicate build number %s", a[i].Name())
				}
				seen[a[i].Name()] = true
				if d := a[i].End.Sub(a[i].Start); d <= 0 || d > maxBuildTime {
					t.Fatalf("build %s takes %v", a[i].ID(), d)
				}
			}
		}
	}
	if len(seen) < 200 {
		t.Errorf("only %d builds in 14 days", len(seen))
	}
}

// TestWholeSecondTimes checks that build times fall on whole seconds. A
// float duration in minutes converted straight to nanoseconds leaves a few
// stray nanoseconds on End, and the tooltips then show every time of the
// chart to the nanosecond ("3:59:12.000000000 AM").
func TestWholeSecondTimes(t *testing.T) {
	for h := epoch.Unix()/3600 - 24*14; h <= epoch.Unix()/3600; h++ {
		for k := range pipelines {
			for _, b := range buildsStarted(&pipelines[k], h) {
				if b.Start.Nanosecond() != 0 || b.End.Nanosecond() != 0 {
					t.Fatalf("build %s: start %v, end %v: want whole seconds", b.ID(), b.Start, b.End)
				}
			}
		}
	}
	body := get(t, newTestServer(t).routes(), "/charts/durations?range=14d", htmxHeader()).Body
	if strings.Contains(body, ".000000000") || strings.Contains(body, ".000 ") {
		t.Error("the durations chart labels times with sub-second digits")
	}
}

// TestOutlierLabels checks that slow builds, and only those, are labeled.
func TestOutlierLabels(t *testing.T) {
	s := newTestServer(t)
	v := s.newView(httptest.NewRequest(http.MethodGet, "/?range=14d", nil))
	c := durationsChart(v.f, v.builds, v.lo, v.hi, 960, time.UTC)
	slow := 0
	for _, m := range c.Marks {
		d := m.(hyperplot.Dot)
		for i, l := range d.Labels {
			if (l != "") != (d.Y[i] > 2*pipelineByKey(d.Name).minutes) {
				t.Errorf("%s point %d (%v min): label %q", d.Name, i, d.Y[i], l)
			}
			if l != "" {
				slow++
			}
		}
	}
	if slow == 0 {
		t.Fatal("no slow build in 14 days to label")
	}
	body := get(t, s.routes(), "/charts/durations?range=14d", htmxHeader()).Body
	if !strings.Contains(body, `class="hp-label hp-dot-label"`) {
		t.Error("the chart draws no dot label")
	}
}

func pipelineByKey(key string) pipeline {
	for _, p := range pipelines {
		if p.Key == key {
			return p
		}
	}
	return pipeline{}
}

func at(t *testing.T, s string) time.Time {
	t.Helper()
	v, err := time.Parse(time.RFC3339, s)
	if err != nil {
		t.Fatal(err)
	}
	return v
}

func TestNewZoom(t *testing.T) {
	lo, hi := at(t, "2026-02-19T00:00:00Z"), at(t, "2026-03-04T16:00:00Z")
	tests := []struct {
		name, t0, t1, from, to string
		ok                     bool
	}{
		{"inside", "2026-03-01T10:00:00Z", "2026-03-02T10:00:00Z", "2026-03-01T10:00:00Z", "2026-03-02T10:00:00Z", true},
		{"reversed", "2026-03-02T10:00:00Z", "2026-03-01T10:00:00Z", "2026-03-01T10:00:00Z", "2026-03-02T10:00:00Z", true},
		{"whole minutes", "2026-03-01T10:00:30Z", "2026-03-02T10:00:30Z", "2026-03-01T10:00:00Z", "2026-03-02T10:01:00Z", true},
		{"clamped", "2026-01-01T00:00:00Z", "2026-12-01T00:00:00Z", "2026-02-19T00:00:00Z", "2026-03-04T16:00:00Z", true},
		{"widened", "2026-03-01T10:00:00Z", "2026-03-01T10:10:00Z", "2026-03-01T09:35:00Z", "2026-03-01T10:35:00Z", true},
		{"widened at the start", "2026-02-19T00:00:00Z", "2026-02-19T00:01:00Z", "2026-02-19T00:00:00Z", "2026-02-19T01:00:00Z", true},
		{"widened at the end", "2026-03-04T15:59:00Z", "2026-03-04T16:00:00Z", "2026-03-04T15:00:00Z", "2026-03-04T16:00:00Z", true},
		{"before the range", "2026-01-01T00:00:00Z", "2026-01-02T00:00:00Z", "", "", false},
		{"after the range", "2026-04-01T00:00:00Z", "2026-04-02T00:00:00Z", "", "", false},
	}
	for _, tt := range tests {
		z, ok := newZoom(at(t, tt.t0), at(t, tt.t1), lo, hi)
		if ok != tt.ok {
			t.Errorf("%s: ok = %v", tt.name, ok)
			continue
		}
		if !ok {
			continue
		}
		if !z.From.Equal(at(t, tt.from)) || !z.To.Equal(at(t, tt.to)) {
			t.Errorf("%s: zoom %v to %v, want %s to %s", tt.name, z.From, z.To, tt.from, tt.to)
		}
		// Idempotent: a zoom read back from its URL is the same zoom.
		if again, _ := newZoom(z.From, z.To, lo, hi); again != z {
			t.Errorf("%s: not idempotent: %v", tt.name, again)
		}
	}
}

// hxGet extracts the hx-get attribute of the element with the given id.
func hxGet(t *testing.T, body, id string) string {
	t.Helper()
	m := regexp.MustCompile(`id="` + id + `"[^>]*\shx-get="([^"]*)"`).FindStringSubmatch(body)
	if m == nil {
		t.Fatalf("no #%s with hx-get", id)
	}
	return html.UnescapeString(m[1])
}

func TestBrushZooms(t *testing.T) {
	s := newTestServer(t)
	s.loc = time.FixedZone("UTC-5", -5*3600)
	h := s.routes()
	tests := []struct{ name, x0, x1 string }{
		{"extension (RFC 3339)", "2026-03-02T05:00:00.000Z", "2026-03-03T05:00:00.000Z"},
		{"form (datetime-local, read in the chart's zone)", "2026-03-02T00:00", "2026-03-03T00:00"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			q := url.Values{"range": {"14d"}, "hp-chart": {"durations"}, "hp-x0": {tt.x0}, "hp-x1": {tt.x1}}
			res := get(t, h, "/charts/durations?"+q.Encode(), htmxHeader())
			if res.Status != http.StatusOK {
				t.Fatalf("status %d", res.Status)
			}
			wantURL := "/?range=14d&from=2026-03-02T05%3A00%3A00Z&to=2026-03-03T05%3A00%3A00Z"
			if got := res.Header.Get("HX-Push-Url"); got != wantURL {
				t.Errorf("HX-Push-Url %q, want %q", got, wantURL)
			}
			if !strings.Contains(res.Body, `data-hp-x="time 1772427600000 1772514000000"`) {
				t.Error("the chart's x domain is not the brushed range")
			}
			poll := hxGet(t, res.Body, "durations-body")
			if !strings.Contains(poll, "from=2026-03-02T05%3A00%3A00Z") {
				t.Errorf("the new poller's hx-get %q does not keep the zoom", poll)
			}
			if !strings.Contains(res.Body, `<hx-partial hx-target="#durations-zoom" hx-swap="innerMorph">`) ||
				!strings.Contains(res.Body, "Zoomed to Mar 2, 12:00 AM to Mar 3, 12:00 AM.") {
				t.Error("the zoom toolbar does not announce the zoom")
			}
			// The poll it sets up stays zoomed and can be a 304.
			header := htmxHeader()
			header.Set(hx.HeaderPTag, attrValue(res.Body, "hx-ptag"))
			if again := get(t, h, poll, header); again.Status != http.StatusNotModified {
				t.Errorf("the zoomed poll got %d, want 304", again.Status)
			}
		})
	}
	out := get(t, h, "/charts/durations?range=14d&zoom=out", htmxHeader())
	if got := out.Header.Get("HX-Push-Url"); got != "/?range=14d" {
		t.Errorf("zoom out pushes %q", got)
	}
}

// TestZoomedPollAgesOut checks that a zoomed poll draws exactly the window
// its URL names while the days still hold it, and answers 204 (keep the
// last frame) once midnight has moved the range past its start, instead of
// drawing another window under a toolbar that still names this one.
func TestZoomedPollAgesOut(t *testing.T) {
	s := newTestServer(t)
	clock := epoch
	s.now = func() time.Time { return clock }
	h := s.routes()
	zoomIn := func(x0, x1 string) (poll, domain string) {
		t.Helper()
		q := url.Values{"range": {"7d"}, "hp-chart": {"durations"}, "hp-x0": {x0}, "hp-x1": {x1}}
		res := get(t, h, "/charts/durations?"+q.Encode(), htmxHeader())
		if res.Status != http.StatusOK {
			t.Fatalf("zoom %s to %s: status %d", x0, x1, res.Status)
		}
		return hxGet(t, res.Body, "durations-body"), attrValue(res.Body, "data-hp-x")
	}
	// The oldest day of the 7-day range (Feb 26), and one in the middle.
	oldest, oldestX := zoomIn("2026-02-26T00:00:00Z", "2026-02-26T02:00:00Z")
	middle, middleX := zoomIn("2026-03-01T10:00:00Z", "2026-03-01T12:00:00Z")
	if oldestX != "time 1772064000000 1772071200000" {
		t.Fatalf("the oldest-day zoom draws %q", oldestX)
	}
	for _, step := range []struct {
		after        time.Duration
		oldestStatus int
	}{
		{0, http.StatusOK},
		{8 * time.Hour, http.StatusOK},         // Mar 4, 23:20: Feb 26 still in range
		{9 * time.Hour, http.StatusNoContent},  // Mar 5, 00:20: Feb 26 has left
		{48 * time.Hour, http.StatusNoContent}, // and stays gone
	} {
		clock = epoch.Add(step.after)
		res := get(t, h, oldest, htmxHeader())
		if res.Status != step.oldestStatus {
			t.Errorf("+%v: oldest-day poll status %d, want %d", step.after, res.Status, step.oldestStatus)
		}
		if res.Status == http.StatusOK && attrValue(res.Body, "data-hp-x") != oldestX {
			t.Errorf("+%v: oldest-day poll draws %q, want %q", step.after, attrValue(res.Body, "data-hp-x"), oldestX)
		}
		mid := get(t, h, middle, htmxHeader())
		if mid.Status != http.StatusOK || attrValue(mid.Body, "data-hp-x") != middleX {
			t.Errorf("+%v: the middle zoom moved: status %d, %q, want %q", step.after, mid.Status, attrValue(mid.Body, "data-hp-x"), middleX)
		}
	}
	// A poll whose from/to was edited off the canonical grid is not
	// redrawn either; a page with an aged-out zoom shows the whole range
	// and says so.
	clock = epoch
	if res := get(t, h, "/charts/durations?range=7d&from=2026-03-01T10%3A00%3A30Z&to=2026-03-01T12%3A00%3A00Z", htmxHeader()); res.Status != http.StatusNoContent {
		t.Errorf("an off-grid zoomed poll got %d, want 204", res.Status)
	}
	clock = epoch.Add(24 * time.Hour)
	page := get(t, h, "/?range=7d&from=2026-02-26T00%3A00%3A00Z&to=2026-02-26T02%3A00%3A00Z", nil)
	if page.Status != http.StatusOK || !strings.Contains(page.Body, "Showing last 7 days.") {
		t.Errorf("a page with an aged-out zoom: status %d, lacks the unzoomed status", page.Status)
	}
}

// attrValue returns the first value of attribute name in body, unescaped.
func attrValue(body, name string) string {
	m := regexp.MustCompile(`\s` + name + `="([^"]*)"`).FindStringSubmatch(body)
	if m == nil {
		return ""
	}
	return html.UnescapeString(m[1])
}

// TestBrushIgnored checks the brushes the spec says to ignore (§5.3):
// they get 204, which htmx does not swap.
func TestBrushIgnored(t *testing.T) {
	h := newTestServer(t).routes()
	for _, q := range []string{
		"hp-x0=yesterday&hp-x1=today",
		"hp-x0=2026-03-02T00%3A00%3A00Z&hp-x1=NaN",
		"hp-chart=days&hp-x0=2026-03-02T00%3A00%3A00Z&hp-x1=2026-03-03T00%3A00%3A00Z",
		"hp-x0=2025-01-01T00%3A00%3A00Z&hp-x1=2025-01-02T00%3A00%3A00Z", // outside the range
	} {
		if res := get(t, h, "/charts/durations?"+q, htmxHeader()); res.Status != http.StatusNoContent {
			t.Errorf("%s: status %d, want 204", q, res.Status)
		}
	}
}

func TestZoomFormWithoutScript(t *testing.T) {
	page := get(t, newTestServer(t).routes(), "/?hp-chart=durations&hp-x0=2026-03-02T00%3A00&hp-x1=2026-03-03T00%3A00", nil).Body
	if !strings.Contains(page, `data-hp-x="time 1772409600000 1772496000000"`) {
		t.Error("the page ignores the zoom form's parameters")
	}
	if !strings.Contains(page, ">Show all</a>") {
		t.Error("a zoomed page has no way back")
	}
}

// failedOn counts the failed builds of one day at epoch.
func failedOn(t *testing.T, label string) (failed, all int) {
	t.Helper()
	s := newTestServer(t)
	v := s.newView(httptest.NewRequest(http.MethodGet, "/", nil))
	for _, d := range v.ds {
		if d.Label != label {
			continue
		}
		for _, b := range v.builds {
			if !b.End.Before(d.Start) && b.End.Before(d.End) {
				all++
				if !b.Passed {
					failed++
				}
			}
		}
	}
	return failed, all
}

func TestSelectDay(t *testing.T) {
	h := newTestServer(t).routes()
	failed, all := failedOn(t, someDay)
	if failed == 0 || all == failed {
		t.Fatalf("%s has %d builds, %d failed: pick a day with both results", someDay, all, failed)
	}
	tests := []struct {
		name, query string
		rows        int    // -1: no table
		contains    string // a marker the body must contain
	}{
		{"click on a failed segment", "hp-chart=days&hp-x=" + someDayQ + "&hp-series=failed", failed, "Failed builds on Tuesday, Mar 3"},
		{"click on a passed segment", "hp-chart=days&hp-x=" + someDayQ + "&hp-series=passed", all - failed, "Showing " + itoa(all-failed) + " passed builds from Mar 3."},
		{"Enter on a day (no series)", "hp-chart=days&hp-x=" + someDayQ, all, "Builds on Tuesday, Mar 3"},
		{"a link (no hp-chart)", "hp-x=" + someDayQ, all, "Builds on Tuesday, Mar 3"},
		{"an unknown series", "hp-chart=days&hp-x=" + someDayQ + "&hp-series=%3Cb%3E", all, "Builds on"},
		{"a day no longer shown", "hp-chart=days&hp-x=Jan+1", -1, "That day is no longer on the chart."},
		{"another chart", "hp-chart=durations&hp-x=" + someDayQ, -1, `id="day-detail-status" class="sr-only" role="status"></p>`},
		{"no selection (Close)", "", -1, `id="day-detail-status" class="sr-only" role="status"></p>`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			res := get(t, h, "/detail/day?range=14d&"+tt.query, htmxHeader())
			if res.Status != http.StatusOK {
				t.Fatalf("status %d", res.Status)
			}
			if !strings.Contains(res.Body, tt.contains) {
				t.Errorf("body lacks %q:\n%s", tt.contains, res.Body)
			}
			rows := strings.Count(res.Body, `<tr id="build-`)
			if tt.rows < 0 {
				if strings.Contains(res.Body, "<table") {
					t.Error("unexpected table")
				}
			} else if rows != tt.rows {
				t.Errorf("%d rows, want %d", rows, tt.rows)
			}
			if strings.Contains(tt.query, "failed") && strings.Contains(res.Body, "result-pass") {
				t.Error("a passed build in the failed list")
			}
		})
	}
}

// TestDayLinks checks the select's plain-HTML equivalent: one link per
// day, with an id from its date, and that the page opens the day it names.
func TestDayLinks(t *testing.T) {
	h := newTestServer(t).routes()
	body := get(t, h, "/charts/days?range=7d", htmxHeader()).Body
	if n := strings.Count(body, `<a id="day-2026-`); n != 7 {
		t.Errorf("%d day links, want 7", n)
	}
	if !strings.Contains(body, `<a id="day-2026-03-03" href="/?range=7d&amp;hp-chart=days&amp;hp-x=Mar&#43;3#day-detail"`) {
		t.Error("the Mar 3 link is not the select as a plain link")
	}
	page := get(t, h, "/?range=7d&hp-chart=days&hp-x="+someDayQ, nil).Body
	if !strings.Contains(page, `<h3 id="day-detail-heading">Builds on Tuesday, Mar 3</h3>`) {
		t.Error("the page does not open the day its URL names")
	}
}

// TestEscaping checks that branch names, which are user input, are
// escaped: one simulated branch holds an ampersand.
func TestEscaping(t *testing.T) {
	h := newTestServer(t).routes()
	found := false
	for _, d := range days(parseFilters(nil), epoch, time.UTC) {
		body := get(t, h, "/detail/day?hp-x="+url.QueryEscape(d.Label), htmxHeader()).Body
		if strings.Contains(body, "r&d-spike") {
			t.Fatalf("%s: unescaped branch name", d.Label)
		}
		found = found || strings.Contains(body, "feature/r&amp;d-spike")
	}
	if !found {
		t.Error("no build on feature/r&d-spike in the range")
	}
	page := get(t, h, `/?theme="><script>`, nil).Body
	if strings.Contains(page, "<script>") || strings.Contains(page, "data-theme=") {
		t.Error("an unknown theme reached the page")
	}
}

// TestBadParamsNever5xx sends junk in every parameter to every route.
func TestBadParamsNever5xx(t *testing.T) {
	h := newTestServer(t).routes()
	junk := []string{
		"range=%00&pipeline=%FF&branch=<b>",
		"hp-w=NaN&hp-h=Inf",
		"hp-w=1e400",
		"hp-chart=days&hp-x=&hp-series=failed",
		"hp-chart=days&hp-x=%E2%80%AE&hp-series=x",
		"hp-x0=&hp-x1=",
		"hp-x0=0001-01-01T00%3A00%3A00Z&hp-x1=9999-12-31T23%3A59%3A59Z",
		"from=garbage&to=2026-03-02T00%3A00%3A00Z",
		"from=2026-03-02T00%3A00%3A00Z&to=2026-03-02T00%3A00%3A00Z",
		"w-durations=-1&w-days=1e308&w-trends=abc",
		"zoom=out&hp-x0=1&hp-x1=2",
	}
	for _, route := range []string{"/", "/charts/durations", "/charts/days", "/charts/trends", "/detail/day"} {
		for _, q := range junk {
			for _, header := range []http.Header{nil, htmxHeader()} {
				if res := get(t, h, route+"?"+q, header); res.Status >= 500 {
					t.Errorf("%s?%s: status %d", route, q, res.Status)
				}
			}
		}
	}
}

// TestFilterResponse checks the answer to the filter form: the contents of
// #charts, a canonical address and the Vary header.
func TestFilterResponse(t *testing.T) {
	res := get(t, newTestServer(t).routes(), "/?range=7d&pipeline=&pipeline=web&branch=main&w-durations=800&w-days=800&w-trends=800", htmxHeader())
	if strings.Contains(res.Body, "<html") || !strings.Contains(res.Body, `id="durations-body"`) {
		t.Error("the filter response is not the contents of #charts")
	}
	if got := res.Header.Get("HX-Replace-Url"); got != "/?range=7d&pipeline=web&branch=main" {
		t.Errorf("HX-Replace-Url %q", got)
	}
	if got := res.Header.Get("Vary"); got != "HX-Request, HX-Request-Type" {
		t.Errorf("Vary %q", got)
	}
	if !strings.Contains(res.Body, `id="durations" class="hp hp-anim" viewBox="0 0 800 `) {
		t.Error("the filter response ignores the chart's current width")
	}
}

func TestTheme(t *testing.T) {
	h := newTestServer(t).routes()
	if page := get(t, h, "/?theme=dark&range=7d", nil).Body; !strings.Contains(page, `<html lang="en" data-theme="dark">`) ||
		!strings.Contains(page, `<a href="/?range=7d&amp;theme=dark" aria-current="true">Dark</a>`) ||
		!strings.Contains(page, `<input type="hidden" name="theme" value="dark">`) {
		t.Error("the dark theme is not written to the page, its links and its form")
	}
	if page := get(t, h, "/", nil).Body; strings.Contains(page, "data-theme=") {
		t.Error("the default page forces a theme")
	}
}

// idAttr and labelledByAttr match id and aria-labelledby attributes.
var (
	idAttr         = regexp.MustCompile(`\sid="([^"]*)"`)
	labelledByAttr = regexp.MustCompile(`\saria-labelledby="([^"]*)"`)
)

// TestUniqueIDs checks that no id appears twice, so morphs match nodes and
// aria-labelledby references resolve to one element.
func TestUniqueIDs(t *testing.T) {
	h := newTestServer(t).routes()
	for _, target := range []string{"/?hp-x=" + someDayQ, "/?range=7d&pipeline=api&hp-x=" + someDayQ} {
		body := get(t, h, target, nil).Body
		count := map[string]int{}
		for _, m := range idAttr.FindAllStringSubmatch(body, -1) {
			count[m[1]]++
		}
		for id, n := range count {
			if n > 1 {
				t.Errorf("%s: id %q appears %d times", target, id, n)
			}
		}
		for _, m := range labelledByAttr.FindAllStringSubmatch(body, -1) {
			for _, id := range strings.Fields(m[1]) {
				if count[id] != 1 {
					t.Errorf("%s: aria-labelledby names %q, which appears %d times", target, id, count[id])
				}
			}
		}
	}
}

var (
	svgTag    = regexp.MustCompile(`<svg\b[^>]*>`)
	classAttr = regexp.MustCompile(`\sclass="([^"]*)"`)
)

// TestOnlyChartsAreSVGHP checks that every svg.hp on the page is a real
// chart. docs/spec.md §6.4 makes any svg.hp a chart, so hx-plot hovers and
// focuses it and the stylesheet gives it chart rules. Decorations such as
// the pipeline swatches must use another class.
func TestOnlyChartsAreSVGHP(t *testing.T) {
	h := newTestServer(t).routes()
	for _, target := range []string{"/", "/?hp-x=" + someDayQ, "/?range=7d&pipeline=api&hp-x=" + someDayQ} {
		body := get(t, h, target, nil).Body
		charts := 0
		for _, tag := range svgTag.FindAllString(body, -1) {
			m := classAttr.FindStringSubmatch(tag)
			if len(m) < 2 || !slices.Contains(strings.Fields(m[1]), "hp") {
				continue
			}
			charts++
			if !strings.Contains(tag, ` data-hp="1"`) || !idAttr.MatchString(tag) {
				t.Errorf("%s: svg.hp without an id and data-hp: %s", target, tag)
			}
		}
		if charts == 0 {
			t.Errorf("%s: no charts", target)
		}
	}
}

// TestScripts checks the page loads the pinned htmx release with its SRI
// hash, not deferred, plus hx-ptag, and no hx-sse (the page never streams).
func TestScripts(t *testing.T) {
	page := get(t, newTestServer(t).routes(), "/", nil).Body
	for _, want := range []string{
		`<script src="https://cdn.jsdelivr.net/npm/htmx.org@4.0.2/dist/htmx.min.js" integrity="sha384-`,
		`<script src="https://cdn.jsdelivr.net/npm/htmx.org@4.0.2/dist/ext/hx-ptag.js" integrity="sha384-`,
		`<script defer src="/static/hx-plot.js"></script>`,
		`<script defer src="/static/hx-plot-brush.js"></script>`,
	} {
		if !strings.Contains(page, want) {
			t.Errorf("page lacks %s", want)
		}
	}
	if strings.Contains(page, "hx-sse") {
		t.Error("the page loads hx-sse, which it does not use")
	}
}

func TestRunServesAndShutsDown(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan error, 1)
	go func() { done <- run(ctx, "127.0.0.1:0", 60, log.New(io.Discard, "", 0)) }()
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

func TestHTTPServerTimeouts(t *testing.T) {
	srv := newHTTPServer(t.Context(), http.NotFoundHandler(), log.New(io.Discard, "", 0))
	if srv.ReadHeaderTimeout <= 0 || srv.ReadTimeout <= 0 || srv.WriteTimeout <= 0 {
		t.Error("the server must bound slow clients with read and write timeouts")
	}
}

func TestFastClock(t *testing.T) {
	now := epoch
	clock := fastClock(func() time.Time { return now }, 60)
	now = now.Add(time.Second)
	if got := clock(); !got.Equal(epoch.Add(time.Minute)) {
		t.Errorf("one real second at speed 60 is %v, want a minute", got.Sub(epoch))
	}
}
