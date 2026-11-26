package scale

import (
	"math"
	"strconv"
	"strings"
)

// Minus is the typographic minus sign (U+2212) used for negative numbers.
// It is the same width as a plus sign, so a column of signed values lines
// up.
const Minus = "−"

// compactUnits are the suffixes used by FormatCompact, largest first.
var compactUnits = []struct {
	div    float64
	suffix string
}{
	{1e12, "T"},
	{1e9, "B"},
	{1e6, "M"},
	{1e3, "K"},
}

// FormatNumber formats v with a fixed number of decimals, comma thousands
// separators and a typographic minus sign: FormatNumber(-1234.5, 1) is
// "−1,234.5". Negative zero is printed as "0".
func FormatNumber(v float64, decimals int) string {
	if math.IsNaN(v) {
		return "NaN"
	}
	if math.IsInf(v, 0) {
		if v < 0 {
			return Minus + "∞"
		}
		return "∞"
	}
	decimals = max(0, min(decimals, 12))
	s := fixed(math.Abs(v), decimals)
	neg := v < 0 && strings.Trim(s, "0.") != ""
	intPart, frac, _ := strings.Cut(s, ".")
	var b strings.Builder
	if neg {
		b.WriteString(Minus)
	}
	b.WriteString(groupThousands(intPart))
	if frac != "" {
		b.WriteByte('.')
		b.WriteString(frac)
	}
	return b.String()
}

// pow10 holds 10^0 through 10^12.
var pow10 = [13]float64{1, 1e1, 1e2, 1e3, 1e4, 1e5, 1e6, 1e7, 1e8, 1e9, 1e10, 1e11, 1e12}

// fixed returns strconv.FormatFloat(a, 'f', decimals, 64) for a finite
// a >= 0. strconv always takes a slow arbitrary-precision path for 'f'
// with a fixed precision, so when it is safe fixed derives the same
// digits from the fast shortest representation instead: if the shortest
// decimal that round-trips to a has at most decimals fractional digits,
// and a·10^decimals < 1e15, the exact binary value lies within
// 0.12·10^-decimals of that decimal, so rounding it to decimals places
// gives the same digits, padded with zeros.
func fixed(a float64, decimals int) string {
	if a*pow10[decimals] < 1e15 {
		s := strconv.FormatFloat(a, 'f', -1, 64)
		_, frac, _ := strings.Cut(s, ".")
		if len(frac) <= decimals {
			if decimals == 0 {
				return s
			}
			if frac == "" {
				s += "."
			}
			return s + strings.Repeat("0", decimals-len(frac))
		}
	}
	return strconv.FormatFloat(a, 'f', decimals, 64)
}

// FormatCompact formats v using a K, M, B or T suffix chosen from v's own
// magnitude: FormatCompact(12500, 1) is "12.5K". Values under 1,000 are
// formatted like FormatNumber. From 1e15 (a thousand trillion) up, where
// a suffix would leave ever longer digit runs ("1,000,000T"), v is
// formatted like FormatExponent instead.
func FormatCompact(v float64, decimals int) string {
	if finite(v) && math.Abs(v) >= exponentAbove {
		return FormatExponent(v, decimals)
	}
	div, suffix := compactUnit(math.Abs(v))
	return FormatNumber(v/div, decimals) + suffix
}

// exponentAbove is the magnitude from which FormatCompact and TickFormat
// switch to scientific notation; exponentBelow is the magnitude under
// which TickFormat does.
const (
	exponentAbove = 1e15
	exponentBelow = 1e-4
)

// FormatExponent formats v in scientific notation with a mantissa of
// decimals fractional digits and a superscript power of ten:
// FormatExponent(1.5e18, 1) is "1.5×10¹⁸" and FormatExponent(-2e-7, 0) is
// "−2×10⁻⁷". Zero is "0", and non-finite values are formatted like
// FormatNumber.
func FormatExponent(v float64, decimals int) string {
	if !finite(v) {
		return FormatNumber(v, 0)
	}
	if v == 0 {
		return "0"
	}
	decimals = max(0, min(decimals, 12))
	exp := int(math.Floor(math.Log10(math.Abs(v))))
	m := v / math.Pow10(exp)
	// Rounding the mantissa can carry it to 10 (9.99 → "10.0").
	if strings.HasPrefix(fixed(math.Abs(m), decimals), "10") {
		exp++
		m = v / math.Pow10(exp)
	}
	return FormatNumber(m, decimals) + powerOfTen(exp)
}

// superscripts holds the superscript digits 0 through 9.
var superscripts = [10]string{"⁰", "¹", "²", "³", "⁴", "⁵", "⁶", "⁷", "⁸", "⁹"}

// powerOfTen returns "×10" followed by exp in superscript digits.
func powerOfTen(exp int) string {
	var b strings.Builder
	b.WriteString("×10")
	if exp < 0 {
		b.WriteString("⁻")
		exp = -exp
	}
	for _, c := range strconv.Itoa(exp) {
		b.WriteString(superscripts[c-'0'])
	}
	return b.String()
}

func compactUnit(magnitude float64) (float64, string) {
	for _, u := range compactUnits {
		if magnitude >= u.div {
			return u.div, u.suffix
		}
	}
	return 1, ""
}

// TickFormat returns a formatter suited to a set of axis ticks. Every tick
// shares one style:
//
//   - If any tick reaches 10,000 in magnitude, values are compact, with a
//     single suffix chosen from the largest tick (0, 5K, 10K, 15K).
//
//   - Otherwise values are printed in full with thousands separators
//     (0, 1,000, 2,000).
//
//   - If the largest tick reaches 1e15, or is under 1e-4, values are in
//     scientific notation with a single power of ten chosen from the
//     largest tick (0, 0.5×10¹⁸, 1.0×10¹⁸), so labels stay short however
//     large or small the data is.
//
// The formatter uses the fewest decimals that represent every tick
// exactly: ticks 0, 0.5, 1 print as "0", "0.5", "1.0" and ticks 0, 0.25,
// 0.5 keep two decimals rather than rounding 0.25 to "0.3". Exactness is
// judged against the spacing of the ticks, so ticks a millionth apart
// never share a label, however large their magnitude.
func TickFormat(ticks []float64) func(float64) string {
	var maxAbs float64
	for _, t := range ticks {
		if finite(t) {
			maxAbs = math.Max(maxAbs, math.Abs(t))
		}
	}
	div, suffix := 1.0, ""
	switch {
	case maxAbs >= exponentAbove || maxAbs > 0 && maxAbs < exponentBelow:
		exp := int(math.Floor(math.Log10(maxAbs)))
		div, suffix = math.Pow10(exp), powerOfTen(exp)
	case maxAbs >= 1e4:
		div, suffix = compactUnit(maxAbs)
	}
	decimals := decimalsFor(ticks, div)
	return func(v float64) string {
		if v == 0 {
			return "0"
		}
		return FormatNumber(v/div, decimals) + suffix
	}
}

// decimalsFor returns the smallest number of decimals (up to 12) at which
// every tick, divided by div, survives a format round trip. A round trip
// survives when it moves the value by less than a thousandth of the
// smallest spacing between consecutive ticks (and by less than 1e-9 of
// the value), so neighbors stay distinguishable. Ticks spaced closer than
// twelve decimals can show (float noise from a domain narrower than float64
// resolves) fall back to the value criterion alone.
func decimalsFor(ticks []float64, div float64) int {
	gap := math.Inf(1)
	prev := math.NaN()
	for _, t := range ticks {
		if !finite(t) {
			continue
		}
		if d := math.Abs(t/div - prev); d > 0 {
			gap = math.Min(gap, d)
		}
		prev = t / div
	}
	if d, ok := fewestDecimals(ticks, div, 1e-3*gap); ok {
		return d
	}
	d, _ := fewestDecimals(ticks, div, math.Inf(1))
	return d
}

// fewestDecimals returns the smallest number of decimals (up to 12) at
// which every finite tick, divided by div, survives a format round trip
// within min(1e-9·max(1, |v|), tol), and false if none does.
func fewestDecimals(ticks []float64, div, tol float64) (int, bool) {
	for d := range 13 {
		ok := true
		for _, t := range ticks {
			if !finite(t) {
				continue
			}
			v := t / div
			s := strconv.FormatFloat(v, 'f', d, 64)
			back, _ := strconv.ParseFloat(s, 64)
			if math.Abs(back-v) > math.Min(1e-9*math.Max(1, math.Abs(v)), tol) {
				ok = false
				break
			}
		}
		if ok {
			return d, true
		}
	}
	return 12, false
}

func groupThousands(digits string) string {
	if len(digits) <= 3 {
		return digits
	}
	var b strings.Builder
	lead := len(digits) % 3
	if lead > 0 {
		b.WriteString(digits[:lead])
	}
	for i := lead; i < len(digits); i += 3 {
		if b.Len() > 0 {
			b.WriteByte(',')
		}
		b.WriteString(digits[i : i+3])
	}
	return b.String()
}
