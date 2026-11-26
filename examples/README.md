# Examples

Runnable servers that draw charts as SVG and keep them live with htmx 4 and
the [hx-plot extension](../client/README.md). Every one speaks the
[wire spec](../docs/spec.md), so the page side is the same whichever
language draws the charts. Run them from the repository root; each README
says what to try and where to look in the browser's developer tools.

Where to start:

- **Not using Go?** Read the [Python](python/) or [Node](node/) server.
- **Want one pattern?** Pick a [recipe](recipes/README.md).
- **Want to see what it draws?** Run the [gallery](gallery/).
- **Want everything at once?** Run the [dashboard](dashboard/).

## Servers in other languages

Proof that the extension needs no Go: each is a complete Level 3 server
([spec §2.1](../docs/spec.md#21-server-levels)) built on the standard
library alone, with the same pages and URLs, so they read side by side.
Both serve `hx-plot.js` from this repository's `client/` directory; a
real app would install it from npm or a CDN.

| Example | Language | Shows | Run |
|---|---|---|---|
| [python](python/) | Python 3.10+, standard library | polling with `hx-ptag`, SSE, grouped bars, select, brush-to-zoom, a live table | `python3 examples/python/server.py` |
| [node](node/) | Node 22+, `node:` built-ins only | the same pages and URLs as the Python example | `node examples/node/server.mjs` |

Both take `--addr host:port` and `--speed N` and listen on port 8080 by
default (Python on http://127.0.0.1:8080, Node on http://localhost:8080). Their fixture renderers and
`hpcheck.json` files plug into the conformance checker,
[`tools/hpcheck`](../tools/hpcheck/README.md), which checks a server in any
language against the spec.

## Go demos

Whole pages built with the Go reference server, several patterns at once.

| Example | Shows | Run |
|---|---|---|
| [dashboard](dashboard/) | polling, SSE, filters, multi-target updates, drill-down and brush-to-zoom on one page; also the end-to-end test of the Go packages | `go run ./examples/dashboard` (`make demo`) |
| [sensors](sensors/) | a grid of sparkline tiles over one SSE stream, select a sensor, brush to zoom its chart, threshold rules, a live alerts table | `go run ./examples/sensors` |
| [builds](builds/) | polling with content tags, a scatter with brush-to-zoom, stacked bars with select-to-drill-down, one filter form | `go run ./examples/builds` |

All three serve http://localhost:8080 and take `-addr` to change it and
`-speed` to make simulated time run faster.

## Chart gallery

| Example | Shows | Run |
|---|---|---|
| [gallery](gallery/) | every mark and variant the Go renderer draws (lines, areas, bars, scatter, rules, fixed domains, formats, edge cases, sparklines, a data table), each card beside the Go function that drew it, in light and dark | `go run ./examples/gallery` |

`go run ./examples/gallery -out out/gallery` writes the same pages as
static files instead (`out/` is ignored by git).

## Go recipes

One pattern each, in one `main.go` you can read top to bottom. The
[recipes index](recipes/README.md) says which htmx pattern and spec level
each one is.

| Recipe | Shows | Run |
|---|---|---|
| [polling-ptag](recipes/polling-ptag/) | polling every 2 s, with an empty `304` while the data is unchanged | `go run ./examples/recipes/polling-ptag` |
| [sse-stream](recipes/sse-stream/) | a chart pushed once a second over server-sent events, with `retry:` and `Last-Event-ID` | `go run ./examples/recipes/sse-stream` |
| [filters-form](recipes/filters-form/) | one form that redraws three charts, plus a summary updated by `<hx-partial>` | `go run ./examples/recipes/filters-form` |
| [select-drilldown](recipes/select-drilldown/) | click or Enter on a bar for its detail, with a plain link per bar | `go run ./examples/recipes/select-drilldown` |
| [brush-zoom](recipes/brush-zoom/) | drag to zoom a live chart; the zoom is a URL and Back undoes it | `go run ./examples/recipes/brush-zoom` |
| [static-page](recipes/static-page/) | charts rendered once, no htmx: `hx-plot.js` alone adds tooltips, a crosshair and keyboard reading | `go run ./examples/recipes/static-page` |
| [sizing](recipes/sizing/) | scaled by CSS, `hx-plot:autosize` and `hx-plot:autosize="both"` | `go run ./examples/recipes/sizing` |
| [theming](recipes/theming/) | a custom palette with `Theme.CSS()`, light and dark, switched with plain links | `go run ./examples/recipes/theming` |
| [sparkline-table](recipes/sparkline-table/) | a KPI table with a sparkline per row and no JavaScript | `go run ./examples/recipes/sparkline-table` |
| [standalone-svg](recipes/standalone-svg/) | self-contained `.svg` files for a README, report or `<img>` | `go run ./examples/recipes/standalone-svg -out DIR` |
| [basic](basic/) | the README quick start: one bar chart from `net/http` | `go run ./examples/basic` |

`standalone-svg` writes files: to `-out DIR`, or, when `-out` is left
out, to `hyperplot-standalone` in the system's temporary directory, the
same directory on every run. The others serve
http://localhost:8080, and all of them but `basic` take `-addr`.

## What every example has in common

- The page loads htmx 4.0.2 and only the extensions it uses (`hx-sse`,
  `hx-ptag`) from pinned jsDelivr URLs with Subresource Integrity, without
  `defer`, then `hx-plot.js` with `defer`
  ([setup](../docs/htmx.md#scripts-and-stylesheet)). The browser needs
  network access for htmx. The gallery and the static-page, theming and
  sparkline-table recipes load no htmx at all.
- Every page a server sends comes with a strict Content Security Policy,
  and no markup carries `style=""` or inline event handlers.
- Data is simulated and deterministic, so the tests can check exact output.

## Tests

```sh
go test ./examples/...                       # every Go example
python3 -m unittest discover examples/python # make py (also runs the checker's tests)
cd examples/node && node --test              # make nodeex
make conformance                             # hpcheck on the Python and Node output, the gallery export, and the dashboard, Python and Node servers
make golden                                  # after a renderer change: also rewrites the recipes' preview/ SVGs
make browser                                 # opens every example in Chrome (needs npm ci in client/)
```

`make test` runs all of them but the browser tests, and `make check` all
of them. On Windows, where Python is usually `python` (or `py`) rather
than `python3`, type that instead in the commands here, in the Python
and checker READMEs and at the top of their scripts, and pass
`PYTHON=python` to make.

The browser test, [`examples.browser.mjs`](../client/browser/examples.browser.mjs),
starts every example but `basic` on a free port in turn and opens it in
one Chrome: the page must draw all its charts with no console error,
failed request or Content Security Policy violation; it must load
`hx-plot.js` (all but sparkline-table) and answer the keyboard; a live
example must update a chart on its own; and a gesture example must
answer its gesture.
