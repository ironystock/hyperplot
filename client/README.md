# htmx-ext-plot

`hx-plot` is an [htmx 4](https://four.htmx.org) extension for charts that
the **server** draws as SVG. There is no client chart library: your server
renders each chart as plain markup, htmx keeps it live with ordinary
requests, polling or server-sent events, and the extension makes it feel
native. It adds tooltips and keyboard reading, keeps hover and focus across
`innerMorph` swaps, slides moving time windows instead of redrawing them,
keeps live data tables at their newest row, and turns clicks and drags on a
chart into htmx requests (`hp-x`, `hp-x0`, `hp-x1`) so that drill-down and
zoom are hypermedia exchanges.

Any server can produce the charts: the markup, the parameters and the events
are a short wire specification, [spec.md](../docs/spec.md), with
conformance fixtures in [`testdata/conformance`](../testdata/conformance/).
The Go module [hyperplot](https://github.com/ironystock/hyperplot) is the
reference server; the Python sample below shows the parts a server in
another language writes.

## Installing

Load the extension after htmx. It registers itself as `plot`.

```html
<script src="https://cdn.jsdelivr.net/npm/htmx.org@4.0.2/dist/htmx.min.js"></script>
<script src="https://cdn.jsdelivr.net/npm/htmx-ext-plot@0.2.0/hx-plot.js"></script>
<link rel="stylesheet" href="https://cdn.jsdelivr.net/npm/htmx-ext-plot@0.2.0/hx-plot.css">
```

The package is not on npm yet; until it is, copy `hx-plot.js` (and the
stylesheet, `hyperplot.Stylesheet()` in Go) into your static files. Go
programs serve the copies embedded in the `client` package:

```go
mux.Handle("GET /static/hx-plot.js", client.Handler())
mux.Handle("GET /static/hx-plot-brush.js", client.BrushHandler()) // optional
mux.Handle("GET /static/hx-plot-tween.js", client.TweenHandler()) // optional
```

Two optional companions load after `hx-plot.js` with their own script tags:
`hx-plot-brush.js` adds brushing (`hx-plot:brush`), and `hx-plot-tween.js`
animates value changes in Safari, whose CSS cannot transition a path's `d`.

If you restrict extensions with `htmx.config.extensions`, list `plot`:
`<meta name="htmx-config" content='{"noSwap":[204,304,"4xx","5xx"],"extensions":"sse,ptag,plot"}'>`.
Load htmx and its extensions without `defer` (a deferred htmx 4.0.2 can start
before a deferred extension has loaded).

A module script (`<script type="module">`, which the `htmx.org` package's
`module` entry needs) and an `async` script run later than a classic script
placed after them. The extension copes with a module htmx: when htmx is not
there yet and the page is still loading, it waits for `DOMContentLoaded`
before it decides whether to run with htmx. An htmx that arrives later than
that (an `async` script, or one a script adds) is too late: the extension
then runs without htmx, so requests carry no `hp-*` parameters and nothing
slides, and it says so on the console. With a bundler, import htmx first;
imports run in order:

```js
import 'htmx.org'
import 'htmx-ext-plot'
import 'htmx-ext-plot/hx-plot-brush.js' // optional
```

## How it works

A chart is an `<svg class="hp">` with an id, a `<metadata class="hp-tips">`
JSON payload (where the chart can be hovered and what each position reads),
and a few root attributes that describe its scales (`data-hp-plot`,
`data-hp-x`, `data-hp-y`). The extension reads those; it never formats or
draws data itself.

- **Pointer and keyboard.** Hover shows a tooltip and a crosshair (a band
  highlight on bar charts, a ring on scatter plots). A focused chart steps
  through its points with the arrow keys, Home and End, and announces each
  step through a `role="status"` region. This works with or without htmx.
- **Swaps.** The extension takes part in every htmx swap through its swap
  hooks, so polls, form responses and SSE messages all get the same
  treatment; `innerMorph` stays the swap style. A chart whose x window moved
  slides to its new place (a transform, so it animates in every browser); a
  window that jumped or zoomed snaps; anything else keeps the stylesheet's
  CSS transitions. Hover and keyboard state carry over by row key. A table
  scrolled to its newest row stays there, and an opened `<details>` around a
  table stays open.
- **Gestures.** A click or Enter on a point dispatches `htmx:plot:select`,
  a drag (or Shift+Arrow, then Enter) dispatches `htmx:plot:brush`. Use them
  as `hx-trigger`s: the extension adds the chart id and the selected key or
  range to the request those events trigger.
- **Sizing.** An element with `hx-plot:autosize` reports its width as `hp-w`
  on requests that target it, and dispatches `htmx:plot:resize` when it
  changes, so the server renders at the size the page has.

## Usage

A chart that polls, re-renders at its container's width, drills down on a
click and zooms on a drag:

```html
<div class="zoom" hx-get="/charts/latency" hx-trigger="htmx:plot:brush"
     hx-target="#latency" hx-swap="outerMorph" hx-sync="replace" hx-plot:brush>
  <div id="latency" hx-get="/charts/latency" hx-trigger="load, every 2s, htmx:plot:resize"
       hx-swap="innerMorph" hx-sync="closest .zoom:drop" hx-plot:autosize>
    <!-- the server's chart -->
  </div>
</div>
<div hx-get="/detail" hx-trigger="htmx:plot:select from:#latency" hx-target="#detail"></div>
<section id="detail" aria-live="polite"></section>
```

Dragging across 2:05 PM to 2:15 PM sends:

```http
GET /charts/latency?hp-w=800&hp-chart=lt&hp-x0=2026-03-03T14%3A05%3A00Z&hp-x1=2026-03-03T14%3A15%3A00Z HTTP/1.1
HX-Request: true
HX-Target: div#latency
```

The server answers with the zoomed polling element (its `hx-get` carries the
range, so later polls stay zoomed) and an `HX-Push-Url` naming the page URL
for that range; the [spec](../docs/spec.md#54-example-brush-to-zoom-a-live-chart)
walks through the exchange. A click on 2:10 PM sends
`GET /detail?hp-chart=lt&hp-x=2026-03-03T14%3A10%3A00Z`.

### Server: Python

A server in any language writes the chart per the spec and reads the
parameters. The value and escaping helpers (`number`, `time_value`,
`parse_time_value`, `xml_attr`, `tips_json`, ...) are in the spec's
[Appendix B](../docs/spec.md#appendix-b-an-exchange-and-helpers-in-python);
`render_latency` stands for your renderer. The
[server guide](../docs/implementing-a-server.md) builds a complete Level 3
server, renderer included, in standard-library Python.

```python
import math
from http.server import BaseHTTPRequestHandler
from urllib.parse import parse_qs, urlparse


class Charts(BaseHTTPRequestHandler):
    def do_GET(self):
        q = {k: v[0] for k, v in parse_qs(urlparse(self.path).query).items()}
        try:
            width = float(q.get('hp-w', ''))
        except ValueError:
            width = 0
        width = min(max(width, 50), 4096) if math.isfinite(width) and width > 0 else 640
        window = None
        if q.get('hp-x0') and q.get('hp-x1'):  # a brush, not a poll
            try:
                window = sorted([parse_time_value(q['hp-x0']), parse_time_value(q['hp-x1'])])
            except ValueError:
                pass  # ignore a brush that does not parse
        body = render_latency(width, window).encode()  # <svg class="hp" ...> per the spec
        self.send_response(200)
        self.send_header('Content-Type', 'text/html; charset=utf-8')
        self.send_header('Vary', 'HX-Request, HX-Request-Type')
        self.end_headers()
        self.wfile.write(body)
```

### Server: Go

With hyperplot, `hx.Size`, `hx.Selected` and `hx.Brushed` read the
parameters:

```go
mux.HandleFunc("GET /charts/latency", func(w http.ResponseWriter, r *http.Request) {
	width, _ := hx.Size(r, 640, 0) // keep the height on the server
	chart := latencyChart(width, 320)
	if b, ok := hx.Brushed(r); ok {
		if t0, t1, err := b.Times(); err == nil {
			chart.X.TimeDomain = [2]time.Time{t0, t1}
		}
	}
	svg, err := chart.Render()
	if err != nil {
		w.WriteHeader(http.StatusNoContent) // keep the last good chart
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Write(svg)
})
```

### Server implementations

| Server | Language | Level |
|---|---|---|
| [hyperplot](https://github.com/ironystock/hyperplot) (the reference) | Go | 3 |
| [the server guide's example](../docs/implementing-a-server.md) (line charts on a time axis) | Python, standard library | 3 |
| [`examples/python`](../examples/python/) (lines, grouped bars, SSE) | Python, standard library | 3 |
| [`examples/node`](../examples/node/) (lines, grouped bars, SSE) | Node, `node:` built-ins | 3 |

To add yours, check it against the
[conformance fixtures](../testdata/conformance/README.md) with
[hpcheck](../tools/hpcheck/README.md) and open a pull request.

## Attributes

Both attributes are container attributes: the extension finds the nearest
element at or above the one in question that carries it. They accept htmx's
spellings: `data-hx-plot:…` (or `htmx.config.prefix`), and
`htmx.config.metaCharacter` in place of `:`. Tuning is configuration, not
attributes (see [Config](#config)).

### `hx-plot:autosize`

On the element whose content box sizes the chart, usually the swap target.
Requests whose target is this element or inside it carry `hp-w`, its content
width in CSS px, and the element dispatches `htmx:plot:resize` when its width
changes by 8 px or more. With `hx-plot:autosize="both"` they also carry `hp-h`
and height changes count; use it only when CSS sets the element's height,
because a wrapper as tall as its chart would grow on every request.

### `hx-plot:brush`

On an ancestor of the charts that may be brushed (needs `hx-plot-brush.js`).
Only charts whose x axis is a time or linear scale can be brushed. A mouse or
pen drag of at least 4 px across the plot, or Shift+ArrowLeft/ArrowRight and
Shift+Home/End from a keyboard point followed by Enter, dispatches
`htmx:plot:brush`. Escape cancels. Touch drags keep scrubbing the tooltip.

## Request parameters

The extension adds these in `htmx_config_request`. It never replaces a value
the request already has, in its body (form values, `hx-vals`) or in its URL.

| Parameter | Sent | Value |
|---|---|---|
| `hp-w`, `hp-h` | on requests that target an `hx-plot:autosize` element or something inside it | content width (and, for `both`, height) in CSS px |
| `hp-chart` | with a select or brush | the chart's id |
| `hp-x` | with a select | the selected row's key: a time (`2026-03-03T14:10:00Z`), a number or a category |
| `hp-series` | with a select, when known | the series name |
| `hp-x0`, `hp-x1` | with a brush | the range's ends, as times or numbers (servers must order them) |

## Headers

The extension reads and sends no headers of its own. The exchanges it
takes part in use htmx's:

| Header | Direction | Use |
|---|---|---|
| `HX-PTag` | both | with htmx's `hx-ptag` extension: the server answers `304` while a polled chart is unchanged |
| `HX-Push-Url` | response | a zoom names the page URL for its range, so it can be bookmarked and Back undoes it |
| `Vary: HX-Request, HX-Request-Type` | response | on a URL that serves either a page or a fragment |

## Events

| Event | Target | Bubbles | `detail` |
|---|---|---|---|
| `htmx:plot:resize` | the `hx-plot:autosize` element | no | `{width, height}` |
| `htmx:plot:select` | the `svg.hp` | yes | `{chart, x, series, index}` |
| `htmx:plot:brush` | the `svg.hp` | yes | `{chart, x0, x1}` |

They go through htmx, so `htmx.config.metaCharacter` applies to their names,
`logAll` logs them and `hx-on::plot:select="…"` works. Without htmx they are
plain `CustomEvent`s.

## Config

Set page defaults in `htmx.config.plot` (for example in the `htmx-config`
meta tag: `{"plot":{"slide":false}}`), and per request with `hx-config` on
the polling or `hx-sse:connect` element: `hx-config="plot.slide:false"`.

| Key | Default | |
|---|---|---|
| `slide` | `true` | slide a moved time window; `false` snaps it |
| `follow` | `true` | a table scrolled to its newest row stays there |
| `keepOpen` | `true` | morphs never open or close a `<details>` holding a table |
| `duration` | `350` | slide (and tween) duration in ms |
| `easing` | `"ease-out"` | slide (and tween) timing function, any CSS easing; anything else (`"easeOutCubic"`) counts as the default |
| `strings` | `{"noData": "no data", "range": "{0} to {1}"}` | the extension's own words, for announcements |

Motion stops with `prefers-reduced-motion: reduce` and during view
transitions.

## Keyboard and accessibility

| Input | Read values | Select | Brush |
|---|---|---|---|
| mouse, pen | hover | click | drag |
| keyboard | Arrow, Home, End | Enter | Shift+Arrow, Shift+Home/End, then Enter |
| touch | tap, sideways scrub | second tap on the active point | no |

Each keyboard step, and a keyboard range, is announced through a visually
hidden `role="status"` region; refreshes are never announced. Offer plain
HTML equivalents for gestures (a link per table row, a from/to form), and
have the server update a polite live region after a gesture: see
[spec.md §5.5](../docs/spec.md#55-equivalents-in-plain-html).

## Notes

- All text from the server is inserted with `textContent`. Styles are set
  through the CSSOM and the Web Animations API, so pages can use a strict
  `Content-Security-Policy` (`script-src 'self'; style-src 'self'`).
- Charts and autosize elements inside `hx-ignore` are left alone.
- One global, `globalThis.hyperplot`, guards against loading the script twice
  and is how the companion scripts find it.
- `hyperplot.tween.force = true` makes `hx-plot-tween.js` tween in every
  browser (a test hook).

## Migration

From `hyperplot.js` 0.1.0, which was never published:

| 0.1.0 | 0.2.0 |
|---|---|
| `hyperplot.js`, registered as `hyperplot` | `hx-plot.js`, registered as `plot` (list `plot` in `htmx.config.extensions`) |
| `data-hp-autosize` | `hx-plot:autosize` |
| `hp:resize` | `htmx:plot:resize` |
| `"morphIgnore":["data-htmx-powered","open"]` in the page's htmx config | not needed: the extension keeps a table's `<details>` open |

There are no aliases for the old names. Charts rendered before spec 1
(`data-hp=""`) still get tooltips and keyboard reading, but no slide, row-key
carry-over or gestures.

## Size

Measured with terser 5.36 (`-c -m`) and `gzip -9` by `npm run size`,
against the budgets in `build.mjs`:

| File | Minified (budget) | Minified + gzip (budget) |
|---|---|---|
| `hx-plot.js` | 18,316 (13,312) | 7,797 (5,632) |
| `hx-plot-brush.js` | 3,360 (4,096) | 1,735 (2,048) |
| `hx-plot-tween.js` | 1,842 (2,048) | 1,014 (1,024) |

The core `hx-plot.js` is **over its budget**, so `npm run size` fails for
now. The budget itself is approved; how the core gets back under it (trim
it, or move more of it into an opt-in companion) is still open
([DESIGN.md, "Size budget"](../docs/DESIGN.md#size-budget)).

## Development

From this directory (from the repository root, `make client`):

```sh
node --test
```

No dependencies are needed. `hx-plot.test.mjs` covers the pure helpers;
`dom.test.mjs`, `swap.test.mjs`, `brush.test.mjs` and `tween.test.mjs` run the
scripts against the small fake browser in `fakedom.mjs` (registration and the
allow-list, attributes, autosize, pointer and keyboard, select, swaps,
brushing, tweens); `conformance.test.mjs` runs every client expectation of
`testdata/conformance`.

The real-browser tests in `browser/` drive an installed Chrome through
`playwright-core`, a dev-only dependency. `extension.browser.mjs` uses static
test pages (gestures, the client without htmx, SSE, the tween);
`htmx.browser.mjs` runs the extension's hooks under the real htmx 4.0.2 on
static pages (Back with `hx-history-cache` and after a boosted link,
innerHTML and outerHTML swaps of a focused chart, `transition:true`,
`hx-ptag` 304s, the `data-hx-plot` spelling with a metaCharacter, live
tables, a chart in a modal dialog, the brush); `edges.browser.mjs` covers
edge cases found in review (marks through settle delays and view
transitions, a module htmx, `hx-targets`, a polled chart in a dialog, a bad
easing, `hx-history-cache` under a strict CSP, transformed dialogs, the client
without htmx on added content, a cancelled tween, a brush across a slide,
the band highlight, a hoverable tooltip);
`tables.browser.mjs`, `slide.browser.mjs`, `dashboard.browser.mjs` and
`dashboard-a11y.browser.mjs` build and run the Go dashboard.

```sh
npm ci
npm run test:browser
```

Set `CHROME_PATH` when Chrome is not at its usual install path and
`HP_BROWSER_TMP` to choose where Chrome profiles go; `browser/harness.mjs`
lists the other settings. `npm run size` minifies and gzips each script and
checks it against its budget, and `npm run build` writes the `.min.js` files
with source maps; both run terser through `npx` (or the command in `TERSER`),
so the package declares no minifier.

MIT licensed.
