package client_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/ironystock/hyperplot/client"
)

// scripts lists the embedded scripts with their accessors and strings
// each must contain.
var scripts = []struct {
	name    string
	js      func() []byte
	handler func() http.Handler
	want    []string
}{
	{"hx-plot.js", client.JS, client.Handler, []string{"/*! hx-plot ", "registerExtension('plot'", "metadata.hp-tips", "htmx:plot:resize", "htmx:plot:select", "textContent"}},
	{"hx-plot-brush.js", client.BrushJS, client.BrushHandler, []string{"/*! hx-plot-brush ", "htmx:plot:brush", "H.brush"}},
	{"hx-plot-tween.js", client.TweenJS, client.TweenHandler, []string{"/*! hx-plot-tween ", "H.tween"}},
}

func TestJSEmbedded(t *testing.T) {
	for _, s := range scripts {
		src := s.js()
		if len(src) == 0 {
			t.Fatalf("%s is empty", s.name)
		}
		for _, want := range s.want {
			if !bytes.Contains(src, []byte(want)) {
				t.Errorf("%s does not contain %q", s.name, want)
			}
		}
		if bytes.Contains(src, []byte("\r")) {
			t.Errorf("%s contains a carriage return; the file must use LF line endings", s.name)
		}
		if bytes.Contains(src, []byte("innerHTML")) {
			t.Errorf("%s uses innerHTML; text must go through textContent", s.name)
		}
	}
}

// TestJSIsACopy guards the strong ETags: if a caller could change the
// served bytes, clients revalidating with the old tag would get 304 and
// keep a stale script.
func TestJSIsACopy(t *testing.T) {
	for _, s := range scripts {
		h := s.handler()
		before := do(t, h, http.MethodGet, nil)

		src := s.js()
		want := src[0]
		for i := range src {
			src[i] = 'X'
		}
		if got := s.js()[0]; got != want {
			t.Fatalf("%s: changing a returned slice changed the next one: first byte %q, want %q", s.name, got, want)
		}
		after := do(t, h, http.MethodGet, nil)
		if !bytes.Equal(after.body, before.body) {
			t.Errorf("%s: changing a returned slice changed what the handler serves", s.name)
		}
		if a, b := after.header.Get("ETag"), before.header.Get("ETag"); a != b {
			t.Errorf("%s: ETag changed from %q to %q", s.name, b, a)
		}
	}
}

func TestHandlersServeTheirOwnScript(t *testing.T) {
	tags := map[string]bool{}
	for _, s := range scripts {
		res := do(t, s.handler(), http.MethodGet, nil)
		if res.code != http.StatusOK || !bytes.Equal(res.body, s.js()) {
			t.Errorf("%s: status %d, or the body differs from its JS function", s.name, res.code)
		}
		if got := res.header.Get("Content-Type"); got != "text/javascript; charset=utf-8" {
			t.Errorf("%s: Content-Type = %q", s.name, got)
		}
		etag := res.header.Get("ETag")
		if tags[etag] {
			t.Errorf("%s: ETag %s is not unique", s.name, etag)
		}
		tags[etag] = true
		if code := do(t, s.handler(), http.MethodGet, http.Header{"If-None-Match": {etag}}).code; code != http.StatusNotModified {
			t.Errorf("%s: revalidation status = %d, want 304", s.name, code)
		}
	}
}

// response is a fully read reply, so tests never leave a body open.
type response struct {
	code   int
	header http.Header
	body   []byte
}

func do(t *testing.T, h http.Handler, method string, header http.Header) response {
	t.Helper()
	req := httptest.NewRequestWithContext(t.Context(), method, "/static/hx-plot.js", nil)
	for k, vs := range header {
		for _, v := range vs {
			req.Header.Add(k, v)
		}
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	res := rec.Result()
	defer res.Body.Close()
	body, err := io.ReadAll(res.Body)
	if err != nil {
		t.Fatal(err)
	}
	return response{res.StatusCode, res.Header, body}
}

func TestHandler(t *testing.T) {
	h := client.Handler()
	res := do(t, h, http.MethodGet, nil)
	if res.code != http.StatusOK {
		t.Fatalf("status = %d, want 200", res.code)
	}
	if !bytes.Equal(res.body, client.JS()) {
		t.Error("body differs from client.JS")
	}
	for k, want := range map[string]string{
		"Content-Type":           "text/javascript; charset=utf-8",
		"Cache-Control":          "public, max-age=3600",
		"X-Content-Type-Options": "nosniff",
		"Content-Length":         fmt.Sprint(len(client.JS())),
	} {
		if got := res.header.Get(k); got != want {
			t.Errorf("%s = %q, want %q", k, got, want)
		}
	}
	etag := res.header.Get("ETag")
	if !strings.HasPrefix(etag, `"`) || !strings.HasSuffix(etag, `"`) || len(etag) < 10 {
		t.Errorf("ETag = %q, want a strong quoted tag", etag)
	}

	// The tag is stable across handlers and calls.
	if again := do(t, client.Handler(), http.MethodGet, nil).header.Get("ETag"); again != etag {
		t.Errorf("ETag changed between calls: %q then %q", etag, again)
	}

	tests := []struct {
		name        string
		ifNoneMatch []string
		want        int
	}{
		{"matching tag", []string{etag}, http.StatusNotModified},
		{"weak form of the tag", []string{"W/" + etag}, http.StatusNotModified},
		{"tag in a list", []string{`"other", ` + etag}, http.StatusNotModified},
		{"tag in a second header line", []string{`"other"`, etag}, http.StatusNotModified},
		{"wildcard", []string{"*"}, http.StatusNotModified},
		{"stale tag", []string{`"hp-000000000000000000000000"`}, http.StatusOK},
		{"unquoted tag", []string{strings.Trim(etag, `"`)}, http.StatusOK},
		{"empty", []string{""}, http.StatusOK},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			res := do(t, h, http.MethodGet, http.Header{"If-None-Match": tt.ifNoneMatch})
			if res.code != tt.want {
				t.Fatalf("status = %d, want %d", res.code, tt.want)
			}
			if tt.want != http.StatusNotModified {
				return
			}
			if len(res.body) != 0 {
				t.Errorf("304 has a %d-byte body", len(res.body))
			}
			if got := res.header.Get("ETag"); got != etag {
				t.Errorf("304 ETag = %q, want %q", got, etag)
			}
			if got := res.header.Get("Cache-Control"); got == "" {
				t.Error("304 lacks Cache-Control")
			}
		})
	}
}

func TestHandlerHead(t *testing.T) {
	res := do(t, client.Handler(), http.MethodHead, nil)
	if res.code != http.StatusOK {
		t.Fatalf("status = %d, want 200", res.code)
	}
	if len(res.body) != 0 {
		t.Errorf("HEAD response has a %d-byte body", len(res.body))
	}
	if got := res.header.Get("Content-Type"); got != "text/javascript; charset=utf-8" {
		t.Errorf("Content-Type = %q", got)
	}
}

func TestHandlerMethodNotAllowed(t *testing.T) {
	for _, m := range []string{http.MethodPost, http.MethodPut, http.MethodDelete} {
		res := do(t, client.Handler(), m, nil)
		if res.code != http.StatusMethodNotAllowed {
			t.Errorf("%s: status = %d, want 405", m, res.code)
		}
		if got := res.header.Get("Allow"); got != "GET, HEAD" {
			t.Errorf("%s: Allow = %q, want %q", m, got, "GET, HEAD")
		}
	}
}

func TestHandlerConcurrent(t *testing.T) {
	srv := httptest.NewServer(client.Handler())
	defer srv.Close()
	errs := make(chan error, 8)
	for range 8 {
		go func() {
			errs <- fetch(t.Context(), srv.URL)
		}()
	}
	for range 8 {
		if err := <-errs; err != nil {
			t.Error(err)
		}
	}
}

func fetch(ctx context.Context, url string) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return err
	}
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer res.Body.Close()
	body, err := io.ReadAll(res.Body)
	if err != nil {
		return err
	}
	if !bytes.Equal(body, client.JS()) {
		return fmt.Errorf("got %d bytes, want %d", len(body), len(client.JS()))
	}
	return nil
}

// TestPackageFiles checks that every file the npm package lists in
// package.json "files" exists, except the minified copies that prepack
// builds: npm packs without a missing one and says nothing. LICENSE, which
// npm adds on its own, must be the repository's.
func TestPackageFiles(t *testing.T) {
	raw, err := os.ReadFile("package.json")
	if err != nil {
		t.Fatal(err)
	}
	var pkg struct {
		Files []string `json:"files"`
	}
	if err := json.Unmarshal(raw, &pkg); err != nil {
		t.Fatal(err)
	}
	for _, f := range append(pkg.Files, "LICENSE") {
		if strings.HasSuffix(f, ".min.js") || strings.HasSuffix(f, ".min.js.map") {
			continue
		}
		if _, err := os.Stat(f); err != nil {
			t.Errorf("package.json lists %s: %v", f, err)
		}
	}
	got, err := os.ReadFile("LICENSE")
	if err != nil {
		t.Fatal(err)
	}
	want, err := os.ReadFile("../LICENSE")
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, want) {
		t.Error("client/LICENSE differs from the repository's LICENSE; copy it")
	}
}

func ExampleHandler() {
	mux := http.NewServeMux()
	mux.Handle("GET /static/hx-plot.js", client.Handler())

	req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/static/hx-plot.js", nil)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	fmt.Println(rec.Code, rec.Header().Get("Content-Type"))
	// Output: 200 text/javascript; charset=utf-8
}
