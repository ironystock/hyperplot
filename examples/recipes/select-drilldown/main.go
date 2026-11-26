// Command select-drilldown shows a bar chart whose bars open a detail view.
//
//	go run ./examples/recipes/select-drilldown            # http://localhost:8080
//	go run ./examples/recipes/select-drilldown -addr :9000
//
// The pattern (docs/htmx.md, "Pattern 5: drill-down on select", and
// docs/spec.md §5.2, §5.5): a click on a bar, or Enter on a focused one,
// makes hx-plot.js fire htmx:plot:select on the chart. The event bubbles to
// an element whose hx-trigger turns it into an ordinary request, and the
// extension adds hp-chart, hp-x (the bar's category, the key the server
// wrote) and hp-series (the clicked bar's series). The server reads them
// with hx.Selected, checks them against its own data, and answers with
// the detail. One plain link per category sends the same request without a
// pointer, and without script loads the page with the detail open.
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
	"slices"
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

// The data: errors per hour, by service and by status class. Each class
// keeps its color slot in both charts.
var (
	services = []string{"api", "auth", "billing", "search", "web"}
	classes  = []struct {
		name string
		slot int
		base float64
	}{{"5xx", 1, 12}, {"4xx", 2, 40}}
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
	mux.HandleFunc("GET /detail", s.detail)
	mux.Handle("GET /static/hx-plot.js", client.Handler())
	mux.HandleFunc("GET /static/hx-plot.css", css(hyperplot.Stylesheet()))
	mux.HandleFunc("GET /static/app.css", css(appCSS))
	return securityHeaders(mux)
}

// link is a select as a plain link (docs/spec.md §5.5): Href loads the
// page with the detail open; Get fetches only the detail through htmx.
type link struct{ Label, Href, Get string }

// detailView fills the "detail" template.
type detailView struct {
	Status  string
	Heading string
	Chart   template.HTML // empty: nothing selected
}

// page renders the bars and the detail of a selection in the URL, which
// is how the links work without script.
func (s *server) page(w http.ResponseWriter, r *http.Request) {
	var v struct {
		Bars   template.HTML
		Links  []link
		Detail detailView
	}
	for _, svc := range services {
		q := url.Values{"hp-chart": {"errors"}, "hp-x": {svc}}.Encode()
		v.Links = append(v.Links, link{Label: svc, Href: "/?" + q, Get: "/detail?" + q})
	}
	var b bytes.Buffer
	bars, err := s.bars().HTML()
	if err == nil {
		v.Bars = bars
		v.Detail, err = s.selection(r)
	}
	if err == nil {
		err = page.ExecuteTemplate(&b, "page", v)
	}
	if err != nil {
		log.Print(err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	writeHTML(w, b.Bytes())
}

// detail answers a select, a link or Close with the detail view, which
// innerMorph swaps into #detail.
func (s *server) detail(w http.ResponseWriter, r *http.Request) {
	d, err := s.selection(r)
	var b bytes.Buffer
	if err == nil {
		err = page.ExecuteTemplate(&b, "detail", d)
	}
	if err != nil {
		log.Printf("%s: %v", r.URL.Path, err)
		w.WriteHeader(http.StatusNoContent) // htmx swaps nothing: the page stays as it is
		return
	}
	w.Header().Set("Cache-Control", "no-cache")
	writeHTML(w, b.Bytes())
}

// selection reads hp-chart, hp-x and hp-series. They come from the browser,
// so they are checked against the server's own keys; a missing or unknown
// value is no selection, not an error (docs/spec.md §5.2).
func (s *server) selection(r *http.Request) (detailView, error) {
	sel, ok := hx.Selected(r)
	if !ok || (sel.Chart != "" && sel.Chart != "errors") {
		return detailView{Status: "No service selected."}, nil
	}
	if !slices.Contains(services, sel.X) {
		// html/template escapes the echo; the value is untrusted.
		return detailView{Status: "There is no service named “" + sel.X + "”."}, nil
	}
	// hp-series names the clicked bar's class. Enter on a keyboard point
	// may send none, and an unknown one is ignored: show both classes.
	only := ""
	for _, c := range classes {
		if c.name == sel.Series {
			only = c.name
		}
	}
	svg, err := s.hourly(sel.X, only).HTML()
	if err != nil {
		return detailView{}, err
	}
	d := detailView{Heading: sel.X + ": errors per hour", Chart: svg, Status: "Showing " + sel.X + ", "}
	if only != "" {
		d.Heading = sel.X + ": " + only + " errors per hour"
		d.Status += only + " only."
	} else {
		d.Status += "all errors."
	}
	return d, nil
}

// hours are the last 24 closed hours.
func (s *server) hours() hyperplot.Times {
	end := s.now().Truncate(time.Hour)
	ts := make(hyperplot.Times, 24)
	for i := range ts {
		ts[i] = end.Add(time.Duration(i-24) * time.Hour)
	}
	return ts
}

// errorsAt is the error count of one service and class in one hour: a
// pure function of the three, so every chart agrees with every other.
func errorsAt(svc, class string, base float64, t time.Time) float64 {
	return math.Round(base * (0.4 + 1.2*noise(svc+"/"+class, t.Unix()/3600)))
}

// bars is the chart people click: errors in the last 24 hours, one group
// of bars per service. Its band keys are the service names, so hp-x is one.
func (s *server) bars() *hyperplot.Chart {
	var marks []hyperplot.Mark
	for _, c := range classes {
		ys := make([]float64, len(services))
		for i, svc := range services {
			for _, t := range s.hours() {
				ys[i] += errorsAt(svc, c.name, c.base, t)
			}
		}
		marks = append(marks, hyperplot.Bar{Name: c.name, Slot: c.slot, X: hyperplot.Categories(services), Y: ys})
	}
	return &hyperplot.Chart{
		ID: "errors", Title: "Errors by service, last 24 hours", Height: 280,
		Y: hyperplot.Axis{Unit: " errors"}, Marks: marks,
	}
}

// hourly is the detail: one service hour by hour, one class or both.
func (s *server) hourly(svc, only string) *hyperplot.Chart {
	var marks []hyperplot.Mark
	for _, c := range classes {
		if only != "" && c.name != only {
			continue
		}
		ys := make([]float64, 24)
		for i, t := range s.hours() {
			ys[i] = errorsAt(svc, c.name, c.base, t)
		}
		marks = append(marks, hyperplot.Line{Name: c.name, Slot: c.slot, X: s.hours(), Y: ys, Points: true})
	}
	return &hyperplot.Chart{
		ID: "service-errors", Title: "Errors per hour for " + svc, Height: 240, Location: s.loc,
		Y: hyperplot.Axis{Unit: " errors", Zero: true}, Marks: marks,
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
