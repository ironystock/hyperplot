# Changelog

All notable changes to this project are documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).
Before v1.0.0, minor releases may include breaking changes; they are listed
under **Changed** and marked **Breaking**.

## [Unreleased]

Initial development. Nothing has been released yet.

### Added

- `hyperplot.Chart`: server-side rendering of charts to deterministic,
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
- Client extension `hyperplot.js` 0.1.0: tooltips, crosshair, band
  highlight and a ring on the active scatter point, keyboard reading
  announced through a persistent status region, morph-safe hover and focus
  state, and `data-hp-autosize` responsive re-rendering; no dependencies and
  no build step. Package `client` embeds it and serves it with caching
  headers.
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
  serving a page or a fragment from one URL send `Vary`, and that
  `examples/basic` stays identical to the README quick start. `make check`
  runs the same lint, hygiene, golden-file and client checks as CI.
- A roadmap in `docs/DESIGN.md` listing known gaps: horizontal and
  time-axis bars, a log scale, small multiples, annotations, a locale
  option, path tweening in Safari, browser tests in CI, and publishing to
  pkg.go.dev and npm.
- The htmx guide covers error pages that the handler never writes (proxy,
  timeout and middleware errors, which htmx 4 swaps and `hx-ptag` then pins
  in place), `Vary` for URLs that serve a page or a fragment, and autosized
  charts in a filtered area. Package `scale` has runnable examples.

[Unreleased]: https://github.com/ironystock/hyperplot/commits/main
