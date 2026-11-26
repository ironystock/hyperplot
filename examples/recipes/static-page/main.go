// Command static-page serves charts on a page with no htmx at all. The
// server renders each chart once; hx-plot.js alone makes them interactive.
//
//	go run ./examples/recipes/static-page            # http://localhost:8080
//	go run ./examples/recipes/static-page -addr :9000
//
// The pattern: put Chart.HTML into the page, serve the stylesheet and
// hx-plot.js, and stop. Without htmx the extension runs in standalone mode
// (spec/spec.md §6.10): it finds every inline chart (svg.hp, spec/spec.md §6.4) and
// adds hover tooltips, a crosshair, arrow-key reading and a status region
// for screen readers. Nothing polls; reload to see the next minute. Turn
// JavaScript off and the charts still show, with native <title> tooltips
// (spec/spec.md §3.7) and the data tables.
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
	"github.com/ironystock/hyperplot/plot"
)

var (
	//go:embed page.html
	pageHTML string
	//go:embed app.css
	appCSS string

	page = template.Must(template.New("page").Parse(pageHTML))
)

// contentSecurityPolicy is examples/dashboard's policy without its htmx
// CDN: this page's one script, hx-plot.js, comes from this origin, so
// script-src 'self' allows exactly that. No inline script or style is
// allowed, and charts need none.
const contentSecurityPolicy = "default-src 'self'; script-src 'self'; " +
	"style-src 'self'; img-src 'self' data:; connect-src 'self'; " +
	"base-uri 'none'; form-action 'self'; frame-ancestors 'none'"

// server renders the page. now and loc are injected so tests can fix them.
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
	mux.Handle("GET /static/hx-plot.js", client.Handler()) // the extension, embedded in the client package
	mux.HandleFunc("GET /static/hx-plot.css", css(plot.Stylesheet()))
	mux.HandleFunc("GET /static/app.css", css(appCSS))
	return securityHeaders(mux)
}

// figure is one chart on the page with its data table.
type figure struct {
	ID, Title  string
	SVG, Table template.HTML
}

func (s *server) page(w http.ResponseWriter, _ *http.Request) {
	end := s.now().Truncate(time.Minute) // the last whole minute: every reload inside it is identical
	var figs []figure
	for _, c := range []*plot.Chart{latencyChart(end, s.loc), regionsChart(end)} {
		svg, err := c.HTML() // escaped by construction, safe in html/template
		if err != nil {
			http.Error(w, "internal error", http.StatusInternalServerError)
			return
		}
		table, err := c.Table() // the same data for screen readers and exact numbers (spec/spec.md §3.8)
		if err != nil {
			http.Error(w, "internal error", http.StatusInternalServerError)
			return
		}
		figs = append(figs, figure{ID: c.ID, Title: c.Title, SVG: svg, Table: table})
	}
	var b bytes.Buffer
	if err := page.Execute(&b, figs); err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_, _ = w.Write(b.Bytes())
}

// latencyChart is the last hour of latency in one-minute buckets, ending at end.
func latencyChart(end time.Time, loc *time.Location) *plot.Chart {
	const n = 60
	xs, p50, p95 := make(plot.Times, n), make([]float64, n), make([]float64, n)
	for i := range n {
		t := end.Add(time.Duration(i-n+1) * time.Minute)
		b := t.Unix() / 60 // the bucket: each value depends on it alone
		xs[i] = t
		p50[i] = 90 + 15*math.Sin(float64(b)/9) + 25*noise("p50", b)
		p95[i] = 180 + 30*math.Sin(float64(b)/7) + 60*noise("p95", b)
	}
	return &plot.Chart{
		ID:       "latency", // explicit and stable: the extension keys hover state on it
		Title:    "API latency, last hour",
		Location: loc,
		Y:        plot.Axis{Label: "ms", Unit: " ms", Zero: true},
		Marks: []plot.Mark{
			plot.Line{Name: "p50", X: xs, Y: p50},
			plot.Line{Name: "p95", X: xs, Y: p95},
			plot.RuleY{Y: 250, Label: "250 ms objective"},
		},
	}
}

// regionsChart is the request count per region in the last whole hour.
func regionsChart(end time.Time) *plot.Chart {
	regions := plot.Categories{"us-east", "us-west", "eu-west", "ap-south"}
	hour := end.Unix() / 3600
	ys := make([]float64, len(regions))
	for i, r := range regions {
		ys[i] = math.Round(40000 + 30000*noise(r, hour))
	}
	return &plot.Chart{
		ID:    "regions",
		Title: "Requests by region, last full hour",
		Y:     plot.Axis{Unit: " requests"},
		Marks: []plot.Mark{plot.Bar{Name: "requests", X: regions, Y: ys}},
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
