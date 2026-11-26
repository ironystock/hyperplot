// Command filters-form shows a filter form that re-renders three charts
// with one request.
//
//	go run ./examples/recipes/filters-form            # http://localhost:8080
//	go run ./examples/recipes/filters-form -addr :9000
//
// The pattern (docs/htmx.md, "Pattern 3: filters" and "Pattern 4:
// multi-target updates"): the form sends hx-get="/" on every change and
// targets #charts with innerMorph. The same URL serves the whole page for
// a direct visit and only the charts for htmx, told apart by
// HX-Request-Type (hx.IsPartial), so the response says Vary. Every chart
// keeps its id and every region its color slot, so the morph updates the
// charts in place and a region keeps its color when others are hidden. An
// <hx-partial> in the same response updates the summary line outside
// #charts. hx-push-url makes each filtered view a URL.
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
	"strings"
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

// The filter choices, in a fixed order. Each region pins a color slot.
var (
	regions = []struct {
		Key  string
		slot int
	}{{"eu-west", 1}, {"us-east", 2}, {"ap-south", 3}}
	ranges = []struct {
		Key, Title string
		span, step time.Duration
	}{
		{"1h", "the last hour", time.Hour, time.Minute},
		{"6h", "the last 6 hours", 6 * time.Hour, 5 * time.Minute},
		{"24h", "the last 24 hours", 24 * time.Hour, 20 * time.Minute},
	}
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
	mux.Handle("GET /static/hx-plot.js", client.Handler())
	mux.HandleFunc("GET /static/hx-plot.css", css(hyperplot.Stylesheet()))
	mux.HandleFunc("GET /static/app.css", css(appCSS))
	return securityHeaders(mux)
}

// filters is what the form asked for. Unknown values are ignored.
type filters struct {
	Range   int    // index into ranges
	Regions []bool // one per region, in order
}

func parseFilters(r *http.Request) filters {
	f := filters{Range: 1, Regions: make([]bool, len(regions))}
	for i, rg := range ranges {
		if r.FormValue("range") == rg.Key {
			f.Range = i
		}
	}
	// The form always sends region="" (a hidden input), so "nothing
	// checked" differs from a first visit, which shows every region.
	values, sent := r.Form["region"]
	for i, rg := range regions {
		f.Regions[i] = !sent
		for _, v := range values {
			f.Regions[i] = f.Regions[i] || v == rg.Key
		}
	}
	return f
}

// page answers both the first visit and every filter change.
func (s *server) page(w http.ResponseWriter, r *http.Request) {
	_ = r.ParseForm() // a GET's form is its query string
	f := parseFilters(r)
	v, err := s.view(f)
	var b bytes.Buffer
	if err == nil && hx.IsPartial(r) {
		// The form targets #charts: send its new contents, plus the
		// summary line, which htmx swaps into #summary after the main swap.
		err = page.ExecuteTemplate(&b, "charts", v)
		b.Write(hx.Partial("#summary", "innerHTML", []byte(template.HTMLEscapeString(v.Summary))))
	} else if err == nil {
		err = page.ExecuteTemplate(&b, "page", v)
	}
	if err != nil {
		log.Print(err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	// One URL, two bodies: caches must keep the page and the fragment
	// apart (docs/spec.md §4.5).
	w.Header().Add("Vary", hx.HeaderRequest+", "+hx.HeaderRequestType)
	w.Header().Set("Cache-Control", "no-cache")
	writeHTML(w, b.Bytes())
}

// choice is a radio button or checkbox of the form.
type choice struct {
	Key     string
	Checked bool
}

type view struct {
	Ranges, Regions          []choice
	Summary                  string
	Latency, Errors, Volumes template.HTML
}

func (s *server) view(f filters) (view, error) {
	v := view{}
	var shown []string
	for i, rg := range ranges {
		v.Ranges = append(v.Ranges, choice{rg.Key, i == f.Range})
	}
	for i, rg := range regions {
		v.Regions = append(v.Regions, choice{rg.Key, f.Regions[i]})
		if f.Regions[i] {
			shown = append(shown, rg.Key)
		}
	}
	v.Summary = "No region selected."
	if len(shown) > 0 {
		v.Summary = "Showing " + strings.Join(shown, ", ") + " over " + ranges[f.Range].Title + "."
	}
	var err [3]error
	v.Latency, err[0] = s.chart(f, "latency", "p95 latency", " ms").HTML()
	v.Errors, err[1] = s.chart(f, "error-rate", "Error rate", "%").HTML()
	v.Volumes, err[2] = s.chart(f, "requests", "Requests", " req").HTML()
	for _, e := range err {
		if e != nil {
			return v, e
		}
	}
	return v, nil
}

// chart is one metric over the selected range, one line per selected
// region (bars for requests, one bar per region). The id is fixed, not
// derived from the series, so a filter change morphs the chart in place.
func (s *server) chart(f filters, id, title, unit string) *hyperplot.Chart {
	rg := ranges[f.Range]
	end := s.now().Truncate(rg.step)
	c := &hyperplot.Chart{
		ID: id, Title: title + ", " + rg.Title, ShowTitle: true, Height: 220, Location: s.loc,
		Animate: true, Y: hyperplot.Axis{Unit: unit, Zero: true}, Stack: id == "requests",
	}
	for i, reg := range regions {
		if !f.Regions[i] {
			continue
		}
		var xs hyperplot.Times
		var ys []float64
		total := 0.0
		for t := end.Add(-rg.span); !t.After(end); t = t.Add(rg.step) {
			v := value(id, reg.Key, t.Unix()/60)
			xs, ys, total = append(xs, t), append(ys, v), total+v
		}
		if id == "requests" {
			// Stack draws one full-width bar for each region's own category.
			c.Marks = append(c.Marks, hyperplot.Bar{Name: reg.Key, Slot: reg.slot, X: hyperplot.Categories{reg.Key}, Y: []float64{total}})
			continue
		}
		c.Marks = append(c.Marks, hyperplot.Line{Name: reg.Key, Slot: reg.slot, X: xs, Y: ys})
	}
	return c
}

// value is a metric for a region in one minute: a pure function of the
// three, so every request for the same filters renders the same bytes.
func value(metric, region string, minute int64) float64 {
	n := noise(metric+"/"+region, minute)
	shift := float64(len(region)) // so the regions differ a little
	wave := math.Sin(float64(minute)/45 + shift)
	switch metric {
	case "latency":
		return math.Round(90 + 12*shift + 30*wave + 15*n)
	case "error-rate":
		return math.Round((0.4+0.1*shift+0.3*wave+0.3*n)*100) / 100
	default:
		return math.Round(600 + 60*shift + 200*wave + 80*n)
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
