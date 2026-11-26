# hyperplot design

hyperplot is a server-rendered SVG charting library for Go. The server renders
a chart to an SVG string; htmx 4 swaps it into the page and keeps it live through
polling, server-sent events, and morph swaps. A small optional client extension
adds hover tooltips, a crosshair, keyboard reading, and responsive re-rendering.
The core uses only the Go standard library.

This document is the contract the packages are built against. If code and this
document disagree, fix one of them in the same change.

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

Non-goals for v0: canvas/raster output, geographic maps, 3D, pie charts (pies are
almost always the wrong form), dual y-axes (never), and charts that need client-side
data processing.

## Module layout

```
github.com/ironystock/hyperplot   (module root, Go ≥ 1.24)
├── *.go                 package hyperplot: Chart, marks, render, theme/CSS, Table, Sparkline
├── scale/               public: Linear, Time, Band scales; ticks; number formatting
├── hx/                  public: htmx 4 server helpers (PTag, SSE stream, partials, sizing)
├── client/              hyperplot.js (htmx 4 extension) + Go package embedding it
├── internal/svg/        deterministic SVG writer + path builder
├── internal/textwidth/  text width estimation
├── examples/dashboard/  runnable live demo: go run ./examples/dashboard
├── examples/basic/      the README quick start: go run ./examples/basic
├── testdata/            golden SVG files (testdata/preview/: standalone copies)
└── docs/                DESIGN.md (this file), adr/, htmx.md guide, documentation tests
```

`internal/` keeps the low-level writer private until its API settles; it can be
promoted later without breaking anything.

## Status of existing code

Everything in the module layout above is implemented, documented and tested
against this document. Nothing has been tagged or published yet: there is no
module version on pkg.go.dev and no `hyperplot` package on npm. Known gaps
and planned work are listed under [Roadmap](#roadmap).

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
- `client/`: `hyperplot.js` 0.1.0 and the Go package that embeds and serves
  it. `node --test` covers the pure helpers and, through `fakedom.mjs`, the
  extension wiring; `client_test.go` covers the handler. Behavior in real
  browsers was verified by hand in headless Chrome (pointer, keyboard,
  morph and SSE refresh, standalone light and dark switching), not in
  Firefox or Safari, and no browser test runs in CI (see "Roadmap").
- `examples/dashboard/`: done; `main_test.go` exercises every endpoint. It
  carries the workarounds for the htmx 4.0.2 extension bugs listed in the
  pitfalls under "htmx 4 integration" below.
- `examples/basic/`: the README quick start. `docs/quickstart_test.go` fails
  when the two differ (other than the example's package comment).
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
more than 8 series (wrapping `ErrTooManySeries`), a non-finite fixed `Domain`, and
sizes out of range: `Width`/`Height` in [50, 32768], `FontSize` in [4, 96],
`BarWidth` in (0, 32768] (0 means the default everywhere). An explicit `ID` may
contain only ASCII letters, digits, `-`, `_`, `:` and `.`, because it is used in
`url(#…)` references. Validation messages name the mark by index and name.

Unnamed series are shown as "Series N" (N = position among data marks, from 1) in
the legend, tooltips, table, description and `data-hp-series`. Theme color values
are sanitized in `CSS()`: only characters CSS colors need survive, so a theme can
never break out of its declaration, and a value must be shaped like a color (#hex
of 3/4/6/8 digits, one keyword, or a call of the color functions, `var`, `calc`,
`min`, `max` or `clamp`, nested only within that list). Anything else, notably
`url()` and `image-set()`, which would make every viewer fetch a URL, is replaced
by the default theme's value for that token.

The default `ID` is `"hp-"` + the 8-hex FNV-1a of the Title, Description, axis
labels and every series' kind and name (it was the Title alone, which gave every
untitled chart the same id). It does not depend on data, size or styling, so a
chart keeps its id across live updates and autosizing. Charts that agree on all of
those inputs (untitled charts of unnamed series, one chart rendered for two
filters) still collide, and duplicate ids cross-wire `aria-labelledby`,
`clip-path` and the client's lookup; the `ID` godoc says to set an ID for them and
for every live chart.

`Render` must be safe to call concurrently on different `Chart`s and must not mutate
the `Chart` or its slices.

## Output structure (the DOM contract)

The client extension, the CSS, and the tests all rely on this structure. Change it
only deliberately.

```xml
<svg xmlns="http://www.w3.org/2000/svg" id="{ID}" class="hp [hp-anim]"
     viewBox="0 0 W H" width="W" height="H" role="img"
     aria-labelledby="{ID}-title {ID}-desc" tabindex="0" data-hp=""
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
  let idiomorph match nodes across updates, so bars animate rather than being
  replaced.
- **Determinism.** The same input gives byte-identical output: no map-iteration
  order, no time-dependent values, and numbers through `svg.Num`.
- **Graceful degradation.** Marks carry presentation attributes in terms of
  `currentColor` (lines: `fill="none" stroke="currentColor" stroke-width="2"`; grid:
  `stroke="currentColor" stroke-opacity="0.12"`; muted text: `fill="currentColor"
  fill-opacity="0.7"`; area: `fill="currentColor" fill-opacity="0.1"`). With no
  stylesheet the chart is legible in monochrome; the stylesheet overrides these with
  theme colors. **Do not** use `style=""` attributes (CSP).
- **Tooltip payload.** `<metadata class="hp-tips">` holds JSON, escaped exactly as
  `encoding/json` escapes it (`<>&` become `<` etc., U+2028/9 and invalid
  UTF-8 are handled), plus `￾`/`￿`, the only characters valid UTF-8 can
  carry that XML forbids, so the JSON is written raw into the XML. It is encoded by
  hand (`tipsjson.go`) because reflection-based `encoding/json` dominated render
  time; tests compare it with `encoding/json` byte for byte and fuzz it.
  Coordinates are in viewBox units, rounded to 2 decimals.
  ```json
  {"mode":"x",  "xs":[x0,x1,…], "bw":0, "top":T, "bottom":B, "rows":[{"t":"Mar 3, 14:05","v":[[slot,"name","12.3 ms"],…]}]}
  {"mode":"band","xs":[centers…], "bw":step, "top":T, "bottom":B, "rows":[…]}
  {"mode":"xy", "pts":[[x,y],…], "rows":[{"t":"label or x value","v":[[slot,"name","y value"]]}]}
  ```
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
- **X:** linear → extent then `Nice`; time → exact extent (or `TimeDomain`);
  band → categories in order of first appearance across marks. Band X labels thin
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
  labels (max a quarter of the width) are truncated with an ellipsis. When
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
  `--hp-axis`, `--hp-series-1..8` are declared on `.hp, .hp-tip, .hp-table`.
- Dark mode is a **selected** palette, not an inversion, applied two ways:
  `@media (prefers-color-scheme: dark) { :root:where(:not([data-theme="light"])) .hp {…} }`
  and `:root[data-theme="dark"] .hp {…}` (and likewise for `.hp-tip` and `.hp-table`).
- `.hp-s1`…`.hp-s8 { color: var(--hp-series-N) }`. Marks use `currentColor`.
- `.hp { max-width: 100%; height: auto; overflow: visible }`.
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

- `role="img"`, `aria-labelledby` points to `<title>` and `<desc>`, and
  `tabindex="0"` makes the chart keyboard-reachable (the client binds arrow keys).
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

## Sparkline

`Sparkline` renders a compact, axis-free SVG (default 120×28) for stat tiles: a
2 px line, an optional area wash, and an accent end dot. It is `role="img"` with an
`aria-label` built from Label plus first, min, max and last values, each with
`Unit` (shortened past 32 runes) and formatted by `Format` when set, so a tile
showing "212 ms" is not read as "212". NaN creates gaps. Like `Chart`, it has
`WriteTo`, `HTML` (so templates never cast bytes to `template.HTML` themselves)
and `Component`.
It uses the same classes (`hp`, `hp-s1`) so it is themed by the same stylesheet.
The y extent is the data's own (no forced zero) with 4 px of padding; the end dot
is `circle.hp-end` with r = 2.5. Width and Height must be in [10, 32768]. No data
gives an empty frame labelled "{Label}: no data". It has no ids, so it can be
repeated freely on a page.

## htmx 4 integration (package hx)

htmx 4.0.2 is current (released 2026-08-28). Key facts verified against
four.htmx.org:
- Requests use `fetch()`. Inheritance is explicit (`hx-target:inherited`).
  Events are named `htmx:phase:action` (for example `htmx:after:swap`). All
  responses swap except 204/304 (`config.noSwap`; see the error-page pitfall
  below).
- Core swaps include `innerMorph`/`outerMorph` (idiomorph), which preserve node
  identity. **This is the main live-update mechanism.**
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
```

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
  event that matches the element's `hx-sse:close`.

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
  of a `<details>` the reader expanded. `"morphIgnore":["data-htmx-powered","open"]`
  in the htmx config keeps it (the array replaces the default, so keep
  `data-htmx-powered`).
- **Error pages the handler never wrote are swapped too** (a proxy's 502, the
  503 of `http.TimeoutHandler`, middleware 401/403, ServeMux 404/405), and
  `makeFragment` copies their `<title>` into `document.title`. hx-ptag then
  pins them: it updates the stored tag only from a response with an `HX-PTag`
  header, so after recovery the next poll sends the last good tag and gets a
  304. The guard is client-side: `"noSwap":[204,304,"4xx","5xx"]` (htmx
  matches `Nxx` patterns; the array replaces the default `[204, 304]`). The
  guide and the dashboard use one meta tag for both:
  `<meta name="htmx-config" content='{"noSwap":[204,304,"4xx","5xx"],"morphIgnore":["data-htmx-powered","open"]}'>`.
- A URL that serves a page or a fragment by `hx.IsPartial` sends
  `Vary: HX-Request, HX-Request-Type`, so caches keep the two apart.
- An element's `hx-get` is read at request time, so a morph that changes it
  (new filters in the URL) takes effect on the next poll; any changed `hx-*`
  attribute also re-initializes the element, which re-fires `load`.

## Client extension (client/hyperplot.js)

Plain ES2020 in a single file with no dependencies and no build step, under 8 KB
minified: 7,929 bytes with `terser -c -m` (3.7 KB gzipped) for 0.1.0, which
leaves about 260 bytes of the budget. It was 6 KB before the persistent status
region, content-box autosize and the xy focus ring were added, so a new feature
needs trimming elsewhere or a deliberate, documented raise of the budget. No
test enforces the size, because measuring it needs a minifier and the client
has no dependencies. It works with or without htmx (pointer and keyboard features use event
delegation on `document`). When `htmx` exists it registers
`htmx.registerExtension("hyperplot", …)`.

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
  global `div.hp-tip` appended to `document.body`, `position: fixed`, flipping to
  stay inside the viewport. Build rows with `textContent` only. When the pointer
  leaves the chart (`pointerout` to outside it), hide everything.
  Modes `x` and `band` respond only between `top` and `bottom` (and, for `band`,
  inside the outer bands). Touch uses pointer events: a tap shows the tooltip,
  which stays until a tap outside the chart (the hit radius doubles to 24 px).
  Escape hides any open tooltip (WCAG 1.4.13). With
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
  the next step.
- **Keyboard.** When an `svg.hp` has focus, ArrowLeft and ArrowRight step through
  points, Home and End jump to the ends, and Escape hides. Anchor the tooltip to the
  point. Modes x and band step in index order (left to right). Mode xy lists its
  points series by series in data order, so the client steps through them in
  spatial order instead (`keyOrder`): by viewBox x, then bottom to top at the same
  x, then by index, leaving out points without a finite position.
- **Survives morph.** In the extension hooks `htmx_after_swap` and
  `htmx_sse_after_message` (the `htmx:after:swap` and `htmx:sse:after:message`
  events; there are no separate DOM listeners, so an app that restricts
  `htmx.config.extensions` must include `hyperplot`), re-apply the active
  hover or focus state to the updated chart, so polling does not make the tooltip flicker or vanish. A
  keyboard point is kept by its row heading (`t`), nearest its old index, so a
  sliding window keeps the reader on the same x; when that row is gone the old
  index is kept, clamped. Refreshing updates the visual tooltip but never
  announces, so a focused live chart does not re-read its row on every poll or
  SSE message. A chart replaced by a non-morph swap is found again by its `id`.
  If it is gone, the tooltip hides.
- **Responsive.** Elements with `data-hp-autosize` are observed with a
  `ResizeObserver`. When the width changes by ≥ 8 px (debounced 150 ms), dispatch
  the `hp:resize` event on the element (it does not bubble; `detail` holds
  `width`/`height`), so authors can write
  `hx-trigger="load, hp:resize"`. In `htmx_config_request`, when the source element,
  or else the request target, has a closest `[data-hp-autosize]`, add `hp-w`
  (the rounded content width: `clientWidth` less padding; omitted if 0, and never
  overriding a value already in the request, in its body or in the query string of
  `ctx.request.action`) to `ctx.request.body`. The URL check matters: for GET and
  DELETE, htmx 4.0.2 deletes every URL parameter whose name is in the body before
  appending the body, so a body `hp-w` would silently replace an `hp-w` the author
  wrote into `hx-get` or `hx-sse:connect`. `hp-h` (the
  content height) is added, and height changes fire `hp:resize`, only for
  `data-hp-autosize="both"`: a wrapper without a CSS height is as tall as the
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
  Test them with `node --test` (`client/hyperplot.test.mjs`, with no
  dependencies). `client/dom.test.mjs` runs the whole script in a `node:vm`
  context against `client/fakedom.mjs`, a minimal fake browser (elements,
  selectors, events, `getScreenCTM`, `ResizeObserver`, timers, an htmx stub), to
  cover the extension hooks, autosize, pointer and keyboard handling, the status
  region and refresh. A change to the wiring needs a test there.
- The Go package `client` uses `//go:embed hyperplot.js` into an unexported
  string and exposes `JS() []byte` (a new copy on each call) and
  `Handler() http.Handler`, which serves it with the correct Content-Type, an
  ETag, and `Cache-Control`. That lets Go users serve it without npm. The script
  is not an exported `[]byte` variable because the strong ETag is computed once
  at init: a caller writing into a shared slice would change the served bytes
  under the same validator, and browsers revalidating would keep a stale copy.
- `client/package.json` is prepared for publishing to npm as `hyperplot` (with
  `"files"`, `"exports"`, MIT license, and keywords). Do not publish. Its
  `version` is the single source of truth: the `/*! hyperplot X.Y.Z | MIT */`
  banner and `globalThis.hyperplot.version` in `hyperplot.js` must match it, and
  `dom.test.mjs` fails when they do not, so a release bumps all three.

## Live-update patterns (to document in docs/htmx.md and to show in the example)

1. **Polling with ptag:**
   `<div hx-get="/charts/latency" hx-trigger="every 2s, hp:resize" hx-swap="innerMorph" data-hp-autosize hx-ptag="…">`.
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
  counts and time zone, and rejects `NaN`/`Inf` in the output and x ticks
  outside the plot. The zones include ones whose clocks change
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

**Next round: maintainer-reported bugs (2026-11-26), fix first**

1. **Example tables don't update with their charts.** In the live dashboard
   the "View as table" contents stay stale while the charts above them
   change. The server side is not at fault: a latency poll response
   (`/charts/latency?…`) does carry the fresh `<table>`, because the
   `chart-body` template renders the SVG, a hidden input and
   `<details>…{{.Table}}</details>` together. So the stale view comes from
   the client during the `innerMorph`. Suspects:
   - the `morphIgnore: ["data-htmx-powered","open"]` config on the page;
   - idiomorph matching id-less `<tr>` rows by position;
   - the SSE `stream` body sending only the SVG.

   Reproduce in a real browser, open and closed `<details>`, and check all
   three live charts (polled latency, polled requests, SSE throughput). Add
   a browser-level regression check.
2. **Sliding-window line charts redraw instead of shifting (latency
   chart).** When the time window advances, the CSS `transition: d .35s`
   interpolates each vertex toward the y value of the point that replaced
   it. The whole line wobbles in place instead of scrolling left. The fix
   is the classic sliding-line technique:
   - The server marks each series (or the plot) with its x-domain start,
     e.g. a `data-hp-x0` value in viewBox pixels per unit time.
   - The client compares the old and new domain start in `htmx_before_swap`
     and `htmx_after_swap`. When the window slid by Δ px, it suppresses the
     `d` transition for that swap, sets the new paths to `translateX(Δ)`
     (inside the existing clip path) and transitions the transform to 0.
   - A real value change with an unchanged domain keeps the current `d`
     tween.
   - Respect `prefers-reduced-motion`.
   - Areas and the end dot move with the line, and grid and tick labels
     either shift with it or update without animating.
   - Consider making it the default for time x axes, with a per-chart
     opt-out.

**Releasing**

- Tag the first module version (`v0.1.0`) so the API reference appears on
  pkg.go.dev; the README links to it already.
- Publish `client/` to npm as `hyperplot` (`package.json` is ready), then add
  the CDN URL to `client/README.md`. A release bumps `package.json`, the
  banner and `globalThis.hyperplot.version` together (`dom.test.mjs` checks
  them) and moves the changelog's Unreleased section under the version.
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

- Path tweening for Safari. Safari does not transition the CSS `d`
  property, so animated bars and areas snap there instead of moving. A small
  opt-in tween in the client could interpolate `d` between morphs; it must fit
  the size budget (above), which is almost used up.
- `touch-action: pan-y` on `.hp` in the stylesheet, so a sideways finger drag
  scrubs the tooltip along the chart instead of being taken by the browser.
- Keyboard tooltips at the top edge of the plot cover the legend while open.
  Placing them below the point there would keep the legend visible.
- Browser tests in CI (Playwright or puppeteer as a dev-only dependency kept
  outside the Go module and the published client) covering Chromium, Firefox
  and WebKit: tooltips, keyboard reading, morph refresh, SSE and autosize.
  Today the DOM behavior is tested against `fakedom.mjs` and was checked by
  hand in headless Chrome only.

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
- The stream sends no `retry:` field, so reconnects use hx-sse's 500 ms
  default; it could show `Stream.Retry` as the guide recommends.
