// Package client embeds hyperplot.js, the optional browser extension for
// hyperplot charts, so that Go programs can serve it without npm or a CDN.
//
// The script adds hover and keyboard tooltips, a crosshair, a band
// highlight, state that survives htmx morph swaps, and responsive
// re-rendering through the hp:resize event and the hp-w and hp-h request
// parameters. Charts render and read correctly without it.
//
// Serve it with [Handler] and load it with a script tag after htmx:
//
//	mux.Handle("GET /static/hyperplot.js", client.Handler())
//
//	<script src="https://cdn.jsdelivr.net/npm/htmx.org@4.0.2/dist/htmx.min.js"></script>
//	<script src="/static/hyperplot.js"></script>
package client

import (
	"crypto/sha256"
	_ "embed"
	"encoding/hex"
	"net/http"
	"strings"
	"time"
)

// js is the embedded script. It is a string, so nothing can change it and
// the ETag computed from it at init stays a valid validator for what
// [Handler] serves.
//
//go:embed hyperplot.js
var js string

// JS returns the source of hyperplot.js: plain ES2020 that needs no build
// step. Each call returns a new copy, so changing it affects neither later
// calls nor what [Handler] serves.
func JS() []byte {
	return []byte(js)
}

// etag is a strong validator over the content of js.
var etag = func() string {
	sum := sha256.Sum256([]byte(js))
	return `"hp-` + hex.EncodeToString(sum[:12]) + `"`
}()

// Handler returns an [http.Handler] that serves hyperplot.js to GET and
// HEAD requests. Responses carry Content-Type text/javascript;
// charset=utf-8, a strong ETag over the content, and Cache-Control: public,
// max-age=3600, and a request whose If-None-Match matches the ETag gets
// 304 Not Modified. Other methods get 405 Method Not Allowed.
//
// The handler ignores the request path, so it can be mounted at any URL.
func Handler() http.Handler {
	return http.HandlerFunc(serve)
}

func serve(w http.ResponseWriter, r *http.Request) {
	h := w.Header()
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		h.Set("Allow", "GET, HEAD")
		http.Error(w, http.StatusText(http.StatusMethodNotAllowed), http.StatusMethodNotAllowed)
		return
	}
	h.Set("ETag", etag)
	h.Set("Cache-Control", "public, max-age=3600")
	h.Set("X-Content-Type-Options", "nosniff")
	if notModified(r.Header.Values("If-None-Match")) {
		w.WriteHeader(http.StatusNotModified)
		return
	}
	h.Set("Content-Type", "text/javascript; charset=utf-8")
	// ServeContent handles HEAD and Range and sets Content-Length.
	http.ServeContent(w, r, "hyperplot.js", time.Time{}, strings.NewReader(js))
}

// notModified reports whether the If-None-Match field lines list the ETag
// or "*". RFC 9110 treats repeated lines as one comma-separated list and
// requires the weak comparison, so W/"x" matches "x".
func notModified(lines []string) bool {
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
