// Command sensors is a building sensor monitor: twelve simulated sensors
// (temperature, humidity, CO₂ and power) whose charts are drawn as SVG on
// the server and kept live by htmx 4 and the hx-plot extension.
//
//	go run ./examples/sensors              # http://localhost:8080
//	go run ./examples/sensors -addr :9000
//	go run ./examples/sensors -speed 30    # simulated time runs 30x faster
//
// The page combines the hx-plot patterns a monitoring screen needs:
//
//   - One server-sent event stream (hx-sse:connect) keeps the tile grid
//     live: each tile is a sensor's reading and a sparkline, and each
//     message also carries the alerts table as an <hx-partial>. Tiles and
//     alert rows have stable ids, so innerMorph updates them in place.
//   - The over-limit chart (minutes at warning and alarm level, per
//     sensor) polls every 10 seconds with hx-ptag. It changes once a
//     minute, so nearly every poll gets a bodiless 304 Not Modified.
//   - Select to drill down: a click on a sensor's bar (or Enter on a
//     focused one) becomes a request with hp-chart and hp-x, which the
//     server reads with hx.Selected and answers with that sensor's detail.
//     Each tile and each alert row is a plain link to the same detail.
//   - The detail chart draws the sensor's limits as rules (RuleY), polls
//     every 5 seconds with hx-ptag, and zooms when you brush across it
//     (hx-plot:brush, read with hx.Brushed). Select and zoom push the page
//     URL, so Back undoes them and the address bar can be shared.
//   - Every chart fits its container: hx-plot:autosize reports the width
//     (hp-w), which the handlers read with hx.Size.
//
// The data is deterministic: each reading is a pure function of the sensor
// and its time bucket. The page loads htmx and its extensions from a CDN,
// so a browser needs network access; the server uses only the standard
// library and this module.
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
	speed := flag.Float64("speed", 1, "how many times faster than real time the simulated clock runs, so minutes close sooner")
	flag.Parse()
	logger := log.New(os.Stderr, "sensors: ", log.LstdFlags)
	if !(*speed > 0 && *speed <= 1000) {
		logger.Fatalf("-speed %v: want a number in (0, 1000]", *speed)
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if err := run(ctx, *addr, *speed, logger); err != nil {
		logger.Fatal(err)
	}
}

// run serves the monitor on addr until ctx is canceled, then shuts down
// gracefully. Open SSE streams end when ctx does, because every request
// context derives from it.
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

// newHTTPServer configures the http.Server. The timeouts drop clients that
// trickle in a request or stop reading a response. They don't cut off the
// tile stream: net/http clears the read deadline once it has read the
// request, and hx.Stream moves the write deadline 30 seconds ahead on
// every message.
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
