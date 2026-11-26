// Package svg is a small, allocation-conscious writer for SVG markup.
//
// It guarantees well-formed output: all text and attribute values are
// escaped, characters that are illegal in XML are dropped, and numbers
// are printed deterministically with at most two decimals. Identical
// input always produces byte-identical output, which keeps golden tests
// stable and lets servers hash a chart to detect changes.
package svg

import (
	"math"
	"strconv"
	"unicode/utf8"
)

// Writer accumulates SVG markup in memory.
type Writer struct {
	buf []byte
}

// Bytes returns the markup written so far.
func (w *Writer) Bytes() []byte { return w.buf }

// String returns the markup written so far.
func (w *Writer) String() string { return string(w.buf) }

// Grow ensures room for another n bytes without reallocating.
func (w *Writer) Grow(n int) {
	if n > 0 && cap(w.buf)-len(w.buf) < n {
		buf := make([]byte, len(w.buf), len(w.buf)+n)
		copy(buf, w.buf)
		w.buf = buf
	}
}

// Len returns the number of bytes written.
func (w *Writer) Len() int { return len(w.buf) }

// Attr is one attribute name/value pair. Name must be a valid XML name;
// it is written verbatim. Value is escaped.
type Attr struct {
	Name, Value string
	// keep writes the attribute even when Value is empty.
	keep bool
}

// A builds an attribute.
func A(name, value string) Attr { return Attr{Name: name, Value: value} }

// N builds an attribute with a numeric value formatted by Num.
func N(name string, v float64) Attr { return Attr{Name: name, Value: Num(v)} }

// Flag builds an attribute that is always written with an empty value,
// such as data-hp="". Use it for boolean marker attributes.
func Flag(name string) Attr { return Attr{Name: name, keep: true} }

// Open writes a start tag: <name a="v" ...>. Attributes with an empty
// value are omitted, except those built with Flag.
func (w *Writer) Open(name string, attrs ...Attr) {
	w.tag(name, attrs)
	w.buf = append(w.buf, '>')
}

// Void writes a self-closing element: <name a="v" .../>.
func (w *Writer) Void(name string, attrs ...Attr) {
	w.tag(name, attrs)
	w.buf = append(w.buf, '/', '>')
}

// Close writes an end tag: </name>.
func (w *Writer) Close(name string) {
	w.buf = append(w.buf, '<', '/')
	w.buf = append(w.buf, name...)
	w.buf = append(w.buf, '>')
}

// Elem writes <name attrs>text</name> with text escaped.
func (w *Writer) Elem(name, text string, attrs ...Attr) {
	w.Open(name, attrs...)
	w.Text(text)
	w.Close(name)
}

// Text writes escaped character data.
func (w *Writer) Text(s string) { w.buf = appendEscaped(w.buf, s, false) }

// Raw writes s verbatim. The caller is responsible for well-formedness.
func (w *Writer) Raw(s string) { w.buf = append(w.buf, s...) }

// RawBytes writes b verbatim. The caller is responsible for
// well-formedness.
func (w *Writer) RawBytes(b []byte) { w.buf = append(w.buf, b...) }

func (w *Writer) tag(name string, attrs []Attr) {
	w.buf = append(w.buf, '<')
	w.buf = append(w.buf, name...)
	for _, a := range attrs {
		if a.Value == "" && !a.keep {
			continue
		}
		w.buf = append(w.buf, ' ')
		w.buf = append(w.buf, a.Name...)
		w.buf = append(w.buf, '=', '"')
		w.buf = appendEscaped(w.buf, a.Value, true)
		w.buf = append(w.buf, '"')
	}
}

// Escape returns s escaped for use in text or a double-quoted attribute.
func Escape(s string) string { return string(appendEscaped(nil, s, true)) }

func appendEscaped(dst []byte, s string, attr bool) []byte {
	for i := 0; i < len(s); {
		// Fast path: ASCII that needs no escaping, the common case.
		if c := s[i]; c < utf8.RuneSelf && c >= 0x20 && c != '&' && c != '<' && c != '>' && (c != '"' || !attr) {
			dst = append(dst, c)
			i++
			continue
		}
		r, size := utf8.DecodeRuneInString(s[i:])
		switch {
		case r == '&':
			dst = append(dst, "&amp;"...)
		case r == '<':
			dst = append(dst, "&lt;"...)
		case r == '>':
			dst = append(dst, "&gt;"...)
		case r == '"' && attr:
			dst = append(dst, "&quot;"...)
		case r == '\r' || (r == '\n' || r == '\t') && attr:
			// Preserve whitespace in attributes through normalization. A
			// CR is written as a reference in text too: a parser reads a
			// raw CR as LF, and it would split an SSE data: line, so the
			// output never holds a raw CR byte (docs/spec.md §3.6).
			dst = append(dst, "&#"...)
			dst = strconv.AppendInt(dst, int64(r), 10)
			dst = append(dst, ';')
		case r == utf8.RuneError && size == 1:
			dst = append(dst, "�"...)
		case !xmlChar(r):
			// Drop characters XML 1.0 forbids outright.
		default:
			dst = append(dst, s[i:i+size]...)
		}
		i += size
	}
	return dst
}

// xmlChar reports whether r is allowed by the XML 1.0 Char production.
func xmlChar(r rune) bool {
	return r == 0x09 || r == 0x0A || r == 0x0D ||
		(r >= 0x20 && r <= 0xD7FF) ||
		(r >= 0xE000 && r <= 0xFFFD) ||
		(r >= 0x10000 && r <= 0x10FFFF)
}

// Num formats a coordinate with at most two decimals and no trailing
// zeros: 1.50 → "1.5", 2.00 → "2", -0.001 → "0". Non-finite values
// become "0" so that bad data can never produce invalid markup.
func Num(v float64) string { return string(AppendNum(nil, v)) }

// AppendNum appends the Num formatting of v to dst.
func AppendNum(dst []byte, v float64) []byte {
	if math.IsNaN(v) || math.IsInf(v, 0) {
		return append(dst, '0')
	}
	if n := math.Round(v * 100); math.Abs(n) < 1<<53 {
		return appendCents(dst, int64(n))
	}
	v = math.Round(v*100) / 100
	start := len(dst)
	dst = strconv.AppendFloat(dst, v, 'f', 2, 64)
	// Trim trailing zeros and a dangling decimal point.
	end := len(dst)
	for end > start && dst[end-1] == '0' {
		end--
	}
	if end > start && dst[end-1] == '.' {
		end--
	}
	return dst[:end]
}

// appendCents appends n/100 with at most two decimals and no trailing
// zeros. It is the fast path of AppendNum: exact integer arithmetic gives
// the same digits as strconv's 'f' formatting, which always takes a slow
// arbitrary-precision path.
func appendCents(dst []byte, n int64) []byte {
	if n == 0 {
		return append(dst, '0') // also normalizes -0
	}
	if n < 0 {
		dst = append(dst, '-')
		n = -n
	}
	dst = strconv.AppendInt(dst, n/100, 10)
	switch frac := n % 100; {
	case frac == 0:
	case frac%10 == 0:
		dst = append(dst, '.', byte('0'+frac/10))
	default:
		dst = append(dst, '.', byte('0'+frac/10), byte('0'+frac%10))
	}
	return dst
}
