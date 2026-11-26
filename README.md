# hyperplot

**`hx-plot`: an htmx 4 extension for live charts that your server draws as SVG.
No client chart library.**

[![Go Reference](https://pkg.go.dev/badge/github.com/ironystock/hyperplot.svg)](https://pkg.go.dev/github.com/ironystock/hyperplot)
[![CI](https://github.com/ironystock/hyperplot/actions/workflows/ci.yml/badge.svg)](https://github.com/ironystock/hyperplot/actions/workflows/ci.yml)
[![License: MIT](https://img.shields.io/badge/license-MIT-blue.svg)](LICENSE)

Your server renders each chart as SVG markup and sends it like any other
fragment. htmx keeps it live with the requests you already know (polling,
server-sent events, forms) and `innerMorph` swaps. The extension
(`hx-plot.js`, npm `htmx-ext-plot`) reads the markup and adds what a chart
needs in the browser: tooltips and keyboard reading, state that survives
swaps, time windows that slide instead of redrawing, and clicks and drags
that become ordinary htmx requests. The browser never receives a data API,
and never formats a number.

Any server language can produce the charts. The contract between server and
extension is a short [wire spec](docs/spec.md) with
[conformance fixtures](testdata/conformance/README.md). The Go library in
this repository is the reference server, and the
[server guide](docs/implementing-a-server.md) builds one in Python.

> **Status: pre-release.** The extension (0.2.0) and spec version 1 are
> complete and tested, but nothing is on npm or tagged yet, and names may
> still change before the first publish. Feedback is very welcome. See
> [Status](#status).

**Contents**

- [Why server-rendered charts with htmx](#why-server-rendered-charts-with-htmx)
- [Quick start: the extension](#quick-start-the-extension)
- [Gestures become requests](#gestures-become-requests)
- [Live updates](#live-updates)
- [Reference server: Go](#reference-server-go)
- [Implement a server in your language](#implement-a-server-in-your-language)
- [Status](#status)
- [Documentation](#documentation)

## Why server-rendered charts with htmx

The htmx chart extensions we know of (htmx-echarts, htmx-plotly, hx-chart)
send JSON to a client-side charting library, which draws the chart in the
browser. hyperplot sends the chart itself:
the response is the hypermedia. That keeps the htmx way of working all the
way down:

- **The server already has the data.** It renders where the data lives, so
  there is no chart-specific JSON endpoint and no client formatting code.
  Units, time zones and number formats are decided once, on the server, and
  the tooltip says exactly what the axis does.
- **The chart is in the first response.** No bundle to load, no spinner, no
  layout shift. It works with JavaScript off, in a `.svg` file, and through a
  strict Content Security Policy.
- **Live updates are ordinary swaps.** Re-render on the server, send the new
  chart, and htmx's morph updates the existing elements in place. CSS
  transitions animate changed values, and when a time window moves the
  extension slides the chart rather than redrawing it.
- **It is accessible by construction.** Every chart has an accessible name
  and description, takes keyboard focus, and comes with an equivalent data
  table that stays in sync.

A fair objection to server-rendered charts is that a live update redraws
the whole chart and looks clunky. With morph swaps it doesn't: the elements
survive the swap, so they animate to their new values, and the extension
keeps the tooltip and keyboard position where they were.

When should you use something else? If readers need to pan and zoom through
very large datasets, or filter data in the browser without a round trip, a
client-side library fits better.

## Quick start: the extension

Load htmx 4 and the extension with plain script tags, no build step and no
`hx-ext` attribute (htmx 4 activates an extension when its script loads):

```html
<!-- Don't swap error pages from proxies or middleware over the charts. -->
<meta name="htmx-config" content='{"noSwap":[204,304,"4xx","5xx"]}'>
<link rel="stylesheet" href="/static/hx-plot.css">
<script src="https://cdn.jsdelivr.net/npm/htmx.org@4.0.2/dist/htmx.min.js"></script>
<script src="/static/hx-plot.js"></script>
```

`hx-plot.js` and the stylesheet `hx-plot.css` come from the npm package
`htmx-ext-plot`. It isn't published yet: until it is, copy them from
[`client/`](client/) (the stylesheet is the Go library's
`hyperplot.Stylesheet()`), or serve them from Go with the `client` package.
The extension registers itself as `plot`; a page that restricts
`htmx.config.extensions` must list it.

Then put a chart your server rendered inside an element that refreshes it:

```html
<div hx-get="/charts/latency" hx-trigger="every 2s, htmx:plot:resize"
     hx-swap="innerMorph" hx-plot:autosize>
  <svg xmlns="http://www.w3.org/2000/svg" id="latency" class="hp hp-anim" data-hp="1" …>…</svg>
</div>
```

That chart now has:

- **Tooltips and a crosshair** on hover (a band highlight on bar charts, a
  ring on scatter plots), filled with text the server formatted.
- **Keyboard reading**: focus the chart, then step through it with the arrow
  keys, Home and End. Each step is announced to screen readers.
- **Updates that keep their place.** After every poll or SSE message the
  tooltip and keyboard position stay on the same data, a moved time window
  slides, an open data table stays open and scrolled to its newest row.
- **Responsive rendering.** `hx-plot:autosize` adds the container's width to
  requests as `hp-w`, and fires `htmx:plot:resize` when it changes, so the
  server renders at the size the page has instead of scaling the text.

Without htmx, the extension still gives charts on a page their tooltips and
keyboard reading.

### Attributes

| Attribute | On | Does |
|---|---|---|
| `hx-plot:autosize` (or `="both"`) | the element whose width sizes the chart, usually the swap target | adds `hp-w` (and `hp-h`) to requests that target it; fires `htmx:plot:resize` when it changes size |
| `hx-plot:brush` | an ancestor of the charts | lets readers drag across a time or linear x axis (needs the companion `hx-plot-brush.js`) |

Both accept htmx's other spellings (`data-hx-plot:…`, and
`htmx.config.metaCharacter` in place of `:`). Tuning, such as turning the
slide off, is configuration: `htmx.config.plot`, or per request
`hx-config="plot.slide:false"`. The [extension's
README](client/README.md) has the events, the config keys and the
accessibility table.

## Gestures become requests

A click on a point, or Enter on a keyboard point, dispatches
`htmx:plot:select`. A drag across a chart inside `hx-plot:brush` (or
Shift+Arrow keys, then Enter) dispatches `htmx:plot:brush`. Use them as
triggers, and the extension adds the gesture to the request as form
parameters:

```html
<section hx-get="/detail" hx-trigger="htmx:plot:select" hx-target="#detail">
  <div class="zoom" hx-get="/charts/latency" hx-trigger="htmx:plot:brush"
       hx-target="#latency" hx-swap="outerMorph" hx-sync="replace" hx-plot:brush>
    <div id="latency" hx-get="/charts/latency" hx-trigger="every 2s, htmx:plot:resize"
         hx-swap="innerMorph" hx-sync="closest .zoom:drop" hx-plot:autosize>…</div>
  </div>
</section>
<p id="detail" aria-live="polite"></p>
```

Clicking 2:10 PM, then dragging from 2:05 PM to 2:15 PM, sends:

```http
GET /detail?hp-chart=lt&hp-x=2026-03-03T14%3A10%3A00Z
GET /charts/latency?hp-w=800&hp-chart=lt&hp-x0=2026-03-03T14%3A05%3A00Z&hp-x1=2026-03-03T14%3A15%3A00Z
```

`hp-x` is the key the server wrote for that point, so the server can match
it exactly. The server answers the brush with a zoomed polling element and
`HX-Push-Url`, so the zoom is a URL: it can be bookmarked, and Back undoes
it. Drill-down and zoom are hypermedia exchanges, with no client state.
Because the parameters are plain form fields, a link per table row and a
from/to form send the same requests without a pointer, or without script
([spec §5.5](docs/spec.md#55-equivalents-in-plain-html)).

## Live updates

The extension adds no transport of its own; htmx's are enough.

**Polling with a polling tag.** With htmx's `hx-ptag` extension the server
answers `304 Not Modified` while a chart is unchanged, so an idle dashboard
costs one empty response per tick. Charts render deterministically, so the
same data always hashes to the same tag.

```html
<div hx-get="/charts/latency" hx-trigger="every 2s, htmx:plot:resize"
     hx-swap="innerMorph" hx-ptag='"hp-1a2b3c4d5e6f7a8b"' hx-plot:autosize>…</div>
```

**Server-sent events.** The server pushes a new chart whenever the data
changes; each unnamed message is swapped in.

```html
<div hx-sse:connect="/stream/throughput" hx-swap="innerMorph">…</div>
```

**Filters and other forms.** A form re-requests the charts for a new slice
of data, and the morph animates from the old values to the new ones.

```html
<form hx-get="/dashboard" hx-trigger="change" hx-target="#charts" hx-swap="innerMorph">…</form>
```

The [htmx integration guide](docs/htmx.md) covers each pattern in depth,
with the htmx 4 behaviours that differ from htmx 2.

## Reference server: Go

The Go module `github.com/ironystock/hyperplot` renders charts to
deterministic, well-formed, accessible SVG that meets every level of the
spec, and its `hx` package reads and writes the htmx side: polling tags, SSE
streams, sizes and gestures. It uses only the standard library.

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

### Install

```sh
go get github.com/ironystock/hyperplot
```

It requires Go 1.24 or later. No version is tagged yet, so this fetches the
latest commit.

### Quick start

A complete program, also in [`examples/basic`](examples/basic/)
(`go run ./examples/basic`): a bar chart served by `net/http` and rendered
into an `html/template` page. The stylesheet is served as a file, so the
page needs no inline styles.

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
from users. For [templ](https://templ.guide) users, `chart.Component()`
satisfies `templ.Component` structurally, without hyperplot importing templ.

### Live charts from Go

Serve the extension from the embedded copy in the `client` package (with an
`ETag` and caching headers), and set `Chart.ID` and `Chart.Animate` on every
chart you update:

```go
mux.Handle("GET /static/hx-plot.js", client.Handler())
mux.Handle("GET /static/hx-plot-brush.js", client.BrushHandler()) // optional: brushing
```

A polling handler renders at the width the extension reports and lets
`hx.CheckPTag` answer `304` when nothing changed:

```go
func latency(w http.ResponseWriter, r *http.Request) {
	width, _ := hx.Size(r, 640, 0)    // hp-w from hx-plot:autosize; keep the height
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

A stream sends a fresh chart on every tick and skips a frame that fails to
render:

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

`hx.Selected` and `hx.Brushed` read the gestures. A brush and a poll can
share a handler: `Brushed` reports a brush only when `hp-x0` and `hp-x1` are
present.

```go
func zoom(w http.ResponseWriter, r *http.Request) {
	width, _ := hx.Size(r, 640, 0)
	chart := latencyChart(width, 320)
	if b, ok := hx.Brushed(r); ok {
		if t0, t1, err := b.Times(); err == nil { // ordered, in UTC
			chart.X.TimeDomain = [2]time.Time{t0, t1}
		}
	}
	svg, err := chart.Render()
	if err != nil {
		w.WriteHeader(http.StatusNoContent)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Write(svg)
}
```

Pin each series to a colour `Slot` when filters can hide some of them, so a
series keeps its colour:

```go
hyperplot.Line{Name: "eu-west", Slot: 1, X: hyperplot.Times(ts), Y: eu}
hyperplot.Line{Name: "us-east", Slot: 2, X: hyperplot.Times(ts), Y: us}
```

### Chart types

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
(time) or `Categories` (band). `Chart.Table()` renders the accessible
`<table>` equivalent of any chart. The defaults follow data-visualization
practice: a colourblind-aware 8-colour palette in a fixed order with its own
dark mode, recessive gridlines, direct labels when they fit, and a legend
only when it is needed.

Deliberately not included: pie charts, dual y-axes, 3D, maps and canvas
output. See the goals and non-goals in the [design document](docs/DESIGN.md).

## Implement a server in your language

A server in Python, PHP, Ruby or anything else implements the
[wire spec](docs/spec.md) and gets the same extension behaviour. The spec is
in three cumulative levels, so you can start small:

| Level | Your server | The page gets |
|---|---|---|
| 1. Static | emits chart markup with a JSON hover payload | native tooltips without script; with the extension, tooltips, a crosshair and keyboard reading |
| 2. Live | adds stable ids, three geometry attributes and row keys, and serves frames over htmx polling or SSE | morph updates that keep their place, sliding windows, live tables, `304`s for unchanged polls |
| 3. Interactive | reads the gesture parameters | drill-down on click or Enter, brush-to-zoom with history |

- [Implementing a server](docs/implementing-a-server.md) builds a Level 3
  server in about 320 lines of standard-library Python, step by step, and
  ends with a conformance checklist.
- [The spec's Appendix B](docs/spec.md#appendix-b-an-exchange-and-helpers-in-python)
  has the value-form, escaping and parsing helpers in Python.
- [`testdata/conformance/`](testdata/conformance/README.md) has
  language-neutral fixtures: chart inputs, reference output, and assertions
  any server's output can be checked against.

## Status

Pre-release. Built and tested: the extension (`hx-plot.js` 0.2.0 and its
brush and tween companions), spec version 1 with its fixtures, and the Go
reference server at Level 3 (renderer, `scale`, `hx`, the `client` package
and the example dashboard). Nothing is on npm and no Go version is tagged.

Known gaps, all on the [roadmap](docs/DESIGN.md#roadmap):

- The extension is tested in Node against a fake DOM and in Chrome. Firefox
  and Safari are untested, and the Safari tween companion has never run in
  Safari.
- The core script is over its 13 KiB minified and 5.5 KiB gzipped budget
  (it is about 18 KiB and 7.6 KiB; the measured sizes are in the
  extension's [size table](client/README.md#size)); trimming it, or moving
  another part into a companion, is still to do.
- Touch readers can't brush, and mobile screen-reader users can't select or
  brush, in spec 1; pages should offer the plain HTML equivalents.
- The Go renderer has no horizontal bars, bars on a time axis, log scale,
  small multiples, annotations beyond a horizontal rule, or locale option
  (labels follow US conventions).

Until v1.0.0, minor versions may change the API and the markup; changes are
listed in the [changelog](CHANGELOG.md), and markup changes go through the
spec.

## Documentation

- [Wire spec](docs/spec.md): the contract between any server and the
  extension, with conformance levels.
- [Implementing a server](docs/implementing-a-server.md): a step-by-step
  guide in Python.
- [The extension's README](client/README.md): attributes, events, config,
  parameters and accessibility, in the layout of htmx's own extension pages.
- [htmx 4 integration guide](docs/htmx.md): every live-update and gesture
  pattern in depth, with Go handlers.
- [Design document](docs/DESIGN.md): how the Go library and the reference
  client are built, with the current status and the [roadmap](docs/DESIGN.md#roadmap).
- [Architecture decision records](docs/adr/README.md): why it is built this
  way, including [why the extension is the product](docs/adr/0005-htmx-community-first.md).
- [Example dashboard](examples/dashboard/): polling, SSE, filters,
  drill-down and brush-to-zoom on one page, run with `make demo`.
  [`examples/basic`](examples/basic/) is the Go quick start.
- Go API reference on [pkg.go.dev](https://pkg.go.dev/github.com/ironystock/hyperplot)
  (available once a version is tagged).

## Contributing

Contributions are welcome. Please read [CONTRIBUTING.md](CONTRIBUTING.md) and
the [code of conduct](CODE_OF_CONDUCT.md). Report security issues privately as
described in [SECURITY.md](SECURITY.md). Project decisions follow
[GOVERNANCE.md](GOVERNANCE.md).

## License

[MIT](LICENSE) © 2026 The hyperplot Authors
