package scale

import (
	"math"
	"slices"
	"strconv"
	"testing"
	"time"
	_ "time/tzdata" // the DST tests must run, not skip, where the OS has no zoneinfo
	"unicode/utf8"
)

// newYork returns America/New_York, from the embedded tzdata if the
// system has none.
func newYork(tb testing.TB) *time.Location {
	tb.Helper()
	ny, err := time.LoadLocation("America/New_York")
	if err != nil {
		tb.Fatal("America/New_York:", err)
	}
	return ny
}

func TestTicks(t *testing.T) {
	tests := []struct {
		start, stop float64
		count       int
		want        []float64
	}{
		{0, 1, 10, []float64{0, 0.1, 0.2, 0.3, 0.4, 0.5, 0.6, 0.7, 0.8, 0.9, 1}},
		{0, 1, 5, []float64{0, 0.2, 0.4, 0.6, 0.8, 1}},
		{0, 10, 5, []float64{0, 2, 4, 6, 8, 10}},
		{0, 100, 4, []float64{0, 20, 40, 60, 80, 100}},
		{-1, 1, 4, []float64{-1, -0.5, 0, 0.5, 1}},
		{1, 0, 5, []float64{1, 0.8, 0.6, 0.4, 0.2, 0}},
		{0.13, 0.97, 5, []float64{0.2, 0.4, 0.6, 0.8}},
		{5, 5, 5, []float64{5}},
		{0, 1, 0, nil},
		{math.NaN(), 1, 5, nil},
	}
	for _, tt := range tests {
		got := Ticks(tt.start, tt.stop, tt.count)
		if !slices.Equal(got, tt.want) {
			t.Errorf("Ticks(%v, %v, %d) = %v, want %v", tt.start, tt.stop, tt.count, got, tt.want)
		}
	}
}

// TestFixedMatchesStrconv checks fixed's fast path against strconv for
// values with short representations, exact halves and long fractions.
func TestFixedMatchesStrconv(t *testing.T) {
	vals := []float64{0, 0.5, 0.125, 0.375, 2.5, 1.005, 2.675, 0.1, 0.3, 12.345, 999.995, 1e14, 123456789.125, 1e15, 9.999999e14, 5e-324}
	for i := range 3000 {
		vals = append(vals, float64(i)/8, float64(i)*0.01, float64(i)*1.37, math.Sqrt(float64(i)), float64(i)*1e9+0.5)
	}
	for _, v := range vals {
		for d := range 13 {
			if got, want := fixed(v, d), strconv.FormatFloat(v, 'f', d, 64); got != want {
				t.Errorf("fixed(%v, %d) = %q, want %q", v, d, got, want)
			}
		}
	}
}

func FuzzFixed(f *testing.F) {
	f.Add(0.125, uint8(2))
	f.Add(1234.5, uint8(0))
	f.Fuzz(func(t *testing.T, v float64, d uint8) {
		if !finite(v) {
			return
		}
		v, dec := math.Abs(v), int(d%13)
		if got, want := fixed(v, dec), strconv.FormatFloat(v, 'f', dec, 64); got != want {
			t.Fatalf("fixed(%v, %d) = %q, want %q", v, dec, got, want)
		}
	})
}

func TestTicksExtremeSpans(t *testing.T) {
	// None of these may panic or return non-finite ticks.
	tests := []struct {
		start, stop float64
		count       int
	}{
		{-math.MaxFloat64, math.MaxFloat64, 5},
		{0, math.MaxFloat64, 10},
		{5e-324, 1e-323, 5},
		{0, 5e-324, 3},
		{0, 1, math.MaxInt32},
	}
	for _, tt := range tests {
		got := Ticks(tt.start, tt.stop, tt.count)
		if len(got) > maxTicks+1 {
			t.Errorf("Ticks(%v, %v, %d) returned %d ticks", tt.start, tt.stop, tt.count, len(got))
		}
		for _, v := range got {
			if !finite(v) {
				t.Errorf("Ticks(%v, %v, %d) contains %v", tt.start, tt.stop, tt.count, v)
			}
		}
	}
}

func TestTicksExactDecimals(t *testing.T) {
	// Ticks must not accumulate floating-point error (0.30000000000000004).
	if got := Ticks(0, 0.3, 3); !slices.Equal(got, []float64{0, 0.1, 0.2, 0.3}) {
		t.Errorf("Ticks(0, 0.3, 3) = %v", got)
	}
}

func TestNice(t *testing.T) {
	tests := []struct {
		d0, d1    float64
		count     int
		want0, w1 float64
	}{
		{0.13, 0.97, 10, 0.1, 1},
		{3, 97, 5, 0, 100},
		{-7.3, 42.1, 5, -10, 50},
		{97, 3, 5, 100, 0},
		{0, 0, 5, 0, 0},
		{1234, 98765, 5, 0, 100000},
	}
	for _, tt := range tests {
		s := NewLinear(tt.d0, tt.d1, 0, 1).Nice(tt.count)
		if s.Domain != [2]float64{tt.want0, tt.w1} {
			t.Errorf("Nice(%v..%v, %d) = %v, want [%v %v]", tt.d0, tt.d1, tt.count, s.Domain, tt.want0, tt.w1)
		}
	}
}

func TestLinearMapInvert(t *testing.T) {
	s := NewLinear(0, 100, 300, 0) // vertical axis: larger values higher
	if got := s.Map(25); got != 225 {
		t.Errorf("Map(25) = %v, want 225", got)
	}
	if got := s.Invert(225); got != 25 {
		t.Errorf("Invert(225) = %v, want 25", got)
	}
	if got := NewLinear(5, 5, 0, 100).Map(5); got != 50 {
		t.Errorf("degenerate domain Map = %v, want 50", got)
	}
	s.Clamp = true
	if got := s.Map(200); got != 0 {
		t.Errorf("clamped Map(200) = %v, want 0", got)
	}
}

func TestTickFormat(t *testing.T) {
	tests := []struct {
		ticks []float64
		want  []string
	}{
		{[]float64{0, 1000, 2000, 3000}, []string{"0", "1,000", "2,000", "3,000"}},
		{[]float64{0, 5000, 10000, 15000}, []string{"0", "5K", "10K", "15K"}},
		{[]float64{0, 2500, 5000, 7500, 10000}, []string{"0", "2.5K", "5.0K", "7.5K", "10.0K"}},
		{[]float64{0, 0.25, 0.5}, []string{"0", "0.25", "0.50"}},
		{[]float64{-1, -0.5, 0, 0.5, 1}, []string{"−1.0", "−0.5", "0", "0.5", "1.0"}},
		{[]float64{0, 2e6, 4e6}, []string{"0", "2M", "4M"}},
		{[]float64{0, 2e9}, []string{"0", "2B"}},
		{[]float64{0, 1e18, 2e18}, []string{"0", "1×10¹⁸", "2×10¹⁸"}},
		{[]float64{0, 5e17, 1e18}, []string{"0", "0.5×10¹⁸", "1.0×10¹⁸"}},
		{[]float64{1e-10, 1.5e-10, 2e-10}, []string{"1.0×10⁻¹⁰", "1.5×10⁻¹⁰", "2.0×10⁻¹⁰"}},
		{[]float64{-4e-5, 0, 4e-5}, []string{"−4×10⁻⁵", "0", "4×10⁻⁵"}},
		{[]float64{0, 5e-5, 1e-4}, []string{"0", "0.00005", "0.00010"}},
	}
	for _, tt := range tests {
		f := TickFormat(tt.ticks)
		for i, v := range tt.ticks {
			if got := f(v); got != tt.want[i] {
				t.Errorf("TickFormat(%v)(%v) = %q, want %q", tt.ticks, v, got, tt.want[i])
			}
		}
	}
}

// TestTickFormatDistinct checks that generated ticks of any magnitude or
// spacing get distinct, short labels.
func TestTickFormatDistinct(t *testing.T) {
	domains := [][2]float64{
		{1e-10, 5e-10},
		{-3e-20, 7e-20},
		{1e-7, 5e-7},
		{1e18, 3e18},
		{-1e300, 1e300},
		{1000, 1000 + 1e-6},
		{1e9, 1e9 + 0.01},
		{-2, 2},
	}
	for _, d := range domains {
		ticks := Ticks(d[0], d[1], 8)
		if len(ticks) < 2 {
			t.Fatalf("Ticks(%v, %v) = %v", d[0], d[1], ticks)
		}
		f := TickFormat(ticks)
		seen := map[string]float64{}
		for _, v := range ticks {
			s := f(v)
			if prev, dup := seen[s]; dup {
				t.Errorf("domain %v: ticks %v and %v both read %q", d, prev, v, s)
			}
			seen[s] = v
			if n := utf8.RuneCountInString(s); n > 16 {
				t.Errorf("domain %v: label %q is %d runes long", d, s, n)
			}
		}
	}
}

func TestFormatExponent(t *testing.T) {
	tests := []struct {
		v    float64
		d    int
		want string
	}{
		{1.5e18, 1, "1.5×10¹⁸"},
		{-2e-7, 0, "−2×10⁻⁷"},
		{9.96e15, 1, "1.0×10¹⁶"},
		{1e300, 0, "1×10³⁰⁰"},
		{0, 2, "0"},
		{math.Inf(-1), 2, "−∞"},
	}
	for _, tt := range tests {
		if got := FormatExponent(tt.v, tt.d); got != tt.want {
			t.Errorf("FormatExponent(%v, %d) = %q, want %q", tt.v, tt.d, got, tt.want)
		}
	}
	if got := FormatCompact(3e18, 1); got != "3.0×10¹⁸" {
		t.Errorf("FormatCompact(3e18, 1) = %q", got)
	}
	if got := FormatCompact(999e12, 0); got != "999T" {
		t.Errorf("FormatCompact(999e12, 0) = %q", got)
	}
}

func TestFormatNumber(t *testing.T) {
	tests := []struct {
		v    float64
		d    int
		want string
	}{
		{1234567.891, 2, "1,234,567.89"},
		{-1234.5, 1, "−1,234.5"},
		{-0.0001, 2, "0.00"},
		{999, 0, "999"},
		{math.Inf(1), 0, "∞"},
		{math.NaN(), 0, "NaN"},
	}
	for _, tt := range tests {
		if got := FormatNumber(tt.v, tt.d); got != tt.want {
			t.Errorf("FormatNumber(%v, %d) = %q, want %q", tt.v, tt.d, got, tt.want)
		}
	}
	if got := FormatCompact(12500, 1); got != "12.5K" {
		t.Errorf("FormatCompact = %q", got)
	}
}

func TestBand(t *testing.T) {
	s := Band{Domain: []string{"a", "b", "c", "d"}, Range: [2]float64{0, 100}}
	if got := s.Bandwidth(); got != 25 {
		t.Errorf("Bandwidth = %v, want 25", got)
	}
	if x, ok := s.Map("c"); !ok || x != 50 {
		t.Errorf("Map(c) = %v, %v", x, ok)
	}
	if _, ok := s.Map("z"); ok {
		t.Error("Map(z) should report false")
	}
	s = NewBand([]string{"a", "b"}, 0, 100)
	x0, _ := s.Map("a")
	x1, _ := s.Map("b")
	bw := s.Bandwidth()
	if x0 <= 0 || x1+bw >= 100 || x1-x0 != s.Step() {
		t.Errorf("padded band layout wrong: a=%v b=%v bw=%v step=%v", x0, x1, bw, s.Step())
	}
	// Outer gaps must be symmetric when centered.
	if left, right := x0, 100-(x1+bw); math.Abs(left-right) > 1e-9 {
		t.Errorf("asymmetric outer padding: %v vs %v", left, right)
	}
}

func TestTimeTicks(t *testing.T) {
	t0 := time.Date(2026, 3, 3, 0, 0, 0, 0, time.UTC)
	tests := []struct {
		name  string
		span  time.Duration
		count int
		first string
		want  int
	}{
		{"hourly over a day", 24 * time.Hour, 8, "2026-03-03T00:00:00Z", 9},
		{"15m over 2h", 2 * time.Hour, 8, "2026-03-03T00:00:00Z", 9},
		{"months over a year", 365 * 24 * time.Hour, 4, "2026-04-01T00:00:00Z", 4},
	}
	for _, tt := range tests {
		s := NewTime(t0, t0.Add(tt.span), 0, 600)
		ticks := s.Ticks(tt.count)
		if len(ticks) == 0 || ticks[0].Format(time.RFC3339) != tt.first {
			t.Errorf("%s: first tick = %v, want %s", tt.name, ticks, tt.first)
			continue
		}
		if len(ticks) != tt.want {
			t.Errorf("%s: got %d ticks %v, want %d", tt.name, len(ticks), ticks, tt.want)
		}
		for _, tk := range ticks {
			if tk.Before(t0) || tk.After(t0.Add(tt.span)) {
				t.Errorf("%s: tick %v outside domain", tt.name, tk)
			}
		}
	}
}

func TestTimeTicksDST(t *testing.T) {
	ny := newYork(t)
	// 2026-03-08 is the US spring-forward day (a 23-hour day).
	t0 := time.Date(2026, 3, 6, 0, 0, 0, 0, ny)
	s := Time{Domain: [2]time.Time{t0, t0.AddDate(0, 0, 5)}, Range: [2]float64{0, 500}, Location: ny}
	var want []time.Time
	for d := 6; d <= 11; d++ {
		want = append(want, time.Date(2026, 3, d, 0, 0, 0, 0, ny))
	}
	if got := s.Ticks(5); !slices.EqualFunc(got, want, time.Time.Equal) {
		t.Errorf("daily ticks across spring-forward:\n got %v\nwant %v (every local midnight)", got, want)
	}
}

// TestTimeTicksFallBackSubHour checks ticks whose domain starts in the
// local hour that repeats when clocks fall back. Rebuilding the floor
// from wall-clock fields resolved 1:40 AM to its first (EDT) occurrence,
// an hour early, so every millisecond, second and minute tick started an
// hour before the domain (up to the 10,000-tick cap).
func TestTimeTicksFallBackSubHour(t *testing.T) {
	ny := newYork(t)
	t0 := time.Date(2026, 11, 1, 6, 40, 0, 0, time.UTC) // 1:40 AM EST, the second 1 AM
	tests := []struct {
		name  string
		span  time.Duration
		count int
		unit  timeUnit
	}{
		{"milliseconds", 2 * time.Second, 7, unitMillisecond},
		{"seconds", 2 * time.Minute, 7, unitSecond},
		{"minutes", 20 * time.Minute, 7, unitMinute},
		{"half hours", 3 * time.Hour, 7, unitMinute},
		{"hours", 6 * time.Hour, 6, unitHour},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t1 := t0.Add(tt.span)
			s := Time{Domain: [2]time.Time{t0, t1}, Range: [2]float64{0, 600}, Location: ny}
			if iv := s.tickInterval(tt.count); iv.unit != tt.unit {
				t.Fatalf("tickInterval(%d) = %+v, want unit %d", tt.count, iv, tt.unit)
			}
			ticks := s.Ticks(tt.count)
			if len(ticks) < 2 || len(ticks) > 3*tt.count {
				t.Fatalf("got %d ticks %v, want about %d", len(ticks), ticks, tt.count)
			}
			for i, tk := range ticks {
				if tk.Before(t0) || tk.After(t1) {
					t.Errorf("tick %v is outside [%v, %v]", tk, t0.In(ny), t1.In(ny))
				}
				if i > 0 && !tk.After(ticks[i-1]) {
					t.Errorf("tick %v does not follow %v", tk, ticks[i-1])
				}
			}
			// New York's offset is a whole number of hours, so its ticks
			// are the instants UTC ticks would be.
			utc := Time{Domain: s.Domain, Range: s.Range}
			if want := utc.Ticks(tt.count); !slices.EqualFunc(ticks, want, time.Time.Equal) {
				t.Errorf("ticks %v, want the UTC instants %v", ticks, want)
			}
		})
	}
}

// TestTimeSubMillisecond checks domains shorter than a millisecond: they
// get ticks, and points a microsecond or 100 ns apart are mapped evenly.
// Mapping float64 Unix seconds (about 240 ns resolution in 2026) gave no
// ticks and collapsed 100 ns data into a few columns.
func TestTimeSubMillisecond(t *testing.T) {
	t0 := time.Date(2026, 3, 3, 14, 0, 0, 123_456_000, time.UTC)
	for _, step := range []time.Duration{50 * time.Microsecond, time.Microsecond, 100, 1} {
		t.Run(step.String(), func(t *testing.T) {
			t1 := t0.Add(9 * step)
			s := NewTime(t0, t1, 0, 588)
			ticks := s.Ticks(8)
			if len(ticks) < 2 {
				t.Fatalf("got %d ticks %v, want at least 2", len(ticks), ticks)
			}
			f := s.TickFormatFor(8)
			seen := map[string]bool{}
			for _, tk := range ticks {
				if tk.Before(t0) || tk.After(t1) {
					t.Errorf("tick %v outside the domain", tk)
				}
				if l := f(tk); seen[l] {
					t.Errorf("tick label %q repeats", l)
				} else {
					seen[l] = true
				}
			}
			for i := 1; i <= 9; i++ {
				gap := s.Map(t0.Add(time.Duration(i)*step)) - s.Map(t0.Add(time.Duration(i-1)*step))
				if math.Abs(gap-588.0/9) > 1e-6 {
					t.Errorf("gap %d = %v px, want %v", i, gap, 588.0/9)
				}
			}
			if got := s.Invert(s.Map(t0.Add(4 * step))); !got.Equal(t0.Add(4 * step)) {
				t.Errorf("Invert(Map(t)) = %v, want %v", got, t0.Add(4*step))
			}
		})
	}
	s := NewTime(t0, t0.Add(450*time.Microsecond), 0, 600)
	ff := s.Formatter(8)
	if got := ff(t0.Add(50 * time.Microsecond)); got != "Mar 3, 2026 2:00:00.123506 PM" {
		t.Errorf("Formatter at 50 µs ticks = %q, want microseconds", got)
	}
}

// TestTimeTicksTwoDayAcrossMonths slides a 14-day window over a month end.
// 2-day ticks must depend only on the calendar (1, 3, 5, … of each month),
// so a tick date shared by two windows is a tick in both.
func TestTimeTicksTwoDayAcrossMonths(t *testing.T) {
	var prev []time.Time
	for start := time.Date(2026, 1, 25, 0, 0, 0, 0, time.UTC); start.Before(time.Date(2026, 2, 6, 0, 0, 0, 0, time.UTC)); start = start.AddDate(0, 0, 1) {
		s := NewTime(start, start.AddDate(0, 0, 14), 0, 640)
		if iv := s.tickInterval(7); iv.unit != unitDay || iv.step != 2 {
			t.Fatalf("tickInterval(7) = %+v, want 2-day steps", iv)
		}
		ticks := s.Ticks(7)
		for _, tk := range ticks {
			if (tk.Day()-1)%2 != 0 {
				t.Errorf("start %s: tick %s is not on an odd day of the month", start.Format("Jan 2"), tk.Format("Jan 2"))
			}
		}
		// Every tick of the previous window inside this one is still a tick.
		for _, tk := range prev {
			if !tk.Before(start) && !slices.ContainsFunc(ticks, tk.Equal) {
				t.Errorf("start %s: tick %s vanished as the window slid", start.Format("Jan 2"), tk.Format("Jan 2"))
			}
		}
		prev = ticks
	}
	feb := NewTime(time.Date(2026, 1, 30, 0, 0, 0, 0, time.UTC), time.Date(2026, 2, 13, 0, 0, 0, 0, time.UTC), 0, 640)
	if !slices.ContainsFunc(feb.Ticks(7), time.Date(2026, 2, 1, 0, 0, 0, 0, time.UTC).Equal) {
		t.Errorf("ticks %v skip Feb 1", feb.Ticks(7))
	}
}

// TestTimeZeroSpanPrecision checks that a single instant (the first sample
// of a live chart) is labeled at the precision its time needs, not at
// milliseconds.
func TestTimeZeroSpanPrecision(t *testing.T) {
	tests := []struct {
		t    time.Time
		want string
	}{
		{time.Date(2026, 3, 3, 14, 0, 0, 0, time.UTC), "Mar 3, 2026 2:00 PM"},
		{time.Date(2026, 3, 3, 14, 0, 5, 0, time.UTC), "Mar 3, 2026 2:00:05 PM"},
		{time.Date(2026, 3, 3, 0, 0, 0, 0, time.UTC), "Mar 3, 2026"},
		{time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC), "Jan 1, 2026"},
		{time.Date(2026, 3, 3, 14, 0, 0, 1000, time.UTC), "Mar 3, 2026 2:00:00.000001 PM"},
	}
	for _, tt := range tests {
		s := NewTime(tt.t, tt.t, 0, 600)
		if got := s.Format(tt.t, 8); got != tt.want {
			t.Errorf("Format(%v) on a zero-span domain = %q, want %q", tt.t, got, tt.want)
		}
		if got := s.FormatterFor(8, slices.Values([]time.Time{tt.t}))(tt.t); got != tt.want {
			t.Errorf("FormatterFor(%v) on a zero-span domain = %q, want %q", tt.t, got, tt.want)
		}
	}
}

// FuzzTimeTicks checks tick invariants over arbitrary domains in zones
// with daylight-saving changes, including Lord Howe's 30-minute shift:
// ticks are strictly increasing, inside the domain, and bounded in number.
func FuzzTimeTicks(f *testing.F) {
	zones := []string{"UTC", "America/New_York", "Europe/London", "Australia/Lord_Howe", "Asia/Kolkata", "America/Sao_Paulo"}
	locs := make([]*time.Location, len(zones))
	for i, z := range zones {
		loc, err := time.LoadLocation(z)
		if err != nil {
			f.Fatal(z, err)
		}
		locs[i] = loc
	}
	nov1 := time.Date(2026, 11, 1, 6, 40, 0, 0, time.UTC).UnixNano()
	mar8 := time.Date(2026, 3, 8, 6, 30, 0, 0, time.UTC).UnixNano()
	lordHowe := time.Date(2026, 4, 4, 14, 45, 0, 0, time.UTC).UnixNano()
	for _, seed := range []struct {
		start, span int64
		zone, count uint8
	}{
		{nov1, int64(2 * time.Second), 1, 7},
		{nov1, int64(20 * time.Minute), 1, 7},
		{nov1, int64(3 * time.Hour), 1, 7},
		{mar8, int64(72 * time.Hour), 1, 12},
		{lordHowe, int64(4 * time.Hour), 3, 8},
		{lordHowe, int64(90 * time.Minute), 3, 6},
		{nov1, 450_000, 0, 8},
		{nov1, int64(14 * 24 * time.Hour), 2, 7},
	} {
		f.Add(seed.start, seed.span, seed.zone, seed.count)
	}
	f.Fuzz(func(t *testing.T, start, span int64, zone, count uint8) {
		t0 := time.Unix(0, start)
		t1 := t0.Add(time.Duration(span))
		s := Time{Domain: [2]time.Time{t0, t1}, Range: [2]float64{0, 600}, Location: locs[int(zone)%len(locs)]}
		if t1.Before(t0) {
			t0, t1 = t1, t0
		}
		n := int(count%50) + 1
		ticks := s.Ticks(n)
		if len(ticks) > 10_000 {
			t.Fatalf("%v..%v in %v: %d ticks", t0, t1, s.Location, len(ticks))
		}
		for i, tk := range ticks {
			if tk.Before(t0) || tk.After(t1) {
				t.Fatalf("%v..%v in %v, count %d: tick %v outside the domain", t0, t1, s.Location, n, tk)
			}
			if i > 0 && !tk.After(ticks[i-1]) {
				t.Fatalf("%v..%v in %v, count %d: tick %v does not follow %v", t0, t1, s.Location, n, tk, ticks[i-1])
			}
			if px := s.Map(tk); px < -1e-6 || px > 600+1e-6 {
				t.Fatalf("tick %v maps to %v, outside the range", tk, px)
			}
		}
		_ = s.FormatterFor(n, slices.Values(ticks))
		_ = s.TickFormatFor(n)(t0)
	})
}

// TestTimeTicksDSTHours checks that hour ticks stay on the local hour grid
// (and keep landing on local midnight, which carries the date label) when
// a daylight-saving change falls inside the domain.
func TestTimeTicksDSTHours(t *testing.T) {
	ny := newYork(t)
	starts := map[string]time.Time{
		"spring forward": time.Date(2026, 3, 7, 0, 0, 0, 0, ny),
		"fall back":      time.Date(2026, 10, 31, 0, 0, 0, 0, ny),
	}
	for name, t0 := range starts {
		for _, step := range []int{1, 3, 6, 12} {
			t.Run(name+"/"+strconv.Itoa(step)+"h", func(t *testing.T) {
				t1 := t0.Add(72 * time.Hour)
				s := Time{Domain: [2]time.Time{t0, t1}, Range: [2]float64{0, 600}, Location: ny}
				count := 72 / step
				if iv := s.tickInterval(count); iv.unit != unitHour || iv.step != step {
					t.Fatalf("tickInterval(%d) = %+v, want %d-hour steps", count, iv, step)
				}
				ticks := s.Ticks(count)
				var prev time.Time
				for i, tk := range ticks {
					if h, m, sec := tk.Clock(); h%step != 0 || m != 0 || sec != 0 {
						t.Errorf("tick %v is off the %d-hour local grid", tk, step)
					}
					if i > 0 && !tk.After(prev) {
						t.Errorf("tick %v does not follow %v", tk, prev)
					}
					prev = tk
				}
				f := s.TickFormatFor(count)
				for d := t0; !d.After(t1); d = d.AddDate(0, 0, 1) {
					want := f(d)
					if !slices.ContainsFunc(ticks, func(tk time.Time) bool { return tk.Equal(d) }) {
						t.Errorf("no tick at local midnight %v (%q)", d, want)
					}
				}
			})
		}
	}
	// Hourly ticks show the repeated 1 AM hour twice when clocks fall back.
	nov1 := time.Date(2026, 11, 1, 0, 0, 0, 0, ny)
	s := Time{Domain: [2]time.Time{nov1, nov1.AddDate(0, 0, 1)}, Range: [2]float64{0, 600}, Location: ny}
	if got := len(s.Ticks(24)); got != 26 {
		t.Errorf("hourly ticks over the 25-hour Nov 1: got %d, want 26", got)
	}
}

func TestTimeTicksSubSecond(t *testing.T) {
	t0 := time.Date(2026, 3, 3, 14, 0, 0, 0, time.UTC)
	s := NewTime(t0, t0.Add(400*time.Millisecond), 0, 600)
	ticks := s.Ticks(5)
	if len(ticks) != 5 {
		t.Fatalf("got %d ticks %v, want 5 (every 100 ms)", len(ticks), ticks)
	}
	f := s.TickFormatFor(5)
	var labels []string
	for _, tk := range ticks {
		labels = append(labels, f(tk))
	}
	if want := []string{"2 PM", ".100", ".200", ".300", ".400"}; !slices.Equal(labels, want) {
		t.Errorf("labels = %q, want %q", labels, want)
	}
	// Times 100 ms apart must print apart in tooltips and tables.
	times := []time.Time{t0, t0.Add(100 * time.Millisecond), t0.Add(200 * time.Millisecond)}
	ff := s.FormatterFor(5, slices.Values(times))
	if a, b := ff(times[1]), ff(times[2]); a == b || a != "Mar 3, 2026 2:00:00.100 PM" {
		t.Errorf("FormatterFor labels %q, %q", a, b)
	}
	if got := s.Format(times[1], 5); got != "Mar 3, 2026 2:00:00.100 PM" {
		t.Errorf("Format = %q", got)
	}
}

func TestTimeTicksLongSpans(t *testing.T) {
	tests := []struct {
		name     string
		t0, t1   time.Time
		maxTicks int
		first    string
	}{
		{"zero time to today", time.Time{}, time.Date(2026, 3, 3, 0, 0, 0, 0, time.UTC), 8, "500"},
		{"ten millennia", time.Date(-5000, 1, 1, 0, 0, 0, 0, time.UTC), time.Date(5000, 1, 1, 0, 0, 0, 0, time.UTC), 8, "-4000"},
		{"two centuries", time.Date(1826, 1, 1, 0, 0, 0, 0, time.UTC), time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC), 8, "1850"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := NewTime(tt.t0, tt.t1, 0, 600)
			ticks := s.Ticks(5)
			if len(ticks) < 2 || len(ticks) > tt.maxTicks {
				t.Fatalf("got %d ticks, want 2..%d: %v", len(ticks), tt.maxTicks, ticks)
			}
			if got := s.TickFormatFor(5)(ticks[0]); got != tt.first {
				t.Errorf("first tick label = %q, want %q", got, tt.first)
			}
			if got := s.Format(ticks[0], 5); got != tt.first {
				t.Errorf("Format(first tick) = %q, want %q", got, tt.first)
			}
		})
	}
}

func TestTimeTickFormat(t *testing.T) {
	s := Time{}
	f := s.TickFormat()
	tests := []struct {
		t    time.Time
		want string
	}{
		{time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC), "2026"},
		{time.Date(2026, 3, 1, 0, 0, 0, 0, time.UTC), "Mar"},
		{time.Date(2026, 3, 3, 0, 0, 0, 0, time.UTC), "Mar 3"},
		{time.Date(2026, 3, 3, 14, 0, 0, 0, time.UTC), "2 PM"},
		{time.Date(2026, 3, 3, 9, 0, 0, 0, time.UTC), "9 AM"},
		{time.Date(2026, 3, 3, 12, 0, 0, 0, time.UTC), "12 PM"},
		{time.Date(2026, 3, 3, 14, 30, 0, 0, time.UTC), "2:30 PM"},
		{time.Date(2026, 3, 3, 14, 30, 5, 0, time.UTC), ":05"},
		{time.Date(2026, 3, 3, 14, 30, 5, 250e6, time.UTC), ".250"},
		{time.Date(50, 1, 1, 0, 0, 0, 0, time.UTC), "50"},
	}
	for _, tt := range tests {
		if got := f(tt.t); got != tt.want {
			t.Errorf("format(%v) = %q, want %q", tt.t, got, tt.want)
		}
	}
}

func TestTimeTickFormatFor(t *testing.T) {
	mar1 := time.Date(2026, 3, 1, 0, 0, 0, 0, time.UTC)
	jan1 := time.Date(2027, 1, 1, 0, 0, 0, 0, time.UTC)
	tests := []struct {
		name  string
		span  time.Duration
		count int
		t     time.Time
		want  string
	}{
		{"daily ticks name the day", 14 * 24 * time.Hour, 7, mar1, "Mar 1"},
		{"hourly ticks name the day", 2 * 24 * time.Hour, 8, mar1, "Mar 1"},
		{"weekly ticks name the day", 60 * 24 * time.Hour, 8, mar1, "Mar 1"},
		{"monthly ticks name the month", 365 * 24 * time.Hour, 12, mar1, "Mar"},
		{"daily ticks keep the year marker", 14 * 24 * time.Hour, 7, jan1, "2027"},
		{"other days are unchanged", 14 * 24 * time.Hour, 7, mar1.AddDate(0, 0, 2), "Mar 3"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := Time{Domain: [2]time.Time{tt.t.Add(-tt.span / 2), tt.t.Add(tt.span / 2)}, Range: [2]float64{0, 600}}
			if got := s.TickFormatFor(tt.count)(tt.t); got != tt.want {
				t.Errorf("TickFormatFor(%d)(%v) = %q, want %q (interval %+v)", tt.count, tt.t, got, tt.want, s.tickInterval(tt.count))
			}
		})
	}
}

func TestTimeWeeksStartSunday(t *testing.T) {
	// Five weeks starting Wednesday 2026-03-04 picks weekly ticks; each
	// must land on a Sunday midnight (US calendar).
	t0 := time.Date(2026, 3, 4, 0, 0, 0, 0, time.UTC)
	s := NewTime(t0, t0.AddDate(0, 0, 35), 0, 500)
	if iv := s.tickInterval(5); iv.unit != unitWeek {
		t.Fatalf("expected weekly interval, got %+v", iv)
	}
	ticks := s.Ticks(5)
	if len(ticks) == 0 {
		t.Fatal("no ticks")
	}
	for _, tk := range ticks {
		if tk.Weekday() != time.Sunday || tk.Hour() != 0 {
			t.Errorf("weekly tick %v is not a Sunday midnight", tk)
		}
	}
	if got := ticks[0].Format("2006-01-02"); got != "2026-03-08" {
		t.Errorf("first weekly tick = %s, want 2026-03-08", got)
	}
}

func TestTimeFormat(t *testing.T) {
	t0 := time.Date(2026, 3, 3, 14, 5, 9, 0, time.UTC)
	ny := newYork(t)
	tests := []struct {
		span time.Duration
		loc  *time.Location
		want string
	}{
		{10 * time.Second, nil, "Mar 3, 2026 2:05:09 PM"},
		{time.Hour, nil, "Mar 3, 2026 2:05 PM"},
		{time.Hour, ny, "Mar 3, 2026 9:05 AM"},
		{20 * 24 * time.Hour, nil, "Mar 3, 2026"},
		{300 * 24 * time.Hour, nil, "Mar 2026"},
		{20 * 365 * 24 * time.Hour, nil, "2026"},
	}
	for _, tt := range tests {
		s := Time{Domain: [2]time.Time{t0, t0.Add(tt.span)}, Range: [2]float64{0, 600}, Location: tt.loc}
		if got := s.Format(t0, 6); got != tt.want {
			t.Errorf("Format over %v = %q, want %q", tt.span, got, tt.want)
		}
		if got := s.Formatter(6)(t0); got != tt.want {
			t.Errorf("Formatter over %v = %q, want %q", tt.span, got, tt.want)
		}
	}
}

func TestTimeFormatterFor(t *testing.T) {
	at := func(h, mi, s int) time.Time { return time.Date(2026, 3, 3, h, mi, s, 0, time.UTC) }
	ny := time.FixedZone("EST", -5*3600)
	tests := []struct {
		name  string
		span  time.Duration
		loc   *time.Location
		times []time.Time
		probe time.Time
		want  string
	}{
		{"nil data keeps the tick precision", time.Hour, nil, nil, at(14, 5, 30), "Mar 3, 2026 2:05 PM"},
		{"whole minutes keep minute labels", time.Hour, nil, []time.Time{at(14, 0, 0), at(14, 1, 0)}, at(14, 1, 0), "Mar 3, 2026 2:01 PM"},
		{"30-second buckets need seconds", time.Hour, nil, []time.Time{at(14, 0, 0), at(14, 0, 30)}, at(14, 0, 30), "Mar 3, 2026 2:00:30 PM"},
		{"millisecond data shows milliseconds", time.Hour, nil, []time.Time{at(14, 0, 0).Add(time.Millisecond)}, at(14, 0, 0).Add(100 * time.Millisecond), "Mar 3, 2026 2:00:00.100 PM"},
		{"microsecond data shows microseconds", time.Hour, nil, []time.Time{at(14, 0, 0).Add(time.Microsecond)}, at(14, 0, 0), "Mar 3, 2026 2:00:00.000000 PM"},
		{"nanosecond data shows nanoseconds", time.Hour, nil, []time.Time{at(14, 0, 0).Add(time.Millisecond), at(14, 0, 0).Add(1)}, at(14, 0, 0).Add(1), "Mar 3, 2026 2:00:00.000000001 PM"},
		{"times of day under date ticks", 20 * 24 * time.Hour, nil, []time.Time{at(0, 0, 0), at(12, 0, 0)}, at(12, 0, 0), "Mar 3, 2026 12:00 PM"},
		{"midnights keep date labels", 20 * 24 * time.Hour, nil, []time.Time{at(0, 0, 0)}, at(0, 0, 0), "Mar 3, 2026"},
		{"days under month ticks", 300 * 24 * time.Hour, nil, []time.Time{at(0, 0, 0)}, at(0, 0, 0), "Mar 3, 2026"},
		{"never coarser than the ticks", 10 * time.Second, nil, []time.Time{at(0, 0, 0)}, at(14, 5, 9), "Mar 3, 2026 2:05:09 PM"},
		{"precision is judged in the location", 20 * 24 * time.Hour, ny, []time.Time{at(5, 0, 0)}, at(5, 0, 0), "Mar 3, 2026"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t0 := at(0, 0, 0)
			s := Time{Domain: [2]time.Time{t0, t0.Add(tt.span)}, Range: [2]float64{0, 600}, Location: tt.loc}
			var times func(func(time.Time) bool)
			if tt.times != nil {
				times = slices.Values(tt.times)
			}
			if got := s.FormatterFor(6, times)(tt.probe); got != tt.want {
				t.Errorf("FormatterFor(6, %v)(%v) = %q, want %q", tt.times, tt.probe, got, tt.want)
			}
		})
	}
}

func TestTimeMapInvert(t *testing.T) {
	t0 := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	s := NewTime(t0, t0.Add(10*time.Second), 0, 100)
	if got := s.Map(t0.Add(5 * time.Second)); got != 50 {
		t.Errorf("Map = %v, want 50", got)
	}
	if got := s.Invert(50); !got.Equal(t0.Add(5 * time.Second)) {
		t.Errorf("Invert = %v", got)
	}
}

func BenchmarkTicks(b *testing.B) {
	for b.Loop() {
		Ticks(-123.4, 98765.4, 10)
	}
}

// BenchmarkFormatterFor scans 1,000 whole-minute times, the common case
// for a chart's tooltip labels.
func BenchmarkFormatterFor(b *testing.B) {
	t0 := time.Date(2026, 3, 3, 0, 0, 0, 0, time.UTC)
	times := make([]time.Time, 1000)
	for i := range times {
		times[i] = t0.Add(time.Duration(i) * time.Minute)
	}
	s := Time{Domain: [2]time.Time{t0, times[len(times)-1]}, Range: [2]float64{0, 640}}
	for b.Loop() {
		s.FormatterFor(7, slices.Values(times))
	}
}
