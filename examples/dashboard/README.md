# Live dashboard

A service dashboard whose charts are rendered on the server by hyperplot and
kept live by htmx 4. It is the project's showcase and its integration test.

```sh
go run ./examples/dashboard              # http://localhost:8080
go run ./examples/dashboard -addr :9000
go run ./examples/dashboard -speed 5     # simulated time runs 5x faster
```

`-speed` makes the sliding windows move sooner, which helps when watching
the slide and is what the real-browser tests in `client/browser/` use.

The server uses only the Go standard library. The page loads htmx 4.0.2 and
its `hx-sse` and `hx-ptag` extensions from jsDelivr (pinned, with Subresource
Integrity), so the browser needs network access. The hx-plot extension
(`hx-plot.js`) and its brush companion (`hx-plot-brush.js`) are served by the
Go package `client`.

## What it shows

| Card | Pattern | Endpoint |
|---|---|---|
| KPI tiles | `Sparkline`; delta vs the previous period with an arrow, a sign and words, never color alone | sent with the latency poll as an `<hx-partial>` |
| Latency (p50/p95/p99) | polling every 2 s with `hx-ptag`: unchanged charts cost a bodiless 304; **brush to zoom** | `GET /charts/latency` |
| Throughput | server-sent events, one `innerMorph` swap per second, message ids from `SendID` | `GET /stream/throughput` |
| Requests by region | grouped or stacked bars that change only when a window closes, so nearly every poll is a 304; **select to drill down** | `GET /charts/requests`, `GET /detail/requests` |
| Latency vs payload size | scatter of three series | `GET /charts/scatter` |

The filter row (range presets and region checkboxes) re-renders everything
with one `hx-get` and `innerMorph`. Each region pins a color `Slot`, so it
keeps its color when others are hidden, and the latency percentiles use
slots 5 to 7 so that no color means two things on the page. Every chart
re-renders at its container's width (`hx-plot:autosize`, `htmx:plot:resize`,
`hx.Size`), and every chart has a "View as table" disclosure built from
`Chart.Table`. Sliding windows draw one bucket before the window, so the
lines reach the left edge while they slide (docs/spec.md §4.4).

## Gestures

Both gestures are ordinary htmx requests (docs/spec.md §5); the server
answers with HTML like for any other request.

**Brush to zoom (latency).** The chart sits in an element with
`hx-plot:brush hx-trigger="htmx:plot:brush" hx-target="#latency-body"
hx-swap="outerMorph" hx-sync="replace"`. Dragging across the chart (or
Shift+arrows and Enter on the focused chart) sends `hp-x0` and `hp-x1` to
`/charts/latency`, which tells a brush from a poll with `hx.Brushed`. It
answers with the whole polling element, whose `hx-get` now carries
`from=…&to=…`, so later polls stay zoomed; the toolbar comes along as an
`<hx-partial>`, and `HX-Push-Url` names the page URL of the zoom, so the
view can be bookmarked and Back undoes it (`app.js` puts keyboard focus
back on the chart after htmx restores the page). The polling element carries
`hx-sync="closest .zoom:drop"`, so a poll cannot swap the unzoomed chart
back while the zoom is pending. A zoomed window uses finer buckets (at most
120). The from/to form sends the same `hp-x0`/`hp-x1` from
`datetime-local` inputs, read in the chart's time zone (`Brush.TimesIn`),
and works without script too; "Back to live" (`zoom=out`) returns to the
sliding window. A brush that does not parse gets 204, which htmx does not
swap.

**Select to drill down (requests by region).** An element around the chart
has `hx-trigger="htmx:plot:select" hx-target="#requests-detail"`. A click on
a bar sends `hp-x` (the bar's category) and `hp-series` (its region) to
`/detail/requests`, which reads them with `hx.Selected` and renders that
window for that region: its numbers, a finer chart and its table. Enter on
a focused bar names no series, so the detail covers every selected region.
`#requests-detail` has `hx-plot:autosize`, so the request also carries its
width. The window links under the chart send the same request without a
pointer (and work as plain links without script); Close sends none, which
is no selection. Each link has an id derived from its window
(`window-` and the FNV-1a hash of its label), so the poll's morph keeps a
focused link on its window as windows slide instead of handing it the
next window's text and URL; when the focused link's window slides out,
`app.js` moves focus to the oldest window still listed.

After either gesture the server updates a polite live region (the zoom
toolbar's status line, the detail's status), so screen readers hear what
changed. A filter change returns to the live window and closes the detail.

The data is simulated and deterministic: each value is a pure function of
a region, a metric and a time bucket (`data.go`), so polls within a bucket
render identical bytes and get 304s.

## Details worth copying

- The page runs under a strict `Content-Security-Policy` with no inline
  script or style. Its `script-src` trusts only the pinned htmx release's
  directory on the CDN, not the CDN's whole origin, which serves anyone's
  packages.
- The `http.Server` sets read, header and write timeouts. The SSE stream
  outlives `WriteTimeout` because `hx.Stream` moves the write deadline on
  every message.
- Handlers that fail answer htmx requests with 204, so a chart keeps its last
  good render (htmx 4 swaps every other status). The SSE stream is the
  exception: if it can't start it answers 503 with `HX-Reswap: none`, because
  hx-sse backs off only after a non-2xx reconnect, and it skips frames that
  fail to render instead of ending the stream.
- A filter change sends each chart's current width along (a hidden input per
  chart, associated with the filter form), so charts re-render at their real
  size without a jump; `HX-Replace-Url` keeps the address bar clean.
- Several htmx 4.0.2 pitfalls are worked around and explained in
  `templates/page.html` and `server.go`; see "htmx 4 gotchas" in
  [docs/htmx.md](../../docs/htmx.md#htmx-4-gotchas).

## Tests

```sh
go test ./examples/dashboard
```

`main_test.go` checks that every route answers 200 with well-formed SVG,
that a ptag round trip yields 304 and changes when a bucket closes, that the
SSE stream emits distinct messages, that slots survive filtering, that
rendering is deterministic, and the gesture exchanges: brush parameters
(RFC 3339 and `datetime-local`), the zoom rules, the zoom answer and the
polls after it, ignored brushes, selects, window links and Close.

`client/browser/dashboard.browser.mjs` drives the same gestures in Chrome
(a real drag, the form, Back, a click on a bar, Enter, the links);
`dashboard-a11y.browser.mjs` checks that a focused window link keeps its
window as windows slide and that the page reflows at 320 CSS px without
sideways scrolling (WCAG 1.4.10); the
other files there cover the slide and the live tables on this page. Run
them with `make browser` after `npm ci` in `client/`.
