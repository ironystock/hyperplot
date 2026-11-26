// Command polling-ptag shows a chart that polls its URL every two seconds
// and costs a bodiless 304 whenever nothing changed.
//
//	go run ./examples/recipes/polling-ptag            # http://localhost:8080
//	go run ./examples/recipes/polling-ptag -addr :9000
//
// The pattern (docs/htmx.md, "Pattern 1: polling with content tags"): the
// chart's wrapper polls with hx-trigger="every 2s" and swaps with
// innerMorph. With htmx's hx-ptag extension loaded, the server tags every
// render with a hash of its bytes (hx.Tag) and sends it as the HX-PTag
// response header; htmx echoes the tag on the next poll, and hx.CheckPTag
// answers a matching tag with a bodiless 304, which htmx does not swap
// (spec/spec.md §4.5). The data here changes every 10 seconds, so about
// four polls in five are 304s.
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
	"os"
	"os/signal"
	"strconv"
	"syscall"
	"time"

	"github.com/ironystock/hyperplot/client"
	"github.com/ironystock/hyperplot/hx"
	"github.com/ironystock/hyperplot/plot"
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

// The chart shows the last five minutes in 10-second buckets. A poll
// within the same bucket renders the same bytes, so it gets the same tag.
const (
	step   = 10 * time.Second
	window = 5 * time.Minute
)

type server struct {
	now func() time.Time
	loc *time.Location
}

func newServer(now func() time.Time, loc *time.Location) *server {
	return &server{now: now, loc: loc}
}

func (s *server) routes() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /{$}", s.page)
	mux.HandleFunc("GET /charts/connections", s.connections)
	mux.Handle("GET /static/hx-plot.js", client.Handler())
	mux.HandleFunc("GET /static/hx-plot.css", css(plot.Stylesheet()))
	mux.HandleFunc("GET /static/app.css", css(appCSS))
	return securityHeaders(mux)
}

// page renders the chart and seeds its tag into hx-ptag, so even the first
// poll is a 304 when the data has not moved on since the page loaded.
func (s *server) page(w http.ResponseWriter, _ *http.Request) {
	svg, err := s.chart().Render()
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	var b bytes.Buffer
	data := struct {
		Chart template.HTML // trusted: hyperplot escapes everything it renders
		Tag   string
	}{template.HTML(svg), hx.Tag(svg)}
	if err := page.Execute(&b, data); err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	writeHTML(w, b.Bytes())
}

// connections answers each poll: render, tag the bytes, and let CheckPTag
// decide between a 304 and the new chart.
func (s *server) connections(w http.ResponseWriter, r *http.Request) {
	svg, err := s.chart().Render()
	if err != nil {
		log.Printf("%s: %v", r.URL.Path, err)
		w.WriteHeader(http.StatusNoContent) // htmx swaps nothing: the last good chart stays
		return
	}
	// The browser must ask every time; the tag, not a cache, saves the work.
	w.Header().Set("Cache-Control", "no-cache")
	// CheckPTag sets the HX-PTag response header, and when the request's
	// HX-PTag (the tag htmx stored from the last response) matches, writes
	// a bodiless 304 and reports true. Call it before writing anything.
	if hx.CheckPTag(w, r, hx.Tag(svg)) {
		return
	}
	writeHTML(w, svg)
}

// chart is open connections over the last five minutes. Its values are a
// pure function of the bucket, so any instant renders the same bytes.
func (s *server) chart() *plot.Chart {
	end := s.now().Truncate(step)
	n := int(window / step)
	// One bucket before the window is drawn too (clipped), so the line
	// reaches the left edge while it slides (spec/spec.md §4.4).
	xs, ys := make(plot.Times, n+1), make([]float64, n+1)
	for i := range n + 1 {
		t := end.Add(time.Duration(i-n) * step)
		b := t.Unix() / int64(step/time.Second)
		xs[i] = t
		ys[i] = math.Round(420 + 90*math.Sin(float64(b)/7) + 60*noise("connections", b))
	}
	return &plot.Chart{
		ID:       "connections", // stable, so the morph updates the chart in place
		Title:    "Open connections, last 5 minutes",
		Animate:  true, // hx-plot.js slides the line when the window moves
		Height:   280,
		Location: s.loc,
		X:        plot.Axis{TimeDomain: [2]time.Time{end.Add(-window), end}},
		Y:        plot.Axis{Zero: true},
		Marks:    []plot.Mark{plot.Line{Name: "connections", X: xs, Y: ys}},
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
