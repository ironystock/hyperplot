# Server-sent events

A chart the server pushes once a second over one long-lived response,
using htmx's `hx-sse` extension and `hx.Stream`.

## Run

```sh
go run ./examples/recipes/sse-stream            # http://localhost:8080
go run ./examples/recipes/sse-stream -addr :9000
```

## What to look at

1. The lines slide one second to the left every second. Each frame is a
   whole chart; `innerMorph` keeps `id="throughput"` and its series, so
   `hx-plot.js` animates the move.
2. In devtools' Network panel, `GET /cards/throughput?hp-w=…` comes first:
   the page's container asks for a stream element at its real width. Then
   `GET /stream/throughput?hp-w=…` stays open. Its EventStream tab lists
   the messages: an `id` (the second shown) and the chart as `data:` lines.
   The first message also carries `retry: 2000`.
3. Resize the window. The container fetches a new stream element for its
   new width, the old element leaves the page and its stream closes, and
   a new stream opens at the new width. Only one stream is ever open.
4. Stop the server while the page is open, then start it again: `hx-sse`
   reconnects (after about two seconds, backing off while the server is
   down) and sends the last id it saw as `Last-Event-ID`. A server that
   still has that second would skip it rather than send it twice.
5. Switch to another tab and back: `hx-sse` pauses the stream of a hidden
   tab; the request is canceled and the server's loop returns.

## Key code

| What | Where |
|---|---|
| The container: `hx-plot:autosize`, `hx-trigger="load, htmx:plot:resize"`, `hx-swap="innerHTML"` | `page.html` |
| The stream element: `hx-sse:connect="/stream/throughput?hp-w=…"`, `hx-swap="innerMorph"` | `page.html` (`stream`); `main.go`: `card` |
| The `hx-sse.min.js` extension script, not deferred | `page.html` |
| `hx.NewStream(w, r)`; on failure `503` with `HX-Reswap: none` | `main.go`: `stream` |
| `stream.Retry(2 * time.Second)` | `main.go`: `stream` |
| `stream.SendID(id, svg)` and `stream.LastEventID()` | `main.go`: `stream` |
| Stop on `r.Context().Done()` or the first send error | `main.go`: `stream` |
| `BaseContext`, so Ctrl-C ends open streams | `main.go`: `run` |

## How it works

`hx-sse:connect` opens a GET request that the server keeps open, and every
unnamed message's `data:` lines are swapped into the element with its
`hx-swap` ([spec §4.5](../../../spec/spec.md#45-transports)). A stream's URL,
and so its `hp-w`, is fixed when it connects, so the page uses the
container shape the spec recommends: the page draws the first frame
without a stream, and the container fetches a stream element at its real
width on `load` and after every resize
([§4.6](../../../spec/spec.md#46-size-parameters)). It replaces the element
with `innerHTML`, because a morph would keep the node and `hx-sse` would
keep reconnecting to the old URL.

The handler sends `retry:` (a SHOULD of §4.5), a frame per tick with an id,
and returns when the request's context is done, which is how a closed or
hidden tab ends the loop. A frame that fails to render is skipped rather
than ending the stream, and a stream that cannot start answers `503` with
`HX-Reswap: none`, so nothing is swapped and a reconnect backs off. See
[Pattern 2: server-sent events](../../../docs/htmx.md#pattern-2-server-sent-events),
[Replay with `Last-Event-ID`](../../../docs/htmx.md#replay-with-last-event-id)
and the [operational notes](../../../docs/htmx.md#operational-notes) in the
htmx guide.

## Tests

```sh
go test ./examples/recipes/sse-stream
```

They check the page (status, CSP header, the pinned htmx and `hx-sse` tags
with their integrity hashes, the container's attributes, no
`hx-sse:connect` on the page itself), the stream element for several
`hp-w` values, and the stream through a real `httptest.Server`: its
content type, `retry: 2000` on the first message, rising ids, and that
each message is one well-formed chart at the requested width. They also
check that a reconnect with `Last-Event-ID` skips the known second, that
a writer that cannot flush gets `503` with `HX-Reswap: none`, that the
handler returns once the client disconnects (`httptest.Server.Close` waits
for every running handler), and that Ctrl-C shuts the server down with a
stream open.
