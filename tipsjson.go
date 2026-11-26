package hyperplot

import (
	"strconv"
	"unicode/utf8"
)

// appendJSON appends the payload as {"mode","xs","bw","top","bottom","rows"}.
func (t tipsAxis) appendJSON(dst []byte) []byte {
	dst = append(dst, `{"mode":`...)
	dst = appendJSONString(dst, t.mode)
	dst = append(dst, `,"xs":[`...)
	for i, x := range t.xs {
		if i > 0 {
			dst = append(dst, ',')
		}
		dst = appendJSONNumber(dst, x)
	}
	dst = append(dst, `],"bw":`...)
	dst = appendJSONNumber(dst, t.bw)
	dst = append(dst, `,"top":`...)
	dst = appendJSONNumber(dst, t.top)
	dst = append(dst, `,"bottom":`...)
	dst = appendJSONNumber(dst, t.bottom)
	dst = append(dst, `,"rows":`...)
	return append(appendRows(dst, t.rows), '}')
}

// appendJSON appends the payload as {"mode":"xy","pts","rows"}.
func (t tipsXY) appendJSON(dst []byte) []byte {
	dst = append(dst, `{"mode":"xy","pts":[`...)
	for i, pt := range t.pts {
		if i > 0 {
			dst = append(dst, ',')
		}
		dst = append(dst, '[')
		dst = appendJSONNumber(dst, pt[0])
		dst = append(dst, ',')
		dst = appendJSONNumber(dst, pt[1])
		dst = append(dst, ']')
	}
	dst = append(dst, `],"rows":`...)
	return append(appendRows(dst, t.rows), '}')
}

func appendRows(dst []byte, rows []tipRow) []byte {
	dst = append(dst, '[')
	for i, r := range rows {
		if i > 0 {
			dst = append(dst, ',')
		}
		dst = append(dst, `{"t":`...)
		dst = appendJSONString(dst, r.t)
		dst = append(dst, `,"k":`...)
		dst = appendJSONString(dst, r.k)
		dst = append(dst, `,"v":[`...)
		for j, e := range r.v {
			if j > 0 {
				dst = append(dst, ',')
			}
			dst = append(dst, '[')
			dst = strconv.AppendInt(dst, int64(e.slot), 10)
			dst = append(dst, ',')
			dst = appendJSONString(dst, e.name)
			dst = append(dst, ',')
			dst = appendJSONString(dst, e.value)
			dst = append(dst, ']')
		}
		dst = append(dst, "]}"...)
	}
	return append(dst, ']')
}

// appendJSONNumber appends a coordinate already rounded by r2. Such
// values are 0 or at least 0.01 and at most maxPixel in magnitude, where
// encoding/json also uses the shortest 'f' form.
func appendJSONNumber(dst []byte, v float64) []byte {
	return strconv.AppendFloat(dst, v, 'f', -1, 64)
}

const hexDigits = "0123456789abcdef"

// appendJSONString appends s as a JSON string, escaped as encoding/json
// escapes it: <, > and & become <, > and &, so the result
// is safe inside HTML and XML text; U+2028 and U+2029 are escaped for
// JavaScript; invalid UTF-8 becomes U+FFFD. In addition, U+FFFE and
// U+FFFF are escaped, so every character of the result is legal in XML.
func appendJSONString(dst []byte, s string) []byte {
	dst = append(dst, '"')
	start := 0
	for i := 0; i < len(s); {
		if b := s[i]; b < utf8.RuneSelf {
			if b >= 0x20 && b != '"' && b != '\\' && b != '<' && b != '>' && b != '&' {
				i++
				continue
			}
			dst = append(dst, s[start:i]...)
			switch b {
			case '\\', '"':
				dst = append(dst, '\\', b)
			case '\b':
				dst = append(dst, '\\', 'b')
			case '\f':
				dst = append(dst, '\\', 'f')
			case '\n':
				dst = append(dst, '\\', 'n')
			case '\r':
				dst = append(dst, '\\', 'r')
			case '\t':
				dst = append(dst, '\\', 't')
			default:
				dst = append(dst, '\\', 'u', '0', '0', hexDigits[b>>4], hexDigits[b&0xF])
			}
			i++
			start = i
			continue
		}
		r, size := utf8.DecodeRuneInString(s[i:])
		switch {
		case r == utf8.RuneError && size == 1:
			dst = append(dst, s[start:i]...)
			dst = append(dst, '\\', 'u', 'f', 'f', 'f', 'd')
		case r == 0x2028 || r == 0x2029:
			dst = append(dst, s[start:i]...)
			dst = append(dst, '\\', 'u', '2', '0', '2', hexDigits[r&0xF])
		case r == 0xFFFE || r == 0xFFFF:
			// Unlike encoding/json, escape the two noncharacters that
			// XML forbids, so the JSON can sit raw in XML text.
			dst = append(dst, s[start:i]...)
			dst = append(dst, '\\', 'u', 'f', 'f', 'f', hexDigits[r&0xF])
		default:
			i += size
			continue
		}
		i += size
		start = i
	}
	dst = append(dst, s[start:]...)
	return append(dst, '"')
}
