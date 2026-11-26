# hx-plot from Python

A live dashboard served by Python's standard library alone, implementing the
[hx-plot wire spec](../../spec/spec.md) at Level 3 with no Go, as proof that
the htmx extension works with a server in any language.

## Run

From the repository root (Python 3.10 or later, no packages to install; on
Windows, type `python` or `py` for `python3`):

```sh
python3 examples/python/server.py                        # http://127.0.0.1:8080
python3 examples/python/server.py --addr 127.0.0.1:9000
python3 examples/python/server.py --speed 5              # simulated time runs 5x faster
```

Open the URL it prints. The page loads htmx 4.0.2 and its `hx-sse` and
`hx-ptag` extensions from jsDelivr (pinned, with Subresource Integrity), so
the browser needs network access. The server reads `hx-plot.js`,
`hx-plot-brush.js` and `hx-plot.css` from this repository's
[`client/`](../../client/README.md) directory; a real app gets them from npm
(`htmx-ext-plot`) or a CDN. Ctrl-C stops it.

## What to look at

1. **Polling with 304s.** Open the network panel. The latency chart polls
   `/charts/latency` every 2 seconds, but its data moves in 10-second
   buckets, so most polls are answered `304 Not Modified` with no body. Each
   200 carries an `HX-PTag` header, and the next poll sends it back. The
   server's log in the terminal shows the same.
2. **Sliding windows.** Every 10 seconds the latency window moves one bucket
   and the lines slide left instead of wobbling in place. Throughput slides
   every second. Run with `--speed 5` to see more of it.
3. **Server-sent events.** In the network panel, open the
   `/stream/throughput?hp-w=…` request and its EventStream tab: one unnamed
   message per second, whose `data:` lines are the chart and its table.
4. **`hp-w`.** Make the window narrower. Each chart's container fires
   `htmx:plot:resize`, and the next request carries `hp-w`, the container's
   width in CSS px; the server draws the chart that wide. The stream
   reconnects with the new width in its URL.
5. **Brush to zoom.** Drag across the latency chart (or focus it, press
   Shift+Arrow a few times, then Enter). The request carries `hp-chart`,
   `hp-x0` and `hp-x1`; the response sets `HX-Push-Url`, so the address bar
   shows the range and Back undoes the zoom. Polls stay zoomed. A drag of
   a few pixels is a brush of a second or two, which can fall between two
   data points, so the server widens a zoom to at least a minute
   (`MIN_ZOOM`). The From/To
   form sends the same request without a pointer.
6. **Select to drill down.** Click a bar of "Requests by region". The
   request carries `hp-x`, the window's category, and `hp-series`, the
   bar's region; the detail for that window and region opens below. Tab to
   the chart, use the arrow keys, then press Enter: the keyboard selects a
   category, not a bar, so the request carries `hp-x` without `hp-series`
   and the detail shows every region for that window. The "Details" links under the chart are the plain-HTML
   equivalent (spec §5.5): they send the same request without
   `hp-series`, so they show every region for that window, and they work
   without JavaScript.
7. **Reading the data.** Hover any chart, or focus it and use the arrow
   keys: the tooltip and the screen-reader announcements come from the
   `metadata.hp-tips` JSON in each chart. Open "View as table": it updates
   with the chart and stays open.
8. **Theme.** The Light, Dark and System links are plain links; the server
   writes the choice into `<html data-theme>`.
9. **No inline anything.** Every response carries a strict
   `Content-Security-Policy`, and the console stays clean.

## Key code

| What | Where |
|---|---|
| `hx-get` + `hx-trigger="load delay:1ms, every 2s, htmx:plot:resize"` + `hx-ptag` on the latency poller | [`server.py`](server.py): `App.latency_poller` |
| `HX-PTag` compared after trimming `W/` and quotes, bodiless 304 | [`server.py`](server.py): `Handler.tagged`, `bare` |
| `hx-plot:brush`, `hx-trigger="htmx:plot:brush"`, `hx-swap="outerMorph"`, `hx-sync` pair | [`page.html`](page.html), `.zoom`; [`server.py`](server.py): `Handler.latency` |
| `hp-x0`/`hp-x1` read and ordered, any RFC 3339 offset or `datetime-local` | [`server.py`](server.py): `brushed`, `brush_times`, `App.zoom_range` |
| `HX-Push-Url` and the live region as `<hx-partial>` | [`server.py`](server.py): `Handler.latency`, `partial` |
| `hx-trigger="htmx:plot:select"`, `hp-x`, `hp-series` | [`page.html`](page.html), `.drill`; [`server.py`](server.py): `selected`, `App.detail` |
| the stream container: first frame without `hx-sse:connect`, `load` fetches the stream element | [`page.html`](page.html), `#throughput-body`; [`server.py`](server.py): `App.stream_element` |
| `text/event-stream`, `retry:`, `data:` lines, 503 with `HX-Reswap: none` | [`server.py`](server.py): `Handler.throughput_stream`; [`hp_wire.py`](hp_wire.py): `sse_message` |
| `hx-plot:autosize` and `hp-w`, clamped to [50, 4096] | [`server.py`](server.py): `size` |
| the chart: root attributes, ids, geometry, hover payload, table | [`render.py`](render.py): `render` |
| FNV-1a keys with the collision rule | [`render.py`](render.py): `fnv1a`, `keys` |
| value forms and escaping | [`hp_wire.py`](hp_wire.py), the spec's Appendix B verbatim |

## How it works

[`server.py`](server.py) is an `http.server.ThreadingHTTPServer` with one
route per fragment, and every route answers HTML, as in any htmx app. It
decides what to draw and [`render.py`](render.py) draws it: the root
attributes, ids and geometry that the extension reads
([§3](../../spec/spec.md#3-the-chart-document-level-1),
[§4.1](../../spec/spec.md#41-identity),
[§4.2](../../spec/spec.md#42-geometry-attributes)), and the table equivalent
([§3.8](../../spec/spec.md#38-the-table-equivalent)).
[`data.py`](data.py) makes up values that depend only on their time bucket,
so an unchanged window renders the same bytes and its poll costs a 304.
[`hp_wire.py`](hp_wire.py) is the spec's
[Appendix B](../../spec/spec.md#appendix-b-an-exchange-and-helpers-in-python),
copied verbatim: number and time forms, escaping, the tips JSON and SSE
messages.

The htmx patterns are the ones the integration guide describes for Go:

- polling with tags: [Pattern 1](../../docs/htmx.md#pattern-1-polling-with-content-tags)
  and [spec §4.5](../../spec/spec.md#45-transports);
- the stream: [Pattern 2](../../docs/htmx.md#pattern-2-server-sent-events),
  with the container of spec §4.5 so that a resize opens a new stream;
- the live region in the same response:
  [Pattern 4](../../docs/htmx.md#pattern-4-multi-target-updates);
- select: [Pattern 5](../../docs/htmx.md#pattern-5-drill-down-on-select) and
  [spec §5.2](../../spec/spec.md#52-select);
- brush: [Pattern 6](../../docs/htmx.md#pattern-6-brush-to-zoom) and
  [spec §5.4](../../spec/spec.md#54-example-brush-to-zoom-a-live-chart);
- sliding windows: [Pattern 7](../../docs/htmx.md#pattern-7-sliding-windows)
  and [spec §4.4](../../spec/spec.md#44-frame-stability);
- sizing: [autosize](../../docs/htmx.md#responsive-charts-with-autosize) and
  [spec §4.6](../../spec/spec.md#46-size-parameters).

[Implementing an hx-plot server](../../docs/implementing-a-server.md) builds a
smaller version of this server step by step. Everything is in UTC: the axes,
the row headings and the zoom form's `datetime-local` inputs.

## Tests

From the repository root:

```sh
python3 -m unittest discover examples/python
```

- `test_wire.py`: `hp_wire.py` equals the spec's Appendix B; the keys of
  Appendix A; the parameter readers against the server cases of
  [`params.json`](../../spec/conformance/params.json) (the time-zone
  cases are skipped on a Python without a tz database, such as Windows
  without the `tzdata` package).
- `test_render.py`: the renderer against the
  [conformance fixtures](../../spec/conformance/README.md) it can draw.
- `test_server.py`: a server on a free port with a fixed clock: CSP, XML
  well-formedness, 304s, `hp-w`, brush, select, the stream, and that no
  parameter gets a 5xx.

The language-neutral checker in [`spec/hpcheck/`](../../spec/hpcheck/README.md)
checks the same output against every assertion of the fixtures, and a
running server against the spec. The fixtures go to a scratch directory
outside the repository:

```sh
out=$(mktemp -d)
python3 examples/python/render_fixtures.py "$out"
python3 spec/hpcheck/hpcheck.py fixtures --out "$out" \
    --require line-time,empty,bars-band,escaping,collisions
python3 spec/hpcheck/hpcheck.py server http://127.0.0.1:8080 \
    --config examples/python/hpcheck.json \
    --spawn "python3 examples/python/server.py --addr 127.0.0.1:{port}"
```

`render_fixtures.py` draws `line-time`, `empty`, `bars-band`, `escaping`,
`collisions` and `slide`; it skips `scatter-xy`, because this example has no
scatter chart. [`hpcheck.json`](hpcheck.json) tells the checker which URLs
to exercise.
