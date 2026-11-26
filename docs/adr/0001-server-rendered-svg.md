---
status: accepted
date: 2026-11-25
decision-makers: hyperplot maintainers
---

# Render charts as SVG on the server

## Context and Problem Statement

Go web applications that render HTML on the server (with `html/template`,
templ, or htmx) still usually draw charts in the browser: the server exposes a
JSON endpoint, and a JavaScript charting library fetches, formats and draws
the data. That splits one feature across two languages, ships a large bundle,
duplicates formatting logic (units, time zones, number formats), and leaves
an empty box until the script runs.

Where should hyperplot draw charts, and in what format?

## Decision Drivers

* Fit hypermedia-driven applications: the server sends HTML, not data.
* One place for formatting: the browser should never format raw numbers.
* Fast first paint with no layout shift and no required JavaScript.
* Accessibility: an accessible name and description, keyboard access, and a
  text equivalent of the data.
* Crisp at any size and pixel density; stylable with CSS, including dark mode.
* Safe with user-controlled labels.
* Testable: output that can be compared byte for byte.

## Considered Options

* Server-rendered SVG
* Server-rendered raster images (PNG)
* A JSON API plus a client-side charting library
* Server-side rendering by a headless JavaScript charting library

## Decision Outcome

Chosen option: "Server-rendered SVG", because it is the only option that is
hypermedia-native, CSS-stylable, accessible, and crisp without a JavaScript
runtime on either side.

The server produces a complete `<svg>` element with an accessible name and
description, preformatted tooltip text in a `<metadata>` element, and a
`<title>` on each bar and dot for a no-JavaScript fallback. A small,
optional client extension adds interaction on top (see
[DESIGN.md](../DESIGN.md#client-extension-clienthyperplotjs)); the chart is
complete without it.

### Consequences

* Good, because the chart arrives in the first HTML response: no spinner, no
  layout shift, and it works with JavaScript disabled.
* Good, because formatting happens once, in Go. Axis ticks, tooltips and the
  data table use the same formatter and unit.
* Good, because SVG is vector, scales to any density, and can be themed with
  CSS custom properties.
* Good, because SVG elements carry ARIA roles and titles, and
  `Chart.Table()` gives screen-reader users a real table.
* Good, because rendering is a pure function of the input, so golden-file
  tests can pin the output exactly.
* Bad, because every update is a server round trip; interactions such as
  pan-and-zoom over large datasets or client-side brushing are out of scope.
* Bad, because the SVG is larger than the raw data for dense charts (for
  example a line with thousands of points). Paths use at most two decimals to
  keep this in check.
* Bad, because the server must estimate text widths for layout without a font
  renderer; hyperplot uses Helvetica/Arial advance widths
  (`internal/textwidth`), which can be slightly off for other fonts.
* Neutral, because tooltips need a little JavaScript; the client extension is
  kept small and dependency-free, and native `<title>` tooltips remain as a
  fallback.

### Confirmation

Golden-file tests compare rendered SVG byte for byte, and every golden file is
parsed with `encoding/xml` to check it is well-formed. A fuzz test checks
well-formedness for arbitrary labels and values.

## Pros and Cons of the Options

### Server-rendered SVG

* Good, because it is HTML-embeddable, accessible, stylable and crisp.
* Good, because it needs no runtime in the browser to display.
* Bad, because interaction needs a small script, and each update is a round
  trip.

### Server-rendered raster images (PNG)

* Good, because it works anywhere an `<img>` works, including email.
* Bad, because it is blurry when scaled, can't be themed with CSS (no dark
  mode without re-rendering), and has no accessible structure.
* Bad, because rasterizing text in Go needs fonts and a rasterizer, which
  means dependencies.

### A JSON API plus a client-side charting library

* Good, because rich client-side interaction is possible.
* Bad, because it splits rendering and formatting across two languages, ships
  a large bundle, and delays first paint.
* Bad, because it doesn't fit hypermedia applications: the server returns
  data that the client must know how to interpret.

### Server-side rendering by a headless JavaScript library

* Good, because it reuses a mature library's layouts.
* Bad, because it requires running Node or a browser next to the Go server,
  which defeats the point of a Go library.

## More Information

* [DESIGN.md](../DESIGN.md): output structure, layout and visual spec.
* [ADR 0003](0003-htmx4-morph-live-updates.md): how server-rendered charts
  stay live.
