package main

import (
	"bytes"
	"embed"
	"html/template"
	"io/fs"
	"log"
	"math"
	"net/http"
	"path"
	"slices"
	"strconv"
	"time"

	"github.com/ironystock/hyperplot/client"
	"github.com/ironystock/hyperplot/hx"
	"github.com/ironystock/hyperplot/plot"
	"github.com/ironystock/hyperplot/scale"
)

//go:embed templates/*.html
var templateFS embed.FS

//go:embed static
var staticFS embed.FS

// contentSecurityPolicy allows scripts from this origin and from the one
// pinned htmx release on the CDN, and no inline script or style at all:
// charts carry classes and presentation attributes, never style="", and
// the page has no inline handlers. The CDN source names the release's
// directory, not the whole host, which serves anyone's packages.
const contentSecurityPolicy = "default-src 'self'; " +
	"script-src 'self' https://cdn.jsdelivr.net/npm/htmx.org@4.0.2/dist/; " +
	"style-src 'self'; img-src 'self' data:; connect-src 'self'; " +
	"base-uri 'none'; form-action 'self'; frame-ancestors 'none'"

// server renders the page and its fragments. Its fields are set at
// construction and only read afterwards, so handlers share it freely.
// There is no stream, so unlike the dashboard it needs no tick.
type server struct {
	now    func() time.Time // the clock; tests substitute a fixed one
	loc    *time.Location   // time zone of the days, axes and labels
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
	css := []byte(plot.Stylesheet())
	return &server{
		now:    time.Now,
		loc:    time.Local,
		tmpl:   tmpl,
		css:    css,
		cssTag: hx.Tag(css),
		logger: logger,
	}, nil
}

// routes returns the demo's handler. Every fragment handler renders the
// same bytes the page embeds for the same filters, time and width, so a
// poll right after page load can already be answered with 304.
func (s *server) routes() http.Handler {
	static, err := fs.Sub(staticFS, "static")
	if err != nil {
		panic(err) // unreachable: the directory is embedded at build time
	}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /{$}", s.page)
	mux.HandleFunc("GET /charts/durations", s.durations)
	mux.HandleFunc("GET /charts/days", s.days)
	mux.HandleFunc("GET /charts/trends", s.trends)
	mux.HandleFunc("GET /detail/day", s.dayDetail)
	mux.HandleFunc("GET /static/hx-plot.css", s.stylesheet)
	// The extension and its brush companion, embedded by package client.
	// Elsewhere they come from npm (htmx-ext-plot) or a CDN.
	mux.Handle("GET /static/hx-plot.js", client.Handler())
	mux.Handle("GET /static/hx-plot-brush.js", client.BrushHandler())
	mux.Handle("GET /static/", http.StripPrefix("/static/", staticTypes(http.FileServerFS(static))))
	return securityHeaders(mux)
}

// staticTypes sets the Content-Type of the static assets explicitly; on
// Windows, package mime may read another answer from the registry.
func staticTypes(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if path.Ext(r.URL.Path) == ".css" {
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

// ---- The state of one request ----------------------------------------------

// view is what every handler derives from a request: the filters, the
// days shown, the x window of the durations chart and the finished builds
// in it. It is a pure function of the query string, the clock and the
// zone.
type view struct {
	f      filters
	ds     []day
	lo, hi time.Time // the durations chart's whole x window
	builds []build
}

// newView computes the view for r's query string at the server's clock.
// The window ends at the next whole hour rather than at now, so it moves
// once an hour and every poll in between can be a 304.
func (s *server) newView(r *http.Request) view {
	now := s.now()
	f := parseFilters(r.URL.Query())
	ds := days(f, now, s.loc)
	lo, hi := ds[0].Start, now.Truncate(time.Hour).Add(time.Hour)
	return view{f: f, ds: ds, lo: lo, hi: hi, builds: finishedBuilds(f, lo, hi, now)}
}

// ---- View models ---------------------------------------------------------

type pageData struct {
	Theme     string // data-theme on <html>: "", "light" or "dark"
	Themes    []choice
	Ranges    []choice
	Pipelines []pipelineChoice
	Branches  []choice
	Charts    chartsData
}

// choice is one option of the filter form, or one theme link.
type choice struct {
	Value, Title, Href string
	Checked            bool
}

type pipelineChoice struct {
	Key     string
	Slot    int
	Checked bool
}

// chartsData is everything inside #charts: the part of the page that the
// filter form re-renders.
type chartsData struct {
	Summary   string
	BrushURL  string // where a brush of the durations chart goes
	Zoom      zoomBar
	Durations poller
	Days      poller
	DetailURL string // where a select of the days chart goes
	Detail    detailData
	Trends    poller
}

// poller is an element that polls its URL. Tag seeds hx-ptag with the tag
// of exactly what its first poll will get, so that poll can be a 304.
type poller struct {
	URL, Tag string
	Body     template.HTML
}

// chartBody is a chart, the hidden input that reports the width it was
// rendered at, extra markup and the chart's "View as table" disclosure.
type chartBody struct {
	Title     string
	SVG       template.HTML
	WidthName string // the hidden input's name, or "" for none
	Width     string
	Extra     template.HTML
	Table     template.HTML
}

// trendsBody is the small multiples grid.
type trendsBody struct {
	Cols   int
	Charts []template.HTML
	Width  string
}

// widths reads the chart widths that the filter form sends along. Each
// chart body holds a hidden input associated with the form (form="filters")
// that records the width it was rendered at, so a filter change renders
// each chart at its current size and nothing jumps. A first visit has
// none and gets the default; each poller's first request (load) then
// fetches its chart at the real width.
func widths(r *http.Request) (durations, days, trends float64) {
	q := r.URL.Query()
	get := func(name string) float64 {
		v, err := strconv.ParseFloat(q.Get("w-"+name), 64)
		if err != nil || math.IsNaN(v) || math.IsInf(v, 0) || v <= 0 {
			return defaultWidth
		}
		return math.Min(math.Max(v, hx.MinSize), hx.MaxSize)
	}
	return get("durations"), get("days"), get("trends")
}

// ---- Page ----------------------------------------------------------------

// page serves the whole page, or only the contents of #charts when the
// filter form asks for them (an htmx request that targets #charts).
//
// The page URL can also carry a zoom (from, to; or hp-x0 and hp-x1 from the
// zoom form submitted without script) and an open day (hp-chart=days and
// hp-x from a day link). The filter form sends neither, so a filter change
// shows the whole range again and closes the day.
func (s *server) page(w http.ResponseWriter, r *http.Request) {
	v := s.newView(r)
	z, ok := brushZoom(r, v.lo, v.hi, s.loc)
	if !ok {
		// A from/to that is no longer drawable (an old bookmark) shows
		// the whole range: the page's toolbar then says so, unlike a
		// poll's, which only the chart would update.
		z, _ = zoomFromQuery(r.URL.Query(), v.lo, v.hi)
	}
	wd, wb, wt := widths(r)
	charts, err := s.charts(r, v, z, wd, wb, wt)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	h := w.Header()
	// One URL answers with a page or a fragment depending on these
	// headers, so caches must keep the two apart (docs/htmx.md, pattern 3).
	h.Add("Vary", hx.HeaderRequest+", "+hx.HeaderRequestType)
	h.Set("Cache-Control", "no-cache")
	if hx.IsPartial(r) {
		// Keep the address bar shareable: the canonical filters, without
		// the widths the request carried.
		h.Set("HX-Replace-Url", pageURL(v.f, z))
		s.render(w, r, "charts", charts)
		return
	}
	f := v.f
	data := pageData{Theme: f.Theme, Charts: charts}
	for _, t := range []choice{{"light", "Light", "", false}, {"dark", "Dark", "", false}, {"", "System", "", false}} {
		t.Href, t.Checked = "/?"+f.withTheme(t.Value).query(), t.Value == f.Theme
		data.Themes = append(data.Themes, t)
	}
	for _, rg := range ranges {
		data.Ranges = append(data.Ranges, choice{Value: rg.Key, Title: rg.Title, Checked: rg.Key == f.Range.Key})
	}
	for _, p := range pipelines {
		data.Pipelines = append(data.Pipelines, pipelineChoice{Key: p.Key, Slot: p.Slot, Checked: !f.NoneSelected && f.has(p.Key)})
	}
	for _, b := range branchKinds {
		data.Branches = append(data.Branches, choice{Value: b.Key, Title: b.Title, Checked: b.Key == f.Branch})
	}
	s.render(w, r, "page", data)
}

// charts renders every chart for the first paint and for filter changes.
func (s *server) charts(r *http.Request, v view, z zoom, wd, wb, wt float64) (chartsData, error) {
	q := v.f.query()
	d := chartsData{
		Summary:   summary(v),
		BrushURL:  "/charts/durations?" + q,
		Zoom:      newZoomBar(v.f, z, v.lo, v.hi, s.loc),
		DetailURL: "/detail/day?" + q,
		Detail:    newDetail(r, v.f, v.ds, v.builds, s.loc),
	}
	var err error
	if d.Durations, err = s.durationsPoller(v, z, wd); err != nil {
		return d, err
	}
	body, resp, err := s.daysResponse(v, wb)
	if err != nil {
		return d, err
	}
	// The page embeds the chart (the summary is already on it), but the
	// seed is the tag of the whole poll response, summary included.
	d.Days = poller{URL: "/charts/days?" + q, Tag: hx.Tag(resp), Body: body}
	if d.Trends.Body, err = s.trendsBody(v, wt); err != nil {
		return d, err
	}
	d.Trends.URL, d.Trends.Tag = "/charts/trends?"+q, hx.Tag([]byte(d.Trends.Body))
	return d, nil
}

// summary is the line above the charts, such as "212 builds · 91.5%
// passed · median 8.1 min · 7 slow".
func summary(v view) string {
	if len(v.builds) == 0 {
		return "No finished builds match these filters."
	}
	var passed, slow int
	ms := make([]float64, len(v.builds))
	for i, b := range v.builds {
		ms[i] = b.Minutes
		if b.Passed {
			passed++
		}
		if b.Slow() {
			slow++
		}
	}
	slices.Sort(ms)
	rate := 100 * float64(passed) / float64(len(v.builds))
	return plural(len(v.builds), "build") + " · " + scale.FormatNumber(rate, 1) + "% passed · median " +
		scale.FormatNumber(percentile(ms, 50), 1) + " min · " + itoa(slow) + " slow"
}

// ---- Durations: polling, brush to zoom -----------------------------------

// durationsBody renders the durations chart over z, or over the whole
// window when z is off.
func (s *server) durationsBody(v view, z zoom, width float64) (template.HTML, error) {
	from, to := v.lo, v.hi
	if z.on() {
		from, to = z.From, z.To
	}
	c := durationsChart(v.f, v.builds, from, to, width, s.loc)
	return s.body(c, "w-durations", "")
}

// durationsPoller renders the polling element for v and z. Its hx-get
// carries the zoom, so later polls stay zoomed.
func (s *server) durationsPoller(v view, z zoom, width float64) (poller, error) {
	body, err := s.durationsBody(v, z, width)
	if err != nil {
		return poller{}, err
	}
	u := "/charts/durations?" + v.f.query()
	if z.on() {
		u += "&" + z.query()
	}
	return poller{URL: u, Tag: hx.Tag([]byte(body)), Body: body}, nil
}

// durations answers three kinds of request on one URL (spec/spec.md §5.4):
//
//   - a brush (hp-x0 and hp-x1, from a drag or the zoom form) zooms in;
//   - zoom=out (the "Show all" link) returns to the whole range;
//   - anything else is a poll, zoomed when the URL carries from and to.
//
// A poll is answered with the chart only (innerMorph on the poller). A zoom
// changes the poller's own hx-get, which an inner swap cannot do, so it is
// answered with the whole poller (outerMorph), the zoom toolbar as an
// <hx-partial>, and HX-Push-Url naming the page URL of the new view.
func (s *server) durations(w http.ResponseWriter, r *http.Request) {
	v := s.newView(r)
	if z, ok := brushZoom(r, v.lo, v.hi, s.loc); ok {
		s.zoomTo(w, r, v, z)
		return
	}
	if _, ok := hx.Brushed(r); ok {
		// A brush that does not parse, misses the range or names another
		// chart: ignore it (§5.3). htmx swaps nothing for a 204.
		w.WriteHeader(http.StatusNoContent)
		return
	}
	q := r.URL.Query()
	if q.Get("zoom") == "out" {
		s.zoomTo(w, r, v, zoom{})
		return
	}
	z, ok := zoomFromQuery(q, v.lo, v.hi)
	if !ok {
		// A zoomed poll whose window the days have moved past (or that
		// was edited by hand). Any other window would contradict the
		// toolbar and the address bar, which still name this zoom and
		// which a poll does not update, so keep the last frame: htmx
		// swaps nothing for a 204. A new brush or "Show all" moves on.
		w.WriteHeader(http.StatusNoContent)
		return
	}
	b, err := s.durationsBody(v, z, chartWidth(r))
	s.writeTagged(w, r, []byte(b), err)
}

// zoomTo answers a zoom in or out with the new polling element.
func (s *server) zoomTo(w http.ResponseWriter, r *http.Request, v view, z zoom) {
	p, err := s.durationsPoller(v, z, chartWidth(r))
	if err != nil {
		s.fail(w, r, err)
		return
	}
	var b, bar bytes.Buffer
	if err := s.tmpl.ExecuteTemplate(&b, "durations", p); err != nil {
		s.fail(w, r, err)
		return
	}
	if err := s.tmpl.ExecuteTemplate(&bar, "zoom-bar", newZoomBar(v.f, z, v.lo, v.hi, s.loc)); err != nil {
		s.fail(w, r, err)
		return
	}
	// One response, two targets: the poller (the request's own target)
	// and the toolbar, whose status line announces the zoom.
	b.Write(hx.Partial("#durations-zoom", "innerMorph", bar.Bytes()))
	h := w.Header()
	h.Set("Cache-Control", "no-cache")
	// The zoom is a URL: bookmarkable, and Back undoes it.
	h.Set("HX-Push-Url", pageURL(v.f, z))
	writeHTML(w, b.Bytes())
}

// ---- Builds per day: polling, select to drill down ------------------------

// daysBody is the days chart, its day links (the select gesture as plain
// links) and its table.
func (s *server) daysBody(v view, width float64) (template.HTML, error) {
	var links bytes.Buffer
	if err := s.tmpl.ExecuteTemplate(&links, "day-links", dayLinks(v.f, v.ds)); err != nil {
		return "", err
	}
	return s.body(daysChart(v.f, v.ds, v.builds, width), "w-days", template.HTML(links.String())) // html/template output
}

// daysResponse is what a poll of the days chart gets: the chart, plus the
// summary line as an <hx-partial>, because both change when a build
// finishes (docs/htmx.md, pattern 4). It returns the chart body alone too,
// for the page. The tag covers the whole response.
func (s *server) daysResponse(v view, width float64) (body template.HTML, resp []byte, err error) {
	if body, err = s.daysBody(v, width); err != nil {
		return "", nil, err
	}
	var sum bytes.Buffer
	if err := s.tmpl.ExecuteTemplate(&sum, "summary", summary(v)); err != nil {
		return "", nil, err
	}
	return body, append([]byte(body), hx.Partial("#summary", "innerMorph", sum.Bytes())...), nil
}

func (s *server) days(w http.ResponseWriter, r *http.Request) {
	_, b, err := s.daysResponse(s.newView(r), chartWidth(r))
	s.writeTagged(w, r, b, err)
}

// dayDetail answers a select of the days chart, a day link, a result tab
// and the list's Close link (no hp-x: no selection).
func (s *server) dayDetail(w http.ResponseWriter, r *http.Request) {
	v := s.newView(r)
	w.Header().Set("Cache-Control", "no-cache")
	s.render(w, r, "detail", newDetail(r, v.f, v.ds, v.builds, s.loc))
}

// ---- Trends: small multiples in one poll ---------------------------------

// trendsBody renders one small multiple per pipeline into a grid whose
// column count the server picks from the container's width (hp-w).
func (s *server) trendsBody(v view, width float64) (template.HTML, error) {
	charts, cols := trendCharts(v.f, v.ds, v.builds, width)
	d := trendsBody{Cols: cols, Width: strconv.FormatFloat(width, 'f', -1, 64)}
	for _, c := range charts {
		b, err := s.body(c, "", "")
		if err != nil {
			return "", err
		}
		d.Charts = append(d.Charts, b)
	}
	var b bytes.Buffer
	if err := s.tmpl.ExecuteTemplate(&b, "trends", d); err != nil {
		return "", err
	}
	return template.HTML(b.String()), nil // html/template output
}

func (s *server) trends(w http.ResponseWriter, r *http.Request) {
	b, err := s.trendsBody(s.newView(r), chartWidth(r))
	s.writeTagged(w, r, []byte(b), err)
}

// ---- Helpers ---------------------------------------------------------------

// writeTagged writes a fragment unless the client already shows it. The
// tag hashes the exact bytes (hx.Tag), and hx.CheckPTag answers a poll
// that echoes it (the HX-PTag request header) with a bodiless 304, which
// htmx does not swap (spec/spec.md §4.5).
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

func (s *server) stylesheet(w http.ResponseWriter, r *http.Request) {
	h := w.Header()
	h.Set("Content-Type", "text/css; charset=utf-8")
	h.Set("Cache-Control", "no-cache")
	h.Set("ETag", s.cssTag)
	http.ServeContent(w, r, "hx-plot.css", time.Time{}, bytes.NewReader(s.css))
}

// chartWidth is the width the extension reported for the request's
// hx-plot:autosize container (hp-w, spec/spec.md §4.6), or the default.
// The height stays the server's: the container is as tall as its chart.
func chartWidth(r *http.Request) float64 {
	w, _ := hx.Size(r, defaultWidth, 0)
	return w
}

// body renders a chart with its data table. widthName names the hidden
// input that reports the chart's width to the filter form, if any.
func (s *server) body(c *plot.Chart, widthName string, extra template.HTML) (template.HTML, error) {
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
		Title:     c.Title,
		SVG:       svg,
		WidthName: widthName,
		Width:     strconv.FormatFloat(c.Width, 'f', -1, 64),
		Extra:     extra,
		Table:     table,
	})
	if err != nil {
		return "", err
	}
	return template.HTML(b.String()), nil // html/template output from escaped parts
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

// fail logs err and answers without breaking the page. htmx 4 swaps every
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
