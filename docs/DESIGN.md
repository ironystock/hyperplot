# hyperplot design

hyperplot is an htmx 4 extension for live, server-rendered SVG charts, and the
Go library that is its reference server. The server renders a chart to SVG;
htmx 4 swaps it into the page and keeps it live through polling, server-sent
events and morph swaps. The extension (`hx-plot.js`, npm `htmx-ext-plot`,
registered as `plot`) adds hover tooltips, a crosshair, keyboard reading,
sliding-window animation, select and brush gestures, and responsive
re-rendering. The Go core uses only the standard library.

Since rev 2 ([ADR 0005](adr/0005-htmx-community-first.md)) the contract
between any server and the extension is [spec.md](spec.md), the wire
specification (spec version 1). It is normative for markup, attributes,
request parameters, events and swap behaviour; where this document and
spec.md overlap, spec.md wins, and a change to one changes the other in the
same commit. This document remains the contract the Go packages and the
reference client are built against: their APIs, layout, visual spec,
palette, CSS, htmx facts and tests. If code and this document disagree, fix
one of them in the same change.

## Goals

1. **Correct and beautiful by default.** The defaults follow established data-viz
   practice (see "Visual spec"), so a chart with no options already looks right.
2. **Hypermedia-native.** The server owns rendering *and* formatting. The
   browser never receives raw data that it has to format; it receives SVG plus
   preformatted tooltip text.
3. **Live without JavaScript frameworks.** Output is deterministic and
   morph-friendly, so `hx-swap="innerMorph"` updates a chart in place and CSS
   transitions animate the change.
4. **Accessible.** Every chart has an accessible name and description, works with
   the keyboard, has a data-table equivalent, and degrades to readable monochrome
   when no stylesheet is loaded.
5. **Safe.** All text, including series names and categories (which are often
   user-controlled), is escaped. Output is always well-formed XML. The client
   inserts text with `textContent` only. No inline event handlers, and CSP-friendly
   (classes plus an external stylesheet; no `style=""` attributes).
6. **Zero dependencies.** The root, `scale`, and `hx` packages and everything in
   `internal/` import only the standard library. The JS client has no runtime
   dependencies and needs no build step.
7. **Any server (rev 2).** The extension's contract with the server is a
   language-neutral wire spec ([spec.md](spec.md)) with conformance
   fixtures, so a Python, PHP or Ruby server can produce the same live
   charts. The Go library is the reference implementation, not a
   requirement.

Non-goals for v0: canvas/raster output, geographic maps, 3D, pie charts (pies are
almost always the wrong form), dual y-axes (never), and charts that need client-side
data processing.

## Module layout

```
github.com/ironystock/hyperplot   (module root, Go ≥ 1.24)
├── *.go                 package hyperplot: Chart, marks, render, theme/CSS, Table, Sparkline
├── scale/               public: Linear, Time, Band scales; ticks; number formatting
├── hx/                  public: htmx 4 server helpers (PTag, SSE stream, partials, sizing)
├── client/              hx-plot.js (the htmx 4 extension, npm htmx-ext-plot), hx-plot.css,
│                        and the Go package embedding the script
├── internal/svg/        deterministic SVG writer + path builder
├── internal/textwidth/  text width estimation
├── examples/dashboard/  runnable live demo: go run ./examples/dashboard
├── examples/basic/      the README quick start: go run ./examples/basic
├── examples/sensors/,   two more live Go demos (SSE tiles; polling, filters, gestures)
│   examples/builds/
├── examples/gallery/    every mark and variant beside the Go code that drew it
│                        (-out DIR writes it as static files)
├── examples/recipes/    one Go pattern each: polling-ptag, sse-stream, filters-form,
│                        select-drilldown, brush-zoom (live); static-page, sizing,
│                        theming, sparkline-table, standalone-svg (static)
├── examples/python/,    Level 3 servers in Python and Node, standard library only
│   examples/node/       (not Go packages; see "Testing standards")
├── tools/hpcheck/       conformance checker for servers in any language (Python)
├── testdata/            golden SVG files (testdata/preview/: standalone copies;
│                        testdata/conformance/: wire-spec fixtures for every language)
└── docs/                spec.md (wire spec), DESIGN.md (this file), adr/, htmx.md guide,
                         documentation tests
```

`internal/` keeps the low-level writer private until its API settles; it can be
promoted later without breaking anything.

## Status of existing code

v0 (everything below) is implemented, documented and tested against this
document. Nothing has been tagged or published yet: there is no module
version on pkg.go.dev and no package on npm. Known gaps and planned work are
listed under [Roadmap](#roadmap).

**Rev 2 (spec 1) is built, and the two reported bugs are fixed; the client
size budget and the release steps are open** (Roadmap, "Rev 2: open" and
"Releasing"). Servers in Python and Node and the checker `tools/hpcheck/`
now show the spec without Go (Roadmap, "Rev 2: open", item 2).
[spec.md](spec.md), [ADR 0005](adr/0005-htmx-community-first.md) and the
fixtures in `testdata/conformance/` were revised after a design review by
three critics (findings and resolutions in the rev-2 workspace notes); the
sections of this document marked "(rev 2)" describe what rev 2 added. The
documentation is repositioned around the extension: the README, the
extension's README, `docs/htmx.md` (patterns 5 to 7) and a server guide,
`docs/implementing-a-server.md`, whose standard-library Python server was
checked against the `line-time` and `empty` fixtures' assertions and run
against the extension in Chrome. Built, to fix the two reported bugs (Roadmap items
1 and 2): the renderer writes `data-hp="1"`, the geometry attributes, row
keys `k`, table and row ids, and leaves points outside the x domain out of
the payload and the table; the client slides, snaps, follows and keeps
tables open in htmx's swap hooks, with
its settings from `htmx.config.plot` and `hx-config`; the dashboard draws
one bucket of overscan; and `client/browser/` holds real-browser
regression tests for both bugs. The Go reference server is now complete
for spec 1 (Level 3): CR in element text is written as `&#13;`, the
stylesheet has the spec-1 additions, `hx` reads the gesture parameters
(`Selected`, `Brushed`), and `TestConformance` reproduces every reference
file of `testdata/conformance/` byte for byte and passes every server
assertion. The client is built too: `client/hx-plot.js` registers `plot`
and starts from `init` (honouring `htmx.config.extensions`), reads the
container attributes `hx-plot:autosize` and `hx-plot:brush` in every htmx
spelling, dispatches `htmx:plot:*` through `triggerHtmxEvent`, selects on
click, Enter and assistive-technology clicks, and adds the gesture
parameters; brushing is in the companion `hx-plot-brush.js` and the Safari
path tween in `hx-plot-tween.js` (see "Client extension"). Still open on
the client side: the core is over its size budget (see "Size budget").

- Root package (`hyperplot`): complete. `chart.go` (API), `build.go`
  (validation, slots, keys, stacking, extents), `layout.go`, `legend.go`,
  `axes.go`, `curve.go`, `marks_render.go`, `labels.go`, `hover.go` +
  `tipsjson.go`, `desc.go`, `format.go`, `theme.go`, `table.go`,
  `sparkline.go`, `render.go`. Covered by 15 golden files (plus 6 standalone
  previews), unit and robustness tests, the well-formedness fuzz test,
  `Example*` functions and benchmarks (see "Testing standards").
- `scale/`: done and tested. `Linear` (Map/Invert/Ticks/TickStep/Nice), `Ticks`,
  `TickStep`, `Time` (Map/Invert/Ticks/TickFormat/TickFormatFor/Format/Formatter/FormatterFor,
  US calendar: Sunday weeks, 12-hour clock), `Band` (Map/MapIndex/Index/Bandwidth/Step), `FormatNumber`,
  `FormatCompact`, `FormatExponent`, `TickFormat`, and `Minus` (U+2212). Negative numbers use the
  typographic minus. The tick interval type is internal: no exported API takes
  or reads one, and `TickFormatFor`/`FormatterFor` cover what callers need.
  - **Time ticks** step from 1 ns (1-2-5 steps through 500 µs, then 1-2-5 ms
    steps up to 500 ms) to 1-2-5 multiples of years; spans are measured in
    float seconds, so domains longer than `time.Duration`'s ~292 years still
    get sensible steps. `Map` measures seconds from `Domain[0]` (whole seconds
    and nanoseconds subtracted separately), so precision follows the span,
    not the distance from 1970: float64 Unix seconds resolve only ~240 ns in
    2026, which collapsed 100 ns data into a few columns. Ticks are
    DST-safe: day and coarser steps use calendar arithmetic, and hour steps
    advance through local hours keeping those that are multiples of the step
    (d3's `timeHour.every`), so a 6-hour axis reads 12 AM, 6 AM, 12 PM after a
    clock change and still lands on local midnight. Sub-day floors subtract
    the local remainder in absolute time instead of rebuilding the wall
    time, which in the repeated hour after a fall-back resolved to the
    earlier occurrence and started every tick an hour before the domain;
    `Ticks` also advances until the first tick is inside the domain. 2-day
    steps restart on the 1st of each month (d3's `timeDay.every`: Jan 29,
    Jan 31, Feb 1, Feb 3), so a tick's date depends only on the calendar and
    labels stay put as a window slides across a month end. Millisecond ticks
    read ".100", finer ticks show six or nine digits (".123450"); year labels
    are unpadded ("50", not "0050"). Full labels (`Format*`) follow the tick
    step down to nanoseconds, and `FormatterFor` goes finer when the data
    needs it. A zero-span domain (a single instant, the first sample of a
    live chart) is labeled at the precision its own time needs, never coarser
    than a day ("Mar 3, 2026 2:00 PM", not "…2:00:00.000 PM").
  - **Number ticks** (`TickFormat`) switch to scientific notation with one
    power of ten from the largest tick when it reaches 1e15 or is under 1e-4
    (`0.5×10¹⁸`, `1.5×10⁻¹⁰`), and choose decimals against the tick spacing,
    so ticks never share a label. `FormatCompact` also uses scientific notation
    from 1e15 up, where a suffix would leave long digit runs ("1,000,000T").
- `internal/svg/`: done, tested, fuzzed. `Writer` (Open/Void/Close/Elem/Text/Raw/
  RawBytes/Grow), `A`/`N`/`Flag` attribute helpers (`Flag` writes `name=""`),
  `Num` (≤2 decimals, trims zeros, −0→0, non-finite→0; an exact integer fast
  path), and `Path` (M/L/H/V/C/Q/A/Z; `A` is the elliptical arc used for rounded
  bar corners, and a zero radius draws a straight line).
- `internal/textwidth/`: `Width(s, fontSize)` and `Truncate`, using Helvetica/Arial
  advance widths. Tested and fuzzed.
- `data.go`: `Values` interface (`Floats`, `Times`, `Categories`), generic `Map`, and
  `valid()`. A value is missing when it is NaN, ±Inf, or beyond ±1e300 (scale
  arithmetic on spans near ±MaxFloat64 overflows).
- `marks.go`: `Mark` (sealed; pointers such as `*Line` are accepted too), `Curve`
  (Linear/Monotone/Step), `Line`, `Area`, `Bar`, `Dot`, `RuleY`, and the internal
  `series` view.
- `hx/`: done and tested (see "htmx 4 integration"). The SSE framing tests
  parse `Stream` output with `sseparse_test.go`, a Go port of `parseSSE` from
  hx-sse 4.0.2 made by hand; it has not been checked mechanically against the
  JavaScript original. `TestStreamOutlivesWriteTimeout` and
  `TestStreamDisconnectOverHTTP` depend on timing (about 360 ms of sleeps,
  with a 15 s safety timeout) and could be slow on a heavily loaded machine.
- `client/`: `hx-plot.js` 0.2.0 (npm `htmx-ext-plot`), its companions
  `hx-plot-brush.js` and `hx-plot-tween.js`, and the Go package that embeds
  and serves all three. `node --test` covers the pure helpers, the
  conformance fixtures' client expectations and, through `fakedom.mjs`, the
  extension wiring; `client_test.go` covers the handlers. Real-browser
  tests in `client/browser/` drive Chrome (see "Testing standards") and run
  in CI (job "Browser tests", ubuntu-latest's preinstalled Chrome); Firefox
  and Safari are untested (see "Roadmap").
- `examples/dashboard/`: done, on the rev 2 names (`hx-plot:autosize`,
  `htmx:plot:resize`, `/static/hx-plot.js` and `hx-plot-brush.js`), with
  the brush-to-zoom demo on the latency chart (pattern 6: `hx-sync` pair,
  outerMorph of the polling element, `HX-Push-Url`, a `datetime-local`
  from/to form, "Back to live") and the drill-down demo on the requests
  bars (pattern 5: `hx.Selected`, window links as the plain-HTML
  equivalent, Close), each with a polite live region. `main_test.go`
  exercises every endpoint and the gesture parameter parsing;
  `client/browser/dashboard.browser.mjs` drives both gestures in Chrome. It
  carries the workarounds for the htmx 4.0.2 extension bugs listed in the
  pitfalls under "htmx 4 integration" below.
- `examples/basic/`: the README quick start. `docs/quickstart_test.go` fails
  when the two differ (other than the example's package comment).
- `examples/sensors/` (SSE tiles, select, brush, threshold rules, a live
  alerts table) and `examples/builds/` (polling with `hx-ptag`, a scatter
  with brush, stacked bars with select, a filter form): done, each with a
  `main_test.go` in the dashboard's style. `examples/gallery/` shows every
  mark and variant (23 cards) beside the source of the function that drew
  it, parsed from the embedded `charts.go` with `go/parser` at startup and
  looked up by the function actually called, so code and chart cannot
  drift; `-out DIR` writes it as static files with a `<meta>` CSP.
  `examples/recipes/` holds ten one-file Go recipes: five live patterns
  (polling-ptag, sse-stream, filters-form, select-drilldown, brush-zoom,
  htmx guide patterns 1 to 6) and five static ones (static-page, sizing,
  theming, sparkline-table, standalone-svg, each with preview SVGs its test
  keeps current and `make golden` rewrites). `examples/README.md` and
  `examples/recipes/README.md` index them (`docs/examples_test.go` fails
  when one is missing), and `client/browser/examples.browser.mjs` opens
  every example in Chrome.
- `examples/python/` and `examples/node/`: Level 3 servers on each
  language's standard library, with the same URLs, tested by `unittest`
  and `node --test` (see "Testing standards").
- `tools/hpcheck/`: the conformance checker in standard-library Python
  (universal checks, fixture assertions, live server checks), with its own
  `unittest` suite that mutates valid charts to prove each check fails.
- `docs/`: `snippets_test.go` and `config_test.go` check the documentation's
  samples (see "Testing standards").

## Public API (package hyperplot)

```go
type Chart struct {
    ID          string  // DOM id; unique on the page, stable for live updates. Default: see below.
    Title       string  // accessible name (<title>); shown visibly only if ShowTitle or Standalone
    Description string  // accessible description (<desc>); auto-generated summary if empty
    Width, Height float64 // default 640 × 320; minimum 50 each (smaller → error)
    FontSize    float64 // default 12
    X, Y        Axis
    Marks       []Mark
    Stack       bool    // stack Bars (and Areas) instead of grouping/overlapping
    Legend      LegendMode // LegendAuto (default: shown for ≥2 series), LegendShow, LegendHide
    Animate     bool    // add the "hp-anim" class so CSS transitions animate morph updates
    ShowTitle   bool    // draw Title as visible text at the top-left
    Standalone  bool    // self-contained file: embed <style>, draw a background, imply ShowTitle
    Theme       *Theme  // used only when Standalone; nil means DefaultTheme()
    Location    *time.Location // time axis zone; nil = UTC
    BarWidth    float64 // max bar thickness in px; default 24
}

type Axis struct {
    Label  string                 // axis title. Y label is drawn horizontally above the plot, top-left.
    Unit   string                 // appended to tooltip/table values, e.g. " ms" (not to ticks); shortened past 32 runes there
    Ticks  int                    // approximate tick count; 0 = auto from length (Y: h/50, X: w/90)
    Format func(float64) string   // numeric tick + value formatter; nil = scale.TickFormat for ticks, smart format for values
    TimeFormat func(time.Time) string // time tick formatter only; nil = scale.Time.TickFormatFor (12-hour, "6 AM")
    TimeValueFormat func(time.Time) string // full time label (tooltips, table, description); nil = scale.Time.FormatterFor
    Domain [2]float64             // fixed numeric domain; used when Domain[0] != Domain[1]
    TimeDomain [2]time.Time       // fixed time domain; used when both are non-zero
    Zero   bool                   // always include 0 (bars and areas always do)
    Hide   bool                   // hide labels (gridlines stay)
    NoGrid bool                   // Y only: hide the horizontal gridlines
}

func (c *Chart) Render() ([]byte, error)
func (c *Chart) WriteTo(w io.Writer) (int64, error)     // io.WriterTo
func (c *Chart) HTML() (template.HTML, error)           // for html/template
func (c *Chart) Component() Component                    // satisfies templ.Component structurally:
//   type Component interface{ Render(ctx context.Context, w io.Writer) error }  (no templ import!)
func (c *Chart) Table() (template.HTML, error)          // accessible <table> equivalent of the data
func Stylesheet() string                                 // DefaultTheme().CSS()
type Palette struct { Surface, Text, TextMuted, Grid, Axis string; Series [8]string }
type Theme struct { Light, Dark Palette }
func DefaultTheme() Theme; func (t Theme) CSS() string
type Sparkline struct { Values []float64; Width, Height float64; Label string; Area bool; Unit string; Format func(float64) string }
func (s Sparkline) Render() ([]byte, error)
func (s Sparkline) WriteTo(w io.Writer) (int64, error)  // io.WriterTo
func (s Sparkline) HTML() (template.HTML, error)        // for html/template, so callers never cast
func (s Sparkline) Component() Component
var ErrTooManySeries error  // >8 series: fold into "Other" or use small multiples

type Mark interface{ /* sealed */ }    // Line, Area, Bar, Dot, RuleY, or pointers to them
type Line struct { Name string; X Values; Y []float64; Curve Curve; Slot int; Points bool }
type Area struct { Name string; X Values; Y []float64; Curve Curve; Slot int; Base []float64 }
type Bar struct { Name string; X Categories; Y []float64; Slot int }
type Dot struct { Name string; X Values; Y []float64; Slot int; Labels []string }
type RuleY struct { Y float64; Label string }
type Curve int        // CurveLinear (default), CurveMonotone, CurveStep
type LegendMode int   // LegendAuto (default), LegendShow, LegendHide
type Values interface{ Len() int /* sealed */ } // Floats, Times, Categories
type Floats []float64; type Times []time.Time; type Categories []string
func Map[T, V any](data []T, fn func(T) V) []V  // row-oriented records → a column
type Component interface{ Render(ctx context.Context, w io.Writer) error }
```

`Render` returns errors, never panics, for: mismatched X/Y lengths (also `Area.Base`
and `Dot.Labels` against `Y`, and a nil `X` with values), X kinds mixed across marks,
a `Bar` with an empty `X` or a repeated category (it would need two ids), a slot
outside 0–8 or two series pinned to the same slot, an unknown `Curve`, a nil mark,
more than 8 series (wrapping `ErrTooManySeries`), a non-finite fixed `Domain`, a
time in `Times` or a used `TimeDomain` outside years 1 to 9999 in UTC (its row key
or `data-hp-x` end could not be an RFC 3339 time value, spec.md §1.1, nor parsed
back by `hx.Selection.Time` or Python's `datetime`), and
sizes out of range: `Width`/`Height` in [50, 32768], `FontSize` in [4, 96],
`BarWidth` in (0, 32768] (0 means the default everywhere). An explicit `ID` may
contain only ASCII letters, digits, `-`, `_`, `:` and `.`, because it is used in
`url(#…)` references. Validation messages name the mark by index and name.
A category's bytes that are not valid UTF-8 each become U+FFFD before
anything else sees them (copying, never modifying, the caller's slice), so
categories are told apart, keyed and checked for repeats by the text the
metadata and the client carry: `"a\xff"` and `"a\xfe"` are one category (a
`Bar` listing both is a repeated category). Before, they passed as two and
wrote two rows with the same key, which spec.md §4.3 forbids. Series names
get the same treatment (and are then compared, keyed and written as that
text): a series named `"a\xff"` was keyed from its raw bytes while
`data-hp-series`, the JSON and a select's `hp-series` carried `a` + U+FFFD,
so its id disagreed with the spec's derivation and a handler matching
`hp-series` against its names never found it (`TestSeriesNamesInvalidUTF8`).

Unnamed series are shown as "Series N" (N = position among data marks, from 1) in
the legend, tooltips, table, description and `data-hp-series`. Theme color values
are sanitized in `CSS()`: only characters CSS colors need survive, so a theme can
never break out of its declaration, and a value must be shaped like a color (#hex
of 3/4/6/8 digits, one keyword, or a call of the color functions, `var`, `calc`,
`min`, `max` or `clamp`, nested only within that list). Anything else, notably
`url()` and `image-set()`, which would make every viewer fetch a URL, is replaced
by the default theme's value for that token.

The default `ID` is `"hp-"` + the first 16 hex digits of the SHA-256 of the
Title, Description, axis labels and every series' kind and name (it was the
Title alone, which gave every untitled chart the same id, and then the 8-hex
FNV-1a of these, which a meet-in-the-middle search inverts in milliseconds: a
user who could name a series found a name that gave their chart a victim
chart's id, and with it the victim's accessible name, description and clip
path; `TestDefaultIDResistsChosenNames`). It does not depend on data, size or
styling, so a chart keeps its id across live updates and autosizing. Charts
that agree on all of those inputs (untitled charts of unnamed series, one chart
rendered for two filters, or a user copying another chart's title and names)
still collide, and duplicate ids cross-wire `aria-labelledby`, `clip-path` and
the client's lookup; the `ID` godoc says to set an ID for them, for every live
chart, and whenever the hashed inputs can come from users. Element keys
(series, bars, rows) stay 32-bit FNV-1a (spec.md §4.1): they are scoped to a
chart's own id, so a forced key collision only reaches the `#n` rehash.

`Render` must be safe to call concurrently on different `Chart`s and must not mutate
the `Chart` or its slices.

## Output structure (the DOM contract)

The client extension, the CSS, and the tests all rely on this structure. Change it
only deliberately. The wire-level requirements (what any server must emit)
are in [spec.md §3–4](spec.md#3-the-chart-document-level-1); this section is
the Go renderer's exact output, which meets Level 3.

```xml
<svg xmlns="http://www.w3.org/2000/svg" id="{ID}" class="hp [hp-anim]"
     viewBox="0 0 W H" width="W" height="H" role="img"
     aria-labelledby="{ID}-title" aria-describedby="{ID}-desc" tabindex="0" data-hp="1"
     data-hp-plot="L T R B" [data-hp-x="{kind} [d0 d1]"] data-hp-y="linear y0 y1"
     font-family="system-ui, -apple-system, 'Segoe UI', sans-serif" font-size="12">
  <title id="{ID}-title">…</title>
  <desc id="{ID}-desc">…</desc>
  [<style>…theme CSS…</style><rect class="hp-bg" …/>]        (Standalone only)
  [<text class="hp-title" …>]                                (ShowTitle)
  <defs><clipPath id="{ID}-clip"><rect …plot area…/></clipPath></defs>
  <g class="hp-legend" aria-hidden="true">…</g>               (when shown)
  <text class="hp-axis-label hp-ylabel" …>…</text>           (Y.Label)
  <g class="hp-grid" aria-hidden="true"><line …/>…</g>
  <rect class="hp-band-hl" visibility="hidden" …/>            (charts with bars: hover band highlight)
  <g class="hp-marks" clip-path="url(#{ID}-clip)">
    <g id="{ID}-s-{key}" class="hp-series hp-s{slot}" data-hp-series="{name}">
      <path class="hp-area" …/> <path class="hp-line" …/> <path class="hp-bar" id="{ID}-b-{key}-{catkey}" …><title>…</title></path>
      <circle class="hp-dot" …><title>…</title></circle>
    </g>
  </g>
  <g class="hp-axis hp-axis-x" aria-hidden="true">baseline + tick labels</g>
  <g class="hp-axis hp-axis-y" aria-hidden="true">tick labels</g>
  <g class="hp-rules" aria-hidden="true">…</g>
  <g class="hp-labels" aria-hidden="true">direct labels, end dots</g>
  <g class="hp-hover" aria-hidden="true">
    <line class="hp-crosshair" visibility="hidden" …/>
    <metadata class="hp-tips">{JSON}</metadata>
    [<circle class="hp-focus" r="7" …/>]                     (added by the client in mode xy, never by the server)
  </g>
</svg>
```

- **Stable keys.** `{key}` is the 8-hex FNV-1a hash of the series name (or of
  `"#"+index` when unnamed); `{catkey}` is the same hash of the category. Stable ids
  let htmx's morph match nodes across updates, so bars animate rather than being
  replaced. `uniqueKey` (build.go) assigns keys in order within a set (series
  in mark order; categories in first-appearance order) and rehashes
  `s + "#" + n` for the smallest free `n` when a hash is taken, so two
  series named `p50` get distinct ids; spec.md §4.1 makes this the
  RECOMMENDED rule for every server (the `collisions` fixture).
- **Spec-1 renderer changes (rev 2).** Besides the geometry attributes and
  row keys below: a point whose x lies outside the x scale's domain (the
  overscan point of a sliding window) is drawn, clipped, but gets no column
  or row in the payload and no row in the table (spec.md §3.5, §3.8;
  `xColumns` in hover.go and `xInDomain`); and a CR in element text is
  written as `&#13;` by `internal/svg` (it was already a character
  reference in attributes), so output never contains a raw CR byte
  (spec.md §3.6). No element of the output carries a `transform`
  attribute, which spec.md §3.3 requires for the root, the marks, labels,
  x axis and hover groups. The table has exactly one body row per payload
  row (spec.md §3.8): a line on a band axis (mode `x`) lists only the
  categories where some series has a value, as its payload does, and a
  point table leaves out points with a missing y, which are not plotted.
  `scale.Linear.Nice` never returns a domain that leaves out part of the
  original one (float rounding in the step arithmetic could nice
  `-0.19999999999999998` to `-0.2`, which put the largest point outside
  `data-hp-x` while it still had a payload row).
- **Geometry attributes (rev 2).** `data-hp="1"` is the wire-spec version
  (it was the empty flag `data-hp=""` in v0). `data-hp-plot` is the plot
  rectangle `svg.Num(p.l) svg.Num(p.t) svg.Num(p.r) svg.Num(p.b)` (the
  unpadded plot, the same numbers as the grid's `x1`/`x2` and the payload's
  `top`/`bottom`). `data-hp-x` is `time {ms0} {ms1}` (Unix milliseconds of
  the time scale's domain), `linear {d0} {d1}` (the linear scale's domain,
  after `Nice`) or `band`; it is omitted when the chart has no x values
  (`xNone`). `data-hp-y` is `linear {y0} {y1}`, the y scale's domain. Domain
  numbers are written in full, not through `svg.Num`, with
  `strconv.AppendFloat(b, v, 'f', -1, 64)` when `1e-6 ≤ |v| < 1e21` and the
  `encoding/json` exponent form otherwise (spec.md §1.1 "number"; zero is
  `0`). Unix milliseconds are `float64(t.Unix())*1000 +
  float64(t.Nanosecond())/1e6`, exact for whole milliseconds. These let the
  client tell a sliding window from a change of values, and turn a brush
  into data values.
- **Determinism.** The same input gives byte-identical output: no map-iteration
  order, no time-dependent values, and numbers through `svg.Num` (domain
  numbers in the geometry attributes excepted, which are written in full).
- **Graceful degradation.** Marks carry presentation attributes in terms of
  `currentColor` (lines: `fill="none" stroke="currentColor" stroke-width="2"`; grid:
  `stroke="currentColor" stroke-opacity="0.12"`; muted text: `fill="currentColor"
  fill-opacity="0.7"`; area: `fill="currentColor" fill-opacity="0.1"`). With no
  stylesheet the chart is legible in monochrome; the stylesheet overrides these with
  theme colors. **Do not** use `style=""` attributes (CSP).
- **Tooltip payload.** `<metadata class="hp-tips">` holds JSON, escaped exactly as
  `encoding/json` escapes it (`<>&` become `\u003c` etc., U+2028/9 and invalid
  UTF-8 are handled), plus `\ufffe`/`\uffff`, the only characters valid UTF-8 can
  carry that XML forbids, so the JSON is written raw into the XML. It is encoded by
  hand (`tipsjson.go`) because reflection-based `encoding/json` dominated render
  time; tests compare it with `encoding/json` byte for byte and fuzz it.
  Coordinates are in viewBox units, rounded to 2 decimals.
  ```json
  {"mode":"x",  "xs":[x0,x1,…], "bw":0, "top":T, "bottom":B, "rows":[{"t":"Mar 3, 14:05","k":"2026-03-03T14:05:00Z","v":[[slot,"name","12.3 ms"],…]}]}
  {"mode":"band","xs":[centers…], "bw":step, "top":T, "bottom":B, "rows":[…]}
  {"mode":"xy", "pts":[[x,y],…], "rows":[{"t":"label or x value","k":"x key","v":[[slot,"name","y value"]]}]}
  ```
  **Row keys (rev 2).** Every row carries `k`, written after `t`: the key of
  the row's x value (spec.md §1.1): the time as
  `t.UTC().Format(time.RFC3339Nano)`, a linear x in the spec's number form
  (as for `data-hp-x`), or the category. In mode `xy` it is the point's x.
  The client keeps a keyboard reader on the same row by `k` and sends it
  back with `htmx:plot:select`, so a server can match it exactly.
  Mode `x` covers line/area charts (crosshair snaps to the nearest x). Mode `band`
  covers charts with bars (the band under the pointer is highlighted and every
  series is listed). Mode `xy` covers dot-only charts (nearest point in 2-D,
  hit radius ≥ 12 px). Row `v` lists **every** series at that x, in series order,
  with `"–"` for missing values. A series with several points at one x (repeated
  observations in a scatter beside a line) lists every value in data order, joined
  by `", "` (`"8, 10, 12"`), here and in the table. The series name in `v` (and in
  each bar's and dot's `<title>`) is shortened with an ellipsis past 100 runes,
  because it repeats once per point; the legend, table header, description and
  `data-hp-series` carry it whole. Likewise `X.Unit` and `Y.Unit` are shortened
  past 32 runes where they follow each value (tooltips, titles, table cells);
  the description carries them whole. A labeled Dot in mode `x` or `band`
  adds its labels at that x after its series name (`[2,"deploys · v2.1
  rollout","30"]`), so the tooltip and keyboard reading carry them.
  Values are formatted server-side with the Y
  formatter plus `Y.Unit`; with no formatter, about four significant digits
  (`0.01235`, `12.35`, `123.5`, `1,235`, `12,345`, `575.4K`, `1.25M`). From
  100,000 up every value is compact at four significant digits, so values of
  different sizes in one tooltip read alike (`1.104M`, `575.4K`) rather than
  mixing `1.1M` with `575,359`. From 1e15 up and under 1e-4 values use scientific
  notation like the ticks (`3.142×10¹⁸`, `1.23×10⁻⁵`). Mode `x` lists only x values
  where at least one series has a value, sorted along the axis; a line over a
  band axis uses mode `x` with band centers. Full time labels (tooltip `t`,
  table rows) use the tick interval's precision, made finer when the data
  needs it (`scale.Time.FormatterFor`), so an hour of 30-second buckets reads
  "2:00:30 PM" rather than two rows of "2:00 PM"; `X.TimeValueFormat` replaces
  them (and the description's times) when set. Numeric x values (tooltip `t`,
  table rows, the description's X range) use the four-significant-digit value
  format unless it would print two distinct x values alike; then every x value
  is written in full with enough decimals to tell the closest pair apart
  (`100,001`, `1,001.2`), or as the shortest round-trip form for float noise.
  The description's Y range gets the same treatment for its two end points. In
  mode `xy`, `t` is the point's label followed by its x value (`"GET /users ·
  512 KB"`), or the x value alone when unlabeled, so the x half of a scatter
  point is never lost; `v` holds only that point's series.
- **No-JS fallback.** Each bar and dot has a `<title>` child (native browser
  tooltip): `"{series} · {x label}: {value}"`, where a labeled Dot reads
  `"{series} · {label} · {x label}: {value}"`.
- **Text in `hp-labels`.** Direct labels are `<text class="hp-label hp-direct">`,
  Dot labels `<text class="hp-label hp-dot-label">`, and end dots
  `<circle class="hp-end hp-s{slot}">`. Tick labels are `text.hp-tick`, legend
  entries `g.hp-legend-item` holding a key (`.hp-key.hp-s{slot}`), a
  `text.hp-legend-label` and, when the name was truncated, a `<title>` with the
  full name. Rules are `line.hp-rule` plus `text.hp-rule-label`, and the baseline
  is `line.hp-baseline` inside `hp-axis-x`.

## Layout

All in viewBox pixels. FontSize f = 12 by default.

- **Top:** visible title row (if shown), then legend rows (each f+6 px, wrapped to
  width), then the Y-label row (if set), then 8 px of padding. These header rows
  may take at most half of the height left after the X-axis rows (less the
  padding), so a short or narrow chart with many series keeps a plot. Rows that do
  not fit are dropped whole: first the legend (after retrying with names
  truncated to 60 px), then the Y label, then the visible title. The tooltip,
  table and description still name every series.
- **Left:** the widest Y tick label (textwidth estimate) + 8 px, at least 24 px.
  Y tick labels are right-aligned, `dy="0.32em"`, in muted ink.
- **Bottom:** f + 10 for X tick labels, plus f + 8 for an X label (right-aligned
  under the plot end).
- **Right:** if direct end labels are drawn, the widest label + 14; otherwise enough
  room for the last X tick label's half-width overflow, at least 12.
- **Y ticks:** `Ticks` or `max(2, round(plotH/50))`. The linear Y domain is the
  extent of every y, base, stacked total and RuleY value. Include 0 if any bar or
  area is present or if `Y.Zero` is set; then `Nice()`. A fixed `Domain` overrides
  this. With no finite data the domain is [0, 1].
- **X:** linear → extent then `Nice`; time → exact extent (or `TimeDomain`),
  then drawn through its ends as float64 epoch ms, rounded outward to the
  nearest representable numbers (`timeWire`), which is what `data-hp-x`
  writes, so the attribute describes the drawing exactly (spec.md §4.2). Ends
  at whole milliseconds are exact and unchanged; a domain a few µs wide
  widens by up to one float64 step at each end (about 0.24 µs near 2026), so
  100 ns data fills about half the plot rather than all of it. `scale.Time`
  over the exact extent still places and labels the ticks. A single instant
  is drawn mid-plot and written widened by 1 s each way; band → categories in order of first appearance across marks. Band X labels thin
  out to every k-th label when they would collide, and lines and dots on a band
  axis are placed at band centers.
- **Crisp lines:** 1 px gridlines and baselines sit on half-pixels (`round(y)+0.5`).
- **Baseline:** draw a 1 px axis-color line at y=0 if 0 is in the domain and any bar
  or area exists, otherwise at the plot bottom. The gridline it covers is skipped.
- No tick marks; the gridlines and labels are enough.

Details settled during implementation:

- **Standalone** charts get a 16 px margin on every side (the title, legend and
  axes sit inside it) because they are drawn on their own background.
- **Title row** is `round(1.25·f)` px text plus 10 px. Long titles, legend names
  (max 200 px), axis labels, rule labels, band labels (max 160 px) and direct
  labels (max a quarter of the width) are truncated with an ellipsis (spaces
  before it are dropped: "6:06…", not "6:06 …"). When
  band labels would need thinning but truncating each to its band step (less
  the label gap, and at least 40 px) lets every band keep a label, they are
  truncated to the step instead, so one long category cannot hide its
  neighbors. They are never both truncated to the step and thinned.
- **Time tick labels** use `scale.Time.TickFormatFor(count)`: when ticks are
  finer than a month, the first of a month reads "Mar 1", not "Mar", which
  among "Mar 3, Mar 5" looked like a label missing its day.
- **Empty charts** (no series with a valid value) draw "No data" in muted ink
  at the plot's center, `text.hp-label.hp-empty` in `hp-labels`.
- **Hidden Y labels** leave an 8 px left margin. The left margin is capped at half
  the width and the right margin at a third, so tiny charts still have a plot.
- **X label thinning** applies to every x kind, not only bands: keep every k-th
  label for the smallest k at which kept labels are at least `max(6, f)` px apart.
  Edge labels whose centered box would leave the chart are anchored `start`/`end`.
  The right margin is measured in a first pass and widened (up to a quarter of the
  width) so the last tick label usually fits centered.
- **Degenerate domains:** a span narrower than 1e-12 of the values' magnitude is
  flat; a flat domain is padded by 10% of its value (±1 at zero), so a flat series
  sits mid-plot. All-zero bars or areas get [0, 1]. A failed `Nice` keeps the raw
  domain.
- **Clip path:** the clip rect is the plot area grown by 6 px on each side, so dots
  and round caps on the plot edge are not cut in half. Marks outside a fixed domain
  may show 6 px past the plot edge.
- **Pixel clamp:** mapped coordinates are clamped to ±1e6 px so far-out values in
  a fixed domain cannot produce huge numbers.

## Visual spec (from data-viz practice; non-negotiable defaults)

- **Marks:** lines 2 px with round joins and caps. Bars are at most `BarWidth`
  (24 px) thick, never filling the band, with a 4 px rounded data end and a square
  end at the baseline (a path with arcs; for negative values the rounded end points
  down). Grouped bars have a 2 px gap; stacked segments have a 2 px surface gap and
  only the outermost segment is rounded. Dots have r = 4 with a 2 px ring in the
  surface color. The area fill is a wash at 10% opacity of the series color with the
  2 px line on top.
- **End dot:** every line and area gets an r=4 dot on its last valid point
  (surface ring), drawn in `hp-labels` so the clip never cuts it. With
  `Line.Points`, every point gets an `hp-dot` (with a `<title>`) in the series
  group instead, and no separate end dot is drawn.
- **Gaps:** a missing value breaks a line or area. An isolated point between gaps
  is a zero-length segment, which the round cap draws as a 2 px dot. Monotone
  curves are computed per unbroken run, in pixel space (d3 `curveMonotoneX`).
- **Stacking** matches points by x value. Positive bar values stack upward and
  negative values downward from 0; areas stack cumulatively in mark order, and
  `Area.Base` is ignored when stacking. An area stacks only on earlier series:
  two of its own points at one x (a vertical step, a duplicate timestamp) both
  stack on the earlier series, and later series stack on its last value
  there. The gap between stacked bar segments is
  geometry (the inner end moves 2 px), not a stroke.
- **Gridlines:** 1 px, solid (never dashed), recessive. The axis or baseline is one
  step darker.
- **Color:** categorical slots 1–8 in **fixed order, never cycled**. A 9th series is
  an error (`ErrTooManySeries`). Color follows the entity: `Slot` pins it. Text
  never wears a series color; it uses the text tokens. Identity comes from a key
  (swatch, line key, or dot) next to the text.
- **Legend:** shown for ≥2 series (none for 1, because the title names it). Placed
  in a top-left row that wraps. The key mirrors the mark: a short 2 px line for
  lines, a 10×10 rounded rect for bars and areas, a dot for dots. Names use
  secondary ink.
- **Direct labels:** for 2–4 line/area series, label each series name at its last
  point (x + 8). If any two labels would be within f+2 px vertically, draw **no**
  direct labels (never nudge them apart); the legend and tooltip carry identity.
  Labels are used only when every series is a line or area and every one has a
  last point inside the plot, vertically and horizontally (a fixed `Domain` or
  `TimeDomain` can leave it outside; the check uses the domain, because the right
  margin the labels need is chosen before the x scale exists). The legend stays (LegendAuto) even when direct labels
  are drawn, so the layout does not jump when a live update toggles them.
- **Dot labels:** tried right of the point (x + 7), then left, then centered
  above, then centered below; the first position that stays inside the plot
  (horizontally; vertically within the clip padding) and overlaps no placed
  label and no dot (r = 4) wins. A label that fits nowhere is skipped (greedy,
  in mark order). Its full text stays in the tooltip and table. Staying inside
  the plot keeps labels out of the Y tick column; avoiding dots means a label
  never hides another point. Placed boxes live in a uniform grid (at most
  256 × 256 cells), so placement stays near linear: 50,000 labeled dots on a
  4096 × 4096 chart render in about 0.15 s. Clustered dots are bounded too:
  dot obstacles are centered on the nearest whole pixel and grown by half a
  pixel (so each still covers its dot), dots on the same pixel are one
  obstacle, and a cell holding 64 boxes counts as full (no label fits there
  at the default size). 80,000 labeled dots on one point took 15 s when each
  label scanned the whole pile. The regression test counts the box
  comparisons the renderer's own grid makes.
- **Label halo:** `.hp-label` and `.hp-rule-label` get a 3 px stroke in the
  surface color painted under the fill (`paint-order: stroke`, CSS only), so a
  line passing behind a label (a spike through a rule label) does not make it
  illegible.
- **RuleY:** 1 px solid line in primary ink at 50% opacity, with its label above the
  right end in secondary ink.
- **Typography:** system-ui stack, with no display or serif faces. Axis ticks use
  `font-variant-numeric: tabular-nums` (via CSS).
- **One axis.** There are no dual y-axes, ever.

### Default theme tokens (validated palette)

| token | light | dark |
|---|---|---|
| surface | `#ffffff` | `#191919` |
| text (primary ink) | `#191919` | `#efefef` |
| text-muted (secondary ink) | `#666666` | `#9c9c9c` |
| grid | `#e7e7e7` | `#303030` |
| axis/baseline | `#c2c2c2` | `#474747` |
| series 1 blue | `#257de0` | `#338ced` |
| series 2 orange | `#f47230` | `#e76624` |
| series 3 aqua | `#00b894` | `#00a888` |
| series 4 yellow | `#f2a600` | `#d28e05` |
| series 5 magenta | `#ee7fac` | `#e24d8b` |
| series 6 green | `#008a17` | `#0aac23` |
| series 7 violet | `#6a4ce2` | `#9887ef` |
| series 8 red | `#ec4647` | `#eb6263` |

The order is the colorblind-safety mechanism; don't reorder it. For scatter plots,
more than 3 series fails the all-pairs color-vision checks, so document this in
godoc. Status colors (good `#0aac23`, warning `#fbb315`, serious `#f18a59`, critical
`#dc3c3d`) belong to the page (deltas, badges), never to chart series; hyperplot
itself never draws them. They are chosen to differ from the series colors at a
glance, with one exception inherited from the validated palette: status good is
the same green as **dark series 6** (`#0aac23`), and in light mode it sits close
to series 6 too. (This document used to call the status colors "reserved and
never used as series colors", which the palette contradicted.) So a status color
never carries meaning by hue alone: pair it with an icon and a label, and keep a
same-hue status cue away from a series in that slot (on a dark page, slot 6 for
good; slots 2 and 8 sit near serious and critical). A page's own dark-mode
status colors must not reuse a series token either. `TestStatusColorsDifferFromSeries`
fails if any other series token equals a status color.

### CSS (Theme.CSS)

- Custom properties `--hp-surface`, `--hp-text`, `--hp-text-muted`, `--hp-grid`,
  `--hp-axis`, `--hp-series-1..8` are declared on `.hp, .hp-spark, .hp-tip, .hp-table`.
- Dark mode is a **selected** palette, not an inversion, applied two ways:
  `@media (prefers-color-scheme: dark) { :root:where(:not([data-theme="light"])) .hp {…} }`
  and `:root[data-theme="dark"] .hp {…}` (and likewise for `.hp-spark`, `.hp-tip` and `.hp-table`).
- `.hp-s1`…`.hp-s8 { color: var(--hp-series-N) }`. Marks use `currentColor`.
- `.hp { max-width: 100%; height: auto; overflow: visible; touch-action: pan-y pinch-zoom }`.
  The `touch-action` lets a sideways finger drag reach the client as
  pointer events (the touch scrub) while vertical scrolling and pinch zoom
  keep working; spec.md §3.4 makes it a MUST for replacement stylesheets.
- The brush (rev 2): `.hp .hp-brush { fill: currentColor; fill-opacity: .1;
  pointer-events: none }` plus the indicator edge below for the client's
  `rect.hp-brush` (forced colours: `Highlight` at 25% with a `Highlight`
  edge), and `:is([hx-plot\:brush],[data-hx-plot\:brush]) .hp
  { cursor: crosshair }` over brushable charts.
- Indicator edge: `.hp-crosshair` (no longer `--hp-text-muted` at 60%),
  `.hp-band-hl` (its 6% `--hp-text` wash kept) and `.hp-brush` get
  `stroke: var(--hp-text-muted); stroke-opacity: 1; stroke-width: 1.5px;
  vector-effect: non-scaling-stroke` (see "Accessibility" for why).
- `client/hx-plot.css` is `Stylesheet()` byte for byte, for servers in other
  languages; `TestStylesheetFile` checks it (and fails when it is missing:
  npm would pack without it silently) and `go test . -run StylesheetFile
  -update` writes it. `client/build.mjs` also refuses to pack when a
  `package.json` "files" entry is missing, and `client/LICENSE` is the
  repository's (checked by `client`'s `TestPackageFiles`).
- `@media (prefers-reduced-motion: no-preference) { .hp-anim … { transition: d .35s, x …, cx …, cy …, r …, opacity … ease-out } }`.
  Bars and areas are paths, so `d` carries their transitions (this works in Chromium
  and Firefox; Safari snaps, which is acceptable).
- Refetch keeps the frame: `.htmx-request .hp, .hp.htmx-request { opacity: .6; transition: opacity .2s .5s }`.
  The half-second delay matters: htmx 4 puts `htmx-request` on the requesting
  element itself when it has no `hx-indicator`, so a polling wrapper carries it on
  every poll, including bodiless 304s. Without the delay every chart would pulse
  each poll; with it only a request slower than 0.5 s dims the chart.
- Tooltip (`.hp-tip`): a fixed-position card in surface color with a hairline border
  ring and shadow. Values come first and are strong, names follow in secondary ink.
  Each row is keyed by a short colored line (`.hp-key` using `.hp-sN`). The client
  builds `div.hp-tip > div.hp-tip-t` (heading) and `div.hp-tip-row > span.hp-key.hp-sN
  + b.hp-tip-v + span.hp-tip-n`; the stylesheet styles exactly these classes.
  The card is at most 320 px wide and long content wraps inside it: the heading
  (a band category the axis truncates) and values (repeated observations)
  break with `overflow-wrap: anywhere`, while the series name keeps its own
  width (`flex: none`) up to 60% of the row and is ellipsized beyond, so a row
  is never identified by its color alone.
- The token selectors also match a standalone file's own root
  (`svg.hp:root[data-theme="dark"]`, and `svg.hp:root:not([data-theme="light"])`
  under the dark media query), because there `:root` is the `<svg>` itself.
- The `@media (forced-colors: active)` block gives text, grid and frame the system
  colors, but keeps the series palette on data marks and keys
  (`.hp-marks`, `.hp-key`, `.hp-end` get `forced-color-adjust: none`), the usual
  data-viz practice: with every series in `CanvasText` the legend and grouped bars
  could no longer tell series apart. The tooltip opts out of forcing to keep its
  keys, so its text (`.hp-tip-t`, `.hp-tip-v`, `.hp-tip-n`) is set to `CanvasText`
  explicitly rather than following page tokens that may not match the system
  theme. The band highlight becomes a 2 px `Highlight` outline over a 25%
  `Highlight` wash: the normal 6% wash is about 1:1 against `Canvas`, and in
  band mode it is the only on-plot mark of the active category.
- `.hp-table`: a visually tidy table that is also screen-reader friendly, with
  tabular numerals. `.hp-sr` is visually hidden text for screen readers (1 px,
  `clip-path: inset(50%)`).

## Accessibility

- `role="img"`, `aria-labelledby` points to `<title>` and `aria-describedby`
  to `<desc>`, and `tabindex="0"` makes the chart keyboard-reachable (the
  client binds arrow keys). Until rev 2 QA, `aria-labelledby` named both
  title and desc; Chrome then made the name "title + desc" and the
  description the desc again, so a screen reader read the description
  twice on every focus (verified in Chrome's accessibility tree).
- The active point's on-plot indicators (`.hp-crosshair`, `.hp-band-hl`,
  `.hp-brush`) are the chart's keyboard focus state, so each has an edge at
  3:1 or more against the surface (WCAG 1.4.11): a 1.5 px
  `--hp-text-muted` stroke with `vector-effect: non-scaling-stroke`, which
  antialiasing never thins below 75% of a pixel (`#666` on white: 3.4:1 at
  worst; dark `#9c9c9c` on `#191919`: 4.2:1). The washes alone measured
  about 1.1:1 to 1.6:1 in Chrome. `TestActivePointIndicatorsContrast`
  checks both default palettes.
- The auto description is factual and brief, for example: "Line chart with 2 series:
  p50, p99. X axis time from Mar 3, 2026 2:00 PM to Mar 3, 2026 3:00 PM. Y axis
  Latency, 0 to 120 ms." It names the chart type(s), the series, and the range of
  each axis. A chart whose series hold no valid value says "No data." in place of
  the ranges (keeping the X range only when x values exist), matching the visible
  "No data".
- `Chart.Table()` returns a `<table class="hp-table">` whose `<caption>` is the
  title (or "Chart data"), with one row per x value and one column per series. The x
  column is headed by `X.Label`, or "Time" (time axes) or "X" when that is empty. Values
  use the same formatter plus unit. Dot labels become the first column when present.
  A chart of only dots lists one row per point instead (Label, Series when there
  are several, X, Y). Put it in `<details>` or a toggle so it stays reachable
  without hovering.
- **Table ids (rev 2).** The table is `<table class="hp-table" id="{ID}-table">`
  and every body row has an id: `{ID}-r-{rowkey}`, where `{rowkey}` is the
  FNV-1a key of the row's `k`, assigned with `uniqueKey` over the table's
  rows in order (so a band row's key equals the bars' `{catkey}`); in a
  point table it is `{seriesKey}-{key}`, with `uniqueKey` per series, so a
  repeated x in one series gets `hash(k + "#1")`. The morph then keeps a
  row's node for as long as its x stays in the table, so a screen reader's
  reading position stays on the same data while a live window slides, and
  slides cost one removal and one insertion. `Table` stays an ascending
  table: the extension keeps a reader who scrolled to the end at the end
  (spec.md §6.8.7), and keeps an expanded `<details>` open across morphs.
- **Missing values** show an en dash, which NVDA and JAWS skip at their default
  punctuation level, so the dash alone would read as a blank cell or a name
  with no value. A table cell holds `<span aria-hidden="true">–</span><span
  class="hp-sr"> no data</span>`, and the client's announcement speaks a `"–"`
  value as "no data". (Without the stylesheet the cell reads "– no data".)
- **Screen-reader browse mode.** NVDA and JAWS stay in browse mode on a focused
  `role="img"`, where the arrow keys move their virtual cursor and never reach
  the page, so keyboard reading works for screen-reader users only after they
  switch to focus mode (NVDA+Space; in JAWS, Insert+Z turns the virtual cursor
  off). The chart does not
  advertise the keys (`aria-keyshortcuts`) or switch itself to
  `role="application"`: the server cannot know whether the optional client is
  loaded, and `role="application"` takes away the screen reader's own reading
  commands. The description and the data table carry the content either way.
  Changing this is a client design decision (see the client section).
- **Gestures (rev 2)** have keyboard paths: Enter selects the active point,
  and with `hx-plot:brush` Shift+ArrowLeft/Right (and Shift+Home/End) extend
  a range, drawn as `rect.hp-brush`, that Enter commits and Escape cancels;
  the range is announced as "{t0} to {t1}". They do not reach everyone:
  touch cannot brush, mobile screen readers cannot select or brush, and
  NVDA/JAWS in browse mode get only a `detail` 0 click, which selects the
  active keyboard point (spec.md §8 has the table). So the guide and the
  dashboard pair every gesture with plain HTML: a link per table row for
  select and a `datetime-local` from/to form for zoom (spec.md §5.5), plus a
  polite live region the server updates after the request.

## Sparkline

`Sparkline` renders a compact, axis-free SVG (default 120×28) for stat tiles: a
2 px line, an optional area wash, and an accent end dot. It is `role="img"` with an
`aria-label` built from Label plus first, min, max and last values, each with
`Unit` (shortened past 32 runes) and formatted by `Format` when set, so a tile
showing "212 ms" is not read as "212". NaN creates gaps. Like `Chart`, it has
`WriteTo`, `HTML` (so templates never cast bytes to `template.HTML` themselves)
and `Component`.
Its root is `<svg class="hp-spark">`, deliberately not `hp`: spec.md makes
every `svg.hp` a chart (§1, §6.4) with the §3.2 contract (id, `data-hp`,
`tabindex`, title and desc), which a sparkline does not meet, so with `hp`
the client and any spec validator took it for a broken chart (it was
`hp hp-spark` before rev 2; `TestSparklineIsNotAChart`). Inside it uses the
chart classes (`hp-s1`, `hp-line`, `hp-area`, `hp-end`), and the stylesheet
declares the tokens on `.hp-spark` too, so the same stylesheet themes it.
The y extent is the data's own (no forced zero) with 4 px of padding; the end dot
is `circle.hp-end` with r = 2.5. Width and Height must be in [10, 32768]. No data
gives an empty frame labelled "{Label}: no data". It has no ids, so it can be
repeated freely on a page.

## htmx 4 integration (package hx)

htmx 4.0.2 is current (released 2026-11-18; 4.0.0 was 2026-08-28). Key
facts verified against four.htmx.org:
- Requests use `fetch()`. Inheritance is explicit (`hx-target:inherited`).
  Events are named `htmx:phase:action` (for example `htmx:after:swap`). All
  responses swap except 204/304 (`config.noSwap`; see the error-page pitfall
  below).
- Core swaps include `innerMorph`/`outerMorph`, which preserve node identity.
  **This is the main live-update mechanism.** (Corrected in rev 2: it is
  htmx's own morph, not idiomorph. For each new child it takes an old
  sibling that shares an id from its id set, else one that `isEqualNode`,
  else the first of the same tag without a conflicting id, looking ahead
  `morphScanLimit` (10) siblings; text nodes and id-less elements therefore
  match by position. A new element whose id is not in the old tree is
  inserted fresh. Options are global only: `morphIgnore`, `morphSkip`,
  `morphSkipChildren`, `morphScanLimit`. Extensions can veto a node or an
  attribute with `htmx_before_morph_node` and `htmx_before_morph_attr`.)
- Extension facts that rev 2 relies on (htmx 4.0.2 `htmx.js`):
  `htmx_after_swap` also fires for a 304 or other no-swap response, with
  `ctx.swap === "none"`; `htmx_before_swap` sees the old DOM and the new
  fragments (`detail.tasks[i].fragment`); `htmx_before_settle` runs right
  after the morph, before the browser renders; `htmx_after_init` fires only
  for elements with an `hx-<verb>` attribute, so `hx-plot:*` elements are
  found by scanning in `htmx_after_process`; `htmx_before_cleanup` fires only
  for elements that have `_htmx` (`api.htmxProp(elt)` creates it); changing
  any `hx-*` attribute (including `hx-plot:*`) in a morph re-initializes the
  element; `api.attributeValue` applies `htmx.config.prefix` (default
  `data-hx-`), `metaCharacter` and `:inherited`; a custom swap style name
  cannot contain `:`; view transitions are off by default and run swaps
  through a serial queue when on. hx-sse swaps each unnamed message with the
  public `htmx.swap`, so all swap hooks fire for SSE frames too.
- More extension facts for rev 2 (checked in the 4.0.2 source after the
  design review): `registerExtension` returns `false` when the name is not
  in `htmx.config.extensions` (read once, from the meta tag, at startup) or
  is already registered, and `undefined` on success, calling `init(api)`
  synchronously only on success, so "started" is best detected in `init`;
  `api.triggerHtmxEvent(elt, name, detail, bubbles)` runs extension hooks
  (`name` with `:` → `_`), applies `metaCharacter` to the name, logs under
  `logAll`, and dispatches a cancelable `CustomEvent` (on `document` when
  `elt` is detached); `hx-on::plot:select` is shorthand for
  `hx-on:htmx:plot:select`, and with a metaCharacter `hx-on--plot-select`
  maps to `htmx-plot-select`, the name htmx dispatches; `hx-config` (HCON or
  JSON, own attribute or `:inherited`) is merged into `ctx.request` when the
  request context is created, so `hx-config="plot.slide:false"` arrives as
  `ctx.request.plot.slide`, and SSE messages reuse the stream's context;
  `htmx_before_settle` gets `{task, newContent, settleTasks}` but no `ctx`,
  so per-request settings must be captured in `htmx_before_swap`; a task
  runs as a view transition when
  `task.swapSpec?.transition ?? mainSwap?.transition ?? ctx.transition`;
  after settle core applies the swap's `scroll:`/`show:` modifiers;
  `hx-ignore` (and `data-hx-ignore`) excludes a subtree from processing;
  `hx-sync="<selector>:<strategy>"` shares one request queue per selected
  element, where a new `replace` request aborts the one in flight and a new
  `drop` request is dropped while one is in flight.
- `<hx-partial hx-target="#id" hx-swap="…">…</hx-partial>` updates several targets
  from one response, after the main swap.
- Extensions are loaded by script tag; there is no `hx-ext`. Register them with
  `htmx.registerExtension(name, {init(api), htmx_config_request(elt, detail), htmx_after_swap(elt, detail), …})`.
  Hook names use underscores. `detail.ctx.request.body` is a `FormData` during
  `htmx_config_request`.
- `hx-sse` extension: `<div hx-sse:connect="/stream" hx-swap="innerMorph">`. Each
  unnamed `data:` message is swapped into the element. Named events (`event: foo`)
  dispatch DOM events. `id:` and `Last-Event-ID` handle replay, and `retry:` sets
  the reconnect delay. Multi-line data uses repeated `data:` lines.
  CDN: `https://cdn.jsdelivr.net/npm/htmx.org@4.0.2/dist/ext/hx-sse.min.js`.
- `hx-ptag` extension: the server sends `HX-PTag: "v42"`, the client echoes it as
  the `HX-PTag` request header, and the server replies `304` when unchanged, which
  skips the swap. The `hx-ptag="v42"` attribute seeds the initial tag.
  CDN: `…/dist/ext/hx-ptag.js`.
- Request headers: `HX-Request: true`, `HX-Request-Type: full|partial`,
  `HX-Source` (`tag#id`), `HX-Target` (`tag#id`).

Package `hx` API:

```go
func IsRequest(r *http.Request) bool                 // HX-Request: true
func IsPartial(r *http.Request) bool                 // HX-Request-Type: partial
func Tag(b []byte) string                            // quoted, stable content hash: `"hp-1a2b3c4d5e6f7a8b"`
func CheckPTag(w http.ResponseWriter, r *http.Request, tag string) bool
     // sets HX-PTag; if the request's HX-PTag equals tag, writes 304 and returns true
type Stream struct{ … }                              // SSE writer, safe for concurrent use
func NewStream(w http.ResponseWriter, r *http.Request) (*Stream, error)
     // sets Content-Type: text/event-stream, Cache-Control: no-cache, X-Accel-Buffering: no;
     // errors if w can't flush (via http.ResponseController)
func (s *Stream) Send(html []byte) error             // unnamed message; splits lines into data: fields; flushes
func (s *Stream) SendID(id string, html []byte) error
func (s *Stream) Event(name string, data []byte) error // named event (DOM event on the client)
func (s *Stream) Retry(d time.Duration) error
func (s *Stream) Comment(text string) error          // keep-alive
func (s *Stream) LastEventID() string                // from the reconnect request header
func Partial(target, swap string, content []byte) []byte // <hx-partial hx-target=".." hx-swap="..">content</hx-partial>, attrs escaped
func Size(r *http.Request, defW, defH float64) (w, h float64)
     // reads "hp-w"/"hp-h" query or form params sent by the client extension, clamped to [50, 4096]
const HeaderRequest, HeaderRequestType, HeaderPTag, HeaderLastEventID // header names
const MinSize = 50; const MaxSize = 4096                               // Size bounds

// rev 2: the Level 3 gesture parameters (spec.md §5)
type Selection struct{ Chart, X, Series string }  // hp-chart, hp-x, hp-series, raw
func Selected(r *http.Request) (Selection, bool)  // ok when hp-x is non-empty
func (s Selection) Time() (time.Time, error)      // X as RFC 3339 (any offset), in UTC
func (s Selection) Float() (float64, error)       // X as a finite number
type Brush struct{ Chart, X0, X1 string }         // hp-chart, hp-x0, hp-x1, raw and unordered
func Brushed(r *http.Request) (Brush, bool)       // ok when hp-x0 and hp-x1 are both non-empty
func (b Brush) Times() (t0, t1 time.Time, err error)  // = TimesIn(time.UTC)
func (b Brush) TimesIn(loc *time.Location) (t0, t1 time.Time, err error)
     // RFC 3339 (any offset), or HTML datetime-local (no offset) read in loc; UTC, ordered t0 <= t1
func (b Brush) Floats() (x0, x1 float64, err error)   // parsed, finite, ordered x0 <= x1
const ParamWidth, ParamHeight, ParamChart, ParamX, ParamSeries, ParamX0, ParamX1 // "hp-w" … "hp-x1"
```

The gesture readers read the parameters the way `Size` does (`r.Form` when
the handler parsed it, else the query plus an urlencoded body of at most
64 KiB, never a multipart or larger body), so they accept query and form
bodies alike without making the server buffer an upload, and they never
fail or panic on bad input: a missing or empty value is `ok == false`, an
unparsable one an error (prefixed `hyperplot: `) from the typed accessor,
which then returns zero values. The raw strings are untrusted input;
escape them if they are echoed. `testdata/conformance/params.json` lists
the cases (its `server` section, run by `TestConformanceParams`).

Both parsers are written by hand to accept exactly what the Python helpers
of spec.md Appendix B accept, so every reference implementation agrees:
numbers follow the JSON grammar (no `NaN`, `Inf`, `+1`, `0x10`, `1_0`,
`.5` or surrounding space) and must be finite after parsing (`1e999` is
rejected); times are `YYYY-MM-DD[Tt]hh:mm[:ss[.fraction]][Z|z|±hh:mm]`,
where an offset needs seconds, fraction digits past nanoseconds are
dropped, no leap second, and the result must lie in years 1 to 9999 in
UTC (as Python's `datetime`). The form without an offset is HTML's
`datetime-local`, which lets a plain from/to form send a zoom with no
script (spec.md §5.3, §5.5); `Brush.TimesIn` reads it as a wall time in
the chart's zone (a nil location is UTC). Where that zone's clocks change
it does not keep `time.Date`'s choice, which depends on the side of UTC
the zone is on (02:10 in New York's skipped hour came back as 01:10 EST,
so a range across the gap was reversed): like JavaScript's `Date` and
Python's `fold=0`, a repeated wall time is its first instant and a
skipped one is read with the offset before the change (`wallTime` in
`hx/gesture.go`; `TestParseTimeWallClock` checks every quarter hour around
the changes of several zones against a brute-force reading, and
`params.json` has a gap and a repeat case). Year 0000 is rejected as
written, and offset minutes run to 59, in Go and in the Python helper
alike.
`Selection.Time` requires an offset: an `hp-x` is a key the server wrote.
`FuzzParseTime`, `FuzzParseNumber` and `FuzzGestureParams` check that the
parsers never panic, that accepted times round-trip and agree with
`time.Parse`, that every finite float64 in `encoding/json` form parses
back to itself, and that `Size` stays in bounds for any request.

Security: escape `target` and `swap` attribute values. Normalize CRLF and lone CR
to LF and split on LF so a value can never inject SSE fields (a lone CR is an SSE
line terminator, so it becomes a line break rather than being dropped). Event
names must not contain CR, LF, or NUL and must not be empty. Event ids must be
printable ASCII (U+0020–U+007E) with no leading or trailing space, because hx-sse
sends the last id back as the `Last-Event-ID` fetch header: browsers throw on
characters above U+00FF (so every reconnect fails), send Latin-1 as single bytes,
and trim surrounding spaces, so such an id could never compare equal to
`LastEventID()`. Invalid names and ids are rejected with an error and nothing is
written. Comments are split the same way.

Behavior details, settled during implementation and checked against the htmx
4.0.2 sources (`dist/ext/hx-sse.js`, `dist/ext/hx-ptag.js`):
- `IsPartial` also requires `HX-Request: true`.
- `CheckPTag` compares tags after trimming whitespace, a `W/` prefix, and one pair
  of surrounding quotes, because hx-ptag echoes the response header verbatim but
  the seeding `hx-ptag` attribute is often written unquoted. An empty tag
  disables the check. On a match it drops `Content-Type`/`Content-Length` and
  writes a bodiless 304.
- `Retry` writes a bare `retry:` block (applied at once by EventSource) **and**
  repeats the field on the next message. hx-sse 4.0.2 drops blocks that carry
  only `retry:` and applies the field only together with data or an event name.
- `Event` always writes at least one `data:` line so that EventSource clients
  dispatch it too.
- Every Stream method returns an error after `r.Context()` is done or after a
  failed write (sticky). Each write sets a write deadline 30 s ahead (when
  supported), so the stream outlives `http.Server.WriteTimeout` while a client
  that stops reading is still dropped. `NewStream` writes nothing and restores
  the headers when `w` cannot flush, so the caller can still send an error.
- `Size` ignores missing, non-numeric, non-finite, zero, and negative values and
  returns the default for them **unclamped** (so a default of 0 can still mean
  "chart default"). Only client-reported values are clamped.
- Returning from an SSE handler makes hx-sse reconnect after its base delay
  (500 ms or `retry:`, ±30% jitter). The delay doubles, up to 60 s, only while
  reconnects fail (network error or non-2xx); any 2xx resets it. So a stream
  handler skips frames that fail to render instead of returning, and one that
  cannot stream answers `503` with `HX-Reswap: none` (no swap on the first
  request, backoff on reconnects), not `204`. To stop for good, send a named
  event that matches the element's `hx-sse:close`. Spec §4.5 states these
  rules, and the replace-don't-morph rule below, for servers in any
  language.

Pitfalls found while building `examples/dashboard`, verified in Chrome against
the 4.0.2 sources (`htmx.js`, `hx-sse.js`, `hx-ptag.js`):
- **Do not morph a new `hx-sse:connect` URL into an existing element.** The morph
  re-initializes the element and the extension cancels its reader, but the read
  loop only stops when the element leaves the document, so it reconnects to the
  old URL and two streams swap into one element. Replace the element instead
  (a plain `innerHTML` swap of its parent removes the old one).
- **An element that htmx processes twice before its stream answers opens two
  streams.** hx-sse's duplicate guard is set only when the response arrives,
  and a morph that inserts a new element processes it once as a placeholder
  and again after the swap. The dashboard swaps its stream element with
  `innerHTML`, triggered by an `HX-Trigger` event that htmx fires after the
  filter response has been swapped, and its container carries
  `hx-morph-skip-children` so the filter morph leaves the stream alone.
- **`load` fires before hx-ptag reads a seeded `hx-ptag` attribute** (load runs
  during element initialization, the seed is read in `htmx:after:init`), so a
  `load` request never sends the seed. `load delay:1ms` sends it.
- **Morphs remove attributes the server did not send**, including the `open`
  of a `<details>` the reader expanded, inline styles and classes a script
  added. The extension keeps the `open` of any `<details>` that holds an
  `hp-table` (spec.md §6.8.7), so pages need no `"open"` in `morphIgnore`
  (v0 pages had `"morphIgnore":["data-htmx-powered","open"]`; the array
  replaces the default, so a page that ignores other attributes keeps
  `data-htmx-powered`). The extension never keeps its own state in
  attributes of server-rendered elements.
- **The "stale table" (roadmap bug 1) was scroll anchoring, not the morph.**
  The table's DOM was current after every poll and SSE message. Opened, the
  table shows its oldest rows; scrolled anywhere, CSS scroll anchoring
  (`overflow-anchor: auto`) lowered `scrollTop` by one row each time the
  morph removed the top row, so the visible rows never changed. The
  extension keeps a table scrolled to its end at its end (follow, spec.md
  §6.8.7), in every browser. Anchoring itself stays on: it is what keeps a
  reader in the middle of a table on the same rows as rows come and go
  (verified in Chrome by `client/browser/tables.browser.mjs`), as spec.md
  §6.8.7 promises, and row ids keep its anchor row the same element. The
  research's `overflow-anchor: none` for the dashboard was dropped for that
  reason: with it, a reader mid-table sees the rows shift under them by one
  per bucket.
- **Deferred htmx and extensions race.** Run from a `defer` script, htmx
  4.0.2 starts on `setTimeout` (the document is no longer `loading`), and
  that timer can fire while a deferred extension script is still
  downloading. The extension then misses htmx's first pass: an
  `hx-sse:connect` element never connects (the "unexplained SSE stall" of
  the bug research, reproduced by the browser tests). The dashboard loads
  htmx and its extensions without `defer`; `hx-plot.js` may keep it,
  because htmx then waits for `DOMContentLoaded`, which follows deferred
  scripts.
- **Error pages the handler never wrote are swapped too** (a proxy's 502, the
  503 of `http.TimeoutHandler`, middleware 401/403, ServeMux 404/405), and
  `makeFragment` copies their `<title>` into `document.title`. hx-ptag then
  pins them: it updates the stored tag only from a response with an `HX-PTag`
  header, so after recovery the next poll sends the last good tag and gets a
  304. The guard is client-side: `"noSwap":[204,304,"4xx","5xx"]` (htmx
  matches `Nxx` patterns; the array replaces the default `[204, 304]`). The
  guide and the dashboard set it in the meta tag
  `<meta name="htmx-config" content='{"noSwap":[204,304,"4xx","5xx"]}'>`,
  plus `"plot":{…}` when a page changes the extension's defaults.
- A URL that serves a page or a fragment by `hx.IsPartial` sends
  `Vary: HX-Request, HX-Request-Type`, so caches keep the two apart.
- An element's `hx-get` is read at request time, so a morph that changes it
  (new filters in the URL) takes effect on the next poll; any changed `hx-*`
  attribute also re-initializes the element, which re-fires `load`.

## Client extension (client/hx-plot.js)

Plain ES2020 with no dependencies and no build step: `client/hx-plot.js`
and two optional companions, `hx-plot-brush.js` (brushing) and
`hx-plot-tween.js` (the Safari path tween), each loaded with its own script
tag after it. It works with or without htmx (pointer and keyboard features
use event delegation on `document`). v0 was `client/hyperplot.js`,
registered `htmx.registerExtension("hyperplot", …)`; rev 2 renamed the
file to `hx-plot.js`, registers `plot`, and implements
[spec.md §6](spec.md#6-the-extension). The bullets under "Behaviour"
describe v0 behaviour, which rev 2 keeps (with the rev 2 names), followed
by the rev 2 implementation.

### Size budget

Measured with terser 5.36 (`-c -m`) and `gzip -9`, on 2026-11-26:

| Script | Source | Minified | Minified + gzip |
|---|---|---|---|
| `hyperplot.js` 0.1.0 (v0) | 21,173 | 7,928 | 3,737 |
| `hyperplot.js` after the two bug fixes | | 12,394 | 5,547 |
| `hx-plot.js` 0.2.0 | 40,413 | 14,823 | 6,488 |
| `hx-plot.js` 0.2.0 after the review fixes | 44,567 | 15,899 | 6,866 |
| `hx-plot.js` 0.2.0 after the second review's fixes | 53,010 | 18,316 | 7,797 |
| `hx-plot-brush.js` 0.2.0 | 9,663 | 3,161 | 1,631 |
| `hx-plot-brush.js` 0.2.0 after the second review's fixes | 10,682 | 3,360 | 1,735 |
| `hx-plot-tween.js` 0.2.0 | 4,717 | 1,674 | 923 |
| `hx-plot-tween.js` 0.2.0 after the second review's fixes | 5,507 | 1,842 | 1,014 |
| htmx 4.0.2 `htmx.js` | | 37,541 | 13,414 |
| htmx 4.0.2 `hx-sse.js` | 17,482 | 5,918 | 2,363 |
| htmx 4.0.2 `hx-live.js` | 35,656 | 15,011 | 5,775 |

**Outcome (rev 2 build).** The estimate below was low: the bug fixes alone
took the v0 file to 12,394 bytes minified, and rev 2's start-up, container
attributes, events, select, gesture parameters, `strings` and the companion
hooks added about 2.4 KB more. As the overflow rule says, the brush moved
to `hx-plot-brush.js`, and the tween is its own file; both companions are
within their budgets. The core is **5,004 bytes minified and 2,165 gzipped
over** (18,316 and 7,797 against 13,312 and 5,632). It was 1,511 and 856
over when first built; the review fixes added about 1,080 and 380, and the
second review's fixes the rest: marks redrawn inside the swap, a deferred
mode decision, records that survive task copies, settings validation,
history copies without inline styles, tooltip placement in transformed
dialogs, a hoverable tooltip, and autosize on added content without htmx.
No spec-1 feature was dropped to fit. The maintainer approved the budget
below (2026-11-26); how the core gets back under it is open: trim it, move
another part out (select and its parameters, about 250 bytes gzipped,
would join the brush as `hx-plot-gestures.js`), or raise the core budget
again (to about 18 KiB minified and 8 KiB gzipped for the code as it is).
The minified build drops the test-only exports
(`globalThis.__HYPERPLOT_TEST__` blocks) with terser's `--define`.

The v0 budget was 8 KiB minified, with 260 bytes left. Rev 2 adds, by
estimate in minified bytes: container-attribute selectors, start-up through
`init`, discovery in `htmx_after_process` and cleanup (≈ 650); swap
bookkeeping and the slide with per-element start transforms and the
effective duration, through the Web Animations API (≈ 1,250, about the
same as the first draft's transition juggling, which needed `transitionend`
and timeout cleanup); table follow and keep-open (≈ 350); select with the
touch snapshot and `detail` 0 clicks (≈ 500); pointer and keyboard brush
with its rectangle, re-projection across swaps and Shift+Home/End
(≈ 1,500); gesture parameters, config merging and `strings` (≈ 500); less
the SSE hook and raw autosize reads (≈ −150). That is about 12.6 KiB.

**Decision** (approved by the maintainer, 2026-11-26). The core
`hx-plot.js` budget is **5,632 bytes gzipped and
13,312 bytes minified** (5.5 KiB and 13 KiB; gzipped is what users
download), which leaves well under 1 KiB for fixes after rev 2, against
htmx 4.0.2's 13.1 KiB gzipped core. If the measured core goes over, the
brush (both pointer and keyboard, with its rectangle) moves to an opt-in
`hx-plot-brush.js` loaded after `hx-plot.js` like the tween, and the core
budget stays. Everything else spec 1 requires stays in the core, including
the slide: it is the fix for the launch-blocking wobble and the only
animation that works in Safari. The one optional piece, the JavaScript `d`
tween for Safari (spec.md §6.8.4), is a separate file, `hx-plot-tween.js`,
with a budget of **2,048 bytes minified and 1,024 gzipped**, included with
its own `<script>` tag after `hx-plot.js` and found through
`globalThis.hyperplot`. Nothing is loaded lazily at run time: a dynamic
`import()` or injected script would need a `script-src` exception, defeat
vendoring, and save little at these sizes.

**Enforcement.** `client/build.mjs` (Node built-ins only) backs two
scripts: `npm run size` minifies and gzips the three files and fails over
budget (the brush's budget is 4,096 bytes minified and 2,048 gzipped), and
`npm run build` writes `hx-plot.min.js` and the companions' `.min.js` with
source maps (run by `prepack`; the minified files are not committed).
The maintainer approved terser as an exact-version devDependency and a CI
size check (2026-11-26); neither is in place yet. Today `build.mjs` runs
`npx --yes terser@5.36.0`, a throwaway copy in npx's cache, or the command
in the `TERSER` environment variable, and there is no `make size` target
and no size step in CI (`npm ci && npm run size` in the client job), which
would fail until the core is back under budget. `node --test` keeps
needing nothing, so `make client` still works offline. The Go package
embeds the unminified sources (18,849 bytes gzipped for `hx-plot.js`), the
readable, vendorable files.

### Behaviour

- **Tooltips.** On `pointermove` inside `svg.hp`, read and cache (by text) the JSON
  in `metadata.hp-tips` and convert the pointer to viewBox coordinates with
  `getScreenCTM().inverse()`. Find the nearest x (binary search) or, in mode `xy`,
  the nearest point within 12 px. Show the crosshair (mode x), the band highlight
  (mode band, setting `x`/`width`/`y`/`height` from xs/bw/top/bottom), or, in mode
  xy, a ring around the active point: the tooltip alone cannot tell dots closer
  than its 12 px offset apart. The ring is a `<circle class="hp-focus">` that the
  client appends to `g.hp-hover` on first use (`r="7"` around the 4 px dots,
  `fill="none"`, `stroke="currentColor"`, `stroke-width="2"`,
  `pointer-events="none"`, `aria-hidden="true"`, and `transition: none` through
  the CSSOM so it keeps up with the tooltip). It is styled by presentation
  attributes only, which any stylesheet rule overrides, and `currentColor` makes
  it `CanvasText` in forced colors. The server does not write it, so a morph
  removes it and the next render adds it again. Fill one
  global `div.hp-tip` appended to `document.body` (or to the open `<dialog>` or
  popover holding the chart, which is in the top layer), `position: fixed`, flipping to
  stay inside the viewport, `width: max-content` (so a dialog does not squeeze
  it). In a dialog or popover with a transform, filter or `contain`, that
  element positions the fixed tooltip, so after placing it the client
  measures where it went and moves it back by the difference. Build rows
  with `textContent` only. When the pointer leaves the chart and the
  tooltip (`pointerout` to outside both), hide everything. The band
  highlight gets `transition: none` through the CSSOM too (it is a `rect`,
  which the stylesheet's value transitions would otherwise slide 350 ms
  behind the tooltip). The tooltip has `pointer-events: auto` (inline, over
  the stylesheet's `none`) and can be hovered (WCAG 1.4.13): a pointer on
  it keeps the active point, and a pointer that rested on a point for
  400 ms and then moves inside the convex hull of that position and the
  tooltip's box is heading for it, so the tooltip stays put (300 ms grace
  after the last such move). A pointer scanning the chart never rests, so
  it is never held.
  Modes `x` and `band` respond only between `top` and `bottom` (and, for `band`,
  inside the outer bands). Touch uses pointer events: a tap shows the tooltip,
  which stays until a tap outside the chart (the hit radius doubles to 24 px).
  Escape hides any open tooltip (WCAG 1.4.13: dismissible; hoverable as
  above; persistent, as it stays until the pointer leaves). With
  `prefers-reduced-motion: reduce`, the tooltip moves only when the active point
  changes instead of following the pointer.
- **Tooltip DOM** (the stylesheet styles these classes):
  ```html
  <div class="hp-tip" aria-hidden="true">
    <div class="hp-tip-t">Mar 3, 14:05</div>                  <!-- row "t", omitted if empty -->
    <div class="hp-tip-row"><span class="hp-key hp-s1"></span><b class="hp-tip-v">12.3 ms</b><span class="hp-tip-n">p50</span></div>
  </div>
  <div class="hp-live" role="status">Mar 3, 14:05: p50 12.3 ms</div>
  ```
  The script sets only `position: fixed`, `pointer-events: none`, `left`/`top`, and
  `display` on the tooltip, and the usual visually hidden styles (1 px,
  `clip-path: inset(50%)`, `overflow: hidden`) on `.hp-live`, all through the
  CSSOM, which CSP allows. The empty `.hp-key` needs its look (a short 2 px line
  in `currentColor`) from the stylesheet. The visual tooltip is always
  `aria-hidden="true"`.
- **Announcements.** Keyboard steps are announced through `div.hp-live`, a
  separate `role="status"` region that is created empty when the script starts
  (and re-created after a swap if one removed it). Screen readers only announce
  changes to a live region that was already in the accessibility tree, so it is
  never `display: none` or `aria-hidden`, and the tooltip (which is hidden and
  shown) is not the live region. Only a keyboard step writes to it, as
  "heading: name value, name value"; pointer hover and refreshes never do.
  Hiding a keyboard tooltip clears it, so the same row is announced again on
  the next step. A chart in an open `<dialog>` or popover gets the region
  inside that element when it takes focus (`focusin`), before its first step:
  a modal dialog makes the rest of the page inert, which would remove a region
  in `<body>` from the accessibility tree. With no keyboard point (just
  focused, or after Escape), the region is placed for the focused chart, at
  start-up (`showModal()` focuses a chart before the extension starts) and
  after every swap; before, a background swap moved it back to `<body>`, and
  the first step then inserted and wrote it in one task.
- **Keyboard.** When an `svg.hp` has focus, ArrowLeft and ArrowRight step through
  points, Home and End jump to the ends, and Escape hides. Anchor the tooltip to the
  point. Modes x and band step in index order (left to right). Mode xy lists its
  points series by series in data order, so the client steps through them in
  spatial order instead (`keyOrder`): by viewBox x, then bottom to top at the same
  x, then by index, leaving out points without a finite position.
- **Survives morph.** In the extension hook `htmx_after_swap` (which SSE
  messages also run; an app that restricts `htmx.config.extensions` must
  include `plot`), re-apply the active
  hover or focus state to the updated chart, so polling does not make the tooltip flicker or vanish. A
  keyboard point is kept by its row key (`k`, else its heading `t`), nearest its old index, so a
  sliding window keeps the reader on the same x; when that row is gone the old
  index is kept, clamped. Refreshing updates the visual tooltip but never
  announces, so a focused live chart does not re-read its row on every poll or
  SSE message. A chart replaced by a non-morph swap is found again by its `id`.
  If it is gone, the tooltip hides. `focusout` decides a frame later whether to
  hide a keyboard tooltip: htmx 4.0.2's innerHTML and outerHTML swaps blur
  the focused chart while removing it (it still reports `isConnected`), then
  focus the replacement. After every swap the client also removes any
  `div.hp-tip` or `div.hp-live` it does not track and hides marks outside the
  active chart (a page copy restored by htmx's `hx-history-cache` brings them
  back), and it leaves what it owns out of that extension's copies in the
  first place (`htmx_history_cache_before_save` and `…_after_save`):
  its elements, and the `style` attribute of every element of a chart (a
  slide's `transition: none`, a tween's, the band highlight's, an emptied
  `style=""`), put back through the CSSOM, which a strict `style-src` allows.
- **Responsive.** Elements with `hx-plot:autosize` are observed with a
  `ResizeObserver`. When the width changes by ≥ 8 px (debounced 150 ms), dispatch
  the `htmx:plot:resize` event on the element (it does not bubble; `detail` holds
  `width`/`height`), so authors can write
  `hx-trigger="load, htmx:plot:resize"`. In `htmx_config_request`, when the
  request target is, or is inside, an `hx-plot:autosize` element, add `hp-w`
  (the rounded content width: `clientWidth` less padding; omitted if 0, and never
  overriding a value already in the request, in its body or in the query string of
  `ctx.request.action`) to `ctx.request.body`. The URL check matters: for GET and
  DELETE, htmx 4.0.2 deletes every URL parameter whose name is in the body before
  appending the body, so a body `hp-w` would silently replace an `hp-w` the author
  wrote into `hx-get` or `hx-sse:connect`. `hp-h` (the
  content height) is added, and height changes fire `htmx:plot:resize`, only for
  `hx-plot:autosize="both"`: a wrapper without a CSS height is as tall as the
  chart inside it plus the inline baseline gap (a 640×320 chart in an 800 px
  `div` measures 324 px), so echoing that height back grows the chart on every
  poll and defeats the ptag `304`. Documentation samples therefore keep the
  height on the server (`width, _ := hx.Size(r, 640, 0)`). That body is a
  `FormData` at this point, and for GET and DELETE htmx 4.0.2 copies it into the
  query string afterwards (verified in `htmx.js`), so `hx-get` and
  `hx-sse:connect` carry the size too. Sets `globalThis.hyperplot = {version}`,
  which also guards against loading the script twice.
- Export the pure helpers (`nearestIndex`, `nearestPoint`, `placeTooltip`,
  `keyOrder`, `parseTips`, `keepIndex`, `announcement`) for tests via
  `globalThis.__hyperplotTest` only when `globalThis.__HYPERPLOT_TEST__` is set.
  Test them with `node --test` (`client/hx-plot.test.mjs`, with no
  dependencies). `client/dom.test.mjs` runs the whole script in a `node:vm`
  context against `client/fakedom.mjs`, a minimal fake browser (elements,
  selectors, events, `getScreenCTM`, `ResizeObserver`, timers, an htmx stub), to
  cover the extension hooks, autosize, pointer and keyboard handling, the status
  region and refresh. A change to the wiring needs a test there.
- The Go package `client` uses `//go:embed` for `hx-plot.js`,
  `hx-plot-brush.js` and `hx-plot-tween.js` into unexported strings and
  exposes `JS()`, `BrushJS()` and `TweenJS() []byte` (a new copy on each
  call) and `Handler()`, `BrushHandler()` and `TweenHandler() http.Handler`,
  which serve them with the correct Content-Type, a strong ETag each (304 on
  a match), and `Cache-Control`. That lets Go users serve them without npm.
  The scripts are not exported `[]byte` variables because the ETags are
  computed once at init: a caller writing into a shared slice would change
  the served bytes under the same validator, and browsers revalidating would
  keep a stale copy.
- `client/package.json` is prepared for publishing to npm as `htmx-ext-plot`
  (with `"files"`, `"exports"`, MIT license, and keywords). Do not publish.
  Its `version` is the single source of truth: the banners
  (`/*! hx-plot X.Y.Z | MIT */` and the companions' `hx-plot-brush`,
  `hx-plot-tween`) and `globalThis.hyperplot.version` must match it, and
  `dom.test.mjs` fails when they do not, so a release bumps them together.

### Rev 2 implementation (spec 1)

Normative behaviour is in [spec.md §6](spec.md#6-the-extension); this is how
the reference client does it. It was revised after a three-critic design
review (htmx conventions, a non-Go implementer, interaction and
accessibility); `.qa/rev2/design.md` lists each finding and its resolution.

**Built** (2026-11-26) as described below, with these differences, found
while building:

- **Companions.** The brush (pointer and keyboard, its rectangle,
  re-projection across swaps, the range announcement, `timeValue` and
  `brushValues`) is `hx-plot-brush.js`, by the size budget's overflow rule;
  `hx-plot-tween.js` is built now rather than after rev 2. The core calls
  them through two optional hook objects on the global,
  `hyperplot.brush` (`key`, `enter`, `cancel`, `click`, `swap`) and
  `hyperplot.tween` (`capture`, `play`), passing its internal API, which it
  publishes as `hyperplot._` once it has started (`attrs`, `own`, `emit`,
  `say`, `render`, `settings`, `tips`, `chart`, `attr`, `cur`, `frame`,
  `vb`; not a public interface). A companion loaded without a started core
  (refused by the allow-list, or loaded first) does nothing. Without the
  brush companion, `hx-plot:brush` has no effect and Enter selects.
- **Brush press.** A press starts a brush anywhere on the chart within the
  plot's vertical span, with x clamped to the plot, not only inside the
  plot: the `scatter-xy` fixture presses at x 0 and expects the left edge.
  spec.md §6.6 says so now. `setPointerCapture` is wrapped, because a
  synthetic press (a test, another script) has no pointer to capture.
- **Tween.** Runs only where CSS cannot set `d`, tested once by drawing (a
  path with a point `d` attribute and a CSS `d` with width; its bounding
  box tells which won), because Safari parses `d`; elsewhere the stylesheet's
  `d` transition does the job and the companion stays out of the way.
  `hyperplot.tween.force = true` (a test hook) tweens everywhere. The clock
  is an empty Web Animation (`svg.animate(null, {duration, easing})`), whose
  `getComputedTiming().progress` applies the easing, so `easing` takes any
  CSS timing function; each frame writes the interpolated `d` attribute.
  The inline `transition: none` is given back two frames after the last
  frame (giving it back with the final `d` would start a CSS transition),
  never on a path a newer tween holds. `capture` (from `htmx_before_swap`)
  only reads: the page or another extension can still cancel the swap, and
  a running tween must then finish on its frame. `play` (from
  `htmx_before_settle`) ends the running tween and starts each path it held
  from where it last drew it; without a capture (a slide, a snap, a static
  frame) it only ends it.
- **Older htmx 4.** Without `api.triggerHtmxEvent` (htmx 4.0.0-alpha7, as
  vendored by the htmx-4-community-extensions test harness) events fall
  back to plain `CustomEvent`s, and without `api.htmxProp` autosize
  elements get no htmx state.
- **Styles.** The tooltip's and the status region's inline styles are set
  with `style.cssText` (CSSOM, allowed by `style-src 'self'`; the browser
  tests run under that policy).

- **Names.** `client/hx-plot.js`, banner `/*! hx-plot X.Y.Z | MIT */`,
  `htmx.registerExtension('plot', …)`, `globalThis.hyperplot = {version}`
  (still the double-load guard, and the documented exception to htmx's
  no-globals pattern). Events `htmx:plot:resize`, `htmx:plot:select`,
  `htmx:plot:brush`. `client/package.json`: name `htmx-ext-plot`, version
  0.2.0 for the first release, `main`/`unpkg`/`jsdelivr` `hx-plot.js`,
  `files` `hx-plot.js`, `hx-plot.min.js`, `hx-plot.min.js.map`,
  `hx-plot.css`, `README.md`, `peerDependencies` `{"htmx.org": ">=4.0.0"}`
  marked optional in `peerDependenciesMeta`, keywords including `htmx`,
  `htmx-extension`, `htmx-4`. `dom.test.mjs` keeps checking that the
  banner, the global and `package.json` agree.
- **Start-up.** The script decides once: htmx mode when
  `globalThis.htmx?.registerExtension` exists, standalone otherwise. It
  decides when it runs or, with no htmx yet in a document that is still
  loading, at `DOMContentLoaded`, by which time a module or deferred htmx
  has run (before, a classic extension script after a module htmx ran
  standalone and its gesture requests went out without `hp-*`
  parameters, silently). Standalone on a page with `hx-get`, `hx-post` or
  `hx-trigger`, it warns on the console. A companion that loads while the
  core is undecided queues itself on `hyperplot.later` and runs again when
  the core starts. In
  htmx mode it registers and attaches its document listeners (pointer,
  click, keyboard, focus, scroll, resize), observers and status region from
  the extension's `init(api)`, which htmx calls only when it accepts the
  registration; `registerExtension` returns `false` when
  `htmx.config.extensions` excludes `plot` and `undefined` on success, so
  the return value cannot be tested for `true`. Refused, the script does
  nothing. Standalone, it attaches the same listeners at once. A
  `dom.test.mjs` case runs with `extensions: "sse,ptag"` and expects no
  listener, no status region and no events.
- **Attributes.** Two container attributes, found with
  `el.closest(SEL(name))`, where `SEL` joins, for each of `hx-plot` and
  `htmx.config.prefix + 'plot'` (default `data-hx-plot`) and each separator
  of `:` and `htmx.config.metaCharacter`, the pieces
  `[${CSS.escape(base + sep + name)}]` (the hx-sse pattern); the value is
  that element's `getAttribute`, tested with `!= null` because
  `hx-plot:brush=""` is a value. `api.attributeValue` is not used for them:
  its `implicitInheritance: false` default would make an ancestor's
  `hx-plot:brush` invisible. `hx-plot:slide` is gone (the setting is
  `slide` in `hx-config`/`htmx.config.plot`). `data-hp-autosize` and the
  `AUTO` selector go away. Charts and autosize elements inside
  `[hx-ignore]`/`[data-hx-ignore]` are skipped in htmx mode.
- **Discovery and cleanup.** `htmx_after_process(root)` scans `root` and its
  descendants for autosize elements (the extension never gets
  `htmx_after_init` for them); `init` also scans `document.body` once the
  DOM is ready, for a script that loads after htmx's first pass. Setup is
  idempotent (the existing `sizes` WeakMap). With htmx, each autosize element
  gets `api.htmxProp(el).plot = true` so that `htmx_before_cleanup` fires
  for it, and the hook unobserves it. Without htmx, a `MutationObserver` on
  the document scans added elements, so autosize elements that page script
  adds later are observed too.
- **Config.** Per swap, `{slide: true, follow: true, keepOpen: true,
  duration: 350, easing: 'ease-out', strings: {noData: 'no data', range:
  '{0} to {1}'}, ...htmx.config.plot, ...ctx.request.plot}` (`strings`
  merged one level deep), computed in `htmx_before_swap`, which has `ctx`,
  and kept with the recorded charts for `htmx_before_settle`, which does
  not. Pointer and keyboard code outside a swap uses the page-level part.
  A `duration` that is not a finite number ≥ 0, or an `easing` that
  `CSS.supports('transition-timing-function', …)` rejects (a JavaScript
  library's `"easeOutCubic"`), falls back to the default: `Element.animate`
  throws on it inside `htmx_before_settle`, which aborted htmx's swap after
  the DOM had changed (no `htmx:after:swap`, new content never processed).
- **Swap hooks.** No class on the swap target and no forced style read: the
  `hp-swapping` guard of the first draft is dropped. Its removal order
  forced a style recalculation while the guard was on, which stopped the
  CSS transitions of every other chart in the target (verified in Chrome
  by the review), and it is unnecessary: an inline `transition: none` set
  after a recalculation started a transition cancels it (verified in
  Chrome, `.qa/rev2/design.md`).
  - `htmx_before_swap(elt, {ctx, tasks})`: return at once when
    `ctx.swap === 'none'` or every task's `swapSpec.style` is `none`. Record,
    for the whole page rather than the task targets, every `svg.hp` (id →
    the root attributes of spec.md §6.8.3) and, per `hp-table` (by id), its
    scrollable ancestors innermost first with whether each overflows and is
    at its end; plus the settings above and `mainSwap?.transition` and
    `ctx.transition`. The record rides on every task under a `Symbol` key
    (and is the latest record, for a task an extension built from scratch):
    htmx runs `htmx_before_swap` hooks in registration order, and htmx's
    `hx-targets`, registered after plot, replaces the main task (whose
    target is the button) with `{...task, target}` copies, which a
    `WeakMap` keyed by task missed (no slide, no follow).
    `htmx_before_settle` decides per task whether it runs as a view
    transition (`task.swapSpec?.transition ?? mainSwap?.transition ??
    ctx.transition`) and whether it has `scroll:` or `show:`; the follow box
    is the innermost overflowing (else the innermost) recorded box inside
    the task's target, a replaced box counting as inside.
  - `htmx_before_morph_attr(elt, {attrName, newValue})`: return `false` for
    `attrName === 'open'` on a `details` that contains `table.hp-table`
    (when `keepOpen`).
  - `htmx_before_settle(target)`: runs after the morph, before paint. For
    each recorded chart in `target`, compute the outcome with the pure
    `slideOf(old, new, {slide, scale, interval})` (`scale` from the old
    chart's `getScreenCTM().a`, read in `htmx_before_swap` so nothing forces
    a style computation after the morph; `interval` from a per-id map of
    the last swap's `performance.now()`). For **slide** and **snap**: set
    `style.transition = 'none'` on the chart's `path, circle, rect, line,
    text`, and cancel the slide animations still running on it (kept in a
    per-chart list). For **slide**, call
    `el.animate([{transform: start}, {transform: 'none'}], {duration:
    effective, easing})` on each anchored element, with `start` per
    spec.md §6.8.3 (affine for `path`/`rect`/`polyline`/`polygon`,
    translate-only by `(a − 1)·x + b` for the rest, `x` from `x`, `cx` or
    `x1`; numbers rounded to 6 decimals), and clear the inline transitions
    (and an emptied `style` attribute) when the last animation finishes;
    for **snap**, two animation frames later, once the browser has
    computed the new frame's style. When `|a − 1| < 0.001`, animate each
    `g.hp-series` once (affine) instead of its children, unless it carries
    a `clip-path` or `mask` attribute (a CSS transform moves the element's
    own clip region with it). Anchored tick and dot labels are found at any
    depth (`g.hp-axis-x text.hp-tick`): spec.md allows a plain `g` per tick.
    Then, when the active chart (by id) is in the target, redraw its marks
    there (`render()` and the brush's `swap`), focus the replacement of a
    keyboard reader's chart that a non-morph swap replaced, and give a
    keyboard reader's crosshair, ring and brush range the slide's translate
    animation. A pointer's crosshair and a brush being dragged stay under
    the pointer. Redrawing here, not in `htmx_after_swap`, matters: htmx
    fires `htmx:after:swap` only after the settle delay (`settle:300ms`, or
    the default 1 ms for innerHTML/outerHTML, whose id-matched elements
    always include the svg) and after a view transition's `finished`, so
    the marks were missing for that long and from the transition's new
    snapshot. During a view transition Chrome hit-tests the snapshot and
    sends `pointerout`/`pointerleave` from the chart; the client ignores
    pointer exits between `htmx_before_viewTransition` and
    `htmx_after_viewTransition` (and `htmx_after_swap`).
  - `htmx_after_swap`: `refresh()` (hover, keyboard, brush, status region,
    scan); then scroll the noted at-end containers to `scrollHeight`. The
    `htmx_sse_after_message` hook goes: SSE frames already fire
    `htmx_after_swap`, and v0 refreshed twice per frame.
- **Keyboard key.** `keepIndex` keeps the reader by `k` when rows have one,
  else by `t` (v0).
- **Select.** A `click` listener on `document` (delegated, like pointer
  handling) hit-tests with the same code as hover. A click with
  `detail === 0` selects the active keyboard point or nothing. A click right
  after a pointer brush is swallowed. Touch: the `pointerdown` listener
  records `{svg, index}` of the active point synchronously, before the
  rAF-coalesced hover runs, and the click selects only when the tapped
  point equals that record. Enter in `onKey` dispatches for the active
  keyboard point. Events go through `api.triggerHtmxEvent(svg, name,
  detail)` in htmx mode and `svg.dispatchEvent(new CustomEvent(name,
  {bubbles: true, detail}))` standalone (`htmx:plot:resize` with bubbles
  false).
- **Brush.** `pointerdown` (mouse or pen, primary button, inside
  `data-hp-plot`, chart inside an `hx-plot:brush` element, x kind time or
  linear) stores the press as a data value, sets pointer capture and an
  inline `user-select: none` on the chart, and calls `preventDefault`;
  `rect.hp-brush` is created in `g.hp-hover` like `hp-focus`; `pointerup`
  after ≥ 4 CSS px dispatches. After a swap, the client re-projects the
  stored value through the new geometry and redraws the rectangle (a slide
  does not translate a rectangle being dragged: its dragged edge stays under
  the pointer); releasing removes the inline `user-select` and an emptied
  `style` attribute; the drag
  ends on `lostpointercapture`, `pointercancel`, or a frame whose x kind
  cannot be brushed. Keyboard: Shift+Arrow and Shift+Home/End set
  `cur.anchor` (by key) and extend, the rectangle spans the two rows' x;
  Enter commits; Escape cancels. The status region announces the range.
- **Parameters.** `htmx_config_request` adds the size when `ctx.target`
  is, or is inside, an autosize element (the source element no longer
  counts), and the gesture parameters when `ctx.sourceEvent.type` equals
  `htmx:plot:select` or `htmx:plot:brush` with `metaCharacter` applied
  (`api` exposes no helper, so the client applies `replace(/:/g, mc)`
  itself), sharing the "never override body or URL" check.
- **Test exports** add `slideOf`, `brushValues` (the inversion and rounding
  of spec.md §6.6), `timeValue` (ms → canonical time value), `startTransform`
  (per element kind) and `gestureParams` to `nearestIndex`, `nearestPoint`,
  `placeTooltip`, `keyOrder`, `parseTips`, `keepIndex` and `announcement`. A
  new `client/conformance.test.mjs` reads `../testdata/conformance/` and
  checks every `client` expectation, through the pure helpers and through
  `fakedom.mjs` (which grows `click` with `detail`, pointer capture and
  `lostpointercapture`, `Element.animate`/`getAnimations` recording, enough
  `getComputedStyle` for the scroll checks, and an htmx stub whose
  `registerExtension` honours an allow-list and whose `triggerHtmxEvent`
  applies `metaCharacter`). New `dom.test.mjs` cases: two charts in one
  swap target, one slides and the other keeps its `d` transition; a fast
  stream shortens the slide and below 60 ms snaps; a tap's rAF flush
  between `pointerdown` and `click` does not select on the first tap.
- **Stylesheet additions** (`Theme.CSS`, mirrored to `client/hx-plot.css`):
  `.hp{touch-action:pan-y pinch-zoom}` (the touch scrub; `pinch-zoom` keeps
  page zoom for low-vision readers); `.hp-brush{fill:currentColor;
  fill-opacity:.1}` and, under forced colours,
  `.hp-brush{fill:Highlight;fill-opacity:.25}`;
  `:is([hx-plot\:brush],[data-hx-plot\:brush]) .hp{cursor:crosshair}` so a
  brushable chart shows it (default spellings only). `client/hx-plot.css`
  is byte-identical to `hyperplot.Stylesheet()`; a root-package test
  compares them and `-update` rewrites the file.

## Live-update patterns (documented in docs/htmx.md, shown in the example)

[docs/htmx.md](htmx.md) documents all seven, with Go handlers that
`docs/snippets_test.go` compiles; the example dashboard shows them all, and
the [server guide](implementing-a-server.md) builds 1, 2, 5, 6 and 7 in
Python.

1. **Polling with ptag:**
   `<div hx-get="/charts/latency" hx-trigger="every 2s, htmx:plot:resize" hx-swap="innerMorph" hx-plot:autosize hx-ptag="…">`.
   The handler renders, computes `hx.Tag(svg)`, calls `hx.CheckPTag`, and writes.
   Unchanged data costs one 304.
2. **Streaming with SSE:**
   `<div hx-sse:connect="/stream/throughput" hx-swap="innerMorph">`. The handler
   loops on a ticker and calls `stream.Send(svg)`, stopping on `r.Context().Done()`.
3. **Filters:** a form row above the charts with
   `hx-get="/dashboard" hx-trigger="change" hx-target="#charts" hx-swap="innerMorph"`.
   The server re-renders every chart for the slice. Series keep their colors via
   `Slot`.
4. **Multi-target:** one response updates a chart plus KPI tiles via `hx.Partial`.
5. **Drill-down (rev 2):** `hx-get="/detail" hx-trigger="htmx:plot:select"
   hx-target="#detail"` on an ancestor of the chart; the handler reads
   `hx.Selected(r)` and renders the detail for that x (and series). The
   table gets a link per row to the same URL with `hp-x` (spec.md §5.5),
   and the response updates a polite live region.
6. **Brush to zoom (rev 2):** `class="zoom" hx-get="/charts/latency"
   hx-trigger="htmx:plot:brush" hx-target="#latency" hx-swap="outerMorph"
   hx-sync="replace" hx-plot:brush` on an ancestor of the polling element,
   which carries `hx-sync="closest .zoom:drop"` so a late poll cannot undo
   the zoom; the handler reads `hx.Brushed(r)`, renders a new polling
   element whose `hx-get` carries the range and a chart with
   `X.TimeDomain` set to it, and sends `HX-Push-Url` with the page URL for
   that range (spec.md §5.4). A from/to form with `datetime-local` inputs
   sends the same request without a pointer (spec.md §5.5;
   `hx.Brush.TimesIn` reads it in the chart's zone).
7. **Sliding windows (rev 2):** with `Animate` set, a window that moves
   slides instead of wobbling, with no code. The data source SHOULD include
   the last point before the window start (drawn, but not hoverable or in
   the table); `X.TimeDomain` fixes the window. `hx-config="plot.slide:false"`
   on the polling or stream element turns the slide into a snap.

## Testing standards

- Table-driven unit tests per package, with `-race` clean.
- **Golden files**: `testdata/*.svg`, regenerated with `go test . -run Golden -update`.
  Cases marked `preview` also write a `Standalone` copy to `testdata/preview/`
  for opening directly in a browser. Every golden file is also parsed with
  `encoding/xml` to assert well-formedness, and a file that no golden case
  writes (left over from a renamed case) fails.
- A **well-formedness fuzz test** over chart inputs (names with markup, NaN/Inf
  values, empty series) that also varies the series count (1–8), point count
  (0–11), fixed X/Y/time domains, font size, bar width, legend mode, slots, tick
  counts and time zone, and rejects `NaN`/`Inf` in the output, x ticks
  outside the plot, and any spec violation `checkSpec` finds (see the spec
  checks below). The zones include ones whose clocks change
  (America/New_York, and Australia/Lord_Howe's 30-minute shift), seeded in
  their repeated hours. Its failure messages print the inputs, and CI uploads
  failing inputs as an artifact.
- `scale.FuzzTimeTicks` checks that time ticks are strictly increasing, inside
  the domain and bounded in number, over arbitrary domains in DST zones.
- Tests that need zones with clock changes import `time/tzdata` (test files
  only, so the library stays small), so they run everywhere instead of
  skipping where the OS has no zoneinfo.
- CI runs every benchmark once (`-benchtime 1x`), so a broken benchmark fails CI.
- `Example*` functions for godoc (`ExampleChart_Render`, `ExampleSparkline`, the
  `hx` examples).
- Benchmarks: render 1 line × 1,000 points, 8 series × 200 points, and 50 bars.
  Target well under 1 ms for typical charts (up to a few hundred points),
  which they meet. Measured on a Ryzen 5 5600X, across runs and machine load:
  50 bars ≈ 0.10–0.14 ms, 1 × 1,000 points ≈ 0.85–1.1 ms, 8 × 200 points
  ≈ 1.1–1.5 ms, and 50,000 labeled dots on a 4096 × 4096 chart ≈ 0.16 s. The
  two 1,000+ point benchmarks sit at or above 1 ms; most of their time goes
  to formatting one tooltip value per point and building the row lookup maps
  (see "Roadmap").
- Client: `node --test`, run inside `client/` (`make client`). From the
  repository root, `node --test client/` finds no test files and passes.
  `swap.test.mjs` drives the swap hooks (slide, snap, follow, keep open)
  against `fakedom.mjs`, and `hx-plot.test.mjs` runs the `slide`
  fixture's decision table through `slideOf`. `dom.test.mjs` covers
  registration and the allow-list, the attribute spellings, `hx-ignore`,
  autosize, select and the events; `brush.test.mjs` and `tween.test.mjs`
  the companions; `conformance.test.mjs` every `client` expectation of the
  fixtures (including `brushAcrossSwap` and the anchored elements of a real
  slide) and the `client` cases of `params.json`. `fakedom.mjs` parses the
  reference SVGs with a small XML parser.
- Example servers in other languages: `python3 -m unittest discover
  examples/python` from the root (`make py`, Python 3.10+, standard library
  only) and `node --test` inside `examples/node` (`make nodeex`). `go test
  ./...` never sees them, but they test against files this repository owns
  (spec Appendix B, `testdata/conformance/`, `client/`), so `make test`,
  `make check` and CI run both, and the checker's own tests
  (`python3 -m unittest discover tools/hpcheck`, also in `make py`).
  `make conformance` (in `make test`, `make check` and a CI job) runs the
  checker on what those tests never read: the files the Python and Node
  fixture renderers write (`hpcheck fixtures --out DIR --require
  line-time,empty,bars-band,escaping,collisions`), the gallery's `-out`
  export (`hpcheck file`, every chart kind against the universal rules),
  and the dashboard, Python and Node servers through their `hpcheck.json` configs
  (`hpcheck server --spawn`). `docs/wiring_test.go` fails if a directory
  of examples or tools has Python or Node tests, a fixture renderer or a
  checker config that `make test`, `make check` or CI does not run. It
  matches the commands that run them (the recipes reachable from each
  entry point, through prerequisites and `$(MAKE)` calls, and the
  workflow's `run:` lines, `working-directory` included), not mentions,
  so a comment, help text or step name that names a directory does not
  count. `docs/paths_test.go` fails if any file mentions an example
  or tool path (run lines and comments included) that does not exist.
- **Example indexes and pins** (`docs/examples_test.go`): every example
  page loads htmx and its extensions with exactly the dashboard's tags
  (version, SRI hash, crossorigin, never deferred); `examples/README.md`
  and `examples/recipes/README.md` link every example and promise none
  that is missing; the browser test starts every example but `basic`; and
  `make golden` rewrites every `preview/` directory, whose packages define
  `-update` and `TestPreview`.
- **Real-browser tests**: `client/browser/*.browser.mjs`, run with
  `npm run test:browser` in `client/` (`make browser`) after `npm ci`. They
  use `playwright-core` (a dev-only dependency, chosen over
  `puppeteer-core` because the same test code can later drive Firefox and
  WebKit, where scroll anchoring and `d` transitions differ) with an
  installed Chrome (`CHROME_PATH`, or the usual install path); it downloads
  no browser. `harness.mjs` builds `examples/dashboard` and starts it on a
  free port with `-speed 5` (simulated time five times faster, so windows
  slide on most polls), gives each Chrome a fresh profile in its own
  temporary directory (`HP_BROWSER_TMP`), answers the page's pinned htmx
  files from the `htmx.org` dev dependency (same bytes, so the SRI hashes
  match) and refuses every other request off the test server, so the tests
  need no network. It kills the server's process tree and closes Chrome
  when a file ends, also on failure. Files run one at a time, so one
  Chrome runs at a time. `tables.browser.mjs` covers bug 1: a table at its
  end shows each new row, a reader mid-table keeps their rows, the table
  matches the chart after every poll and SSE message, and `<details>`
  stays open. `slide.browser.mjs` covers bug 2, sampled on every frame:
  the drawn path is its new `d`, the only motion is
  `matrix(a, 0, 0, 1, e, 0)`, no vertex moves vertically, and a mid-line
  vertex and every tick label move left steadily; `slide: false` and
  reduced motion snap. `HP_REPO` runs them against another checkout (the
  committed v0 tree fails both files, which is how the fixes were
  verified), and `HP_SHOTS` saves screenshots. `extension.browser.mjs`
  needs no Go: `harness.startStatic()` serves the client scripts, htmx and
  hx-sse from `node_modules`, the stylesheet and pages built from the
  conformance fixtures, under a strict Content-Security-Policy, and logs
  every request. It covers select by click and Enter (parameters, and no
  `hp-w` for a target outside the autosize element), a pointer brush (one
  request with the range and `hp-w`, no select, no text selection), a
  keyboard brush and its announcement, the client without htmx on an
  inline chart and an inline standalone chart, slides on SSE messages with
  an open `<details>` that stays open, and the tween (forced, then
  unforced in Chrome, where it must stay out of the way).
  `htmx.browser.mjs` (also static) runs the hooks under the real htmx
  rather than the node suites' stub: Back with `hx-history-cache` (no
  stale tooltip, crosshair or second status region) and after a boosted
  link, a focused chart replaced by innerHTML and by outerHTML (focus,
  tooltip and keyboard point kept), `innerMorph transition:true` (no
  slide), `hx-ptag` 304 polls, `data-hx-plot-autosize` with metaCharacter
  `-`, a followed live table (the pointer tooltip stays) and one that
  starts shorter than its box, a chart in a modal `<dialog>` (tooltip
  painted, status region in the accessibility tree), a click on a line
  of a brushable chart (its series), Escape during a drag (no select),
  and elements named `hyperplot`. `harness.startStatic()` also serves
  htmx's own extensions at `/ext/hx-*.js`.
  `dashboard.browser.mjs` drives the dashboard's gestures: a real drag
  zooms the latency chart, two counted polls carry the zoom and keep it,
  Back undoes it, a keyboard
  zoom undone with Back keeps focus on the chart, the from/to
  form and "Back to live", a click on a bar and Enter open the window
  detail at the detail container's width, and the window links.
  `dashboard-a11y.browser.mjs` runs the dashboard at `-speed 60`: a
  focused window link keeps its window (its id) as windows slide, and the
  page reflows at 320 CSS px without sideways scrolling.
  `examples.browser.mjs` opens every example in one Chrome, one at a time:
  `harness.buildGo` builds all the Go examples with one `go build`, and
  `harness.startServer` runs each (or the Python and Node servers, with
  `PYTHON` and `node`) on `127.0.0.1:0` and reads the port from its
  "listening on" line. Each page must answer 200, draw exactly the number
  of charts its entry declares, each with a size (a chart is
  `svg.hp[data-hp]` or a sparkline, `svg.hp-spark[role="img"]`; the
  `aria-hidden` color-key swatches do not count), log no console error (a
  CSP violation is one), fail no request and answer no 4xx or 5xx. Every
  page but sparkline-table (declared `kbd: false`, and checked to load
  none) must load `hx-plot.js`, and Home on a chart must announce a row.
  `docs/csp_test.go` pins every example's policy to the dashboard's (or
  it with `script-src 'self'` or `'none'`), since each example's own test
  compares the header with its own constant. Live examples (run at `-speed 30` where they take
  it) must show new data on their own, compared by the hover payload's
  rows so a resize does not count; the others answer one gesture or
  feature: a brush zooms and pushes the URL, Enter on a bar opens the
  detail, a filter change redraws, autosize draws at the `hp-w` it sent,
  polls get `304`, `?theme=dark` changes the palette, and standalone-svg's
  files render opened directly and as `<img>`. `examples/basic` listens
  on :8080 only and is left out. The CI job "Browser tests" runs the
  whole suite on ubuntu-latest with its preinstalled Chrome
  (`CHROME_BIN`) and setup-python's Python, after `npm ci` in `client/`;
  `make check` runs it too.
- Documentation samples: `docs/snippets_test.go` compiles every Go code block in
  `README.md`, `docs/htmx.md`, `client/README.md` and the `hx` package doc
  against the current API. Helpers that samples leave to the reader
  (`latencyChart`, `metrics`, `w`, …) are declared at their documented types in
  a prelude, so calls on their results are type-checked too; only unused
  variables and imports are ignored, and a new helper must be added to the
  prelude. The tests also reject samples that misbehave under htmx 4:
  `http.Error` in a handler that uses `hx`, using the height from `hx.Size`,
  returning from an SSE handler when `Render` fails, and serving a page or a
  fragment by `hx.IsPartial` without `Vary`. `docs/config_test.go` checks that
  every `htmx-config` meta tag in the docs and the dashboard sets
  `"noSwap":[204,304,"4xx","5xx"]`. `docs/quickstart_test.go` checks that
  `examples/basic/main.go` is the README quick start, so the program CI
  builds and lints is the one readers copy.
- **Conformance fixtures (rev 2)**: `testdata/conformance/` (format in its
  README). The root-package `TestConformance` (`conformance_test.go`) maps
  each `input.json` to a `Chart` (a small decoder that rejects unknown
  fields, so a fixture that grows a field fails until the decoder learns
  it), renders it and its table, compares them byte for byte with the
  reference files (`go test . -run Conformance -update` rewrites them;
  review like goldens), and runs every `server` assertion, `should` ones
  included, with a selector matcher over `encoding/xml`
  (`specdom_test.go`: the README's subset of type and `*`, class, id,
  attribute presence and equality, descendant and child combinators),
  plus `forbid` (including `where`), `raw.absent` (on the raw bytes),
  `tipsRaw.absent`, `unique` and `stable`. The golden well-formedness test
  ignores subdirectories, so the fixtures are exempt from its "no stray
  files" rule. `hx` runs the `server` cases of `params.json`
  (`TestConformanceParams`, including `timesIn` through `Brush.TimesIn`).
- **Spec checks on every chart**: `checkSpec` (`speccheck_test.go`) checks
  the MUSTs of spec.md that hold whatever the data: the root contract and
  first children, no `style`/`on*`/`script`, no external references
  (`href`/`src` only to `#…`, every `url(…)` to an id in the chart, the
  same lists as `tools/hpcheck`), no `transform` on the root or
  in the slid groups, ids unique and in the `{id}-*` namespace, series
  groups and slots, bar ids under their series key, geometry attributes in
  the canonical number form with `d0 < d1`, one hover group and payload,
  `xs` ascending and inside the plot, `top`/`bottom` equal to the plot,
  every row with a key in the x domain (canonical time value or number),
  unique keys in modes `x` and `band`, one entry per series, the
  crosshair or band highlight, no raw CR, and the table (id, caption,
  `th[scope]`, one row per payload row with a unique `{id}-r-` id, missing
  values spelled out, and each row compared in order with its payload row:
  in modes `x` and `band` the `th[scope=row]` is the row's `t` and the
  cells after it are its values in series order; in a chart of only dots
  a cell holds the value and the row header is part of `t`). `FuzzRender`
  also checks that distinct time keys never share a heading in mode `x`. It runs on the fixtures, on every golden case and
  its standalone copy (`TestGoldensMeetSpec`), and on every chart
  `FuzzRender` accepts; `TestSpecCheckCatchesViolations` mutates a valid
  chart and table to prove each check can fail. In `client/`,
  `conformance.test.mjs` runs every `client` expectation and the `client`
  cases of `params.json`. The evidence that the spec needs no Go is
  `tools/hpcheck/`, the same checks in standard-library Python, run by
  `make conformance` on the Python and Node examples' fixture output and
  servers (see "Example servers in other languages" above and Roadmap,
  "Rev 2: open", item 2). The server guide's Python
  (`docs/implementing-a-server.md`) was run through the assertions of
  `line-time` and `empty` and against the extension in Chrome by hand;
  `docs/wire_test.go` runs its helpers on edge cases.
- **Client size (rev 2)**: `npm run size` in `client/` (see "Size budget").

## Conventions

- **US locale by default** (maintainer decision). Time ticks and labels use a
  12-hour clock ("3 PM", "3:30 PM"; tooltips "Mar 3, 2026 3:05 PM"), dates are
  month-first ("Mar 3"), weeks start on Sunday, and numbers use comma thousands
  separators. Never emit 24-hour times or Monday-start weeks by default. A
  locale option is on the roadmap.
- Format with `gofumpt` and keep `go vet` and `staticcheck` clean.
- Every exported identifier has godoc that starts with its name.
- Use LF line endings, enforced by `.gitattributes`.
- Errors are prefixed `hyperplot: `.
- No commits are made by automation; the maintainer commits.

## Roadmap

Known gaps and planned work, roughly in priority order within each group.
None of these is promised for a particular release; API additions follow
the same rule as everything else here (this document first, then the code).

**Rev 2 (spec 1): closed**

The design is [spec.md](spec.md), [ADR 0005](adr/0005-htmx-community-first.md)
and `testdata/conformance/`. The two maintainer-reported bugs
(2026-11-26) are closed, and the renderer, `hx`, the client, the dashboard
and the docs are built; what rev 2 leaves open is listed under "Rev 2:
open" below.

- **Bug 1, closed: example tables don't update with their charts.** Diagnosed in headless
   Chrome: the table DOM was current after every poll and SSE message, with
   `<details>` open or closed, for all three live charts. The three suspects
   (`morphIgnore` `open`, position-matched rows, the SSE body) are cleared.
   The reader's *view* was stale: the table opens at its oldest rows, and
   CSS scroll anchoring pins any scrolled position as the morph removes the
   top row (see the pitfall under "htmx 4 integration"). It is an
   accessibility bug first, because the table is the chart's equivalent.
   **Fixed** (in the client, now `hx-plot.js`): the
   extension's follow and keep-open (spec.md §6.8.7) and table row ids
   (spec.md §4.1); the dashboard drops `"open"` from `morphIgnore` and keeps
   scroll anchoring (the pitfall under "htmx 4 integration" says why not
   `overflow-anchor: none`). Regression: `client/browser/tables.browser.mjs`.
- **Bug 2, closed: sliding-window line charts wobble instead of shifting.** Measured on
   the latency chart: after a one-bucket slide every vertex keeps its x and
   tweens vertically to its neighbour's value (mean 15.8 px, up to 55.3 px,
   0 px horizontally), and the x tick labels lurch right by one tick
   spacing whenever a tick leaves the window. Fix: the geometry attributes
   (spec.md §4.2) and the slide (spec.md §6.8.3; Bostock's "interpolate the
   transform, not the path"), on by default for animated time and linear
   axes (the maintainer chose default-on over opt-in, 2026-11-26), off with
   `hx-config="plot.slide:false"` or `htmx.config.plot`,
   never under reduced motion. A frame whose window moved but cannot slide
   (a zoom, a resize, a jump) snaps instead of tweening `d`, so the wobble
   cannot come back through another path. It is also the only update
   animation that works in Safari. The right margin follows the last tick
   label's overflow, so the plot width changes while a window slides (the
   `slide` fixture's frames differ by 11 px); the slide maps between plot
   widths, and the stable right margin (open item 3) removes the cause. **Fixed** (in the client, now
   `hx-plot.js`), with the geometry attributes and one
   bucket of overscan in the dashboard's sliding charts. Regression:
   `client/browser/slide.browser.mjs` (on the v0 build, vertices travel up
   to 81 viewBox units vertically during a slide; with the fix, 0).
- **Renderer:** done: `data-hp="1"`, `data-hp-plot`, `data-hp-x`,
   `data-hp-y`; row `k` in the payload; table and row ids (with
   `uniqueKey`); points outside the x domain left out of the payload and
   the table; CR in element text as `&#13;`;
   `.hp{touch-action:pan-y pinch-zoom}`, the brush rules and the brushable
   cursor in `Theme.CSS`; goldens regenerated; `TestConformance` and the
   spec checks in `FuzzRender`. **Done**, including `client/hx-plot.css`
   (`go test . -run StylesheetFile -update`, which must be re-run after any
   change to `Theme.CSS`; the test fails otherwise).
- **hx:** `Selected`, `Brushed`, `Brush.TimesIn` (with the
   `datetime-local` form) and the `Param*` constants, with tests over
   `params.json` and fuzz targets for the parsers. **Done.**
- **Client:** rename to `hx-plot.js` and `htmx-ext-plot`; register `plot`
   and start from `init` (honouring the allow-list); the two container
   attributes; `htmx:plot:*` events through `triggerHtmxEvent`; config from
   `htmx.config.plot` and `hx-config`; the swap hooks (slide with the Web
   Animations API, snap, follow, keep-open); select and brush; gesture
   parameters; one refresh per SSE frame; `hx-plot.css`; `npm run size`;
   `conformance.test.mjs`. `client/README.md` rewritten in the layout of
   core extension pages, with Go and Python server samples. **Done**
   (see "Rev 2 implementation" for what differs from the plan: the brush
   and tween companions, the brush press rule, the tween's feature test),
   except the size budget (open item 1). Real-htmx coverage:
   `client/browser/htmx.browser.mjs` runs the hooks under htmx 4.0.2 (hx-history-cache and boosted Back,
   innerHTML/outerHTML, `transition:true`, hx-ptag 304s, `data-hx-plot` with
   metaCharacter, followed tables, a modal dialog, the brush), and
   `client/browser/edges.browser.mjs` the second review's cases.
- **Examples and docs:** the plan was the dashboard on the new names, without `"open"` in
   `morphIgnore`, with `overflow-anchor: none`, one point of overscan in
   sliding windows, and a brush-to-zoom (with `hx-sync` and a from/to form)
   plus a drill-down demo (with row links and a live region);
   `examples/python/` (Level 2 at least, standard library only, using the
   helpers of spec.md Appendix B) with its conformance check (open item 2);
   `docs/htmx.md` and the README repositioned as an htmx extension
   ("server-rendered SVG, no client chart library").
   **Dashboard done** (see "Status"), with two differences from this plan:
   no `overflow-anchor: none` (dropped for the reason given in the htmx
   pitfalls), and the select equivalent is one link per bar group under
   the chart rather than per table row, since `Chart.Table` writes the
   table. A filter change ends a zoom and closes the detail.
   Browser QA (2026-11-26) fixed two dashboard bugs: the throughput chart
   was never fitted to its container (the page connected its stream at the
   default 640 px, and `htmx:plot:resize` fires only on a change, so at
   phone width the chart sat at half size; the page now renders it without
   `hx-sse:connect` and the container's `load` opens the one stream at its
   real width), and Close and "Back to live" dropped keyboard focus to
   `<body>` when they removed themselves (they now carry
   `data-focus-return`, which `app.js` honours). Regressions in
   `main_test.go` and `client/browser/dashboard.browser.mjs`.
   Two more dashboard fixes: the requests chart's window labels (its
   categories and the select key) repeated "1 AM" on a fall-back day at
   range 6h, so Render failed and the page returned 500, and in
   half-hour-offset zones they dropped the minutes ("7 AM" for 7:30);
   labels now keep the minutes when a window does not start on the hour
   and add the zone abbreviation, or the offset, to wall-clock times that
   repeat (`TestRequestWindowsZones`). And Back after a keyboard zoom
   dropped focus to `<body>` when no keyboard point was shown, because
   htmx restores history by replacing the children of `<body>`; `app.js`
   now notes the focused id on `htmx:before:history:restore` and focuses
   it again after the restore's swap.
   **Docs done**: the README and `client/README.md` lead with the
   extension; `docs/htmx.md` has the new names and patterns 5 to 7; and
   `docs/implementing-a-server.md` builds a Level 3 server in
   standard-library Python (one live line chart: polling with ptag, SSE,
   select, brush-to-zoom with `HX-Push-Url`, the from/to form and live
   regions). The guide's Python is run by `docs/wire_test.go`
   (lone surrogates in names, out-of-range brushes and select keys, and
   every server brush case of `params.json` through the Appendix B
   parsers), and `docs/links_test.go` checks every relative link and
   anchor in the repository's Markdown. Docs review fixes: spec §4.5 now
   states the hx-sse facts a server in any language needs (a failed first
   connection is never retried; non-2xx backs off and 2xx resets; a stream
   element whose URL changes MUST be replaced, not morphed) with a
   container recipe for resize and zoom, checked in Chrome; §5.5's form
   shares the brush's `hx-sync` queue; §5.3 rejects every non-JSON number
   spelling, as `hx` and Appendix B already did, and `params.json` pins
   it. The fixtures'
   geometry `match` assertions use spec §1.1's number pattern.

**Rev 2: open**

1. **Client size.** The core `hx-plot.js` is 5,004 bytes minified and
   2,165 gzipped over the approved budget, so `npm run size` fails (see
   "Size budget" for the options). Once it passes: terser as an
   exact-version devDependency, `make size`, and a size step in the CI
   client job, as approved (see "Releasing").
2. **`examples/python/`**: **done**, and more than planned. The Python
   example is a Level 3 server on the standard library (lines, grouped
   bars, SSE, select, brush) using a verbatim copy of spec.md Appendix B,
   which its tests compare with the spec; `examples/node/` is the same
   server on Node's built-ins. The checker is its own tool,
   `tools/hpcheck/` (one standard-library Python file), rather than a
   script in the example's directory: it runs every fixture's
   `checks.json` and the universal checks of `checkSpec` on any renderer's
   output, and checks a running server over HTTP. Both examples' fixture
   output passes `line-time`, `empty`, `bars-band`, `escaping` and
   `collisions`, and both servers, like the dashboard, pass the server
   checks (`make conformance`, also a CI job that has not run on GitHub
   yet). Left out: neither renderer draws `scatter-xy` (dots on a linear
   axis), so the checker skips it for them. ADR 0005's confirmation names
   this item.
3. **Stable right margin.** Reserve half the widest x tick label on time
   and linear axes whether or not a tick sits at the plot's end, so the
   plot width stops changing as ticks move (spec.md §4.4 SHOULD). Today the
   reference server itself makes `a` alternate between about 0.94 and 1.06
   on a sliding window, so the affine path is the common case and the clip's
   right edge jumps 11 px every other frame. It changes most goldens and the
   `slide` fixture's reference frames (its `a ≠ 1` case would move into an
   attribute-only frame), so it lands after the rev 2 goldens settle.
4. **Browser tests**: done for the two bugs, and for select, brush, the
   client without htmx, SSE slides with `<details>` kept open, and the
   tween (`client/browser/`, see "Testing standards"; Chrome only, run by
   the CI job "Browser tests", which has not run on GitHub yet). To do: the
   touch scrub (a scratch check showed `touch-action` is what keeps it
   working), touch select on a real device (only emulated touch and the
   fake DOM cover the second-tap rule), and Firefox and WebKit (see
   "Client" below; scroll anchoring for a reader mid-table, the slide's
   look, and the tween, which has never run in a real Safari).
5. **Conformance gaps.** No portable assertion ties each row's `xs` to its
   `k` through `data-hp-x` and `data-hp-plot`, so a server whose domain
   doesn't describe its drawing passes the `server` assertions; `checkSpec`
   also leaves out what the DOM alone cannot show (the mode rule, slot
   order, `data-hp-series` on unnamed series), which the fixtures cover.
   `Chart.Table` has no option for the select equivalent of spec.md §5.5
   (a link per row); pages add links themselves.
6. **Assistive technology.** Clicks with `detail` 0 (spec.md §6.6), which
   is how screen readers click, are tested against the fake DOM, not with
   a screen reader such as NVDA.

**Releasing**

- Tag the first module version (`v0.1.0`) so the API reference appears on
  pkg.go.dev; the README links to it already.
- Get the core `hx-plot.js` back under its size budget (see "Size
  budget"), then add terser as an exact-version devDependency in
  `client/package.json`, a `make size` target and a size step in the CI
  client job (`npm ci && npm run size`), as the maintainer approved.
- Publish `client/` to npm as `htmx-ext-plot`, then add SRI hashes to the
  pinned jsDelivr URLs in `client/README.md`. The maintainer kept the name
  (2026-11-26) after weighing the fallback `htmx-ext-hyperplot` and the
  soft collision with `c-cube/hx-plot`, an inactive, unlicensed htmx 2
  Vega-Lite extension that also registers `plot` (ADR 0005); check that the
  name is still free just before publishing. A release bumps
  `package.json`, the banner and `globalThis.hyperplot.version` together
  (`dom.test.mjs` checks them) and moves the changelog's Unreleased section
  under the version. ADR 0005 becomes `accepted` when it is merged.
- After publishing, open one pull request against `bigskysoftware/htmx`,
  base `four`, adding one bullet to `www/src/content/extensions/community.md`
  (ADR 0005, "Submission path").
- Report the htmx 4.0.2 extension bugs found while building the dashboard to
  bigskysoftware/htmx: hx-sse keeps reconnecting to the old URL after a
  morph changes `hx-sse:connect`; hx-sse opens two streams for an element
  processed twice before its response arrives; hx-ptag never sends the seeded
  tag on a `load` trigger. The workarounds (see the pitfalls under "htmx 4
  integration") stay until fixed releases are out, then the guide and the
  dashboard drop them.

**Charts**

- Horizontal bars. `Bar` draws vertical columns only.
- Bars on a time or numeric x axis (histograms, time-bucketed counts).
  `Bar.X` is `Categories` only today.
- A log scale in `scale` and an `Axis` option to use it (for latency
  percentiles and other heavy-tailed data).
- Small multiples: a helper that lays out one chart per group with shared
  scales, which `ErrTooManySeries` already recommends.
- Annotations beyond `RuleY`: a vertical rule (deploys, incidents), shaded
  ranges, and labeled points.
- A locale option. Labels follow US conventions (12-hour clock, month-first
  dates, Sunday weeks, comma thousands separators); today other locales need
  `Axis.Format`, `Axis.TimeFormat` and `Axis.TimeValueFormat`.
- Stacked areas with separate positive and negative stacks. Areas stack the
  raw signed sum today, and `Area.Base` is ignored when `Chart.Stack` is set.
- Better dot-label placement. Placement is greedy (four positions, first fit
  wins), so labels in dense scatters are dropped; they stay in the tooltip
  and the table.
- Tighter clipping for fixed domains. The clip rectangle is padded 6 px so
  edge dots and round caps are whole, which lets marks outside a fixed domain
  show up to 6 px past the plot. Clipping lines and areas to the exact plot
  (and only dots to the padded one) would remove that.
- A larger marker for an isolated point between gaps, which is drawn as a
  2 px round-cap dot and is easy to miss.
- Render speed for charts of 1,000+ points, which take about 1 ms or more
  (see "Testing standards"): format tooltip values lazily or in bulk, and
  avoid the per-row lookup maps.

**Client**

- Path tweening for Safari: built as the optional `hx-plot-tween.js`
  (spec.md §6.8.4) and tested in Chrome with its `force` hook, but never in
  a real Safari. Check the feature test and the tween there (WebKit through
  playwright-core, with the browser tests' other WebKit work). The
  companion tweens only path `d`; whether Safari transitions the other
  geometry properties the stylesheet animates (`cx`, `cy`, `r`, `x`, `y`)
  is unchecked.
- Opt-in, throttled announcements of data changes for live charts (the
  Highcharts accessibility module's "dynamic data" feature), never on every
  poll by default; and a server-written summary of ranges in `<desc>`.
- Drill-down links: an SVG `<a href>` around a mark as a hypermedia control
  that works without script. Not in spec 1; it needs a spec addition and a
  check that htmx processes `hx-*` attributes inside inline SVG.
- Brushing on band axes and two-dimensional brushing in scatter plots (spec
  1 brushes time and linear x axes only), and touch brushing (a long press).
- Gesture access beyond spec 1 (spec.md §8): touch brushing, and select
  and brush for mobile screen readers (an actionable role or a companion
  control). Pages use the plain HTML equivalents of spec.md §5.5 meanwhile.
- Keyboard parity for series: ArrowUp/ArrowDown to cycle the series in the
  active row (announced as "{name} {value}"), so Enter can send
  `hp-series` in modes x and band as a mouse click on a line does.
- Composing a slide with one still in flight (fold the running
  transform into `b`) instead of ending it at once; the effective duration
  (spec.md §6.8.3) already keeps fast streams smooth.
- Keyboard tooltips at the top edge of the plot cover the legend while open.
  Placing them below the point there would keep the legend visible.
- Browser tests in Firefox and WebKit. The Chrome suite runs in CI
  (playwright-core, a dev-only dependency outside the Go module and the
  published client, so the same tests can drive the other engines); the
  CI job would need those browsers installed.

**hx**

- A configurable SSE write deadline. Each write is bounded by a fixed 30 s
  (`writeTimeout` in `stream.go`); changing it needs new API, such as a
  `NewStream` option.
- A mechanical parity check of `sseparse_test.go` against hx-sse's own
  `parseSSE` (run under Node in CI), instead of the hand port alone.
- `Partial` cannot verify that its content is free of `</hx-`; the godoc
  requires trusted content. A checked variant could escape or reject it.

**Example dashboard**

- Smaller SSE messages. Each throughput message is a whole chart plus its
  data table, roughly 40–60 KB per second at the 1h range; a production page
  would send fewer points or update the table less often.
- Layout: grid rows stretch to the tallest card, so the throughput and
  scatter cards have empty space at the bottom next to the requests card
  (more with a detail open). `align-items: start` would trade that for
  uneven card bottoms.
- A filter change ends a zoom and closes the detail. Keeping the zoom
  would need hidden `from`/`to` inputs in the filter form.
- After a keyboard zoom the keyboard tooltip opens at the chart's top left
  and covers part of the zoom status line until the reader moves.
- Close empties `#requests-detail-status` instead of announcing that the
  detail closed (focus does return to the chart).
- At 390 CSS px the requests chart's band labels lose their "AM"/"PM"
  ("6:06…"); the tooltip, the table and the window links keep the full
  label. Shorter labels at narrow widths would fix it.

**Other examples**

- Browser coverage is a smoke test. `examples.browser.mjs` opens every
  example but `basic` (fixed port) and checks one update or gesture each;
  only the dashboard has gesture-by-gesture browser tests. The sensors'
  select and brush, the builds' brush and filters, and the Python and
  Node gestures are tested over HTTP only.
- The recipes are longer than one pattern needs: the live recipes'
  `main.go` files run 228 to 390 lines and the static ones 153 to 264,
  mostly the deliberately repeated server setup and the comments at each
  htmx attribute.
- The Python and Node renderers draw no dots on a linear x axis, so the
  checker skips the `scatter-xy` fixture for them.
- The Node example's zoom URL (`HX-Push-Url: /?from=…&to=…`) drops the
  `theme` parameter, so reloading a zoomed page returns to the system
  theme.
- `examples/python/test_wire.py` skips its `America/New_York` cases when
  Python has no time zone database (Windows without `tzdata`); CI on Linux
  runs them.
