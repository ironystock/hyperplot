# Building sensors

A building monitor whose twelve simulated sensors stream over server-sent
events, with a select to drill into one sensor and a brush to zoom its chart,
all drawn as SVG by the Go reference server and kept live by htmx 4 and
hx-plot.

## Run

```sh
go run ./examples/sensors              # http://localhost:8080
go run ./examples/sensors -speed 30    # simulated time 30x faster: minutes close every 2 s
go run ./examples/sensors -addr :9000
```

`-speed` takes a number in (0, 1000]. The page loads htmx 4.0.2 and its
`hx-sse` and `hx-ptag` extensions from a CDN, so the browser needs network
access; the server serves `hx-plot.js`, `hx-plot-brush.js` and
`hx-plot.css` itself, from this module.

## What to look at

1. **The tile grid streams.** Each tile shows a sensor's reading of the current
   second and a sparkline of its last 30 minutes. In devtools, Network,
   open `/stream/tiles` and watch the EventStream tab: one message per
   second, each with an `id:` (the second) and the whole grid as `data:`
   lines, followed by an `<hx-partial hx-target="#alerts">`. A `retry: 2000`
   line comes first.
2. **Morphs keep nodes.** In Elements, select a tile or an alerts table row
   and watch it: the node stays and only its text and paths change, because
   tiles (`tile-t-srv`) and rows (`alert-t-srv-20261126t1506`, the sensor
   and the minute the alert began) have stable ids.
3. **Polls are mostly 304.** The over-limit chart polls every 10 s and the
   detail chart every 5 s. Both send `HX-PTag` and get a bodiless
   `304 Not Modified` until a minute closes (run without `-speed` to see
   long runs of 304s).
4. **Select a sensor.** Click a bar of the over-limit chart, or focus the
   chart, step with the arrow keys and press Enter. The request is
   `/detail?hp-w=…&hp-chart=overlimit&hp-x=CO2-MTG`: `hp-x` is the bar's
   category, the sensor code. The response replaces the detail panel and
   sends `HX-Push-Url: /?sensor=CO2-MTG`, so Back returns to the previous
   sensor. Clicking a tile sends `/detail?sensor=…`, the same request without
   a pointer.
5. **Thresholds.** The detail chart draws the sensor's warning and alarm
   limits as rules. The y axis always reaches both, so you see how close a
   reading came.
6. **Brush to zoom.** Drag across the detail chart (or Shift+arrow keys,
   then Enter). The request carries `hp-chart=trend&hp-x0=…&hp-x1=…`; the
   answer is the whole polling element with `from` and `to` in its `hx-get`,
   so later polls stay zoomed, plus the zoom bar as an `<hx-partial>` and
   `HX-Push-Url`. The From/To form sends the same parameters; without script
   it submits to the page. An alert row's sensor link zooms the detail to that
   alert, ten minutes either side.
7. **Sizing.** Resize the window: the charts re-render at their container's
   width (`hp-w` on each poll), while the sparklines keep their size.
8. **Theme.** The Light, Dark and System links are plain links: the server
   writes `data-theme` on `<html>` and remembers the choice in a cookie. The
   page has no script of its own, and its strict Content-Security-Policy
   allows nothing inline.

## Key code

| What | Where |
|---|---|
| `hx-sse:connect="/stream/tiles"`, `hx-swap="innerMorph"` | [templates/page.html](templates/page.html), `#tiles` |
| SSE stream: `hx.NewStream`, `Retry`, `SendID`, `LastEventID` | [server.go](server.go) `tileStream` |
| Tiles + alerts in one message: `hx.Partial("#alerts", "innerMorph", …)` | [server.go](server.go) `liveFragment` |
| `plot.Sparkline` tiles, stable tile ids | [charts.go](charts.go) `tiles` |
| Alert rows with stable ids | [charts.go](charts.go) `newAlertsView`, [data.go](data.go) `alerts` |
| `hx-ptag`, `hx.Tag`, `hx.CheckPTag` | [server.go](server.go) `writeTagged`, `page` (seeded tags) |
| `hx-trigger="htmx:plot:select"` on the bar chart's wrapper | [templates/page.html](templates/page.html), `.drill` |
| `hx.Selected`, untrusted codes | [gestures.go](gestures.go) `selectedSensor` |
| `HX-Push-Url` on select and zoom | [server.go](server.go) `detail`, `zoomTo` |
| `plot.RuleY` thresholds | [charts.go](charts.go) `detailChart` |
| `hx-plot:brush`, `hx-trigger="htmx:plot:brush"`, `hx-swap="outerMorph"` | [templates/page.html](templates/page.html), `.zoom` |
| `hx.Brushed`, `Brush.TimesIn`, zoom limits | [gestures.go](gestures.go) `brushZoom`, `newZoom` |
| `hx-sync="#detail:replace"` / `"#detail:drop"` | [templates/page.html](templates/page.html), every request that changes `#detail` |
| `hx-plot:autosize`, `hx.Size` | [templates/page.html](templates/page.html), [server.go](server.go) `chartWidth` |
| Deterministic readings (FNV-1a noise per bucket) | [data.go](data.go) `noise`, `at`, `live` |

## How it works

The page uses three transports, each for what it does best:

- **One SSE stream for everything that changes every second.** The tile
  grid is the stream's own content, and the alerts table rides along as an
  `<hx-partial>` ([spec §4.5](../../spec/spec.md#45-transports),
  [Pattern 2](../../docs/htmx.md#pattern-2-server-sent-events) and
  [Pattern 4](../../docs/htmx.md#pattern-4-multi-target-updates)). One
  connection per page instead of two. The sparklines have a fixed size, so
  the stream URL carries no width and the page connects straight away; the
  [dashboard](../dashboard/README.md) shows the container recipe for a
  streamed chart that must follow its width.
- **Polling with content tags for charts that change once a minute**
  ([Pattern 1](../../docs/htmx.md#pattern-1-polling-with-content-tags)).
  The server hashes each response; a poll whose `HX-PTag` matches gets a
  304, and htmx swaps nothing. The page seeds each container's `hx-ptag`
  with the tag of what its first poll will return.
- **Gestures are ordinary requests.** A select
  ([spec §5.2](../../spec/spec.md#52-select),
  [Pattern 5](../../docs/htmx.md#pattern-5-drill-down-on-select)) and a
  brush ([spec §5.3](../../spec/spec.md#53-brush),
  [§5.4](../../spec/spec.md#54-example-brush-to-zoom-a-live-chart),
  [Pattern 6](../../docs/htmx.md#pattern-6-brush-to-zoom)) arrive as
  `hp-*` query parameters. The server validates them like any input:
  an unknown sensor code or another chart's select gets `204 No Content`
  (htmx swaps nothing), and a brush whose values don't parse, or that
  lies wholly outside the 7-day history (a brush in year 1 or year 9999),
  is ignored the same way ([spec §7](../../spec/spec.md#7-security-considerations)).
  A zoomed poll draws exactly the `from` and `to` in its URL; once the
  history starts to cut that window, the poll answers 204 and the chart
  keeps its last frame rather than drawing a window the zoom bar and the
  address bar do not name. Tiles, alert rows and the From/To form are the plain-HTML equivalents
  ([spec §5.5](../../spec/spec.md#55-equivalents-in-plain-html)).

Ids are part of the wire ([spec §4.1](../../spec/spec.md#41-identity)):
chart ids are explicit (`overlimit`, `trend`) and every tile and alert row
has an id that names what it shows, so morphs update in place. The detail
chart keeps its id when you switch sensors, so it morphs from one sensor to
the next. The live detail window includes one minute before its left edge,
drawn and clipped, so the line reaches the edge as it slides
([spec §4.4](../../spec/spec.md#44-frame-stability),
[Pattern 7](../../docs/htmx.md#pattern-7-sliding-windows)).

Readings are a pure function of the sensor and the time bucket (a minute for
the charts and alerts, a second for the tile values), with hash-based noise
and occasional 20-minute excursions that cross the limits. The same instant
always renders the same bytes, which is what makes the 304s and the tests
work.

## Tests

```sh
go test ./examples/sensors
go test -race ./examples/sensors
```

[main_test.go](main_test.go) drives `routes()` with a fixed clock: every
route returns 200 with the CSP header; every `<svg>` parses as XML; no
response has a `style` or `on*` attribute or inline script; page ids are
unique; the ptag round trip (304 on a matching tag in all its forms, 200 on
another tag or width); the tags change when a minute closes and the page
seeds them; `hp-w` sets and clamps the width; select and brush change the
output and push the page URL; a brush outside the history gets 204; a
zoomed poll keeps its window, or answers 204 once the clock ages it out;
bad or hostile parameters never cause a 5xx or
echo markup; the SSE stream sends `retry:`, distinct ids, the tiles and the
alerts partial, and honors `Last-Event-ID`; alert row ids survive as time
moves on.
