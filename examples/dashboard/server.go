package main

import (
	"bytes"
	"context"
	"embed"
	"errors"
	"html/template"
	"io/fs"
	"log"
	"math"
	"net/http"
	"path"
	"strconv"
	"time"

	"github.com/ironystock/hyperplot"
	"github.com/ironystock/hyperplot/client"
	"github.com/ironystock/hyperplot/hx"
)

//go:embed templates/*.html
var templateFS embed.FS

//go:embed static
var staticFS embed.FS

// contentSecurityPolicy allows scripts from this origin and from the one
// pinned htmx release on the CDN, and no inline script or style at all:
// hyperplot charts carry classes and presentation attributes, never
// style="" attributes, and the htmx features this page uses need no eval.
//
// The CDN source names the release's directory, not the whole host. A
// public CDN serves any package anyone publishes, so trusting its host
// would let an injected <script> load arbitrary code; a source whose path
// ends in "/" matches only URLs under that path. The integrity attributes
// on the page's own script tags protect those tags only and do not narrow
// the policy.
const contentSecurityPolicy = "default-src 'self'; " +
	"script-src 'self' https://cdn.jsdelivr.net/npm/htmx.org@4.0.2/dist/; " +
	"style-src 'self'; img-src 'self' data:; connect-src 'self'; " +
	"base-uri 'none'; form-action 'self'; frame-ancestors 'none'"

// filtersApplied is the event the filter response triggers (HX-Trigger)
// once htmx has swapped it in.
const filtersApplied = "filters-applied"

// server renders the dashboard. Its fields are set at construction and
// only read afterwards, so concurrent handlers share it freely.
type server struct {
	now    func() time.Time // the clock; tests substitute a fixed one
	loc    *time.Location   // time zone of the time axes and labels
	tick   time.Duration    // interval between SSE messages
	tmpl   *template.Template
	css    []byte // hyperplot's stylesheet, rendered once
	cssTag string // its ETag
	logger *log.Logger
}

func newServer(logger *log.Logger) (*server, error) {
	tmpl, err := template.ParseFS(templateFS, "templates/*.html")
	if err != nil {
		return nil, err
	}
	css := []byte(hyperplot.Stylesheet())
	return &server{
		now:    time.Now,
		loc:    time.Local,
		tick:   time.Second,
		tmpl:   tmpl,
		css:    css,
		cssTag: hx.Tag(css),
		logger: logger,
	}, nil
}

// routes returns the dashboard's handler. The fragment handlers render
// the same bytes as the first paint does, so a poll right after page
// load can already be answered with 304.
func (s *server) routes() http.Handler {
	static, err := fs.Sub(staticFS, "static")
	if err != nil {
		panic(err) // unreachable: the directory is embedded at build time
	}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /{$}", s.page)
	mux.HandleFunc("GET /charts/latency", s.latency)
	mux.HandleFunc("GET /charts/requests", s.requests)
	mux.HandleFunc("GET /charts/scatter", s.scatter)
	mux.HandleFunc("GET /detail/requests", s.requestsDetail)
	mux.HandleFunc("GET /cards/throughput", s.throughputCard)
	mux.HandleFunc("GET /stream/throughput", s.throughputStream)
	mux.HandleFunc("GET /static/hx-plot.css", s.stylesheet)
	mux.Handle("GET /static/hx-plot.js", client.Handler())
	mux.Handle("GET /static/hx-plot-brush.js", client.BrushHandler())
	mux.Handle("GET /static/", http.StripPrefix("/static/", staticTypes(http.FileServerFS(static))))
	return securityHeaders(mux)
}

// staticTypes sets the Content-Type of the static assets explicitly. The
// file server otherwise asks package mime, which on Windows reads the
// registry and may answer application/javascript for .js files.
func staticTypes(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch path.Ext(r.URL.Path) {
		case ".js":
			w.Header().Set("Content-Type", "text/javascript; charset=utf-8")
		case ".css":
			w.Header().Set("Content-Type", "text/css; charset=utf-8")
		}
		next.ServeHTTP(w, r)
	})
}

func securityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()
		h.Set("Content-Security-Policy", contentSecurityPolicy)
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("Referrer-Policy", "no-referrer")
		next.ServeHTTP(w, r)
	})
}

// ---- View models ----------------------------------------------------------

type pageData struct {
	Ranges  []rangeOption
	Regions []regionOption
	Charts  chartsData
}

type rangeOption struct {
	Key, Title string
	Checked    bool
}

type regionOption struct {
	Key     string
	Slot    int
	Checked bool
}

// chartsData is everything inside #charts: the part of the page that the
// filter row re-renders.
type chartsData struct {
	Scope string
	KPIs  []kpi

	BrushURL string // where a brush of the latency chart goes
	Latency  latencyPoller
	Zoom     zoomBar

	Stream streamData

	RequestsURL string
	RequestsTag string
	Requests    template.HTML
	Stacked     bool
	DetailURL   string // where a select of the requests chart goes
	Detail      detailData

	ScatterURL string
	ScatterTag string
	Scatter    template.HTML
	ScatterN   int
}

// latencyPoller is the element that polls the latency chart. A zoom
// replaces it whole (outerMorph), because its hx-get carries the zoom.
type latencyPoller struct {
	URL  string
	Tag  string // seeds hx-ptag, so the first poll can be a 304
	Body template.HTML
}

// streamData is the element that holds the throughput SSE connection.
type streamData struct {
	URL  string
	Body template.HTML
}

// chartBody is a chart, its "View as table" disclosure, and a hidden
// input that reports the width the chart was rendered at (see widths).
type chartBody struct {
	ID    string
	Title string
	Width string
	SVG   template.HTML
	Extra template.HTML // between the chart and its table
	Table template.HTML
}

// widths holds the width of each chart on the page.
type widths struct {
	Latency, Throughput, Requests, Scatter float64
}

// pageWidths reads the chart widths that the filter row sends along. Every
// chart body holds a hidden input, associated with the filter form, that
// records the width it was rendered at; a filter change therefore renders
// each chart at its current size, and nothing jumps. A first visit has no
// such inputs and gets the default width, and each chart container then
// re-requests its chart at its real width (hx-trigger "load").
func pageWidths(r *http.Request) widths {
	q := r.URL.Query()
	get := func(name string) float64 {
		v, err := strconv.ParseFloat(q.Get("w-"+name), 64)
		if err != nil || math.IsNaN(v) || math.IsInf(v, 0) || v <= 0 {
			return defaultWidth
		}
		return math.Min(math.Max(v, hx.MinSize), hx.MaxSize)
	}
	return widths{get("latency"), get("throughput"), get("requests"), get("scatter")}
}

// ---- Page ----------------------------------------------------------------

// page serves the whole dashboard, or only the contents of #charts when
// the filter row asks for them (an htmx request that targets #charts).
//
// The page URL can carry a latency zoom (from, to; or hp-x0 and hp-x1 from
// the zoom form submitted without script) and an open window detail
// (hp-chart=requests, hp-x, hp-series from a window link). The filter row
// sends neither, so a filter change returns to the live window.
func (s *server) page(w http.ResponseWriter, r *http.Request) {
	f, now := parseFilters(r), s.now()
	z, ok := brushZoom(r, now, s.loc)
	if !ok {
		z = zoomFromQuery(r.URL.Query(), now)
	}
	charts, err := s.charts(r, f, z, now, pageWidths(r))
	if err != nil {
		s.fail(w, r, err)
		return
	}
	h := w.Header()
	// The same URL answers with a page or a fragment depending on these
	// headers, so caches must keep the two apart.
	h.Add("Vary", hx.HeaderRequest+", "+hx.HeaderRequestType)
	h.Set("Cache-Control", "no-cache")
	if hx.IsPartial(r) {
		// Keep the address bar shareable: the filters, without the
		// widths the request carried.
		h.Set("HX-Replace-Url", pageURL(f, z))
		// htmx fires this event after the swap; the throughput card
		// listens for it and opens a stream for the new filters.
		h.Set("HX-Trigger", filtersApplied)
		s.render(w, r, "charts", charts)
		return
	}
	data := pageData{Charts: charts}
	for _, rg := range ranges {
		data.Ranges = append(data.Ranges, rangeOption{Key: rg.Key, Title: rg.Title, Checked: rg.Key == f.Range.Key})
	}
	for _, g := range regions {
		data.Regions = append(data.Regions, regionOption{Key: g.Key, Slot: g.Slot, Checked: !f.NoneSelected && f.has(g.Key)})
	}
	s.render(w, r, "page", data)
}

// charts renders every chart for the first paint and for filter changes.
// The tags seed each container's hx-ptag: they hash exactly the bytes its
// own endpoint returns for the same filters, time and width.
func (s *server) charts(r *http.Request, f filters, z zoom, now time.Time, ws widths) (chartsData, error) {
	q := f.query()
	d := chartsData{
		Scope:       f.scope(),
		BrushURL:    "/charts/latency?" + q,
		Zoom:        newZoomBar(f, z, now, s.loc),
		RequestsURL: "/charts/requests?" + q,
		DetailURL:   "/detail/requests?" + q,
		ScatterURL:  "/charts/scatter?" + q,
		ScatterN:    scatterN,
		Stacked:     f.Stacked,
	}
	var err error
	if d.KPIs, err = kpis(f, latencyEnd(f, now)); err != nil {
		return d, err
	}
	if d.Latency, err = s.latencyPoller(f, z, now, ws.Latency); err != nil {
		return d, err
	}
	if d.Requests, err = s.requestsBody(f, now, ws.Requests); err != nil {
		return d, err
	}
	d.RequestsTag = hx.Tag([]byte(d.Requests))
	if d.Detail, err = s.detail(r, f, now, ws.Requests); err != nil {
		return d, err
	}
	if d.Scatter, err = s.body(scatterChart(f, now, ws.Scatter)); err != nil {
		return d, err
	}
	d.ScatterTag = hx.Tag([]byte(d.Scatter))
	if d.Stream, err = s.stream(f, now, ws.Throughput); err != nil {
		return d, err
	}
	// The page shows the chart but opens no stream: its container fetches
	// the stream card on load, at the container's real width (the page
	// cannot know it, and htmx:plot:resize fires only when the width
	// changes). Connecting here would open a stream at the default width
	// that the card then replaces, and the chart would sit scaled down
	// until then. Filter responses don't touch the container's children
	// (hx-morph-skip-children); filters-applied fetches a new card.
	d.Stream.URL = ""
	return d, nil
}

// ---- Polled charts: hx-ptag and innerMorph -------------------------------

// latencyEnd is the end of the last closed latency bucket. Every poll
// inside one bucket renders the same bytes and gets 304 Not Modified.
func latencyEnd(f filters, now time.Time) time.Time {
	return now.Truncate(f.Range.Step)
}

// latencyFor is the latency chart: the live window, or the zoom.
func (s *server) latencyFor(f filters, z zoom, now time.Time, width float64) *hyperplot.Chart {
	if z.on() {
		return zoomedLatencyChart(f, z, now, width, s.loc)
	}
	return latencyChart(f, latencyEnd(f, now), width, s.loc)
}

// latencyResponse is the latency chart plus the KPI tiles, which ride
// along as an <hx-partial> because they change with the same buckets. The
// tiles always cover the range preset, also while the chart is zoomed.
func (s *server) latencyResponse(f filters, z zoom, now time.Time, width float64) ([]byte, error) {
	body, err := s.body(s.latencyFor(f, z, now, width))
	if err != nil {
		return nil, err
	}
	tiles, err := kpis(f, latencyEnd(f, now))
	if err != nil {
		return nil, err
	}
	var kb bytes.Buffer
	if err := s.tmpl.ExecuteTemplate(&kb, "kpis", tiles); err != nil {
		return nil, err
	}
	return append([]byte(body), hx.Partial("#kpis", "innerMorph", kb.Bytes())...), nil
}

// latencyPoller renders the polling element for f and z. Its hx-get
// carries the zoom, so later polls stay zoomed, and its seeded tag is the
// tag of exactly what its first poll will get.
func (s *server) latencyPoller(f filters, z zoom, now time.Time, width float64) (latencyPoller, error) {
	resp, err := s.latencyResponse(f, z, now, width)
	if err != nil {
		return latencyPoller{}, err
	}
	body, err := s.body(s.latencyFor(f, z, now, width))
	if err != nil {
		return latencyPoller{}, err
	}
	u := "/charts/latency?" + f.query()
	if z.on() {
		u += "&" + z.query()
	}
	return latencyPoller{URL: u, Tag: hx.Tag(resp), Body: body}, nil
}

// latency answers three kinds of request on one URL (docs/spec.md §5.4):
//
//   - a brush (hp-x0 and hp-x1, from a drag or the zoom form) zooms in;
//   - zoom=out (the "Back to live" link) returns to the live window;
//   - anything else is a poll, zoomed when the URL carries from and to.
//
// A poll is answered with the chart only (innerMorph). A zoom changes the
// polling element's own hx-get, which an inner swap cannot do, so it is
// answered with the whole polling element (outerMorph), the zoom toolbar
// as an <hx-partial>, and HX-Push-Url naming the page URL of the new view.
func (s *server) latency(w http.ResponseWriter, r *http.Request) {
	f, now := parseFilters(r), s.now()
	if z, ok := brushZoom(r, now, s.loc); ok {
		s.zoomTo(w, r, f, z, now)
		return
	}
	if _, ok := hx.Brushed(r); ok {
		// A brush whose values do not parse, or of another chart: ignore
		// it (§5.3). htmx swaps nothing for a 204.
		w.WriteHeader(http.StatusNoContent)
		return
	}
	q := r.URL.Query()
	if q.Get("zoom") == "out" {
		s.zoomTo(w, r, f, zoom{}, now)
		return
	}
	b, err := s.latencyResponse(f, zoomFromQuery(q, now), now, chartWidth(r))
	s.writeTagged(w, r, b, err)
}

// zoomTo answers a zoom in or out with the new polling element.
func (s *server) zoomTo(w http.ResponseWriter, r *http.Request, f filters, z zoom, now time.Time) {
	p, err := s.latencyPoller(f, z, now, chartWidth(r))
	if err != nil {
		s.fail(w, r, err)
		return
	}
	var b, bar bytes.Buffer
	if err := s.tmpl.ExecuteTemplate(&b, "latency", p); err != nil {
		s.fail(w, r, err)
		return
	}
	if err := s.tmpl.ExecuteTemplate(&bar, "zoom-bar", newZoomBar(f, z, now, s.loc)); err != nil {
		s.fail(w, r, err)
		return
	}
	b.Write(hx.Partial("#latency-zoom", "innerMorph", bar.Bytes()))
	h := w.Header()
	h.Set("Cache-Control", "no-cache")
	h.Set("HX-Push-Url", pageURL(f, z))
	writeHTML(w, b.Bytes())
}

// ---- Drill-down ------------------------------------------------------------

// requestsBody is the requests chart, its window links (the select
// gesture as plain links) and its table.
func (s *server) requestsBody(f filters, now time.Time, width float64) (template.HTML, error) {
	var links bytes.Buffer
	if err := s.tmpl.ExecuteTemplate(&links, "windows", windowLinks(f, requestWindows(f, now, s.loc))); err != nil {
		return "", err
	}
	return s.bodyWith(requestsChart(f, now, width, s.loc), template.HTML(links.String())) // html/template output
}

func (s *server) requests(w http.ResponseWriter, r *http.Request) {
	b, err := s.requestsBody(parseFilters(r), s.now(), chartWidth(r))
	s.writeTagged(w, r, []byte(b), err)
}

// detail renders the window detail that r selects, or the closed detail.
func (s *server) detail(r *http.Request, f filters, now time.Time, width float64) (detailData, error) {
	d := detailData{
		BackURL: pageURL(f, zoom{}),
		BackGet: "/detail/requests?" + f.query(),
	}
	win, rs, notice, ok := selectedWindow(r, f, requestWindows(f, now, s.loc))
	if !ok {
		d.Notice, d.Status = notice, notice
		return d, nil
	}
	c := detailChart(f, rs, win.Start, width, s.loc)
	svg, err := c.HTML()
	if err != nil {
		return d, err
	}
	table, err := c.Table()
	if err != nil {
		return d, err
	}
	span := rangeText(win.Start, win.Start.Add(f.Range.Window), s.loc)
	d.Open = true
	d.Heading = "Window from " + win.Label
	d.Sub = regionNames(rs) + " · " + span + ", in " + bucketText(f.Range.Window/detailBuckets) + " buckets"
	d.Status = "Showing requests for " + regionNames(rs) + " from " + span + "."
	d.Stats = detailStats(f, rs, win.Start)
	d.Title = c.Title
	d.SVG, d.Table = svg, table
	return d, nil
}

// requestsDetail answers a select of the requests chart, a window link and
// the detail's Close link (no hp-x: no selection).
func (s *server) requestsDetail(w http.ResponseWriter, r *http.Request) {
	d, err := s.detail(r, parseFilters(r), s.now(), chartWidth(r))
	if err != nil {
		s.fail(w, r, err)
		return
	}
	w.Header().Set("Cache-Control", "no-cache")
	s.render(w, r, "detail", d)
}

func (s *server) scatter(w http.ResponseWriter, r *http.Request) {
	b, err := s.body(scatterChart(parseFilters(r), s.now(), chartWidth(r)))
	s.writeTagged(w, r, []byte(b), err)
}

// writeTagged writes a fragment unless the client already shows it. The
// polling tag hashes the exact bytes, so an unchanged chart costs one
// bodiless 304, and htmx skips the swap.
func (s *server) writeTagged(w http.ResponseWriter, r *http.Request, b []byte, err error) {
	if err != nil {
		s.fail(w, r, err)
		return
	}
	w.Header().Set("Cache-Control", "no-cache")
	if hx.CheckPTag(w, r, hx.Tag(b)) {
		return
	}
	writeHTML(w, b)
}

// ---- Streamed chart: hx-sse and innerMorph -------------------------------

// stream renders the element that holds the SSE connection. Its connect
// URL names the filters, so a filter change or a resize needs a new
// stream: /cards/throughput then renders a new element, which replaces
// the old one with a plain innerHTML swap. Removing the old element ends
// its stream.
//
// Two hx-sse 4.0.2 behaviors rule out morphing here. Morphing a new
// hx-sse:connect URL into the same element leaves the old read loop
// reconnecting to the old URL for as long as the element stays in the
// document. And an element that htmx processes twice before its stream
// answers (a morph inserts it, then the swap processes the new content)
// opens two streams.
func (s *server) stream(f filters, now time.Time, width float64) (streamData, error) {
	body, err := s.body(throughputChart(f, now, width, s.loc))
	if err != nil {
		return streamData{}, err
	}
	u := "/stream/throughput?" + f.query() + "&hp-w=" + strconv.FormatFloat(width, 'f', -1, 64)
	return streamData{URL: u, Body: body}, nil
}

func (s *server) throughputCard(w http.ResponseWriter, r *http.Request) {
	d, err := s.stream(parseFilters(r), s.now(), chartWidth(r))
	if err != nil {
		s.fail(w, r, err)
		return
	}
	w.Header().Set("Cache-Control", "no-cache")
	s.render(w, r, "stream", d)
}

// throughputStream sends a freshly rendered throughput chart once per
// tick until the client goes away. Each message's id is the second it
// shows; after a reconnect, the second the client already has is skipped.
func (s *server) throughputStream(w http.ResponseWriter, r *http.Request) {
	f, width := parseFilters(r), chartWidth(r)
	stream, err := hx.NewStream(w, r)
	if err != nil {
		// Not an event stream, so htmx would swap an error body over the
		// chart. HX-Reswap: none prevents that, and a non-2xx status makes
		// a reconnecting hx-sse back off; after a 204 it would retry at
		// its base delay forever.
		s.logger.Printf("%s %s: %v", r.Method, r.URL.Path, err)
		w.Header().Set("HX-Reswap", "none")
		w.WriteHeader(http.StatusServiceUnavailable)
		return
	}
	last := stream.LastEventID()
	// send renders and sends the current second. A render error skips the
	// frame and keeps the stream open: returning would make the client
	// reconnect at hx-sse's base delay for as long as the error lasts.
	send := func() error {
		now := s.now().Truncate(time.Second)
		id := strconv.FormatInt(now.Unix(), 10)
		if id == last {
			return nil
		}
		body, err := s.body(throughputChart(f, now, width, s.loc))
		if err != nil {
			s.logger.Printf("throughput chart: %v", err)
			return nil
		}
		last = id
		return stream.SendID(id, []byte(body))
	}
	if err := send(); err != nil {
		s.streamEnded(err)
		return
	}
	t := time.NewTicker(s.tick)
	defer t.Stop()
	for {
		select {
		case <-r.Context().Done():
			return
		case <-t.C:
			if err := send(); err != nil {
				s.streamEnded(err)
				return
			}
		}
	}
}

// streamEnded logs why a stream failed, unless the client simply left.
func (s *server) streamEnded(err error) {
	if !errors.Is(err, context.Canceled) {
		s.logger.Printf("throughput stream: %v", err)
	}
}

// ---- Static files --------------------------------------------------------

func (s *server) stylesheet(w http.ResponseWriter, r *http.Request) {
	h := w.Header()
	h.Set("Content-Type", "text/css; charset=utf-8")
	h.Set("Cache-Control", "no-cache")
	h.Set("ETag", s.cssTag)
	http.ServeContent(w, r, "hx-plot.css", time.Time{}, bytes.NewReader(s.css))
}

// ---- Helpers ---------------------------------------------------------------

// chartWidth is the width that the client extension reported for the
// request's autosize container (hp-w), or the default.
func chartWidth(r *http.Request) float64 {
	w, _ := hx.Size(r, defaultWidth, 0)
	return w
}

// body renders a chart with its data table.
func (s *server) body(c *hyperplot.Chart) (template.HTML, error) { return s.bodyWith(c, "") }

// bodyWith renders a chart, then extra, then the chart's data table.
func (s *server) bodyWith(c *hyperplot.Chart, extra template.HTML) (template.HTML, error) {
	svg, err := c.HTML()
	if err != nil {
		return "", err
	}
	table, err := c.Table()
	if err != nil {
		return "", err
	}
	var b bytes.Buffer
	err = s.tmpl.ExecuteTemplate(&b, "chart-body", chartBody{
		ID:    c.ID,
		Title: c.Title,
		Width: strconv.FormatFloat(c.Width, 'f', -1, 64),
		SVG:   svg,
		Extra: extra,
		Table: table,
	})
	if err != nil {
		return "", err
	}
	return template.HTML(b.String()), nil // produced by html/template from escaped parts
}

// render executes a template into a buffer first, so that a failure can
// still become a clean error response.
func (s *server) render(w http.ResponseWriter, r *http.Request, name string, data any) {
	var b bytes.Buffer
	if err := s.tmpl.ExecuteTemplate(&b, name, data); err != nil {
		s.fail(w, r, err)
		return
	}
	writeHTML(w, b.Bytes())
}

func writeHTML(w http.ResponseWriter, b []byte) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Content-Length", strconv.Itoa(len(b)))
	_, _ = w.Write(b) // the client may be gone; there is nothing left to do
}

// fail logs err and answers without breaking the page. htmx swaps every
// response except 204 and 304, so an htmx request gets 204 No Content and
// the chart keeps its last good render; anything else gets a plain 500.
func (s *server) fail(w http.ResponseWriter, r *http.Request, err error) {
	s.logger.Printf("%s %s: %v", r.Method, r.URL.Path, err)
	if hx.IsRequest(r) {
		w.WriteHeader(http.StatusNoContent)
		return
	}
	http.Error(w, "internal error", http.StatusInternalServerError)
}
