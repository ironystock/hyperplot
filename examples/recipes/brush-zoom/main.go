// Command brush-zoom shows a live chart you zoom by dragging across it.
//
//	go run ./examples/recipes/brush-zoom            # http://localhost:8080
//	go run ./examples/recipes/brush-zoom -addr :9000
//
// The pattern (docs/htmx.md, "Pattern 6: brush to zoom", and docs/spec.md
// §5.3 to §5.5): with hx-plot-brush.js loaded, a drag across a chart inside
// an hx-plot:brush element (or Shift+arrows and Enter) fires
// htmx:plot:brush. That element's hx-trigger turns it into an ordinary
// request, to which the extension adds hp-chart, hp-x0 and hp-x1. The
// server reads them with hx.Brushed, ignores a range that does not parse,
// and answers with the whole polling element (outerMorph), whose hx-get now
// carries the range, so later polls stay zoomed. HX-Push-Url makes the zoom
// a page URL: it can be bookmarked and Back undoes it. A from/to form sends
// the same request without a pointer, or without script at all.
package main

import (
	"bytes"
	"context"
	_ "embed"
	"flag"
	"hash/fnv"
	"html/template"
	"io"
	"log"
	"math"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/signal"
	"strconv"
	"syscall"
	"time"

	"github.com/ironystock/hyperplot"
	"github.com/ironystock/hyperplot/client"
	"github.com/ironystock/hyperplot/hx"
)

var (
	//go:embed page.html
	pageHTML string
	//go:embed app.css
	appCSS string

	page = template.Must(template.New("page").Parse(pageHTML))
)

// contentSecurityPolicy is examples/dashboard's policy: scripts from this
// origin and the pinned htmx release only, no inline script or style.
const contentSecurityPolicy = "default-src 'self'; " +
	"script-src 'self' https://cdn.jsdelivr.net/npm/htmx.org@4.0.2/dist/; " +
	"style-src 'self'; img-src 'self' data:; connect-src 'self'; " +
	"base-uri 'none'; form-action 'self'; frame-ancestors 'none'"

const (
	res       = 10 * time.Second // the data's resolution
	live      = 30 * time.Minute // the unzoomed window
	history   = 6 * time.Hour    // how far back a zoom may reach
	minZoom   = time.Minute      // a shorter range is widened around its middle
	maxPoints = 120              // a zoom draws at most this many points
	localTime = "2006-01-02T15:04:05"
)

type server struct {
	now func() time.Time
	loc *time.Location // the chart's time zone, also for datetime-local input
}

func newServer(now func() time.Time, loc *time.Location) *server {
	return &server{now: now, loc: loc}
}

func (s *server) routes() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /{$}", s.page)
	mux.HandleFunc("GET /charts/latency", s.latency)
	mux.Handle("GET /static/hx-plot.js", client.Handler())
	mux.Handle("GET /static/hx-plot-brush.js", client.BrushHandler())
	mux.HandleFunc("GET /static/hx-plot.css", css(hyperplot.Stylesheet()))
	mux.HandleFunc("GET /static/app.css", css(appCSS))
	return securityHeaders(mux)
}

// zoom is a fixed window; the zero zoom is the live, sliding one.
type zoom struct{ from, to time.Time }

// query is the zoom's canonical form, "from=…&to=…", or "" when live.
func (z zoom) query() string {
	if z.from.IsZero() {
		return ""
	}
	return url.Values{"from": {z.from.Format(time.RFC3339)}, "to": {z.to.Format(time.RFC3339)}}.Encode()
}

// newZoom turns an ordered range into a window we can draw: clipped to the
// data we keep, at least minZoom long, in whole seconds. ok is false when
// the range lies wholly outside the data.
func (s *server) newZoom(t0, t1 time.Time) (z zoom, ok bool) {
	latest := s.now().Truncate(res)
	oldest := latest.Add(-history)
	if t1.Before(t0) || t0.After(latest) || t1.Before(oldest) {
		return zoom{}, false
	}
	if t0.Before(oldest) {
		t0 = oldest
	}
	if t1.After(latest) {
		t1 = latest
	}
	// A drag of a few pixels is a brush of a few seconds, which may hold
	// no point at all. Widen it around its middle rather than refuse it,
	// as the dashboard and the Python and Node examples do: the reader
	// sees the zoom happen and the status line says what it shows.
	if t1.Sub(t0) < minZoom {
		t0 = t0.Add(t1.Sub(t0)/2 - minZoom/2)
		t1 = t0.Add(minZoom)
		if t1.After(latest) {
			t0, t1 = latest.Add(-minZoom), latest
		}
		if t0.Before(oldest) {
			t0, t1 = oldest, oldest.Add(minZoom)
		}
	}
	return zoom{t0.UTC().Truncate(time.Second), t1.UTC().Truncate(time.Second)}, true
}

// page serves the live view, a pushed or bookmarked zoom (from, to), and
// the from/to form submitted without script (hp-x0, hp-x1).
func (s *server) page(w http.ResponseWriter, r *http.Request) {
	z, ok := s.brushed(r)
	if !ok {
		z, _ = s.fromQuery(r) // live when it cannot be drawn as it is
	}
	var b bytes.Buffer
	if err := s.execute(&b, "page", z); err != nil {
		log.Print(err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	writeHTML(w, b.Bytes())
}

// latency serves the polls, the brush, the form and "Back to live".
func (s *server) latency(w http.ResponseWriter, r *http.Request) {
	// A brush and a poll share this URL; the brush has hp-x0 and hp-x1.
	if _, brush := hx.Brushed(r); brush {
		z, ok := s.brushed(r)
		if !ok {
			// docs/spec.md §5.3: ignore a brush that does not parse (or
			// that lies wholly outside the data, or is another chart's).
			// htmx swaps nothing for a 204, so the chart stays as it was.
			w.WriteHeader(http.StatusNoContent)
			return
		}
		s.zoomTo(w, z)
		return
	}
	if r.FormValue("zoom") == "out" { // "Back to live"
		s.zoomTo(w, zoom{})
		return
	}
	// A poll. Its URL carries the range of a zoomed element; the answer is
	// the chart alone, which innerMorph swaps into the polling element.
	z, ok := s.fromQuery(r)
	if !ok && (r.FormValue("from") != "" || r.FormValue("to") != "") {
		// A zoomed poll whose range has aged out of the data (or was edited
		// by hand). The live chart would contradict the toolbar and the
		// address bar, which still say "Zoomed", so keep the frame: htmx
		// swaps nothing for a 204. Back to live or a new brush moves on.
		w.WriteHeader(http.StatusNoContent)
		return
	}
	svg, err := s.chart(z).Render()
	if err != nil {
		log.Printf("%s: %v", r.URL.Path, err)
		w.WriteHeader(http.StatusNoContent) // keep the last good chart
		return
	}
	w.Header().Set("Cache-Control", "no-cache")
	writeHTML(w, svg)
}

// brushed reads hp-x0 and hp-x1. Brush.TimesIn orders the ends (they may
// arrive either way round) and reads datetime-local values in the chart's
// zone; anything that does not parse, or lies wholly outside the data, is
// not ok.
func (s *server) brushed(r *http.Request) (zoom, bool) {
	b, ok := hx.Brushed(r)
	if !ok || (b.Chart != "" && b.Chart != "latency") {
		return zoom{}, false
	}
	t0, t1, err := b.TimesIn(s.loc)
	if err != nil {
		return zoom{}, false
	}
	return s.newZoom(t0, t1)
}

// fromQuery reads the canonical from and to that zoomTo pushed and put in
// the polling element's hx-get. They are drawn exactly as they are, never
// clipped again, so every poll of a zoom shows the same window. ok is false
// (and the zoom live) when they are missing or not canonical, or once the
// window has started to leave the data we keep.
func (s *server) fromQuery(r *http.Request) (zoom, bool) {
	t0, err0 := time.Parse(time.RFC3339, r.FormValue("from"))
	t1, err1 := time.Parse(time.RFC3339, r.FormValue("to"))
	if err0 != nil || err1 != nil {
		return zoom{}, false
	}
	z, ok := s.newZoom(t0, t1)
	if !ok || !z.from.Equal(t0) || !z.to.Equal(t1) {
		return zoom{}, false
	}
	return z, true
}

// zoomTo answers a zoom in or out with the whole polling element, whose
// hx-get carries the range, plus the toolbar as an <hx-partial>.
func (s *server) zoomTo(w http.ResponseWriter, z zoom) {
	var b, bar bytes.Buffer
	err := s.execute(&b, "latency", z)
	if err == nil {
		err = s.execute(&bar, "toolbar", z)
	}
	if err != nil {
		log.Print(err)
		w.WriteHeader(http.StatusNoContent)
		return
	}
	// The toolbar holds the polite status line (docs/spec.md §5.5) and
	// the form's from/to values; htmx swaps it after the main swap.
	b.Write(hx.Partial("#zoom-bar", "innerMorph", bar.Bytes()))
	// The page URL of this view, not the fragment's (docs/spec.md §5.3).
	push := "/"
	if q := z.query(); q != "" {
		push += "?" + q
	}
	w.Header().Set("HX-Push-Url", push)
	w.Header().Set("Cache-Control", "no-cache")
	writeHTML(w, b.Bytes())
}

// view fills page.html's templates. html/template escapes every string.
type view struct {
	Chart    template.HTML
	URL      string // the polling element's hx-get
	Zoomed   bool
	Status   string
	From, To string // datetime-local values in the chart's zone
}

// execute renders one of page.html's templates for zoom z.
func (s *server) execute(w io.Writer, name string, z zoom) error {
	svg, err := s.chart(z).HTML()
	if err != nil {
		return err
	}
	v := view{Chart: svg, URL: "/charts/latency", Zoomed: !z.from.IsZero()}
	from, to := z.from, z.to
	if v.Zoomed {
		v.URL += "?" + z.query()
		v.Status = "Zoomed to " + from.In(s.loc).Format("3:04:05 PM") + " to " + to.In(s.loc).Format("3:04:05 PM") + "."
	} else {
		to = s.now().Truncate(res)
		from = to.Add(-live)
		v.Status = "Live: the last 30 minutes."
	}
	v.From, v.To = from.In(s.loc).Format(localTime), to.In(s.loc).Format(localTime)
	return page.ExecuteTemplate(w, name, v)
}

// chart is request latency over a zoom, or over the live window.
func (s *server) chart(z zoom) *hyperplot.Chart {
	latest := s.now().Truncate(res)
	from, to := z.from, z.to
	if from.IsZero() {
		from, to = latest.Add(-live), latest
	}
	// The point spacing grows with the range, so a zoom reveals detail.
	step := res * time.Duration(math.Ceil(float64(to.Sub(from))/float64(maxPoints*res)))
	// One point beyond each end is drawn too (clipped), so the lines reach
	// both edges (docs/spec.md §4.4).
	var xs hyperplot.Times
	var p50, p95 []float64
	for t := from.Truncate(step).Add(-step); !t.After(latest) && !t.After(to.Add(step)); t = t.Add(step) {
		b := t.Unix() / int64(res/time.Second)
		xs = append(xs, t)
		p50 = append(p50, math.Round(80+20*math.Sin(float64(b)/40)+15*noise("p50", b)))
		p95 = append(p95, math.Round(190+60*math.Sin(float64(b)/40)+90*noise("p95", b)*noise("spike", b)))
	}
	return &hyperplot.Chart{
		ID:       "latency", // stable, so zooms and polls morph the chart in place
		Title:    "Request latency",
		Animate:  true,
		Width:    720,
		Height:   300,
		Location: s.loc,
		X:        hyperplot.Axis{TimeDomain: [2]time.Time{from, to}},
		Y:        hyperplot.Axis{Unit: " ms", Zero: true},
		Marks: []hyperplot.Mark{
			hyperplot.Line{Name: "p50", X: xs, Y: p50},
			hyperplot.Line{Name: "p95", X: xs, Y: p95},
		},
	}
}

// noise is a deterministic number in [0, 1) for a series and a bucket:
// FNV-1a of both, finished with the splitmix64 mixer.
func noise(series string, bucket int64) float64 {
	h := fnv.New64a()
	_, _ = io.WriteString(h, series+"/"+strconv.FormatInt(bucket, 10))
	x := h.Sum64()
	x = (x ^ x>>30) * 0xbf58476d1ce4e5b9
	x = (x ^ x>>27) * 0x94d049bb133111eb
	x ^= x >> 31
	return float64(x>>11) / (1 << 53)
}

func writeHTML(w http.ResponseWriter, b []byte) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_, _ = w.Write(b)
}

func css(body string) http.HandlerFunc {
	return func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/css; charset=utf-8")
		_, _ = io.WriteString(w, body)
	}
}

func securityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Security-Policy", contentSecurityPolicy)
		w.Header().Set("X-Content-Type-Options", "nosniff")
		next.ServeHTTP(w, r)
	})
}

func main() {
	addr := flag.String("addr", "localhost:8080", "address to listen on")
	flag.Parse()
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	err := run(ctx, *addr, newServer(time.Now, time.Local).routes())
	stop()
	if err != nil {
		log.Fatal(err)
	}
}

// run serves h on addr until ctx is canceled (Ctrl-C), then shuts down.
func run(ctx context.Context, addr string, h http.Handler) error {
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return err
	}
	srv := &http.Server{
		Handler:           h,
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       10 * time.Second,
		WriteTimeout:      30 * time.Second,
		IdleTimeout:       2 * time.Minute,
	}
	log.Printf("listening on http://%s", displayAddr(ln.Addr()))
	errc := make(chan error, 1)
	go func() { errc <- srv.Serve(ln) }()
	select {
	case err := <-errc:
		return err
	case <-ctx.Done():
	}
	shutdown, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer cancel()
	return srv.Shutdown(shutdown)
}

// displayAddr turns a wildcard listen address (-addr :9000 listens on
// [::]:9000) into one a browser can open.
func displayAddr(a net.Addr) string {
	host, port, err := net.SplitHostPort(a.String())
	if err != nil {
		return a.String()
	}
	if ip := net.ParseIP(host); ip == nil || ip.IsUnspecified() {
		host = "localhost"
	}
	return net.JoinHostPort(host, port)
}
