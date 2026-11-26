# Changelog

All notable changes to this project are documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).
Before v1.0.0, minor releases may include breaking changes; they are listed
under **Changed** and marked **Breaking**.

## [Unreleased]

Initial development. Nothing has been released yet. The project is now
presented as an htmx extension first: `hx-plot` (npm `htmx-ext-plot`) for
charts a server renders as SVG, with a language-neutral wire spec, and the
Go library as its reference server ([ADR 0005](docs/adr/0005-htmx-community-first.md)).

### Added

- `TraceTheme()`, hyperplot's own branded palette: a phosphor trace on
  dark glass, with slot 1 as the brand color and no red series. It is
  opt-in; `DefaultTheme()` and `Stylesheet()` are unchanged. The brand
  assets are in [docs/brand/](docs/brand/README.md), and the README now
  opens with the lockup and a hero image.
- The wire spec, [spec/spec.md](spec/spec.md) (spec version 1): the chart
  markup, hover payload, ids, geometry attributes and row keys a server
  writes; the `hx-plot:` attributes, `hp-*` request parameters and
  `htmx:plot:*` events; the extension's swap behaviour; three conformance
  levels (static, live, interactive); and Python helpers for the value
  forms, escaping and parsing.
- Conformance fixtures in `spec/conformance/`: language-neutral chart
  inputs, reference output and assertions for servers (geometry numbers
  are checked against the spec's number pattern), and expectations for
  clients. The Go renderer reproduces every reference file byte for
  byte, and the client tests run every client expectation.
- The `hx-plot` extension 0.2.0 (`client/hx-plot.js`), registered as `plot`
  and started only when htmx accepts it (it honours
  `htmx.config.extensions`). It reads the container attributes
  `hx-plot:autosize` and `hx-plot:brush` in every htmx spelling, takes its
  settings from `htmx.config.plot` and the request's `hx-config`, and sends
  its events through htmx. It also works without htmx.
- Select: a click, Enter, a second tap or an assistive-technology click on a
  point dispatches `htmx:plot:select`, and requests it triggers carry
  `hp-chart`, `hp-x` (the row key) and `hp-series`.
- Brush, in the optional companion `hx-plot-brush.js`: a mouse or pen drag,
  or Shift+Arrow keys then Enter, on a time or linear x axis dispatches
  `htmx:plot:brush`, and requests it triggers carry `hp-chart`, `hp-x0` and
  `hp-x1`. The range keeps its data values across live swaps.
- Sliding windows: when a chart's time window moves between frames, the
  extension slides the content with the Web Animations API instead of
  letting CSS tween the line's `d`, and snaps a window that can't slide
  (a zoom, a resize, a jump). On by default for animated charts;
  `hx-config="plot.slide:false"` turns it off.
- Live tables: the extension keeps a `<details>` holding a chart's table
  open or closed as the reader left it, and keeps a table scrolled to its
  newest row there as rows arrive.
- The optional companion `hx-plot-tween.js`, which animates value changes
  in browsers whose CSS can't transition a path's `d` (Safari).
- npm packaging for `htmx-ext-plot` (not yet published), with a size check
  and a minified build (`npm run size`, `npm run build`). The core script
  is over its size budget for now, so `npm run size` fails.
- Renderer output for spec 1: `data-hp="1"`, the geometry attributes
  `data-hp-plot`, `data-hp-x` and `data-hp-y`, a row key `k` in every
  tooltip row, and ids on the table and every table row.
- Package `hx`: `Selected` and `Brushed` read the gesture parameters, with
  `Selection.Time`, `Selection.Float`, `Brush.Times`, `Brush.TimesIn` (which
  also reads the HTML `datetime-local` form in a given zone) and
  `Brush.Floats`, plus the `Param*` constants.
- Package `client`: `BrushJS`, `BrushHandler`, `TweenJS` and `TweenHandler`
  serve the companions.
- Stylesheet rules for spec 1: `touch-action: pan-y pinch-zoom` on charts
  (touch scrubbing depends on it), the brush rectangle, and a crosshair
  cursor on brushable charts.
- Real-browser tests in `client/browser/` (Chrome, through the dev-only
  `playwright-core`): the two reported bugs (`tables.browser.mjs`,
  `slide.browser.mjs`); select, brush, the extension without htmx, SSE and
  the tween (`extension.browser.mjs`); the extension's hooks under the real
  htmx 4.0.2 (`htmx.browser.mjs`: history, swaps, hx-ptag, live tables,
  dialogs); edge cases found in review (`edges.browser.mjs`); and the
  example dashboard's gestures and accessibility (`dashboard.browser.mjs`,
  `dashboard-a11y.browser.mjs`). `make browser` runs them, and CI runs
  them in a "Browser tests (Chrome)" job.
- Example dashboard: brush-to-zoom on the latency chart (with a
  `datetime-local` from/to form, `HX-Push-Url` and "Back to live") and
  drill-down on the requests chart (with window links as the plain-HTML
  equivalent), each with a polite live region; one bucket of overscan in
  the sliding charts.
- Spec checks on every chart the Go tests render, including the fuzz test.
- Documentation: a [server guide](docs/implementing-a-server.md) that builds
  a Level 3 server in standard-library Python, with a conformance
  checklist (`go test ./docs` runs its Python and the spec's helpers when
  Python 3.8+ is installed); the README and the extension's README repositioned around the
  extension; the htmx guide's drill-down, zoom and sliding-window patterns.
- `plot.Chart`: server-side rendering of charts to deterministic,
  well-formed, accessible SVG, with `Render`, `WriteTo`, `HTML` (for
  `html/template`), `Component` (structurally compatible with
  `templ.Component`) and `Table` (an accessible `<table>` equivalent).
- Marks: `Line`, `Area`, `Bar`, `Dot` and `RuleY`, with linear, monotone and
  step curves, grouped and stacked bars and areas, gaps for missing values,
  end dots, direct labels and an automatic legend.
- X values as `Floats`, `Times` or `Categories`, selecting a linear, time or
  band scale, and the generic `Map` helper for row-oriented data.
- Fixed-order, colorblind-aware 8-color categorical palette with a selected
  dark mode; `Slot` pins a series to a color; `ErrTooManySeries` for more than
  8 series.
- `Theme`, `DefaultTheme` and `Stylesheet` for CSS theming via custom
  properties, with `prefers-color-scheme`, `data-theme`, reduced-motion and
  forced-colors support, and `Standalone` charts with embedded styles.
- `Sparkline` for compact, axis-free charts in stat tiles.
- Package `scale`: `Linear`, `Time` (DST-safe; US calendar with Sunday weeks and 12-hour labels) and `Band`
  scales, "nice" tick generation, and number formatting (`FormatNumber`,
  `FormatCompact`, `TickFormat`) with a typographic minus sign.
- Package `hx`: htmx 4 server helpers: `IsRequest`, `IsPartial`, content tags
  (`Tag`, `CheckPTag` with `304 Not Modified`), a concurrency-safe SSE
  `Stream` with replay support, multi-target `Partial`, and responsive `Size`.
- The client extension's base features: tooltips, crosshair, band
  highlight and a ring on the active scatter point, keyboard reading
  announced through a persistent status region, morph-safe hover and focus
  state, and responsive re-rendering; no dependencies and no build step.
  Package `client` embeds it and serves it with caching headers.
- `scale.Time.FormatterFor`: full time labels at the tick precision, made
  finer where the data needs it. Tooltips and tables use it, so 30-second
  buckets under minute ticks no longer print as duplicate rows.
- `scale.Time.TickFormatFor`: tick labels that know their interval, so the
  first of a month reads "Mar 1" among daily ticks instead of a bare "Mar".
- Visual polish found by screenshot review: "No data" in empty charts; dot
  labels that try four positions, stay inside the plot and never cover
  another point; band labels truncated to their band when that keeps every
  label (and never truncated and thinned at once); tooltip and table values
  from 100,000 up compact at four significant digits ("1.104M", "575.4K");
  a surface-colored halo behind chart labels.
- Example live dashboard (`examples/dashboard`) demonstrating polling with
  content tags, server-sent events, filters, multi-target updates and
  autosizing under a strict Content-Security-Policy, and a minimal
  `examples/basic` matching the README quick start.
- Project infrastructure: CI on Linux, Windows and macOS, linting, fuzzing,
  golden-file tests, contribution guidelines and architecture decision
  records. A docs test compiles every Go sample in the README, the htmx
  guide and the `hx` package doc against the current API, with the
  helpers a sample leaves to the reader declared at their documented
  types, so calls on their results are checked too. Other docs tests
  check that every `htmx-config` shown sets `noSwap` for 4xx and 5xx,
  that SSE samples skip frames that fail to render, and that samples
  serving a page or a fragment from one URL send `Vary`, that
  `examples/basic` stays identical to the README quick start, and that
  every relative link and anchor in the Markdown files resolves. `make check`
  runs the same lint, hygiene, golden-file and client checks as CI.
- More examples, indexed in [examples/README.md](examples/README.md):
  Level 3 servers in standard-library Python (`examples/python`) and in
  Node with `node:` built-ins only (`examples/node`), with the same pages
  and URLs; two more Go demos, `examples/sensors` (SSE sparkline tiles,
  select, brush, threshold rules, a live alerts table) and
  `examples/builds` (polling with content tags, a scatter with brush,
  stacked bars with select, a filter form); a chart gallery,
  `examples/gallery`, with every mark and variant beside the Go code that
  drew it, in light and dark, which `-out DIR` writes as static files; and
  ten one-pattern Go recipes under `examples/recipes`
  ([index](examples/recipes/README.md)): polling-ptag, sse-stream,
  filters-form, select-drilldown and brush-zoom, one per htmx guide
  pattern and linked from it, and static-page, sizing, theming,
  sparkline-table and standalone-svg, with preview SVGs their tests keep
  current and `make golden` rewrites.
- A real-browser test of every example,
  `client/browser/examples.browser.mjs` (in `npm run test:browser`,
  `make browser` and the CI browser job, which now sets up Python): it
  starts each Go, Python and Node example on a free port and opens it in
  one Chrome, and fails on a console error or CSP violation, a failed
  request, a chart that does not draw, a live chart that does not update
  or a gesture with no answer. The harness gains `buildGo` and
  `startServer` for any example server.
- The example dashboard's stream sends `retry: 2000`, as the htmx guide
  recommends. The Go spec checks (`checkSpec`) reject a chart reference to
  anything outside the chart (`href`, `src`, `url(…)`), like hpcheck, and
  hpcheck checks every `svg.hp` in an HTML file as a chart.
- `spec/hpcheck`, a conformance checker for servers in any language: one
  standard-library Python file that runs the universal spec checks and
  each fixture's `checks.json` on a renderer's output, and checks a running
  server's content tags, `hp-w`, gesture parameters and SSE frames over
  HTTP. Every failure names its spec section.
- The Python and Node example tests and the checker's tests run in
  `make test`, `make check` and CI (`make py`, `make nodeex`; a Python
  job on 3.10 and current 3.x, and the Node examples on the client job's
  Node 22 and 24). `make conformance` and a CI conformance job run the
  checker on the Python and Node fixture renderers' output, on the
  gallery's static export, and on the dashboard, Python and Node servers. Docs tests fail when a directory of
  examples or tools gains Python or Node tests, a fixture renderer or a
  checker config that the Makefile's `test` and `check` targets or CI
  never run (matching the commands, not comments or names), and when any
  file mentions an example or tool path that does not exist. Further docs
  tests fail when an example page's htmx tags differ from the dashboard's
  pinned, SRI-hashed ones, when an example is missing from the examples
  indexes or the browser test, and when `make golden` would not rewrite a
  `preview/` directory. `.gitignore` covers `__pycache__/` and the `out/`
  directory the fixture renderers and the gallery export write to.
- A roadmap in `docs/DESIGN.md` listing known gaps: horizontal and
  time-axis bars, a log scale, small multiples, annotations, a locale
  option, browser tests in Firefox and WebKit (the tween has never run in
  a real Safari), and publishing to pkg.go.dev and npm.
- The htmx guide covers error pages that the handler never writes (proxy,
  timeout and middleware errors, which htmx 4 swaps and `hx-ptag` then pins
  in place), `Vary` for URLs that serve a page or a fragment, and autosized
  charts in a filtered area. Package `scale` has runnable examples.

### Changed

These change behaviour of the initial commit on `main`; nothing had been
published.

- **Breaking:** the Go library moved from the repository root to `plot/`. Import
  `github.com/ironystock/hyperplot/plot` and write `plot.Chart`,
  `plot.Stylesheet()` and so on. The wire spec, its fixtures and the
  checker now live together in `spec/` (`spec/spec.md`, `spec/conformance/`,
  `spec/hpcheck/`), and the community files are in `.github/`
  ([ADR 0006](docs/adr/0006-monorepo-layout.md)).
- **Breaking:** the client script is `hx-plot.js` and registers as `plot`
  (was `hyperplot.js`, registered as `hyperplot`). `data-hp-autosize` is
  now `hx-plot:autosize`, and the `hp:resize` event is now
  `htmx:plot:resize`. There are no aliases.
- **Breaking:** `hp-w` and `hp-h` are sent only on requests whose target
  is, or is inside, an `hx-plot:autosize` element (the triggering element
  no longer counts).
- Pages no longer need `"open"` in `morphIgnore`; the extension keeps
  tables' `<details>` open itself.
- Charts write `data-hp="1"` instead of `data-hp=""`, and a CR in element
  text as `&#13;`, so a chart never contains a raw CR byte.
- The root `<svg>` names its title with `aria-labelledby="{id}-title"` and
  its description with a new `aria-describedby="{id}-desc"` (was
  `aria-labelledby="{id}-title {id}-desc"`), so screen readers read the
  description once, as a description. Every chart's markup changes.
- The crosshair and the band highlight (and the new brush rectangle) have a
  1.5px non-scaling edge in the muted text color at full opacity (the
  crosshair was at opacity .6), so they reach 3:1 against the surface
  (WCAG 1.4.11).
- `scale.Linear.Nice` always contains the original domain; float rounding
  could leave the largest point just outside it.
- **Breaking:** `Sparkline` writes `<svg class="hp-spark">` (was
  `class="hp hp-spark"`): every `svg.hp` is a chart under the spec, and a
  sparkline meets none of a chart's contract. The stylesheet themes
  `.hp-spark` itself; page CSS that styled sparklines as `.hp` must use
  `.hp-spark`.
- The default chart id is `"hp-"` and 16 hex digits of a SHA-256 (was 8 of
  a 32-bit FNV-1a, which a chosen series name could make equal to another
  chart's id). Set `Chart.ID` whenever titles or series names come from
  users.

### Fixed

- Live data tables looked stale: they opened at their oldest rows, and
  scroll anchoring kept a scrolled table on the same rows while new ones
  arrived. The extension now follows a table that is scrolled to its end.
- Sliding-window line charts wobbled on every update, each point tweening
  towards its neighbour's value, and tick labels jumped. They now slide.
- A line on a category axis listed categories without a value in its
  table, and a scatter plot's table listed points with a missing y; tables
  now have exactly one row per tooltip row.
- Truncated labels no longer keep a space before the ellipsis ("6:06…",
  not "6:06 …").
- Example dashboard: the streamed throughput chart was never fitted to its
  card and showed scaled down (at half size on a phone); Close and "Back
  to live" no longer drop keyboard focus to the top of the page.
- Example dashboard: the requests chart's window labels repeated "1 AM" on
  a daylight-saving fall-back day at range 6h, so the page answered 500,
  and in half-hour-offset zones they dropped the minutes. Labels now keep
  the minutes and add the zone to wall-clock times that repeat. Back after
  a keyboard zoom no longer drops focus to `<body>`.
- Example dashboard: its legend swatches were `<svg class="hp">`, which
  the spec reserves for charts, so a checker read twelve broken charts;
  they are `hp-spark` now.
- `hx.Brush.TimesIn` read a `datetime-local` wall time in an hour the zone
  skips an hour early (02:10 in New York's gap was 01:10 EST), so a range
  across the gap came back reversed; in zones east of UTC, a repeated wall
  time read as its second instant. It now reads them as browsers and
  Python's `fold=0` do. It also rejects year 0000, like the spec's Python
  helper, which in turn now rejects offset minutes past 59.
- Default tooltip headings and table row headers no longer repeat in the
  hour after the clocks go back: those times gain the zone ("1:30 AM EDT",
  "1:30 AM EST").
- A series name with bytes that are not UTF-8 is keyed by its U+FFFD text,
  like a category, so its id follows the spec and `hp-series` matches it.
- A fixed `Axis.Domain` beyond ±1e300 is rejected as "must be finite and
  within ±1e300" (it said "must be finite" of finite values).

[Unreleased]: https://github.com/ironystock/hyperplot/commits/main
