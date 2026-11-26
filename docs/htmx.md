# Live charts with htmx 4

This guide shows how to keep hyperplot charts live with
[htmx 4](https://four.htmx.org): polling with content tags, server-sent
events, filters, multi-target updates and responsive sizing. It assumes you
have rendered a static chart already (see the [README](../README.md#quick-start)).

The facts about htmx here are for **htmx 4.0.2**. htmx 4 differs from htmx 2
in ways that matter for charts; see [htmx 4 gotchas](#htmx-4-gotchas) before
porting htmx 2 code.

> hyperplot is pre-1.0. The `hx` and `client` packages described here follow
> the API in [DESIGN.md](DESIGN.md); details may change before v1.0.0.

**Contents**

- [How live updates work](#how-live-updates-work)
- [Setup](#setup)
- [Reference: attributes, headers and events](#reference-attributes-headers-and-events)
- [Pattern 1: polling with content tags](#pattern-1-polling-with-content-tags)
- [Pattern 2: server-sent events](#pattern-2-server-sent-events)
- [Pattern 3: filters](#pattern-3-filters)
- [Pattern 4: multi-target updates](#pattern-4-multi-target-updates)
- [Responsive charts with autosize](#responsive-charts-with-autosize)
- [Tooltips and keyboard reading](#tooltips-and-keyboard-reading)
- [Errors](#errors)
- [Content Security Policy](#content-security-policy)
- [htmx 4 gotchas](#htmx-4-gotchas)
- [Testing handlers](#testing-handlers)
- [Production checklist](#production-checklist)

## How live updates work

1. The server renders a chart to SVG. The output is **deterministic**: the
   same data gives byte-identical SVG.
2. Every element that can change has a **stable id**: the chart has
   `Chart.ID`, each series group is `{ID}-s-{key}`, and each bar is
   `{ID}-b-{key}-{catkey}`, where the keys are hashes of the series name and
   category.
3. htmx swaps the new SVG in with **`innerMorph`**. Morphing (idiomorph, part
   of htmx 4 core) matches elements by id and updates their attributes in
   place instead of replacing them.
4. Because the elements survive, **CSS transitions** animate the change:
   with `Chart.Animate`, the stylesheet transitions bar and area paths (`d`),
   dot positions and opacity. Users who prefer reduced motion get no
   animation.
5. The optional **client extension** re-applies the hover tooltip or keyboard
   focus after each swap, so updates don't make the tooltip flicker.

Two things follow from this: always set a stable `Chart.ID` for a chart you
update, and swap with `innerMorph` into a wrapper element.

## Setup

### Scripts and stylesheet

```html
<link rel="stylesheet" href="/hyperplot.css">

<script src="https://cdn.jsdelivr.net/npm/htmx.org@4.0.2/dist/htmx.min.js"></script>
<!-- Only the extensions you use: -->
<script src="https://cdn.jsdelivr.net/npm/htmx.org@4.0.2/dist/ext/hx-ptag.js"></script>
<script src="https://cdn.jsdelivr.net/npm/htmx.org@4.0.2/dist/ext/hx-sse.min.js"></script>
<!-- Optional: tooltips, keyboard reading, autosize. Load after htmx. -->
<script src="/hyperplot.js"></script>
```

htmx 4 extensions are activated by loading their script. There is no
`hx-ext` attribute. Pin exact versions in production, or self-host the files
(see [Content Security Policy](#content-security-policy)). If you restrict
extensions with `htmx.config.extensions`, include `hyperplot`: without it the
client still shows tooltips, but doesn't re-apply them after swaps or send
`hp-w` and `hp-h`.

### htmx configuration

Put this in the `<head>`, before the htmx script:

```html
<meta name="htmx-config"
      content='{"noSwap":[204,304,"4xx","5xx"],"morphIgnore":["data-htmx-powered","open"]}'>
```

- `noSwap` stops htmx from swapping error pages that your handler never
  wrote, such as a proxy's 502, a timeout's 503 or a login middleware's 401,
  over your charts (see [Errors](#errors-your-handler-never-sees)).
- `morphIgnore` keeps a `<details>` the reader opened open across morphs (see
  [htmx 4 gotchas](#htmx-4-gotchas)).

Each array replaces htmx's default rather than adding to it, so keep `204`,
`304` and `data-htmx-powered`.

### Serving the stylesheet and client from Go

```go
import (
	"net/http"

	"github.com/ironystock/hyperplot"
	"github.com/ironystock/hyperplot/client"
)

func routes(mux *http.ServeMux) {
	css := []byte(hyperplot.Stylesheet())
	mux.HandleFunc("GET /hyperplot.css", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/css; charset=utf-8")
		w.Write(css)
	})
	// Embedded hyperplot.js with Content-Type, ETag and Cache-Control.
	mux.Handle("GET /hyperplot.js", client.Handler())
}
```

`Stylesheet()` returns the default theme's CSS. Use `DefaultTheme()`, change
fields, and call `CSS()` on it to customize; or override the `--hp-*` custom
properties in your own stylesheet.

### Dark mode

The stylesheet follows `prefers-color-scheme` automatically. To let users
choose, set `data-theme` on the root element:

```html
<html data-theme="dark">   <!-- or "light"; omit to follow the system -->
```

Switching the attribute recolors every chart instantly, with no request: the
SVG doesn't change, only the CSS custom properties do.

## Reference: attributes, headers and events

### Attributes

| Attribute | Where | Purpose |
|---|---|---|
| `hx-get="/charts/x"` | wrapper | URL that returns the chart fragment |
| `hx-trigger="every 2s, hp:resize"` | wrapper | Poll, and re-render when the client reports a resize |
| `hx-swap="innerMorph"` | wrapper | Morph the new SVG into the wrapper, preserving element identity |
| `hx-target="#charts"` | form, button | Where the response goes, if not the element itself |
| `hx-target:inherited="#charts"` | ancestor | Lets descendants inherit the target (htmx 4 inheritance is explicit) |
| `hx-ptag="…"` | wrapper | Seeds the content tag for the `hx-ptag` extension |
| `hx-sse:connect="/stream/x"` | wrapper | Opens an SSE stream; each unnamed message is swapped in |
| `data-hp-autosize` (or `="both"`) | wrapper | Client extension: observe the width (and height), fire `hp:resize`, send `hp-w` (and `hp-h`) |

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
| `Content-Type: text/event-stream` | SSE responses | `hx.NewStream` sets it, with `Cache-Control: no-cache` and `X-Accel-Buffering: no` |

### Request parameters sent by the client extension

| Parameter | Value | Helper |
|---|---|---|
| `hp-w` | wrapper content width (`clientWidth` less padding) in CSS pixels, rounded; omitted when 0 | `hx.Size(r, defW, defH)` |
| `hp-h` | wrapper content height, sent only for `data-hp-autosize="both"`; omitted when 0 | `hx.Size(r, defW, defH)` |

`hx.Size` clamps both to the range 50–4096 and falls back to the defaults.
Use `data-hp-autosize="both"` (and so `hp-h`) only when the wrapper's height
comes from CSS: a wrapper without one takes its height from the chart inside
it (see [autosize](#height)).

### Events

| Event | Fired on | Use |
|---|---|---|
| `hp:resize` | a `[data-hp-autosize]` element whose width changed by at least 8 px (debounced 150 ms) | `hx-trigger="hp:resize"` |
| `htmx:after:swap` | the element that issued the request (bubbles); the target only if the swap removed that element | hook after any swap, for example on `document` |
| `htmx:after:settle` | the swap target (bubbles) | hook for your own code after a particular chart updates |
| `htmx:sse:after:message` | the SSE element | hook after each streamed update |

htmx 4 event names follow `htmx:phase:action`. The htmx 2 names (for example
`htmx:afterSwap`) don't fire.

`htmx:after:swap` does not fire on the swapped element when the request
comes from somewhere else. With the [filter form](#pattern-3-filters)
(`hx-target="#charts"`), it fires on the `<form>` and bubbles up from there,
so a listener on `#charts` never runs. Listen for `htmx:after:settle` on the
target instead, or listen on `document` and check `evt.detail.ctx.target`.

## Pattern 1: polling with content tags

Polling is the simplest live pattern and works through any proxy. With the
`hx-ptag` extension, an unchanged chart costs one `304 Not Modified` with no
body, and htmx skips the swap entirely.

```html
<div id="latency-wrap"
     hx-get="/charts/latency"
     hx-trigger="every 2s, hp:resize"
     hx-swap="innerMorph"
     hx-ptag="{{.Tag}}"
     data-hp-autosize>
  {{.Chart}}
</div>
```

```go
func latencyChart(width, height float64) *hyperplot.Chart {
	ts, p50, p99 := metrics.Latency() // your data
	return &hyperplot.Chart{
		ID:      "latency", // stable: morphing matches on it
		Title:   "Request latency",
		Width:   width,
		Height:  height,
		Animate: true,
		Y:       hyperplot.Axis{Label: "Latency", Unit: " ms"},
		Marks: []hyperplot.Mark{
			hyperplot.Line{Name: "p50", Slot: 1, X: hyperplot.Times(ts), Y: p50},
			hyperplot.Line{Name: "p99", Slot: 2, X: hyperplot.Times(ts), Y: p99},
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
  the very first poll can already be a `304`. Let `html/template` escape the
  attribute; the quotes in the tag round-trip correctly.
- The tag covers the whole SVG, including its size. After a resize the bytes
  differ, so the swap happens; that is what you want.
- Only the width comes from the client. The wrapper has no CSS height, so its
  `clientHeight` is the chart's own height plus a few pixels; feeding it back
  would grow the chart on every poll and no poll would ever be a `304`. See
  [autosize](#responsive-charts-with-autosize).
- `latencyChart` returns a `*hyperplot.Chart`: `Render`, `HTML`, `Table` and
  the other `Chart` methods have pointer receivers, so
  `latencyChart(w, h).Render()` compiles only on a pointer.

Choose the interval to match how often the data changes. If rendering is
expensive, cache the rendered bytes and tag per data version and serve them to
every client: a `304` then costs a hash comparison.

## Pattern 2: server-sent events

Server-sent events push a new chart the moment data changes, over one
long-lived HTTP response. Each unnamed `data:` message is swapped into the
element with its `hx-swap` style.

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
  `hp-w` (and `hp-h`, for `data-hp-autosize="both"`) when the element sits
  inside a `data-hp-autosize` container, and `hx.Size` works in the stream
  handler. The size is fixed for the life of
  the stream: to follow a resize, re-render the stream element from its
  container on `hp:resize` (see the gotchas below for how to swap it).
- **Changing the stream.** When filters or the size change, replace the
  stream element rather than morphing a new `hx-sse:connect` value into it
  (see [htmx 4 gotchas](#htmx-4-gotchas)).

## Pattern 3: filters

Filters re-render every chart for a new slice of data. The server does all the
work; morphing animates the transition.

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
	marks = append(marks, hyperplot.Line{
		Name: reg.Name,
		Slot: regionSlot[reg.Name],
		X:    hyperplot.Times(reg.Times),
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

A filter request comes from the form and targets `#charts`, and neither sits
inside a `data-hp-autosize` element, so the client sends no `hp-w` with it.
One width couldn't describe several charts anyway. A handler that renders
every chart at its default width therefore shrinks or stretches each
[autosized](#responsive-charts-with-autosize) chart on every filter change,
and `hp:resize` doesn't fix it, because the container's size hasn't changed.
A polling chart snaps back at its next poll; any other chart stays wrong
until the window is resized.

Have each chart report its own width with the form. Give the form an id, and
render a hidden input associated with it (`form="filters"`) next to each
chart, holding the width the chart was rendered at:

```html
<form id="filters" hx-get="/dashboard" hx-trigger="change" hx-target="#charts" hx-swap="innerMorph">
  …
</form>

<div id="charts">
  <div hx-get="/charts/latency" hx-trigger="every 2s, hp:resize"
       hx-swap="innerMorph" data-hp-autosize>
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
each wrapper's trigger (`hx-trigger="every 2s, hp:resize, filters-applied from:body"`)
and pass the filters along with `hx-include="#filters"`. That costs one
request per chart instead of one in all.

## Pattern 4: multi-target updates

One response can update a chart and the KPI tiles around it. The main content
is swapped into the target as usual; each `<hx-partial>` element is then
swapped into its own target.

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
	spark, err := hyperplot.Sparkline{Values: snap.P99, Label: "p99 latency"}.Render()
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

## Responsive charts with autosize

Every chart has a `viewBox`, and the stylesheet sets
`.hp { max-width: 100%; height: auto }`, so charts always scale to fit. Scaling
also scales the text, though: a 640 px chart squeezed to 320 px has 6 px
labels. Autosize re-renders the chart for the space it actually has, so text
stays at its real size and tick counts adapt.

```html
<div class="chart-box" hx-get="/charts/latency" hx-trigger="load, hp:resize"
     hx-swap="innerMorph" data-hp-autosize>
</div>
```

```go
width, _ := hx.Size(r, 640, 0) // from hp-w, clamped to 50–4096
chart := latencyChart(width, 320)
```

How it works:

1. The client observes every `[data-hp-autosize]` element with a
   `ResizeObserver`.
2. When the width changes by at least 8 px (debounced 150 ms), it fires
   `hp:resize` on the element, which your `hx-trigger` turns into a request.
3. On every htmx request from that element (or from inside it), the client
   adds `hp-w`, the element's rounded content width (`clientWidth` less
   padding), to the request parameters.
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
`data-hp-autosize="both"`. The client then also sends `hp-h` and fires
`hp:resize` when the height changes, and the handler can use both values:

```css
.chart-box { height: 360px; overflow: hidden }
```

```html
<div class="chart-box" hx-get="/charts/latency" hx-trigger="load, hp:resize"
     hx-swap="innerMorph" data-hp-autosize="both">
</div>
```

and in the handler, `width, height := hx.Size(r, 640, 320)`.

Tips:

- Combine triggers: `hx-trigger="every 2s, hp:resize"` polls and resizes.
- With content tags, a resize always swaps (the size is part of the SVG), and
  polls at a steady size still get `304`.

## Tooltips and keyboard reading

The renderer embeds preformatted tooltip rows in a
`<metadata class="hp-tips">` element as JSON. The client extension reads it
on hover. The browser never formats numbers: what the tooltip shows is
exactly what the server's formatter and `Axis.Unit` produced.

- **Hover**: a crosshair (line and area charts), a band highlight (bar
  charts) or the nearest point within 12 px, marked by a ring (scatter
  plots), with a tooltip card listing every series at that x. On touch, a
  tap shows the tooltip until a tap outside the chart.
- **Keyboard**: charts have `tabindex="0"`. With a chart focused, ArrowLeft
  and ArrowRight step through the data, Home and End jump to the ends, and
  Escape hides the tooltip. Each step is announced to screen readers through
  a visually hidden `role="status"` region (`div.hp-live`) that the client
  adds at page load.
- **Across updates**: after `htmx:after:swap` and after each SSE message, the
  client re-applies the active hover or focus position to the morphed chart,
  so a 2 s poll doesn't make the tooltip vanish. A keyboard reader stays on
  the same point (matched by its heading) as a sliding window moves, and
  updates are not announced: only the reader's own steps are.
- **Without JavaScript**: each bar and dot has a native `<title>` tooltip, and
  `Chart.Table()` provides the data as a table:

  ```html
  <details><summary>Data table</summary>{{.Table}}</details>
  ```

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
`hyperplot.ErrTooManySeries` signals that you should fold small series into
"Other" or use small multiples.

## Content Security Policy

hyperplot is designed to run under a strict CSP:

- The SVG contains **no `style=""` attributes and no event-handler
  attributes**. Colors come from classes and the external stylesheet.
- The tooltip payload is inert JSON inside `<metadata>`; it is parsed with
  `JSON.parse`, never evaluated.
- The client extension inserts text with `textContent` only and doesn't use
  `eval`, `new Function` or inline scripts. It positions the tooltip by
  setting element style properties from JavaScript (the CSSOM), which CSP's
  `style-src` does not restrict.

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
  if you use them. hyperplot's patterns in this guide need none of them.

## htmx 4 gotchas

If you know htmx 2, these are the differences that affect charts:

| htmx 2 habit | htmx 4 behavior | What to do |
|---|---|---|
| `hx-ext="..."` on an element | No `hx-ext`; an extension is active once its script is loaded | Load `hx-ptag`, `hx-sse` and `hyperplot.js` with script tags |
| Attributes inherit from ancestors | Inheritance is explicit | Write `hx-target:inherited="#charts"` on the ancestor |
| Error responses (4xx/5xx) don't swap by default | All responses swap except 204 and 304 | Return 204 to keep the chart, or render an error state on purpose; set `noSwap` to `[204,304,"4xx","5xx"]` so proxy and middleware errors don't swap either |
| Morphing needs the idiomorph extension | `innerMorph` and `outerMorph` are core swap styles | Use `hx-swap="innerMorph"`; no extra script |
| Events like `htmx:afterSwap` | Events are `htmx:phase:action`, like `htmx:after:swap` | Update listeners; the htmx 2 names don't fire |
| `XMLHttpRequest` | Requests use `fetch()` | Nothing, unless you hooked XHR events |
| `hx-ext="sse" sse-connect="/url" sse-swap="message"` | `hx-sse:connect="/url"`; unnamed messages swap into the element | Use the new attribute form with `hx-swap="innerMorph"` |

Two more chart-specific points:

- **Use `innerMorph` on a wrapper, not `outerMorph` on the SVG.** The wrapper
  carries the `hx-*` attributes; `innerMorph` updates the SVG inside it and
  leaves the wrapper (and its polling trigger) alone.
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
  `hx-trigger="hp:resize, filters-applied from:body"`.
- **`load` doesn't send a seeded `hx-ptag`.** `load` fires while htmx
  initializes the element, before the ptag extension reads the attribute.
  Use `hx-trigger="load delay:1ms, every 2s"` when the first request should
  carry the seed.
- **Morphs close an open `<details>`.** The server's markup has no `open`
  attribute, so the morph removes the one the reader's click added. Tell
  htmx to leave it alone with `"morphIgnore":["data-htmx-powered","open"]`
  in the [htmx configuration](#htmx-configuration); the array replaces the
  default, so keep its `data-htmx-powered` entry.

## Testing handlers

Handlers are plain `net/http`, so `httptest` covers them. Set the htmx headers
to exercise each branch:

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
      `hx-ptag` never pins one in place).
- [ ] A URL that serves a full page or a fragment, depending on
      `hx.IsPartial`, sends `Vary: HX-Request, HX-Request-Type`.
- [ ] SSE handlers stop on `r.Context().Done()`, send keep-alive comments, and
      aren't held back by proxy buffering.
- [ ] The `http.Server` sets `ReadHeaderTimeout`, `ReadTimeout` and
      `WriteTimeout` (`hx.Stream` keeps streams alive past `WriteTimeout`).
- [ ] The stylesheet and client are served from your origin with caching, and
      the CSP allows them.
- [ ] Each chart has a meaningful `Title`, and a `Chart.Table()` is reachable
      for screen-reader and keyboard users.

See the [example dashboard](../examples/dashboard/) for all of these patterns
in one runnable program.
