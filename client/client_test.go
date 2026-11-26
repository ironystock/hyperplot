package client_test

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/ironystock/hyperplot/client"
)

func TestJSEmbedded(t *testing.T) {
	src := client.JS()
	if len(src) == 0 {
		t.Fatal("JS is empty")
	}
	for _, want := range []string{"registerExtension", "'hyperplot'", "metadata.hp-tips", "hp:resize", "textContent"} {
		if !bytes.Contains(src, []byte(want)) {
			t.Errorf("JS does not contain %q", want)
		}
	}
	if bytes.Contains(src, []byte("\r")) {
		t.Error("JS contains a carriage return; the file must use LF line endings")
	}
	if bytes.Contains(src, []byte("innerHTML")) {
		t.Error("JS uses innerHTML; tooltip text must go through textContent")
	}
}

// TestJSIsACopy guards the strong ETag: if a caller could change the
// served bytes, clients revalidating with the old tag would get 304 and
// keep a stale script.
func TestJSIsACopy(t *testing.T) {
	h := client.Handler()
	before := do(t, h, http.MethodGet, nil)

	src := client.JS()
	want := src[0]
	for i := range src {
		src[i] = 'X'
	}
	if got := client.JS()[0]; got != want {
		t.Fatalf("changing a returned slice changed the next one: first byte %q, want %q", got, want)
	}
	after := do(t, h, http.MethodGet, nil)
	if !bytes.Equal(after.body, before.body) {
		t.Error("changing a returned slice changed what Handler serves")
	}
	if a, b := after.header.Get("ETag"), before.header.Get("ETag"); a != b {
		t.Errorf("ETag changed from %q to %q", b, a)
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
	req := httptest.NewRequestWithContext(t.Context(), method, "/static/hyperplot.js", nil)
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

func ExampleHandler() {
	mux := http.NewServeMux()
	mux.Handle("GET /static/hyperplot.js", client.Handler())

	req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/static/hyperplot.js", nil)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	fmt.Println(rec.Code, rec.Header().Get("Content-Type"))
	// Output: 200 text/javascript; charset=utf-8
}
