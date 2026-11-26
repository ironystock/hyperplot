package hyperplot_test

import (
	"fmt"
	"html/template"
	"log"
	"net/http"
	"strings"
	"time"

	"github.com/ironystock/hyperplot"
)

func ExampleChart_Render() {
	start := time.Date(2026, time.March, 3, 14, 0, 0, 0, time.UTC)
	x := make(hyperplot.Times, 5)
	for i := range x {
		x[i] = start.Add(time.Duration(i) * 15 * time.Minute)
	}
	chart := hyperplot.Chart{
		ID:    "latency",
		Title: "Request latency",
		Y:     hyperplot.Axis{Label: "Latency", Unit: " ms"},
		Marks: []hyperplot.Mark{
			hyperplot.Line{Name: "p50", X: x, Y: []float64{42, 45, 41, 48, 44}},
			hyperplot.Line{Name: "p99", X: x, Y: []float64{98, 121, 104, 152, 112}},
		},
	}
	svg, err := chart.Render()
	if err != nil {
		log.Fatal(err)
	}
	s := string(svg)
	fmt.Println(strings.HasPrefix(s, `<svg xmlns="http://www.w3.org/2000/svg" id="latency" class="hp"`))
	start2 := strings.Index(s, "<desc")
	end := strings.Index(s, "</desc>")
	fmt.Println(s[strings.Index(s[start2:], ">")+start2+1 : end])
	// Output:
	// true
	// Line chart with 2 series: p50, p99. X axis time from Mar 3, 2026 2:00 PM to Mar 3, 2026 3:00 PM. Y axis Latency, 40 to 160 ms.
}

func ExampleChart_Render_bars() {
	quarters := hyperplot.Categories{"Q1", "Q2", "Q3", "Q4"}
	chart := hyperplot.Chart{
		Title: "Revenue",
		Stack: true,
		Marks: []hyperplot.Mark{
			hyperplot.Bar{Name: "Products", X: quarters, Y: []float64{120, 135, 150, 170}},
			hyperplot.Bar{Name: "Services", X: quarters, Y: []float64{40, 42, 55, 61}},
			hyperplot.RuleY{Y: 200, Label: "Target"},
		},
	}
	// WriteTo streams the SVG to any io.Writer, such as an
	// http.ResponseWriter.
	var b strings.Builder
	n, err := chart.WriteTo(&b)
	if err != nil {
		log.Fatal(err)
	}
	svg := b.String()
	fmt.Println(n == int64(len(svg)))
	fmt.Println(strings.Count(svg, `class="hp-bar"`), "bars")
	fmt.Println(strings.Contains(svg, ">Target</text>"))
	// Output:
	// true
	// 8 bars
	// true
}

func ExampleChart_HTML() {
	tmpl := template.Must(template.New("page").Parse(`<figure>{{.Chart}}<details><summary>Data</summary>{{.Table}}</details></figure>`))
	chart := &hyperplot.Chart{
		Title: "Signups",
		Marks: []hyperplot.Mark{hyperplot.Bar{Name: "Signups", X: hyperplot.Categories{"Mon", "Tue"}, Y: []float64{12, 19}}},
	}
	svg, err := chart.HTML()
	if err != nil {
		log.Fatal(err)
	}
	table, err := chart.Table()
	if err != nil {
		log.Fatal(err)
	}
	var b strings.Builder
	if err := tmpl.Execute(&b, map[string]template.HTML{"Chart": svg, "Table": table}); err != nil {
		log.Fatal(err)
	}
	out := b.String()
	if i := strings.Index(out, "<details>"); i >= 0 {
		fmt.Println(out[i:])
	}
	// Output:
	// <details><summary>Data</summary><table class="hp-table"><caption>Signups</caption><thead><tr><th scope="col">X</th><th scope="col">Signups</th></tr></thead><tbody><tr><th scope="row">Mon</th><td>12</td></tr><tr><th scope="row">Tue</th><td>19</td></tr></tbody></table></details></figure>
}

func ExampleChart_Table() {
	chart := &hyperplot.Chart{
		Title: "Error rate",
		Y:     hyperplot.Axis{Unit: "%"},
		Marks: []hyperplot.Mark{
			hyperplot.Line{Name: "api", X: hyperplot.Floats{1, 2}, Y: []float64{0.5, 1.25}},
		},
	}
	table, err := chart.Table()
	if err != nil {
		log.Fatal(err)
	}
	fmt.Println(table)
	// Output:
	// <table class="hp-table"><caption>Error rate</caption><thead><tr><th scope="col">X</th><th scope="col">api</th></tr></thead><tbody><tr><th scope="row">1</th><td>0.5%</td></tr><tr><th scope="row">2</th><td>1.25%</td></tr></tbody></table>
}

func ExampleSparkline() {
	spark := hyperplot.Sparkline{Values: []float64{3, 4, 2, 6}, Label: "Requests per second", Width: 60, Height: 20}
	svg, err := spark.Render()
	if err != nil {
		log.Fatal(err)
	}
	fmt.Println(string(svg))
	// Output:
	// <svg xmlns="http://www.w3.org/2000/svg" class="hp hp-spark" viewBox="0 0 60 20" width="60" height="20" role="img" aria-label="Requests per second: first 3, min 2, max 6, last 6"><g class="hp-s1"><path class="hp-line" d="M4,13L21.33,10L38.67,16L56,4" fill="none" stroke="currentColor" stroke-width="2" stroke-linejoin="round" stroke-linecap="round"/><circle class="hp-end" cx="56" cy="4" r="2.5" fill="currentColor"/></g></svg>
}

func ExampleSparkline_HTML() {
	// HTML returns template.HTML, so a template embeds the sparkline
	// without a conversion of its own.
	tile := template.Must(template.New("tile").Parse(`<div class="tile"><b>{{.Value}}</b>{{.Spark}}</div>`))
	spark, err := hyperplot.Sparkline{Values: []float64{198, 180, 240, 212}, Label: "p99 latency", Unit: " ms"}.HTML()
	if err != nil {
		log.Fatal(err)
	}
	var b strings.Builder
	if err := tile.Execute(&b, map[string]any{"Value": "212 ms", "Spark": spark}); err != nil {
		log.Fatal(err)
	}
	fmt.Println(strings.Contains(b.String(), `aria-label="p99 latency: first 198 ms, min 180 ms, max 240 ms, last 212 ms"`))
	// Output: true
}

func ExampleStylesheet() {
	// Serve the stylesheet once; every chart on the page uses it.
	http.HandleFunc("/hyperplot.css", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/css; charset=utf-8")
		fmt.Fprint(w, hyperplot.Stylesheet())
	})
}

func ExampleTheme_CSS() {
	theme := hyperplot.DefaultTheme()
	theme.Light.Series[0] = "#0b6bcb" // a brand blue for slot 1
	css := theme.CSS()
	fmt.Println(strings.Contains(css, "--hp-series-1:#0b6bcb;"))
	// Output: true
}

func ExampleMap() {
	type row struct {
		Day   string
		Count float64
	}
	rows := []row{{"Mon", 3}, {"Tue", 5}}
	bar := hyperplot.Bar{
		Name: "Count",
		X:    hyperplot.Map(rows, func(r row) string { return r.Day }),
		Y:    hyperplot.Map(rows, func(r row) float64 { return r.Count }),
	}
	fmt.Println(bar.X, bar.Y)
	// Output: [Mon Tue] [3 5]
}
