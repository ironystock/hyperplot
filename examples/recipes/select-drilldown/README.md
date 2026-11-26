# Select to drill down

A bar chart whose bars open a detail view: click a bar, or press Enter on
a focused one, and the server answers with that service hour by hour.

## Run

```sh
go run ./examples/recipes/select-drilldown            # http://localhost:8080
go run ./examples/recipes/select-drilldown -addr :9000
```

## What to look at

1. Click a `5xx` bar. The detail below shows that service's 5xx errors
   per hour; click a `4xx` bar for the 4xx ones. Each class keeps its color
   in both charts.
2. In devtools' Network panel, the click is an ordinary
   `GET /detail?hp-chart=errors&hp-x=auth&hp-series=5xx`. `hp-x` is the
   bar's category, the key the server wrote into the chart; `hp-series` is
   the clicked bar's series.
3. Without a mouse: Tab to the chart, move with the arrow keys and press
   Enter. The status line ("Showing auth, all errors.") is a polite live
   region, so a screen reader hears the result.
4. Use the **Details** links under the chart. With script they fetch the
   same fragment; open one in a new tab, or turn JavaScript off, and it
   loads the page with the detail open.
5. Try junk by hand: `/detail?hp-x=nope` answers "There is no service
   named “nope”." with the value escaped, never an error.

## Key code

| What | Where |
|---|---|
| `hx-trigger="htmx:plot:select"` on the chart's wrapper | `page.html` |
| `hx-target="#detail" hx-swap="innerMorph"` | `page.html` |
| `hx.Selected(r)` reads `hp-chart`, `hp-x`, `hp-series` | `main.go`: `selection` |
| Check `hp-x` against the server's own keys; unknown is no selection | `main.go`: `selection` |
| `hp-series` as a hint: unknown or missing shows both classes | `main.go`: `selection` |
| One link per category: `href="/?hp-x=…"`, `hx-get="/detail?hp-x=…"` | `main.go`: `page`; `page.html` |
| The polite status line, `role="status"`, with a stable id | `page.html` (`detail`) |
| `Slot` keeps each class's color | `main.go`: `classes`, `bars`, `hourly` |

## How it works

A click on a bar, or Enter on a keyboard point, makes `hx-plot.js` fire
`htmx:plot:select` on the chart. It bubbles to the wrapper, whose
`hx-trigger` turns it into a GET of `/detail`, and the extension adds the
selection as parameters
([spec §5.2](../../../spec/spec.md#52-select)). On a band axis `hp-x` is the
category itself, so the handler matches it exactly against its own list;
a missing or unknown value is no selection, and an echo goes through
`html/template`. The answer is morphed into `#detail`, where the status
line keeps its id, so its new text is announced.

Not every reader can click a bar, so the page also offers the same request
as plain links ([§5.5](../../../spec/spec.md#55-equivalents-in-plain-html)).
See [Pattern 5: drill-down on select](../../../docs/htmx.md#pattern-5-drill-down-on-select)
in the htmx guide; the [dashboard](../../dashboard/) does the same on a
polled chart.

Both charts have a fixed width, scaled down by CSS on a narrow screen,
their text with them. To draw them at their box's width, add
`hx-plot:autosize`, as the [sizing recipe](../sizing/) does.

## Tests

```sh
go test ./examples/recipes/select-drilldown
```

They check the page (status, CSP header, the pinned htmx tag, the select
trigger, a link per service with the same query as the gesture, the row
keys in the chart), a table of selections (with and without a series, an
unknown series, nothing selected, another chart, an unknown service with
markup in it, which is escaped), the link without script, and that a
class keeps its color slot when shown alone.
