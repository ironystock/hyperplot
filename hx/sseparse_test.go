package hx

import (
	"bufio"
	"errors"
	"io"
	"strconv"
	"strings"
)

// sseMessage is one dispatched SSE message, as the hx-sse client sees it.
type sseMessage struct {
	Data    string
	Event   string
	ID      string // last event id in effect when the message was dispatched
	HasData bool
	HasID   bool
	Retry   int // -1 when the message carried no valid retry field
}

// sseParser is a line-for-line port of parseSSE in htmx 4.0.2's
// dist/ext/hx-sse.js, so the tests check the stream exactly as the client
// reads it:
//
//   - lines end at CRLF, CR or LF, and a leading BOM is dropped;
//   - a blank line dispatches if the block had data, an id, or an event;
//   - a line starting with ':' is a comment;
//   - "field: value" loses one leading space from the value;
//   - data lines are joined with "\n";
//   - an id containing NUL is ignored, and the id persists across messages;
//   - retry is parsed with parseInt semantics.
//
// Unlike the browser, it also records retry-only blocks in Bare so the
// tests can see fields that hx-sse would drop.
type sseParser struct {
	lastID string
	Bare   []sseMessage // blocks that carried only retry (not dispatched)
}

// parseAll reads r to EOF and returns the dispatched messages.
func (p *sseParser) parseAll(r io.Reader) ([]sseMessage, error) {
	var out []sseMessage
	err := p.parse(r, func(m sseMessage) bool { out = append(out, m); return true })
	return out, err
}

// parse reads r and calls yield for each dispatched message until yield
// returns false or r ends.
func (p *sseParser) parse(r io.Reader, yield func(sseMessage) bool) error {
	br := bufio.NewReader(r)
	first := true
	var (
		msg     = sseMessage{Retry: -1}
		hasData bool
		hasID   bool
	)
	for {
		line, err := readSSELine(br)
		if err != nil {
			if errors.Is(err, io.EOF) {
				return nil
			}
			return err
		}
		if first {
			line = strings.TrimPrefix(line, "\xef\xbb\xbf") // U+FEFF, UTF-8 encoded
			first = false
		}
		if line == "" {
			if hasData || hasID || msg.Event != "" {
				msg.ID = p.lastID
				msg.HasData = hasData
				msg.HasID = hasID
				if !yield(msg) {
					return nil
				}
			} else if msg.Retry >= 0 {
				p.Bare = append(p.Bare, msg)
			}
			msg = sseMessage{Retry: -1}
			hasData, hasID = false, false
			continue
		}
		colon := strings.IndexByte(line, ':')
		if colon == 0 {
			continue // comment
		}
		field, value := line, ""
		if colon > 0 {
			field, value = line[:colon], line[colon+1:]
			value = strings.TrimPrefix(value, " ")
		}
		switch field {
		case "data":
			if hasData {
				msg.Data += "\n"
			}
			msg.Data += value
			hasData = true
		case "event":
			msg.Event = value
		case "id":
			if !strings.Contains(value, "\x00") {
				p.lastID = value
				hasID = true
			}
		case "retry":
			if n, ok := parseIntJS(value); ok {
				msg.Retry = n
			}
		}
	}
}

// readSSELine returns the next line without its terminator, splitting on
// CRLF, CR or LF. A final line with no terminator is never dispatched by
// the client, so it is reported as EOF.
func readSSELine(br *bufio.Reader) (string, error) {
	var sb strings.Builder
	for {
		c, err := br.ReadByte()
		if err != nil {
			return "", err
		}
		switch c {
		case '\n':
			return sb.String(), nil
		case '\r':
			if next, err := br.Peek(1); err == nil && next[0] == '\n' {
				_, _ = br.ReadByte()
			}
			return sb.String(), nil
		default:
			sb.WriteByte(c)
		}
	}
}

// parseIntJS mimics JavaScript's parseInt(value, 10): optional leading
// whitespace and sign, then the longest run of digits.
func parseIntJS(s string) (int, bool) {
	s = strings.TrimLeft(s, " \t\n\r\f\v")
	end := 0
	if end < len(s) && (s[end] == '+' || s[end] == '-') {
		end++
	}
	start := end
	for end < len(s) && s[end] >= '0' && s[end] <= '9' {
		end++
	}
	if end == start {
		return 0, false
	}
	n, err := strconv.Atoi(s[:end])
	return n, err == nil
}
