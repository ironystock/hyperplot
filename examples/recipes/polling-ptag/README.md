# Polling with ptag

A chart that polls its URL every two seconds and costs an empty
`304 Not Modified` whenever its data has not changed.

## Run

```sh
go run ./examples/recipes/polling-ptag            # http://localhost:8080
go run ./examples/recipes/polling-ptag -addr :9000
```

## What to look at

1. Open devtools' Network panel. A request to `/charts/connections` goes
   out every two seconds. The data moves on every ten, so about four in
   five answers are `304` with no body, and htmx swaps nothing.
2. Click one of the `200` answers: its response headers carry
   `HX-PTag: "hp-…"`, a hash of the chart's bytes. The next request sends
   the same value back as its `HX-PTag` request header.
3. View the page source: the wrapper's `hx-ptag` attribute already holds
   the tag of the chart the page was drawn with, so even the first poll is
   a `304` when the data has not moved on.
4. When a `200` arrives, the line slides left instead of being redrawn:
   the chart keeps `id="connections"`, so `innerMorph` updates it in place
   and `hx-plot.js` animates the change.
5. Try it by hand: `curl -i localhost:8080/charts/connections`, then send
   the tag back with `-H 'HX-PTag: "hp-…"'` (or `hp-…` without quotes, or
   `W/"hp-…"`) and get a `304`.

## Key code

| What | Where |
|---|---|
| `hx-trigger="every 2s"`, `hx-swap="innerMorph"` | `page.html` |
| `hx-ptag="{{.Tag}}"` seeds the first poll's tag | `page.html`; `main.go`: `page` |
| The `hx-ptag.js` extension script, not deferred | `page.html` |
| `hx.CheckPTag(w, r, hx.Tag(svg))` answers a match with a bodiless `304` | `main.go`: `connections` |
| A render error answers `204`, which keeps the last chart | `main.go`: `connections` |
| Values are a pure function of the 10-second bucket | `main.go`: `chart`, `noise` |

## How it works

htmx's `hx-ptag` extension stores the `HX-PTag` header of each response on
the element and sends it back with the next request. The server renders
the chart, hashes the bytes with `hx.Tag`, and `hx.CheckPTag` compares the
two, ignoring a `W/` prefix, quotes and spaces: a match gets a `304` with
no body ([spec §4.5](../../../docs/spec.md#45-transports)). That only works
because the output is deterministic: the same data renders the same bytes
([§4.4](../../../docs/spec.md#44-frame-stability)), so the chart is drawn
from the bucket, never from the clock's exact second. The page sets
`noSwap` to `[204,304,"4xx","5xx"]`, so neither a `304` nor a proxy's error
page is ever swapped over the chart. See
[Pattern 1: polling with content tags](../../../docs/htmx.md#pattern-1-polling-with-content-tags)
in the htmx guide.

This chart has a fixed width, scaled down by CSS on a narrow screen. To
draw it at its box's width as well, add `hx-plot:autosize` and a
`htmx:plot:resize` trigger, as the [sizing recipe](../sizing/) does; a poll
at a steady size is still a `304`.

## Tests

```sh
go test ./examples/recipes/polling-ptag
```

They check the page (status, CSP header, the pinned htmx and `hx-ptag`
tags with their integrity hashes, the polling attributes, well-formed
charts, no `style` or `on*` attributes), that the seeded tag makes the
first poll a `304`, that a matching tag in every accepted form gets a
bodiless `304` and a stale or foreign one a `200`, that the next bucket
changes the tag, and that rendering is deterministic.
