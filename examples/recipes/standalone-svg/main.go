// Command standalone-svg writes charts as self-contained SVG files, for
// places with no page stylesheet and no script: a README, a report, a
// slide, an <img> tag, an export button.
//
//	go run ./examples/recipes/standalone-svg                           # writes to a temp directory
//	go run ./examples/recipes/standalone-svg -out ../hyperplot-charts  # or to a directory you pick
//
// Pick a directory outside the repository, so the generated files don't
// show up in git status. A relative path works in every shell; Windows
// PowerShell and cmd.exe do not expand ~.
//
// The pattern: set Chart.Standalone and render. The file then carries the
// theme's stylesheet in a <style> element, a background, a margin and the
// visible title, so it looks right on its own. Its colors still follow the
// reader's light or dark preference (the stylesheet's media query); set
// Chart.Theme to pick other colors, here a palette that is always dark.
// Shown on its own or through <img>, a chart gets only the native fallback
// (spec/spec.md §3.7): no tooltips or keyboard reading, which need the
// chart inline in a page with hx-plot.js. The same data always gives the
// same bytes, so committed files diff cleanly.
package main

import (
	"bytes"
	"flag"
	"fmt"
	"html/template"
	"log"
	"os"
	"path/filepath"
	"time"

	"github.com/ironystock/hyperplot/plot"
)

// index is a page that shows the files with <img> tags, as an email or a
// report would. An <img> hides the SVG's own <title> and <desc> from
// screen readers, so every tag needs alt text.
var index = template.Must(template.New("index").Parse(`<!doctype html>
<html lang="en">
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<meta name="color-scheme" content="light dark">
<meta http-equiv="Content-Security-Policy" content="default-src 'none'; img-src 'self'; style-src 'self'">
<title>Standalone SVG charts</title>
<link rel="stylesheet" href="index.css">
<h1>Standalone SVG charts</h1>
{{range .}}<h2>{{.File}}</h2>
<p><img src="{{.File}}" width="{{.Width}}" height="{{.Height}}" alt="{{.Alt}}"></p>
{{end}}</html>
`))

// indexCSS lets the images shrink to a narrow window, such as a phone's,
// instead of widening the page. The width and height attributes keep the
// aspect ratio. It is a file rather than a <style> element, so the page's
// Content-Security-Policy stays strict.
const indexCSS = "img { max-width: 100%; height: auto }\n"

// file is one chart written to the output directory.
type file struct {
	File          string
	Width, Height float64
	Alt           string
}

func main() {
	out := flag.String("out", filepath.Join(os.TempDir(), "hyperplot-standalone"), "directory to write the files to")
	flag.Parse()
	files, err := write(*out)
	if err != nil {
		log.Fatal(err)
	}
	log.Printf("wrote %d charts to %s; open %s in a browser", len(files), *out, filepath.Join(*out, "index.html"))
}

// write renders every chart into dir, plus index.html, and returns the charts.
func write(dir string) ([]file, error) {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, err
	}
	d := plot.DefaultTheme()
	dark := plot.Theme{Light: d.Dark, Dark: d.Dark} // dark whatever the reader prefers

	charts := []struct {
		name  string
		chart *plot.Chart
	}{
		{"latency.svg", latencyChart(nil)},        // nil: the default theme
		{"latency-dark.svg", latencyChart(&dark)}, // the same chart, always dark
		{"deploys.svg", deploysChart()},
	}
	var files []file
	for _, f := range charts {
		c := f.chart
		c.Standalone = true // embed the stylesheet, draw a background and the title
		svg, err := c.Render()
		if err != nil {
			return nil, fmt.Errorf("%s: %w", f.name, err)
		}
		if err := os.WriteFile(filepath.Join(dir, f.name), svg, 0o644); err != nil {
			return nil, err
		}
		files = append(files, file{File: f.name, Width: c.Width, Height: c.Height, Alt: c.Title})
	}
	var b bytes.Buffer
	if err := index.Execute(&b, files); err != nil {
		return nil, err
	}
	if err := os.WriteFile(filepath.Join(dir, "index.css"), []byte(indexCSS), 0o644); err != nil {
		return nil, err
	}
	return files, os.WriteFile(filepath.Join(dir, "index.html"), b.Bytes(), 0o644)
}

// latencyChart is a fixed hour of latency in the given theme (nil for the
// default). It is a report, not a live view, so it reads no clock.
func latencyChart(theme *plot.Theme) *plot.Chart {
	start := time.Date(2026, 3, 3, 14, 0, 0, 0, time.UTC)
	p50 := []float64{92, 95, 91, 98, 104, 101, 97, 99, 110, 118, 112, 104, 99}
	p95 := []float64{188, 193, 185, 204, 231, 226, 214, 219, 262, 281, 247, 222, 210}
	xs := make(plot.Times, len(p50))
	for i := range xs {
		xs[i] = start.Add(time.Duration(i) * 5 * time.Minute)
	}
	return &plot.Chart{
		ID:     "latency", // also the prefix of the ids inside the file
		Title:  "API latency, Mar 3, 2026",
		Width:  640,
		Height: 300,
		Theme:  theme, // used only by Standalone: inline charts take the page's stylesheet
		Y:      plot.Axis{Label: "ms", Unit: " ms", Zero: true},
		Marks: []plot.Mark{
			plot.Line{Name: "p50", X: xs, Y: p50},
			plot.Line{Name: "p95", X: xs, Y: p95},
			plot.RuleY{Y: 250, Label: "250 ms objective"},
		},
	}
}

// deploysChart is a week of deploys per day, as bars.
func deploysChart() *plot.Chart {
	return &plot.Chart{
		ID:     "deploys",
		Title:  "Deploys per day, week 10",
		Width:  480,
		Height: 260,
		Marks: []plot.Mark{plot.Bar{
			Name: "deploys",
			X:    plot.Categories{"Mon", "Tue", "Wed", "Thu", "Fri"},
			Y:    []float64{12, 19, 7, 15, 22},
		}},
	}
}
