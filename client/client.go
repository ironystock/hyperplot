// Package client embeds hx-plot.js, the htmx 4 extension for hyperplot
// charts (npm htmx-ext-plot), and its two optional companions, so that Go
// programs can serve them without npm or a CDN.
//
// hx-plot.js adds hover and keyboard tooltips, a crosshair, a band
// highlight, state that survives htmx morph swaps, sliding windows,
// live tables that follow their newest row, responsive re-rendering through
// the htmx:plot:resize event and the hp-w and hp-h request parameters, and
// select gestures that become requests (spec/spec.md). hx-plot-brush.js adds
// brushing (hx-plot:brush) and hx-plot-tween.js tweens paths where CSS
// cannot (Safari). Charts render and read correctly without any of them.
//
// Serve them with [Handler], [BrushHandler] and [TweenHandler] and load them
// with script tags after htmx:
//
//	mux.Handle("GET /static/hx-plot.js", client.Handler())
//	mux.Handle("GET /static/hx-plot-brush.js", client.BrushHandler())
//
//	<script src="https://cdn.jsdelivr.net/npm/htmx.org@4.0.2/dist/htmx.min.js"></script>
//	<script src="/static/hx-plot.js"></script>
//	<script src="/static/hx-plot-brush.js"></script>
package client

import (
	"crypto/sha256"
	_ "embed"
	"encoding/hex"
	"net/http"
	"strings"
	"time"
)

// The embedded scripts are strings, so nothing can change them and the
// ETags computed from them at init stay valid validators for what the
// handlers serve.
var (
	//go:embed hx-plot.js
	coreJS string
	//go:embed hx-plot-brush.js
	brushJS string
	//go:embed hx-plot-tween.js
	tweenJS string
)

// script is one embedded file with its strong validator.
type script struct {
	name, src, etag string
}

func newScript(name, src string) *script {
	sum := sha256.Sum256([]byte(src))
	return &script{name, src, `"hp-` + hex.EncodeToString(sum[:12]) + `"`}
}

var (
	core  = newScript("hx-plot.js", coreJS)
	brush = newScript("hx-plot-brush.js", brushJS)
	tween = newScript("hx-plot-tween.js", tweenJS)
)

// JS returns the source of hx-plot.js: plain ES2020 that needs no build
// step. Each call returns a new copy, so changing it affects neither later
// calls nor what [Handler] serves.
func JS() []byte {
	return []byte(core.src)
}

// BrushJS returns the source of hx-plot-brush.js, the optional companion
// that adds brush gestures. Each call returns a new copy.
func BrushJS() []byte {
	return []byte(brush.src)
}

// TweenJS returns the source of hx-plot-tween.js, the optional companion
// that tweens paths in browsers whose CSS cannot transition d. Each call
// returns a new copy.
func TweenJS() []byte {
	return []byte(tween.src)
}

// Handler returns an [http.Handler] that serves hx-plot.js to GET and HEAD
// requests. Responses carry Content-Type text/javascript; charset=utf-8, a
// strong ETag over the content, and Cache-Control: public, max-age=3600,
// and a request whose If-None-Match matches the ETag gets 304 Not
// Modified. Other methods get 405 Method Not Allowed.
//
// The handler ignores the request path, so it can be mounted at any URL.
func Handler() http.Handler {
	return core
}

// BrushHandler returns an [http.Handler] that serves hx-plot-brush.js like
// [Handler] serves hx-plot.js. Load it after hx-plot.js.
func BrushHandler() http.Handler {
	return brush
}

// TweenHandler returns an [http.Handler] that serves hx-plot-tween.js like
// [Handler] serves hx-plot.js. Load it after hx-plot.js.
func TweenHandler() http.Handler {
	return tween
}

func (s *script) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	h := w.Header()
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		h.Set("Allow", "GET, HEAD")
		http.Error(w, http.StatusText(http.StatusMethodNotAllowed), http.StatusMethodNotAllowed)
		return
	}
	h.Set("ETag", s.etag)
	h.Set("Cache-Control", "public, max-age=3600")
	h.Set("X-Content-Type-Options", "nosniff")
	if notModified(r.Header.Values("If-None-Match"), s.etag) {
		w.WriteHeader(http.StatusNotModified)
		return
	}
	h.Set("Content-Type", "text/javascript; charset=utf-8")
	// ServeContent handles HEAD and Range and sets Content-Length.
	http.ServeContent(w, r, s.name, time.Time{}, strings.NewReader(s.src))
}

// notModified reports whether the If-None-Match field lines list etag or
// "*". RFC 9110 treats repeated lines as one comma-separated list and
// requires the weak comparison, so W/"x" matches "x".
func notModified(lines []string, etag string) bool {
	for _, line := range lines {
		for _, t := range strings.Split(line, ",") {
			t = strings.TrimSpace(t)
			if t == "*" || strings.TrimPrefix(t, "W/") == etag {
				return true
			}
		}
	}
	return false
}
