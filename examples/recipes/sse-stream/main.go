// Command sse-stream shows a chart the server pushes once a second over
// server-sent events.
//
//	go run ./examples/recipes/sse-stream            # http://localhost:8080
//	go run ./examples/recipes/sse-stream -addr :9000
//
// The pattern (docs/htmx.md, "Pattern 2: server-sent events", and the
// container of spec/spec.md §4.5): the page draws the first frame with no
// stream. Its container (hx-plot:autosize) fetches a stream element at its
// real width on "load", and fetches a new one when its width changes. The
// stream element carries hx-sse:connect="/stream/throughput?hp-w=…" and
// hx-swap="innerMorph": htmx's hx-sse extension opens the stream and morphs
// each message into it. The handler opens an hx.Stream, sets the reconnect
// delay with retry:, and sends a chart per tick with an id until the
// client goes away. The container swaps with innerHTML, never a morph, so
// the old stream element leaves the page and its stream closes.
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
// connect-src 'self' allows the event stream.
const contentSecurityPolicy = "default-src 'self'; " +
	"script-src 'self' https://cdn.jsdelivr.net/npm/htmx.org@4.0.2/dist/; " +
	"style-src 'self'; img-src 'self' data:; connect-src 'self'; " +
	"base-uri 'none'; form-action 'self'; frame-ancestors 'none'"

const defaultWidth = 640

type server struct {
	now  func() time.Time
	loc  *time.Location
	tick time.Duration // how often the stream sends a frame
}

func newServer(now func() time.Time, loc *time.Location, tick time.Duration) *server {
	return &server{now: now, loc: loc, tick: tick}
}

func (s *server) routes() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /{$}", s.page)
	mux.HandleFunc("GET /cards/throughput", s.card)
	mux.HandleFunc("GET /stream/throughput", s.stream)
	mux.Handle("GET /static/hx-plot.js", client.Handler())
	mux.HandleFunc("GET /static/hx-plot.css", css(plot.Stylesheet()))
	mux.HandleFunc("GET /static/app.css", css(appCSS))
	return securityHeaders(mux)
}

// streamData fills the "stream" template: the chart, and the stream's URL,
// which is empty on the page so that only the card's element connects.
type streamData struct {
	URL   string
	Chart template.HTML
}

func (s *server) page(w http.ResponseWriter, _ *http.Request) {
	s.render(w, "page", defaultWidth, "")
}

// card answers the container's "load" and htmx:plot:resize requests with a
// stream element whose URL carries the container's width (hp-w).
func (s *server) card(w http.ResponseWriter, r *http.Request) {
	width, _ := hx.Size(r, defaultWidth, 0)
	w.Header().Set("Cache-Control", "no-cache")
	s.render(w, "stream", width, "/stream/throughput?hp-w="+strconv.FormatFloat(width, 'f', -1, 64))
}

func (s *server) render(w http.ResponseWriter, name string, width float64, url string) {
	svg, err := s.chart(s.now(), width).HTML()
	var b bytes.Buffer
	if err == nil {
		err = page.ExecuteTemplate(&b, name, streamData{URL: url, Chart: svg})
	}
	if err != nil {
		log.Print(err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	writeHTML(w, b.Bytes())
}

// stream sends a freshly rendered chart once per tick until the client
// goes away. Each message's id is the second it shows.
func (s *server) stream(w http.ResponseWriter, r *http.Request) {
	width, _ := hx.Size(r, defaultWidth, 0) // the URL is fixed when the stream connects
	stream, err := hx.NewStream(w, r)
	if err != nil {
		// Not an event stream, so htmx would swap this answer over the
		// chart: HX-Reswap: none stops that, and a non-2xx status makes a
		// reconnecting hx-sse back off (spec/spec.md §4.5).
		log.Printf("%s: %v", r.URL.Path, err)
		w.Header().Set("HX-Reswap", "none")
		w.WriteHeader(http.StatusServiceUnavailable)
		return
	}
	// retry: is how long hx-sse waits before reconnecting a stream that
	// dropped (spec/spec.md §4.5 SHOULD); without it, 500 ms.
	if err := stream.Retry(2 * time.Second); err != nil {
		return
	}
	// After a reconnect, Last-Event-ID names the second the client already
	// shows; that second is not sent again.
	last := stream.LastEventID()
	t := time.NewTicker(s.tick)
	defer t.Stop()
	for {
		now := s.now().Truncate(time.Second)
		if id := strconv.FormatInt(now.Unix(), 10); id != last {
			svg, err := s.chart(now, width).Render()
			if err != nil {
				log.Printf("%s: %v", r.URL.Path, err) // skip the frame; returning would reconnect
			} else if err := stream.SendID(id, svg); err != nil {
				return // the client went away
			}
			last = id
		}
		select {
		case <-r.Context().Done(): // the tab closed, or hx-sse paused a hidden tab
			return
		case <-t.C:
		}
	}
}

// chart is requests per second over the last minute, read and write.
func (s *server) chart(now time.Time, width float64) *plot.Chart {
	end := now.Truncate(time.Second)
	const n = 60
	xs := make(plot.Times, n+1) // one extra second, drawn clipped at the left edge
	reads, writes := make([]float64, n+1), make([]float64, n+1)
	for i := range n + 1 {
		t := end.Add(time.Duration(i-n) * time.Second)
		xs[i] = t
		reads[i] = math.Round(900 + 250*math.Sin(float64(t.Unix())/9) + 150*noise("reads", t.Unix()))
		writes[i] = math.Round(300 + 80*math.Cos(float64(t.Unix())/13) + 90*noise("writes", t.Unix()))
	}
	return &plot.Chart{
		ID:       "throughput", // stable, so each message morphs the chart in place
		Title:    "Requests per second, last minute",
		Animate:  true, // hx-plot.js slides the lines one second left per frame
		Width:    width,
		Height:   300,
		Location: s.loc,
		X:        plot.Axis{TimeDomain: [2]time.Time{end.Add(-n * time.Second), end}},
		Y:        plot.Axis{Unit: " req/s", Zero: true},
		Marks: []plot.Mark{
			plot.Line{Name: "reads", X: xs, Y: reads},
			plot.Line{Name: "writes", X: xs, Y: writes},
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
	err := run(ctx, *addr, newServer(time.Now, time.Local, time.Second).routes())
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
		Handler: h,
		// Every request's context derives from ctx, so Ctrl-C ends the
		// open streams too; Shutdown alone waits for them to go idle,
		// which a stream never does.
		BaseContext:       func(net.Listener) context.Context { return ctx },
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       10 * time.Second,
		// hx.Stream pushes the write deadline ahead on every message, so
		// a stream outlives WriteTimeout while other responses keep it.
		WriteTimeout: 30 * time.Second,
		IdleTimeout:  2 * time.Minute,
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
	if err := srv.Shutdown(shutdown); err != nil {
		return err
	}
	if err := <-errc; !errors.Is(err, http.ErrServerClosed) {
		return err
	}
	return nil
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
