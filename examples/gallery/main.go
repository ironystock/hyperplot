// Command gallery shows every mark and variant hyperplot draws, each card
// next to the Go code that drew it.
//
//	go run ./examples/gallery                    # http://localhost:8080
//	go run ./examples/gallery -addr :9000
//	go run ./examples/gallery -out out/gallery   # write static files and exit
//
// Each card's chart comes from one func in charts.go, and its code is that
// func's source, read from the same file: charts.go is embedded into the
// binary and parsed at startup (source.go), so the two cannot drift apart.
//
// The page needs no htmx. It is rendered once and served as is; hx-plot.js
// alone runs in standalone mode (docs/spec.md §6.10) and adds tooltips, a
// crosshair and arrow-key reading to every inline chart. Light and dark are
// plain links (?theme=light, ?theme=dark): the server writes data-theme on
// <html>, which hyperplot's stylesheet honors. With -out, the same page is
// written as index.html, light.html and dark.html, with the stylesheets and
// hx-plot.js under static/, ready for any static host.
package main

import (
	"bytes"
	"context"
	_ "embed"
	"flag"
	"fmt"
	"html/template"
	"io"
	"log"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"github.com/ironystock/hyperplot"
	"github.com/ironystock/hyperplot/client"
)

var (
	//go:embed page.html
	pageHTML string
	//go:embed static/app.css
	appCSS string

	page = template.Must(template.New("page").Parse(pageHTML))
)

// contentSecurityPolicy is examples/dashboard's policy without its htmx
// CDN: the page's one script, hx-plot.js, comes from this origin, and no
// inline script or style is allowed. The export's policy below allows the
// same scripts.
const contentSecurityPolicy = "default-src 'self'; script-src 'self'; " +
	"style-src 'self'; img-src 'self' data:; connect-src 'self'; " +
	"base-uri 'none'; form-action 'self'; frame-ancestors 'none'"

// exportPolicy is the policy the static export carries in a <meta> tag,
// since a static host may send no headers. frame-ancestors is left out:
// browsers ignore it in a <meta> and log a warning.
const exportPolicy = "default-src 'self'; script-src 'self'; style-src 'self'; " +
	"img-src 'self' data:; base-uri 'none'; form-action 'none'"

// pageData is what page.html renders.
type pageData struct {
	*gallery
	Theme  string      // "", "light" or "dark": the data-theme on <html>
	Themes []themeLink // the links that switch it
	Policy string      // a <meta> CSP, for the static export only
}

type themeLink struct {
	Label, Href string
	Current     bool
}

// themes are the three choices. "" follows the reader's system setting.
var themes = []struct{ value, label, file string }{
	{"", "System", "index.html"},
	{"light", "Light", "light.html"},
	{"dark", "Dark", "dark.html"},
}

// renderPage writes the page for one theme. href gives each theme's link:
// a query string on the server, a file name in the export.
func renderPage(w io.Writer, g *gallery, theme string, href func(theme, file string) string, policy string) error {
	data := pageData{gallery: g, Theme: theme, Policy: policy}
	for _, t := range themes {
		data.Themes = append(data.Themes, themeLink{Label: t.label, Href: href(t.value, t.file), Current: t.value == theme})
	}
	return page.Execute(w, data)
}

type server struct {
	gallery *gallery
}

func newServer(g *gallery) *server { return &server{gallery: g} }

func (s *server) routes() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /{$}", s.page)
	mux.Handle("GET /static/hx-plot.js", client.Handler()) // the extension, embedded in the client package
	mux.HandleFunc("GET /static/hx-plot.css", css(hyperplot.Stylesheet()))
	mux.HandleFunc("GET /static/app.css", css(appCSS))
	return securityHeaders(mux)
}

// page serves the gallery. ?theme= is untrusted: only "light" and "dark"
// reach the page; anything else follows the system setting.
func (s *server) page(w http.ResponseWriter, r *http.Request) {
	theme := r.URL.Query().Get("theme")
	if theme != "light" && theme != "dark" {
		theme = ""
	}
	query := func(t, _ string) string {
		if t == "" {
			return "./"
		}
		return "?theme=" + t
	}
	var b bytes.Buffer
	if err := renderPage(&b, s.gallery, theme, query, ""); err != nil {
		log.Printf("page: %v", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_, _ = w.Write(b.Bytes())
}

// export writes the gallery as static files: one page per theme, and the
// assets under static/. Every URL in the pages is relative, so the
// directory works from any path on any host, or opened from disk.
func export(dir string, g *gallery) error {
	if err := os.MkdirAll(filepath.Join(dir, "static"), 0o755); err != nil {
		return err
	}
	files := map[string][]byte{
		"static/hx-plot.js":  client.JS(),
		"static/hx-plot.css": []byte(hyperplot.Stylesheet()),
		"static/app.css":     []byte(appCSS),
	}
	for _, t := range themes {
		var b bytes.Buffer
		if err := renderPage(&b, g, t.value, func(_, file string) string { return file }, exportPolicy); err != nil {
			return fmt.Errorf("%s: %w", t.file, err)
		}
		files[t.file] = b.Bytes()
	}
	for name, body := range files {
		if err := os.WriteFile(filepath.Join(dir, filepath.FromSlash(name)), body, 0o644); err != nil {
			return err
		}
	}
	return nil
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
	out := flag.String("out", "", "write the gallery as static files to this directory and exit")
	flag.Parse()

	// Every chart renders here, once. An entry that fails stops the
	// program instead of leaving a hole in the page.
	g, err := build(sections, chartsGo)
	if err != nil {
		log.Fatal(err)
	}
	if *out != "" {
		if err := export(*out, g); err != nil {
			log.Fatal(err)
		}
		log.Printf("wrote %s", filepath.Join(*out, "index.html"))
		return
	}
	// Ctrl-C cancels ctx, and run shuts the server down cleanly.
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	err = run(ctx, *addr, newServer(g).routes())
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
