package hx

import (
	"bytes"
	"encoding/binary"
	"encoding/hex"
	"hash/fnv"
	"html"
	"io"
	"math"
	"mime"
	"net/http"
	"net/url"
	"strconv"
	"strings"
)

// Request and response header names used by htmx 4 and its extensions.
const (
	// HeaderRequest is set to "true" on every request htmx issues.
	HeaderRequest = "HX-Request"
	// HeaderRequestType is "partial" when the request targets an element
	// and "full" when it targets the body or uses hx-select.
	HeaderRequestType = "HX-Request-Type"
	// HeaderPTag carries the polling tag of the hx-ptag extension, in
	// both directions.
	HeaderPTag = "HX-PTag"
	// HeaderLastEventID is sent by the hx-sse extension (and by
	// EventSource) when it reconnects after receiving an event id.
	HeaderLastEventID = "Last-Event-ID"
)

// Bounds applied by [Size] to client-reported dimensions, in pixels.
const (
	// MinSize is the smallest width or height [Size] returns for a
	// client-reported value. It matches the chart minimum.
	MinSize = 50
	// MaxSize is the largest width or height [Size] returns for a
	// client-reported value.
	MaxSize = 4096
)

// IsRequest reports whether r was issued by htmx, which marks every
// request with the header "HX-Request: true".
func IsRequest(r *http.Request) bool {
	return strings.EqualFold(strings.TrimSpace(r.Header.Get(HeaderRequest)), "true")
}

// IsPartial reports whether r is an htmx request that targets a single
// element ("HX-Request-Type: partial"), as opposed to a full-page request
// (a boosted link, hx-target="body", or hx-select). Handlers use it to
// decide between rendering a fragment and rendering the whole page.
func IsPartial(r *http.Request) bool {
	return IsRequest(r) &&
		strings.EqualFold(strings.TrimSpace(r.Header.Get(HeaderRequestType)), "partial")
}

// Tag returns a stable tag for content b, suitable for the hx-ptag
// extension and for ETags: the 64-bit FNV-1a hash of b as 16 lowercase hex
// digits, prefixed with "hp-" and wrapped in double quotes, for example
// `"hp-1a2b3c4d5e6f7a8b"`.
//
// Equal inputs always give equal tags, across processes and releases. The
// hash is not cryptographic; it detects change, not tampering.
func Tag(b []byte) string {
	h := fnv.New64a()
	_, _ = h.Write(b) // hash.Hash.Write never returns an error
	var sum [8]byte
	binary.BigEndian.PutUint64(sum[:], h.Sum64())
	var out [3 + 1 + 16 + 1]byte // `"hp-` + 16 hex + `"`
	out[0] = '"'
	copy(out[1:], "hp-")
	hex.Encode(out[4:20], sum[:])
	out[20] = '"'
	return string(out[:])
}

// CheckPTag implements the server side of the hx-ptag extension. It sets
// the HX-PTag response header to tag and compares tag with the HX-PTag
// header of the request. When they match, the client already shows this
// content: CheckPTag writes "304 Not Modified" with no body and returns
// true, and htmx skips the swap. Otherwise it returns false and the
// handler writes the response as usual (the header is already set).
//
// The comparison ignores surrounding whitespace, a weak-validator "W/"
// prefix, and one pair of surrounding double quotes on either side, so
// `"hp-1a2b"`, `hp-1a2b` and `W/"hp-1a2b"` all match. This matters
// because the hx-ptag attribute that seeds the first request is often
// written without quotes. An empty tag disables the check: nothing is
// set and CheckPTag returns false.
//
// Call CheckPTag before writing anything else to w.
func CheckPTag(w http.ResponseWriter, r *http.Request, tag string) bool {
	want := normalizeTag(tag)
	if want == "" {
		return false
	}
	w.Header().Set(HeaderPTag, tag)
	got := normalizeTag(r.Header.Get(HeaderPTag))
	if got != want {
		return false
	}
	// A 304 carries no body; drop entity headers a handler may have set.
	h := w.Header()
	h.Del("Content-Type")
	h.Del("Content-Length")
	w.WriteHeader(http.StatusNotModified)
	return true
}

// normalizeTag reduces a tag to its opaque value: trimmed, without a
// weak-validator prefix, and without one pair of surrounding quotes.
func normalizeTag(s string) string {
	s = strings.TrimSpace(s)
	s = strings.TrimPrefix(s, "W/")
	if len(s) >= 2 && s[0] == '"' && s[len(s)-1] == '"' {
		s = s[1 : len(s)-1]
	}
	return s
}

// Partial wraps content in an <hx-partial> element so that one response
// can update several parts of the page:
//
//	<hx-partial hx-target="{target}" hx-swap="{swap}">{content}</hx-partial>
//
// htmx 4 removes each partial from the response and swaps its content
// into the elements matched by target (a CSS selector or extended
// selector such as "#kpi-p99") after the main swap. An empty swap omits
// the attribute, so htmx uses its default swap style; an empty target
// omits hx-target, in which case htmx ignores the partial.
//
// target and swap are escaped. content is inserted as is: it must be
// trusted HTML (such as a rendered chart) and must not itself contain the
// text "</hx-", which htmx would read as the end of the partial.
func Partial(target, swap string, content []byte) []byte {
	var b strings.Builder
	b.Grow(len(content) + len(target) + len(swap) + 48)
	b.WriteString("<hx-partial")
	if target != "" {
		b.WriteString(` hx-target="`)
		b.WriteString(html.EscapeString(target))
		b.WriteByte('"')
	}
	if swap != "" {
		b.WriteString(` hx-swap="`)
		b.WriteString(html.EscapeString(swap))
		b.WriteByte('"')
	}
	b.WriteByte('>')
	b.Write(content)
	b.WriteString("</hx-partial>")
	return []byte(b.String())
}

// maxSizeBody is the largest urlencoded request body that [Size],
// [Selected] and [Brushed] parse themselves. The client extension adds a
// few bytes to a form; anything much larger is an upload the handler
// should bound and parse on its own terms.
const maxSizeBody = 64 << 10

// Size returns the chart size requested by the hx-plot client extension
// (hx-plot.js), which adds parameters to every request whose target is,
// or is inside, an element with the hx-plot:autosize attribute
// (docs/spec.md §4.6, §6.7):
//
//   - "hp-w" ([ParamWidth]) is the element's content width: clientWidth
//     less horizontal padding, rounded to whole CSS pixels.
//   - "hp-h" ([ParamHeight]) is its content height, sent only when the
//     element has hx-plot:autosize="both".
//
// Use the height only for a container whose height CSS sets on its own
// (a height, or an aspect-ratio with overflow hidden). A container without
// one is as tall as the chart inside it, so rendering at its reported
// height grows the chart on every poll. That is why plain hx-plot:autosize
// sends no hp-h; most handlers keep their own height:
//
//	width, _ := hx.Size(r, 640, 0)
//
// See docs/htmx.md#height. Each dimension is read independently. A
// client-reported value is clamped to [MinSize, MaxSize]. A missing,
// empty, non-numeric, non-finite, zero or negative value is ignored and
// the corresponding default (defW or defH) is returned unchanged, so a
// default of 0 can still mean "let the chart decide".
//
// htmx sends the parameters in the query string for GET and in an
// urlencoded body for POST, PUT and PATCH. Size reads r.Form when the
// handler has already parsed it, and otherwise the query string plus an
// urlencoded body of at most 64 KiB, which it parses with
// [http.Request.ParseForm] so the handler can still read the form. Size
// never reads a larger body or a multipart body, so calling it on every
// request cannot make the server buffer or spool an upload; such a body
// is left unread, and the query string alone is used. To take the size
// from a multipart form (hx-encoding="multipart/form-data") or a large
// one, parse it first with limits of your own, for example with
// [http.MaxBytesReader] and [http.Request.ParseMultipartForm].
func Size(r *http.Request, defW, defH float64) (w, h float64) {
	v := formValues(r)
	return sizeParam(v.Get(ParamWidth), defW), sizeParam(v.Get(ParamHeight), defH)
}

// formValues returns the request parameters that [Size], [Selected] and
// [Brushed] read: r.Form if the handler or a small urlencoded body
// populated it, else the query. It returns nil for a nil request.
func formValues(r *http.Request) url.Values {
	if r == nil {
		return nil
	}
	if r.Form == nil && bufferSmallForm(r) {
		// A malformed pair is skipped; whatever parsed is still used.
		_ = r.ParseForm()
	}
	if r.Form != nil {
		return r.Form
	}
	if r.URL == nil {
		return nil
	}
	return r.URL.Query()
}

// bufferSmallForm reports whether r carries an urlencoded body of at most
// maxSizeBody bytes, which ParseForm can then read. It replaces r.Body
// with an in-memory copy of that body. A body of another kind is left
// untouched. An urlencoded body that turns out to be larger, or that
// fails to read, is restored with the bytes already read put back in
// front of the rest, so the handler still sees the whole body.
func bufferSmallForm(r *http.Request) bool {
	switch r.Method {
	case http.MethodPost, http.MethodPut, http.MethodPatch:
	default:
		return false // ParseForm reads no body for other methods
	}
	if r.Body == nil || r.ContentLength > maxSizeBody {
		return false
	}
	ct, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if err != nil || ct != "application/x-www-form-urlencoded" {
		return false
	}
	body := r.Body
	buf, err := io.ReadAll(io.LimitReader(body, maxSizeBody+1))
	if err != nil || len(buf) > maxSizeBody {
		r.Body = readCloser{io.MultiReader(bytes.NewReader(buf), body), body}
		return false
	}
	r.Body = readCloser{bytes.NewReader(buf), body}
	return true
}

// readCloser reads from Reader and closes Closer, the original body.
type readCloser struct {
	io.Reader
	io.Closer
}

func sizeParam(s string, def float64) float64 {
	s = strings.TrimSpace(s)
	if s == "" {
		return def
	}
	v, err := strconv.ParseFloat(s, 64)
	if err != nil || math.IsNaN(v) || math.IsInf(v, 0) || v <= 0 {
		return def
	}
	return math.Min(math.Max(v, MinSize), MaxSize)
}
