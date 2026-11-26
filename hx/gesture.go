package hx

import (
	"errors"
	"math"
	"net/http"
	"strconv"
	"time"
)

// Request parameters of the hx-plot wire spec (docs/spec.md §4.6, §5.1).
// The extension adds them to htmx requests as form values: in the query
// string for GET and DELETE, in an urlencoded body otherwise. Every "hp-"
// name is reserved for the spec.
const (
	// ParamWidth ("hp-w") is the content-box width, in CSS pixels, of the
	// hx-plot:autosize element at or above the request's target. Read it
	// with [Size].
	ParamWidth = "hp-w"
	// ParamHeight ("hp-h") is that element's content-box height, sent
	// only for hx-plot:autosize="both". Read it with [Size].
	ParamHeight = "hp-h"
	// ParamChart ("hp-chart") is the id of the chart a select or brush
	// came from.
	ParamChart = "hp-chart"
	// ParamX ("hp-x") is the key of the selected x value: the row key "k"
	// the server wrote into the chart's hover metadata. Read it with
	// [Selected].
	ParamX = "hp-x"
	// ParamSeries ("hp-series") is the name of the selected series, from
	// the series group's data-hp-series attribute, when a select has one.
	ParamSeries = "hp-series"
	// ParamX0 ("hp-x0") is one end of a brushed x range. Read it with
	// [Brushed].
	ParamX0 = "hp-x0"
	// ParamX1 ("hp-x1") is the other end of a brushed x range. The ends
	// arrive in either order.
	ParamX1 = "hp-x1"
)

// Selection is a select gesture (docs/spec.md §5.2): the reader clicked,
// tapped or pressed Enter on an x value of a chart, and maybe on one
// series. The fields are the raw parameter values; they come from the
// browser and can be anything, so validate them like any input and escape
// them when you echo them.
type Selection struct {
	// Chart is the chart's id (hp-chart); empty when not sent.
	Chart string
	// X is the key of the selected x value (hp-x), never empty in a
	// Selection that [Selected] reported: the row key the chart's hover
	// metadata carries, which is the time value, the number or the
	// category of that x. Match it against your own keys exactly, or parse
	// it with [Selection.Time] or [Selection.Float].
	X string
	// Series is the selected series' name (hp-series); empty when the
	// select named no series.
	Series string
}

// Selected reads a select gesture's parameters from r. It reports false
// when hp-x is missing or empty, which a handler treats as "no selection",
// not as an error (docs/spec.md §5.2).
//
// Like [Size], it reads r.Form when the handler has parsed it, and
// otherwise the query string plus an urlencoded body of at most 64 KiB,
// so it never buffers or spools an upload. It never fails and never
// panics.
func Selected(r *http.Request) (Selection, bool) {
	v := formValues(r)
	s := Selection{Chart: v.Get(ParamChart), X: v.Get(ParamX), Series: v.Get(ParamSeries)}
	return s, s.X != ""
}

// Time parses X as a time value: an RFC 3339 date-time with any offset
// and any number of fraction digits (digits past nanoseconds are
// dropped), returned in UTC. A key the server wrote has the form
// "2026-03-03T14:10:00Z". The HTML datetime-local form, which has no
// offset, is not accepted here: an hp-x is a key the server wrote.
func (s Selection) Time() (time.Time, error) {
	t, offset, err := parseTime(s.X, time.UTC)
	if err == nil && !offset {
		return time.Time{}, errBadTime(s.X)
	}
	return t, err
}

// Float parses X as a finite number in the JSON number syntax
// ("2", "-0.5", "1e+21"). Infinities, NaN, hexadecimal, underscores and
// surrounding space are rejected.
func (s Selection) Float() (float64, error) { return parseNumber(s.X) }

// Brush is a brush gesture (docs/spec.md §5.3): the reader marked an x
// range on a chart, usually to zoom into it. The fields are the raw
// parameter values, in the order the browser sent them, which need not
// be ascending. They come from the browser and can be anything: parse
// them with [Brush.Times], [Brush.TimesIn] or [Brush.Floats], which order
// the pair, and ignore the brush when they fail.
type Brush struct {
	// Chart is the chart's id (hp-chart); empty when not sent.
	Chart string
	// X0 and X1 are the ends of the range (hp-x0, hp-x1): time values on a
	// time axis, numbers on a linear axis. Both are non-empty in a Brush
	// that [Brushed] reported.
	X0, X1 string
}

// Brushed reads a brush gesture's parameters from r. It reports false
// unless both hp-x0 and hp-x1 are present and non-empty; a request
// without them is an ordinary request (a poll of the same URL, for
// example), which is how a handler tells the two apart (docs/spec.md
// §5.4). A Brush whose values do not parse is still reported, so that the
// handler can decide; the spec says to ignore it.
//
// Like [Size], it reads r.Form when the handler has parsed it, and
// otherwise the query string plus an urlencoded body of at most 64 KiB.
// It never fails and never panics.
func Brushed(r *http.Request) (Brush, bool) {
	v := formValues(r)
	b := Brush{Chart: v.Get(ParamChart), X0: v.Get(ParamX0), X1: v.Get(ParamX1)}
	return b, b.X0 != "" && b.X1 != ""
}

// Times parses the range as times, ordered so that t0 is not after t1,
// both in UTC. It is TimesIn(time.UTC): see there for the accepted forms.
func (b Brush) Times() (t0, t1 time.Time, err error) { return b.TimesIn(time.UTC) }

// TimesIn parses the range as times, ordered so that t0 is not after t1,
// both returned in UTC. Each end is either an RFC 3339 date-time (any
// offset, any number of fraction digits; digits past nanoseconds are
// dropped), which is what the extension sends, or the HTML datetime-local
// form "2006-01-02T15:04", optionally with ":05" and a fraction and with
// no offset, which a plain <input type="datetime-local"> sends and which
// is read as a wall time in loc, the chart's time zone (docs/spec.md
// §5.3, §5.5). Where loc's clocks change, it reads wall times as browsers
// and Python's fold=0 do: a time the clocks repeat is the first of its
// two instants, and a time they skip is read with the offset in effect
// before the change (02:10 in a 02:00 to 03:00 gap is 03:10). A nil loc
// means UTC. Anything else is an error, and both times are then zero.
func (b Brush) TimesIn(loc *time.Location) (t0, t1 time.Time, err error) {
	if loc == nil {
		loc = time.UTC
	}
	if t0, _, err = parseTime(b.X0, loc); err != nil {
		return time.Time{}, time.Time{}, err
	}
	if t1, _, err = parseTime(b.X1, loc); err != nil {
		return time.Time{}, time.Time{}, err
	}
	if t1.Before(t0) {
		t0, t1 = t1, t0
	}
	return t0, t1, nil
}

// Floats parses the range as finite numbers in the JSON number syntax,
// ordered so that x0 <= x1. NaN, infinities and anything that is not a
// JSON number are errors, and both values are then zero. Every finite
// float64 is accepted (docs/spec.md §5.3), but a hyperplot Axis.Domain
// takes magnitudes up to 1e300 only, so a handler that zooms with the
// range should ignore (or clamp) values beyond ±1e300.
func (b Brush) Floats() (x0, x1 float64, err error) {
	if x0, err = parseNumber(b.X0); err != nil {
		return 0, 0, err
	}
	if x1, err = parseNumber(b.X1); err != nil {
		return 0, 0, err
	}
	if x1 < x0 {
		x0, x1 = x1, x0
	}
	return x0, x1, nil
}

// maxQuoted bounds how much of an invalid value an error message repeats.
const maxQuoted = 64

func quoteShort(s string) string {
	if len(s) > maxQuoted {
		s = s[:maxQuoted] + "..."
	}
	return strconv.Quote(s)
}

func errBadTime(s string) error {
	return errors.New("hyperplot: " + quoteShort(s) + " is not an RFC 3339 date-time")
}

// parseNumber parses s as a finite number in the JSON number grammar
// (RFC 8259 §6): -?(0|[1-9][0-9]*)(\.[0-9]+)?([eE][+-]?[0-9]+)?.
func parseNumber(s string) (float64, error) {
	if !jsonNumber(s) {
		return 0, errors.New("hyperplot: " + quoteShort(s) + " is not a number")
	}
	v, err := strconv.ParseFloat(s, 64)
	if err != nil || math.IsInf(v, 0) || math.IsNaN(v) {
		return 0, errors.New("hyperplot: " + quoteShort(s) + " is not a finite number")
	}
	return v, nil
}

// jsonNumber reports whether s matches the JSON number grammar.
func jsonNumber(s string) bool {
	i := 0
	if i < len(s) && s[i] == '-' {
		i++
	}
	switch {
	case i < len(s) && s[i] == '0':
		i++
	case i < len(s) && s[i] >= '1' && s[i] <= '9':
		i = digits(s, i)
	default:
		return false
	}
	if i < len(s) && s[i] == '.' {
		j := digits(s, i+1)
		if j == i+1 {
			return false
		}
		i = j
	}
	if i < len(s) && (s[i] == 'e' || s[i] == 'E') {
		i++
		if i < len(s) && (s[i] == '+' || s[i] == '-') {
			i++
		}
		j := digits(s, i)
		if j == i {
			return false
		}
		i = j
	}
	return i == len(s)
}

// digits returns the index after the run of ASCII digits starting at i.
func digits(s string, i int) int {
	for i < len(s) && s[i] >= '0' && s[i] <= '9' {
		i++
	}
	return i
}

// parseTime parses an RFC 3339 date-time, or the HTML datetime-local form
// (no offset, seconds optional), which is read as a wall time in loc
// (see wallTime for the hours a zone skips or repeats). It accepts the
// strings that the Python helper parse_time_value of spec.md Appendix B
// accepts:
//
//	YYYY-MM-DD[Tt]hh:mm[:ss[.fraction]][Z|z|±hh:mm]
//
// where an offset requires seconds (RFC 3339), offset hours run to 23 and
// minutes to 59, and the year, and the instant in UTC, fall in years 1 to
// 9999. For those strings both give the same instant, except that Python
// drops fraction digits past microseconds and this keeps nanoseconds. It
// reports whether s had an offset and returns the time in UTC.
func parseTime(s string, loc *time.Location) (t time.Time, offset bool, err error) {
	bad := func() (time.Time, bool, error) { return time.Time{}, false, errBadTime(s) }
	num := func(i, n int) int { // n ASCII digits at s[i:], or -1
		if i+n > len(s) {
			return -1
		}
		v := 0
		for _, c := range []byte(s[i : i+n]) {
			if c < '0' || c > '9' {
				return -1
			}
			v = v*10 + int(c-'0')
		}
		return v
	}
	// YYYY-MM-DDThh:mm is 16 bytes.
	if len(s) < 16 || s[4] != '-' || s[7] != '-' || (s[10] != 'T' && s[10] != 't') || s[13] != ':' {
		return bad()
	}
	year, month, day, hour, minute := num(0, 4), num(5, 2), num(8, 2), num(11, 2), num(14, 2)
	// No year 0, even when an offset would move the instant into year 1:
	// Python's datetime cannot hold the wall time, so parse_time_value
	// rejects it.
	if year < 1 || month < 1 || month > 12 || day < 1 || hour < 0 || hour > 23 || minute < 0 || minute > 59 {
		return bad()
	}
	if day > daysIn(year, month) {
		return bad()
	}
	t, offset, ok := parseClock(s, year, month, day, hour, minute, loc)
	// Years 1 to 9999 in UTC, as in Python's datetime: an offset can push
	// year 1 or 9999 out of range, and RFC 3339 has no other years.
	if !ok || t.Year() < 1 || t.Year() > 9999 {
		return bad()
	}
	return t, offset, nil
}

// parseClock parses what follows "YYYY-MM-DDThh:mm" in s for parseTime.
func parseClock(s string, year, month, day, hour, minute int, loc *time.Location) (t time.Time, offset, ok bool) {
	num := func(i int) int { // two ASCII digits at s[i:], or -1
		if i+2 > len(s) || s[i] < '0' || s[i] > '9' || s[i+1] < '0' || s[i+1] > '9' {
			return -1
		}
		return int(s[i]-'0')*10 + int(s[i+1]-'0')
	}
	sec, nsec, hasSec := 0, 0, false
	i := 16
	if i < len(s) && s[i] == ':' {
		if sec = num(i + 1); sec < 0 || sec > 59 {
			return time.Time{}, false, false // RFC 3339 allows a leap second 60; time.Time cannot hold it
		}
		hasSec = true
		i += 3
		if i < len(s) && s[i] == '.' {
			j := digits(s, i+1)
			if j == i+1 {
				return time.Time{}, false, false
			}
			for k, scale := i+1, 100000000; k < j && scale > 0; k, scale = k+1, scale/10 {
				nsec += int(s[k]-'0') * scale
			}
			i = j
		}
	}
	date := func(loc *time.Location) time.Time {
		return time.Date(year, time.Month(month), day, hour, minute, sec, nsec, loc).UTC()
	}
	switch {
	case i == len(s):
		if loc == nil {
			loc = time.UTC
		}
		t := time.Date(year, time.Month(month), day, hour, minute, sec, nsec, loc)
		return wallTime(t, year, month, day, hour, minute, sec, nsec).UTC(), false, true
	case !hasSec:
		return time.Time{}, false, false // an offset needs seconds
	case len(s) == i+1 && (s[i] == 'Z' || s[i] == 'z'):
		return date(time.UTC), true, true
	case len(s) == i+6 && (s[i] == '+' || s[i] == '-') && s[i+3] == ':':
		oh, om := num(i+1), num(i+4)
		if oh < 0 || oh > 23 || om < 0 || om > 59 {
			return time.Time{}, false, false
		}
		off := oh*3600 + om*60
		if s[i] == '-' {
			off = -off
		}
		return date(time.FixedZone("", off)), true, true
	}
	return time.Time{}, false, false
}

// wallTime resolves a wall-clock time in a time zone the way ECMAScript's
// Date, Temporal's "compatible" disambiguation and Python's fold=0 (PEP
// 495, as in parse_time_value of spec.md Appendix B) do. t is
// time.Date(year, …, loc) in loc, whose choice the time package leaves open
// where loc's offset changes:
//
//   - a wall time that occurs twice (the clocks go back) is the first of
//     the two instants;
//   - a wall time that does not occur (the clocks go forward) is read with
//     the offset in effect before the change, so 02:10 in a gap from 02:00
//     to 03:00 is 03:10 after it.
//
// Both are "the offset before the transition". Only the transitions that
// bound t's zone period can be involved, because time.Date returns one of
// the two readings either side of the transition the wall time falls in.
func wallTime(t time.Time, year, month, day, hour, minute, sec, nsec int) time.Time {
	start, end := t.ZoneBounds()
	if start.IsZero() && end.IsZero() {
		return t // a fixed zone, or one without transitions
	}
	// The wall clock read as if it were UTC, comparable with an instant
	// plus its offset.
	wall := time.Date(year, time.Month(month), day, hour, minute, sec, nsec, time.UTC)
	for _, tr := range [2]time.Time{start, end} {
		if tr.IsZero() {
			continue
		}
		_, before := tr.Add(-time.Nanosecond).Zone()
		_, after := tr.Zone()
		// The wall times that occur twice or never around tr run from tr
		// read with the smaller offset to tr read with the larger one.
		lo := tr.UTC().Add(time.Duration(min(before, after)) * time.Second)
		hi := tr.UTC().Add(time.Duration(max(before, after)) * time.Second)
		if !wall.Before(lo) && wall.Before(hi) {
			return wall.Add(-time.Duration(before) * time.Second)
		}
	}
	return t
}

// daysIn returns the number of days in month of year (proleptic
// Gregorian).
func daysIn(year, month int) int {
	switch month {
	case 2:
		if year%4 == 0 && (year%100 != 0 || year%400 == 0) {
			return 29
		}
		return 28
	case 4, 6, 9, 11:
		return 30
	}
	return 31
}
