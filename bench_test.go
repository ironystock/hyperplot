package hyperplot

import (
	"math"
	"strconv"
	"testing"
	"time"
)

func benchTimes(n int) Times {
	x := make(Times, n)
	for i := range x {
		x[i] = t0.Add(time.Duration(i) * time.Second)
	}
	return x
}

func benchValues(n int, phase float64) []float64 {
	y := make([]float64, n)
	for i := range y {
		y[i] = 50 + 30*math.Sin(float64(i)/20+phase) + float64(i%7)
	}
	return y
}

func benchRender(b *testing.B, c *Chart) {
	b.Helper()
	b.ReportAllocs()
	for b.Loop() {
		if _, err := c.Render(); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkRenderLine1000(b *testing.B) {
	benchRender(b, &Chart{Title: "One line", Marks: []Mark{Line{Name: "a", X: benchTimes(1000), Y: benchValues(1000, 0)}}})
}

func BenchmarkRenderLines8x200(b *testing.B) {
	x := benchTimes(200)
	var marks []Mark
	for i := range 8 {
		marks = append(marks, Line{Name: "series " + strconv.Itoa(i), X: x, Y: benchValues(200, float64(i)), Curve: CurveMonotone})
	}
	benchRender(b, &Chart{Title: "Eight lines", Marks: marks})
}

func BenchmarkRenderBars50(b *testing.B) {
	cats := make(Categories, 50)
	for i := range cats {
		cats[i] = "c" + strconv.Itoa(i)
	}
	benchRender(b, &Chart{Title: "Bars", Marks: []Mark{Bar{Name: "a", X: cats, Y: benchValues(50, 0)}}})
}

func BenchmarkSparkline(b *testing.B) {
	s := Sparkline{Values: benchValues(60, 0), Area: true}
	b.ReportAllocs()
	for b.Loop() {
		if _, err := s.Render(); err != nil {
			b.Fatal(err)
		}
	}
}
