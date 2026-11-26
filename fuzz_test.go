package hyperplot

import (
	"encoding/json"
	"math"
	"strconv"
	"strings"
	"testing"
	"time"
	_ "time/tzdata" // zones with clock changes for FuzzRender, even without system zoneinfo
)

// fuzzZones are the time zones FuzzRender picks from. The embedded tzdata
// makes the zones with daylight-saving changes load everywhere.
var fuzzZones = []*time.Location{
	time.FixedZone("odd", -(5*3600 + 30*60)),
	mustZone("America/New_York"),
	mustZone("Australia/Lord_Howe"),
}

func mustZone(name string) *time.Location {
	loc, err := time.LoadLocation(name)
	if err != nil {
		panic(err)
	}
	return loc
}

// FuzzRender builds charts of every kind from fuzzed names, categories,
// values and options, and checks that rendering never panics and that
// any chart it accepts is well-formed XML with a valid tooltip payload
// and no non-finite numbers.
//
// flags picks the x kind, the mark kinds and boolean options; shape
// picks the number of series (1–8) and points (0–11), and the layout
// options that depend on them: fixed X and Y domains, font size, bar
// width, legend mode, slots, tick counts and the time zone. Points cycle
// through a, b and c with a growing offset, so x values repeat, collide
// and fall outside fixed domains.
func FuzzRender(f *testing.F) {
	f.Add("p50", "Mon", 1.0, 2.0, 3.0, uint8(0), uint32(0))
	f.Add(`<script>"&'`, "", math.NaN(), math.Inf(1), math.Inf(-1), uint8(1), uint32(1))
	f.Add("", "\x00\xff", 0.0, 0.0, 0.0, uint8(2), uint32(2))
	f.Add("\U0001F600 emoji", "日本", -1e300, 1e300, 5e-324, uint8(3), uint32(3))
	f.Add("a", "a", -5.0, 0.0, 5.0, uint8(255), uint32(0xffffffff))
	f.Add("x", "]]>", math.MaxFloat64, -math.MaxFloat64, 1.0, uint8(77), uint32(77))
	f.Add("eu-central", "region", 10.0, 20.0, 30.0, uint8(4), uint32(7+8*11+96*0b1111111)) // 8 series, 11 points, every option
	f.Add("live", "now", 1.7e9, 1.7e9+60, 1.7e9+125, uint8(1), uint32(3+8*5+96*0b11))      // fixed time and Y domains
	f.Add("tiny", "fs", 1.0, 2.0, 3.0, uint8(5), uint32(5+8*9+96*0b100))                   // small font, narrow chart
	// Times in the repeated hour after a fall-back (New York, 1:40 AM
	// EST) and in Lord Howe's repeated half hour.
	f.Add("fall back", "dst", 1793515200.0, 1793515201.0, 1793515202.0, uint8(1), uint32(8*5+96*(128+256*1)))
	f.Add("lord howe", "dst", 1775315700.0, 1775316600.0, 1775317500.0, uint8(1), uint32(8*6+96*(128+256*2)))
	// Two points an hour apart with one wall time, 1:40 AM EDT and EST
	// (both with the nanoseconds of b).
	f.Add("repeat", "dst", 1793511600.0, 1793515200.0, 2793515200.0, uint8(1), uint32(8*3+96*(128+256*1)))
	// Times in years -249 and 0, which RFC 3339 cannot write as keys.
	f.Add("n", "c", -7e10, -7e10, -7e10, uint8(1), uint32(16))
	f.Add("n", "c", -6.2e10, -6.2e10, -6.2e10, uint8(1), uint32(16))
	// A time domain under a microsecond wide (5 s + 5 ns to 5 s + 1 µs).
	f.Add("us", "span", 5.2, 5.7, 1000.0, uint8(1), uint32(2*8))
	// Categories "x\xfe1" and "x\xff1", which the metadata writes alike,
	// on a line and on bars.
	f.Add("x\xfe1", "x\xff", 1.0, 2.0, 3.0, uint8(2), uint32(2*8))
	f.Add("x\xfe1", "x\xff", 1.0, 2.0, 3.0, uint8(11), uint32(2*8))
	f.Fuzz(func(t *testing.T, name, cat string, a, b, c float64, flags uint8, shape uint32) {
		vals := [3]float64{a, b, c}
		at := func(i int) float64 { return vals[i%3] + float64(i/3) }
		nSeries, nPts, opt := 1+int(shape%8), int(shape/8%12), shape/96
		xKind := flags % 3

		var x Values
		switch xKind {
		case 0:
			fs := make(Floats, nPts)
			for i := range fs {
				fs[i] = at(i)
			}
			x = fs
		case 1:
			ts := make(Times, nPts)
			for i := range ts {
				ts[i] = time.Unix(int64(at(i))%1e11, int64(math.Abs(at(i+1)))%1e9)
			}
			x = ts
		default:
			cs := make(Categories, nPts)
			for i := range cs {
				cs[i] = cat + strconv.Itoa(i)
			}
			if nPts > 0 {
				cs[0] = name
				if strings.HasPrefix(name, cat) {
					cs[0] = "#" + name // keep categories unique for bars
				}
			}
			x = cs
		}
		var marks []Mark
		for s := range nSeries {
			y := make([]float64, nPts)
			for i := range y {
				y[i] = at(i + s)
			}
			sname := name
			if s > 0 {
				sname = cat + " " + strconv.Itoa(s)
			}
			slot := 0
			if opt&32 != 0 {
				slot = 8 - s
			}
			kinds := 3
			if xKind == 2 {
				kinds = 4
			}
			switch (int(flags)/3 + s) % kinds {
			case 0:
				marks = append(marks, Line{Name: sname, X: x, Y: y, Curve: Curve(s % 3), Points: flags&8 != 0, Slot: slot})
			case 1:
				var base []float64
				if flags&8 != 0 {
					base = make([]float64, nPts)
					for i := range base {
						base[i] = at(i + 2)
					}
				}
				marks = append(marks, Area{Name: sname, X: x, Y: y, Base: base, Slot: slot})
			case 2:
				var labels []string
				if nPts > 0 && flags&8 != 0 {
					labels = make([]string, nPts)
					for i := range labels {
						labels[i] = []string{name, cat, ""}[i%3]
					}
				}
				marks = append(marks, Dot{Name: sname, X: x, Y: y, Labels: labels, Slot: slot})
			default:
				marks = append(marks, Bar{Name: sname, X: x.(Categories), Y: y, Slot: slot})
			}
		}
		marks = append(marks, RuleY{Y: b, Label: name})

		ch := &Chart{
			Title: name, Description: cat, Marks: marks,
			Stack: flags&16 != 0, Standalone: flags&32 != 0, ShowTitle: flags&64 != 0,
			Width: 50 + math.Mod(math.Abs(c), 1000), Height: 50 + math.Mod(math.Abs(b), 500),
			X: Axis{Label: cat, Hide: flags&128 != 0},
			Y: Axis{Label: name, Unit: cat},
		}
		if math.IsNaN(ch.Width) {
			ch.Width = 0
		}
		if math.IsNaN(ch.Height) {
			ch.Height = 0
		}
		if opt&1 != 0 && valid(a) && valid(b) {
			ch.Y.Domain = [2]float64{a, b}
			ch.X.Domain = [2]float64{b, c}
		}
		if opt&2 != 0 {
			t0 := time.Unix(int64(a)%1e11, 0)
			ch.X.TimeDomain = [2]time.Time{t0, t0.Add(time.Duration(int64(c)%1e6) * time.Second)}
		}
		if opt&4 != 0 {
			ch.FontSize = 4 + float64(shape>>24%93) // 4..96
		}
		if opt&8 != 0 {
			ch.BarWidth = 1 + math.Mod(math.Abs(a), 100)
		}
		if math.IsNaN(ch.BarWidth) {
			ch.BarWidth = 0
		}
		ch.Legend = LegendMode(opt >> 4 % 3)
		if opt&64 != 0 {
			ch.X.Ticks, ch.Y.Ticks = int(shape>>20%30), int(shape>>25%30)
			ch.ID = "c-" + strconv.Itoa(int(shape))
		}
		if opt&128 != 0 {
			// A fixed odd offset, and zones whose clocks change (Lord Howe
			// by 30 minutes), so ticks near a transition are reachable.
			ch.Location = fuzzZones[opt>>8%uint32(len(fuzzZones))]
		}

		out, err := ch.Render()
		if err != nil {
			return
		}
		inputs := func() string {
			return strconv.Quote(name) + " " + strconv.Quote(cat) + " " +
				strconv.FormatFloat(a, 'g', -1, 64) + " " + strconv.FormatFloat(b, 'g', -1, 64) + " " +
				strconv.FormatFloat(c, 'g', -1, 64) + " flags=" + strconv.Itoa(int(flags)) + " shape=" + strconv.Itoa(int(shape))
		}
		if _, err := parseSVG(out); err != nil {
			t.Fatalf("inputs %s: %v\n%s", inputs(), err, out)
		}
		if s := string(out); strings.Contains(s, "NaN") || strings.Contains(s, "Inf") {
			if !strings.Contains(name+cat, "NaN") && !strings.Contains(name+cat, "Inf") {
				t.Fatalf("inputs %s: output contains a non-finite number\n%s", inputs(), out)
			}
		}
		table, err := ch.Table()
		if err != nil {
			t.Fatalf("inputs %s: Table failed where Render succeeded: %v", inputs(), err)
		}
		// The wire spec's MUSTs hold for any input: the geometry
		// attributes parse, every row has a key in the x domain, the table
		// has a row per key, no raw CR, no transform where the slide moves
		// things, and every id is unique (speccheck_test.go).
		if v := checkSpec(out, []byte(table)); len(v) > 0 {
			t.Fatalf("inputs %s: spec violations:\n%s\n%s\n%s", inputs(), strings.Join(v, "\n"), out, table)
		}
		// Distinct times read differently in tooltips, keyboard readings
		// and table rows (Axis.TimeValueFormat), also in the hour a zone
		// repeats when its clocks go back.
		if xKind == 1 {
			const open, end = `<metadata class="hp-tips">`, `</metadata>`
			s := string(out)
			if i := strings.Index(s, open); i >= 0 {
				var tips wireTipsPayload
				body, _, _ := strings.Cut(s[i+len(open):], end)
				if err := json.Unmarshal([]byte(body), &tips); err != nil {
					t.Fatalf("inputs %s: %v", inputs(), err)
				}
				if tips.Mode == "x" {
					seen := map[string]string{}
					for _, r := range tips.Rows {
						if k, ok := seen[*r.T]; ok && k != *r.K {
							t.Fatalf("inputs %s: rows %s and %s both read %q", inputs(), k, *r.K, *r.T)
						}
						seen[*r.T] = *r.K
					}
				}
			}
		}
		// Ticks lie inside the domain, so their labels sit over the plot
		// (an hour early in a repeated DST hour, they ran off the chart).
		if p, err := newPlan(ch); err == nil {
			for _, tk := range p.xTicks {
				if tk.v < p.l-0.01 || tk.v > p.r+0.01 {
					t.Fatalf("inputs %s: x tick %q at %v is outside the plot [%v, %v]", inputs(), tk.text, tk.v, p.l, p.r)
				}
			}
		}
		if _, err := (Sparkline{Values: vals[:], Label: name, Area: true}).Render(); err != nil {
			t.Fatalf("inputs %s: Sparkline: %v", inputs(), err)
		}
	})
}
