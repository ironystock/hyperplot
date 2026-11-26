package hx

import (
	"context"
	"encoding/json"
	"math"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"
	_ "time/tzdata" // America/New_York for the timesIn case, even without system zoneinfo
)

func queryRequest(tb testing.TB, query string) *http.Request {
	tb.Helper()
	return httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/c?"+query, nil)
}

// paramsCase is one "server" case of testdata/conformance/params.json
// (format in that directory's README).
type paramsCase struct {
	Query string `json:"query"`
	Size  *struct {
		Default [2]float64 `json:"default"`
		Want    [2]float64 `json:"want"`
	} `json:"size"`
	Select *struct {
		OK     bool    `json:"ok"`
		Chart  string  `json:"chart"`
		X      string  `json:"x"`
		Series string  `json:"series"`
		Time   *string `json:"time"`
	} `json:"select"`
	Brush *struct {
		OK      bool            `json:"ok"`
		Chart   string          `json:"chart"`
		X0      string          `json:"x0"`
		X1      string          `json:"x1"`
		Times   json.RawMessage `json:"times"`
		Floats  json.RawMessage `json:"floats"`
		TimesIn *struct {
			Zone  string    `json:"zone"`
			Times [2]string `json:"times"`
		} `json:"timesIn"`
	} `json:"brush"`
}

// isError reports whether a fixture field holds the string "error".
func isError(raw json.RawMessage) bool { return strings.TrimSpace(string(raw)) == `"error"` }

// TestConformanceParams runs the server cases of the shared conformance
// fixture, which servers in every language read too.
func TestConformanceParams(t *testing.T) {
	b, err := os.ReadFile("../testdata/conformance/params.json")
	if err != nil {
		t.Fatal(err)
	}
	var fixture struct {
		Server []paramsCase `json:"server"`
	}
	if err := json.Unmarshal(b, &fixture); err != nil {
		t.Fatal(err)
	}
	if len(fixture.Server) == 0 {
		t.Fatal("no server cases")
	}
	for _, c := range fixture.Server {
		t.Run(c.Query, func(t *testing.T) {
			r := queryRequest(t, c.Query)
			checked := false
			if s := c.Size; s != nil {
				checked = true
				w, h := Size(r, s.Default[0], s.Default[1])
				if w != s.Want[0] || h != s.Want[1] {
					t.Errorf("Size = %v, %v; want %v", w, h, s.Want)
				}
			}
			if s := c.Select; s != nil {
				checked = true
				sel, ok := Selected(r)
				if ok != s.OK {
					t.Fatalf("Selected ok = %v, want %v", ok, s.OK)
				}
				if ok && (sel.Chart != s.Chart || sel.X != s.X || sel.Series != s.Series) {
					t.Errorf("Selected = %+v, want chart %q x %q series %q", sel, s.Chart, s.X, s.Series)
				}
				if s.Time != nil {
					tm, err := sel.Time()
					if err != nil || tm.Format(time.RFC3339Nano) != *s.Time {
						t.Errorf("Time = %v, %v; want %s", tm, err, *s.Time)
					}
				}
			}
			if s := c.Brush; s != nil {
				checked = true
				br, ok := Brushed(r)
				if ok != s.OK {
					t.Fatalf("Brushed ok = %v, want %v", ok, s.OK)
				}
				if !ok {
					return
				}
				if br.Chart != s.Chart || br.X0 != s.X0 || br.X1 != s.X1 {
					t.Errorf("Brushed = %+v, want chart %q x0 %q x1 %q", br, s.Chart, s.X0, s.X1)
				}
				checkTimes := func(name string, t0, t1 time.Time, err error, raw json.RawMessage) {
					if isError(raw) {
						if err == nil {
							t.Errorf("%s = %v, %v; want an error", name, t0, t1)
						}
						return
					}
					var want [2]string
					if e := json.Unmarshal(raw, &want); e != nil {
						t.Fatal(e)
					}
					got := [2]string{t0.Format(time.RFC3339Nano), t1.Format(time.RFC3339Nano)}
					if err != nil || got != want {
						t.Errorf("%s = %v, %v; want %v", name, got, err, want)
					}
				}
				if s.Times != nil {
					t0, t1, err := br.Times()
					checkTimes("Times", t0, t1, err, s.Times)
				}
				if s.TimesIn != nil {
					loc, err := time.LoadLocation(s.TimesIn.Zone)
					if err != nil {
						t.Fatal(err)
					}
					t0, t1, err := br.TimesIn(loc)
					want, _ := json.Marshal(s.TimesIn.Times)
					checkTimes("TimesIn", t0, t1, err, want)
				}
				if s.Floats != nil {
					x0, x1, err := br.Floats()
					if isError(s.Floats) {
						if err == nil {
							t.Errorf("Floats = %v, %v; want an error", x0, x1)
						}
					} else {
						var want [2]float64
						if e := json.Unmarshal(s.Floats, &want); e != nil {
							t.Fatal(e)
						}
						if err != nil || x0 != want[0] || x1 != want[1] {
							t.Errorf("Floats = %v, %v, %v; want %v", x0, x1, err, want)
						}
					}
				}
			}
			if !checked {
				t.Error("case checks nothing; is the fixture format out of date?")
			}
		})
	}
}

func TestSelected(t *testing.T) {
	tests := []struct {
		query string
		ok    bool
		want  Selection
	}{
		{"", false, Selection{}},
		{"hp-chart=bb", false, Selection{Chart: "bb"}},
		{"hp-x=Q1", true, Selection{X: "Q1"}},
		{"hp-x=Q1&hp-x=Q2", true, Selection{X: "Q1"}},
		{"hp-chart=bb&hp-x=+&hp-series=a%26b", true, Selection{Chart: "bb", X: " ", Series: "a&b"}},
		{"hp-x=%3Cscript%3E", true, Selection{X: "<script>"}},
		{"hp-x=%ZZ;hp-x=Q1", false, Selection{}}, // malformed query: nothing parses
	}
	for _, tt := range tests {
		t.Run(tt.query, func(t *testing.T) {
			r := queryRequest(t, "")
			r.URL.RawQuery = tt.query
			got, ok := Selected(r)
			if ok != tt.ok || got != tt.want {
				t.Errorf("Selected = %+v, %v; want %+v, %v", got, ok, tt.want, tt.ok)
			}
		})
	}
}

func TestGesturesReadForms(t *testing.T) {
	body := url.Values{ParamChart: {"lt"}, ParamX0: {"1"}, ParamX1: {"2"}, ParamX: {"Q1"}, "other": {"kept"}}.Encode()
	r := httptest.NewRequestWithContext(context.Background(), http.MethodPost, "/c?hp-series=s", strings.NewReader(body))
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	b, ok := Brushed(r)
	if !ok || b != (Brush{Chart: "lt", X0: "1", X1: "2"}) {
		t.Errorf("Brushed = %+v, %v", b, ok)
	}
	s, ok := Selected(r)
	if !ok || s != (Selection{Chart: "lt", X: "Q1", Series: "s"}) {
		t.Errorf("Selected = %+v, %v", s, ok)
	}
	if got := r.FormValue("other"); got != "kept" {
		t.Errorf("FormValue(other) after the readers = %q, want kept", got)
	}
	// A nil request or one without a URL reads nothing.
	if _, ok := Selected(nil); ok {
		t.Error("Selected(nil) reported a selection")
	}
	if _, ok := Brushed(&http.Request{}); ok {
		t.Error("Brushed of an empty request reported a brush")
	}
	if w, h := Size(nil, 1, 2); w != 1 || h != 2 {
		t.Errorf("Size(nil) = %v, %v", w, h)
	}
}

func TestParseNumber(t *testing.T) {
	tests := []struct {
		in   string
		want float64
		ok   bool
	}{
		{"0", 0, true},
		{"-0", 0, true},
		{"2", 2, true},
		{"-0.5", -0.5, true},
		{"1.7451", 1.7451, true},
		{"1e+21", 1e21, true},
		{"1E-7", 1e-7, true},
		{"1772546400000", 1772546400000, true},
		{"1e308", 1e308, true},
		{"", 0, false},
		{"-", 0, false},
		{"NaN", 0, false},
		{"nan", 0, false},
		{"Inf", 0, false},
		{"+Inf", 0, false},
		{"Infinity", 0, false},
		{"1e999", 0, false},
		{"-1e999", 0, false},
		{"1_0", 0, false},
		{"0x10", 0, false},
		{"01", 0, false},
		{"+1", 0, false},
		{" 1", 0, false},
		{"1 ", 0, false},
		{"1.", 0, false},
		{".5", 0, false},
		{"1e", 0, false},
		{"1e+", 0, false},
		{"1.5.5", 0, false},
		{"１", 0, false}, // fullwidth digit
	}
	for _, tt := range tests {
		t.Run(tt.in, func(t *testing.T) {
			got, err := parseNumber(tt.in)
			if (err == nil) != tt.ok || got != tt.want {
				t.Errorf("parseNumber(%q) = %v, %v; want %v, ok %v", tt.in, got, err, tt.want, tt.ok)
			}
			if err != nil && !strings.HasPrefix(err.Error(), "hyperplot: ") {
				t.Errorf("error %q lacks the hyperplot: prefix", err)
			}
		})
	}
}

func TestParseTime(t *testing.T) {
	ny, berlin, lordHowe := mustZone(t, "America/New_York"), mustZone(t, "Europe/Berlin"), mustZone(t, "Australia/Lord_Howe")
	tests := []struct {
		in     string
		loc    *time.Location
		want   string // RFC 3339 in UTC; "" for an error
		offset bool
	}{
		// Wall times a zone skips or repeats read as in Python's
		// parse_time_value (fold=0) and JavaScript's Date: the offset
		// before the change. New York skips 02:00-03:00 on 2026-03-08 and
		// repeats 01:00-02:00 on 2026-11-01.
		{"2026-03-08T01:50", ny, "2026-03-08T06:50:00Z", false},
		{"2026-03-08T02:00", ny, "2026-03-08T07:00:00Z", false},
		{"2026-03-08T02:10", ny, "2026-03-08T07:10:00Z", false},
		{"2026-03-08T02:30:00", ny, "2026-03-08T07:30:00Z", false},
		{"2026-03-08T03:00", ny, "2026-03-08T07:00:00Z", false},
		{"2026-03-08T03:30", ny, "2026-03-08T07:30:00Z", false},
		{"2026-11-01T00:59:59.5", ny, "2026-11-01T04:59:59.5Z", false},
		{"2026-11-01T01:00", ny, "2026-11-01T05:00:00Z", false},
		{"2026-11-01T01:30", ny, "2026-11-01T05:30:00Z", false},
		{"2026-11-01T02:00", ny, "2026-11-01T07:00:00Z", false},
		{"2026-11-01T02:30", ny, "2026-11-01T07:30:00Z", false},
		// East of UTC, where time.Date picks the other reading.
		{"2026-03-29T02:30", berlin, "2026-03-29T01:30:00Z", false},
		{"2026-10-25T02:30", berlin, "2026-10-25T00:30:00Z", false},
		{"2026-10-25T03:00", berlin, "2026-10-25T02:00:00Z", false},
		// A half-hour change: Lord Howe repeats 01:30-02:00 on 2026-04-05.
		{"2026-04-05T01:45", lordHowe, "2026-04-04T14:45:00Z", false},
		{"2026-04-05T02:00", lordHowe, "2026-04-04T15:30:00Z", false},
		{"0000-12-31T23:30:00-01:00", nil, "", false}, // year 0 as written, as in Python's datetime
		{"0000-12-31T23:30", nil, "", false},
		{"2026-03-03T14:05:00Z", nil, "2026-03-03T14:05:00Z", true},
		{"2026-03-03t14:05:00z", nil, "2026-03-03T14:05:00Z", true},
		{"2026-03-03T14:05:00.25Z", nil, "2026-03-03T14:05:00.25Z", true},
		{"2026-03-03T14:05:00.123456789123Z", nil, "2026-03-03T14:05:00.123456789Z", true},
		{"2026-03-03T15:05:00+01:00", nil, "2026-03-03T14:05:00Z", true},
		{"2026-03-03T09:35:00-04:30", nil, "2026-03-03T14:05:00Z", true},
		{"2026-03-03T14:05:00-00:00", nil, "2026-03-03T14:05:00Z", true},
		{"2026-03-03T14:05", nil, "2026-03-03T14:05:00Z", false},
		{"2026-03-03T14:05:30.5", nil, "2026-03-03T14:05:30.5Z", false},
		{"2026-03-03T14:05", ny, "2026-03-03T19:05:00Z", false},
		{"2026-07-03T14:05:00", ny, "2026-07-03T18:05:00Z", false},
		{"2026-03-03T14:05:00Z", ny, "2026-03-03T14:05:00Z", true}, // an offset wins over loc
		{"2024-02-29T00:00:00Z", nil, "2024-02-29T00:00:00Z", true},
		{"0001-01-01T00:00:00Z", nil, "0001-01-01T00:00:00Z", true},
		{"0000-01-01T00:00:00Z", nil, "", false},      // no year 0, as in Python's datetime
		{"0001-01-01T00:00:00+01:00", nil, "", false}, // year 0 in UTC
		{"9999-12-31T23:30:00-01:00", nil, "", false}, // year 10000 in UTC
		{"9999-12-31T23:59:59.999999999Z", nil, "9999-12-31T23:59:59.999999999Z", true},
		{"2026-03-03T14", nil, "", false},
		{"2026-03-03T14:05Z", nil, "", false},      // an offset needs seconds
		{"2026-03-03T14:05+01:00", nil, "", false}, // likewise
		{"2026-03-03 14:05:00Z", nil, "", false},
		{"2026-03-03T14:05:00", nil, "2026-03-03T14:05:00Z", false},
		{"2026-03-03T14:05:00.Z", nil, "", false},
		{"2026-03-03T14:05:00+0100", nil, "", false},
		{"2026-03-03T14:05:00+24:00", nil, "", false},
		{"2026-03-03T14:05:00+01:60", nil, "", false},
		{"2026-03-03T14:05:60Z", nil, "", false},
		{"2026-03-03T24:00:00Z", nil, "", false},
		{"2026-03-03T14:60:00Z", nil, "", false},
		{"2026-13-03T14:05:00Z", nil, "", false},
		{"2026-00-03T14:05:00Z", nil, "", false},
		{"2026-02-29T14:05:00Z", nil, "", false},
		{"2026-04-31T14:05:00Z", nil, "", false},
		{"2026-03-00T14:05:00Z", nil, "", false},
		{"2026-3-3T14:05:00Z", nil, "", false},
		{"+2026-03-03T14:05:00Z", nil, "", false},
		{"2026-03-03T14:05:00Zjunk", nil, "", false},
		{"1772546700000", nil, "", false},
		{"", nil, "", false},
		{"２026-03-03T14:05:00Z", nil, "", false},
	}
	for _, tt := range tests {
		t.Run(tt.in, func(t *testing.T) {
			got, offset, err := parseTime(tt.in, tt.loc)
			if tt.want == "" {
				if err == nil {
					t.Errorf("parseTime(%q) = %v; want an error", tt.in, got)
				}
				return
			}
			if err != nil || got.Format(time.RFC3339Nano) != tt.want || offset != tt.offset {
				t.Errorf("parseTime(%q) = %v, %v, %v; want %s, %v", tt.in, got.Format(time.RFC3339Nano), offset, err, tt.want, tt.offset)
			}
			if got.Location() != time.UTC {
				t.Errorf("parseTime(%q) is in %v, want UTC", tt.in, got.Location())
			}
		})
	}
}

func mustZone(tb testing.TB, name string) *time.Location {
	tb.Helper()
	loc, err := time.LoadLocation(name)
	if err != nil {
		tb.Fatal(err)
	}
	return loc
}

// TestParseTimeWallClock checks every quarter hour of wall time around
// the clock changes of several zones against a brute-force reading of
// fold=0 (PEP 495) and ECMAScript: of the instants whose wall time in the
// zone is the input, the earliest; when there is none (the clocks skip
// it), the input read with the offset in effect before the change.
func TestParseTimeWallClock(t *testing.T) {
	spans := []struct {
		zone     string
		from, to string // wall times, inclusive
	}{
		{"America/New_York", "2026-01-01T00:00", "2026-12-31T23:45"},
		{"Europe/Berlin", "2026-03-28T00:00", "2026-03-30T00:00"},
		{"Europe/Berlin", "2026-10-24T00:00", "2026-10-26T00:00"},
		{"Australia/Lord_Howe", "2026-04-04T00:00", "2026-04-06T00:00"},
		{"Australia/Lord_Howe", "2026-10-03T00:00", "2026-10-05T00:00"},
		{"Pacific/Apia", "2011-12-28T00:00", "2012-01-02T00:00"},      // skipped Dec 30, 2011
		{"America/Sao_Paulo", "2018-11-03T00:00", "2018-11-05T00:00"}, // midnight gap
		{"America/Sao_Paulo", "2019-02-15T00:00", "2019-02-18T00:00"}, // midnight repeat
		{"Asia/Kolkata", "2026-03-01T00:00", "2026-03-02T00:00"},
	}
	for _, sp := range spans {
		loc := mustZone(t, sp.zone)
		from, err := time.Parse("2006-01-02T15:04", sp.from)
		if err != nil {
			t.Fatal(err)
		}
		to, err := time.Parse("2006-01-02T15:04", sp.to)
		if err != nil {
			t.Fatal(err)
		}
		offsetAt := func(u time.Time) time.Duration {
			_, off := u.In(loc).Zone()
			return time.Duration(off) * time.Second
		}
		for w := from; !w.After(to); w = w.Add(15 * time.Minute) {
			// w is the wall time, held as if it were UTC.
			offsets := []time.Duration{offsetAt(w.Add(-30 * time.Hour)), offsetAt(w.Add(30 * time.Hour))}
			var want time.Time
			for _, o := range offsets {
				if u := w.Add(-o); offsetAt(u) == o && (want.IsZero() || u.Before(want)) {
					want = u
				}
			}
			if want.IsZero() {
				earliest := w.Add(-max(offsets[0], offsets[1]))
				want = w.Add(-offsetAt(earliest))
			}
			in := w.Format("2006-01-02T15:04")
			got, _, err := parseTime(in, loc)
			if err != nil || !got.Equal(want) {
				t.Errorf("%s in %s: parseTime = %v, %v; want %v", in, sp.zone, got, err, want)
			}
		}
	}
}

func TestSelectionTimeNeedsAnOffset(t *testing.T) {
	if _, err := (Selection{X: "2026-03-03T14:05"}).Time(); err == nil {
		t.Error("Selection.Time accepted a datetime-local value")
	}
	if v, err := (Selection{X: "2.5"}).Float(); err != nil || v != 2.5 {
		t.Errorf("Selection.Float = %v, %v", v, err)
	}
}

func TestBrushOrdersAndFails(t *testing.T) {
	b := Brush{X0: "2026-03-03T14:15:00Z", X1: "2026-03-03T14:15:00Z"}
	if t0, t1, err := b.Times(); err != nil || !t0.Equal(t1) {
		t.Errorf("equal ends: %v, %v, %v", t0, t1, err)
	}
	if t0, t1, err := b.TimesIn(nil); err != nil || !t0.Equal(t1) {
		t.Errorf("nil location: %v, %v, %v", t0, t1, err)
	}
	for _, b := range []Brush{{X0: "bad", X1: "2026-03-03T14:15:00Z"}, {X0: "2026-03-03T14:15:00Z", X1: "bad"}} {
		if t0, t1, err := b.Times(); err == nil || !t0.IsZero() || !t1.IsZero() {
			t.Errorf("Times(%+v) = %v, %v, %v; want zero times and an error", b, t0, t1, err)
		}
		if x0, x1, err := b.Floats(); err == nil || x0 != 0 || x1 != 0 {
			t.Errorf("Floats(%+v) = %v, %v, %v; want zeros and an error", b, x0, x1, err)
		}
	}
	if x0, x1, err := (Brush{X0: "3", X1: "bad"}).Floats(); err == nil || x0 != 0 || x1 != 0 {
		t.Errorf("Floats with a bad x1 = %v, %v, %v", x0, x1, err)
	}
	long := strings.Repeat("9", 10000) + "x"
	if _, err := parseNumber(long); err == nil || len(err.Error()) > 200 {
		t.Errorf("long input: error %d bytes long", len(err.Error()))
	}
}

// FuzzParseTime checks that the time parser never panics, returns UTC,
// and that whatever it accepts round-trips through the spec's time value
// form.
func FuzzParseTime(f *testing.F) {
	for _, s := range []string{
		"2026-03-03T14:05:00Z", "2026-03-03T15:05:00+01:00", "2026-03-03T14:05", "2026-03-03T14:05:30.5",
		"2026-03-03T14:05:00.123456789123-23:59", "2024-02-29T23:59:59z", "2026-03-03T14", "", "\xff",
	} {
		f.Add(s)
	}
	ny, err := time.LoadLocation("America/New_York")
	if err != nil {
		f.Fatal(err)
	}
	f.Fuzz(func(t *testing.T, s string) {
		for _, loc := range []*time.Location{nil, time.UTC, ny} {
			got, offset, err := parseTime(s, loc)
			if err != nil {
				if !got.IsZero() || offset {
					t.Fatalf("parseTime(%q) failed but returned %v, %v", s, got, offset)
				}
				continue
			}
			if got.Location() != time.UTC {
				t.Fatalf("parseTime(%q) = %v, not UTC", s, got)
			}
			canon := got.Format(time.RFC3339Nano)
			back, _, err := parseTime(canon, nil)
			if err != nil || !back.Equal(got) {
				t.Fatalf("parseTime(%q) = %s, which reparses as %v, %v", s, canon, back, err)
			}
			if offset {
				// The standard library agrees on what it can parse.
				if std, err := time.Parse(time.RFC3339Nano, strings.ToUpper(s)); err == nil && !std.Equal(got) {
					t.Fatalf("parseTime(%q) = %v; time.Parse gives %v", s, got, std)
				}
			}
		}
	})
}

// FuzzParseNumber checks that the number parser never panics, accepts
// only finite values, and that every finite float64 written in the
// JSON/ECMAScript form parses back to itself.
func FuzzParseNumber(f *testing.F) {
	for _, s := range []string{"0", "-0.5", "1e+21", "1E-7", "NaN", "Inf", "1e999", "1_0", "0x1p3", " 1", "-"} {
		f.Add(s, 1.5)
	}
	f.Fuzz(func(t *testing.T, s string, v float64) {
		got, err := parseNumber(s)
		if err == nil {
			if math.IsNaN(got) || math.IsInf(got, 0) {
				t.Fatalf("parseNumber(%q) = %v", s, got)
			}
			if !json.Valid([]byte(s)) {
				t.Fatalf("parseNumber(%q) accepted text that is not JSON", s)
			}
		}
		if math.IsNaN(v) || math.IsInf(v, 0) {
			return
		}
		js, _ := json.Marshal(v) // the spec's number form
		if back, err := parseNumber(string(js)); err != nil || back != v {
			t.Fatalf("parseNumber(%s) = %v, %v; want %v", js, back, err, v)
		}
		g := strconv.FormatFloat(v, 'g', -1, 64)
		if back, err := parseNumber(g); err == nil && back != v {
			t.Fatalf("parseNumber(%s) = %v; want %v", g, back, v)
		}
	})
}

// FuzzGestureParams checks that the request readers never panic on any
// query string or urlencoded body, that Size stays in bounds, and that
// the typed accessors either fail or return ordered, finite values.
func FuzzGestureParams(f *testing.F) {
	f.Add("hp-w=640&hp-chart=lt&hp-x0=2026-03-03T14%3A05%3A00Z&hp-x1=1", "hp-x=Q1")
	f.Add("hp-x0=NaN&hp-x1=-Inf&hp-w=1e999&hp-h=-0", "")
	f.Add("hp-x=%zz&;;&hp-x1", "hp-x0=1&hp-x1=0")
	f.Fuzz(func(t *testing.T, query, body string) {
		r := httptest.NewRequestWithContext(context.Background(), http.MethodPost, "/c", strings.NewReader(body))
		r.URL.RawQuery = query
		r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		w, h := Size(r, 640, 0)
		if w != 640 && (w < MinSize || w > MaxSize) || h != 0 && (h < MinSize || h > MaxSize) {
			t.Fatalf("Size = %v, %v", w, h)
		}
		if s, ok := Selected(r); ok {
			if s.X == "" {
				t.Fatal("Selected reported an empty hp-x")
			}
			_, _ = s.Time()
			_, _ = s.Float()
		}
		if b, ok := Brushed(r); ok {
			if t0, t1, err := b.Times(); err == nil && t1.Before(t0) {
				t.Fatalf("Times out of order: %v, %v", t0, t1)
			}
			if x0, x1, err := b.Floats(); err == nil && !(x0 <= x1) {
				t.Fatalf("Floats out of order or not finite: %v, %v", x0, x1)
			}
		}
	})
}
