# hyperplot

**Server-rendered SVG charts for Go, live with htmx 4.**

[![Go Reference](https://pkg.go.dev/badge/github.com/ironystock/hyperplot.svg)](https://pkg.go.dev/github.com/ironystock/hyperplot)
[![CI](https://github.com/ironystock/hyperplot/actions/workflows/ci.yml/badge.svg)](https://github.com/ironystock/hyperplot/actions/workflows/ci.yml)
[![Go Report Card](https://goreportcard.com/badge/github.com/ironystock/hyperplot)](https://goreportcard.com/report/github.com/ironystock/hyperplot)
[![License: MIT](https://img.shields.io/badge/license-MIT-blue.svg)](LICENSE)

hyperplot turns Go data into clean, accessible SVG on the server. You put the
SVG in your HTML like any other fragment, and htmx 4 keeps it live: polling,
server-sent events and filters all re-render on the server and **morph** the
chart in place, so bars and lines animate to their new values instead of
blinking. There is no JavaScript charting framework, no JSON API to design,
and no build step.

```go
chart := hyperplot.Chart{
	Title: "Latency",
	Y:     hyperplot.Axis{Label: "ms", Unit: " ms"},
	Marks: []hyperplot.Mark{
		hyperplot.Line{Name: "p50", X: hyperplot.Times(times), Y: p50},
		hyperplot.Line{Name: "p99", X: hyperplot.Times(times), Y: p99},
	},
}
svg, err := chart.Render() // []byte of well-formed, deterministic SVG
```

> **Status: pre-1.0, under active development.** Nothing has been tagged or
> published yet, and the API may change before v1.0.0. Feedback on the API is
> very welcome. See [Status](#status).

## Features

- **Line, area, bar, dot and rule marks** that combine in one plot, with
  grouped or stacked bars and areas, three curve types, and gaps for missing
  (`NaN`) values.
- **Linear, time and category axes** with "nice" ticks, DST-safe time ticks,
  and number formatting that uses a real minus sign (−). Labels follow US
  conventions by default ("Mar 3", "2 PM"); custom formatters change them.
- **Good defaults from data-viz practice**: a validated, colorblind-aware
  8-color palette in fixed order, recessive gridlines, rounded bar ends,
  direct end labels when they fit, and a legend only when you need one.
- **Live updates with htmx 4**: deterministic output with stable element ids,
  so `hx-swap="innerMorph"` updates a chart in place and CSS transitions
  animate the change.
- **An `hx` helper package** for content tags (`HX-PTag` with `304 Not
  Modified`), server-sent event streams, multi-target partials, and
  responsive sizing.
- **Accessible**: an accessible name and description on every chart, keyboard
  focus (with arrow-key reading via the client extension), and an equivalent
  `<table>` of the data.
- **Optional client extension** (one small file, no dependencies) for hover
  tooltips, a crosshair, keyboard reading and responsive re-rendering.
- **Sparklines** for stat tiles, themed by the same stylesheet.
- **Selected dark mode** that follows `prefers-color-scheme` or a
  `data-theme` attribute.
- **Zero dependencies**: the Go packages use only the standard library.

## Install

```sh
go get github.com/ironystock/hyperplot
```

hyperplot requires Go 1.24 or later.

## Quick start

A complete program, also in [`examples/basic`](examples/basic/) (`go run ./examples/basic`):
a bar chart served by `net/http` and rendered into an `html/template` page.
The stylesheet is served as a file, so the page needs no inline styles.

```go
package main

import (
	"html/template"
	"io"
	"log"
	"net/http"
	"time"

	"github.com/ironystock/hyperplot"
)

var page = template.Must(template.New("page").Parse(`<!doctype html>
<title>Deploys</title>
<link rel="stylesheet" href="/hyperplot.css">
<main>{{.}}</main>`))

func main() {
	http.HandleFunc("/hyperplot.css", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/css; charset=utf-8")
		_, _ = io.WriteString(w, hyperplot.Stylesheet())
	})
	http.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		chart := hyperplot.Chart{
			Title: "Deploys per day",
			Marks: []hyperplot.Mark{hyperplot.Bar{
				Name: "deploys",
				X:    hyperplot.Categories{"Mon", "Tue", "Wed", "Thu", "Fri"},
				Y:    []float64{12, 19, 7, 15, 22},
			}},
		}
		svg, err := chart.HTML() // template.HTML: every label is escaped
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		if err := page.Execute(w, svg); err != nil {
			log.Print(err)
		}
	})
	// The timeouts drop clients that send a request or read a response
	// too slowly, so one stalled client cannot hold a connection forever.
	srv := &http.Server{
		Addr:              "localhost:8080",
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       10 * time.Second,
		WriteTimeout:      30 * time.Second,
	}
	log.Fatal(srv.ListenAndServe())
}
```

Every string in the chart, including series names and categories, is escaped
by the renderer, so `chart.HTML()` is safe to embed even when labels come
from users.

For [templ](https://templ.guide) users, `chart.Component()` satisfies
`templ.Component` structurally, without hyperplot importing templ.

## Live charts with htmx 4

Load htmx 4 and, if you want tooltips and autosizing, the hyperplot client
extension. htmx 4 extensions load by script tag; there is no `hx-ext`
attribute.

```html
<!-- Don't swap error pages from proxies or middleware over the charts. -->
<meta name="htmx-config" content='{"noSwap":[204,304,"4xx","5xx"]}'>
<link rel="stylesheet" href="/hyperplot.css">
<script src="https://cdn.jsdelivr.net/npm/htmx.org@4.0.2/dist/htmx.min.js"></script>
<script src="https://cdn.jsdelivr.net/npm/htmx.org@4.0.2/dist/ext/hx-ptag.js"></script>
<script src="https://cdn.jsdelivr.net/npm/htmx.org@4.0.2/dist/ext/hx-sse.min.js"></script>
<script src="/hyperplot.js"></script>
```

htmx 4 swaps every response except `204` and `304`, so without the
`htmx-config` line a proxy's `502` page or a login middleware's `401` would
replace a chart, and `hx-ptag` would keep it there after the error clears
(see [Errors](docs/htmx.md#errors-your-handler-never-sees)).

Set `Chart.ID` to a stable value for any chart you update, and `Chart.Animate`
to have CSS transitions animate the morph.

### 1. Polling with a content tag

The client polls; the server answers `304 Not Modified` when the chart has
not changed, so an idle dashboard costs one tiny response per tick.

```html
<div hx-get="/charts/latency" hx-trigger="every 2s, hp:resize"
     hx-swap="innerMorph" hx-ptag="{{.Tag}}" data-hp-autosize>
  {{.Chart}}
</div>
```

```go
func latency(w http.ResponseWriter, r *http.Request) {
	width, _ := hx.Size(r, 640, 0)    // the container's width, sent by the client extension
	chart := latencyChart(width, 320) // a *hyperplot.Chart with ID and Animate set
	svg, err := chart.Render()
	if err != nil {
		w.WriteHeader(http.StatusNoContent) // keep the last good chart
		return
	}
	if hx.CheckPTag(w, r, hx.Tag(svg)) {
		return // 304: unchanged, htmx skips the swap
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Write(svg)
}
```

The server keeps the height. A wrapper without a CSS height takes its
height from the chart inside it, so sending that back would grow the chart
on every poll (see [autosize](docs/htmx.md#responsive-charts-with-autosize)).

### 2. Streaming with server-sent events

The server pushes a freshly rendered chart whenever the data changes.

```html
<div hx-sse:connect="/stream/throughput" hx-swap="innerMorph">{{.Chart}}</div>
```

```go
func throughput(w http.ResponseWriter, r *http.Request) {
	stream, err := hx.NewStream(w, r)
	if err != nil {
		log.Print(err)
		w.Header().Set("HX-Reswap", "none")          // keep the chart
		w.WriteHeader(http.StatusServiceUnavailable) // reconnects back off
		return
	}
	tick := time.NewTicker(time.Second)
	defer tick.Stop()
	for {
		select {
		case <-r.Context().Done():
			return
		case <-tick.C:
			svg, err := throughputChart().Render()
			if err != nil {
				log.Print(err)
				continue // skip the frame; returning makes every client reconnect
			}
			if stream.Send(svg) != nil {
				return // client gone
			}
		}
	}
}
```

### 3. Filters

A form re-requests the charts for a new slice of data. Pin each series to a
color `Slot` so that a series keeps its color when filters hide others.

```html
<form hx-get="/dashboard" hx-trigger="change" hx-target="#charts" hx-swap="innerMorph">
  <select name="region">
    <option>all</option><option>eu-west</option><option>us-east</option>
  </select>
</form>
<div id="charts">{{.Charts}}</div>
```

```go
hyperplot.Line{Name: "eu-west", Slot: 1, X: hyperplot.Times(ts), Y: eu}
hyperplot.Line{Name: "us-east", Slot: 2, X: hyperplot.Times(ts), Y: us}
```

The [htmx integration guide](docs/htmx.md) covers each pattern in depth, plus
multi-target updates with `hx.Partial`, SSE replay with `Last-Event-ID`,
responsive autosizing, CSP, and the htmx 4 behaviors that differ from htmx 2.

## Chart types

| Mark | Draws | Notes |
|---|---|---|
| `Line` | A 2 px line per series | Linear, monotone or step curves; breaks at `NaN`; end dot, optional point dots |
| `Area` | A filled wash with a line on top | Stacks with `Chart.Stack`; `Base` draws a band between two series |
| `Bar` | One column per category | Grouped side by side, or stacked with `Chart.Stack`; rounded data ends |
| `Dot` | One point per value | Scatter plots; optional per-point labels for tooltips and the table |
| `RuleY` | A horizontal reference line | Targets, thresholds, averages; not a series |
| `Sparkline` | A compact, axis-free line | For stat tiles; separate type, same stylesheet |

Marks share the chart's scales, so you can combine them, for example bars with
a `RuleY` target. The x values pick the scale: `Floats` (linear), `Times`
(time) or `Categories` (band). `Chart.Table()` renders an accessible
`<table>` equivalent of any chart.

Deliberately not included: pie charts, dual y-axes, 3D, maps, and canvas
output. See the goals and non-goals in the [design document](docs/DESIGN.md).

## The client extension

`client/hyperplot.js` is a single plain-JavaScript file with no dependencies
and no build step. It is optional: charts are complete, readable and
accessible without it. With it you get:

- **Tooltips and a crosshair** on hover, built from text the server already
  formatted (the browser never formats numbers).
- **Keyboard reading**: focus a chart, then use the arrow keys, Home and End
  to step through the data, and Escape to dismiss.
- **Morph-safe state**: an open tooltip survives polling and SSE updates.
- **Responsive re-rendering**: elements with `data-hp-autosize` fire an
  `hp:resize` event when their width changes, and requests carry the
  element's width so the server can render to fit (`hx.Size`).

Go programs can serve it without npm through the `client` package, which
embeds the file and provides an `http.Handler` with caching headers:

```go
http.Handle("/hyperplot.js", client.Handler())
```

It also works without htmx: tooltips and keyboard reading use event
delegation on `document`.

## Design principles

- **Accessible.** Every chart is `role="img"` with an accessible name and a
  factual auto-generated description, is reachable by keyboard, and has a
  data-table equivalent. Text never relies on series color alone.
- **Deterministic.** The same input produces byte-identical output: no
  map-order or time dependence. That makes golden tests, caching, content
  tags and morphing reliable.
- **Safe by construction.** All text is escaped, the output is always
  well-formed XML, and the client inserts text with `textContent` only.
- **CSP-friendly.** Charts use classes and an external stylesheet: no
  `style=""` attributes, no inline event handlers, no `eval`.
- **Graceful without CSS.** Marks are drawn in `currentColor`, so a chart
  without the stylesheet is still legible in monochrome.
- **Selected dark mode.** Dark mode uses its own validated palette rather
  than an inversion, and follows `prefers-color-scheme` or
  `<html data-theme="dark">`.
- **Zero dependencies.** The Go packages import only the standard library;
  the client has no runtime dependencies.

## Why server-rendered?

- **The server already has the data.** Rendering where the data lives means
  no chart-specific JSON endpoint, no client-side formatting code, and no
  data shipped to the browser that the user never sees.
- **One source of truth for formatting.** Units, time zones and number
  formats are decided once, in Go, and the tooltip text is generated by the
  same code as the axis.
- **Fast first paint.** The chart is in the HTML response. There is no
  loading spinner and no layout shift while a bundle downloads and runs.
- **Works wherever SVG works**: in pages, as self-contained `.svg` files
  (with `Standalone`), in reports, and with JavaScript disabled.
- **Hypermedia fits live data.** With htmx 4 morph swaps, "re-render on the
  server and send the new chart" is cheap and animates smoothly, so live
  dashboards need no client state.

When should you use something else? If users need to pan and zoom through
very large datasets, or brush and filter data client-side without a round
trip, a client-side library is a better fit.

## Status

hyperplot is **pre-1.0**. The scales, the SVG writer, the chart renderer,
the `hx` package, the client extension and the example dashboard are built
and tested. Nothing has been published or tagged yet. Until v1.0.0, minor
versions may change the API; changes will be listed in the
[changelog](CHANGELOG.md).

Not there yet: horizontal bars, bars on a time axis, a log scale, small
multiples, annotations beyond a horizontal rule, and a locale option (labels
follow US conventions: a 12-hour clock and month-first dates). The
[roadmap](docs/DESIGN.md#roadmap) lists these and the other known gaps.

## Documentation

- [Design document](docs/DESIGN.md): the API, DOM output, visual spec and
  palette. The contract the code is built against, with its current status
  and [roadmap](docs/DESIGN.md#roadmap).
- [htmx 4 integration guide](docs/htmx.md): every live-update pattern in depth.
- [Architecture decision records](docs/adr/): why it is built this way.
- [Example dashboard](examples/dashboard/): every live pattern on one page;
  run it with `make demo`. [`examples/basic`](examples/basic/) is the quick
  start above.
- API reference on [pkg.go.dev](https://pkg.go.dev/github.com/ironystock/hyperplot)
  (available once the module is published).

## Contributing

Contributions are welcome. Please read [CONTRIBUTING.md](CONTRIBUTING.md) and
the [code of conduct](CODE_OF_CONDUCT.md). Report security issues privately as
described in [SECURITY.md](SECURITY.md). Project decisions follow
[GOVERNANCE.md](GOVERNANCE.md).

## License

[MIT](LICENSE) © 2026 The hyperplot Authors
