// Command dashboard is hyperplot's live demo: a service dashboard whose
// charts are rendered on the server and kept live by htmx 4.
//
//	go run ./examples/dashboard            # http://localhost:8080
//	go run ./examples/dashboard -addr :9000
//	go run ./examples/dashboard -speed 5   # simulated time runs 5x faster
//
// It shows every live-update pattern hyperplot supports, on simulated,
// deterministic data:
//
//   - Latency polls every 2 seconds with hx-ptag. The server hashes each
//     render, so a poll that would return the same chart gets a bodiless
//     304 Not Modified and htmx skips the swap. The KPI tiles ride along
//     in the same response as an <hx-partial>.
//   - Throughput streams over server-sent events (hx-sse:connect), one
//     innerMorph swap per second.
//   - Requests by region changes only when a window closes, so nearly all
//     of its polls are 304s. Its grouped/stacked toggle morphs the bars
//     from one layout to the other.
//   - The filter row re-renders every chart with one hx-get and
//     innerMorph. Regions keep their colors because each pins a Slot.
//   - Every chart fits its container: the hx-plot extension fires
//     htmx:plot:resize and reports the width (hp-w), which the handlers
//     read with hx.Size.
//   - Brush to zoom: dragging across the latency chart (hx-plot:brush)
//     becomes a request with hp-x0 and hp-x1. The handler reads it with
//     hx.Brushed, answers with a polling element whose hx-get carries the
//     range, and pushes the page URL of the zoom, so Back undoes it. A
//     from/to form sends the same request without a pointer.
//   - Select to drill down: a click on a bar of the requests chart (or
//     Enter on a focused one, or a window link) becomes a request with
//     hp-x and hp-series; the handler reads it with hx.Selected and renders
//     that window in detail.
//
// Each gesture also updates a polite live region, so screen readers hear
// what the request did.
//
// The page loads htmx and its extensions from a CDN, so a browser needs
// network access; the server itself uses only the standard library.
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
	addr := flag.String("addr", ":8080", "address to listen on")
	speed := flag.Float64("speed", 1, "how many times faster than real time the simulated clock runs, so windows slide sooner (demos and browser tests)")
	flag.Parse()
	logger := log.New(os.Stderr, "dashboard: ", log.LstdFlags)
	if !(*speed > 0 && *speed <= 1000) {
		logger.Fatalf("-speed %v: want a number in (0, 1000]", *speed)
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if err := run(ctx, *addr, *speed, logger); err != nil {
		logger.Fatal(err)
	}
}

// run serves the dashboard on addr until ctx is canceled, then shuts down
// gracefully. Open SSE streams end when ctx does, because every request
// context derives from it. The simulated clock runs speed times faster than
// real time.
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
	// ctx is done by now; give in-flight requests a few seconds of their own.
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

// newHTTPServer configures the dashboard's http.Server. Every request
// context derives from ctx.
//
// The timeouts bound slow clients. ReadHeaderTimeout and ReadTimeout drop
// a client that trickles in its request (slowloris). WriteTimeout drops
// one that stops reading a response, so a handler is never stuck in Write.
//
// Neither cuts off the throughput stream. net/http clears the read
// deadline once it has read the request, and hx.Stream moves the write
// deadline 30 seconds ahead on every message, which overrides the
// server's WriteTimeout for that connection.
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
