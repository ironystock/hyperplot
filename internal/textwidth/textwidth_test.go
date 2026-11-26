package textwidth

import (
	"fmt"
	"math"
	"strings"
	"testing"
	"unicode/utf8"
)

func TestWidth(t *testing.T) {
	tests := []struct {
		name     string
		s        string
		fontSize float64
		want     float64
	}{
		{"empty", "", 12, 0},
		{"digit", "0", 10, 0.556 * 10 * safety},
		{"digits are tabular", "1234", 12, 4 * 0.556 * 12 * safety},
		{"scales with font size", "M", 20, 0.833 * 20 * safety},
		{"space", " ", 10, 0.278 * 10 * safety},
		{"typographic minus matches plus", "−", 10, 0.584 * 10 * safety},
		{"en dash", "–", 10, 0.584 * 10 * safety},
		{"ellipsis", "…", 10, 1 * 10 * safety},
		{"han is one em", "中", 10, 1 * 10 * safety},
		{"emoji is one em", "\U0001F600", 10, 1 * 10 * safety},
		{"combining mark is zero", "é", 10, 0.556 * 10 * safety},
		{"other runes default", "é", 10, 0.6 * 10 * safety},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := Width(tt.s, tt.fontSize); math.Abs(got-tt.want) > 1e-9 {
				t.Errorf("Width(%q, %v) = %v, want %v", tt.s, tt.fontSize, got, tt.want)
			}
		})
	}
}

func TestWidthOrdering(t *testing.T) {
	// Sanity checks on relative widths that layout depends on.
	if Width("iiii", 12) >= Width("MMMM", 12) {
		t.Error("narrow glyphs should measure narrower than wide ones")
	}
	if Width("abc", 12) >= Width("abcd", 12) {
		t.Error("width should grow with length")
	}
}

func TestTruncate(t *testing.T) {
	tests := []struct {
		name     string
		s        string
		maxWidth float64
		want     string
	}{
		{"fits", "short", 100, "short"},
		{"exact fit", "ab", Width("ab", 12), "ab"},
		// The longest prefix that fits 60 px together with the ellipsis.
		{"truncated", "a very long series name", 60, "a very lo…"},
		{"too narrow for anything", "hello", 1, "…"},
		{"empty", "", 0, ""},
		{"multibyte stays valid", "日本語のシリーズ名", 40, "日本…"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := Truncate(tt.s, 12, tt.maxWidth)
			if got != tt.want {
				t.Errorf("Truncate(%q, 12, %v) = %q, want %q", tt.s, tt.maxWidth, got, tt.want)
			}
			if err := checkTruncate(tt.s, tt.maxWidth, got); err != "" {
				t.Error(err)
			}
		})
	}
}

// checkTruncate returns a description of how got = Truncate(s, 12,
// maxWidth) breaks Truncate's contract, or "" if it keeps it: s comes
// back unchanged exactly when it fits (or is already a lone ellipsis);
// otherwise the result is a prefix of s plus an ellipsis, no wider than
// maxWidth (unless even the ellipsis alone is too wide), and valid UTF-8
// when s is.
func checkTruncate(s string, maxWidth float64, got string) string {
	fits := Width(s, 12) <= maxWidth
	switch {
	case math.IsNaN(maxWidth):
		return "" // nothing fits a NaN width, and nothing exceeds it
	case s == "" && got != "":
		return fmt.Sprintf("Truncate(%q, %v) = %q", s, maxWidth, got)
	case s == "":
		return "" // nothing to shorten, even below a zero width
	case fits && got != s:
		return fmt.Sprintf("Truncate(%q, %v) = %q, but the input fits", s, maxWidth, got)
	case fits:
		return ""
	case got == s && s != "…":
		// A lone ellipsis is already the shortest result Truncate can
		// give, so it comes back unchanged even when it does not fit.
		return fmt.Sprintf("Truncate(%q, %v) returned its input, %v wide", s, maxWidth, Width(s, 12))
	case !strings.HasSuffix(got, "…"):
		return fmt.Sprintf("Truncate(%q, %v) = %q lacks an ellipsis", s, maxWidth, got)
	case !strings.HasPrefix(s, strings.TrimSuffix(got, "…")):
		return fmt.Sprintf("Truncate(%q, %v) = %q is not a prefix", s, maxWidth, got)
	case got != "…" && Width(got, 12) > maxWidth+1e-9:
		return fmt.Sprintf("Truncate(%q, %v) = %q is %v wide", s, maxWidth, got, Width(got, 12))
	case utf8.ValidString(s) && !utf8.ValidString(got):
		return fmt.Sprintf("Truncate(%q, %v) = %q is not valid UTF-8", s, maxWidth, got)
	}
	return ""
}

func FuzzTruncate(f *testing.F) {
	f.Add("hello world", 30.0)
	f.Add("日本語", 5.0)
	f.Add("\xff\xfe", 3.0)
	f.Add("a very long series name", 60.0)
	f.Add("", -36.0)
	f.Fuzz(func(t *testing.T, s string, maxWidth float64) {
		if err := checkTruncate(s, maxWidth, Truncate(s, 12, maxWidth)); err != "" {
			t.Fatal(err)
		}
	})
}

func BenchmarkWidth(b *testing.B) {
	for b.Loop() {
		Width("Requests per second (p99)", 12)
	}
}
