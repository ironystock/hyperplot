package main

import (
	"hash/fnv"
	"math"
	"net/url"
	"slices"
	"strconv"
	"time"
)

// The demo shows simulated CI builds for three pipelines. Every build is
// a pure function of its pipeline, the hour it started in and its index
// within that hour: there is no stored state and no random generator. A
// build counts once it has finished (its End is not after now), so the
// charts change only when a build finishes, and every poll in between
// renders the same bytes and is answered with 304 Not Modified.

// pipeline is one CI pipeline. Its color slot is pinned, so a pipeline
// keeps its color whichever others the filter hides (docs/htmx.md,
// "Keep colors stable with Slot").
type pipeline struct {
	Key     string  // series name and query value
	Slot    int     // categorical color slot, 1–8
	minutes float64 // typical duration of a passing build
	perHour float64 // builds started per working hour
}

// pipelines lists every pipeline in display order. A scatter plot keeps
// its colors apart for at most three series, so there are three.
var pipelines = []pipeline{
	{Key: "api", Slot: 1, minutes: 6, perHour: 0.9},
	{Key: "web", Slot: 2, minutes: 9, perHour: 0.7},
	{Key: "mobile", Slot: 3, minutes: 16, perHour: 0.4},
}

// branchKinds are the values of the branch filter, in display order.
var branchKinds = []struct{ Key, Title string }{
	{"", "All branches"},
	{"main", "main"},
	{"release", "release/*"},
	{"feature", "feature/*"},
}

// featureNames name the simulated feature branches. One holds an
// ampersand: branch names are user input, and html/template escapes them.
var featureNames = []string{
	"login-flow", "cache-keys", "dark-mode", "search-v2",
	"retry-budget", "i18n", "r&d-spike", "csv-export",
}

// build is one finished (or running) CI build.
type build struct {
	Pipeline *pipeline // an entry of the pipelines table
	Number   int
	Kind     string // "main", "release" or "feature"
	Branch   string // the full branch name
	Start    time.Time
	End      time.Time
	Minutes  float64 // End - Start, in minutes, rounded to 0.1
	Passed   bool
}

// ID is the build's stable element id, unique across pipelines.
func (b build) ID() string { return "build-" + b.Pipeline.Key + "-" + strconv.Itoa(b.Number) }

// Name is how people refer to the build: "#888671".
func (b build) Name() string { return "#" + strconv.Itoa(b.Number) }

// Slow reports whether the build took more than twice its pipeline's
// typical time. The durations chart labels these points.
func (b build) Slow() bool { return b.Minutes > 2*b.Pipeline.minutes }

// noise returns a number in [0, 1) that depends only on its arguments: the
// FNV-1a hash of the pipeline, the hour, the build index and a salt that
// tells the different draws for one build apart.
func noise(p string, hour int64, i int, salt string) float64 {
	h := fnv.New64a()
	b := make([]byte, 0, 48)
	b = append(b, p...)
	b = append(b, '/')
	b = strconv.AppendInt(b, hour, 10)
	b = append(b, '/')
	b = strconv.AppendInt(b, int64(i), 10)
	b = append(b, '/')
	b = append(b, salt...)
	h.Write(b) // never fails
	return float64(h.Sum64()>>11) / (1 << 53)
}

// activity scales the build rate by the time of week: busy during working
// hours (UTC) on weekdays, quiet at night and at weekends.
func activity(t time.Time) float64 {
	t = t.UTC()
	a := 1.0
	if h := t.Hour(); h < 7 || h >= 19 {
		a = 0.15
	}
	if wd := t.Weekday(); wd == time.Saturday || wd == time.Sunday {
		a *= 0.25
	}
	return a
}

// maxBuildsPerHour bounds the builds one pipeline starts in an hour.
const maxBuildsPerHour = 3

// buildsStarted returns the builds that pipeline p started in the given
// Unix hour, whether or not they have finished.
func buildsStarted(p *pipeline, hour int64) []build {
	start := time.Unix(hour*3600, 0).UTC()
	rate := p.perHour * activity(start)
	var out []build
	for i := range maxBuildsPerHour {
		// Each slot starts a build with probability rate/3, so the
		// expected count is rate.
		if noise(p.Key, hour, i, "starts") >= rate/maxBuildsPerHour {
			continue
		}
		b := build{
			Pipeline: p,
			Number:   (int(hour%100000)*len(pipelines)+p.Slot-1)*maxBuildsPerHour + i, // unique across pipelines
			Start:    start.Add(time.Duration(noise(p.Key, hour, i, "minute") * float64(time.Hour))).Truncate(time.Second),
		}
		var failRate, slower float64
		switch k := noise(p.Key, hour, i, "branch"); {
		case k < 0.35:
			b.Kind, b.Branch, failRate, slower = "main", "main", 0.07, 1.1
		case k < 0.45:
			b.Kind, failRate, slower = "release", 0.05, 1.25
			b.Branch = "release/1." + strconv.FormatInt(hour/(24*14)%10, 10)
		default:
			b.Kind, failRate, slower = "feature", 0.16, 1
			b.Branch = "feature/" + featureNames[int(noise(p.Key, hour, i, "name")*float64(len(featureNames)))]
		}
		// A slow drift over a nine-day cycle gives the trend lines
		// something to show.
		day := float64(hour) / 24
		drift := 1 + 0.15*math.Sin(2*math.Pi*day/9+float64(p.Slot))
		m := p.minutes * slower * drift * (0.75 + 0.5*noise(p.Key, hour, i, "jitter"))
		b.Passed = noise(p.Key, hour, i, "result") >= failRate
		if !b.Passed {
			m *= 0.35 + 0.55*noise(p.Key, hour, i, "failed-at") // a failure stops the build early
		}
		if noise(p.Key, hour, i, "outlier") < 0.035 {
			m *= 2.3 + 1.2*noise(p.Key, hour, i, "outlier-size") // a cold cache or a queue
		}
		b.Minutes = math.Round(m*10) / 10
		// Whole seconds: 6.9 × float64(time.Minute) is a hair under the true
		// value, and a stray nanosecond would make the tooltips print every
		// time to the nanosecond.
		b.End = b.Start.Add(time.Duration(math.Round(b.Minutes*60)) * time.Second)
		out = append(out, b)
	}
	return out
}

// maxBuildTime bounds any build's duration (16 × 1.25 × 1.15 × 1.25 × 3.5
// minutes is under 2 hours), so builds that end in a window started at
// most this long before it.
const maxBuildTime = 2 * time.Hour

// finishedBuilds returns the builds of the pipelines in f that match its
// branch filter and finished in [from, to) and no later than now, ordered
// by finish time.
func finishedBuilds(f filters, from, to, now time.Time) []build {
	if now.Before(to) {
		to = now.Add(time.Nanosecond) // a build that ends exactly now counts
	}
	var out []build
	first := from.Add(-maxBuildTime).Unix() / 3600
	last := to.Unix() / 3600
	for i := range f.Pipelines {
		p := &f.Pipelines[i]
		for h := first; h <= last; h++ {
			for _, b := range buildsStarted(p, h) {
				if b.End.Before(from) || !b.End.Before(to) {
					continue
				}
				if f.Branch != "" && b.Kind != f.Branch {
					continue
				}
				out = append(out, b)
			}
		}
	}
	slices.SortStableFunc(out, func(a, b build) int {
		if c := a.End.Compare(b.End); c != 0 {
			return c
		}
		if a.Pipeline.Slot != b.Pipeline.Slot {
			return a.Pipeline.Slot - b.Pipeline.Slot
		}
		return a.Number - b.Number
	})
	return out
}

// ---- Filters ---------------------------------------------------------------

// dayRange is one range preset of the filter form.
type dayRange struct {
	Key   string // query value: "7d", "14d"
	Title string // "Last 7 days"
	Days  int
}

var ranges = []dayRange{
	{Key: "7d", Title: "Last 7 days", Days: 7},
	{Key: "14d", Title: "Last 14 days", Days: 14},
}

const defaultRange = 1 // "14d"

// filters is the state of the filter form, decoded from the query string.
type filters struct {
	Range     dayRange
	Pipelines []pipeline // selected pipelines, in display order; never empty
	// NoneSelected reports that every pipeline checkbox was cleared. The
	// page then shows all pipelines rather than empty charts.
	NoneSelected bool
	Branch       string // "" for all, or a branchKinds key
	Theme        string // "", "light" or "dark": written to <html data-theme>
}

// parseFilters reads the filters from a query string. Unknown values fall
// back to the defaults, so any URL renders a valid page.
func parseFilters(q url.Values) filters {
	f := filters{Range: ranges[defaultRange]}
	for _, rg := range ranges {
		if rg.Key == q.Get("range") {
			f.Range = rg
		}
	}
	for _, k := range branchKinds {
		if k.Key == q.Get("branch") {
			f.Branch = k.Key
		}
	}
	if t := q.Get("theme"); t == "light" || t == "dark" {
		f.Theme = t
	}
	// The form sends an empty "pipeline" before its checkboxes, so a form
	// with every box cleared still says so; a URL without the parameter
	// means every pipeline.
	if picked, ok := q["pipeline"]; ok {
		for _, p := range pipelines {
			if slices.Contains(picked, p.Key) {
				f.Pipelines = append(f.Pipelines, p)
			}
		}
		f.NoneSelected = len(f.Pipelines) == 0
	}
	if len(f.Pipelines) == 0 {
		f.Pipelines = pipelines
	}
	return f
}

// has reports whether pipeline key is selected.
func (f filters) has(key string) bool {
	return slices.ContainsFunc(f.Pipelines, func(p pipeline) bool { return p.Key == key })
}

// query is the canonical query string of f, in a fixed order, so equal
// filters always give equal URLs. It keeps the empty "pipeline" when every
// box was cleared, so a reload, a bookmark or Back shows the boxes the way
// the reader left them; parseFilters(query) gives f back.
func (f filters) query() string {
	q := "range=" + f.Range.Key
	if f.NoneSelected {
		q += "&pipeline="
	} else if len(f.Pipelines) < len(pipelines) {
		for _, p := range f.Pipelines {
			q += "&pipeline=" + p.Key
		}
	}
	if f.Branch != "" {
		q += "&branch=" + f.Branch
	}
	if f.Theme != "" {
		q += "&theme=" + f.Theme
	}
	return q
}

// withTheme returns f with another theme, for the theme links.
func (f filters) withTheme(t string) filters {
	f.Theme = t
	return f
}

// ---- Days --------------------------------------------------------------------

// day is one calendar day in the page's time zone. Its label is also the
// category of its bar group, which a select sends back as hp-x, so the
// labels of the days shown at one time are unique.
type day struct {
	Start, End time.Time
	Label      string // "Mar 3"
}

// days returns the range's calendar days in loc, oldest first, ending with
// today (which is still running). Days are built with AddDate, so a day
// with a clock change is 23 or 25 hours long, as it should be.
func days(f filters, now time.Time, loc *time.Location) []day {
	y, m, d := now.In(loc).Date()
	today := time.Date(y, m, d, 0, 0, 0, 0, loc)
	out := make([]day, f.Range.Days)
	for i := range out {
		start := today.AddDate(0, 0, i-f.Range.Days+1)
		out[i] = day{Start: start, End: start.AddDate(0, 0, 1), Label: start.Format("Jan 2")}
	}
	return out
}

// percentile returns the nearest-rank p-th percentile of sorted values, or
// NaN when there are none (a gap in the trend line).
func percentile(sorted []float64, p float64) float64 {
	if len(sorted) == 0 {
		return math.NaN()
	}
	i := int(math.Ceil(p/100*float64(len(sorted)))) - 1
	return sorted[max(i, 0)]
}
