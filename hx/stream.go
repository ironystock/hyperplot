package hx

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"
)

// writeTimeout bounds each write to the client. Every write pushes the
// connection's write deadline this far into the future, so a long-lived
// stream outlives http.Server.WriteTimeout while a client that stops
// reading is still dropped.
const writeTimeout = 30 * time.Second

// Stream writes a server-sent event (SSE) stream for the htmx 4 hx-sse
// extension, or for any EventSource client. Create one with [NewStream].
//
// Unnamed messages ([Stream.Send], [Stream.SendID]) are swapped into the
// connecting element by hx-sse. Named events ([Stream.Event]) are
// dispatched as DOM events instead. Each call writes one complete event
// and flushes it.
//
// A Stream is safe for concurrent use: events from different goroutines
// never interleave. Once the client disconnects (the request context is
// done) or a write fails, every method returns an error; handlers stop
// streaming when they see one.
//
// Where the server supports write deadlines, each write sets the deadline
// 30 seconds ahead. The stream therefore outlives http.Server.WriteTimeout,
// but a client that stops reading is still dropped.
type Stream struct {
	mu     sync.Mutex
	w      http.ResponseWriter
	rc     *http.ResponseController
	ctx    context.Context
	lastID string
	retry  int64 // pending retry in ms to attach to the next message; 0 = none
	err    error // sticky write or flush error
	buf    []byte
}

// NewStream prepares w for a server-sent event stream and returns a
// [Stream] that writes to it. It sets the headers
//
//	Content-Type: text/event-stream
//	Cache-Control: no-cache
//	X-Accel-Buffering: no
//
// and flushes them, so the client sees the stream open immediately. The
// last header stops nginx and similar proxies from buffering the stream.
//
// NewStream returns an error, and writes nothing, if w cannot flush (see
// [http.ResponseController]); a handler can then still write an error
// response. It also returns an error if the request context is already
// done.
//
// The stream ends when the handler returns. Stop on the first error from
// any Stream method, or when r.Context() is done.
func NewStream(w http.ResponseWriter, r *http.Request) (*Stream, error) {
	ctx := r.Context()
	if err := ctx.Err(); err != nil {
		return nil, fmt.Errorf("hyperplot: SSE client gone: %w", context.Cause(ctx))
	}
	s := &Stream{
		w:      w,
		rc:     http.NewResponseController(w),
		ctx:    ctx,
		lastID: r.Header.Get(HeaderLastEventID),
	}
	h := w.Header()
	prev := map[string][]string{
		"Content-Type":      h.Values("Content-Type"),
		"Cache-Control":     h.Values("Cache-Control"),
		"X-Accel-Buffering": h.Values("X-Accel-Buffering"),
		"Content-Length":    h.Values("Content-Length"),
	}
	h.Set("Content-Type", "text/event-stream")
	h.Set("Cache-Control", "no-cache")
	h.Set("X-Accel-Buffering", "no")
	h.Del("Content-Length")
	if err := s.rc.Flush(); err != nil {
		if errors.Is(err, http.ErrNotSupported) {
			// Nothing was written; restore the headers so the caller can
			// still send an ordinary error response.
			for k, v := range prev {
				if len(v) == 0 {
					h.Del(k)
				} else {
					h[k] = v
				}
			}
			return nil, fmt.Errorf("hyperplot: SSE needs a flushable ResponseWriter: %w", err)
		}
		return nil, fmt.Errorf("hyperplot: SSE flush: %w", err)
	}
	return s, nil
}

// LastEventID returns the Last-Event-ID header of the request that opened
// the stream, or "" if there is none. hx-sse sends it when it reconnects
// after the stream carried an id (see [Stream.SendID]); the server can use
// it to replay the events the client missed.
func (s *Stream) LastEventID() string {
	return s.lastID
}

// Send writes data as one unnamed message, which hx-sse swaps into the
// connecting element (with its hx-swap, for example innerMorph).
//
// Line breaks are preserved without letting data inject SSE fields: data
// is split on newlines into consecutive "data:" lines, which the client
// joins back with "\n". CRLF and lone CR are normalized to LF. Empty data
// is sent as one empty "data:" line, which hx-sse does not swap.
func (s *Stream) Send(data []byte) error {
	return s.write(false, "", "", data)
}

// SendID is like [Stream.Send] but also sets the event id, which the client
// remembers and returns as Last-Event-ID when it reconnects. An empty id
// clears the client's last event id.
//
// Because the id travels back as an HTTP request header, it must be
// printable ASCII (U+0020 to U+007E) with no leading or trailing space.
// Browsers refuse to send header values with characters above U+00FF, so
// hx-sse would fail every reconnect; Latin-1 characters arrive as single
// bytes rather than UTF-8; and surrounding spaces are trimmed. In each case
// [Stream.LastEventID] could never equal the id that was sent. SendID
// returns an error, and writes nothing, for an id that breaks these rules.
// Numbers, timestamps and other ASCII tokens are always safe.
func (s *Stream) SendID(id string, data []byte) error {
	if err := checkID(id); err != nil {
		return err
	}
	return s.write(true, id, "", data)
}

// checkID reports why id cannot round-trip through the Last-Event-ID
// header unchanged, if it cannot.
func checkID(id string) error {
	for i := range len(id) {
		if c := id[i]; c < 0x20 || c > 0x7e {
			return fmt.Errorf("hyperplot: SSE event id %q must be printable ASCII; it is sent back as the Last-Event-ID header", id)
		}
	}
	if id != "" && (id[0] == ' ' || id[len(id)-1] == ' ') {
		return fmt.Errorf("hyperplot: SSE event id %q must not start or end with a space; the Last-Event-ID header would trim it", id)
	}
	return nil
}

// Event writes a named event. hx-sse does not swap it; it dispatches a DOM
// event called name on the connecting element, with event.detail.data set
// to data, so the page can react with hx-on or hx-trigger="name from:body".
// Two names are special to hx-sse: the name given in the element's
// hx-sse:close attribute closes the connection, and "hx:release" ends the
// request lifecycle (indicators) early.
//
// The name must be non-empty and must not contain CR, LF or NUL; Event
// returns an error, and writes nothing, otherwise. data is encoded as in
// [Stream.Send]; at least one "data:" line is always written so that
// EventSource clients dispatch the event too.
func (s *Stream) Event(name string, data []byte) error {
	if name == "" {
		return errors.New("hyperplot: SSE event name must not be empty")
	}
	if strings.ContainsAny(name, "\r\n\x00") {
		return errors.New("hyperplot: SSE event name must not contain CR, LF or NUL")
	}
	return s.write(false, "", name, data)
}

// Retry sets the client's reconnection delay, sent as the SSE "retry"
// field in whole milliseconds (rounded up). hx-sse waits about this long
// (give or take 30%) before it reconnects, and doubles the wait, up to a
// minute, only while reconnects fail with a network error or a non-2xx
// status; any 2xx answer resets it to this base delay.
//
// The field is written immediately, which standard EventSource clients
// apply at once, and is repeated on the next message, because hx-sse
// (htmx 4.0.2) only applies retry when it arrives together with data or an
// event name. d must be positive.
func (s *Stream) Retry(d time.Duration) error {
	if d <= 0 {
		return fmt.Errorf("hyperplot: SSE retry must be positive, got %v", d)
	}
	ms := int64((d + time.Millisecond - 1) / time.Millisecond)
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.usable(); err != nil {
		return err
	}
	s.retry = ms
	b := s.buf[:0]
	b = append(b, "retry: "...)
	b = strconv.AppendInt(b, ms, 10)
	b = append(b, "\n\n"...)
	return s.flush(b)
}

// Comment writes an SSE comment, which clients ignore. Send one
// periodically (every 15–30 seconds) as a keep-alive so that proxies and
// load balancers do not close an idle stream. Each line of text becomes a
// separate comment line, so text cannot inject fields.
func (s *Stream) Comment(text string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.usable(); err != nil {
		return err
	}
	b := s.buf[:0]
	b = appendLines(b, ":", text)
	b = append(b, '\n')
	return s.flush(b)
}

// write encodes one event and flushes it.
func (s *Stream) write(withID bool, id, event string, data []byte) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.usable(); err != nil {
		return err
	}
	b := s.buf[:0]
	if event != "" {
		b = append(b, "event: "...)
		b = append(b, event...)
		b = append(b, '\n')
	}
	if withID {
		if id == "" {
			b = append(b, "id:\n"...)
		} else {
			b = append(b, "id: "...)
			b = append(b, id...)
			b = append(b, '\n')
		}
	}
	if s.retry > 0 {
		b = append(b, "retry: "...)
		b = strconv.AppendInt(b, s.retry, 10)
		b = append(b, '\n')
	}
	b = appendLines(b, "data:", string(data))
	b = append(b, '\n')
	if err := s.flush(b); err != nil {
		return err
	}
	s.retry = 0
	return nil
}

// appendLines appends text to b as one or more field lines, splitting on
// LF, CRLF and CR so that no line break survives inside a field value.
// Every line gets prefix and, when the line is not empty, a single space;
// the client strips exactly one leading space, so leading spaces in text
// are preserved.
func appendLines(b []byte, prefix, text string) []byte {
	for {
		i := strings.IndexAny(text, "\r\n")
		line := text
		if i >= 0 {
			line = text[:i]
		}
		b = append(b, prefix...)
		if line != "" {
			b = append(b, ' ')
			b = append(b, line...)
		}
		b = append(b, '\n')
		if i < 0 {
			return b
		}
		if text[i] == '\r' && i+1 < len(text) && text[i+1] == '\n' {
			i++
		}
		text = text[i+1:]
	}
}

// usable reports why the stream can no longer be written, if it cannot.
// The caller holds s.mu.
func (s *Stream) usable() error {
	if s.err != nil {
		return s.err
	}
	if s.ctx.Err() != nil {
		s.err = fmt.Errorf("hyperplot: SSE client gone: %w", context.Cause(s.ctx))
		return s.err
	}
	return nil
}

// flush writes b, flushes it to the client, and keeps b's storage for
// reuse. The caller holds s.mu.
func (s *Stream) flush(b []byte) error {
	s.buf = b[:0]
	s.extendDeadline()
	// Sending the caller's HTML is the point of the stream; appendLines
	// has already made sure it cannot inject SSE fields.
	if _, err := s.w.Write(b); err != nil { //nolint:gosec // G705: text/event-stream of trusted HTML, not a page
		s.err = fmt.Errorf("hyperplot: SSE write: %w", err)
		return s.err
	}
	if err := s.rc.Flush(); err != nil {
		s.err = fmt.Errorf("hyperplot: SSE flush: %w", err)
		return s.err
	}
	return nil
}

// extendDeadline pushes the write deadline forward when the server
// supports deadlines; it is a no-op otherwise.
func (s *Stream) extendDeadline() {
	_ = s.rc.SetWriteDeadline(time.Now().Add(writeTimeout))
}
