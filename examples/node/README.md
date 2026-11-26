# hx-plot on Node

A live dashboard served by Node's own `http` module, with no dependencies
and no framework, that speaks the [hx-plot wire spec](../../spec/spec.md) at
[Level 3](../../spec/spec.md#21-server-levels): polled and streamed charts,
brush to zoom and click to drill down, all drawn as SVG by about 250 lines
of JavaScript.

## Run

Node 22 or later. There is nothing to install.

```sh
node examples/node/server.mjs
node examples/node/server.mjs --addr 127.0.0.1:9000 --speed 10
```

Open http://localhost:8080/. `--speed` runs the simulated clock faster
(`10` shows a minute of data in six seconds). The server logs one line per
request, so you can watch the `304`s go by. Ctrl-C stops it.

The page loads htmx 4.0.2 and its `hx-sse` and `hx-ptag` extensions from
the CDN (pinned, with Subresource Integrity), and the server reads
`hx-plot.js`, `hx-plot-brush.js` and `hx-plot.css` from this repository's
[`client/`](../../client/) directory. A real app installs `htmx-ext-plot`
from npm or loads it from a CDN.

## What to look at

1. **Polls that cost nothing.** Open the developer tools' Network tab. The
   latency chart polls `/charts/latency` every 2 s, but its data moves in
   5 s buckets, so two polls in three come back `304 Not Modified` with no
   body: htmx's `hx-ptag` extension sends the last response's `HX-PTag`,
   and the server answers an unchanged chart with a `304`.
2. **Charts sized by their container.** Every chart request carries
   `hp-w`, the width of its `hx-plot:autosize` container. Make the window
   narrower: the extension dispatches `htmx:plot:resize`, the triggers turn
   it into a request, and the server redraws the chart at the new width.
3. **A sliding window.** When the latency window moves one bucket, the
   line slides left instead of wobbling in place. The Animations panel
   shows the extension's Web Animations on the anchored elements.
4. **Brush to zoom.** Drag across the latency chart (or focus it, press
   Shift+Arrow keys, then Enter). The request carries `hp-chart=latency`,
   `hp-x0` and `hp-x1`; the response is the whole polling element, whose
   `hx-get` now carries `from` and `to`, an `<hx-partial>` for the status
   line, and an `HX-Push-Url` header. The address bar changes, Back undoes
   the zoom, and polls of the zoomed window are all `304`s. The From/To
   form under the chart sends the same request without a pointer.
5. **Server-sent events.** Click `/stream/throughput` in the Network tab
   and open its EventStream view: one message a second with an `id:` and
   the chart and its table split into `data:` lines. The first message
   also carries `retry:`, because hx-sse ignores a `retry:` that arrives
   in a message without data.
6. **Click to drill down.** Click a bar. The request is
   `/detail/requests?hp-chart=requests&hp-x=14:05&hp-series=eu-west&hp-w=…`, and
   the detail for that minute and region lands below. Focus the bar chart,
   use the arrow keys, then press Enter: the keyboard selects a minute, not
   a bar, so the request carries `hp-x` without `hp-series` and the detail
   shows every region for that minute. The minute links
   under the chart are the plain-HTML equivalent (spec §5.5): they send
   the same request without `hp-series`, so they show every region for
   that minute, and they work without JavaScript.
7. **The table equivalent.** Open "View as table" under any chart: it
   updates with the chart, stays open across polls, and keeps its rows,
   which have stable ids.
8. **A strict Content-Security-Policy.** Every response sends one that
   allows no inline script or style, and the Console stays empty. The
   theme links (`?theme=dark`) need no script either.

## Key code

| htmx attribute, header or parameter | Where |
|---|---|
| `hx-get`, `hx-trigger="load delay:1ms, every 2s, htmx:plot:resize"`, `hx-swap="innerMorph"`, `hx-ptag` | [page.mjs](page.mjs): `poller` |
| `HX-PTag` and the bodiless `304` | [server.mjs](server.mjs): `ptag`, `sendTagged`; [wire.mjs](wire.mjs): `bareTag` |
| `hx-plot:autosize` and `hp-w` | [page.mjs](page.mjs): `poller`, `page`; [server.mjs](server.mjs): `chartWidth`; [wire.mjs](wire.mjs): `readSize` |
| `hx-plot:brush`, `htmx:plot:brush`, `hx-sync` | [page.mjs](page.mjs): `page` (the `.zoom` element) |
| `hp-x0`, `hp-x1`, `HX-Push-Url`, `<hx-partial>` | [server.mjs](server.mjs): `latencyRoute`; [charts.mjs](charts.mjs): `zoomWindow`; [wire.mjs](wire.mjs): `brushTimes`, `parseTimeValue` |
| `hx-sse:connect` in a container that fetches it | [page.mjs](page.mjs): `page` (`#throughput-body`), `stream`; [server.mjs](server.mjs): `throughputCard` |
| `text/event-stream`, `retry:`, `id:`, `data:` | [server.mjs](server.mjs): `throughputStream`; [wire.mjs](wire.mjs): `sseMessage` |
| `htmx:plot:select`, `hp-chart`, `hp-x`, `hp-series` | [page.mjs](page.mjs): `page`; [server.mjs](server.mjs): `detailFor`; [charts.mjs](charts.mjs): `findMinute` |
| Links that select without a pointer | [page.mjs](page.mjs): `requestsBody` |
| The chart markup, ids and hover JSON | [render.mjs](render.mjs): `render`; [wire.mjs](wire.mjs): `keys`, `tipsJson` |
| Escaping | [wire.mjs](wire.mjs): `xmlText`, `xmlAttr`; [page.mjs](page.mjs): `html` |
| `Content-Security-Policy` | [server.mjs](server.mjs): `CSP`, `send` |

## How it works

| File | What it does |
|---|---|
| [server.mjs](server.mjs) | the `node:http` server: routes, polling tags, the stream, gesture parameters, flags |
| [page.mjs](page.mjs) | the page and its fragments, written with an `html` tagged template that escapes everything it interpolates |
| [charts.mjs](charts.mjs) | the four charts as data for a time window, and the bounds on zooms and selects |
| [render.mjs](render.mjs) | one frame of a line chart (time axis) or a bar chart (band axis) as SVG, and its table |
| [wire.mjs](wire.mjs) | a port of the spec's [Appendix B](../../spec/spec.md#appendix-b-an-exchange-and-helpers-in-python) helpers (`number`, `timeValue`, `parseTimeValue`, `xmlText`, `tipsJson`, `sseMessage`, …), the [FNV-1a keys](../../spec/spec.md#appendix-a-fnv-1a-keys), and readers for the `hp-*` parameters |
| [data.mjs](data.mjs) | stand-in data: each value a pure function of its series and time bucket |

There is no JSON API: every response is HTML, and htmx swaps it in. The
server draws each chart, formats every value, and gives every chart, series,
bar and table row a stable id ([§4.1](../../spec/spec.md#41-identity)); the
extension reads the markup and the hover JSON
([§3.5](../../spec/spec.md#35-hover-metadata)) and handles the pointer, the
keyboard, the morph and the slide.

- Polling with tags: [spec §4.5](../../spec/spec.md#45-transports),
  [htmx guide, pattern 1](../../docs/htmx.md#pattern-1-polling-with-content-tags).
- Server-sent events and the stream container:
  [spec §4.5](../../spec/spec.md#45-transports),
  [pattern 2](../../docs/htmx.md#pattern-2-server-sent-events).
- Drill-down: [spec §5.2](../../spec/spec.md#52-select),
  [pattern 5](../../docs/htmx.md#pattern-5-drill-down-on-select).
- Brush to zoom: [spec §5.3](../../spec/spec.md#53-brush) and
  [§5.4](../../spec/spec.md#54-example-brush-to-zoom-a-live-chart),
  [pattern 6](../../docs/htmx.md#pattern-6-brush-to-zoom).
- Sliding windows and the overscan point:
  [spec §4.4](../../spec/spec.md#44-frame-stability),
  [pattern 7](../../docs/htmx.md#pattern-7-sliding-windows).
- Sizes: [spec §4.6](../../spec/spec.md#46-size-parameters),
  [autosize](../../docs/htmx.md#responsive-charts-with-autosize).
- Everything a server must get right, as a checklist:
  [implementing a server](../../docs/implementing-a-server.md#conformance-checklist).
  The [Python example](../python/README.md) is the same server in Python:
  the same routes, chart ids (`latency`, `throughput`, `requests`,
  `detail`) and gesture parameters, so its requests work against this
  server. Its stand-in data differs: Python's latency buckets are 10 s
  (here 5 s, so the `304` ratio differs) and its bars are five-minute
  windows (here one minute).

Times in this example are UTC: row keys and `hp-x0`/`hp-x1` are RFC 3339
times with `Z`, and the zoom form's `datetime-local` values are read as UTC.
`parseTimeValue` also reads them in any IANA time zone, as the spec asks of
a chart drawn in local time.

## Tests

```sh
cd examples/node && node --test
```

`wire.test.mjs` checks the helpers against the spec's examples and every
server case of [`params.json`](../../spec/conformance/params.json).
`render.test.mjs` renders the `line-time`, `empty`, `bars-band`,
`escaping`, `collisions` and `slide` fixtures and runs their `checks.json`
assertions, `should` ones included, with a small XML parser and selector
matcher in `testing.mjs` (Node has no XML parser built in).
`server.test.mjs` starts the server on a free port with a clock the tests
control, and checks the page's CSP, that every chart in every response
parses, the `304`s, zoom, drill-down, the stream, `hp-w`, and that no
parameter causes a `5xx`.

To check the output with the repository's language-neutral checker,
[`spec/hpcheck/`](../../spec/hpcheck/README.md), render the fixtures to
a scratch directory outside the repository and point it at them, or at
the server (`--spawn` starts it on a free port and stops it afterwards):

```sh
out=$(mktemp -d)
node examples/node/render-fixtures.mjs "$out"
python3 spec/hpcheck/hpcheck.py fixtures --out "$out" --require line-time,empty,bars-band,escaping,collisions
python3 spec/hpcheck/hpcheck.py server http://127.0.0.1:8080 --config examples/node/hpcheck.json \
    --spawn "node examples/node/server.mjs --addr 127.0.0.1:{port}"
```
