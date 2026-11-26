package main

import (
	"math"
	"net/http"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"time"
)

// The dashboard shows simulated traffic for an API served from three
// regions. Every number is a pure function of a region, a metric and a
// time bucket: there is no global random state and nothing is stored. Two
// requests that fall in the same bucket therefore render byte-identical
// charts, which is what lets the hx-ptag polls answer 304 Not Modified
// until a bucket closes.

// region is one simulated deployment. Its color slot is pinned, so a
// region keeps its color whichever other regions the filter hides.
type region struct {
	Key     string  // series name and query value
	Slot    int     // categorical color slot, 1–8
	latency float64 // median latency baseline, in ms
	rps     float64 // mean request rate, requests per second
	phase   float64 // offset of the daily traffic cycle, in hours
	errRate float64 // baseline error rate, as a fraction
}

// regions lists every region in display order. Scatter plots stay
// readable for at most three series, so there are three.
var regions = []region{
	{Key: "us-east", Slot: 1, latency: 38, rps: 1450, phase: 0, errRate: 0.0042},
	{Key: "eu-west", Slot: 2, latency: 52, rps: 980, phase: 6, errRate: 0.0031},
	{Key: "ap-south", Slot: 3, latency: 86, rps: 620, phase: 11.5, errRate: 0.0065},
}

// timeRange is one date-range preset of the filter row.
type timeRange struct {
	Key    string        // query value and button text: "15m", "1h", "6h"
	Title  string        // human description: "Last hour"
	Period string        // the previous period, for KPI deltas: "previous hour"
	Span   time.Duration // the window the charts show
	Step   time.Duration // bucket size of the latency, throughput and KPI series
	// Window and Windows define the bar chart: Windows closed periods of
	// length Window each. They change rarely, so most polls of the bar
	// chart are answered with 304.
	Window  time.Duration
	Windows int
}

var ranges = []timeRange{
	{Key: "15m", Title: "Last 15 minutes", Period: "previous 15 min", Span: 15 * time.Minute, Step: 10 * time.Second, Window: 3 * time.Minute, Windows: 5},
	{Key: "1h", Title: "Last hour", Period: "previous hour", Span: time.Hour, Step: 30 * time.Second, Window: 10 * time.Minute, Windows: 6},
	{Key: "6h", Title: "Last 6 hours", Period: "previous 6 hours", Span: 6 * time.Hour, Step: 3 * time.Minute, Window: time.Hour, Windows: 6},
}

const defaultRange = 1 // "1h"

// filters is the state of the filter row, decoded from the query string.
type filters struct {
	Range   timeRange
	Regions []region // selected regions, in display order; never empty
	// NoneSelected reports that every region checkbox was cleared. The
	// dashboard then shows all regions rather than empty charts.
	NoneSelected bool
	// Stacked selects the stacked layout of the requests bar chart.
	Stacked bool
}

// parseFilters reads the filters from r's query string. Unknown values
// fall back to the defaults, so any URL renders a valid dashboard.
func parseFilters(r *http.Request) filters {
	q := r.URL.Query()
	f := filters{Range: ranges[defaultRange], Stacked: q.Get("bars") == "stacked"}
	for _, rg := range ranges {
		if rg.Key == q.Get("range") {
			f.Range = rg
		}
	}
	selected := q["region"]
	for _, g := range regions {
		if slices.Contains(selected, g.Key) {
			f.Regions = append(f.Regions, g)
		}
	}
	if len(f.Regions) == 0 {
		f.Regions = regions
		// The filter form always sends an empty "region" (a hidden
		// input), so a region key that names no region means every
		// checkbox was cleared. A link with no region key at all, such
		// as "/?range=6h", means every region.
		f.NoneSelected = q.Has("region")
	}
	return f
}

// query encodes the range and region filters so that parseFilters
// decodes the same filters from it. The bar layout is not part of it:
// the bar chart reads that from its own toggle (hx-include).
func (f filters) query() string {
	v := url.Values{"range": {f.Range.Key}}
	if f.NoneSelected {
		v.Set("region", "")
	} else {
		for _, g := range f.Regions {
			v.Add("region", g.Key)
		}
	}
	return v.Encode()
}

// has reports whether region key is selected.
func (f filters) has(key string) bool {
	return slices.ContainsFunc(f.Regions, func(g region) bool { return g.Key == key })
}

// scope describes the current slice in words, for the line above the KPIs.
func (f filters) scope() string {
	names := make([]string, len(f.Regions))
	for i, g := range f.Regions {
		names[i] = g.Key
	}
	s := f.Range.Title + " · " + strings.Join(names, ", ")
	if f.NoneSelected {
		s += " (no region selected, so all are shown)"
	}
	return s
}

// ---- Deterministic noise ----------------------------------------------

// noise returns a uniform value in [0, 1) determined only by its inputs:
// a region key, a metric name, a bucket index and the bucket size. It
// hashes them with FNV-1a and finishes with the splitmix64 mixer, so
// neighboring buckets are uncorrelated.
func noise(key, metric string, bucket int64, step time.Duration) float64 {
	const (
		offset = 14695981039346656037
		prime  = 1099511628211
	)
	h := uint64(offset)
	for i := range len(key) {
		h = (h ^ uint64(key[i])) * prime
	}
	h = (h ^ '/') * prime
	for i := range len(metric) {
		h = (h ^ uint64(metric[i])) * prime
	}
	for _, v := range [2]uint64{uint64(bucket), uint64(step)} {
		for range 8 {
			h = (h ^ (v & 0xff)) * prime
			v >>= 8
		}
	}
	h += 0x9e3779b97f4a7c15
	h = (h ^ (h >> 30)) * 0xbf58476d1ce4e5b9
	h = (h ^ (h >> 27)) * 0x94d049bb133111eb
	h ^= h >> 31
	return float64(h>>11) / (1 << 53)
}

// bucketIndex numbers the bucket of size step that starts at t.
func bucketIndex(t time.Time, step time.Duration) int64 {
	return t.UnixNano() / int64(step)
}

// ---- Simulated metrics ------------------------------------------------

// load is the region's traffic level at t relative to its mean: a daily
// cycle plus two slower waves, smooth in t so the charts look plausible.
func (g region) load(t time.Time) float64 {
	s := float64(t.Unix())
	hours := math.Mod(s/3600+g.phase, 24)
	return 1 +
		0.22*math.Sin(2*math.Pi*hours/24) +
		0.06*math.Sin(2*math.Pi*s/1020+g.phase) +
		0.03*math.Sin(2*math.Pi*s/317+2*g.phase)
}

// rate is the region's mean request rate over the bucket of size step
// starting at t, in requests per second.
func (g region) rate(t time.Time, step time.Duration) float64 {
	j := noise(g.Key, "rps", bucketIndex(t, step), step)
	return g.rps * g.load(t) * (0.94 + 0.12*j)
}

// percentiles returns the region's p50, p95 and p99 latency in ms over
// the bucket of size step starting at t. Latency rises with load, and an
// occasional bucket has a tail-latency spike.
func (g region) percentiles(t time.Time, step time.Duration) (p50, p95, p99 float64) {
	b := bucketIndex(t, step)
	p50 = g.latency * (0.75 + 0.25*g.load(t)) * (0.94 + 0.12*noise(g.Key, "p50", b, step))
	p95 = p50 * (2.0 + 0.35*noise(g.Key, "p95", b, step))
	p99 = p95 * (1.4 + 0.3*noise(g.Key, "p99", b, step))
	if noise(g.Key, "spike", b, step) > 0.95 {
		p95 *= 1.2
		p99 *= 1.6
	}
	return p50, p95, p99
}

// errors returns the fraction of failed requests in the bucket.
func (g region) errors(t time.Time, step time.Duration) float64 {
	b := bucketIndex(t, step)
	e := g.errRate * (0.7 + 0.6*noise(g.Key, "err", b, step))
	if noise(g.Key, "incident", b, step) > 0.97 {
		e *= 4
	}
	return e
}

// sample is the aggregate of the selected regions over one bucket.
type sample struct {
	End      time.Time // the bucket's end, where it is plotted
	Rate     float64   // total requests per second
	ByRegion []float64 // requests per second per selected region
	P50      float64   // latency percentiles, in ms, weighted by traffic
	P95      float64
	P99      float64
	Errors   float64 // fraction of failed requests
}

// bucket aggregates the selected regions over [end-step, end). Merging
// percentiles exactly needs the raw distributions; weighting each
// region's percentile by its traffic is a fair approximation for a demo.
func bucket(f filters, end time.Time, step time.Duration) sample {
	start := end.Add(-step)
	s := sample{End: end, ByRegion: make([]float64, len(f.Regions))}
	var errs float64
	for i, g := range f.Regions {
		r := g.rate(start, step)
		p50, p95, p99 := g.percentiles(start, step)
		s.ByRegion[i] = r
		s.Rate += r
		s.P50 += r * p50
		s.P95 += r * p95
		s.P99 += r * p99
		errs += r * g.errors(start, step)
	}
	if s.Rate > 0 {
		s.P50 /= s.Rate
		s.P95 /= s.Rate
		s.P99 /= s.Rate
		s.Errors = errs / s.Rate
	}
	return s
}

// history returns the closed buckets of f's range that end in
// (end-span, end], oldest first. end must be a multiple of the step.
func history(f filters, end time.Time) []sample {
	step := f.Range.Step
	n := int(f.Range.Span / step)
	out := make([]sample, n)
	for i := range n {
		out[i] = bucket(f, end.Add(-time.Duration(n-1-i)*step), step)
	}
	return out
}

// liveRates returns each selected region's request rate in the current,
// still open second: the value the SSE stream appends to the throughput
// chart. It is seeded by the second, so it changes once a second.
func liveRates(f filters, now time.Time) []float64 {
	out := make([]float64, len(f.Regions))
	for i, g := range f.Regions {
		out[i] = g.rate(now, time.Second)
	}
	return out
}

// payloads returns n simulated requests of region g for the scatter plot:
// payload size in KB and latency in ms. Larger payloads take longer, with
// a long tail. The sample is seeded by the bucket containing t.
func payloads(g region, t time.Time, step time.Duration, n int) (kb, ms []float64) {
	b := bucketIndex(t, step)
	kb = make([]float64, n)
	ms = make([]float64, n)
	for i := range n {
		key := g.Key + "#" + strconv.Itoa(i)
		// A log-normal-ish size between about 2 KB and 120 KB.
		size := math.Exp(1.2 + 2.6*(noise(key, "size", b, step)+noise(key, "size2", b, step))/2)
		jitter := noise(key, "lat", b, step)
		kb[i] = math.Round(size*10) / 10
		ms[i] = math.Round(g.latency*(0.8+0.5*jitter) + 1.1*size + 60*math.Pow(jitter, 8))
	}
	return kb, ms
}
