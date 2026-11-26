# Live dashboard

A service dashboard whose charts are rendered on the server by hyperplot and
kept live by htmx 4. It is the project's showcase and its integration test.

```sh
go run ./examples/dashboard              # http://localhost:8080
go run ./examples/dashboard -addr :9000
```

The server uses only the Go standard library. The page loads htmx 4.0.2 and
its `hx-sse` and `hx-ptag` extensions from jsDelivr (pinned, with Subresource
Integrity), so the browser needs network access.

## What it shows

| Card | Pattern | Endpoint |
|---|---|---|
| KPI tiles | `Sparkline`; delta vs the previous period with an arrow, a sign and words, never color alone | sent with the latency poll as an `<hx-partial>` |
| Latency (p50/p95/p99) | polling every 2 s with `hx-ptag`: unchanged charts cost a bodiless 304 | `GET /charts/latency` |
| Throughput | server-sent events, one `innerMorph` swap per second, message ids from `SendID` | `GET /stream/throughput` |
| Requests by region | grouped or stacked bars that change only when a window closes, so nearly every poll is a 304 | `GET /charts/requests` |
| Latency vs payload size | scatter of three series | `GET /charts/scatter` |

The filter row (range presets and region checkboxes) re-renders everything
with one `hx-get` and `innerMorph`. Each region pins a color `Slot`, so it
keeps its color when others are hidden, and the latency percentiles use
slots 5 to 7 so that no color means two things on the page. Every chart
re-renders at its container's width (`data-hp-autosize`, `hp:resize`,
`hx.Size`), and every chart has a "View as table" disclosure built from
`Chart.Table`.

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
SSE stream emits distinct messages, that slots survive filtering, and that
rendering is deterministic.
