package main

import (
	"bytes"
	"context"
	"embed"
	"errors"
	"html/template"
	"io/fs"
	"log"
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
// style="" attributes, and nothing on this page needs eval. The CDN source
// names the release's directory, not the whole host, so an injected
// <script> cannot load some other package from it.
const contentSecurityPolicy = "default-src 'self'; " +
	"script-src 'self' https://cdn.jsdelivr.net/npm/htmx.org@4.0.2/dist/; " +
	"style-src 'self'; img-src 'self' data:; connect-src 'self'; " +
	"base-uri 'none'; form-action 'self'; frame-ancestors 'none'"

// server renders the sensor monitor. Its fields are set at construction
// and only read afterwards, so concurrent handlers share it freely.
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

// routes returns the monitor's handler.
func (s *server) routes() http.Handler {
	static, err := fs.Sub(staticFS, "static")
	if err != nil {
		panic(err) // unreachable: the directory is embedded at build time
	}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /{$}", s.page)
	mux.HandleFunc("GET /stream/tiles", s.tileStream)    // SSE: tiles + alerts
	mux.HandleFunc("GET /charts/overlimit", s.overLimit) // polled with hx-ptag
	mux.HandleFunc("GET /detail", s.detail)              // select → the detail panel
	mux.HandleFunc("GET /detail/chart", s.detailChart)   // polled; brush → zoom
	// The extension's files come from the client package, which embeds
	// them, so they always match this module's renderer.
	mux.HandleFunc("GET /static/hx-plot.css", s.stylesheet)
	mux.Handle("GET /static/hx-plot.js", client.Handler())
	mux.Handle("GET /static/hx-plot-brush.js", client.BrushHandler())
	mux.Handle("GET /static/", http.StripPrefix("/static/", staticTypes(http.FileServerFS(static))))
	return securityHeaders(mux)
}

// staticTypes sets the Content-Type of the static assets explicitly. The
// file server otherwise asks package mime, which on Windows reads the
// registry.
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

// ---- View models ------------------------------------------------------------

type pageData struct {
	Theme  string // "light", "dark", or "" to follow the system
	Themes []themeLink
	Tiles  []tile
	Alerts alertsView
	Detail detailView

	OverLimit    template.HTML
	OverLimitTag string // seeds hx-ptag, so the first poll can be a 304
}

// detailView is the content of #detail, which a select replaces.
type detailView struct {
	Status   string // the polite live region's text
	Name     string
	Code     string
	Sub      string
	BrushURL string // where a brush of the detail chart goes
	Zoom     zoomBar
	Poller   poller
}

// poller is the element that polls the detail chart. A zoom replaces it
// whole (outerMorph), because its hx-get carries the zoom.
type poller struct {
	URL  string
	Tag  string // seeds hx-ptag
	Body template.HTML
}

// chartBody is a chart and its "View as table" disclosure.
type chartBody struct {
	Title string
	SVG   template.HTML
	Table template.HTML
}

// ---- Page ---------------------------------------------------------------------

// page serves the whole monitor. Its URL names the detail's sensor and any
// zoom (sensor, from, to), or carries the zoom form's hp-x0 and hp-x1 when
// that form is submitted without script.
func (s *server) page(w http.ResponseWriter, r *http.Request) {
	now := s.now()
	sn := pageSensor(r)
	z, ok := brushZoom(r, now, s.loc)
	if !ok {
		// A from/to that is no longer drawable (an old bookmark) shows
		// the live window, and the page's toolbar says so.
		z, _ = zoomFromQuery(r.URL.Query(), now)
	}
	d := pageData{Theme: s.theme(w, r)}
	d.Themes = themeLinks(pageURL(sn, z), d.Theme)
	var err error
	if d.Tiles, err = tiles(now); err != nil {
		s.fail(w, r, err)
		return
	}
	d.Alerts = newAlertsView(now, s.loc)
	if d.Detail, err = s.detailView(sn, z, now, defaultWidth); err != nil {
		s.fail(w, r, err)
		return
	}
	if d.OverLimit, err = s.body(overLimitChart(now, defaultWidth)); err != nil {
		s.fail(w, r, err)
		return
	}
	// The tag hashes exactly the bytes /charts/overlimit returns for the
	// same minute and width, so a poll right after load can be a 304.
	d.OverLimitTag = hx.Tag([]byte(d.OverLimit))
	w.Header().Set("Cache-Control", "no-cache")
	s.render(w, r, "page", d)
}

// themeLink is one choice of the theme switch: a plain link, no script.
type themeLink struct {
	Label, Href string
	Current     bool
}

func themeLinks(page, current string) []themeLink {
	choices := []struct{ key, label string }{{"light", "Light"}, {"dark", "Dark"}, {"system", "System"}}
	out := make([]themeLink, len(choices))
	for i, c := range choices {
		out[i] = themeLink{
			Label:   c.label,
			Href:    page + "&theme=" + c.key,
			Current: c.key == current || (c.key == "system" && current == ""),
		}
	}
	return out
}

// theme reads the color theme: ?theme=light|dark|system from a theme link,
// which a cookie then remembers, or that cookie. The page writes it into
// <html data-theme>, which hx-plot.css and app.css both honor; without one
// the page follows prefers-color-scheme.
func (s *server) theme(w http.ResponseWriter, r *http.Request) string {
	set := func(v string, age int) {
		http.SetCookie(w, &http.Cookie{Name: "theme", Value: v, Path: "/", MaxAge: age, HttpOnly: true, SameSite: http.SameSiteLaxMode})
	}
	switch v := r.URL.Query().Get("theme"); v {
	case "light", "dark":
		set(v, 365*24*3600)
		return v
	case "system":
		set("", -1)
		return ""
	}
	if c, err := r.Cookie("theme"); err == nil && (c.Value == "light" || c.Value == "dark") {
		return c.Value
	}
	return ""
}

// ---- Tiles and alerts: one SSE stream ----------------------------------------

// liveFragment is one SSE message: the tile grid, swapped into the
// connecting element, and the alerts as an <hx-partial>, which htmx swaps
// into #alerts (docs/spec.md §4.5, "Several targets"). One stream carries
// both, so the page holds one connection, not two.
func (s *server) liveFragment(now time.Time) ([]byte, error) {
	ts, err := tiles(now)
	if err != nil {
		return nil, err
	}
	var b, a bytes.Buffer
	if err := s.tmpl.ExecuteTemplate(&b, "tiles", ts); err != nil {
		return nil, err
	}
	if err := s.tmpl.ExecuteTemplate(&a, "alerts", newAlertsView(now, s.loc)); err != nil {
		return nil, err
	}
	return append(b.Bytes(), hx.Partial("#alerts", "innerMorph", a.Bytes())...), nil
}

// tileStream sends the tiles and alerts once per tick until the client
// goes away. Each message's id is the second it shows; after a reconnect,
// the second the client already has is skipped (Last-Event-ID).
func (s *server) tileStream(w http.ResponseWriter, r *http.Request) {
	stream, err := hx.NewStream(w, r)
	if err != nil {
		// Not an event stream, so htmx would swap an error body over the
		// tiles. HX-Reswap: none prevents that, and a 503 (not a 204)
		// makes a reconnecting hx-sse back off (docs/spec.md §4.5).
		s.logger.Printf("%s %s: %v", r.Method, r.URL.Path, err)
		w.Header().Set("HX-Reswap", "none")
		w.WriteHeader(http.StatusServiceUnavailable)
		return
	}
	// retry: tells hx-sse how long to wait before reconnecting after the
	// stream drops (docs/spec.md §4.5 SHOULD).
	if err := stream.Retry(2 * time.Second); err != nil {
		s.streamEnded(err)
		return
	}
	last := stream.LastEventID()
	send := func() error {
		now := s.now().Truncate(time.Second)
		id := strconv.FormatInt(now.Unix(), 10)
		if id == last {
			return nil
		}
		b, err := s.liveFragment(now)
		if err != nil {
			// Skip the frame and keep the stream open: ending it would make
			// every page reconnect for as long as the error lasts.
			s.logger.Printf("tiles: %v", err)
			return nil
		}
		last = id
		return stream.SendID(id, b)
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
		s.logger.Printf("tile stream: %v", err)
	}
}

// ---- Over-limit chart: polled with hx-ptag -----------------------------------

func (s *server) overLimit(w http.ResponseWriter, r *http.Request) {
	b, err := s.body(overLimitChart(s.now(), chartWidth(r)))
	s.writeTagged(w, r, []byte(b), err)
}

// writeTagged writes a fragment unless the client already shows it. The tag
// hashes the exact bytes, so an unchanged chart costs one bodiless 304
// and htmx skips the swap (docs/spec.md §4.5, Polling).
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

// ---- Detail: select, poll and brush ------------------------------------------

// detailView renders the detail panel for sensor sn over z.
func (s *server) detailView(sn sensor, z zoom, now time.Time, width float64) (detailView, error) {
	p, err := s.poller(sn, z, now, width)
	if err != nil {
		return detailView{}, err
	}
	return detailView{
		Name:     sn.Name(),
		Code:     sn.Code,
		Sub:      "Warning at " + sn.format(sn.Warn) + ", alarm at " + sn.format(sn.Alarm) + ". Drag across the chart to zoom in.",
		BrushURL: chartURL(sn, zoom{}),
		Zoom:     newZoomBar(sn, z, now, s.loc),
		Poller:   p,
	}, nil
}

// poller renders the polling element for sn and z. Its hx-get carries the
// zoom, so later polls stay zoomed, and its seeded tag is the tag of
// exactly what its first poll will get.
func (s *server) poller(sn sensor, z zoom, now time.Time, width float64) (poller, error) {
	body, err := s.body(detailChart(sn, z, now, width, s.loc))
	if err != nil {
		return poller{}, err
	}
	return poller{URL: chartURL(sn, z), Tag: hx.Tag([]byte(body)), Body: body}, nil
}

// detail answers a select of the over-limit chart (hp-chart, hp-x), a tile
// link and an alert link (sensor, and from and to for an alert) with the
// detail panel, which the page morphs into #detail. A request that selects
// nothing valid gets 204 No Content: htmx swaps nothing, and the detail
// stays as it was (docs/spec.md §5.2).
func (s *server) detail(w http.ResponseWriter, r *http.Request) {
	sn, ok := selectedSensor(r)
	if !ok {
		w.WriteHeader(http.StatusNoContent)
		return
	}
	now := s.now()
	// An alert link's window that has aged out of the history opens the
	// live window; the panel's toolbar says which it shows.
	z, _ := zoomFromQuery(r.URL.Query(), now)
	// #detail carries hx-plot:autosize, so the request has its width.
	d, err := s.detailView(sn, z, now, chartWidth(r))
	if err != nil {
		s.fail(w, r, err)
		return
	}
	d.Status = "Showing " + sn.Name() + "."
	if z.on() {
		d.Status = "Showing " + sn.Name() + ", " + rangeText(z.From, z.To, now, s.loc) + "."
	}
	h := w.Header()
	h.Set("Cache-Control", "no-cache")
	// The selection is page state: the address bar names it, so it can be
	// shared, and Back returns to the previous sensor.
	h.Set("HX-Push-Url", pageURL(sn, z))
	s.render(w, r, "detail", d)
}

// detailChart answers three kinds of request on one URL (docs/spec.md §5.4):
//
//   - a brush (hp-x0 and hp-x1, from a drag or the zoom form) zooms in;
//   - zoom=out (the "Back to live" link) returns to the live window;
//   - anything else is a poll, zoomed when the URL carries from and to.
//
// A poll is answered with the chart only (innerMorph). A zoom changes the
// polling element's own hx-get, which an inner swap cannot do, so it is
// answered with the whole polling element (outerMorph), the zoom toolbar as
// an <hx-partial>, and HX-Push-Url naming the page URL of the new view.
func (s *server) detailChart(w http.ResponseWriter, r *http.Request) {
	sn, now := pageSensor(r), s.now()
	if z, ok := brushZoom(r, now, s.loc); ok {
		s.zoomTo(w, r, sn, z, now)
		return
	}
	if _, ok := hx.Brushed(r); ok {
		// A brush whose values do not parse, that lies wholly outside the
		// data, or of another chart: ignore it (§5.3, §7). htmx swaps
		// nothing for a 204, and nothing is pushed into the history.
		w.WriteHeader(http.StatusNoContent)
		return
	}
	q := r.URL.Query()
	if q.Get("zoom") == "out" {
		s.zoomTo(w, r, sn, zoom{}, now)
		return
	}
	z, ok := zoomFromQuery(q, now)
	if !ok {
		// A zoomed poll whose window the 7-day history has started to cut
		// (or that was edited by hand). Any other window would contradict
		// the toolbar and the address bar, which still name this zoom and
		// which a poll does not update, so keep the last frame: htmx swaps
		// nothing for a 204. A new brush or "Back to live" moves on.
		w.WriteHeader(http.StatusNoContent)
		return
	}
	b, err := s.body(detailChart(sn, z, now, chartWidth(r), s.loc))
	s.writeTagged(w, r, []byte(b), err)
}

// zoomTo answers a zoom in or out with the new polling element.
func (s *server) zoomTo(w http.ResponseWriter, r *http.Request, sn sensor, z zoom, now time.Time) {
	p, err := s.poller(sn, z, now, chartWidth(r))
	if err != nil {
		s.fail(w, r, err)
		return
	}
	var b, bar bytes.Buffer
	if err := s.tmpl.ExecuteTemplate(&b, "detail-chart", p); err != nil {
		s.fail(w, r, err)
		return
	}
	if err := s.tmpl.ExecuteTemplate(&bar, "zoom-bar", newZoomBar(sn, z, now, s.loc)); err != nil {
		s.fail(w, r, err)
		return
	}
	// The toolbar's status line is a polite live region, so the zoom is
	// announced (docs/spec.md §5.5).
	b.Write(hx.Partial("#detail-zoom", "innerMorph", bar.Bytes()))
	h := w.Header()
	h.Set("Cache-Control", "no-cache")
	h.Set("HX-Push-Url", pageURL(sn, z))
	writeHTML(w, b.Bytes())
}

// ---- Static files ---------------------------------------------------------------

func (s *server) stylesheet(w http.ResponseWriter, r *http.Request) {
	h := w.Header()
	h.Set("Content-Type", "text/css; charset=utf-8")
	h.Set("Cache-Control", "no-cache")
	h.Set("ETag", s.cssTag)
	http.ServeContent(w, r, "hx-plot.css", time.Time{}, bytes.NewReader(s.css))
}

// ---- Helpers ------------------------------------------------------------------

// chartWidth is the width that the client extension reported for the
// request's autosize container (hp-w, docs/spec.md §4.6), or the default.
// hx.Size clamps it and ignores values that are not positive numbers.
func chartWidth(r *http.Request) float64 {
	w, _ := hx.Size(r, defaultWidth, 0)
	return w
}

// body renders a chart with its data table.
func (s *server) body(c *hyperplot.Chart) (template.HTML, error) {
	svg, err := c.HTML()
	if err != nil {
		return "", err
	}
	table, err := c.Table()
	if err != nil {
		return "", err
	}
	var b bytes.Buffer
	if err := s.tmpl.ExecuteTemplate(&b, "chart-body", chartBody{Title: c.Title, SVG: svg, Table: table}); err != nil {
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

// fail logs err and answers without breaking the page. An htmx request gets
// 204 No Content, so the chart keeps its last good render; anything else
// gets a plain 500.
func (s *server) fail(w http.ResponseWriter, r *http.Request, err error) {
	s.logger.Printf("%s %s: %v", r.Method, r.URL.Path, err)
	if hx.IsRequest(r) {
		w.WriteHeader(http.StatusNoContent)
		return
	}
	http.Error(w, "internal error", http.StatusInternalServerError)
}
