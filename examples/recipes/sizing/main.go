// Command sizing shows three ways a chart fits its container: scaled by
// CSS, re-rendered at the container's width, and re-rendered at its width
// and height.
//
//	go run ./examples/recipes/sizing            # http://localhost:8080
//	go run ./examples/recipes/sizing -addr :9000
//
// The pattern (docs/htmx.md, "Responsive charts with autosize"): put
// hx-plot:autosize on the element that holds the chart and make
// htmx:plot:resize one of its triggers. The extension watches the element,
// fires htmx:plot:resize when its width changes, and adds hp-w (its content
// width) to every request that targets it (docs/spec.md §4.6, §6.7). The
// handler reads it with hx.Size and renders that wide, so the text keeps
// its real size. hx-plot:autosize="both" adds hp-h too, for a container
// whose height CSS sets on its own. Every chart is visible on the first
// paint at a default size; "load" then fetches it at the real one.
package main

import (
	"bytes"
	"context"
	_ "embed"
	"errors"
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

// The server's own sizes: what a request without hp-w or hp-h gets.
const (
	defaultWidth  = 640
	defaultHeight = 320
	widthOnlyH    = 240 // the height of the width-only chart, which CSS never sets
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
	mux.HandleFunc("GET /charts/width", s.width)
	mux.HandleFunc("GET /charts/both", s.both)
	mux.Handle("GET /static/hx-plot.js", client.Handler())
	mux.HandleFunc("GET /static/hx-plot.css", css(hyperplot.Stylesheet()))
	mux.HandleFunc("GET /static/app.css", css(appCSS))
	return securityHeaders(mux)
}

// page renders all three charts at the server's default sizes.
func (s *server) page(w http.ResponseWriter, _ *http.Request) {
	scaled, err1 := s.chart("scaled", defaultWidth, defaultHeight).HTML()
	width, err2 := s.chart("width", defaultWidth, widthOnlyH).HTML()
	both, err3 := s.chart("both", defaultWidth, defaultHeight).HTML()
	data := struct{ Scaled, Width, Both template.HTML }{scaled, width, both}
	var b bytes.Buffer
	if err := errors.Join(err1, err2, err3); err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	if err := page.Execute(&b, data); err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	writeHTML(w, b.Bytes())
}

// width answers the hx-plot:autosize container: hp-w sets the width, and
// the height stays the server's. Rendering at a reported height would grow
// a container without a CSS height on every request (docs/htmx.md#height).
func (s *server) width(w http.ResponseWriter, r *http.Request) {
	width, _ := hx.Size(r, defaultWidth, 0) // clamped to [50, 4096]; bad values give the default
	s.fragment(w, r, s.chart("width", width, widthOnlyH))
}

// both answers the hx-plot:autosize="both" container, whose height comes
// from CSS (app.css), so hp-h is safe to use.
func (s *server) both(w http.ResponseWriter, r *http.Request) {
	width, height := hx.Size(r, defaultWidth, defaultHeight)
	s.fragment(w, r, s.chart("both", width, height))
}

// fragment writes a chart for an innerMorph swap. A render error answers
// htmx with 204 No Content, which htmx does not swap, so the chart keeps
// its last good render.
func (s *server) fragment(w http.ResponseWriter, r *http.Request, c *hyperplot.Chart) {
	svg, err := c.Render()
	if err != nil {
		log.Printf("%s: %v", r.URL.Path, err)
		if hx.IsRequest(r) {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	writeHTML(w, svg)
}

// chart is requests per hour over the last day, at a given size. Its
// visible title states that size, so the page shows what each request got.
func (s *server) chart(id string, width, height float64) *hyperplot.Chart {
	end := s.now().Truncate(time.Hour)
	xs, ys := make(hyperplot.Times, 24), make([]float64, 24)
	for i := range 24 {
		t := end.Add(time.Duration(i-23) * time.Hour)
		b := t.Unix() / 3600
		xs[i] = t
		ys[i] = math.Round(3000 + 1200*math.Sin(float64(b)/4) + 500*noise("requests", b))
	}
	size := strconv.FormatFloat(width, 'f', -1, 64) + " × " + strconv.FormatFloat(height, 'f', -1, 64)
	return &hyperplot.Chart{
		ID:        id, // stable across sizes, so a resize morphs the chart in place
		Title:     "Requests per hour, drawn at " + size + " px",
		ShowTitle: true,
		Width:     width,
		Height:    height,
		Location:  s.loc,
		Y:         hyperplot.Axis{Unit: " req/h"},
		Marks:     []hyperplot.Mark{hyperplot.Area{Name: "requests", X: xs, Y: ys}},
	}
}

// noise is a deterministic number in [0, 1) for a series and a bucket:
// FNV-1a of both, finished with the splitmix64 mixer. (FNV-1a alone
// barely changes its high bits when only the last digit changes.)
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
	// Ctrl-C cancels ctx, and run shuts the server down cleanly.
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
	// The timeouts drop clients that send a request or read a response
	// too slowly, so one stalled client cannot hold a connection forever.
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
