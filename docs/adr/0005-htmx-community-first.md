---
status: proposed
date: 2026-11-26
decision-makers: hyperplot maintainers
---

# The htmx extension is the product; Go is its reference server

## Context and Problem Statement

hyperplot v0 is a Go charting library with an optional browser script that
adds tooltips, keyboard reading and live-update support. Only Go
applications can produce the charts, and the script's contract with the
server lives in Go-centric prose ([DESIGN.md](../DESIGN.md)).

The maintainer's direction for rev 2: "I want this to be an HTMX-community
contribution more than a chart lib contribution." Research for this record
(notes in the rev-2 workspace, summarised in "More Information") found:

* Every existing htmx chart extension (hx-chart, htmx-plotly, htmx-echarts,
  c-cube/hx-plot) sends JSON to a client charting library. None sends the
  chart itself as hypermedia, which is what hyperplot does.
* The most active of them, htmx-echarts, argues against server SVG in its
  README: "Live updates redraw the whole chart, which looks clunky." Morph
  swaps answer that, once sliding windows animate correctly (roadmap bug 2).
* htmx 4 has conventions for third-party extensions: one self-contained
  `hx-<name>.js` file loaded after htmx, a registered name, namespaced
  `hx-<name>:<attr>` attributes read through the extension API, events named
  per extension, `htmx.config.<name>` for settings, and no `hx-ext`.
* The htmx team wants third-party extension docs kept off-site and listed by
  a one-line entry on the four.htmx.org community page (Carson Gross
  closing bigskysoftware/htmx#3906, 2026-09-22). The page lists no real
  extension yet. `bigskysoftware/htmx-4-community-extensions` holds only
  the team's own ports and has never taken an outside contribution.
* Nothing of hyperplot has been tagged or published, so names can still
  change for free.

What should hyperplot be, and how should it present itself to the htmx
community?

## Decision Drivers

* Any server language (Python, PHP, Ruby, Go, …) can produce live charts.
* Follow htmx 4's extension conventions closely enough that the extension
  looks native.
* Stay hypermedia: the server sends the chart and its formatted text; the
  browser never receives a data API.
* One small, readable, dependency-free file with no build step, which users
  can vendor.
* Keep what the Go library already does well (rendering, formatting,
  accessibility, determinism) and make it the proof of the design.
* Names that are free, conventional and hard to confuse.

## Considered Options

* The extension is the product, with a language-neutral wire spec; the Go
  library is its reference server
* Stay a Go charting library with an optional client script
* Contribute the extension to `bigskysoftware/htmx-4-community-extensions`
* Render in the browser like the other extensions (JSON in, SVG out)

## Decision Outcome

Chosen option: "The extension is the product", because it is the only
option that lets any backend produce the charts while keeping the
hypermedia design that sets hyperplot apart.

The contract between server and extension becomes
[docs/spec.md](../spec.md), versioned (spec 1), in RFC 2119 language, with
three conformance levels (static, live, interactive) and fixtures in
`testdata/conformance/` that servers in any language can test against. The
Go module stays `github.com/ironystock/hyperplot` and implements Level 3.
The repository keeps its name.

### Names

| Thing | Name | Why |
|---|---|---|
| npm package | `htmx-ext-plot` | the `htmx-ext-*` prefix is how third parties name htmx extensions (htmx-ext-transport, htmx-ext-form-json, …); free on npm. Fallback: `htmx-ext-hyperplot`, also free |
| file | `hx-plot.js` | core's `hx-<name>.js` pattern (`hx-sse.js`, `hx-ptag.js`) |
| registered name | `plot` | mirrors `hx-sse.js` → `sse` and `hx-ptag.js` → `ptag`; it is what a page lists in `htmx.config.extensions` |
| attributes | `hx-plot:autosize`, `hx-plot:brush` | the namespaced form core uses for extensions with several attributes (`hx-sse:connect`, `hx-ws:send`), in every htmx spelling (`data-hx-` prefix, `metaCharacter`). Attributes say what and where only; both are container attributes found with `closest()` (spec §6.2) |
| events | `htmx:plot:resize`, `htmx:plot:select`, `htmx:plot:brush` | the convention of every extension shipped with htmx 4 (`htmx:sse:after:message`, `htmx:head:after:merge`); sent through `api.triggerHtmxEvent`, so `metaCharacter`, `logAll`, extension hooks and `hx-on::plot:select` all work. Without htmx the same names are plain `CustomEvent`s |
| config | `htmx.config.plot`, and per request the `plot` key of `hx-config` | the hx-sse pattern (`{...defaults, ...htmx.config.sse, ...hx-config .sse}`): tuning such as turning the slide off is configuration, not an attribute |
| swap style | none | `innerMorph` stays the swap. htmx calls `handle_swap` only for styles it does not know, and the chart-aware behaviour fits in swap hooks, which fire for polls and SSE messages alike. A server only has to emit morph-friendly HTML |
| markup and request parameters | `hp-*` classes, `data-hp*` attributes, `hp-*` parameters (unchanged) | they are document vocabulary, not htmx vocabulary, and v0 already uses them |
| brand | hyperplot | the project, the Go module and the site; "hyperplot, the `hx-plot` extension" |

The rule behind the two prefixes: authors write `plot`; servers read and
write `hp`. The one global, `globalThis.hyperplot` (the double-load guard
and the hook for the optional tween script), is a documented exception to
htmx's no-globals pattern. When a page's `htmx.config.extensions` does not
list `plot`, the extension does nothing at all.

The v0 names `hyperplot` (registration), `data-hp-autosize` and `hp:resize`
are replaced without aliases, since nothing was published.

### Distribution

* npm `htmx-ext-plot`: `hx-plot.js` (the source, unminified, the primary
  artifact), `hx-plot.min.js` and its source map (built at publish time),
  `hx-plot.css` (the reference stylesheet), and the optional companions
  `hx-plot-brush.js` (brushing) and `hx-plot-tween.js` (the Safari path
  tween), each with its own minified copy. `htmx.org >=4.0.0` is an
  optional peer dependency,
  because the script also works without htmx. jsDelivr and unpkg serve the
  files from npm.
* Go: package `client` embeds the same `hx-plot.js` and its companions and
  serves them, so Go users need no npm.
* Docs: the client README follows the layout of core extension pages
  (Installing, How It Works, Usage with the raw HTTP exchange and server
  samples in more than one language, Attributes, Events, Config).

### Submission path

1. Host the extension and its docs in this repository.
2. The maintainer publishes `htmx-ext-plot` to npm.
3. The maintainer opens one pull request against `bigskysoftware/htmx`,
   base branch `four`, adding one bullet to
   `www/src/content/extensions/community.md` in the page's placeholder
   format. No docs page (#3906 was declined for that) and nothing in
   `htmx-4-community-extensions`.
4. Before announcing, the maintainer files the htmx 4.0.2 extension bugs
   already on the roadmap.

### Consequences

* Good, because a Python, PHP or Ruby server can produce live,
  accessible, interactive charts by following one document and testing
  against fixtures, without reading Go.
* Good, because the extension follows htmx's own conventions (registration,
  attributes, events, config, no private transport), so it reads as an htmx
  extension rather than a charting framework.
* Good, because the positioning is distinct: the only htmx chart extension
  where the server sends the chart, consistent with the "prime directive" of
  the htmx essay *Hypermedia-Friendly Scripting*.
* Good, because the Go library loses nothing: it becomes the reference
  implementation, and the fixtures make that claim testable.
* Bad, because the wire format is now public API in two directions. A
  markup change needs a spec change, a fixture change and a version
  decision, not just a golden update.
* Bad, because `hx-plot` collides softly with `c-cube/hx-plot` (an
  unlicensed, unpublished, inactive htmx 2 Vega-Lite extension that also
  registers `plot`), "plot" suggests Observable Plot, and the extension
  claims a generic word (`hx-plot:`, `htmx.config.plot`, `htmx:plot:*`) for
  one particular wire format. The README's first
  line has to say "server-rendered SVG, no client chart library". The
  maintainer weighed the fallback `htmx-ext-hyperplot` and kept `plot`
  (2026-11-26).
* Bad, because the client grows (slide, gestures, table behaviour), so its
  size budget rises from 3.7 KiB to 5.5 KiB gzipped (8 KiB to 13 KiB
  minified), about a third of htmx core
  ([DESIGN.md, "Size budget"](../DESIGN.md#size-budget); approved by the
  maintainer, 2026-11-26). If the measured core exceeds it, brushing moves
  to an opt-in file rather than the budget rising again. It did:
  brushing is `hx-plot-brush.js`, and the core is still over its budget,
  which DESIGN.md records as open.
* Neutral, because the renames are free now and expensive after the first
  publish, so they happen in rev 2 or not at all.

### Confirmation

* `testdata/conformance/` fixtures: the Go renderer must reproduce the
  reference files byte for byte, and the client tests consume the same
  files.
* A small standard-library Python server in `examples/` passes the fixtures'
  server assertions, which shows the spec is implementable without Go.
  Met: [`examples/python/`](../../examples/python/) is a Level 3 server on
  the standard library, and [`tools/hpcheck/`](../../tools/hpcheck/README.md),
  a standard-library Python checker, passes its fixture output and its
  running server (`make conformance`; DESIGN.md Roadmap, "Rev 2: open",
  item 2). `examples/node/` passes the same checks. The
  [server guide](../implementing-a-server.md) builds a smaller Level 3
  Python server, and `docs/wire_test.go` runs its Python and the spec's
  Appendix B helpers on edge cases.
* The four.htmx.org community page lists the extension.

## Pros and Cons of the Options

### The extension is the product

* Good, because it reaches every htmx user, not only Go users.
* Good, because the spec forces the client/server contract to be explicit
  and tested.
* Bad, because it is more to maintain: spec, fixtures, two test suites.

### A Go charting library with an optional client

* Good, because it is what exists, with the least work.
* Bad, because only Go applications benefit, and the htmx community sees a
  Go library rather than an htmx extension.

### Contribute to htmx-4-community-extensions

* Good, because it would sit next to official ports.
* Bad, because that repository is managed by the htmx team, holds only
  their ports of htmx 2 extensions, and has no process for outside
  contributions. The team asked for third-party extensions to be hosted
  off-site and linked.

### Render in the browser like the other extensions

* Good, because the payload is smaller and familiar charting libraries do
  the drawing.
* Bad, because it turns the server into a JSON API, against the hypermedia
  approach, and loses server-side formatting, determinism and the
  accessibility work. [ADR 0001](0001-server-rendered-svg.md) already
  rejected it.

## More Information

* [docs/spec.md](../spec.md): the wire spec.
* [testdata/conformance/](../../testdata/conformance/README.md): fixtures.
* [ADR 0003](0003-htmx4-morph-live-updates.md) says htmx 4 morphs with
  idiomorph. htmx 4.0.2 uses its own morph: id-set matching, then exact
  equality, then position, with a 10-node look-ahead. The decision there is
  unchanged; DESIGN.md has the corrected facts.
* Prior art: htmx-echarts (github.com/marcingolenia/htmx-echarts),
  hx-chart (github.com/jop-software/hx-chart), htmx-plotly
  (github.com/ocramz/htmx-plotly), c-cube/hx-plot; Mike Bostock, "Path
  Transitions" (bost.ocks.org/mike/path) for the sliding-window technique;
  the Datastar SDK ADR as a model for a language-neutral spec.
