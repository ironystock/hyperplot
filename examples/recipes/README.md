# Go recipes

Each recipe shows one pattern in one `main.go` you can read top to bottom,
with the page it serves beside it. The [example dashboard](../dashboard/)
puts the live patterns together on one page; the recipes take them apart.

## Live patterns

One htmx pattern each, in the order of the
[htmx guide](../../docs/htmx.md). The level is the
[spec's server level](../../docs/spec.md#21-server-levels) the recipe
needs.

| Recipe | Pattern | Level | Shows | Run |
|---|---|---|---|---|
| [polling-ptag](polling-ptag/) | [1: polling with content tags](../../docs/htmx.md#pattern-1-polling-with-content-tags) | 2 | a chart that polls every 2 s and costs an empty `304` while its data is unchanged (`hx.Tag`, `hx.CheckPTag`) | `go run ./examples/recipes/polling-ptag` |
| [sse-stream](sse-stream/) | [2: server-sent events](../../docs/htmx.md#pattern-2-server-sent-events) | 2 | a chart pushed once a second over one stream (`hx.NewStream`, `retry:`, `Last-Event-ID`), opened at the chart's real width | `go run ./examples/recipes/sse-stream` |
| [filters-form](filters-form/) | [3: filters](../../docs/htmx.md#pattern-3-filters) and [4: multi-target updates](../../docs/htmx.md#pattern-4-multi-target-updates) | 2 | one form that redraws three charts per change, a summary line updated by `<hx-partial>`, colors kept with `Slot` | `go run ./examples/recipes/filters-form` |
| [select-drilldown](select-drilldown/) | [5: drill-down on select](../../docs/htmx.md#pattern-5-drill-down-on-select) | 3 | a click or Enter on a bar opens that service's detail (`hx.Selected`), with a plain link per bar and a live region | `go run ./examples/recipes/select-drilldown` |
| [brush-zoom](brush-zoom/) | [6: brush to zoom](../../docs/htmx.md#pattern-6-brush-to-zoom) | 3 | a drag zooms a live chart (`hx.Brushed`), the zoom is a URL (`HX-Push-Url`) that Back undoes, and a from/to form does the same without a pointer | `go run ./examples/recipes/brush-zoom` |

## Static pages and files

Charts rendered once. static-page and theming load `hx-plot.js` without
htmx, which still adds tooltips and keyboard reading (spec §6.10);
sparkline-table loads no script at all, and sizing loads htmx only to
redraw at the box's size.

| Recipe | Level | Shows | Run |
|---|---|---|---|
| [static-page](static-page/) | 1 | charts rendered once, with `hx-plot.js` alone for tooltips, a crosshair and keyboard reading | `go run ./examples/recipes/static-page` |
| [theming](theming/) | 1 | a custom palette with `Theme.CSS()`, light and dark, switched with plain links | `go run ./examples/recipes/theming` |
| [sparkline-table](sparkline-table/) | n/a | a KPI table with a sparkline per row and no JavaScript | `go run ./examples/recipes/sparkline-table` |
| [sizing](sizing/) | 2 | scaled by CSS, drawn at the box's width with `hx-plot:autosize`, and at its width and height with `hx-plot:autosize="both"` (`hx.Size`) | `go run ./examples/recipes/sizing` |
| [standalone-svg](standalone-svg/) | 1 | self-contained `.svg` files for a README, a report or an `<img>` | `go run ./examples/recipes/standalone-svg -out DIR` |

The [README quick start](../basic/) (`go run ./examples/basic`) is the
smallest of all: one bar chart from `net/http`.

## Run

Run a recipe from the repository root. Every one but standalone-svg serves
http://localhost:8080 and takes `-addr` to change it:

```sh
go run ./examples/recipes/polling-ptag -addr :9000
```

standalone-svg writes files and exits: to `-out DIR`, or, when `-out` is
left out, to `hyperplot-standalone` in the system's temporary directory,
the same directory on every run. Pages that load htmx fetch it
from jsDelivr, so the browser needs network access.

## How the recipes are built

- One `main.go` each, with the page (`page.html`) and stylesheet
  (`app.css`) embedded beside it. There is no shared package: the server
  setup and page head are repeated in every recipe on purpose, so each
  reads alone.
- Every comment at an htmx attribute, header or parameter says why it is
  there and cites the section of the [wire spec](../../docs/spec.md).
- Every page sends a strict Content Security Policy that allows only the
  scripts it loads: the live pages and sizing load htmx 4.0.2 and only the
  extensions they use from pinned URLs with Subresource Integrity, and
  allow that path on jsDelivr, as the dashboard does; static-page and
  theming allow scripts from their own origin only (`script-src 'self'`),
  and sparkline-table none (`script-src 'none'`). No page has `style=""`,
  inline event handlers or inline script.

## Tests

```sh
go test ./examples/recipes/...   # each recipe's main_test.go, with httptest
make golden                      # after a renderer change: rewrites the preview/ SVGs too
cd client && npm run test:browser   # opens every recipe in Chrome (needs npm ci)
```

The static recipes keep `preview/` SVGs, the pictures in their READMEs;
their tests fail when a picture is out of date.
