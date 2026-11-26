# Live charts with htmx 4

This guide shows how to keep charts live with [htmx 4](https://four.htmx.org)
and the `hx-plot` extension: polling with content tags, server-sent events,
filters, multi-target updates, responsive sizing, drill-down, zoom and
sliding windows. The HTML is the same whatever your server is written in;
the handlers here use the Go reference server (`hyperplot` and its `hx`
package). If your server is in another language, read the HTML and the
explanations, and take the wire details from the [spec](../spec/spec.md) and the
[server guide](implementing-a-server.md). The numbered patterns all run in
the [examples](../examples/README.md), and the Python and Node examples
serve the same kind of HTML without Go.

It assumes you have rendered a static chart already (see the
[README](../README.md#quick-start)). The facts about htmx here are for
**htmx 4.0.2**. htmx 4 differs from htmx 2 in ways that matter for charts;
see [htmx 4 gotchas](#htmx-4-gotchas) before porting htmx 2 code.

> hyperplot is pre-release. The extension follows [spec version 1](../spec/spec.md),
> and the Go packages follow [DESIGN.md](DESIGN.md); details may change
> before the first release.

**Contents**

- [How live updates work](#how-live-updates-work)
- [Setup](#setup)
- [Reference: attributes, headers, parameters and events](#reference-attributes-headers-parameters-and-events)
- [Pattern 1: polling with content tags](#pattern-1-polling-with-content-tags)
- [Pattern 2: server-sent events](#pattern-2-server-sent-events)
- [Pattern 3: filters](#pattern-3-filters)
- [Pattern 4: multi-target updates](#pattern-4-multi-target-updates)
- [Pattern 5: drill-down on select](#pattern-5-drill-down-on-select)
- [Pattern 6: brush to zoom](#pattern-6-brush-to-zoom)
- [Pattern 7: sliding windows](#pattern-7-sliding-windows)
- [Responsive charts with autosize](#responsive-charts-with-autosize)
- [Tooltips, keyboard reading and tables](#tooltips-keyboard-reading-and-tables)
- [Errors](#errors)
- [Content Security Policy](#content-security-policy)
- [htmx 4 gotchas](#htmx-4-gotchas)
- [Testing handlers](#testing-handlers)
- [Production checklist](#production-checklist)

## How live updates work

1. The server renders a chart to SVG. The output is **deterministic**: the
   same data gives byte-identical SVG.
2. Every element that can change has a **stable id**: the chart has
   `Chart.ID`, each series group is `{ID}-s-{key}`, each bar is
   `{ID}-b-{key}-{catkey}`, and each table row is `{ID}-r-{key}`, where the
   keys are hashes of the series name, category or x value
   ([spec §4.1](../spec/spec.md#41-identity)).
3. htmx swaps the new SVG in with **`innerMorph`**. htmx 4's morph (its own,
   not idiomorph) matches elements by id first, then by equality, then by
   position, and updates them in place instead of replacing them.
4. Because the elements survive, **CSS transitions** animate the change:
   with `Chart.Animate`, the stylesheet transitions bar and area paths (`d`),
   dot positions and opacity. Readers who prefer reduced motion get no
   animation.
5. The **`hx-plot` extension** takes part in every swap through htmx's swap
   hooks. It re-applies the tooltip or keyboard position to the new frame,
   slides a chart whose time window moved (a CSS transition of `d` would
   make the line wobble in place), and keeps an open data table open and
   scrolled to its newest row.

Two things follow from this: set a stable `Chart.ID` on every chart you
update, and swap with `innerMorph` into a wrapper element.

## Setup

### Scripts and stylesheet

```html
<link rel="stylesheet" href="/static/hx-plot.css">

<script src="https://cdn.jsdelivr.net/npm/htmx.org@4.0.2/dist/htmx.min.js"></script>
<!-- Only the htmx extensions you use: -->
<script src="https://cdn.jsdelivr.net/npm/htmx.org@4.0.2/dist/ext/hx-ptag.js"></script>
<script src="https://cdn.jsdelivr.net/npm/htmx.org@4.0.2/dist/ext/hx-sse.min.js"></script>
<!-- The chart extension, and its optional companions: -->
<script src="/static/hx-plot.js"></script>
<script src="/static/hx-plot-brush.js"></script>  <!-- brushing (pattern 6) -->
<script src="/static/hx-plot-tween.js"></script>  <!-- animates value changes in Safari -->
```

htmx 4 extensions are activated by loading their script. There is no
`hx-ext` attribute. Load htmx and the extensions without `defer`: a
deferred htmx 4.0.2 can start before a deferred extension has loaded, and
an `hx-sse:connect` element then never connects. Pin exact versions in
production, or self-host the files (see
[Content Security Policy](#content-security-policy)).

`hx-plot.js`, its companions and `hx-plot.css` come from the npm package
`htmx-ext-plot` ([its README](../client/README.md)); until it is published,
take them from [`client/`](../client/), or serve them from Go as below.

If you restrict extensions with `htmx.config.extensions`, list `plot`
(`"extensions":"sse,ptag,plot"`). The extension honours the list: when
`plot` is missing it does nothing at all, not even tooltips.

### htmx configuration

Put this in the `<head>`, before the htmx script:

```html
<meta name="htmx-config" content='{"noSwap":[204,304,"4xx","5xx"]}'>
```

`noSwap` stops htmx from swapping error pages that your handler never
wrote, such as a proxy's 502, a timeout's 503 or a login middleware's 401,
over your charts (see [Errors](#errors-your-handler-never-sees)). The array
replaces htmx's default rather than adding to it, so keep `204` and `304`.

The same tag can change the extension's defaults for the page, for example
`{"noSwap":[204,304,"4xx","5xx"],"plot":{"easing":"linear"}}`. Pages written
for hyperplot 0.1 also had `"morphIgnore":["data-htmx-powered","open"]`
to keep a reader's `<details>` open; the extension now does that itself for
every `<details>` that holds a chart's table, so drop it.

### Serving the stylesheet and scripts from Go

```go
import (
	"net/http"

	"github.com/ironystock/hyperplot/plot"
	"github.com/ironystock/hyperplot/client"
)

func routes(mux *http.ServeMux) {
	css := []byte(plot.Stylesheet())
	mux.HandleFunc("GET /static/hx-plot.css", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/css; charset=utf-8")
		w.Write(css)
	})
	// The embedded scripts, with Content-Type, ETag and Cache-Control.
	mux.Handle("GET /static/hx-plot.js", client.Handler())
	mux.Handle("GET /static/hx-plot-brush.js", client.BrushHandler())
	mux.Handle("GET /static/hx-plot-tween.js", client.TweenHandler())
}
```

`Stylesheet()` returns the default theme's CSS, the same bytes as the npm
package's `hx-plot.css`. Use `DefaultTheme()`, change fields, and call
`CSS()` on it to customize; or override the `--hp-*` custom properties in
your own stylesheet. A replacement stylesheet must keep
`.hp { touch-action: pan-y pinch-zoom }`, or touch scrubbing stops working
([spec §3.4](../spec/spec.md#34-presentation)).

### Dark mode

The stylesheet follows `prefers-color-scheme` automatically. To let users
choose, set `data-theme` on the root element:

```html
<html data-theme="dark">   <!-- or "light"; omit to follow the system -->
```

Switching the attribute recolors every chart instantly, with no request: the
SVG doesn't change, only the CSS custom properties do. The
[theming recipe](../examples/recipes/theming/) switches it with plain links
and adds a custom palette from `Theme.CSS()`.

## Reference: attributes, headers, parameters and events

### Attributes

| Attribute | Where | Purpose |
|---|---|---|
| `hx-get="/charts/x"` | wrapper | URL that returns the chart fragment |
| `hx-trigger="every 2s, htmx:plot:resize"` | wrapper | Poll, and re-render when the extension reports a resize |
| `hx-swap="innerMorph"` | wrapper | Morph the new SVG into the wrapper, preserving element identity |
| `hx-target="#charts"` | form, button | Where the response goes, if not the element itself |
| `hx-target:inherited="#charts"` | ancestor | Lets descendants inherit the target (htmx 4 inheritance is explicit) |
| `hx-ptag="…"` | wrapper | Seeds the content tag for the `hx-ptag` extension |
| `hx-sse:connect="/stream/x"` | wrapper | Opens an SSE stream; each unnamed message is swapped in |
| `hx-sync="closest .zoom:drop"` | wrapper | Puts polls on a shared request queue with a zoom (pattern 6) |
| `hx-plot:autosize` (or `="both"`) | wrapper | Observe the width (and height), fire `htmx:plot:resize`, send `hp-w` (and `hp-h`) on requests that target it |
| `hx-plot:brush` | ancestor of charts | Allow brushing time and linear x axes (needs `hx-plot-brush.js`) |
| `hx-config="plot.slide:false"` | wrapper | Per-request extension settings (see [Config](../client/README.md#config)) |

The `hx-plot:` attributes are container attributes: the extension finds the
nearest element carrying one with `closest()`. They also accept htmx's
other spellings: `data-hx-plot:autosize`, and `htmx.config.metaCharacter` in
place of `:`.

### Request headers sent by htmx

| Header | Value | Helper |
|---|---|---|
| `HX-Request` | `true` on every htmx request | `hx.IsRequest(r)` |
| `HX-Request-Type` | `full` or `partial` | `hx.IsPartial(r)` |
| `HX-Source` | the triggering element, as `tag#id` | |
| `HX-Target` | the target element, as `tag#id` | |
| `HX-PTag` | the last content tag (with the `hx-ptag` extension) | `hx.CheckPTag` |
| `Last-Event-ID` | the last SSE `id:` seen, on reconnect | `stream.LastEventID()` |

### Response headers

| Header | Purpose | Helper |
|---|---|---|
| `HX-PTag` | The content tag of this response; the client echoes it next time | `hx.CheckPTag` sets it |
| `HX-Push-Url` | The page URL for a zoomed view, so it can be bookmarked and Back undoes it | |
| `Content-Type: text/event-stream` | SSE responses | `hx.NewStream` sets it, with `Cache-Control: no-cache` and `X-Accel-Buffering: no` |

### Request parameters sent by the extension

| Parameter | Sent | Value | Helper |
|---|---|---|---|
| `hp-w` | on requests whose target is, or is inside, an `hx-plot:autosize` element | that element's content width (`clientWidth` less padding) in CSS pixels, rounded; omitted when 0 | `hx.Size(r, defW, defH)` |
| `hp-h` | the same, only for `hx-plot:autosize="both"` | its content height | `hx.Size(r, defW, defH)` |
| `hp-chart` | with a select or brush | the chart's id | `hx.Selected`, `hx.Brushed` |
| `hp-x` | with a select | the selected row's key: a time (`2026-03-03T14:10:00Z`), a number or a category | `hx.Selected` |
| `hp-series` | with a select, when the pointer was on a series | the series name | `hx.Selected` |
| `hp-x0`, `hp-x1` | with a brush | the range's ends, times or numbers, in either order | `hx.Brushed` |

`hx.Size` clamps the sizes to the range 50–4096 and falls back to the
defaults. Use `hx-plot:autosize="both"` (and so `hp-h`) only when the
wrapper's height comes from CSS: a wrapper without one takes its height
from the chart inside it (see [autosize](#height)). The extension never
overrides a parameter the request already has, so your own `hx-vals` or
URL values win.

### Events

| Event | Fired on | Use |
|---|---|---|
| `htmx:plot:resize` | an `hx-plot:autosize` element whose width changed by at least 8 px (debounced 150 ms); doesn't bubble | `hx-trigger="htmx:plot:resize"` |
| `htmx:plot:select` | the `svg.hp` (bubbles) | `hx-trigger="htmx:plot:select"` on an ancestor (pattern 5) |
| `htmx:plot:brush` | the `svg.hp` (bubbles) | `hx-trigger="htmx:plot:brush"` on an ancestor (pattern 6) |
| `htmx:after:swap` | the element that issued the request (bubbles); the target only if the swap removed that element | hook after any swap, for example on `document` |
| `htmx:after:settle` | the swap target (bubbles) | hook for your own code after a particular chart updates |
| `htmx:sse:after:message` | the SSE element | hook after each streamed update |

htmx 4 event names follow `htmx:phase:action`; the htmx 2 names (for
example `htmx:afterSwap`) don't fire. The extension sends its events
through htmx, so `metaCharacter` applies to their names and
`hx-on::plot:select="…"` works.

`htmx:after:swap` does not fire on the swapped element when the request
comes from somewhere else. With the [filter form](#pattern-3-filters)
(`hx-target="#charts"`), it fires on the `<form>` and bubbles up from there,
so a listener on `#charts` never runs. Listen for `htmx:after:settle` on the
target instead, or listen on `document` and check `evt.detail.ctx.target`.

## Pattern 1: polling with content tags

Polling is the simplest live pattern and works through any proxy. With the
`hx-ptag` extension, an unchanged chart costs one `304 Not Modified` with no
body, and htmx skips the swap entirely.

Runnable alone: the [polling-ptag recipe](../examples/recipes/polling-ptag/), in one
Go file.

```html
<div id="latency-wrap"
     hx-get="/charts/latency"
     hx-trigger="every 2s, htmx:plot:resize"
     hx-swap="innerMorph"
     hx-ptag="{{.Tag}}"
     hx-plot:autosize>
  {{.Chart}}
</div>
```

```go
func latencyChart(width, height float64) *plot.Chart {
	ts, p50, p99 := metrics.Latency() // your data
	return &plot.Chart{
		ID:      "latency", // stable: morphing matches on it
		Title:   "Request latency",
		Width:   width,
		Height:  height,
		Animate: true,
		Y:       plot.Axis{Label: "Latency", Unit: " ms"},
		Marks: []plot.Mark{
			plot.Line{Name: "p50", Slot: 1, X: plot.Times(ts), Y: p50},
			plot.Line{Name: "p99", Slot: 2, X: plot.Times(ts), Y: p99},
		},
	}
}

func latency(w http.ResponseWriter, r *http.Request) {
	width, _ := hx.Size(r, 640, 0) // the wrapper's width; the height stays fixed
	chart := latencyChart(width, 320)
	svg, err := chart.Render()
	if err != nil {
		log.Printf("latency chart: %v", err)
		w.WriteHeader(http.StatusNoContent) // keep showing the last good chart
		return
	}
	if hx.CheckPTag(w, r, hx.Tag(svg)) {
		return // the client already has this exact chart: 304, no swap
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Write(svg)
}
```

How it works:

- `hx.Tag(svg)` is a short, stable hash of the bytes, quoted like an ETag
  (for example `"hp-1a2b3c4d5e6f7a8b"`). Determinism makes it meaningful:
  identical data always gives an identical tag.
- `hx.CheckPTag` sets the `HX-PTag` response header. If the request's
  `HX-PTag` already equals the tag, it writes `304` and returns `true`.
  Call it **before** writing the body.
- On the full page render, compute the same tag and put it in `hx-ptag` so
  the very first poll can already be a `304`. It is one only if the page
  rendered the chart at the width the poll's `hp-w` reports, and only for a
  trigger other than `load`, which never sends the seed (see the pitfalls).
  Let `html/template` escape the attribute; the quotes in the tag round-trip
  correctly.
- The tag covers the whole SVG, including its size. After a resize the bytes
  differ, so the swap happens; that is what you want.
- Only the width comes from the client. The wrapper has no CSS height, so its
  `clientHeight` is the chart's own height plus a few pixels; feeding it back
  would grow the chart on every poll and no poll would ever be a `304`. See
  [autosize](#responsive-charts-with-autosize).
- `latencyChart` returns a `*plot.Chart`: `Render`, `HTML`, `Table` and
  the other `Chart` methods have pointer receivers, so
  `latencyChart(w, h).Render()` compiles only on a pointer.

Choose the interval to match how often the data changes. If rendering is
expensive, cache the rendered bytes and tag per data version and serve them to
every client: a `304` then costs a hash comparison.

## Pattern 2: server-sent events

Server-sent events push a new chart the moment data changes, over one
long-lived HTTP response. Each unnamed `data:` message is swapped into the
element with its `hx-swap` style, and the extension treats it like any
other swap.

Runnable alone: the [sse-stream recipe](../examples/recipes/sse-stream/), with
`retry:`, `Last-Event-ID` and the stream opened at the chart's real width.

```html
<div hx-sse:connect="/stream/throughput" hx-swap="innerMorph">
  {{.Chart}}
</div>
```

### A basic stream

```go
func throughput(w http.ResponseWriter, r *http.Request) {
	stream, err := hx.NewStream(w, r) // errors if w can't flush
	if err != nil {
		// Not an event stream: swap nothing, and make reconnects back off.
		log.Printf("throughput stream: %v", err)
		w.Header().Set("HX-Reswap", "none")
		w.WriteHeader(http.StatusServiceUnavailable)
		return
	}
	stream.Retry(3 * time.Second) // reconnect delay after a drop

	tick := time.NewTicker(time.Second)
	defer tick.Stop()
	keepAlive := time.NewTicker(15 * time.Second)
	defer keepAlive.Stop()

	for {
		select {
		case <-r.Context().Done(): // client went away
			return
		case <-keepAlive.C:
			if err := stream.Comment("ping"); err != nil {
				return
			}
		case <-tick.C:
			svg, err := throughputChart().Render()
			if err != nil {
				log.Printf("throughput chart: %v", err)
				continue // skip this frame; the client keeps the last chart
			}
			if err := stream.Send(svg); err != nil {
				return
			}
		}
	}
}
```

`Stream` handles the SSE framing: it splits the SVG into `data:` lines
(turning CRLF and lone CR line breaks into LF first), flushes after each
message, and is safe for concurrent use.
Event names and ids containing newlines are rejected with an error, so
untrusted values can never inject extra SSE fields. Ids must also be printable
ASCII without leading or trailing spaces, because the browser sends the last one
back as an HTTP header (see below); numbers and timestamps are always fine.

When `NewStream` fails, answer `503 Service Unavailable` with the header
`HX-Reswap: none`, as above. hx-sse takes over a response only when its
`Content-Type` is `text/event-stream`; anything else goes through the normal
htmx swap, so an `http.Error` body would replace the chart with the error
text. `HX-Reswap: none` stops that swap (htmx applies it before its status
code rules). A failed first connection is not retried: hx-sse reconnects only
streams that were once open.

The status matters when an open stream drops and the reconnect lands on the
failing handler. hx-sse waits the reconnect delay (500 ms, or `stream.Retry`,
give or take 30%) and doubles it, up to a minute, only while reconnects fail
with a network error or a non-2xx status. Any 2xx resets the wait to the base
delay, so a `204` (which also has no body to read, and logs an
`htmx:sse:error` on every attempt) keeps every open tab reconnecting about
twice a second. A `503` backs off. For the same reason, don't return from a
stream handler on a render error: skip the frame with `continue`. Returning
ends the stream, and the client reconnects after the base delay, again and
again while the error lasts.

### Replay with `Last-Event-ID`

When a connection drops, the browser reconnects after the `retry:` delay and
sends the last `id:` it received in the `Last-Event-ID` header. Because every
message is a **complete chart**, replay is simple: you never need to resend
history, only the latest state if the client's is stale.

```go
type feed struct {
	mu      sync.Mutex
	version uint64 // increments on every data change
	svg     []byte // latest rendered chart
	changed chan struct{} // closed and replaced on every change
}

func (f *feed) serve(w http.ResponseWriter, r *http.Request) {
	stream, err := hx.NewStream(w, r)
	if err != nil {
		log.Printf("feed: %v", err)
		w.Header().Set("HX-Reswap", "none") // keep the last chart
		w.WriteHeader(http.StatusServiceUnavailable) // and back off; see above
		return
	}
	last := stream.LastEventID() // "" on the first connection
	for {
		f.mu.Lock()
		id, svg, changed := strconv.FormatUint(f.version, 10), f.svg, f.changed
		f.mu.Unlock()

		if id != last { // new data, or a reconnect with a stale chart
			if err := stream.SendID(id, svg); err != nil {
				return
			}
			last = id
		}
		select {
		case <-r.Context().Done():
			return
		case <-changed:
		}
	}
}
```

Render once per data change in the producer, not once per client: every
subscriber receives the same bytes.

### Named events

`stream.Event(name, data)` sends a named event. htmx dispatches it as a DOM
event on the SSE element instead of swapping it, which is useful for
"something changed" notifications that trigger a normal request:

```go
stream.Event("errors-changed", nil)
```

```html
<div id="alerts" hx-sse:connect="/stream/alerts"></div>

<div hx-get="/charts/errors" hx-trigger="errors-changed from:#alerts"
     hx-swap="innerMorph">{{.Chart}}</div>
```

### Operational notes

- **Reverse proxies** may buffer responses. `NewStream` sets
  `X-Accel-Buffering: no` for nginx; configure others to disable buffering
  for `text/event-stream`.
- **Server timeouts.** Keep `ReadHeaderTimeout`, `ReadTimeout` and
  `WriteTimeout` set on your `http.Server`; streams survive all three. Each
  `hx.Stream` write moves the connection's write deadline 30 s ahead, which
  overrides `WriteTimeout`, so the stream outlives it however long it stays
  open, even through quiet spells (the deadline is renewed before each
  write), while a client that stops reading is still dropped. You don't need
  to drop `WriteTimeout` for SSE routes.
  Don't clear the deadline with `SetWriteDeadline(time.Time{})`: that removes
  the slow-reader protection. net/http clears the read deadline once it has
  read a request without a body, so `ReadTimeout` doesn't affect streams.
- **Connection limits.** Over HTTP/1.1, browsers allow about six connections
  per origin, shared by all tabs. Use HTTP/2 (multiplexed), or one stream per
  page that carries several charts with [`hx.Partial`](#pattern-4-multi-target-updates).
- **Keep-alives.** Send `stream.Comment` every 15–30 s so idle proxies and
  load balancers don't close the connection.
- **Sizing.** The connect request is an ordinary htmx request, so it carries
  `hp-w` (and `hp-h`, for `hx-plot:autosize="both"`) when the stream element
  is, or sits inside, an `hx-plot:autosize` element, and `hx.Size` works in
  the stream handler. The size is fixed for the life of the stream: to
  follow a resize, re-render the stream element from its container on
  `htmx:plot:resize` (see the gotchas below for how to swap it).
- **Changing the stream.** When filters, the size or a zoom change, replace
  the stream element rather than morphing a new `hx-sse:connect` value into
  it (see [htmx 4 gotchas](#htmx-4-gotchas)). The
  [spec's transports section](../spec/spec.md#45-transports) has a container that
  handles both a resize and a brush that way.

## Pattern 3: filters

Filters re-render every chart for a new slice of data. The server does all the
work; morphing animates the transition.

Runnable alone: the [filters-form recipe](../examples/recipes/filters-form/), which
also shows pattern 4 and `Slot`.

```html
<form hx-get="/dashboard" hx-trigger="change" hx-target="#charts" hx-swap="innerMorph">
  <label>Region
    <select name="region">
      <option value="">All</option>
      <option>eu-west</option>
      <option>us-east</option>
      <option>ap-south</option>
    </select>
  </label>
  <label>Window
    <select name="window"><option>1h</option><option>24h</option></select>
  </label>
</form>

<div id="charts">
  {{template "charts" .}}
</div>
```

```go
func dashboard(w http.ResponseWriter, r *http.Request) {
	data := load(r.FormValue("region"), r.FormValue("window"))
	h := w.Header()
	h.Set("Content-Type", "text/html; charset=utf-8")
	// One URL, two bodies: caches must keep the page and the fragment apart.
	h.Add("Vary", hx.HeaderRequest+", "+hx.HeaderRequestType)
	h.Set("Cache-Control", "no-cache")
	if hx.IsPartial(r) {
		tmpl.ExecuteTemplate(w, "charts", data) // just the contents of #charts
		return
	}
	tmpl.ExecuteTemplate(w, "page", data) // full page for direct visits
}
```

The handler answers the same URL with a whole page or a fragment, so it
sends `Vary: HX-Request, HX-Request-Type`. Without it, a shared cache (a CDN,
Varnish) or the browser's own cache may store the fragment from an htmx
request and serve it to the next direct visit of that URL: a page with no
`<head>`, stylesheet or scripts. `Cache-Control: no-cache` makes caches check
back with the server before reusing either response, which suits data that
changes.

### Keep colors stable with `Slot`

By default, series take color slots in mark order. If a filter hides the
first series, every remaining series would shift to a new color, which breaks
the reader's mental map. **Pin each entity to a slot**:

```go
var regionSlot = map[string]int{"eu-west": 1, "us-east": 2, "ap-south": 3}

for _, reg := range data.Regions { // only the regions the filter selected
	marks = append(marks, plot.Line{
		Name: reg.Name,
		Slot: regionSlot[reg.Name],
		X:    plot.Times(reg.Times),
		Y:    reg.Values,
	})
}
```

Now `us-east` is the same orange in every view. Series ids stay stable too,
because each is `{ID}-s-{key}` with the key taken from the series name, so
morphing animates the remaining lines instead of redrawing them. That needs
an explicit `Chart.ID`: the default id hashes the series names, so it changes
whenever the filter adds or removes a series.

Notes:

- The `hx-target` is on the form, which issues the request, so it applies
  directly. If you put `hx-target` on an ancestor so several controls share
  it, write `hx-target:inherited`; htmx 4 doesn't inherit attributes
  implicitly.
- Keep chart `ID`s the same across filter values so that the morph matches
  them.
- Add `hx-push-url="true"` to the form if filtered views should be linkable;
  the handler above already serves the full page for direct visits.

### Autosized charts in a filtered area

A filter request targets `#charts`, which is neither an
`hx-plot:autosize` element nor inside one, so the extension sends no `hp-w`
with it. One width couldn't describe several charts anyway. A handler that
renders every chart at its default width therefore shrinks or stretches
each [autosized](#responsive-charts-with-autosize) chart on every filter
change, and `htmx:plot:resize` doesn't fix it, because the container's size
hasn't changed. A polling chart snaps back at its next poll; any other chart
stays wrong until the window is resized.

Have each chart report its own width with the form. Give the form an id, and
render a hidden input associated with it (`form="filters"`) next to each
chart, holding the width the chart was rendered at:

```html
<form id="filters" hx-get="/dashboard" hx-trigger="change" hx-target="#charts" hx-swap="innerMorph">
  …
</form>

<div id="charts">
  <div hx-get="/charts/latency" hx-trigger="every 2s, htmx:plot:resize"
       hx-swap="innerMorph" hx-plot:autosize>
    {{.Latency}}<input type="hidden" form="filters" name="w-latency" value="{{.LatencyWidth}}">
  </div>
</div>
```

The form's request now carries `w-latency` and one such field for every other
chart. Render the input as part of the chart's fragment, in the chart's own
handler too, so its value always matches the chart on screen. The filter
handler reads each width back and clamps it like `hx.Size` does:

```go
// chartWidth returns the width chart id was last rendered at, or 640.
func chartWidth(r *http.Request, id string) float64 {
	v, err := strconv.ParseFloat(r.FormValue("w-"+id), 64)
	if err != nil || math.IsNaN(v) || v <= 0 {
		return 640
	}
	return min(max(v, hx.MinSize), hx.MaxSize)
}
```

and builds each chart with it, for example `latencyChart(chartWidth(r, "latency"), 320)`.

Alternatively, leave the charts out of the filter response and let each one
fetch itself: send an event with the `HX-Trigger` response header, add it to
each wrapper's trigger (`hx-trigger="every 2s, htmx:plot:resize, filters-applied from:body"`)
and pass the filters along with `hx-include="#filters"`. That costs one
request per chart instead of one in all.

## Pattern 4: multi-target updates

One response can update a chart and the KPI tiles around it. The main content
is swapped into the target as usual; each `<hx-partial>` element is then
swapped into its own target.

Runnable alone: the [filters-form recipe](../examples/recipes/filters-form/) updates a
summary line this way; the [brush-zoom recipe](../examples/recipes/brush-zoom/) its
zoom status and form.

```html
<div class="tiles">
  <div class="tile">p99 <strong id="kpi-p99">212 ms</strong> <span id="spark-p99">…</span></div>
</div>
<div hx-get="/refresh" hx-trigger="every 5s" hx-swap="innerMorph">{{.Chart}}</div>
```

```go
func refresh(w http.ResponseWriter, r *http.Request) {
	snap := metrics.Snapshot()
	chart, err := latencyChart(640, 320).Render()
	if err != nil {
		w.WriteHeader(http.StatusNoContent)
		return
	}
	spark, err := plot.Sparkline{Values: snap.P99, Label: "p99 latency"}.Render()
	if err != nil {
		w.WriteHeader(http.StatusNoContent)
		return
	}
	kpi := html.EscapeString(scale.FormatNumber(snap.P99Last, 0) + " ms")

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Write(chart)
	w.Write(hx.Partial("#kpi-p99", "innerHTML", []byte(kpi)))
	w.Write(hx.Partial("#spark-p99", "innerMorph", spark))
}
```

`hx.Partial` escapes the `hx-target` and `hx-swap` attribute values, but the
content is inserted as HTML: escape any text you put in it yourself, as
above. Rendered charts and sparklines are already safe.

Combine this with content tags by hashing the whole response body with
`hx.Tag`. Over SSE, send the concatenated bytes with `stream.Send` to update
several charts through one connection.

## Pattern 5: drill-down on select

A click on a point (or Enter on a keyboard point, or a second tap on touch)
dispatches `htmx:plot:select` on the chart. It bubbles, so an `hx-trigger`
on any ancestor turns it into a request, and the extension adds the
selection as parameters: `hp-chart`, `hp-x` (the row's key) and, when the
pointer was on a series, `hp-series`.

Runnable alone: the [select-drilldown recipe](../examples/recipes/select-drilldown/),
with a plain link per bar and a live region.

```html
<section hx-get="/detail" hx-trigger="htmx:plot:select" hx-target="#detail">
  <div hx-get="/charts/latency" hx-trigger="every 2s, htmx:plot:resize"
       hx-swap="innerMorph" hx-plot:autosize>{{.Chart}}</div>
</section>
<div id="detail" aria-live="polite"></div>
```

```go
func detail(w http.ResponseWriter, r *http.Request) {
	sel, ok := hx.Selected(r)
	at, err := sel.Time() // hp-x is a key the chart carries: an RFC 3339 time here
	if !ok || err != nil {
		w.WriteHeader(http.StatusNoContent) // no selection, or not one of our keys
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	fmt.Fprintf(w, "<p>Requests at %s", at.Format(time.Kitchen))
	if sel.Series != "" {
		fmt.Fprintf(w, " for %s", html.EscapeString(sel.Series)) // parameters are input
	}
	fmt.Fprint(w, "</p>")
}
```

`hp-x` is the key the server wrote into the chart for that x value: an RFC
3339 time in UTC on a time axis, a number on a linear axis
(`sel.Float()`), or the category on a band axis (`sel.X`). Match it
exactly. A missing or unknown value is no selection, so answer `204` and
leave the page as it is.

Treat `hp-series` as a hint on line charts: a 2 px line is hard to hit, and
Enter never names a series. The `#detail` element is a polite live region,
so screen readers hear the result; the extension can't know what the
request did.

Not every reader can click a point (a mobile screen reader can't). The
parameters are plain form fields, so offer the same request as a link per
table row, `<a href="/detail?hp-chart=latency&amp;hp-x=…">`, with the row's
key ([spec §5.5](../spec/spec.md#55-equivalents-in-plain-html)). `Chart.Table`
doesn't write these links yet.

## Pattern 6: brush to zoom

Runnable alone: the [brush-zoom recipe](../examples/recipes/brush-zoom/), with a
from/to form that zooms without a pointer.

With `hx-plot-brush.js` loaded, a drag across a chart inside an
`hx-plot:brush` element (or Shift+Arrow keys from a keyboard point, then
Enter) dispatches `htmx:plot:brush`, and the extension adds `hp-chart`,
`hp-x0` and `hp-x1`. Only time and linear x axes can be brushed, and touch
drags keep scrubbing the tooltip. Zooming a live chart takes three pieces:

```html
<div class="zoom" hx-get="/charts/latency" hx-trigger="htmx:plot:brush"
     hx-target="#latency" hx-swap="outerMorph" hx-sync="replace" hx-plot:brush>
  {{template "latency" .}}
</div>
<p id="status" aria-live="polite"></p>

{{define "latency"}}
<div id="latency" hx-get="{{.URL}}" hx-trigger="every 2s, htmx:plot:resize"
     hx-swap="innerMorph" hx-sync="closest .zoom:drop" hx-plot:autosize>{{.Chart}}</div>
{{end}}
```

1. **The answer replaces the polling element** (`outerMorph`, targeting
   `#latency`). A poll can't change its own `hx-get` with an inner swap, and
   a zoomed chart needs polls that keep the range, so the server sends the
   whole element with the range in its URL.
2. **One request queue.** `hx-sync="replace"` on the brush element and
   `hx-sync="closest .zoom:drop"` on the polling element share the `.zoom`
   element's queue: a brush aborts a poll in flight, and polls are dropped
   while the brush is pending. Without them a late poll can swap the
   unzoomed chart back.
3. **The zoom is a URL.** `HX-Push-Url` names the page URL for the range, so
   the zoomed view can be bookmarked and Back undoes it. htmx restores that
   history entry in the same document by fetching the page and replacing
   the children of `<body>`, which removes the focused chart. The extension
   focuses the new copy of a chart (found by id) only while its keyboard
   point is shown; so that a keyboard reader who pressed Escape first does
   not land on `<body>` either, note `document.activeElement.id` on
   `htmx:before:history:restore` and focus that id again after the swap of
   the request that carries `HX-History-Restore-Request` (the dashboard's
   `static/app.js` does).

The chart's own handler serves both the polls and the brush; `hx.Brushed`
tells them apart:

```go
func latencyZoom(w http.ResponseWriter, r *http.Request) {
	width, _ := hx.Size(r, 640, 0)
	chart := latencyChart(width, 320)
	b, brushed := hx.Brushed(r)
	if !brushed { // a poll; a zoomed element's URL carries its range as from and to
		b = hx.Brush{X0: r.FormValue("from"), X1: r.FormValue("to")}
	}
	t0, t1, err := b.TimesIn(time.UTC) // datetime-local values are read in this zone
	if err == nil {
		chart.X.TimeDomain = [2]time.Time{t0, t1}
	} else if brushed {
		w.WriteHeader(http.StatusNoContent) // a range that doesn't parse: ignore it
		return
	}
	svg, err := chart.HTML()
	if err != nil {
		w.WriteHeader(http.StatusNoContent)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if !brushed {
		w.Write([]byte(svg)) // innerMorph: just the chart
		return
	}
	q := url.Values{"from": {t0.Format(time.RFC3339Nano)}, "to": {t1.Format(time.RFC3339Nano)}}.Encode()
	w.Header().Set("HX-Push-Url", "/dashboard?"+q)
	tmpl.ExecuteTemplate(w, "latency", struct {
		URL   string
		Chart template.HTML
	}{"/charts/latency?" + q, svg})
	msg := "Showing " + t0.Format(time.Kitchen) + " to " + t1.Format(time.Kitchen)
	w.Write(hx.Partial("#status", "innerHTML", []byte(html.EscapeString(msg))))
}
```

`Brush.TimesIn` orders the ends and accepts any RFC 3339 time, plus the
offset-less `datetime-local` form, read in the zone you pass. That makes
the plain HTML equivalent work with the same handler: a from/to form for
readers who can't drag ([spec §5.5](../spec/spec.md#55-equivalents-in-plain-html)).

```html
<!-- inside the .zoom wrapper -->
<form action="/dashboard" hx-get="/charts/latency" hx-target="#latency" hx-swap="outerMorph"
      hx-sync="closest .zoom:replace">
  <input type="hidden" name="hp-chart" value="latency">
  <label>From <input type="datetime-local" name="hp-x0" step="1"></label>
  <label>To <input type="datetime-local" name="hp-x1" step="1"></label>
  <button>Zoom</button>
</form>
```

Put the form inside `.zoom` and give it `hx-sync="closest .zoom:replace"`,
like the brush: without a shared queue, a poll already in flight isn't
aborted, lands after the zoom and morphs the live chart back into the
zoomed element until the next poll. A "Back to live" link that targets
`#latency` needs the same attribute.

The page handler for `/dashboard` reads `from` and `to` (and, for the form
without script, `hp-x0` and `hp-x1`) the same way, so a pushed URL and a
bookmark show the same view. On a linear axis use `Brush.Floats` and
`Axis.Domain` instead. The [server guide](implementing-a-server.md#step-4-the-page-and-the-live-element)
builds the same page in Python.

## Pattern 7: sliding windows

Runnable: the [polling-ptag](../examples/recipes/polling-ptag/) and
[sse-stream](../examples/recipes/sse-stream/) recipes slide this way.

A live time series usually shows a moving window: the last hour, say. When
the window moves, the extension compares the old and new frames' x domains
(the `data-hp-x` attribute) and slides the content by the difference, the
technique Mike Bostock described for path transitions. Without it, a CSS
transition of the line's `d` moves every vertex towards its neighbour's old
value, and the line wobbles in place. This needs no code beyond
`Chart.Animate`, but three things make it look right:

- **Fix the window.** Set `X.TimeDomain` to the window, rather than letting
  the domain follow the data, so every frame's domain is exactly the
  window:

  ```go
  end := time.Now().Truncate(10 * time.Second) // the newest bucket
  chart := latencyChart(640, 320)
  chart.X.TimeDomain = [2]time.Time{end.Add(-time.Hour), end}
  ```

- **Include one point before the window.** The renderer draws a point
  outside the domain (clipped) but leaves it out of the tooltips and the
  table, so during a slide the line reaches the plot's left edge instead of
  leaving a gap.
- **Keep the plot the same width.** The slide maps between frames when tick
  labels change the margins a little, but a plot whose width changes by
  more than 10% snaps instead.

A frame whose window can't slide snaps to the new frame at once: a zoom, a
resize, a jump of more than a quarter of the window, or updates that arrive
less than 75 ms apart. A frame whose window didn't move (only values changed)
keeps the stylesheet's transitions. Under `prefers-reduced-motion` nothing
moves. To snap instead of sliding, set `hx-config="plot.slide:false"` on the
polling or stream element, or `"plot":{"slide":false}` in the page's
`htmx-config`; `plot.duration` and `plot.easing` tune the slide (`"linear"`
suits charts that update every second).

Safari doesn't transition a path's `d`, so value changes snap there; slides
use transforms and work everywhere. Load the optional `hx-plot-tween.js` to
animate value changes in Safari too.

## Responsive charts with autosize

Every chart has a `viewBox`, and the stylesheet sets
`.hp { max-width: 100%; height: auto }`, so charts always scale to fit. Scaling
also scales the text, though: a 640 px chart squeezed to 320 px has 6 px
labels. Autosize re-renders the chart for the space it actually has, so text
stays at its real size and tick counts adapt. The
[sizing recipe](../examples/recipes/sizing/) shows CSS scaling, autosize and
`hx-plot:autosize="both"` side by side.

```html
<div class="chart-box" hx-get="/charts/latency" hx-trigger="load, htmx:plot:resize"
     hx-swap="innerMorph" hx-plot:autosize>
</div>
```

```go
width, _ := hx.Size(r, 640, 0) // from hp-w, clamped to 50–4096
chart := latencyChart(width, 320)
```

How it works:

1. The extension observes every `hx-plot:autosize` element with a
   `ResizeObserver`.
2. When the width changes by at least 8 px (debounced 150 ms), it fires
   `htmx:plot:resize` on the element, which your `hx-trigger` turns into a
   request.
3. On every htmx request whose target is that element or inside it, the
   extension adds `hp-w`, the element's rounded content width
   (`clientWidth` less padding), to the request parameters. A request that
   targets something else, such as a drill-down into another element, gets
   no size.
4. `hx.Size` reads it, clamps it, and falls back to your default when the
   extension isn't loaded.

### Height

Keep the height on the server unless CSS sizes the wrapper. A wrapper with
no height of its own is exactly as tall as the chart inside it, plus a few
pixels of inline baseline gap: a 640×320 chart in an 800 px wide `div`
measures 324 px. A server that rendered at that height would make the next
poll measure 328, and so on until `hx.MaxSize`, and since every render
differs, no poll would ever be a `304`.

When CSS does give the wrapper its own height (a fixed `height`, or
`aspect-ratio` with `overflow: hidden`), opt in with
`hx-plot:autosize="both"`. The extension then also sends `hp-h` and fires
`htmx:plot:resize` when the height changes, and the handler can use both
values:

```css
.chart-box { height: 360px; overflow: hidden }
```

```html
<div class="chart-box" hx-get="/charts/latency" hx-trigger="load, htmx:plot:resize"
     hx-swap="innerMorph" hx-plot:autosize="both">
</div>
```

and in the handler, `width, height := hx.Size(r, 640, 320)`.

Tips:

- Combine triggers: `hx-trigger="every 2s, htmx:plot:resize"` polls and
  resizes.
- With content tags, a resize always swaps (the size is part of the SVG), and
  polls at a steady size still get `304`.

## Tooltips, keyboard reading and tables

The renderer embeds preformatted tooltip rows in a
`<metadata class="hp-tips">` element as JSON. The extension reads it on
hover. The browser never formats numbers: what the tooltip shows is exactly
what the server's formatter and `Axis.Unit` produced.

The [static-page recipe](../examples/recipes/static-page/) shows all of this on a page
without htmx, and the [gallery](../examples/gallery/) on every kind of chart.

- **Hover**: a crosshair (line and area charts), a band highlight (bar
  charts) or the nearest point within 12 px, marked by a ring (scatter
  plots), with a tooltip card listing every series at that x. On touch, a
  tap shows the tooltip until a tap outside the chart, and a sideways drag
  scrubs it along the chart.
- **Keyboard**: charts have `tabindex="0"`. With a chart focused, ArrowLeft
  and ArrowRight step through the data, Home and End jump to the ends,
  Enter selects (pattern 5), Shift+Arrow marks a range to brush (pattern 6),
  and Escape hides the tooltip. Each step is announced to screen readers
  through a visually hidden `role="status"` region (`div.hp-live`) that the
  extension adds.
- **Across updates**: after every swap, poll or SSE message, the extension
  re-applies the active hover or keyboard position to the new frame, so a
  2 s poll doesn't make the tooltip vanish. A keyboard reader stays on the
  same data point (matched by its row key) as a sliding window moves, and
  updates are not announced: only the reader's own steps are.
- **Without JavaScript**: each bar and dot has a native `<title>` tooltip, and
  `Chart.Table()` provides the data as a table:

  ```html
  <details><summary>Data table</summary>{{.Table}}</details>
  ```

Put the table in the same fragment as the chart, so every poll or SSE
message updates both. Its rows have ids like the chart's elements, so the
morph moves rows instead of rewriting them, and a screen reader keeps its
place. The extension keeps a `<details>` holding a table open (or closed)
as the reader left it, and a table the reader scrolled to its newest row
stays there as rows arrive. To let a response open or close it on purpose,
send it with `hx-config="plot.keepOpen:false"` on the requesting element.

## Errors

**In htmx 4, every response swaps except `204 No Content` and
`304 Not Modified`.** An `http.Error(w, "boom", 500)` in a polling handler
replaces your chart with the text "boom".

Choose deliberately:

- **Keep the last good chart** (best for polling and transient failures):
  log the error and respond `204`.
- **Show an error state** (best for user actions such as filters): render a
  small, intentional placeholder in the same wrapper, for example
  `<p class="chart-error" role="status">Couldn't load latency data.</p>`, with
  a normal `200`.
- **SSE**: skip the frame (`continue`) and keep the stream open. The client
  keeps the last chart. If the stream can't start at all (`hx.NewStream`
  fails), respond `503` with `HX-Reswap: none`: the response isn't an event
  stream, so hx-sse leaves it to htmx, which would otherwise swap an error
  body over the chart, and a non-2xx status makes reconnects back off (a
  `204` would not; see [A basic stream](#a-basic-stream)).
- **Gestures**: a select or brush whose parameters don't parse is no
  gesture. Answer `204`.

### Errors your handler never sees

Not every response comes from your handler. A reverse proxy answers
`502 Bad Gateway` or `504` while the backend restarts, `http.TimeoutHandler`
answers `503` with a small HTML page, authentication middleware answers `401`
or `403`, and `http.ServeMux` answers `404` or `405`. htmx swaps each of these
over the chart, and when the body is a whole HTML page it also copies the
page's `<title>` (say, "502 Bad Gateway") into the browser tab.

With `hx-ptag`, the damage outlasts the outage. The extension updates its
stored tag only from a response that carries an `HX-PTag` header, and an
error page doesn't. Once the backend is back, the next poll still sends the
tag of the last good chart, `hx.CheckPTag` answers `304`, and htmx keeps the
error page on screen until the data changes. A chart that doesn't poll keeps
it until its next request.

Guard against this on the client, where it happens. Tell htmx not to swap any
error status (see [htmx configuration](#htmx-configuration)):

```html
<meta name="htmx-config" content='{"noSwap":[204,304,"4xx","5xx"]}'>
```

htmx matches `"4xx"` and `"5xx"` against the response status, so every client
and server error leaves the chart as it was. To limit the guard to part of the
page, put `hx-status:5xx:inherited="swap:none"` (and the same for `4xx`) on a
common ancestor of the charts instead.

The guard doesn't cover middleware that redirects: a `302` to a login page is
followed by the browser, and the login page arrives as a `200`. Have such
middleware answer htmx requests (`hx.IsRequest(r)`) with `401` instead, or
send an `HX-Redirect` header to move the whole page.

### Render errors

`Chart.Render` returns errors for invalid input (mismatched lengths, mixed x
kinds, more than eight series, sizes below 50 px, and so on). Most of these
are programming errors; validate in tests so production renders don't fail.
`plot.ErrTooManySeries` signals that you should fold small series into
"Other" or use small multiples.

## Content Security Policy

Charts and the extension are designed to run under a strict CSP:

- The SVG contains **no `style=""` attributes and no event-handler
  attributes**. Colors come from classes and the external stylesheet.
- The tooltip payload is inert JSON inside `<metadata>`; it is parsed with
  `JSON.parse`, never evaluated.
- The extension inserts text with `textContent` only and doesn't use `eval`,
  `new Function` or inline scripts. It sets styles from JavaScript only
  through the CSSOM and the Web Animations API, which CSP's `style-src` does
  not restrict. Gestures need no `hx-vals="js:…"`: the extension adds their
  parameters itself.

A policy that works with self-hosted assets:

```
Content-Security-Policy: default-src 'self'; script-src 'self'; style-src 'self';
  img-src 'self' data:; connect-src 'self'; object-src 'none'; base-uri 'self'
```

If you load htmx from a CDN, add the pinned release's directory to
`script-src`, not the CDN's whole origin (for example
`https://cdn.jsdelivr.net/npm/htmx.org@4.0.2/dist/`). A public CDN serves any
package anyone publishes, so a bare origin such as `https://cdn.jsdelivr.net`
would let an injected `<script>` load arbitrary code. A source whose path ends
in `/` matches only URLs under that path. Add Subresource Integrity to the
script tags as well; it protects those tags but does not narrow the policy.
`connect-src 'self'` covers both htmx requests and SSE streams on the same
origin.

Things to watch:

- **`Chart.Standalone`** embeds a `<style>` element so the SVG is
  self-contained as a file. Inline `<style>` needs `style-src 'unsafe-inline'`
  or a hash, so don't embed standalone charts in pages; use the normal output
  and the stylesheet.
- Some htmx features evaluate expressions (for example inline event-handler
  attributes and trigger filters); check htmx's own security documentation
  if you use them. The patterns in this guide need none of them.

## htmx 4 gotchas

If you know htmx 2, these are the differences that affect charts:

| htmx 2 habit | htmx 4 behavior | What to do |
|---|---|---|
| `hx-ext="..."` on an element | No `hx-ext`; an extension is active once its script is loaded | Load `hx-ptag`, `hx-sse` and `hx-plot.js` with script tags |
| Attributes inherit from ancestors | Inheritance is explicit | Write `hx-target:inherited="#charts"` on the ancestor |
| Error responses (4xx/5xx) don't swap by default | All responses swap except 204 and 304 | Return 204 to keep the chart, or render an error state on purpose; set `noSwap` to `[204,304,"4xx","5xx"]` so proxy and middleware errors don't swap either |
| Morphing needs the idiomorph extension | `innerMorph` and `outerMorph` are core swap styles, with htmx's own morph | Use `hx-swap="innerMorph"`; no extra script |
| Events like `htmx:afterSwap` | Events are `htmx:phase:action`, like `htmx:after:swap`; extensions' events add their name, like `htmx:plot:select` | Update listeners; the htmx 2 names don't fire |
| `XMLHttpRequest` | Requests use `fetch()` | Nothing, unless you hooked XHR events |
| `hx-ext="sse" sse-connect="/url" sse-swap="message"` | `hx-sse:connect="/url"`; unnamed messages swap into the element | Use the new attribute form with `hx-swap="innerMorph"` |

Two more chart-specific points:

- **Use `innerMorph` on a wrapper, not `outerMorph` on the SVG.** The wrapper
  carries the `hx-*` attributes; `innerMorph` updates the SVG inside it and
  leaves the wrapper (and its polling trigger) alone. The one exception is a
  response that must change the wrapper's own attributes, such as a zoom that
  changes its `hx-get` ([pattern 6](#pattern-6-brush-to-zoom)): that replaces
  the wrapper with `outerMorph`.
- **Keep ids stable.** Generated ids, or a `Chart.ID` that changes per
  request, defeat morphing: everything gets replaced and nothing animates.
  When `ID` is empty it is a hash of the title, the description, both axis
  labels and every series' kind and name. Changing any of them changes the
  id, and so does a filter that adds or removes a series, which replaces the
  chart instead of morphing it. Set `Chart.ID` on every chart you update.

Pitfalls in htmx 4.0.2 and its extensions, each reproduced in Chrome while
building the [example dashboard](../examples/dashboard/):

- **Don't morph a new `hx-sse:connect` URL into a live element.** The morph
  cancels the stream's reader, but hx-sse's read loop keeps reconnecting to
  the old URL for as long as the element stays in the page, so two streams end
  up swapping into one element. Swap the stream element out with
  `hx-swap="innerHTML"` on its parent, so the old element leaves the document.
- **Process a new stream element only once.** hx-sse marks an element as
  connected only when the response arrives. A morph that inserts a new stream
  element processes it twice (once while inserting, once after the swap), and
  each pass opens a stream. If a filter response re-renders the area around a
  stream, put `hx-morph-skip-children` on the stream's container and have the
  container fetch its new stream itself, triggered by an event the filter
  response sends with `HX-Trigger` (htmx fires it after the swap):
  `hx-trigger="load, htmx:plot:resize, filters-applied from:body"`.
  Keep `load` there: the page can't know the container's width, and
  `htmx:plot:resize` fires only when the width *changes*, so without it a
  stream rendered at a default width stays scaled down for the life of the
  page. The example dashboard renders the chart in the page without
  `hx-sse:connect`, and `load` fetches the stream element at the real
  width, so only one stream ever opens.
- **`load` doesn't send a seeded `hx-ptag`.** `load` fires while htmx
  initializes the element, before the ptag extension reads the attribute.
  Use `hx-trigger="load delay:1ms, every 2s"` when the first request should
  carry the seed.
- **Don't `defer` htmx and its extensions.** A deferred htmx 4.0.2 starts on
  a timer that can fire before a deferred extension script has loaded; the
  extension then misses htmx's first pass, and an `hx-sse:connect` element
  never connects.
- **Morphs remove attributes the server didn't send**, such as the `open` of
  a `<details>` the reader expanded, or a class a script added. The
  extension handles `open` for `<details>` that hold a chart's table. For
  others, list the attribute in `morphIgnore` (keeping the default
  `data-htmx-powered`), and don't keep script state in server-rendered
  attributes.

## Testing handlers

Handlers are plain `net/http`, so `httptest` covers them. Set the htmx headers
and parameters to exercise each branch:

```go
func TestLatencyNotModified(t *testing.T) {
	first := httptest.NewRecorder()
	latency(first, httptest.NewRequest("GET", "/charts/latency", nil))
	tag := first.Header().Get("HX-PTag")
	if tag == "" {
		t.Fatal("no HX-PTag header")
	}

	req := httptest.NewRequest("GET", "/charts/latency", nil)
	req.Header.Set("HX-Request", "true")
	req.Header.Set("HX-PTag", tag)
	second := httptest.NewRecorder()
	latency(second, req)
	if second.Code != http.StatusNotModified {
		t.Fatalf("status = %d, want 304", second.Code)
	}
}

func TestDetailIgnoresUnknownKeys(t *testing.T) {
	rec := httptest.NewRecorder()
	detail(rec, httptest.NewRequest("GET", "/detail?hp-chart=latency&hp-x=not-a-time", nil))
	if rec.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want 204", rec.Code)
	}
}
```

Make chart data injectable (a function or interface) so tests render fixed
data; determinism then lets you compare whole responses.

## Production checklist

- [ ] Every updated chart has a stable `Chart.ID`, and `Animate` is set where
      motion helps.
- [ ] Updates swap with `hx-swap="innerMorph"` into a wrapper element.
- [ ] Series that filters can hide have pinned `Slot`s.
- [ ] Polling handlers call `hx.CheckPTag` before writing, and the page seeds
      `hx-ptag`.
- [ ] Error paths return `204` (or a deliberate error state), never a raw
      error body. SSE handlers that can't stream answer `503` with
      `HX-Reswap: none`, and skip frames that fail to render.
- [ ] The page's `htmx-config` sets `"noSwap":[204,304,"4xx","5xx"]`, so error
      pages from proxies, timeouts and middleware never replace a chart (and
      `hx-ptag` never pins one in place). If it restricts `extensions`, it
      lists `plot`.
- [ ] A URL that serves a full page or a fragment, depending on
      `hx.IsPartial`, sends `Vary: HX-Request, HX-Request-Type`.
- [ ] SSE handlers stop on `r.Context().Done()`, send keep-alive comments, and
      aren't held back by proxy buffering.
- [ ] The `http.Server` sets `ReadHeaderTimeout`, `ReadTimeout` and
      `WriteTimeout` (`hx.Stream` keeps streams alive past `WriteTimeout`).
- [ ] htmx and the extensions load without `defer`; the stylesheet and
      scripts are served from your origin with caching, and the CSP allows
      them.
- [ ] Sliding charts fix `X.TimeDomain` to the window and include one point
      before it.
- [ ] Gesture handlers treat `hp-*` values as input: unparsable values are
      ignored, echoed values are escaped. Zooms send `HX-Push-Url`.
- [ ] Everything that replaces a polling element (the brush, the zoom form,
      "Back to live") shares its `hx-sync` queue, as in
      [pattern 6](#pattern-6-brush-to-zoom).
- [ ] A stream element whose URL changes (resize, zoom, filters) is
      replaced with `innerHTML` or `outerHTML`, never morphed.
- [ ] Each chart has a meaningful `Title`, and a `Chart.Table()` is reachable
      for screen-reader and keyboard users; pages with gestures offer the
      plain HTML equivalents and a polite live region.

See the [example dashboard](../examples/dashboard/) for all of these
patterns in one runnable Go program, the [other examples](../examples/README.md)
for servers in Python and Node, more demos and one-pattern recipes, and the
[server guide](implementing-a-server.md) for a smaller page in Python.
