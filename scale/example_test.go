package scale_test

import (
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/ironystock/hyperplot/scale"
)

// A vertical axis maps data onto pixels from the bottom up. Nice rounds
// the domain out to tick boundaries, so the axis starts and ends on a
// labeled tick.
func ExampleLinear_Ticks() {
	y := scale.NewLinear(3, 97, 300, 0).Nice(5)
	fmt.Println(y.Domain)
	for _, v := range y.Ticks(5) {
		fmt.Printf("%v at %vpx\n", v, y.Map(v))
	}
	// Output:
	// [0 100]
	// 0 at 300px
	// 20 at 240px
	// 40 at 180px
	// 60 at 120px
	// 80 at 60px
	// 100 at 0px
}

// TickFormat picks one format for a whole set of ticks: the fewest
// decimals that tell them apart, a compact unit for large values and a
// typographic minus sign.
func ExampleTickFormat() {
	for _, ticks := range [][]float64{
		{0, 0.5, 1},
		{0, 0.25, 0.5},
		{-20000, 0, 20000, 40000},
		{0, 5e17, 1e18},
	} {
		format := scale.TickFormat(ticks)
		labels := make([]string, len(ticks))
		for i, t := range ticks {
			labels[i] = format(t)
		}
		fmt.Println(strings.Join(labels, ", "))
	}
	// Output:
	// 0, 0.5, 1.0
	// 0, 0.25, 0.50
	// −20K, 0, 20K, 40K
	// 0, 0.5×10¹⁸, 1.0×10¹⁸
}

// FormatNumber and FormatCompact format single values, for tooltips and
// stat tiles.
func ExampleFormatNumber() {
	fmt.Println(scale.FormatNumber(1234567.891, 2))
	fmt.Println(scale.FormatNumber(-0.5, 1))
	fmt.Println(scale.FormatCompact(1234567, 1))
	// Output:
	// 1,234,567.89
	// −0.5
	// 1.2M
}

// TickFormatFor labels each tick at its own precision: hour ticks show
// the hour, and the tick at midnight shows the date instead.
func ExampleTime_TickFormatFor() {
	t0 := time.Date(2026, time.March, 2, 18, 0, 0, 0, time.UTC)
	t1 := time.Date(2026, time.March, 3, 12, 0, 0, 0, time.UTC)
	x := scale.NewTime(t0, t1, 0, 600)
	format := x.TickFormatFor(5)
	labels := []string{}
	for _, t := range x.Ticks(5) {
		labels = append(labels, format(t))
	}
	fmt.Println(strings.Join(labels, ", "))
	// Output:
	// 6 PM, 9 PM, Mar 3, 3 AM, 6 AM, 9 AM, 12 PM
}

// FormatterFor returns full labels for tooltips and tables. It takes the
// data's own times, as an iterator, so that times finer than the tick
// interval still print apart: here the axis has hourly ticks, but two
// samples fall within the same minute.
func ExampleTime_FormatterFor() {
	t0 := time.Date(2026, time.March, 3, 9, 0, 0, 0, time.UTC)
	data := []time.Time{
		t0,
		t0.Add(90 * time.Minute),
		t0.Add(90*time.Minute + 30*time.Second),
		t0.Add(6 * time.Hour),
	}
	x := scale.NewTime(data[0], data[len(data)-1], 0, 600)

	coarse := x.Formatter(5)
	fine := x.FormatterFor(5, slices.Values(data))
	for _, t := range data {
		fmt.Printf("%-24s %s\n", coarse(t), fine(t))
	}
	// Output:
	// Mar 3, 2026 9:00 AM      Mar 3, 2026 9:00:00 AM
	// Mar 3, 2026 10:30 AM     Mar 3, 2026 10:30:00 AM
	// Mar 3, 2026 10:30 AM     Mar 3, 2026 10:30:30 AM
	// Mar 3, 2026 3:00 PM      Mar 3, 2026 3:00:00 PM
}

// A Band scale lays out bar chart categories. Map returns the start of a
// category's band; Bandwidth is the width of each bar.
func ExampleNewBand() {
	x := scale.NewBand([]string{"eu-west", "us-east", "ap-south"}, 0, 300)
	for _, c := range x.Domain {
		start, _ := x.Map(c)
		fmt.Printf("%-8s %6.2f to %6.2f\n", c, start, start+x.Bandwidth())
	}
	_, ok := x.Map("sa-east")
	fmt.Println("sa-east found:", ok)
	// Output:
	// eu-west   10.00 to  90.00
	// us-east  110.00 to 190.00
	// ap-south 210.00 to 290.00
	// sa-east found: false
}

// The padding fields are plain struct fields: set PaddingInner to 0 for
// bands that touch, as in a histogram.
func ExampleBand() {
	x := scale.NewBand([]string{"a", "b", "c", "d"}, 0, 400)
	x.PaddingInner, x.PaddingOuter = 0, 0
	fmt.Println(x.Step(), x.Bandwidth())
	fmt.Println(x.MapIndex(2))
	// Output:
	// 100 100
	// 200
}
