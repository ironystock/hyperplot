// Package textwidth estimates the rendered width of text without font
// files or a browser.
//
// A server rendering SVG cannot measure text, yet layout depends on it:
// axis margins, legend wrapping and label collision all need widths. The
// estimates here use per-glyph advance widths of a typical UI sans-serif
// (Helvetica/Arial metrics, which system-ui faces such as Segoe UI and San
// Francisco track closely). They err slightly wide, so layout leaves a
// little room rather than clipping.
package textwidth

import (
	"strings"
	"unicode"
)

// ascii holds advance widths in thousandths of an em for runes 0x20-0x7E.
var ascii = [95]uint16{
	278, 278, 355, 556, 556, 889, 667, 191, 333, 333, 389, 584, 278, 333, 278, 278, // space - /
	556, 556, 556, 556, 556, 556, 556, 556, 556, 556, // 0-9
	278, 278, 584, 584, 584, 556, 1015, // : - @
	667, 667, 722, 722, 667, 611, 778, 722, 278, 500, 667, 556, 833, // A-M
	722, 778, 667, 778, 722, 667, 611, 722, 667, 944, 667, 667, 611, // N-Z
	278, 278, 278, 469, 556, 333, // [ - `
	556, 556, 500, 556, 556, 278, 556, 556, 222, 222, 500, 222, 833, // a-m
	556, 556, 556, 556, 333, 500, 278, 556, 500, 722, 500, 500, 500, // n-z
	334, 260, 334, 584, // { - ~
}

// safety inflates estimates slightly to absorb font differences.
const safety = 1.04

// Width returns the estimated width of s in pixels at the given font size.
func Width(s string, fontSize float64) float64 {
	var em float64
	for _, r := range s {
		em += runeWidth(r)
	}
	return em * fontSize * safety
}

func runeWidth(r rune) float64 {
	switch {
	case r >= 0x20 && r <= 0x7E:
		return float64(ascii[r-0x20]) / 1000
	case r == '−' || r == '–': // minus, en dash
		return 0.584
	case r == '—': // em dash
		return 1
	case r == '…': // ellipsis
		return 1
	case r == ' ' || r == ' ' || r == ' ': // nbsp, thin spaces
		return 0.278
	case unicode.Is(unicode.Mn, r): // combining marks take no space
		return 0
	case isWide(r):
		return 1
	default:
		return 0.6
	}
}

// isWide reports East Asian wide and fullwidth characters, plus emoji,
// which render at roughly one em.
func isWide(r rune) bool {
	return unicode.Is(unicode.Han, r) ||
		unicode.Is(unicode.Hiragana, r) ||
		unicode.Is(unicode.Katakana, r) ||
		unicode.Is(unicode.Hangul, r) ||
		(r >= 0xFF01 && r <= 0xFF60) || // fullwidth forms
		(r >= 0x1F300 && r <= 0x1FAFF) // emoji & pictographs
}

// Truncate shortens s with a trailing ellipsis so that its estimated width
// fits within maxWidth. It returns s unchanged if it already fits. Spaces
// before the ellipsis are dropped ("6:06…", not "6:06 …").
func Truncate(s string, fontSize, maxWidth float64) string {
	if Width(s, fontSize) <= maxWidth {
		return s
	}
	ell := runeWidth('…') * fontSize * safety
	var w float64
	for i, r := range s {
		w += runeWidth(r) * fontSize * safety
		if w+ell > maxWidth {
			return strings.TrimRightFunc(s[:i], unicode.IsSpace) + "…"
		}
	}
	return s
}
