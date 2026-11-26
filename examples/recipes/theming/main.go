// Command theming serves the same charts in two palettes, light or dark:
// the default theme and a custom "brand" theme built from it.
//
//	go run ./examples/recipes/theming            # http://localhost:8080
//	go run ./examples/recipes/theming -addr :9000
//
// The pattern: charts carry classes, never colors (hp-s1 … hp-s8), and one
// stylesheet maps them to colors through CSS custom properties (--hp-*).
// So theming is choosing a stylesheet: start from DefaultTheme, change the
// tokens you need, and serve Theme.CSS() instead of Stylesheet(). The
// stylesheet switches to the theme's Dark palette under
// prefers-color-scheme: dark, or when <html data-theme="dark">; the theme
// links here are plain links that make the server write that attribute.
// Forced colors (Windows contrast themes) are handled by the same
// stylesheet. No chart is re-rendered to change its colors.
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
)

var (
	//go:embed page.html
	pageHTML string
	//go:embed app.css
	appCSS string

	page = template.Must(template.New("page").Parse(pageHTML))
)

// contentSecurityPolicy is examples/dashboard's policy without its htmx
// CDN (this page's one script, hx-plot.js, comes from this origin). It
// allows no inline style, which is why colors live in a stylesheet and
// never in the SVG.
const contentSecurityPolicy = "default-src 'self'; script-src 'self'; " +
	"style-src 'self'; img-src 'self' data:; connect-src 'self'; " +
	"base-uri 'none'; form-action 'self'; frame-ancestors 'none'"

// brandTheme keeps the default series order, which is what keeps adjacent
// series apart for color-vision deficiencies, and changes the surface, the
// ink and slot 1. Slot 1 is the page's own --brand variable (app.css), with
// a fallback for places that lack it, such as a standalone file.
func brandTheme() hyperplot.Theme {
	t := hyperplot.DefaultTheme()
	t.Light.Surface, t.Light.Text, t.Light.TextMuted = "#fbfaf7", "#1d1b16", "#6b665c"
	t.Light.Grid, t.Light.Axis = "#ebe7de", "#c9c3b6"
	t.Light.Series[0] = "var(--brand, #0b6bcb)"
	t.Dark.Surface, t.Dark.Text, t.Dark.TextMuted = "#1b1a17", "#f1eee8", "#a29d92"
	t.Dark.Grid, t.Dark.Axis = "#34312b", "#4b4740"
	t.Dark.Series[0] = "var(--brand-dark, #5aa2ec)"
	return t
}

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
	mux.Handle("GET /static/hx-plot.js", client.Handler()) // tooltips use the theme too
	mux.HandleFunc("GET /static/hx-plot.css", css(hyperplot.Stylesheet()))
	mux.HandleFunc("GET /static/brand.css", css(brandTheme().CSS()))
	mux.HandleFunc("GET /static/app.css", css(appCSS))
	return securityHeaders(mux)
}

type link struct {
	Label, Href string
	Current     bool
}

type figure struct {
	ID, Title  string
	SVG, Table template.HTML
}

func (s *server) page(w http.ResponseWriter, r *http.Request) {
	// Both parameters are untrusted: anything unknown means the default.
	theme, palette := r.URL.Query().Get("theme"), r.URL.Query().Get("palette")
	if theme != "light" && theme != "dark" {
		theme = "" // follow the reader's system setting
	}
	// The palette picks the stylesheet; the charts are the same either way.
	stylesheet := "/static/brand.css"
	if palette != "brand" {
		palette, stylesheet = "default", "/static/hx-plot.css"
	}
	href := func(t, p string) string { return "/?" + url.Values{"theme": {t}, "palette": {p}}.Encode() }
	data := struct {
		Theme, Stylesheet string
		Themes, Palettes  []link
		Figures           []figure
	}{
		Theme:      theme,
		Stylesheet: stylesheet,
		Themes: []link{
			{"System", href("", palette), theme == ""},
			{"Light", href("light", palette), theme == "light"},
			{"Dark", href("dark", palette), theme == "dark"},
		},
		Palettes: []link{
			{"Default", href(theme, "default"), palette == "default"},
			{"Brand", href(theme, "brand"), palette == "brand"},
		},
	}
	end := s.now().Truncate(time.Hour)
	for _, c := range []*hyperplot.Chart{regionsChart(end, s.loc), clientsChart()} {
		svg, err := c.HTML()
		if err != nil {
			http.Error(w, "internal error", http.StatusInternalServerError)
			return
		}
		table, err := c.Table()
		if err != nil {
			http.Error(w, "internal error", http.StatusInternalServerError)
			return
		}
		data.Figures = append(data.Figures, figure{c.ID, c.Title, svg, table})
	}
	var b bytes.Buffer
	if err := page.Execute(&b, data); err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_, _ = w.Write(b.Bytes())
}

// regionsChart is requests per hour for four regions: slots 1 to 4.
func regionsChart(end time.Time, loc *time.Location) *hyperplot.Chart {
	c := &hyperplot.Chart{ID: "regions", Title: "Requests by region, last 24 hours", Location: loc, Y: hyperplot.Axis{Unit: " req/h"}}
	for _, region := range []string{"us-east", "us-west", "eu-west", "ap-south"} {
		xs, ys := make(hyperplot.Times, 24), make([]float64, 24)
		for i := range 24 {
			t := end.Add(time.Duration(i-23) * time.Hour)
			b := t.Unix() / 3600
			xs[i] = t
			ys[i] = math.Round(3000 + 1500*noise(region, 0) + 1200*math.Sin(float64(b)/4+6*noise(region, 1)) + 400*noise(region, b))
		}
		c.Marks = append(c.Marks, hyperplot.Line{Name: region, X: xs, Y: ys})
	}
	return c
}

// clientsChart is grouped bars pinned to slots 5 to 8, so the page shows
// all eight series colors.
func clientsChart() *hyperplot.Chart {
	quarters := hyperplot.Categories{"Q1", "Q2", "Q3", "Q4"}
	c := &hyperplot.Chart{ID: "clients", Title: "Sessions by client, 2026", Y: hyperplot.Axis{Unit: "k"}}
	for i, name := range []string{"web", "ios", "android", "partner api"} {
		ys := make([]float64, len(quarters))
		for q := range quarters {
			ys[q] = math.Round(20 + 60*noise(name, int64(q)))
		}
		c.Marks = append(c.Marks, hyperplot.Bar{Name: name, X: quarters, Y: ys, Slot: 5 + i})
	}
	return c
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
