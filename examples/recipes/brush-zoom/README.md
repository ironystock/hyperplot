# Brush to zoom

A live chart you zoom by dragging across it. The zoom is a URL: it can be
bookmarked, and Back undoes it.

## Run

```sh
go run ./examples/recipes/brush-zoom            # http://localhost:8080
go run ./examples/recipes/brush-zoom -addr :9000
```

## What to look at

1. Drag across the chart. The address bar changes to `/?from=…&to=…`, the
   status line says what the chart shows, and the from/to fields follow.
   Press Back: the live view returns.
2. Without a mouse: Tab to the chart, move with the arrow keys, hold Shift
   and move again to mark a range, then press Enter.
3. In devtools' Network panel, the brush is an ordinary
   `GET /charts/latency?hp-chart=latency&hp-x0=…&hp-x1=…`. Its answer is
   the whole polling element, whose `hx-get` now carries `from` and `to`,
   plus an `<hx-partial>` for the toolbar, and an `HX-Push-Url` header.
4. Watch the polls every five seconds: a zoomed chart's polls carry the
   range in their URL, so they stay zoomed. Back to live returns the
   sliding window, and its polls drop the range again. A zoom is drawn
   exactly as it was pushed; once its range leaves the six hours of data,
   its polls get an empty `204`, so the chart keeps the zoom the status
   line and the address bar name.
5. Type a range into the From and To fields and press Zoom: the same
   request without a pointer. With JavaScript turned off, the same form
   loads the zoomed page.
6. Drag a few pixels inside a zoomed chart: a range under a minute is
   widened to a minute around its middle, as the dashboard and the
   Python and Node examples do, so even a tiny brush zooms and the status
   line says what it shows. Try a bad brush by hand:
   `/charts/latency?hp-x0=abc&hp-x1=1e999`, or a range wholly outside the
   last six hours. The answer is an empty `204`: the brush is ignored and
   the chart stays as it was.

## Key code

| What | Where |
|---|---|
| `hx-plot:brush`, `hx-trigger="htmx:plot:brush"` on the wrapper | `page.html` |
| `hx-target="#latency-body" hx-swap="outerMorph"`: the answer is the polling element | `page.html`; `main.go`: `zoomTo` |
| `hx-sync="replace"` and `hx-sync="closest .zoom:drop"`: one request queue | `page.html` |
| The `hx-plot-brush.js` companion script | `page.html`; `main.go`: `routes` |
| `hx.Brushed(r)` tells a brush from a poll of the same URL | `main.go`: `latency` |
| `Brush.TimesIn(loc)` orders the ends and reads `datetime-local` in the chart's zone | `main.go`: `brushed` |
| Clip to the data and widen to at least a minute; `204` for a range wholly outside it | `main.go`: `newZoom`, `latency` |
| A zoomed poll draws its range as is, or gets `204` once it ages out | `main.go`: `fromQuery`, `latency` |
| `HX-Push-Url: /?from=…&to=…` | `main.go`: `zoomTo` |
| The status line and form values as an `<hx-partial>` | `main.go`: `zoomTo`; `page.html` (`toolbar`) |
| The from/to form, with `hx-sync="closest .zoom:replace"` | `page.html` (`toolbar`) |
| The page reads `from`/`to` and the no-script `hp-x0`/`hp-x1` | `main.go`: `page` |

## How it works

`hx-plot-brush.js` turns a drag (or Shift+arrows and Enter) into an
`htmx:plot:brush` event, and the extension adds `hp-chart`, `hp-x0` and
`hp-x1` to the request that the wrapper's `hx-trigger` makes
([spec §5.3](../../../spec/spec.md#53-brush)). The values come from the
browser, so the server orders them, ignores a range that does not parse,
clips it to the data it has and widens one under a minute. It answers with the polling element
itself, because only an outer swap can change that element's `hx-get`,
and that is what keeps later polls zoomed. The `hx-sync` attributes put
the brush, the form and the polls on the `.zoom` element's queue, so a late
poll cannot swap the unzoomed chart back
([§5.4](../../../spec/spec.md#54-example-brush-to-zoom-a-live-chart)).

`HX-Push-Url` names the page URL of the zoom, and the page handler reads
it, so a bookmark, a reload and Back all show the same view. The from/to
form and the polite status line are the plain-HTML equivalents of
[§5.5](../../../spec/spec.md#55-equivalents-in-plain-html). See
[Pattern 6: brush to zoom](../../../docs/htmx.md#pattern-6-brush-to-zoom)
in the htmx guide; the [dashboard](../../dashboard/) also keeps keyboard
focus across Back with a small script.

The chart is drawn 720 px wide and scaled down by CSS on a narrow screen,
its text with it. To draw it at its box's width instead, put
`hx-plot:autosize` on the polling element, as the dashboard's brushable
chart does; the [sizing recipe](../sizing/) shows the attribute on its own.

## Tests

```sh
go test ./examples/recipes/brush-zoom
```

They check the page (status, CSP header, the pinned htmx tag, the brush
script, the `hx-sync` pair, the form), that a brush answers with the
zoomed polling element, the pushed URL and the toolbar partial, that an
inverted brush answers exactly like the ordered one, the accepted forms
(offsets, `datetime-local` in the chart's zone, fractions, a huge range
clipped to the data, a range under a minute widened), that bad values, a
range wholly outside the data and another chart's brush all get an empty
`204` with no pushed URL, that polls keep the range unchanged as the clock
moves and get a `204` once it ages out, Back to live, and that the page
shows the pushed URL, the no-script form and junk without echoing it.
