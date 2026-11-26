package scale

import (
	"iter"
	"math"
	"strconv"
	"time"
)

// Time is a continuous scale for time.Time values. Tick generation is
// calendar-aware: day, week, month and year ticks fall on local midnight
// in Location, and hour ticks on local hours that are multiples of the
// step, so they stay correct across daylight-saving changes. It follows
// the US calendar: week ticks fall on Sundays, and labels use a 12-hour
// clock and month-first dates.
type Time struct {
	// Domain is the input interval [t0, t1]. t0 may be after t1; ticks
	// are always returned in ascending order.
	Domain [2]time.Time
	// Range is the output interval [r0, r1] in pixels. r0 may be greater
	// than r1, which inverts the mapping.
	Range [2]float64
	// Location sets the time zone for tick placement and labels.
	// nil means UTC.
	Location *time.Location
}

// NewTime returns a Time scale mapping [t0, t1] onto [r0, r1] in UTC.
func NewTime(t0, t1 time.Time, r0, r1 float64) Time {
	return Time{Domain: [2]time.Time{t0, t1}, Range: [2]float64{r0, r1}}
}

func (s Time) loc() *time.Location {
	if s.Location == nil {
		return time.UTC
	}
	return s.Location
}

// linear maps seconds since Domain[0] onto the range. Measuring from the
// domain start rather than from 1970 keeps precision proportional to the
// span: a float64 of Unix seconds in 2026 resolves only about 240 ns,
// which would collapse microsecond data into a few columns.
func (s Time) linear() Linear {
	return Linear{
		Domain: [2]float64{0, secondsBetween(s.Domain[0], s.Domain[1])},
		Range:  s.Range,
	}
}

// Map converts a time to a range value.
func (s Time) Map(t time.Time) float64 {
	return s.linear().Map(secondsBetween(s.Domain[0], t))
}

// Invert converts a range value back to a time.
func (s Time) Invert(px float64) time.Time {
	return addSeconds(s.Domain[0], s.linear().Invert(px)).In(s.loc())
}

// secondsBetween returns b − a in seconds. Whole seconds and nanoseconds
// are subtracted separately, so the result is exact to the nanosecond for
// spans up to about 100 days and loses precision only with the span's own
// magnitude, never with the distance from 1970.
func secondsBetween(a, b time.Time) float64 {
	as, bs := a.Unix(), b.Unix()
	d := bs - as
	whole := float64(d)
	if (d < 0) != (bs < as) {
		// The int64 difference overflowed (a span of hundreds of billions
		// of years); the float difference is as good as anything.
		whole = float64(bs) - float64(as)
	}
	return whole + float64(b.Nanosecond()-a.Nanosecond())/1e9
}

// addSeconds returns t + secs, exact to the nanosecond while the whole
// seconds fit in an int64 offset from t.
func addSeconds(t time.Time, secs float64) time.Time {
	whole, frac := math.Modf(secs)
	ns := int64(t.Nanosecond()) + int64(math.Round(frac*1e9))
	const limit = 1 << 62
	if math.IsNaN(whole) || math.Abs(whole) >= limit {
		whole = 0
	}
	base := t.Unix()
	sec := base + int64(whole)
	if (whole > 0 && sec < base) || (whole < 0 && sec > base) {
		sec = base // overflow: keep t rather than wrap around
	}
	return time.Unix(sec, ns)
}

// interval is a calendar step used for time ticks, such as "every 15
// minutes" or "every 3 months". approx is its nominal length in seconds.
type interval struct {
	unit   timeUnit
	step   int
	approx float64
}

type timeUnit int

const (
	// unitNanosecond steps are counted in nanoseconds, so the 1-2-5 steps
	// from 1 ns to 500 µs share one unit; each divides a second evenly.
	unitNanosecond timeUnit = iota
	unitMillisecond
	unitSecond
	unitMinute
	unitHour
	unitDay
	unitWeek
	unitMonth
	unitYear
)

// Nominal unit lengths in seconds, used only to choose an interval.
const (
	secMinute = 60
	secHour   = 60 * secMinute
	secDay    = 24 * secHour
	secWeek   = 7 * secDay
	secMonth  = 30 * secDay
	secYear   = 365 * secDay
)

// intervals lists the candidate tick steps, finest first.
var intervals = []interval{
	{unitNanosecond, 1, 1e-9},
	{unitNanosecond, 2, 2e-9},
	{unitNanosecond, 5, 5e-9},
	{unitNanosecond, 10, 1e-8},
	{unitNanosecond, 20, 2e-8},
	{unitNanosecond, 50, 5e-8},
	{unitNanosecond, 100, 1e-7},
	{unitNanosecond, 200, 2e-7},
	{unitNanosecond, 500, 5e-7},
	{unitNanosecond, 1_000, 1e-6},
	{unitNanosecond, 2_000, 2e-6},
	{unitNanosecond, 5_000, 5e-6},
	{unitNanosecond, 10_000, 1e-5},
	{unitNanosecond, 20_000, 2e-5},
	{unitNanosecond, 50_000, 5e-5},
	{unitNanosecond, 100_000, 1e-4},
	{unitNanosecond, 200_000, 2e-4},
	{unitNanosecond, 500_000, 5e-4},
	{unitMillisecond, 1, 0.001},
	{unitMillisecond, 2, 0.002},
	{unitMillisecond, 5, 0.005},
	{unitMillisecond, 10, 0.01},
	{unitMillisecond, 20, 0.02},
	{unitMillisecond, 50, 0.05},
	{unitMillisecond, 100, 0.1},
	{unitMillisecond, 200, 0.2},
	{unitMillisecond, 500, 0.5},
	{unitSecond, 1, 1},
	{unitSecond, 5, 5},
	{unitSecond, 15, 15},
	{unitSecond, 30, 30},
	{unitMinute, 1, secMinute},
	{unitMinute, 5, 5 * secMinute},
	{unitMinute, 15, 15 * secMinute},
	{unitMinute, 30, 30 * secMinute},
	{unitHour, 1, secHour},
	{unitHour, 3, 3 * secHour},
	{unitHour, 6, 6 * secHour},
	{unitHour, 12, 12 * secHour},
	{unitDay, 1, secDay},
	{unitDay, 2, 2 * secDay},
	{unitWeek, 1, secWeek},
	{unitMonth, 1, secMonth},
	{unitMonth, 3, 3 * secMonth},
	{unitYear, 1, secYear},
}

// tickInterval picks the calendar interval whose spacing is closest to
// dividing the domain into count parts.
func (s Time) tickInterval(count int) interval {
	t0, t1 := s.Domain[0], s.Domain[1]
	if t1.Before(t0) {
		t0, t1 = t1, t0
	}
	if count <= 0 {
		count = 1
	}
	// Measure in float seconds: time.Duration saturates at about 292
	// years, which would size the steps of a longer span for 292 years.
	span := secondsBetween(t0, t1)
	target := span / float64(count)
	if target <= intervals[0].approx {
		return intervals[0]
	}
	last := intervals[len(intervals)-1]
	if target > last.approx {
		// Multi-year spans: step a 1-2-5 number of years.
		years := TickStep(0, span/secYear, count)
		return interval{unitYear, int(max(1, min(math.Round(years), 1e9))), secYear}
	}
	for i := 1; i < len(intervals); i++ {
		hi := intervals[i]
		if target <= hi.approx {
			lo := intervals[i-1]
			// Choose by ratio so that, e.g., a 40-minute target picks 30m
			// rather than 1h.
			if target/lo.approx < hi.approx/target {
				return lo
			}
			return hi
		}
	}
	return last
}

// Ticks returns calendar-aligned times within the domain, roughly count of
// them, in ascending order.
func (s Time) Ticks(count int) []time.Time {
	t0, t1 := s.Domain[0], s.Domain[1]
	if t1.Before(t0) {
		t0, t1 = t1, t0
	}
	if t0.Equal(t1) {
		return []time.Time{t0.In(s.loc())}
	}
	iv := s.tickInterval(count)
	var ticks []time.Time
	t := iv.floor(t0.In(s.loc()))
	// The floor is at or before t0, but may lie more than one step before
	// it (a multi-hour floor across a clock change), so advance until the
	// ticks are inside the domain. next always moves forward; the bound
	// only guards against a zone with pathological rules.
	for i := 0; t.Before(t0) && i < 100; i++ {
		t = iv.next(t)
	}
	for i := 0; !t.After(t1) && i < 10_000; i++ {
		ticks = append(ticks, t)
		t = iv.next(t)
	}
	return ticks
}

// floorMod returns a mod m in [0, m) for m > 0, also for negative a.
func floorMod(a, m int) int {
	return ((a % m) + m) % m
}

// floor returns the latest interval boundary at or before t.
//
// Sub-day floors subtract the local remainder from t rather than rebuild
// the time from its wall-clock fields with time.Date: in the hour repeated
// when clocks fall back, a wall time is ambiguous and time.Date picks its
// first occurrence, an hour before t, which started every tick an hour
// before the domain.
func (iv interval) floor(t time.Time) time.Time {
	loc := t.Location()
	y, mo, d := t.Date()
	_, mi, sec := t.Clock()
	ns := time.Duration(t.Nanosecond())
	switch iv.unit {
	case unitNanosecond:
		return t.Add(-(ns % time.Duration(iv.step)))
	case unitMillisecond:
		return t.Add(-(ns % (time.Duration(iv.step) * time.Millisecond)))
	case unitSecond:
		return t.Add(-(time.Duration(sec%iv.step)*time.Second + ns))
	case unitMinute:
		return t.Add(-(time.Duration(mi%iv.step)*time.Minute + time.Duration(sec)*time.Second + ns))
	case unitHour:
		// Back to the start of the local hour, then hour by hour to one
		// that is a multiple of the step, mirroring next. A day holds at
		// most 25 local hours, so 2·step tries always suffice.
		f := t.Add(-(time.Duration(mi)*time.Minute + time.Duration(sec)*time.Second + ns))
		for i := 0; i < 2*iv.step && f.Hour()%iv.step != 0; i++ {
			f = f.Add(-time.Hour)
		}
		return f
	case unitDay:
		// Align multi-day steps to the day of the month (1, 3, 5, … for
		// 2-day steps), restarting at each month in next, so a tick's
		// date depends only on the calendar and labels stay put as the
		// window slides.
		return time.Date(y, mo, d-(d-1)%iv.step, 0, 0, 0, 0, loc)
	case unitWeek:
		// Weeks start on Sunday (US calendar).
		return time.Date(y, mo, d-int(t.Weekday()), 0, 0, 0, 0, loc)
	case unitMonth:
		m0 := int(mo) - 1
		return time.Date(y, time.Month(m0-m0%iv.step+1), 1, 0, 0, 0, 0, loc)
	default: // unitYear
		return time.Date(y-floorMod(y, iv.step), 1, 1, 0, 0, 0, 0, loc)
	}
}

// next returns the boundary one interval after t. Steps at day
// granularity or coarser use calendar arithmetic, so a "day" across a
// daylight-saving change is 23 or 25 hours, as a reader expects. Hour
// steps advance an hour at a time to the next local hour that is a
// multiple of the step (as d3's timeHour.every does), so a 6-hour axis
// keeps reading 12 AM, 6 AM, 12 PM after the clocks change instead of
// drifting to 1 AM, 7 AM, 1 PM. A repeated local hour (when clocks fall
// back) appears twice only for 1-hour steps. Multi-day steps restart on
// the 1st of each month (as d3's timeDay.every does), so 2-day ticks read
// Jan 29, Jan 31, Feb 1, Feb 3 whatever day the domain starts on.
func (iv interval) next(t time.Time) time.Time {
	switch iv.unit {
	case unitNanosecond:
		return t.Add(time.Duration(iv.step))
	case unitMillisecond:
		return t.Add(time.Duration(iv.step) * time.Millisecond)
	case unitSecond:
		return t.Add(time.Duration(iv.step) * time.Second)
	case unitMinute:
		return t.Add(time.Duration(iv.step) * time.Minute)
	case unitHour:
		n := t.Add(time.Hour)
		// A day holds at most 25 local hours, so 2·step+1 tries always
		// reach the next boundary; the bound only guards odd zones.
		for i := 0; i < 2*iv.step && n.Hour()%iv.step != 0; i++ {
			n = n.Add(time.Hour)
		}
		return n
	case unitDay:
		n := t.AddDate(0, 0, iv.step)
		if y, mo, _ := t.Date(); iv.step > 1 {
			if ny, nmo, _ := n.Date(); ny != y || nmo != mo {
				// Steps are at most a few days, so n is in the next month.
				return time.Date(y, mo+1, 1, 0, 0, 0, 0, t.Location())
			}
		}
		return n
	case unitWeek:
		return t.AddDate(0, 0, 7*iv.step)
	case unitMonth:
		return t.AddDate(0, iv.step, 0)
	default:
		return t.AddDate(iv.step, 0, 0)
	}
}

// TickFormat returns a multi-scale label formatter for ticks: each tick is
// labeled at the coarsest unit that changed at that boundary, so a day of
// hourly ticks reads "Mar 3, 3 AM, 6 AM, …", half-hour ticks read
// "2 PM, 2:30 PM, …", second ticks read "2 PM, :05, :10, …", millisecond
// ticks read ":05, .100, .200, …", finer ticks show six or nine digits
// (".123450", ".123455") and a year of monthly ticks reads
// "2026, Feb, Mar, …". Labels follow US conventions: a 12-hour clock and
// month-first dates. Years are printed without zero padding.
func (s Time) TickFormat() func(time.Time) string {
	return s.tickFormat(false)
}

// TickFormatFor is like [Time.TickFormat], but knows the tick interval
// that count implies. When ticks are finer than a month, the first of a
// month reads "Mar 1" rather than "Mar", which next to "Mar 3" and
// "Mar 5" would look like a label missing its day. Charts use this form.
func (s Time) TickFormatFor(count int) func(time.Time) string {
	return s.tickFormat(s.tickInterval(count).unit < unitMonth)
}

func (s Time) tickFormat(dayStarts bool) func(time.Time) string {
	loc := s.loc()
	return func(t time.Time) string {
		t = t.In(loc)
		_, mo, d := t.Date()
		h, mi, sec := t.Clock()
		switch ns := t.Nanosecond(); {
		case ns%int(time.Microsecond) != 0:
			return t.Format(".000000000")
		case ns%int(time.Millisecond) != 0:
			return t.Format(".000000")
		case ns != 0:
			return t.Format(".000")
		case sec != 0:
			return t.Format(":05")
		case mi != 0:
			return t.Format("3:04 PM")
		case h != 0:
			return t.Format("3 PM")
		case d != 1 || dayStarts && mo != time.January:
			return t.Format("Jan 2")
		case mo != time.January:
			return t.Format("Jan")
		default:
			return strconv.Itoa(t.Year())
		}
	}
}

// Format returns a full, unambiguous label for a single time, at the
// precision implied by the tick interval for count ticks. It is meant for
// tooltips and data tables, where a value is read out of context.
func (s Time) Format(t time.Time, count int) string {
	return s.Formatter(count)(t)
}

// Formatter returns a function equivalent to Format(t, count) for a fixed
// count. It picks the tick interval once, so it is cheaper than calling
// Format repeatedly.
func (s Time) Formatter(count int) func(time.Time) string {
	return s.precision(count).formatter(s.loc())
}

// FormatterFor is like [Time.Formatter], but never coarser than the data
// it labels: if any of times falls between the boundaries of the
// precision that count implies (a time with seconds under minute labels,
// a time of day under date labels, a fraction of a second under second
// labels, and so on), it formats at the finest precision those times
// need, down to nanoseconds. Distinct data times then never print alike,
// which matters in tooltips and data tables. A nil times behaves like
// Formatter.
func (s Time) FormatterFor(count int, times iter.Seq[time.Time]) func(time.Time) string {
	loc, prec := s.loc(), s.precision(count)
	if times != nil {
		for t := range times {
			if ns := t.Nanosecond(); ns != 0 {
				// Sub-second parts do not depend on the location.
				if prec = min(prec, subSecond(ns)); prec == precNanosecond {
					break // nothing is finer
				}
				continue
			}
			t = t.In(loc)
			if t.Second() != 0 {
				prec = min(prec, precSecond)
				continue
			}
			// Only seconds and fractions can be finer than minutes; skip
			// the calendar fields, which cost more, in the common case.
			if prec > precMinute {
				prec = min(prec, precisionOf(t))
			}
		}
	}
	return prec.formatter(loc)
}

// precision is the finest calendar field a full time label shows.
type precision int

const (
	precNanosecond precision = iota
	precMicrosecond
	precMillisecond
	precSecond
	precMinute
	precDay
	precMonth
	precYear
)

// precision returns the label precision implied by the tick interval for
// count ticks. A zero-span domain (a single instant, such as the first
// sample of a live chart) has no tick interval to speak of; it uses the
// precision its own time needs, never coarser than a day.
func (s Time) precision(count int) precision {
	if s.Domain[0].Equal(s.Domain[1]) {
		return min(precisionOf(s.Domain[0].In(s.loc())), precDay)
	}
	switch iv := s.tickInterval(count); iv.unit {
	case unitNanosecond:
		if iv.step%int(time.Microsecond) == 0 {
			return precMicrosecond
		}
		return precNanosecond
	case unitMillisecond:
		return precMillisecond
	case unitSecond:
		return precSecond
	case unitMinute, unitHour:
		return precMinute
	case unitDay, unitWeek:
		return precDay
	case unitMonth:
		return precMonth
	default:
		return precYear
	}
}

// subSecond returns the coarsest precision that shows a nonzero
// nanosecond-of-second ns exactly.
func subSecond(ns int) precision {
	switch {
	case ns%int(time.Microsecond) != 0:
		return precNanosecond
	case ns%int(time.Millisecond) != 0:
		return precMicrosecond
	default:
		return precMillisecond
	}
}

// precisionOf returns the coarsest precision that shows t exactly, with t
// already in the display location.
func precisionOf(t time.Time) precision {
	if ns := t.Nanosecond(); ns != 0 {
		return subSecond(ns)
	}
	h, mi, sec := t.Clock()
	_, mo, d := t.Date()
	switch {
	case sec != 0:
		return precSecond
	case h != 0 || mi != 0:
		return precMinute
	case d != 1:
		return precDay
	case mo != time.January:
		return precMonth
	default:
		return precYear
	}
}

func (p precision) layout() string {
	switch p {
	case precNanosecond:
		return "Jan 2, 2006 3:04:05.000000000 PM"
	case precMicrosecond:
		return "Jan 2, 2006 3:04:05.000000 PM"
	case precMillisecond:
		return "Jan 2, 2006 3:04:05.000 PM"
	case precSecond:
		return "Jan 2, 2006 3:04:05 PM"
	case precMinute:
		return "Jan 2, 2006 3:04 PM"
	case precDay:
		return "Jan 2, 2006"
	case precMonth:
		return "Jan 2006"
	default:
		return "2006"
	}
}

// formatter returns a function that labels a time at precision p in loc.
// Year-only labels are printed without the zero padding of the "2006"
// layout, so year 50 reads "50", not "0050".
func (p precision) formatter(loc *time.Location) func(time.Time) string {
	if p == precYear {
		return func(t time.Time) string { return strconv.Itoa(t.In(loc).Year()) }
	}
	layout := p.layout()
	return func(t time.Time) string { return t.In(loc).Format(layout) }
}
