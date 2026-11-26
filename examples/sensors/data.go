package main

import (
	"math"
	"slices"
	"time"
)

// The building has twelve simulated sensors. Every reading is a pure
// function of a sensor and a time bucket (a one-minute bucket for the
// charts and alerts, a one-second bucket for the live tile values): there
// is no random state and nothing is stored. Two requests in the same
// bucket therefore render byte-identical HTML, which is what lets the
// polled charts answer 304 Not Modified (hx-ptag) until a minute closes,
// and what makes the tests independent of request order.

// minute is the bucket size of the charts and the alerts. Alerts are raised
// on closed one-minute readings, as a building management system would, so
// a tile can show a brief spike that never becomes an alert.
const minute = time.Minute

// kind is what a sensor measures.
type kind struct {
	Noun string // "temperature", for the sensor's name
	Unit string // "°C", shown after values
	Axis string // the detail chart's y-axis label
	// Slot pins the detail chart's line to a color per kind. The over-limit
	// chart uses slots 4 and 2 for warning and alarm, so the kinds avoid
	// them: no color means two things on the page.
	Slot     int
	Decimals int // how readings are rounded and shown
}

var (
	temperature = &kind{Noun: "temperature", Unit: "°C", Axis: "Temperature (°C)", Slot: 1, Decimals: 1}
	humidity    = &kind{Noun: "humidity", Unit: "%", Axis: "Relative humidity (%)", Slot: 3, Decimals: 0}
	co2         = &kind{Noun: "CO₂", Unit: "ppm", Axis: "CO₂ (ppm)", Slot: 7, Decimals: 0}
	power       = &kind{Noun: "power", Unit: "kW", Axis: "Power (kW)", Slot: 5, Decimals: 1}
)

// axisUnit is the unit as the chart appends it to values: " °C", but "%".
func (k kind) axisUnit() string {
	if k.Unit == "%" {
		return k.Unit
	}
	return " " + k.Unit
}

// sensor is one simulated sensor. Codes follow the usual building
// management naming: a prefix for the measurement and a location.
type sensor struct {
	Code  string // "T-SRV": the tile id, the over-limit category and the sensor query value
	Place string // "Server room"
	Kind  *kind
	// Warn and Alarm are the high limits. Readings at or above Warn raise
	// a warning, at or above Alarm an alarm. The detail chart draws both
	// as rules (hyperplot.RuleY).
	Warn, Alarm float64

	base  float64 // typical reading
	swing float64 // amplitude of the daily cycle
	jit   float64 // amplitude of the per-bucket noise
	phase float64 // offset of the daily cycle, in hours
}

// sensors lists every sensor in display order: the tile grid, the
// over-limit chart's categories and the alert order on ties.
var sensors = []sensor{
	{Code: "T-LOB", Place: "Lobby", Kind: temperature, Warn: 25, Alarm: 27, base: 21.5, swing: 1.2, jit: 0.15, phase: 0},
	{Code: "T-2E", Place: "Floor 2 east", Kind: temperature, Warn: 25, Alarm: 27, base: 22.4, swing: 1.0, jit: 0.15, phase: 2},
	{Code: "T-3W", Place: "Floor 3 west", Kind: temperature, Warn: 25, Alarm: 27, base: 22, swing: 1.4, jit: 0.15, phase: 5},
	{Code: "T-SRV", Place: "Server room", Kind: temperature, Warn: 24, Alarm: 27, base: 20.5, swing: 0.4, jit: 0.1, phase: 9},
	{Code: "RH-LOB", Place: "Lobby", Kind: humidity, Warn: 60, Alarm: 70, base: 44, swing: 4, jit: 0.8, phase: 3},
	{Code: "RH-SRV", Place: "Server room", Kind: humidity, Warn: 55, Alarm: 65, base: 40, swing: 2, jit: 0.6, phase: 7},
	{Code: "CO2-2E", Place: "Floor 2 east", Kind: co2, Warn: 1000, Alarm: 1400, base: 620, swing: 140, jit: 15, phase: 1},
	{Code: "CO2-3W", Place: "Floor 3 west", Kind: co2, Warn: 1000, Alarm: 1400, base: 580, swing: 160, jit: 15, phase: 4},
	{Code: "CO2-MTG", Place: "Meeting room", Kind: co2, Warn: 1000, Alarm: 1400, base: 700, swing: 220, jit: 20, phase: 6},
	{Code: "KW-HVAC", Place: "HVAC plant", Kind: power, Warn: 70, Alarm: 85, base: 46, swing: 9, jit: 1.2, phase: 2},
	{Code: "KW-SRV", Place: "Server racks", Kind: power, Warn: 24, Alarm: 28, base: 18.5, swing: 0.8, jit: 0.3, phase: 8},
	{Code: "KW-LGT", Place: "Lighting", Kind: power, Warn: 14, Alarm: 16, base: 8.5, swing: 2.5, jit: 0.2, phase: 0},
}

// defaultSensor is the sensor the detail shows when the URL names none.
const defaultSensor = "T-SRV"

// sensorByCode finds a sensor. Codes come from URLs, so they are
// untrusted: an unknown code is simply not found.
func sensorByCode(code string) (sensor, bool) {
	i := slices.IndexFunc(sensors, func(s sensor) bool { return s.Code == code })
	if i < 0 {
		return sensor{}, false
	}
	return sensors[i], true
}

// Name is the sensor's full name: "Server room temperature".
func (s sensor) Name() string {
	return s.Place + " " + s.Kind.Noun
}

// ---- Deterministic noise ------------------------------------------------

// noise returns a uniform value in [0, 1) determined only by its inputs: a
// sensor code, a stream name and a bucket index. It hashes them with
// FNV-1a and finishes with the splitmix64 mixer, so neighboring buckets
// are uncorrelated.
func noise(code, stream string, bucket int64) float64 {
	const (
		offset = 14695981039346656037
		prime  = 1099511628211
	)
	h := uint64(offset)
	for i := range len(code) {
		h = (h ^ uint64(code[i])) * prime
	}
	h = (h ^ '/') * prime
	for i := range len(stream) {
		h = (h ^ uint64(stream[i])) * prime
	}
	v := uint64(bucket)
	for range 8 {
		h = (h ^ (v & 0xff)) * prime
		v >>= 8
	}
	h += 0x9e3779b97f4a7c15
	h = (h ^ (h >> 30)) * 0xbf58476d1ce4e5b9
	h = (h ^ (h >> 27)) * 0x94d049bb133111eb
	h ^= h >> 31
	return float64(h>>11) / (1 << 53)
}

// ---- Readings -------------------------------------------------------------

// Excursions: in some 20-minute blocks a sensor rises well above normal
// and falls back (a door left open, a meeting, a cooling fault), so that a
// two-hour window usually holds a few warnings and the odd alarm.
const (
	block       = 20 * 60 // seconds
	eventChance = 0.15    // the share of blocks with an excursion, per sensor
)

// smooth is the sensor's noiseless reading at t: a daily cycle, a slower
// wave and any excursion.
func (s sensor) smooth(t time.Time) float64 {
	sec := t.Unix()
	hours := math.Mod(float64(sec)/3600+s.phase, 24)
	v := s.base +
		s.swing*math.Sin(2*math.Pi*hours/24) +
		0.25*s.swing*math.Sin(2*math.Pi*float64(sec)/2700+s.phase)
	b := floorDiv(sec, block)
	if noise(s.Code, "event", b) >= 1-eventChance {
		pos := float64(sec-b*block) / block
		peak := (s.Alarm - s.base) * (0.55 + 0.6*noise(s.Code, "size", b))
		bump := math.Sin(math.Pi * pos)
		v += peak * bump * bump
	}
	return v
}

// at returns the reading for the one-minute bucket that ends at end (a
// multiple of a minute): the value the charts plot and alerts judge.
func (s sensor) at(end time.Time) float64 {
	j := 2*noise(s.Code, "minute", floorDiv(end.Unix(), 60)) - 1
	return s.round(s.smooth(end) + s.jit*j)
}

// live returns the reading in the second that contains now: the value a
// tile shows, which changes once a second.
func (s sensor) live(now time.Time) float64 {
	j := 2*noise(s.Code, "second", now.Unix()) - 1
	return s.round(s.smooth(now.Truncate(time.Second)) + 0.6*s.jit*j)
}

func (s sensor) round(v float64) float64 {
	p := math.Pow(10, float64(s.Kind.Decimals))
	return math.Round(v*p) / p
}

// floorDiv divides rounding toward negative infinity, so buckets before
// 1970 are numbered consistently too.
func floorDiv(a, b int64) int64 {
	q := a / b
	if a%b != 0 && (a < 0) != (b < 0) {
		q--
	}
	return q
}

// series returns the sensor's one-minute readings ending at first, first
// plus a minute, …, up to and including last, with their bucket ends.
func (s sensor) series(first, last time.Time) (ends []time.Time, vals []float64) {
	for e := first; !e.After(last); e = e.Add(minute) {
		ends = append(ends, e)
		vals = append(vals, s.at(e))
	}
	return ends, vals
}

// ---- Levels and alerts -----------------------------------------------------

// level is a reading judged against the sensor's limits.
type level int

const (
	levelOK level = iota
	levelWarn
	levelAlarm
)

func (s sensor) judge(v float64) level {
	switch {
	case v >= s.Alarm:
		return levelAlarm
	case v >= s.Warn:
		return levelWarn
	}
	return levelOK
}

// Key is the level's CSS modifier: "ok", "warn", "alarm".
func (l level) Key() string { return [...]string{"ok", "warn", "alarm"}[l] }

// Text is the level in words, which the page always shows next to its
// color: "Normal", "Warning", "Alarm".
func (l level) Text() string { return [...]string{"Normal", "Warning", "Alarm"}[l] }

// alert is one stretch of consecutive one-minute readings at or above a
// sensor's warning limit.
type alert struct {
	Sensor sensor
	Level  level     // the highest level reached
	Start  time.Time // end of the first bucket over the limit
	End    time.Time // end of the last bucket over the limit
	Peak   float64   // the highest reading
	Active bool      // the last closed bucket is still over the limit
}

// Alert history settings.
const (
	alertWindow = 2 * time.Hour // alerts that ended earlier are dropped
	maxAlerts   = 8             // the table shows the newest ones only
	// alertLookback is how far before alertWindow the scan starts, so an
	// alert that began before the window keeps its true start, and so its
	// row id. Excursions last less than a block.
	alertLookback = block * time.Second
)

// alerts returns the alerts that were active at some point in the
// alertWindow before last, newest first, at most maxAlerts. last is the
// end of the last closed minute.
func alerts(last time.Time) []alert {
	from := last.Add(-alertWindow - alertLookback)
	var out []alert
	for _, s := range sensors {
		cur := -1 // index in out of the sensor's open alert, or -1
		ends, vals := s.series(from, last)
		for i, e := range ends {
			lv := s.judge(vals[i])
			switch {
			case lv == levelOK:
				cur = -1
			case cur < 0:
				out = append(out, alert{Sensor: s, Level: lv, Start: e, End: e, Peak: vals[i]})
				cur = len(out) - 1
			default:
				a := &out[cur]
				a.End, a.Level, a.Peak = e, max(a.Level, lv), max(a.Peak, vals[i])
			}
		}
		if cur >= 0 {
			out[cur].Active = true
		}
	}
	// Drop alerts that ended before the window, and any that was already
	// running when the scan began: its true start, and so its row id, is
	// unknown.
	cutoff := last.Add(-alertWindow)
	out = slices.DeleteFunc(out, func(a alert) bool { return !a.End.After(cutoff) || a.Start.Equal(from) })
	// Newest first; on a tie, display order (a stable sort keeps it).
	slices.SortStableFunc(out, func(a, b alert) int { return b.Start.Compare(a.Start) })
	if len(out) > maxAlerts {
		out = out[:maxAlerts]
	}
	return out
}

// minutesOver counts the one-minute buckets in the hour ending at last in
// which s read at the warning level and at the alarm level.
func minutesOver(s sensor, last time.Time) (warn, alarm int) {
	_, vals := s.series(last.Add(-time.Hour+minute), last)
	for _, v := range vals {
		switch s.judge(v) {
		case levelWarn:
			warn++
		case levelAlarm:
			alarm++
		}
	}
	return warn, alarm
}
