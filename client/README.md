# hyperplot client

`hyperplot.js` is the optional browser half of
[hyperplot](https://github.com/ironystock/hyperplot), a Go library that renders
charts as server-side SVG. Charts are complete without it; the script adds:

- **Tooltips** on hover and touch, with a crosshair on line and area charts, a
  highlighted band on bar charts, and nearest-point hits on scatter plots,
  where a ring marks the active dot. The text is formatted by the server and
  inserted with `textContent` only.
- **Keyboard reading.** Focus a chart, then use ArrowLeft and ArrowRight to step
  through points (on a scatter plot, left to right whatever the data order),
  Home and End to jump to the ends, and Escape to close. Each
  step is announced to screen readers through a visually hidden
  `role="status"` region that exists from page load; the visual tooltip itself
  stays `aria-hidden`.
- **Live updates that keep their state.** After an htmx morph swap or SSE
  message, an open tooltip is re-applied to the updated chart. A keyboard
  reader stays on the same point (matched by its heading) as the data slides,
  and updates are never announced, so a focused live chart stays quiet until
  the reader moves.
- **Responsive re-rendering.** Elements with `data-hp-autosize` fire `hp:resize`
  when their width changes by 8px or more, and htmx requests from (or into)
  them carry an `hp-w` parameter (and `hp-h`, for `data-hp-autosize="both"`)
  for the server to render at. A value already in the request, including one
  written into the `hx-get` or `hx-sse:connect` URL, is never replaced.

It is under 8KB minified (about 3.7KB gzipped), has no dependencies and needs
no build step. It works without htmx too; only the live-update and sizing
features need htmx 4.

## Usage

Go programs serve the copy embedded in the `client` package, with no npm
involved:

```go
import "github.com/ironystock/hyperplot/client"

mux.Handle("GET /static/hyperplot.js", client.Handler())
```

Anything else can copy `hyperplot.js` into its static files. Either way, load
it after htmx:

```html
<script src="https://cdn.jsdelivr.net/npm/htmx.org@4.0.2/dist/htmx.min.js"></script>
<script src="https://cdn.jsdelivr.net/npm/htmx.org@4.0.2/dist/ext/hx-ptag.js"></script>
<script src="/static/hyperplot.js"></script>
```

The npm package is not published yet; this README will list a CDN URL once it
is.

A live chart that polls, skips unchanged data with a ptag, and re-renders at
its container's width:

```html
<div hx-get="/charts/latency"
     hx-trigger="load, every 2s, hp:resize"
     hx-swap="innerMorph"
     data-hp-autosize>
</div>
```

On the server, `width, _ := hx.Size(r, 640, 0)` reads `hp-w`; keep the
chart's height fixed. A wrapper without a CSS height is as tall as the chart
inside it, so its height is only sent for `data-hp-autosize="both"`, which
is for wrappers whose height CSS sets (see the
[autosize guide](../docs/htmx.md#height)).

## Notes

- If you restrict extensions with `htmx.config.extensions`, include `hyperplot`.
- `hp:resize` does not bubble; listen for it on the `data-hp-autosize` element.
  Add `load` to `hx-trigger` if the first render should also fit the container.
- Motion lives in the hyperplot stylesheet, behind
  `prefers-reduced-motion: no-preference`. With reduced motion, the tooltip
  moves only when the active point changes instead of following the pointer.

## Development

Run the tests from this directory (from the repository root, use
`make client`):

```sh
node --test
```

`hyperplot.test.mjs` covers the pure helpers. `dom.test.mjs` runs the whole
script against the small fake browser in `fakedom.mjs` to cover the htmx
extension hooks, autosize, pointer and keyboard tooltips, the status
region, and that the version in `package.json`, the banner and
`globalThis.hyperplot.version` agree. Neither needs dependencies. There is
no real-browser test yet; see the roadmap in
[DESIGN.md](../docs/DESIGN.md#roadmap).

The minified size budget is 8KB and nearly used up (7,929 bytes with
`terser -c -m` for 0.1.0), so new features need trimming elsewhere.

MIT licensed.
