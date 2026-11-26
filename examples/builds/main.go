// Command builds is a CI analytics page: simulated builds of three
// pipelines, drawn as server-side SVG by hyperplot and kept current by
// htmx 4 and the hx-plot extension.
//
//	go run ./examples/builds              # http://localhost:8080
//	go run ./examples/builds -addr :9000
//	go run ./examples/builds -speed 60    # builds finish 60x faster
//
// It is analytical rather than streaming: nothing is pushed, and the
// charts change only when a build finishes. That makes it a good fit for
// polling with content tags (hx-ptag): every chart polls its URL, the
// server hashes the render with hx.Tag, and while no build has finished
// hx.CheckPTag answers with a bodiless 304 that htmx does not swap.
//
//   - Build durations is a scatter, one series per pipeline, with the slow
//     outliers labeled by build number. Drag across it to zoom (brush,
//     docs/spec.md §5.3); the zoom becomes the page URL, so Back undoes it.
//   - Builds per day stacks passed and failed builds. Click a segment (or
//     focus a day and press Enter, or use the day links) to list that
//     day's builds (select, docs/spec.md §5.2).
//   - Duration trends are small multiples, one per pipeline, with daily
//     p50 and p95 lines; the server picks the column count from the
//     container's width.
//   - One filter form (range, pipelines, branch) re-renders all of it with
//     one hx-get and innerMorph. Each pipeline pins a color slot, so it
//     keeps its color whichever others are shown.
//
// The page loads htmx and its hx-ptag extension from a CDN, so a browser
// needs network access; the server uses only the standard library.
package main

import (
	"context"
	"errors"
	"flag"
	"log"
	"net"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"
)

func main() {
	addr := flag.String("addr", "localhost:8080", "address to listen on")
	speed := flag.Float64("speed", 1, "how many times faster than real time the simulated clock runs, so builds finish sooner")
	flag.Parse()
	logger := log.New(os.Stderr, "builds: ", log.LstdFlags)
	if !(*speed > 0 && *speed <= 1000) {
		logger.Fatalf("-speed %v: want a number in (0, 1000]", *speed)
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if err := run(ctx, *addr, *speed, logger); err != nil {
		logger.Fatal(err)
	}
}

// run serves the page on addr until ctx is canceled, then shuts down
// gracefully. The simulated clock runs speed times faster than real time.
func run(ctx context.Context, addr string, speed float64, logger *log.Logger) error {
	s, err := newServer(logger)
	if err != nil {
		return err
	}
	if speed != 1 {
		s.now = fastClock(time.Now, speed)
	}
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return err
	}
	srv := newHTTPServer(ctx, s.routes(), logger)
	logger.Printf("listening on http://%s", displayAddr(ln.Addr()))
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

// newHTTPServer configures the http.Server, with timeouts that bound slow
// clients: ReadHeaderTimeout and ReadTimeout drop one that trickles in its
// request, WriteTimeout one that stops reading a response. Every request
// context derives from ctx.
func newHTTPServer(ctx context.Context, h http.Handler, logger *log.Logger) *http.Server {
	return &http.Server{
		Handler:           h,
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       10 * time.Second,
		WriteTimeout:      30 * time.Second,
		IdleTimeout:       2 * time.Minute,
		BaseContext:       func(net.Listener) context.Context { return ctx },
		ErrorLog:          logger,
	}
}

// fastClock returns a clock that starts at now() and runs speed times
// faster than it.
func fastClock(now func() time.Time, speed float64) func() time.Time {
	start := now()
	return func() time.Time {
		return start.Add(time.Duration(float64(now().Sub(start)) * speed))
	}
}

// displayAddr turns a wildcard listen address into one a browser can open.
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
