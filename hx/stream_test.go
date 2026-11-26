package hx

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

// normalizeNewlines is what the client is expected to receive for data:
// CRLF and lone CR become LF.
func normalizeNewlines(s string) string {
	return strings.ReplaceAll(strings.ReplaceAll(s, "\r\n", "\n"), "\r", "\n")
}

// headerSafeID is the fuzz oracle for SendID: an id is accepted when it is
// printable ASCII without surrounding spaces.
func headerSafeID(id string) bool {
	if strings.TrimSpace(id) != id {
		return false
	}
	for _, r := range id {
		if r < ' ' || r > '~' {
			return false
		}
	}
	return true
}

// headerRoundTrip sends id to a Go server as the Last-Event-ID header of a
// raw HTTP request, the way a reconnecting client does, and returns what
// [Stream.LastEventID] would see.
func headerRoundTrip(t *testing.T, id string) string {
	t.Helper()
	raw := "GET / HTTP/1.1\r\nHost: x\r\n" + HeaderLastEventID + ": " + id + "\r\n\r\n"
	req, err := http.ReadRequest(bufio.NewReader(strings.NewReader(raw)))
	if err != nil {
		t.Fatalf("id %q is not a valid header value: %v", id, err)
	}
	return req.Header.Get(HeaderLastEventID)
}

// get sends a GET request for srv's root that lives as long as the test.
func get(t *testing.T, srv *httptest.Server) (*http.Response, error) {
	t.Helper()
	req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, srv.URL, nil)
	if err != nil {
		return nil, err
	}
	return srv.Client().Do(req)
}

// newRecorderStream opens a Stream on a ResponseRecorder.
func newRecorderStream(t *testing.T, r *http.Request) (*Stream, *httptest.ResponseRecorder) {
	t.Helper()
	if r == nil {
		r = httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/stream", nil)
	}
	w := httptest.NewRecorder()
	s, err := NewStream(w, r)
	if err != nil {
		t.Fatalf("NewStream: %v", err)
	}
	return s, w
}

func TestNewStreamHeaders(t *testing.T) {
	w := httptest.NewRecorder()
	w.Header().Set("Content-Length", "12")
	r := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/stream", nil)
	if _, err := NewStream(w, r); err != nil {
		t.Fatalf("NewStream: %v", err)
	}
	if !w.Flushed {
		t.Error("headers were not flushed")
	}
	if w.Code != http.StatusOK {
		t.Errorf("status = %d, want 200", w.Code)
	}
	want := map[string]string{
		"Content-Type":      "text/event-stream",
		"Cache-Control":     "no-cache",
		"X-Accel-Buffering": "no",
		"Content-Length":    "",
	}
	for k, v := range want {
		if got := w.Header().Get(k); got != v {
			t.Errorf("%s = %q, want %q", k, got, v)
		}
	}
	if w.Body.Len() != 0 {
		t.Errorf("body = %q, want empty", w.Body.String())
	}
}

// plainWriter is a ResponseWriter that cannot flush.
type plainWriter struct {
	h       http.Header
	code    int
	written bytes.Buffer
}

func (p *plainWriter) Header() http.Header         { return p.h }
func (p *plainWriter) Write(b []byte) (int, error) { return p.written.Write(b) }
func (p *plainWriter) WriteHeader(code int)        { p.code = code }

func TestNewStreamNotFlushable(t *testing.T) {
	w := &plainWriter{h: http.Header{}}
	w.h.Set("Content-Type", "text/plain")
	w.h.Set("Content-Length", "5")
	r := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/stream", nil)

	s, err := NewStream(w, r)
	if err == nil || s != nil {
		t.Fatalf("NewStream = %v, %v; want nil stream and an error", s, err)
	}
	if !errors.Is(err, http.ErrNotSupported) {
		t.Errorf("error %v does not wrap http.ErrNotSupported", err)
	}
	if !strings.HasPrefix(err.Error(), "hyperplot: ") {
		t.Errorf("error %q lacks the hyperplot prefix", err)
	}
	if w.code != 0 || w.written.Len() != 0 {
		t.Errorf("NewStream wrote a response: code %d, body %q", w.code, w.written.String())
	}
	if got := w.h.Get("Content-Type"); got != "text/plain" {
		t.Errorf("Content-Type not restored: %q", got)
	}
	if got := w.h.Get("Content-Length"); got != "5" {
		t.Errorf("Content-Length not restored: %q", got)
	}
	for _, k := range []string{"Cache-Control", "X-Accel-Buffering"} {
		if _, ok := w.h[k]; ok {
			t.Errorf("%s left behind after failure", k)
		}
	}
}

func TestNewStreamClientGone(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	r := httptest.NewRequestWithContext(ctx, http.MethodGet, "/stream", nil)
	w := httptest.NewRecorder()
	if _, err := NewStream(w, r); !errors.Is(err, context.Canceled) {
		t.Fatalf("NewStream error = %v, want context.Canceled", err)
	}
	if w.Flushed || w.Body.Len() != 0 {
		t.Error("NewStream wrote to a gone client")
	}
}

func TestStreamLastEventID(t *testing.T) {
	r := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/stream", nil)
	s, _ := newRecorderStream(t, r)
	if got := s.LastEventID(); got != "" {
		t.Errorf("LastEventID without header = %q, want empty", got)
	}
	r = httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/stream", nil)
	r.Header.Set("Last-Event-ID", "tick-41")
	s, _ = newRecorderStream(t, r)
	if got := s.LastEventID(); got != "tick-41" {
		t.Errorf("LastEventID = %q, want tick-41", got)
	}
}

// TestStreamEncoding pins the exact bytes on the wire.
func TestStreamEncoding(t *testing.T) {
	tests := []struct {
		name string
		do   func(*Stream) error
		want string
	}{
		{"send", func(s *Stream) error { return s.Send([]byte("<svg/>")) }, "data: <svg/>\n\n"},
		{"send multi-line", func(s *Stream) error { return s.Send([]byte("<svg>\n  <g/>\n</svg>")) }, "data: <svg>\ndata:   <g/>\ndata: </svg>\n\n"},
		{"send CRLF and CR", func(s *Stream) error { return s.Send([]byte("a\r\nb\rc")) }, "data: a\ndata: b\ndata: c\n\n"},
		{"send trailing newline", func(s *Stream) error { return s.Send([]byte("a\n")) }, "data: a\ndata:\n\n"},
		{"send empty", func(s *Stream) error { return s.Send(nil) }, "data:\n\n"},
		{"send leading space", func(s *Stream) error { return s.Send([]byte(" x")) }, "data:  x\n\n"},
		{"send id", func(s *Stream) error { return s.SendID("42", []byte("x")) }, "id: 42\ndata: x\n\n"},
		{"send empty id", func(s *Stream) error { return s.SendID("", []byte("x")) }, "id:\ndata: x\n\n"},
		{"event", func(s *Stream) error { return s.Event("done", []byte("ok")) }, "event: done\ndata: ok\n\n"},
		{"event no data", func(s *Stream) error { return s.Event("hx:release", nil) }, "event: hx:release\ndata:\n\n"},
		{"retry", func(s *Stream) error { return s.Retry(2 * time.Second) }, "retry: 2000\n\n"},
		{"retry rounds up", func(s *Stream) error { return s.Retry(time.Nanosecond) }, "retry: 1\n\n"},
		{"comment", func(s *Stream) error { return s.Comment("keep-alive") }, ": keep-alive\n\n"},
		{"comment empty", func(s *Stream) error { return s.Comment("") }, ":\n\n"},
		{"comment multi-line", func(s *Stream) error { return s.Comment("a\nb") }, ": a\n: b\n\n"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s, w := newRecorderStream(t, nil)
			if err := tt.do(s); err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got := w.Body.String(); got != tt.want {
				t.Errorf("wire =\n%q\nwant\n%q", got, tt.want)
			}
		})
	}
}

// TestStreamRoundTrip streams over a real HTTP connection and parses the
// result the way the hx-sse client does.
func TestStreamRoundTrip(t *testing.T) {
	chart := "<svg xmlns=\"http://www.w3.org/2000/svg\">\n  <g class=\"hp-marks\">\r\n    <path d=\"M0 0L10 10\"/>\n  </g>\n</svg>\n"
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		s, err := NewStream(w, r)
		if err != nil {
			t.Errorf("NewStream: %v", err)
			return
		}
		steps := []error{
			s.Comment("hello"),
			s.Retry(1500 * time.Millisecond),
			s.SendID("1", []byte(chart)),
			s.Send([]byte("<p>two</p>")),
			s.Event("kpi", []byte(`{"p99":12.5}`)),
			s.SendID("", []byte("cleared")),
			s.Event("done", nil),
		}
		for i, err := range steps {
			if err != nil {
				t.Errorf("step %d: %v", i, err)
			}
		}
	}))
	defer srv.Close()

	req, _ := http.NewRequestWithContext(context.Background(), http.MethodGet, srv.URL, nil)
	req.Header.Set("Accept", "text/html, text/event-stream")
	res, err := srv.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	if ct := res.Header.Get("Content-Type"); !strings.Contains(ct, "text/event-stream") {
		t.Fatalf("Content-Type = %q; hx-sse would not stream it", ct)
	}

	var p sseParser
	got, err := p.parseAll(res.Body)
	if err != nil {
		t.Fatal(err)
	}
	want := []sseMessage{
		{Data: normalizeNewlines(chart), ID: "1", HasData: true, HasID: true, Retry: 1500},
		{Data: "<p>two</p>", ID: "1", HasData: true, Retry: -1},
		{Data: `{"p99":12.5}`, Event: "kpi", ID: "1", HasData: true, Retry: -1},
		{Data: "cleared", ID: "", HasData: true, HasID: true, Retry: -1},
		{Data: "", Event: "done", ID: "", HasData: true, Retry: -1},
	}
	if len(got) != len(want) {
		t.Fatalf("got %d messages, want %d:\n%+v", len(got), len(want), got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("message %d =\n  %+v\nwant\n  %+v", i, got[i], want[i])
		}
	}
	if len(p.Bare) != 1 || p.Bare[0].Retry != 1500 {
		t.Errorf("bare retry blocks = %+v, want one with retry 1500", p.Bare)
	}
}

// TestStreamInjection checks that no value can create SSE fields or
// messages of its own: line breaks in data stay inside the data.
func TestStreamInjection(t *testing.T) {
	payloads := []string{
		"a\n\nevent: evil\nid: 666\nretry: 1\ndata: x",
		"a\r\revent: evil\rid: 666\r",
		"a\r\n\r\nevent: evil\r\nid: 666\r\n\r\n",
		"\n\n\n",
		"data: nested\n\n: comment\n\nevent:evil",
		"</svg>\n\nid\nevent\nretry:0\n",
		"\uFEFFbom\x00nul",
	}
	for _, payload := range payloads {
		t.Run(fmt.Sprintf("%.24q", payload), func(t *testing.T) {
			r := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/stream", nil)
			s, w := newRecorderStream(t, r)
			if err := s.SendID("safe", []byte("first")); err != nil {
				t.Fatal(err)
			}
			if err := s.Send([]byte(payload)); err != nil {
				t.Fatal(err)
			}
			if err := s.Event("named", []byte(payload)); err != nil {
				t.Fatal(err)
			}
			if err := s.Comment(payload); err != nil {
				t.Fatal(err)
			}

			var p sseParser
			got, err := p.parseAll(w.Body)
			if err != nil {
				t.Fatal(err)
			}
			want := []sseMessage{
				{Data: "first", ID: "safe", HasData: true, HasID: true, Retry: -1},
				{Data: normalizeNewlines(payload), ID: "safe", HasData: true, Retry: -1},
				{Data: normalizeNewlines(payload), Event: "named", ID: "safe", HasData: true, Retry: -1},
			}
			if len(got) != len(want) {
				t.Fatalf("payload produced %d messages, want %d:\n%+v", len(got), len(want), got)
			}
			for i := range want {
				if got[i] != want[i] {
					t.Errorf("message %d =\n  %+v\nwant\n  %+v", i, got[i], want[i])
				}
			}
			if len(p.Bare) != 0 {
				t.Errorf("payload injected retry blocks: %+v", p.Bare)
			}
		})
	}
}

func TestStreamRejectsInvalidFields(t *testing.T) {
	bad := []string{"a\nb", "a\rb", "a\r\nb", "\n", "a\x00b"}
	for _, v := range bad {
		s, w := newRecorderStream(t, nil)
		if err := s.SendID(v, []byte("x")); err == nil || !strings.HasPrefix(err.Error(), "hyperplot: ") {
			t.Errorf("SendID(%q) error = %v, want a hyperplot error", v, err)
		}
		if err := s.Event(v, []byte("x")); err == nil || !strings.HasPrefix(err.Error(), "hyperplot: ") {
			t.Errorf("Event(%q) error = %v, want a hyperplot error", v, err)
		}
		if w.Body.Len() != 0 {
			t.Errorf("rejected field %q still wrote %q", v, w.Body.String())
		}
		// A rejected call does not poison the stream.
		if err := s.Send([]byte("ok")); err != nil {
			t.Errorf("Send after rejected field: %v", err)
		}
	}

	// Ids travel back as the Last-Event-ID request header, so anything that
	// cannot round-trip through a header unchanged is rejected: browsers
	// throw on characters above U+00FF (hx-sse would then fail every
	// reconnect), send Latin-1 as single bytes, and trim surrounding spaces.
	badIDs := []string{"東京-42", "café-1", " 42", "42 ", " ", "a\tb", "a\x7fb", "\xff"}
	for _, id := range badIDs {
		s, w := newRecorderStream(t, nil)
		if err := s.SendID(id, []byte("x")); err == nil || !strings.HasPrefix(err.Error(), "hyperplot: ") {
			t.Errorf("SendID(%q) error = %v, want a hyperplot error", id, err)
		}
		if w.Body.Len() != 0 {
			t.Errorf("rejected id %q still wrote %q", id, w.Body.String())
		}
	}
	goodIDs := []string{"42", "tick-41", "2026-11-25T10:00:00Z", "region a/7", "~!@#$%^&*()_+{}|:\"<>?`-=[]\\;',./"}
	for _, id := range goodIDs {
		s, w := newRecorderStream(t, nil)
		if err := s.SendID(id, []byte("x")); err != nil {
			t.Errorf("SendID(%q) = %v, want success", id, err)
		}
		if want := "id: " + id + "\ndata: x\n\n"; w.Body.String() != want {
			t.Errorf("SendID(%q) wrote %q, want %q", id, w.Body.String(), want)
		}
		if got := headerRoundTrip(t, id); got != id {
			t.Errorf("id %q comes back as Last-Event-ID %q", id, got)
		}
	}

	s, w := newRecorderStream(t, nil)
	if err := s.Event("", []byte("x")); err == nil {
		t.Error("Event with empty name succeeded")
	}
	for _, d := range []time.Duration{0, -time.Second} {
		if err := s.Retry(d); err == nil {
			t.Errorf("Retry(%v) succeeded", d)
		}
	}
	if w.Body.Len() != 0 {
		t.Errorf("rejected calls wrote %q", w.Body.String())
	}
}

// TestStreamRetryAttachesOnce checks that a retry is repeated on exactly
// the next message, which is where hx-sse applies it.
func TestStreamRetryAttachesOnce(t *testing.T) {
	s, w := newRecorderStream(t, nil)
	for _, err := range []error{
		s.Send([]byte("before")),
		s.Retry(3 * time.Second),
		s.Event("tick", []byte("1")),
		s.Send([]byte("after")),
	} {
		if err != nil {
			t.Fatal(err)
		}
	}
	var p sseParser
	got, err := p.parseAll(w.Body)
	if err != nil {
		t.Fatal(err)
	}
	retries := []int{}
	for _, m := range got {
		retries = append(retries, m.Retry)
	}
	if fmt.Sprint(retries) != "[-1 3000 -1]" {
		t.Errorf("retry per message = %v, want [-1 3000 -1]", retries)
	}
}

func TestStreamClientGone(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	r := httptest.NewRequestWithContext(ctx, http.MethodGet, "/stream", nil)
	s, w := newRecorderStream(t, r)
	if err := s.Send([]byte("live")); err != nil {
		t.Fatal(err)
	}
	cancel()
	n := w.Body.Len()
	calls := map[string]func() error{
		"Send":    func() error { return s.Send([]byte("x")) },
		"SendID":  func() error { return s.SendID("1", []byte("x")) },
		"Event":   func() error { return s.Event("e", nil) },
		"Retry":   func() error { return s.Retry(time.Second) },
		"Comment": func() error { return s.Comment("ping") },
	}
	for name, call := range calls {
		err := call()
		if !errors.Is(err, context.Canceled) {
			t.Errorf("%s after disconnect: error = %v, want context.Canceled", name, err)
		}
		if err != nil && !strings.HasPrefix(err.Error(), "hyperplot: ") {
			t.Errorf("%s error %q lacks the hyperplot prefix", name, err)
		}
	}
	if w.Body.Len() != n {
		t.Errorf("wrote %q after disconnect", w.Body.String()[n:])
	}
}

// failWriter flushes, but fails every write while fail is set.
type failWriter struct {
	*httptest.ResponseRecorder
	fail bool
}

var errBrokenPipe = errors.New("broken pipe")

func (f *failWriter) Write(b []byte) (int, error) {
	if f.fail {
		return 0, errBrokenPipe
	}
	return f.ResponseRecorder.Write(b)
}

func TestStreamWriteErrorIsSticky(t *testing.T) {
	w := &failWriter{ResponseRecorder: httptest.NewRecorder()}
	s, err := NewStream(w, httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/stream", nil))
	if err != nil {
		t.Fatal(err)
	}
	w.fail = true
	if err := s.Send([]byte("x")); !errors.Is(err, errBrokenPipe) {
		t.Fatalf("Send error = %v, want broken pipe", err)
	}
	w.fail = false
	if err := s.Comment("still there?"); !errors.Is(err, errBrokenPipe) {
		t.Errorf("Comment after failed write: error = %v, want the sticky broken pipe", err)
	}
	if w.Body.Len() != 0 {
		t.Errorf("wrote %q after a failed write", w.Body.String())
	}
}

// TestStreamDisconnectOverHTTP checks the real failure path: the client
// hangs up mid-stream and the handler's next sends return an error.
func TestStreamDisconnectOverHTTP(t *testing.T) {
	handlerErr := make(chan error, 1)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		s, err := NewStream(w, r)
		if err != nil {
			handlerErr <- err
			return
		}
		deadline := time.After(10 * time.Second)
		for {
			select {
			case <-deadline:
				handlerErr <- errors.New("stream never failed after disconnect")
				return
			default:
			}
			if err := s.Send([]byte("<svg/>")); err != nil {
				handlerErr <- err
				return
			}
			time.Sleep(5 * time.Millisecond)
		}
	}))
	defer srv.Close()

	ctx, cancel := context.WithCancel(context.Background())
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, srv.URL, nil)
	res, err := srv.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	var p sseParser
	if err := p.parse(res.Body, func(sseMessage) bool { return false }); err != nil {
		t.Fatal(err)
	}
	cancel()
	res.Body.Close()

	select {
	case err := <-handlerErr:
		if err == nil || !strings.HasPrefix(err.Error(), "hyperplot: ") {
			t.Fatalf("handler error = %v, want a hyperplot error", err)
		}
	case <-time.After(15 * time.Second):
		t.Fatal("handler did not stop after the client disconnected")
	}
}

// TestStreamOutlivesWriteTimeout checks that each write extends the write
// deadline, so a server WriteTimeout does not cut a long-lived stream.
func TestStreamOutlivesWriteTimeout(t *testing.T) {
	srv := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		s, err := NewStream(w, r)
		if err != nil {
			t.Errorf("NewStream: %v", err)
			return
		}
		for i := range 3 {
			time.Sleep(120 * time.Millisecond)
			if err := s.Send([]byte(fmt.Sprint(i))); err != nil {
				t.Errorf("Send %d: %v", i, err)
				return
			}
		}
	}))
	srv.Config.WriteTimeout = 100 * time.Millisecond
	srv.Start()
	defer srv.Close()

	res, err := get(t, srv)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	var p sseParser
	got, err := p.parseAll(res.Body)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 3 {
		t.Fatalf("received %d messages past the write timeout, want 3: %+v", len(got), got)
	}
}

// TestStreamConcurrent sends from many goroutines at once and checks that
// every message arrives whole: no interleaved lines, nothing lost.
func TestStreamConcurrent(t *testing.T) {
	const goroutines, perG = 8, 40
	payload := func(g, i int) string {
		return fmt.Sprintf("<svg id=\"g%d-%d\">\n  <path d=\"M%d %d\"/>\r\n</svg>", g, i, g, i)
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		s, err := NewStream(w, r)
		if err != nil {
			t.Errorf("NewStream: %v", err)
			return
		}
		var wg sync.WaitGroup
		for g := range goroutines {
			wg.Add(1)
			go func() {
				defer wg.Done()
				for i := range perG {
					var err error
					switch i % 4 {
					case 0:
						err = s.Send([]byte(payload(g, i)))
					case 1:
						err = s.SendID(fmt.Sprintf("%d-%d", g, i), []byte(payload(g, i)))
					case 2:
						err = s.Event("tick", []byte(payload(g, i)))
					case 3:
						err = s.Comment(payload(g, i)) // comments are invisible
						if err == nil {
							err = s.Send([]byte(payload(g, i)))
						}
					}
					if err == nil && i%10 == 0 {
						err = s.Retry(time.Duration(g+1) * time.Second)
					}
					_ = s.LastEventID()
					if err != nil {
						t.Errorf("goroutine %d send %d: %v", g, i, err)
						return
					}
				}
			}()
		}
		wg.Wait()
	}))
	defer srv.Close()

	res, err := get(t, srv)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	var p sseParser
	got, err := p.parseAll(res.Body)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != goroutines*perG {
		t.Fatalf("received %d messages, want %d", len(got), goroutines*perG)
	}
	seen := make(map[string]bool, len(got))
	for _, m := range got {
		var g, i int
		if _, err := fmt.Sscanf(m.Data, "<svg id=\"g%d-%d\">", &g, &i); err != nil {
			t.Fatalf("garbled message %q: %v", m.Data, err)
		}
		if want := normalizeNewlines(payload(g, i)); m.Data != want {
			t.Fatalf("message g%d-%d interleaved:\n%q\nwant\n%q", g, i, m.Data, want)
		}
		if seen[m.Data] {
			t.Fatalf("message g%d-%d delivered twice", g, i)
		}
		seen[m.Data] = true
		if wantEvent := i%4 == 2; (m.Event == "tick") != wantEvent {
			t.Errorf("message g%d-%d event = %q", g, i, m.Event)
		}
	}
}

// FuzzStreamRoundTrip checks that any id, event name and data either is
// rejected or arrives exactly as sent (with newlines normalized) as one
// message.
func FuzzStreamRoundTrip(f *testing.F) {
	f.Add("", "", []byte("<svg/>"))
	f.Add("7", "tick", []byte("a\nb"))
	f.Add("x\ny", "e", []byte("data: evil\n\n"))
	f.Add("1", "a\rb", []byte("\r\n\r"))
	f.Add("", "", []byte("\n\nid: 9\nevent: x\n"))
	f.Add("\x00", "\x00", []byte("\x00"))
	f.Add("東京-42", "", []byte("x"))
	f.Add(" 42 ", "", []byte("x"))
	f.Add("region a/7", "", []byte("x"))
	f.Fuzz(func(t *testing.T, id, event string, data []byte) {
		s, w := newRecorderStream(t, nil)
		var err error
		switch {
		case event != "":
			err = s.Event(event, data)
		case id != "":
			err = s.SendID(id, data)
		default:
			err = s.Send(data)
		}
		invalid := event != "" && strings.ContainsAny(event, "\r\n\x00") ||
			event == "" && id != "" && !headerSafeID(id)
		if invalid {
			if err == nil {
				t.Fatalf("invalid id %q / event %q accepted", id, event)
			}
			if w.Body.Len() != 0 {
				t.Fatalf("rejected call wrote %q", w.Body.String())
			}
			return
		}
		if err != nil {
			t.Fatalf("valid call failed: %v", err)
		}
		var p sseParser
		got, err := p.parseAll(w.Body)
		if err != nil {
			t.Fatal(err)
		}
		if len(got) != 1 {
			t.Fatalf("got %d messages, want 1: %+v", len(got), got)
		}
		m := got[0]
		if m.Data != normalizeNewlines(string(data)) {
			t.Errorf("data = %q, want %q", m.Data, normalizeNewlines(string(data)))
		}
		if m.Event != event {
			t.Errorf("event = %q, want %q", m.Event, event)
		}
		if event == "" && id != "" {
			if m.ID != id {
				t.Errorf("id = %q, want %q", m.ID, id)
			}
			if got := headerRoundTrip(t, id); got != id {
				t.Errorf("accepted id %q comes back as Last-Event-ID %q", id, got)
			}
		}
		if m.Retry != -1 {
			t.Errorf("unexpected retry %d", m.Retry)
		}
	})
}
