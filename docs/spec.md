# hx-plot wire specification

**Spec version 1** · Status: draft for rev 2 · 2026-11-26

This document defines how a server and the `hx-plot` htmx 4 extension (npm
`htmx-ext-plot`, file `hx-plot.js`) talk to each other about charts: the
markup a server sends, the attributes page authors write, the request
parameters the extension adds, the events it dispatches and what it does
during a swap. A server written in any language can implement it from this
document alone. The Go module `github.com/ironystock/hyperplot` is the
reference server, `client/hx-plot.js` is the reference client, and
[`testdata/conformance/`](../testdata/conformance/README.md) holds fixtures
that pin this text to bytes.

Why the extension is the product and Go is the reference: see
[ADR 0005](adr/0005-htmx-community-first.md). How the Go library implements
it: [DESIGN.md](DESIGN.md).

## Contents

1. [Conventions](#1-conventions)
2. [Conformance](#2-conformance)
3. [The chart document (Level 1)](#3-the-chart-document-level-1)
4. [Live updates (Level 2)](#4-live-updates-level-2)
5. [Gestures (Level 3)](#5-gestures-level-3)
6. [The extension](#6-the-extension)
7. [Security considerations](#7-security-considerations)
8. [Accessibility considerations](#8-accessibility-considerations)
9. [Names, versions and changes from pre-spec output](#9-names-versions-and-changes-from-pre-spec-output)
10. [Appendix A: FNV-1a keys](#appendix-a-fnv-1a-keys)
11. [Appendix B: an exchange, and helpers in Python](#appendix-b-an-exchange-and-helpers-in-python)
12. [Appendix C: class names (informative)](#appendix-c-class-names-informative)

## 1. Conventions

The key words MUST, MUST NOT, REQUIRED, SHALL, SHALL NOT, SHOULD, SHOULD NOT,
RECOMMENDED, NOT RECOMMENDED, MAY and OPTIONAL are to be interpreted as
described in BCP 14 (RFC 2119, RFC 8174) when, and only when, they appear in
all capitals.

- **Server**: whatever produces chart markup and answers requests.
- **Client**: the extension script running in the browser.
- **Author**: the person writing the page that contains charts.
- **Chart**: one `<svg class="hp">` element and its content. A **frame** is
  one rendering of a chart; live charts replace frames with newer frames.
- **Plot**: the rectangle inside the chart where data is drawn.
- **Data mark**: a line, area, bar or dot series. Reference lines (rules)
  are not data marks.
- **viewBox units**: the chart's own coordinate system (`viewBox="0 0 W H"`).
  **CSS px**: page pixels.

### 1.1 Value forms

These forms are used wherever this document says so. Producers MUST write
them exactly; consumers MUST accept the wider input stated.

| Form | Producers write | Consumers accept |
|---|---|---|
| **number** | a finite JSON number (RFC 8259 §6) in its shortest round-trip form, formatted like ECMAScript `Number.prototype.toString`: `1`, `2.5`, `0.001`, `1e+21`, `1e-7` | any finite JSON number |
| **epoch ms** | a number: milliseconds since 1970-01-01T00:00:00Z, fractional when needed | the same |
| **time value** | an RFC 3339 `date-time` in UTC with `Z`, in years 0001 to 9999; fractional seconds only when non-zero, without trailing zeros, at most 9 digits: `2026-03-03T14:05:00Z`, `2026-03-03T14:05:00.25Z` | any RFC 3339 `date-time` (any offset, any number of fraction digits) |
| **linear value** | a number | a number |
| **category** | the category string itself | the same |
| **key** (of an x value) | the time value, linear value or category of that x, by axis kind | the same; clients treat keys as opaque strings |

The **number** form is required where a value is compared as text or read
back by a server: row keys `k`, the `data-hp-*` geometry attributes, and
`hp-x0`/`hp-x1` on a linear axis. A key written with a language's default
float formatting (Python's `str(2.0)` is `"2.0"`) is not the number form and
will not match. Coordinates in the hover metadata (§3.5) MAY be any finite
JSON number.

Every value in the **number** form matches this regular expression
(ECMAScript and RE2 syntax), which the fixtures use to check it:

```
^-?(0|[1-9][0-9]*)(\.[0-9]*[1-9])?(e[+-][1-9][0-9]*)?$
```

It rejects a trailing `.0`, trailing zeros in a fraction, leading zeros, `E`
and an exponent without a sign. Matching it is necessary, not sufficient: the
form is also the shortest one (`1000`, not `1e+3`; `0.1`, not
`0.10000000000000001`).

Go's `encoding/json` writes `float64` exactly as **number** requires, and
`time.Time.UTC().Format(time.RFC3339Nano)` writes a **time value** for times
in years 1 to 9999 (outside them it writes forms such as `-0249-…` and
`12000-…`, which are not RFC 3339, and year `0000`, which Python's
`datetime` cannot read; a server rejects such data or leaves it out).
Appendix B has Python equivalents.

## 2. Conformance

### 2.1 Server levels

Levels are cumulative: a Level 2 server also meets Level 1.

| Level | Name | A server at this level | The page gets |
|---|---|---|---|
| 1 | Static | emits charts per §3 | native `<title>` tooltips on bars and dots without script; with the client: tooltips, crosshair, band highlight, keyboard reading and announcements, on any page, with or without htmx |
| 2 | Live | also emits §4 identity, geometry and keys, and serves frames over htmx polling or SSE | morph updates that keep hover and keyboard state, sliding windows, tables that keep their place, open disclosures that stay open, 304s for unchanged polls (with `hx-ptag`) |
| 3 | Interactive | also reads the §5 gesture parameters | drill-down on click or Enter, and brush-to-zoom with history |

A server SHOULD state its level. The Go reference meets Level 3.

### 2.2 Client

The reference client implements every level. Another client claiming spec 1
MUST implement §6 for each level it claims and MUST ignore attributes, JSON
fields and parameters it does not know.

### 2.3 Versions

The root attribute `data-hp` carries the spec version, `1` for this
document. Additions that a version-1 client can ignore (a new optional
attribute, a new JSON field) keep version 1. Any change that would make a
version-1 client misread a chart increments it. A client that meets a chart
whose `data-hp` is a version it does not know MUST NOT use that chart's
geometry (§4.2) and MAY still offer Level 1 behaviour if the metadata
parses. A chart whose `data-hp` is empty or missing is pre-spec output
(§9.2) and gets Level 1 behaviour only.

## 3. The chart document (Level 1)

### 3.1 Embedding

A chart is an `<svg>` element in the SVG namespace, inlined in an HTML page
or fragment. Its markup MUST be well-formed XML (so the same bytes also work
as a standalone `.svg` file) and MUST parse to the same tree under the HTML
parser (no constructs the two parsers read differently; §3.5 and §3.6 cover
the ones that matter). It MUST NOT contain a raw CR byte (§3.6), which keeps
it intact in SSE messages and in repositories that normalize line endings.
Fragments are served as `text/html; charset=utf-8`. A chart saved as a file
MAY be served as `image/svg+xml`; shown on its own or through `<img>`, it
gets only the native fallback (§3.7).

### 3.2 The root element

| Attribute | Value | Requirement |
|---|---|---|
| `xmlns` | `http://www.w3.org/2000/svg` | MUST |
| `id` | the chart id (§4.1): only ASCII letters, digits, `-`, `_`, `:` and `.` (it appears in `url(#…)`); it SHOULD start with a letter | MUST |
| `class` | space-separated; MUST include `hp`; `hp-anim` opts the chart in to animated updates (§6.8) | MUST |
| `viewBox` | `0 0 W H`, W and H positive numbers | MUST |
| `width`, `height` | W, H | SHOULD |
| `role` | `img` | MUST |
| `aria-labelledby` | `{id}-title` | MUST |
| `aria-describedby` | `{id}-desc` (not also in `aria-labelledby`, which would make browsers read the description twice: in the name and again as the description) | MUST |
| `tabindex` | `0` | MUST |
| `data-hp` | `1` | MUST |
| `data-hp-plot` | the plot rectangle (§4.2) | MUST at Level 2 |
| `data-hp-x` | the x scale (§4.2) | MUST at Level 2 when the chart has x values; MAY be omitted otherwise |
| `data-hp-y` | the y scale (§4.2) | MUST at Level 2 |
| `font-family`, `font-size` | presentation defaults | SHOULD |

The root MUST NOT carry `style`, `transform` or any `on*` attribute.

### 3.3 Required and conventional descendants

The first child element MUST be `<title id="{id}-title">` holding the
chart's accessible name, and the second child element `<desc
id="{id}-desc">` holding a short factual description (chart type, series,
the range of each axis). Whitespace text between elements is allowed; it
SHOULD be the same in every frame, because the morph matches text nodes by
position. The rest SHOULD follow this order, which keeps morph matching
stable (§4.4):

```xml
<svg …>
  <title id="{id}-title">…</title>
  <desc id="{id}-desc">…</desc>
  <defs><clipPath id="{id}-clip"><rect …/></clipPath></defs>
  <g class="hp-legend" aria-hidden="true">…</g>                    when shown
  <text class="hp-axis-label hp-ylabel">…</text>                   when labelled
  <g class="hp-grid" aria-hidden="true">…</g>
  <rect class="hp-band-hl" visibility="hidden" …/>                 mode band
  <g class="hp-marks" clip-path="url(#{id}-clip)">
    <g id="{id}-s-{key}" class="hp-series hp-s{N}" data-hp-series="{name}">…marks…</g>
  </g>
  <g class="hp-axis hp-axis-x" aria-hidden="true">…<text class="hp-tick">…</text></g>
  <g class="hp-axis hp-axis-y" aria-hidden="true">…</g>
  <g class="hp-rules" aria-hidden="true">…</g>
  <g class="hp-labels" aria-hidden="true">…</g>
  <g class="hp-hover" aria-hidden="true">
    <line class="hp-crosshair" visibility="hidden" …/>               mode x
    <metadata class="hp-tips">{JSON}</metadata>
  </g>
</svg>
```

What the client relies on, and so what a server MUST emit:

| Element | Requirement |
|---|---|
| `g.hp-hover` | exactly one, holding exactly one `metadata.hp-tips` (§3.5) |
| `line.hp-crosshair` in `g.hp-hover` | in mode `x`: exactly one, `visibility="hidden"` |
| `rect.hp-band-hl` | in mode `band`: exactly one, `visibility="hidden"`, before `g.hp-marks` so it paints under the bars |
| `g.hp-series` | one per data mark, at most 8 per chart, inside `g.hp-marks`, with class `hp-s{N}` and `data-hp-series` holding the series' full display name |

N, the series' **slot**, is an integer from 1 to 8, unique in the chart; the
stylesheet colours series by slot. Slots SHOULD be assigned in series order
starting at 1, skipping any slot an author pinned to a series (pinning keeps
a series' colour when other series come and go).

**Positions are in root viewBox units.** The client moves elements with the
CSS `transform` property during a slide (§6.8.3) and maps the pointer to
viewBox units, and an inline CSS transform replaces an SVG `transform`
attribute. So the root, `g.hp-marks`, `g.hp-labels`, `g.hp-axis-x` and
`g.hp-hover` and everything inside them, `rect.hp-band-hl`, and any element
of class `hp-xa` (§6.8.3) MUST NOT carry a `transform` attribute, and those
groups MUST NOT contain `svg`, `symbol` or `use` elements (no nested
viewports or offsets). Position these elements with `x`, `y`, `cx`, `cy`,
`x1`…`y2`, `width`, `height` and `d`. A server that rotates crowded x tick
labels cannot do it with `transform` in spec 1; it SHOULD thin the ticks
instead. Elsewhere (the legend, the y axis) `transform` is allowed.

What the stylesheet and the slide (§6.8.3) rely on, and so what a server
SHOULD use when it draws these things: `path.hp-line`, `path.hp-area`,
`path.hp-bar`, `circle.hp-dot` inside series groups; `g.hp-labels` for
direct labels (`text.hp-direct`), dot labels (`text.hp-dot-label`) and end
dots (`circle.hp-end`); `text.hp-tick` in `g.hp-axis-x` for x tick labels
(directly or in a plain `g`); the plot's clip path on `g.hp-marks`, not on a
`g.hp-series` (a slide may move a series group, and its own clip with it);
and the group classes above. Decorative groups SHOULD carry
`aria-hidden="true"`. [Appendix C](#appendix-c-class-names-informative)
lists every class the reference stylesheet styles.

The client adds two elements of its own to `g.hp-hover`:
`circle.hp-focus` (the active scatter point) and `rect.hp-brush` (§6.6). A
server MUST NOT emit them; a morph removes them and the client re-creates
them.

### 3.4 Presentation

Marks SHOULD carry presentation attributes in terms of `currentColor` (for
example `stroke="currentColor"` on lines), so a chart is legible in
monochrome without a stylesheet. The stylesheet colours series through
`.hp-sN { color: … }`. The reference stylesheet ships as `hx-plot.css` in
the npm package and is `hyperplot.Stylesheet()` in Go; servers in other
languages SHOULD serve that file rather than write their own. A stylesheet
that replaces it MUST keep `.hp { touch-action: pan-y pinch-zoom }`: without
it a browser takes a sideways finger drag on a chart as a pan and cancels
the pointer, and the touch scrub (§6.5) never happens.

### 3.5 Hover metadata

`metadata.hp-tips` holds one JSON object describing where the chart can be
hovered and what each position reads. Its shape depends on `mode`:

```json
{"mode":"x",   "xs":[24,126,228], "bw":0,     "top":8,  "bottom":98, "rows":[ROW, …]}
{"mode":"band","xs":[78.61,178.2],"bw":99.59, "top":26, "bottom":98, "rows":[ROW, …]}
{"mode":"xy",  "pts":[[24,98],[126,26]],                             "rows":[ROW, …]}
ROW = {"t":"Mar 3, 2026 2:10 PM", "k":"2026-03-03T14:10:00Z", "v":[[1,"p50","45 ms"], …]}
```

The mode follows from the chart's data marks: **`band`** if it has any bar;
otherwise **`x`** if it has any line or area, or no data marks at all;
otherwise (only dots) **`xy`**.

| Field | Mode | Type | Meaning |
|---|---|---|---|
| `mode` | all | `"x"`, `"band"`, `"xy"` | `x`: the pointer snaps to the nearest x; `band`: the band under the pointer; `xy`: the nearest point in 2-D |
| `xs` | x, band | array of numbers | x positions in viewBox units, **non-decreasing**: data x values in mode x (only those where some series has a value), band centres in mode band, in axis order |
| `bw` | x, band | number ≥ 0 | 0 in mode x; the band step in mode band |
| `top`, `bottom` | x, band | numbers | the plot's vertical span in viewBox units; the pointer is active only between them |
| `pts` | xy | array of `[x, y]` | point positions in viewBox units |
| `rows` | all | array of ROW | exactly one per entry of `xs` (or `pts`), in the same order |
| `t` | ROW | string | the heading, preformatted; may be empty |
| `k` | ROW | string, a **key** (§1.1) | the row's x value (Level 2: MUST; Level 1: MAY be omitted) |
| `v` | ROW | array of `[slot, name, value]` | slot: the series' slot (1–8; clients MUST tolerate 0 and show no colour key); name: the series name (MAY be shortened); value: preformatted, `"–"` (U+2013) when missing |

Rules:

- In modes `x` and `band` every row lists **every** series, in series
  order. In mode `xy`, `pts` and `rows` list the plotted points series by
  series in mark order, and in data order within a series; a row lists only
  the point's own series, and `k` is the point's x value.
- A point with a missing x or y value is not plotted and has no entry.
- A point whose x lies outside the x domain in `data-hp-x` (§4.2), for
  example the overscan point of a sliding window (§4.4), is drawn only
  (clipped). It MUST NOT appear in `xs`, `pts` or `rows`, and a column of
  `xs` exists only for x values inside the domain. Otherwise the pointer,
  which has no horizontal bound in mode `x`, could land on data that is not
  visible.
- Coordinates SHOULD be rounded to 2 decimals. All numbers MUST be finite.
- Values are preformatted by the server, with units. The client never
  formats data.
- The JSON MUST be written with `<`, `>` and `&` escaped as `\u003c`,
  `\u003e` and `\u0026`, and MUST escape the characters XML forbids
  (C0 controls other than tab, LF and CR, and U+FFFE, U+FFFF) as `\uXXXX`.
  The text then needs no XML escaping, reads the same under the XML and
  HTML parsers, and can never close the element. JSON's own escapes
  (`\n`, `\r`, `\t`) are fine.
- Strings in the JSON MUST be valid Unicode. A lone surrogate has no UTF-8
  encoding, so a server whose strings can hold one (Python and JavaScript
  strings can; a Go string cannot) MUST replace each with U+FFFD before it
  writes the JSON, as invalid UTF-8 becomes U+FFFD (§3.6). The same applies
  to the strings that keys are hashed from (§4.1, Appendix A), so a key and
  the `k` or name it came from agree. Appendix B's `wire_text` does this.
- Consumers MUST ignore unknown fields and MUST NOT fail on missing
  optional ones (`bw`, `top`, `bottom` default to none).

### 3.6 Text and safety

Every string that comes from data (series names, categories, labels,
titles, units) MUST be escaped for its context. HTML escapers such as
Django's `escape` and Jinja's autoescape are not enough: they leave control
characters and raw CRs in place.

| Context | Escape |
|---|---|
| element text | `&` → `&amp;`, `<` → `&lt;`, `>` → `&gt;`, CR → `&#13;` |
| attribute value (double-quoted) | as element text, plus `"` → `&quot;`, TAB → `&#9;`, LF → `&#10;` |
| both | characters outside the XML 1.0 `Char` production (C0 controls other than TAB, LF and CR; lone surrogates; U+FFFE and U+FFFF) MUST be dropped; invalid UTF-8 MUST become U+FFFD |
| `metadata.hp-tips` | the JSON escaping of §3.5 |

Character references keep TAB, LF and CR intact through XML attribute
normalization, so an attribute decodes to the same string under both
parsers. A raw CR in element text would be read as LF by both parsers, and
would split an SSE `data:` line (§4.5). Note that a dropped character makes
`data-hp-series` differ from the name in the JSON: a series named
`"a\x01b"` arrives in `hp-series` (§5.1) as `"ab"`, while row keys, which
come from the JSON, round-trip exactly.

A chart MUST NOT contain `<script>`, `<foreignObject>`, `style` attributes,
`on*` attributes, or references to external resources; `url(#…)` references
point inside the chart only. These rules keep charts safe under a strict
Content Security Policy and with user-controlled names. The
[`escaping` fixture](../testdata/conformance/escaping/checks.json) checks
every channel. Appendix B has `xml_text` and `xml_attr` in Python.

### 3.7 Native fallback

Each bar and each dot SHOULD have a `<title>` child,
`"{series} · {x label}: {value}"`, so browsers show a native tooltip with no
script, in standalone files and through `<img>`. Lines and areas have no
native tooltip, so for a line chart without the client the table (§3.8) is
the only way to read values.

### 3.8 The table equivalent

A server SHOULD offer a data table next to each chart (for example inside
`<details>`), because the table is the accessible equivalent of the chart
and, for line charts, the only fallback without script. When it does, the
table:

- MUST be `<table class="hp-table">` with a `<caption>` (the chart title),
  a `<thead>` row of `<th scope="col">` and a `<tbody>`;
- in a chart with modes `x` or `band`, MUST have one row per entry of the
  metadata's `rows`, in the same (axis) order, each headed by
  `<th scope="row">` with the x label, and SHOULD have one column per
  series in series order;
- in a chart of only dots, MUST have one row per point, in the metadata's
  order (series by series, data order); its columns and where its
  `<th scope="row">` sits are the server's choice;
- MUST NOT have rows for points outside the x domain (§3.5);
- MUST write a missing value as
  `<td><span aria-hidden="true">–</span><span class="hp-sr"> no data</span></td>`,
  because screen readers skip a bare en dash;
- MUST use the same formatted values as the metadata;
- at Level 2, MUST carry the ids of §4.1.

## 4. Live updates (Level 2)

### 4.1 Identity

htmx 4's morph matches an element to its new version by id first, then by
equality, then by position. Ids are therefore part of the wire. A server at
Level 2:

- MUST give every chart an id that is unique on the page and stable across
  frames;
- MUST give every series group an id, `{id}-s-{key}`, unique in the chart
  and stable for as long as the series exists;
- MUST give every bar an id, `{id}-b-{seriesKey}-{catKey}`;
- MUST give a table `id="{id}-table"` and every body row an id,
  `{id}-r-{rowKey}`, stable for as long as the row's x value is in the
  table (and a page MUST NOT show the same chart's table twice);
- MUST NOT reuse an id for a different series, bar or row, and MUST keep
  every id unique, even when two series share a name.

Stable row ids keep a screen reader's reading position on the same data row
while rows come and go, and let the morph move rows instead of rewriting
their text.

The key derivation is RECOMMENDED, so that ids agree with the reference
server, the fixtures and authors' CSS. A key is the 32-bit FNV-1a hash of a
UTF-8 string, written as 8 lowercase hex digits (Appendix A); lone
surrogates in the key input are first replaced by U+FFFD (§3.5). Keys are
assigned in order within a **key set**; when a string's hash is already
taken in its set (a repeated string, or a hash collision), the key is the
hash of the string followed by `#` and `n`, for the smallest `n ≥ 1` whose
hash is free.

| Id | Key set, in assignment order | Key input |
|---|---|---|
| series `{key}` | the chart's series, in mark order | the series name; for an unnamed series `#` followed by its index among data marks, from 0 |
| bar `{catKey}` | the chart's categories, in order of first appearance across series in mark order | the category |
| table row `{rowKey}`, modes x and band | the table's rows, in row order | the row's **key** (§1.1); in mode band this gives the same keys as the categories, so a band row and its bars share `{catKey}` |
| table row `{rowKey}`, charts of only dots | per series, that series' rows in row order; the id is `{seriesKey}-{key}` | the row's key |

For example, two series both named `p50` get `2e119f3a` (`p50`) and
`4e3e42de` (`p50#1`); see the
[`collisions` fixture](../testdata/conformance/collisions/checks.json).

### 4.2 Geometry attributes

Three root attributes tell the client how the chart maps data to the plot,
so it can tell a sliding window from a change of values (§6.8.3) and turn a
drag into data values (§6.6). They MUST describe the scales actually used to
draw the frame.

```
data-hp-plot = left SP top SP right SP bottom        ; viewBox units, left < right, top < bottom
data-hp-x    = "time" SP d0 SP d1                    ; epoch ms, d0 < d1
             / "linear" SP d0 SP d1                  ; linear values, d0 < d1
             / "band"                                ; categories; no numeric domain
data-hp-y    = "linear" SP y0 SP y1                  ; y0 < y1
```

All numbers are in the **number** form. `d0` maps to `left` and `d1` to
`right`, `y0` to `bottom` and `y1` to `top`; values between map linearly. A
degenerate domain (one point, or all values equal) MUST be widened, in any
way, so that `d0 < d1` and `y0 < y1`. Plot coordinates SHOULD be rounded to
2 decimals like other coordinates; domain values MUST NOT be rounded (a
sliding window compares them exactly). The plot is the unclipped data area,
not the clip rectangle (which may be padded). "Describe" is exact: a key
mapped through these attributes lands where its row is drawn. A float64
of epoch ms resolves only about 0.24 µs near 2026, so for a time domain a
few microseconds wide, rounded ends describe a different scale (by tens of
pixels); such a server meets this rule by drawing through the numbers it
writes (the reference server rounds the ends outward to representable
numbers). For example, the reference `line-time` fixture has
`data-hp-plot="24 8 228 98" data-hp-x="time 1772546400000 1772547600000" data-hp-y="linear 40 46"`.

New kinds (`log`, for example) will be new keywords; a client MUST treat an
unknown kind like `band` (no slide, no brush). Spec 1 defines mode `band`
only with `data-hp-x="band"`. Bars on a time or linear axis (a histogram,
time-bucketed counts) are not defined in spec 1; a server that draws them
SHOULD use mode `x`, which gives them a crosshair.

### 4.3 Row keys

At Level 2 every metadata row MUST carry `k` (§3.5). The client keeps a
keyboard reader on the same x by key across frames, and select and brush
send keys back to the server (§5). Keys MUST be unique among the rows of a
chart in modes `x` and `band` (one row per x value). In mode `xy` two points
of a series may share a key. An empty category is a valid key, but the
client sends no empty parameter (§6.7), so such a row cannot be selected.

### 4.4 Frame stability

- A server SHOULD produce the same bytes for the same data (no clock reads
  or map-iteration order in the output), so `HX-PTag` and 304s work.
- Element order and structure SHOULD stay the same from frame to frame,
  apart from added and removed data. Id-less elements (tick labels, grid
  lines) are matched by position.
- A server SHOULD keep the plot rectangle stable while only the x window
  moves. A rectangle that changes width by more than 10% between frames
  turns the slide into a snap (§6.8.3).
- A sliding window SHOULD include the last point before the window start
  (drawn, clipped by the plot's clip path, and left out of the metadata and
  the table per §3.5), so the line reaches the plot's left edge during a
  slide instead of leaving a gap.

### 4.5 Transports

Level 2 uses htmx's own transports. The client adds no transport of its own.

- **Polling.** `hx-get` with `hx-trigger="every Ns"` and
  `hx-swap="innerMorph"`. With the `hx-ptag` extension, a server SHOULD send
  `HX-PTag: "<tag>"` with each frame, where the tag is a content hash of the
  response body, and answer a request whose `HX-PTag` header equals the
  current tag with a bodiless `304`. Tags are compared after trimming
  whitespace, a `W/` prefix and one pair of quotes.
- **Server-sent events.** `hx-sse:connect` with `hx-swap="innerMorph"`. Each
  unnamed message's `data:` lines carry one complete fragment (the chart,
  and its table when the page shows one). A server MUST split the fragment
  into `data:` lines at every CRLF, CR and LF, as hx-sse does when it reads
  them. A server SHOULD send `retry:`. The rest of this item describes
  hx-sse 4.0.2, which a server and a page have to work with:
  - **Starting.** A stream that cannot start SHOULD answer `503` with
    `HX-Reswap: none`. hx-sse takes over a response only when its
    `Content-Type` is `text/event-stream`; any other answer goes through the
    normal htmx swap (so without `HX-Reswap: none` an error body would
    replace the chart), and a failed *first* connection is never retried:
    hx-sse reconnects only a stream that was once open. No status code
    changes this, so a `503` on the first connection leaves the chart on its
    current frame until the page reloads. A page that must recover from that
    needs its own retry, such as a trigger that fetches the stream element
    again (see the container below).
  - **Reconnecting.** When an open stream ends, hx-sse reconnects after the
    `retry:` delay (500 ms by default), give or take 30%. It doubles the
    delay, up to a minute, only while reconnects fail with a network error
    or a non-2xx status; any 2xx resets it. A server that cannot serve a
    reconnect therefore SHOULD answer a non-2xx status such as `503`, never
    a `204` (which keeps every open page reconnecting about twice a second).
    For the same reason a server SHOULD skip a frame it fails to render and
    keep the stream open, rather than end the stream.
  - **Changing the stream.** The stream URL, including any `hp-w`, is fixed
    when the stream connects. To change it (a new size, a zoom, a filter), a
    page MUST replace the stream element with a swap that removes the old
    element from the document: `innerHTML` on its container, or
    `outerHTML`. It MUST NOT morph a new `hx-sse:connect` value into a live
    element (`outerMorph`, or a morph of its container). A morph keeps the
    node, so hx-sse's read loop, which runs while the element is in the
    document, reconnects to the old URL, and two streams swap frames into
    one element.

  The usual shape is a container that fetches its stream element, so that
  a resize or a zoom replaces it:

  ```html
  <div class="zoom" hx-get="/cards/tp" hx-trigger="htmx:plot:brush"
       hx-target="#tp" hx-swap="outerHTML" hx-sync="replace" hx-plot:brush>
    <div id="tp" hx-get="/cards/tp" hx-trigger="load, htmx:plot:resize"
         hx-swap="innerHTML" hx-plot:autosize>
      <div class="stream"><svg id="tp-chart" class="hp" data-hp="1" …>…</svg></div>
    </div>
  </div>
  ```

  `/cards/tp` answers a request from `#tp` with a stream element whose URL
  carries the size and any range:
  `<div class="stream" hx-sse:connect="/stream/tp?hp-w=800" hx-swap="innerMorph">…</div>`.
  The page renders the first frame without `hx-sse:connect`, and `load`
  fetches the stream element at the container's real width, so only one
  stream opens. `htmx:plot:resize` fires on `#tp` itself, so `#tp`'s own
  trigger sees it although the event does not bubble. A brush answers with
  the whole container (`outerHTML`), whose `hx-get` carries the range
  (`/cards/tp?from=…&amp;to=…`) and whose child is the zoomed frame without
  `hx-sse:connect`; the new container's `load` then opens the zoomed stream,
  and later resizes keep the range.
- **Several targets.** One response MAY update other elements with
  `<hx-partial hx-target="…" hx-swap="…">`.
- **Errors.** Pages SHOULD set
  `<meta name="htmx-config" content='{"noSwap":[204,304,"4xx","5xx"]}'>` so
  an error page from a proxy is never swapped into a chart.
- A URL that serves either a full page or a fragment depending on
  `HX-Request` SHOULD send `Vary: HX-Request, HX-Request-Type`.

### 4.6 Size parameters

When the page uses `hx-plot:autosize` (§6.2), requests carry `hp-w` and,
for `both`, `hp-h` (§6.7). A server:

- MUST accept these parameters on every chart URL without error;
- SHOULD render the chart that wide (or that tall), in CSS px;
- SHOULD clamp values to [50, 4096] and use its default for missing,
  non-numeric, non-finite, zero or negative values.

Only `hp-w` is sent by default, because a wrapper without a CSS height takes
its height from the chart inside it; echoing that back would grow the chart
on every request.

## 5. Gestures (Level 3)

Select and brush are hypermedia exchanges: the client dispatches an event
(§6.9), the author's `hx-trigger` turns it into an ordinary htmx request,
and the extension adds the gesture's values as parameters (§6.7). The server
answers with HTML like any other request.

### 5.1 Parameters

| Parameter | Sent with | Value |
|---|---|---|
| `hp-chart` | select, brush | the chart's id |
| `hp-x` | select | the **key** of the selected x |
| `hp-series` | select | the name of the selected series, from `data-hp-series` (omitted when none) |
| `hp-x0`, `hp-x1` | brush | the range's ends: **time values** on a time axis, **linear values** on a linear axis |

The values are form values: htmx encodes them as
`application/x-www-form-urlencoded` (in the query string for GET and DELETE,
in the body otherwise). An author's own `hp-*` values always win (§6.7).

### 5.2 Select

A select means "the reader chose this x (and maybe this series)". A Level 3
server:

- MUST parse `hp-x` by the chart's x kind (a time value, a linear value or
  a category) and MAY match it against its data exactly, because the value
  is a key the server itself wrote;
- SHOULD treat a missing or unknown value as no selection, not an error.

What it answers is the application's business: a detail fragment, a
filtered table, a highlighted chart.

### 5.3 Brush

A brush means "the reader marked this x range", usually to zoom. A Level 3
server:

- MUST NOT assume `hp-x0 ≤ hp-x1`; it orders them;
- on a time axis, MUST accept any RFC 3339 `date-time` and SHOULD also
  accept the HTML `datetime-local` form (`YYYY-MM-DDTHH:MM`, optionally
  `:SS` and a fraction, no offset), read in the chart's time zone, so that a
  plain `<input type="datetime-local">` form can send a range (§5.5). Where
  that zone's clocks change, it SHOULD read a wall time as JavaScript's
  `Date` and Python's `fold=0` do: a time the clocks repeat is the first of
  its two instants, and a time they skip is read with the offset in effect
  before the change (in New York on 2026-03-08, `02:10` is 07:10Z, which
  is 03:10 EDT). Appendix B's `parse_time_value` does this;
- on a linear axis, MUST accept any finite JSON number and MUST reject
  everything else: values that overflow to infinity (`1e999`), and forms
  that a language's float parser may take but JSON does not (`inf`,
  `Infinity`, `NaN`, `1_0`, `0x10`, `+1`, `1.`, `.5`, surrounding spaces);
- SHOULD ignore a brush whose values do not parse;
- SHOULD answer a zoom by re-rendering the chart with the x domain fixed to
  the range, so that axes, ticks and labels are the server's again.

A zoom SHOULD become page state: the server answers with an
`HX-Push-Url` header naming the page URL that shows this range (not the
fragment URL), so the zoomed view can be bookmarked and Back undoes it.

### 5.4 Example: brush to zoom a live chart

```html
<div class="zoom" hx-get="/charts/latency" hx-trigger="htmx:plot:brush"
     hx-target="#latency" hx-swap="outerMorph" hx-sync="replace" hx-plot:brush>
  <div id="latency" hx-get="/charts/latency" hx-trigger="every 10s"
       hx-swap="innerMorph" hx-sync="closest .zoom:drop" hx-plot:autosize>
    <svg id="lt" class="hp hp-anim" data-hp="1" …>…</svg>
  </div>
</div>
```

Dragging across 2:05 PM to 2:15 PM sends:

```http
GET /charts/latency?hp-w=800&hp-chart=lt&hp-x0=2026-03-03T14%3A05%3A00Z&hp-x1=2026-03-03T14%3A15%3A00Z HTTP/1.1
HX-Request: true
HX-Request-Type: partial
HX-Source: div
HX-Target: div#latency
```

`hp-w` comes from `#latency`, the request's target, because it carries
`hx-plot:autosize`. The server tells this request from a poll of the same
URL by the presence of `hp-x0` and `hp-x1`. Because the swap is
`outerMorph`, it answers with the whole polling element, whose `hx-get` now
carries the range so later polls stay zoomed:

```http
HTTP/1.1 200 OK
Content-Type: text/html; charset=utf-8
HX-Push-Url: /dashboard?from=2026-03-03T14%3A05%3A00Z&to=2026-03-03T14%3A15%3A00Z

<div id="latency" hx-get="/charts/latency?from=2026-03-03T14%3A05%3A00Z&amp;to=2026-03-03T14%3A15%3A00Z"
     hx-trigger="every 10s" hx-swap="innerMorph" hx-sync="closest .zoom:drop" hx-plot:autosize>
  <svg id="lt" class="hp hp-anim" data-hp="1" data-hp-x="time 1772546700000 1772547300000" …>…</svg>
</div>
```

A poll answers with the inner content only (`innerMorph`). A polling element
is the one place where the outer swap matters: an inner swap cannot change
the polling element's own `hx-get`. The two `hx-sync` attributes put both
elements on one request queue (htmx 4 takes the queue from the selector
before the last colon): the brush aborts a poll in flight, and polls are
dropped while the brush is pending, so a late poll cannot swap the unzoomed
frame back.

### 5.5 Equivalents in plain HTML

Pointer gestures are not available to every reader (§8). A page that offers
select or brush SHOULD offer the same requests through ordinary HTML
controls. Because the parameters are plain form fields, these work with no
client and no script:

```html
<!-- select: one link per table row, with the row's key -->
<a href="/detail?hp-chart=lt&amp;hp-x=2026-03-03T14%3A10%3A00Z">Details for 2:10 PM</a>

<!-- brush: a from/to form, inside the .zoom wrapper of §5.4 -->
<form action="/dashboard" hx-get="/charts/latency" hx-target="#latency" hx-swap="outerMorph"
      hx-sync="closest .zoom:replace">
  <input type="hidden" name="hp-chart" value="lt">
  <label>From <input type="datetime-local" name="hp-x0" step="1"></label>
  <label>To <input type="datetime-local" name="hp-x1" step="1"></label>
  <button>Zoom</button>
</form>
```

The form needs the same `hx-sync` as the brush in §5.4, for the same
reason: without it, its request has a queue of its own, a poll already in
flight is not aborted, and when that poll lands it morphs the live frame
back into the zoomed element, which then stays wrong until the next poll.
A "back to live" link that targets the polling element needs it too.

After a select or a zoom, the server SHOULD also update a polite live region
on the page (for example through `<hx-partial>`) with what changed, such as
"Showing 2:05 PM to 2:15 PM": the client cannot know what the request did,
and a morph of `<title>` or `<desc>` is not announced.

## 6. The extension

### 6.1 Loading and registration

- The script is `hx-plot.js`, with no dependencies and no build
  step, loaded with a plain `<script>` tag after htmx 4
  (`htmx.org@4.0.2` or later 4.x). The reference client keeps two parts in
  optional companion scripts, each loaded with its own tag after
  `hx-plot.js`: brushing (§6.6) in `hx-plot-brush.js` and the path tween
  (§6.8.4) in `hx-plot-tween.js`. A companion does nothing unless
  `hx-plot.js` has started. It MUST register the extension under the
  name **`plot`**: `htmx.registerExtension('plot', …)`. A page that
  restricts extensions lists it by that name:
  `<meta name="htmx-config" content='{"noSwap":[204,304,"4xx","5xx"],"extensions":"sse,ptag,plot"}'>`.
- The client decides once between **htmx mode** (htmx is loaded) and
  **standalone mode** (§6.10): when it runs, or, when htmx is not loaded
  yet and the document is still loading, at `DOMContentLoaded`. By then
  an htmx loaded by a module script (the `htmx.org` package's `module`
  entry) or a deferred script has run, though such a script runs after a
  classic `<script>` placed after it. When it chooses standalone mode on a
  page that has htmx attributes (`hx-get`, `hx-post`, `hx-trigger`), it
  SHOULD warn on the console that htmx did not load first: its requests
  would carry no `hp-*` parameters. In htmx mode it MUST start only
  if htmx accepts the registration: when `htmx.config.extensions` is
  non-empty and does not list `plot`, the client MUST do nothing at all (no
  listeners, observers, status region or events), as the page's operator
  asked.
- It MUST be safe to load twice (the second copy does nothing). It sets
  `globalThis.hyperplot = {version}`, which is that guard and the hook for
  the optional companion script (§6.8.4). This one global is the
  extension's only deviation from htmx's no-globals pattern. The guard and
  the companions MUST test for an own property of `globalThis`, not a truthy
  `globalThis.hyperplot`: an element with `id="hyperplot"` (or an `<img>`,
  `<form>`, `<embed>` or `<iframe>` named so, which user content can
  carry) also appears as `window.hyperplot`, from the window's prototype
  chain.
- In htmx mode the client ignores charts and autosize elements inside
  `[hx-ignore]` (or the prefixed `[data-hx-ignore]`), as htmx itself does.
- There is no `hx-ext` attribute; htmx 4 removed it.

### 6.2 Attributes

Authors put these attributes on elements around charts. Both are
**container attributes**: the client finds the nearest element at or above
the element in question that carries the attribute, with `closest()`, and
reads its value there. htmx's `:inherited` and `:append` suffixes and
`htmx.config.implicitInheritance` do not apply to them. The client accepts
htmx's spellings: `hx-plot` and `htmx.config.prefix + 'plot'` (default
`data-hx-plot`), each joined to the name by `:` or by
`htmx.config.metaCharacter` when one is set (`hx-plot:brush`,
`data-hx-plot:brush`, `hx-plot-brush` with metaCharacter `-`). An empty
value is a value: the client tests for presence, not truthiness.

| Attribute | On | Values | Meaning |
|---|---|---|---|
| `hx-plot:autosize` | the element whose content box sizes the chart (usually the swap target) | empty or `width`; `both` | watch the element's size: dispatch `htmx:plot:resize` when it changes, and add `hp-w` (and for `both`, `hp-h`) to requests whose target is this element or inside it |
| `hx-plot:brush` | a chart's ancestor | empty or `x` | allow brushing the x axis of the charts inside (time and linear axes only) |

Unknown values MUST be treated as the default. Changing any `hx-*`
attribute in a morph makes htmx re-initialize the element; servers that
render these attributes SHOULD keep them constant across frames. Tuning
(whether to slide, how long) is configuration, not an attribute (§6.3).

### 6.3 Configuration

The settings for a swap are, from lowest to highest precedence: the
defaults below, `htmx.config.plot` (usually set from the `htmx-config` meta
tag: `{"plot":{"slide":false}}`), and the `plot` object of the request's
`hx-config`, which htmx merges into `ctx.request` for every request,
including SSE messages, whose request is the stream's. So an element can
tune the swaps its own requests cause:
`hx-config='{"plot":{"slide":false}}'`, or in htmx's short form
`hx-config="plot.slide:false"`, on a polling element or an
`hx-sse:connect` element (with `hx-config:inherited` on an ancestor to cover
several). Without htmx only the defaults apply.

| Key | Default | Meaning |
|---|---|---|
| `slide` | `true` | sliding-window animation (§6.8.3); `false` makes a moved window snap instead |
| `follow` | `true` | live tables that were scrolled to their end stay at the end (§6.8.7) |
| `keepOpen` | `true` | morphs never close or open a `<details>` that holds a table (§6.8.7) |
| `duration` | `350` | slide duration in ms; it matches the stylesheet's transitions. A value that is not a finite number ≥ 0 counts as the default |
| `easing` | `"ease-out"` | slide timing function; `"linear"` suits charts that update every second. A value that is not a CSS easing function (such as a JavaScript library's `"easeOutCubic"`) counts as the default, so a configuration mistake never breaks a swap |
| `strings` | `{"noData": "no data", "range": "{0} to {1}"}` | the client's own words in announcements: a `"–"` value, and a brush range (`{0}`, `{1}`: the two row headings) |

### 6.4 Finding charts

A chart is any `svg.hp` element. The client parses its metadata when it
first needs it and re-parses only when the text changes. It MUST tolerate
malformed JSON (the chart is then inert) and MUST NOT throw.

### 6.5 Pointer and keyboard (Level 1)

- **Hover.** In mode `x` the pointer activates the nearest `xs` entry (the
  lowest index on a tie) while it is between `top` and `bottom`; the client
  shows `line.hp-crosshair` there. In mode `band` it activates the nearest
  band centre while inside the outer bands (`xs[0] − bw/2` to
  `xs[n−1] + bw/2`) and between `top` and `bottom`, and shows
  `rect.hp-band-hl` over that band. In mode `xy` it activates the nearest
  point within 12 CSS px (24 for touch) and rings it with `circle.hp-focus`.
- **Tooltip.** One `div.hp-tip` (`aria-hidden="true"`) for the page:
  `div.hp-tip-t` with `t`, then per entry `div.hp-tip-row` holding
  `span.hp-key.hp-s{slot}`, `b.hp-tip-v` (value) and `span.hp-tip-n` (name).
  All text MUST be inserted with `textContent`. The tooltip and the status
  region (below) go in the open `<dialog>` or popover that holds the active
  chart, else in `<body>`: a modal dialog or a popover is in the top layer
  and makes the rest of the page inert, so a tooltip outside it is painted
  under it and a status region outside it is not exposed. The client moves
  the status region when a chart takes focus, before its first step is
  announced, and keeps it with the focused chart across swaps. In a dialog
  or popover that has a transform, filter or `contain` (which then
  positions a fixed element instead of the viewport), the tooltip still
  sits by its point, at its own width. A pointer tooltip closes when the
  page, or an element that holds the chart, scrolls; scrolling any other
  element (a followed live table, a log) leaves it. The pointer can rest on
  the tooltip (WCAG 1.4.13, hoverable): while it is on the tooltip, or
  heading for it after resting on a point, the active point stays.
  `rect.hp-band-hl` and the ring, like the crosshair, move to the active
  point at once, with no CSS transition.
- **Keyboard.** A focused chart steps through its points with ArrowLeft and
  ArrowRight, Home and End; Escape hides. Modes `x` and `band` step in index
  order; mode `xy` steps left to right, then bottom to top at equal x. Each
  step is announced through a `role="status"` region as
  `"{t}: {name} {value}, …"`, with `"–"` spoken as `strings.noData`.
  Refreshes are never announced. Focus that leaves the chart hides the
  keyboard tooltip, but only once it has really moved (the client decides a
  frame later): a swap that removes a focused chart blurs it first, and the
  chart is then found again (§6.8.6).
- **Touch.** A tap shows the tooltip, which stays until a tap outside the
  chart. A sideways drag scrubs the tooltip along the chart; it depends on
  the stylesheet's `touch-action` (§3.4).

[DESIGN.md, "Client extension"](DESIGN.md#client-extension-clienthx-plotjs)
has the complete behaviour of the reference client.

### 6.6 Select and brush

**Select.** The client dispatches `htmx:plot:select` (§6.9) when:

- the chart is clicked and the click's position activates a point under the
  hover rules of §6.5. For touch, the point must have been active before
  this tap's `pointerdown` (so the first tap still just shows the tooltip,
  and the second selects); the client records the active point when
  `pointerdown` arrives, before any coalesced hover update can activate it;
- a `click` with `detail` 0 arrives (a click made by assistive technology or
  by script, such as Enter in a screen reader's browse mode): it selects the
  active keyboard point if there is one, and nothing otherwise, whatever its
  coordinates; or
- Enter is pressed while the chart has focus and a keyboard point is active.

No point, no event. A `click` that ends a pointer brush (below), or the
release of a drag that was cancelled (Escape, or a swap), selects nothing. The detail is `{chart, x, series, index}`: `chart` the svg's id;
`index` the active point; `x` that row's `k` (no event when the row has no
`k`); `series` the `data-hp-series` of the `g.hp-series` that contains the
click target in modes `x` and `band` (when pointer capture, such as a
brush's, made the svg the click target, the target of the `pointerdown`
counts instead; empty when the target is in no series,
and always empty for Enter in those modes; a 2 px line is hard to hit, so
treat `series` in mode `x` as a hint), and in mode `xy` the
`data-hp-series` of the group whose class is `hp-s{slot}` for the point's
first entry.

**Brush.** Only charts inside an `hx-plot:brush` element whose
`data-hp-x` kind is `time` or `linear` can be brushed.

- *Pointer*: a primary-button mouse or pen press on the chart within the
  plot's vertical span (`data-hp-plot`, top to bottom) starts a range; a
  press beside the plot starts at its nearer edge, because the position is
  clamped to the plot (the `scatter-xy` fixture presses at x 0). At the
  press the client converts the
  press position to a data value through the current frame's geometry, and
  keeps that value, not the pixel. Moving draws `rect.hp-brush` across the
  plot's full height, clamped to the plot. Releasing after a horizontal
  travel of at least 4 CSS px dispatches `htmx:plot:brush`, with the release
  position read through the geometry current at release; less travel is a
  click. So is a release whose value equals the press's once both are
  written as below (a drag that stayed beside the plot clamps both ends to
  the same edge): an empty range is not a brush. A drag MUST NOT start a text selection. When a swap replaces the
  frame during a drag, the client re-projects the kept value through the new
  frame and redraws the rectangle; it cancels the drag when the chart is
  gone, the new frame's x kind cannot be brushed, or pointer capture is lost.
  Touch never brushes in spec 1 (a sideways drag stays a tooltip scrub).
- *Keyboard*: with a keyboard point active, Shift+ArrowLeft and
  Shift+ArrowRight extend a range from that point (the anchor) one point at
  a time, and Shift+Home and Shift+End extend it to the ends; the client
  draws `rect.hp-brush` from the anchor row's x to the active row's x, and
  the region announces `strings.range` (`"{t0} to {t1}"`); Enter
  dispatches `htmx:plot:brush` for the range; Escape cancels it.
- *Values*: a keyboard range sends the two rows' keys, lower first. A
  pointer value inverts a viewBox x position through the plot and the x
  domain:
  `v = d0 + (clamp(px, left, right) − left) / (right − left) × (d1 − d0)`;
  the two values are then ordered. On a time axis each value is rounded to
  whole milliseconds and written as a **time value**. On a linear axis each
  value is rounded to `d` decimals, the smallest `d` in 0–15 with
  `10^−d ≤ (d1 − d0) / (10 × (right − left))` (a tenth of a pixel), and
  written as a **number**.
- The detail is `{chart, x0, x1}`.
- `rect.hp-brush` (`fill="currentColor" fill-opacity="0.1"`,
  `pointer-events="none"`, `aria-hidden="true"`) stays until Escape or the
  next press on the chart; after a swap the client redraws it while the
  range is still in progress, and drops it otherwise.

### 6.7 Request parameters

In `htmx_config_request` the client adds parameters to the request's body
(a `FormData` at that point; htmx copies it into the query string for GET
and DELETE):

- **Size**: when the request's target (`ctx.target`) is an
  `hx-plot:autosize` element or inside one, `hp-w` is the nearest such
  element's content-box width in CSS px (client width less padding), rounded
  to an integer, and for `both` also `hp-h` (the content-box height). A
  value of 0 is never sent. A request that targets something else, such as
  a drill-down into another element, gets no size.
- **Gestures**: when the request's triggering event (`ctx.sourceEvent`) is
  an `htmx:plot:select` or `htmx:plot:brush` event (its type compared with
  the name as htmx dispatches it, with `htmx.config.metaCharacter` applied),
  its detail becomes `hp-chart`, `hp-x`, `hp-series` or `hp-chart`,
  `hp-x0`, `hp-x1` (§5.1). Empty values are not sent.
- The client MUST NOT add a parameter that the request already has, in its
  body (form values, `hx-vals`) or in the query string of its URL. htmx
  deletes a URL parameter whose name is in the body before appending the
  body, so this check protects values the author wrote into `hx-get` or
  `hx-sse:connect`.

[`params.json`](../testdata/conformance/params.json) has the cases.

### 6.8 Swap behaviour

#### 6.8.1 Scope

The client takes part in every htmx swap: poll responses, SSE messages,
form responses, any `hx-swap` style. It does nothing for a swap whose style
is `none` (a ptag 304 still runs htmx's swap events with `ctx.swap` set to
`"none"`). There is no custom swap style: `innerMorph` (or `outerMorph`)
stays the documented swap, so a server only has to emit morph-friendly HTML.

Before the swap the client records, for each chart in the page, its id and
root attributes, and the settings of §6.3 for this request; after it, it
pairs each new chart in a swap target with the old chart of the same id. A
new chart with no old partner is simply shown. (It records the page rather
than the swap targets, and keeps the record with the request rather than
with htmx's swap task objects, because another extension may still change
the targets or replace the tasks with copies: htmx's `hx-targets` does
both.)

#### 6.8.2 Outcomes

For each pair the client chooses one outcome:

- **slide**: the x window moved; the content slides (§6.8.3).
- **snap**: the x window changed in a way that cannot slide; the new frame
  appears at once, with no transitions.
- **none**: anything else (the x window is unchanged, or the chart cannot
  be compared); the swap proceeds untouched, and the stylesheet's CSS
  transitions (on `d`, `x`, `y`, `cx`, `cy`, `r`, `opacity`) animate value
  changes in browsers that support them.

A frame whose x window changed never gets **none**: interpolating a path's
`d` across a moved window moves every vertex toward its neighbour's value,
so the line wobbles in place.

#### 6.8.3 Sliding windows

The fix for the wobble is Bostock's: interpolate a transform, not the path.
With `o` the old frame and `n` the new one, the client decides in this order
(the first rule that applies wins; the reason names are the fixtures'):

1. Either frame lacks `data-hp="1"`, a `data-hp-plot` of four finite
   numbers, or a `data-hp-x` of kind `time` or `linear` with finite
   `d0 < d1`: **none** (`unsupported`).
2. The new frame's class lacks `hp-anim`, motion is reduced (§6.8.5), or
   the swap runs as a view transition (§6.8.5): **none** (`static`).
3. The x kind and both domain ends are equal: **none** (`still`). This
   covers value changes and resizes that keep the window.
4. `slide` is false (§6.3): **snap** (`off`).
5. The x kinds differ: **snap** (`kind`).
6. The `viewBox` differs: **snap** (`size`).
7. The plot's top or bottom differ: **snap** (`plot`).
8. `a` (below) is not within [0.9, 1.1]: **snap** (`scale`). A zoom in or
   out lands here.
9. `|s| > (o.right − o.left) / 4`: **snap** (`far`).
10. The largest displacement, `max(|Δ(n.left)|, |Δ(n.right)|)`, is under
    0.5 CSS px at the chart's current scale (CSS px per viewBox unit):
    **snap** (`tiny`).
11. The effective duration (below) is under 60 ms: **snap** (`fast`).
12. Otherwise **slide**.

where

```
s    = (n.d0 − o.d0) / (o.d1 − o.d0) × (o.right − o.left)   ; content shift, old viewBox units
a    = ((o.right − o.left) / (o.d1 − o.d0)) / ((n.right − n.left) / (n.d1 − n.d0))
b    = o.left + s − a · n.left
Δ(x) = (a − 1) · x + b                                        ; x_old − x_new for new position x
```

`x_old = a · x_new + b` maps a position in the new frame to where the same
data was drawn in the old one. `a` is 1 when the plot keeps its width and
the window its span; it differs slightly when tick labels change the
margins or the window's span changes a little (a server whose window
follows the data's own first and last timestamps), which is why the map is
not a plain translation.

**Anchored elements.** These elements of the new frame move with the data:
the children of every `g.hp-series`, the `text.hp-dot-label` elements of
`g.hp-labels` and the `text.hp-tick` elements of `g.hp-axis-x` (at any depth:
a server may group each tick's line and label in a plain `g`), and any element
of class `hp-xa` (reserved for x-anchored marks a server adds, such as a
vertical event marker). End dots (`circle.hp-end`), direct series labels
(`text.hp-direct`) and an empty-chart message (`text.hp-empty`) are not
anchored: they sit at the newest point and move to their new place at once
while the line's head slides in under them.

**Effect.** Right after the morph and before the browser renders, the
client:

1. sets `transition: none` on every `path`, `circle`, `rect`, `line` and
   `text` of the new chart, so no CSS transition starts for this swap and
   any that a style recalculation during the morph started is cancelled
   (the marks' vertical changes snap, which also handles a y domain
   change);
2. stops any slide still running on the chart: the previous slide ends at
   once, and this one starts from the new frame's geometry;
3. animates each anchored element's CSS `transform` from a start value to
   `none` over the effective duration with `easing`. The start value puts
   the element where its data was drawn in the old frame. For `path`,
   `rect`, `polyline` and `polygon` it is
   `translateX(b px) scaleX(a)` (viewBox units; the transform origin is
   `0 0`, the SVG default). For every other element (`text`, `circle`,
   `line`, …) it is the translation alone, `translateX(Δ(x) px)`, with `x`
   the element's own `x` (its first value), `cx` or `x1`, so glyphs and dots
   are never squeezed;
4. clears its inline styles when the slide ends; a later morph also strips
   them, because the server sends no `style`.

The effective duration is `min(duration, 0.8 × t)`, where `t` is the time
since this chart's previous swap (no limit for its first), so a stream that
updates faster than `duration` slides continuously instead of jumping and
restarting.

The reference client animates with the Web Animations API
(`el.animate([{transform: start}, {transform: 'none'}], {duration, easing})`),
which needs no forced style read, so charts with outcome **none** in the
same swap target keep their CSS transitions. A client MAY move a whole
`g.hp-series` instead of its children when `|a − 1| < 0.001` and the group
carries no `clip-path` or `mask` attribute (a CSS transform moves an
element's own clip region and mask with it; the clip path on its parent
`g.hp-marks` stays put).

The clip path on `g.hp-marks` keeps sliding marks inside the plot; a
server SHOULD clip there (§3.3) rather than on each `g.hp-series`. Tick and
dot labels are not clipped and may show beyond the plot's edges for the
length of the slide. Hover and keyboard state follow the final positions
(§6.8.6); the client's own `line.hp-crosshair`, `circle.hp-focus` and
`rect.hp-brush`, redrawn right after the morph (before a settle delay or
the end of a view transition, so no frame shows the chart without them),
get the same translation so they move with their data when they mark a
keyboard point or a keyboard range. A pointer's crosshair and a brush
being dragged with the pointer are not translated: they stay under the
pointer, where the new frame's data now is (the dragged brush's other end
moves to its data at once).

**Snap** performs steps 1 and 2 and nothing else, so neither the `d`
wobble nor a long horizontal tween plays.

The [`slide` fixture](../testdata/conformance/slide/checks.json) pins every
branch, including a plot whose width changes between frames.

#### 6.8.4 Path tweening where CSS cannot

Safari does not transition the CSS `d` property: it parses and ignores it,
so value changes snap there. Slides use `transform` and work in every
engine. An OPTIONAL companion script, `hx-plot-tween.js`, loaded after
`hx-plot.js`, tweens `d` in JavaScript for outcome **none** (on a chart with
`hp-anim`, with motion allowed) in browsers where CSS cannot set a path's
`d`, for the series paths whose old and new `d` have the same command
sequence; others snap. It suppresses the CSS `d` transition on the paths it
handles while it runs, with the duration and easing of §6.3. It tests this
once by drawing, not with a feature query (Safari parses `d`, so
`CSS.supports` may accept it): a path whose `d` attribute is a point gets a
CSS `d` with width, and its bounding box tells which one the browser drew. Where CSS sets `d`, the
stylesheet's transition already animates these changes and the companion
does nothing. It is not needed for conformance at any level.

#### 6.8.5 Reduced motion and view transitions

With `prefers-reduced-motion: reduce` the client never slides or tweens, and
the stylesheet's transitions are already off. When a swap runs as a view
transition the client does not animate: the browser cross-fades snapshots,
and a slide underneath would show twice. A task runs as a view transition
when `task.swapSpec.transition ?? mainSwap.transition ?? ctx.transition` is
true, the same precedence htmx uses, so `transition:false` on a swap turns
the slide back on even with `htmx.config.transitions` set.

#### 6.8.6 Hover and keyboard carry-over

After every swap the client re-applies the active state to the new frame,
without announcing it:

- A pointer stays where it is; the active point is recomputed from the new
  metadata, so the tooltip updates in place instead of flickering.
- A keyboard point stays on the row with the same key `k` (nearest the old
  index when several rows share it; by `t` when rows have no `k`); when that
  row has left the window, it keeps its index, clamped. A chart that a
  non-morph swap replaced is found again by id and keeps focus.
- An unfinished keyboard brush keeps its anchor by key, and its rectangle
  is redrawn; it is cancelled when the anchor's row is gone. An unfinished
  pointer brush is re-projected (§6.6).

#### 6.8.7 Tables

For each `table.hp-table` inside the swap target:

- **Follow.** Before the swap the client notes, for the table's nearest
  scrollable ancestor up to and including the swap target (computed
  `overflow-y` of `auto` or `scroll`, content taller than the box; the
  table itself counts), whether it is scrolled to its end (within 2 px).
  When no such ancestor has content taller than its box yet, the nearest
  with `overflow-y` `auto` or `scroll` counts, and is at its end: the
  reader sees every row, and must keep seeing the newest once the table
  outgrows the box.
  After the swap it scrolls those back to the end. A reader at the end keeps
  seeing the newest rows in every browser, whatever its scroll-anchoring
  behaviour; a reader elsewhere keeps their rows, which stable row ids
  (§4.1) make possible. When the swap's own `scroll:` or `show:` modifier is
  set, the author has said where to scroll, and follow does nothing for that
  task.
- **Keep open.** During the morph the client keeps the current `open` state
  of every `<details>` that contains an `hp-table`: the server's frame does
  not know whether the reader expanded it. Pages no longer need a global
  `morphIgnore` for `open`. This also ignores an `open` change the server
  sends on purpose (a reset view); `hx-config="plot.keepOpen:false"` on the
  requesting element lets such a response through.

#### 6.8.8 Elements the client owns

`circle.hp-focus`, `rect.hp-brush`, inline styles and running animations on
chart elements, and the display attributes the client writes on
`line.hp-crosshair` and `rect.hp-band-hl` (`visibility`, `x1`, `x2`, `x`,
`y`, `width`, `height`) are the client's. A morph removes or resets them
(the server's frame has only its own values), and the client re-applies
what the current state needs after the swap. The client keeps no state of
its own in server-rendered attributes: everything it needs survives in
memory, keyed by chart id.

A swap can also bring back a copy of the page taken earlier (htmx's
`hx-history-cache` extension restores one on Back). The client keeps what
it owns out of such copies where it can: `div.hp-tip`, the status region,
`circle.hp-focus`, `rect.hp-brush` and inline styles on chart elements are
left out while `hx-history-cache` saves the page (its
`htmx:history:cache:before:save` and `…:after:save` events). After every
swap it also removes any `div.hp-tip` or status region it does not track
and hides any crosshair, band highlight or ring outside the active chart,
which covers copies taken any other way.

### 6.9 Events

| Event | Target | Bubbles | `detail` | When |
|---|---|---|---|---|
| `htmx:plot:resize` | the `hx-plot:autosize` element | no | `{width, height}` in CSS px (content box) | its width changed by at least 8 px (or, for `both`, its height did), after 150 ms without further change |
| `htmx:plot:select` | the `svg.hp` | yes | `{chart, x, series, index}` | §6.6 |
| `htmx:plot:brush` | the `svg.hp` | yes | `{chart, x0, x1}` | §6.6 |

The names follow the convention of htmx 4's own extensions
(`htmx:sse:after:message`, `htmx:head:after:merge`). In htmx mode the client
dispatches them through the extension API's `triggerHtmxEvent`, so htmx
applies `htmx.config.metaCharacter` to the name (with metaCharacter `-`,
`htmx:plot:select` is dispatched as `htmx-plot-select`), logs them with
`logAll`, and calls other extensions' `htmx_plot_select` hooks. They are
cancelable, as all htmx events are; cancelling one has no effect. In
standalone mode the client dispatches a `CustomEvent` with the same name.

Authors use them in `hx-trigger` (`hx-trigger="every 10s,
htmx:plot:resize"`, with the metaCharacter spelling when one is set), in
`hx-on::plot:select="…"` (htmx's shorthand for `hx-on:htmx:plot:select`),
or in their own listeners. Later lifecycle events, if any, will follow the
same pattern (`htmx:plot:before:slide`).

### 6.10 Without htmx

Without htmx the client still provides every Level 1 behaviour (§6.5) on
any page that contains charts inline, still dispatches `htmx:plot:resize`,
`htmx:plot:select` and `htmx:plot:brush` as plain `CustomEvent`s, and reads
`hx-plot:*` attributes with the default spellings (`hx-plot:` and
`data-hx-plot:`). Configuration is the defaults of §6.3. There are no swaps,
so §6.7 and §6.8 do not apply. Nothing tells the client about content that
page script or another library adds later, so it watches the document for
added `hx-plot:autosize` elements (the reference client uses a
`MutationObserver`). A standalone SVG file has no page around it;
the client does not run inside SVG documents in spec 1, and such files rely
on the native fallback (§3.7).

## 7. Security considerations

- **Injection.** Series names, categories and labels are often
  user-controlled. §3.5 and §3.6 keep them text in every channel. The
  client inserts all text with `textContent` and builds no markup from
  data.
- **CSP.** Charts carry no inline styles, scripts or handlers, and the client
  sets styles only through the CSSOM and the Web Animations API, so pages
  can use `style-src 'self'` and `script-src 'self'` (the client never
  evaluates code; gestures need no `hx-vals="js:…"`).
- **Parameters are input.** `hp-*` values arrive from the browser and can be
  anything. Servers MUST validate them like any parameter (§4.6, §5), and
  escape them if they echo them. A value that parses can still be absurd:
  a brush from year 1 to year 9999 is a valid pair of time values. Bound a
  range by the data the chart can show before it drives a query or date
  arithmetic, and ignore it otherwise (§5.3).
- **No exfiltration.** Charts reference nothing outside themselves (§3.6),
  and the extension makes no network requests of its own (§4.5).

## 8. Accessibility considerations

- Every chart has an accessible name and description and takes keyboard
  focus. With the client, a keyboard user reads it point by point through a
  status region (§6.5).
- The table (§3.8) is the equivalent for screen readers, for exact values,
  and for line charts without script. It must stay in sync with the chart:
  stable row ids, follow and keep-open (§4.1, §6.8.7) keep a live table
  usable.
- Which inputs reach which gesture:

  | Input | Read values | Select | Brush |
  |---|---|---|---|
  | mouse, pen | hover | click | drag |
  | keyboard | Arrow, Home, End | Enter | Shift+Arrow, Shift+Home/End, then Enter |
  | touch | tap, sideways scrub | second tap on the active point | no |
  | screen reader in browse mode (NVDA, JAWS) | the table; the chart's keys only after switching to focus mode | its click (`detail` 0) selects the active keyboard point, if any | no |
  | mobile screen reader (VoiceOver, TalkBack) | the table | no | no |

  Touch users cannot brush, and mobile screen-reader users cannot select or
  brush, in spec 1. Pages SHOULD therefore offer the plain HTML equivalents
  of §5.5 for every gesture they rely on; the chart remains `role="img"`.
- Motion respects `prefers-reduced-motion` (§6.8.5).
- Charts degrade to monochrome without a stylesheet (§3.4), and the
  reference stylesheet handles forced colours.
- With a keyboard, `line.hp-crosshair`, `rect.hp-band-hl` and
  `rect.hp-brush` are the chart's focus state. The reference stylesheet
  gives each a 1.5 px edge (`vector-effect: non-scaling-stroke`) in
  `--hp-text-muted`, at least 3:1 against the surface even when
  antialiased (WCAG 1.4.11); a replacement stylesheet SHOULD keep them at
  3:1.
- The client's own words are configurable (`strings`, §6.3) for pages that
  are not in English.

## 9. Names, versions and changes from pre-spec output

### 9.1 Names

| Thing | Name |
|---|---|
| npm package | `htmx-ext-plot` |
| script | `hx-plot.js` (optional `hx-plot.min.js`; companions `hx-plot-brush.js`, `hx-plot-tween.js`); stylesheet `hx-plot.css` |
| registered extension | `plot` |
| attributes | `hx-plot:autosize`, `hx-plot:brush` |
| config | `htmx.config.plot`; per request, `hx-config` key `plot` |
| events | `htmx:plot:resize`, `htmx:plot:select`, `htmx:plot:brush` |
| request parameters | `hp-w`, `hp-h`, `hp-chart`, `hp-x`, `hp-series`, `hp-x0`, `hp-x1`; every `hp-` name is reserved |
| markup | classes `hp` and `hp-*` (including `hp-xa`), attributes `data-hp` and `data-hp-*`, ids `{id}-*`; all reserved |
| global | `globalThis.hyperplot` |

The rule behind the two prefixes: **authors write `plot`** (attributes,
event names, config), **servers read and write `hp`** (markup and request
parameters).

### 9.2 Pre-spec (v0) output

Output from before this spec (`data-hp=""`) has no geometry, row keys or
table ids; clients give it Level 1 behaviour. v0 of the client used the
names `hyperplot` (registration), `data-hp-autosize` (attribute) and
`hp:resize` (event). They were never published, and spec 1 replaces them
with no aliases.

## Appendix A: FNV-1a keys

The 32-bit FNV-1a hash of the UTF-8 bytes of a string, as 8 lowercase hex
digits. A string holding lone surrogates, which have no UTF-8 encoding, is
hashed with each replaced by U+FFFD (§3.5):

```
h = 0x811c9dc5
for each byte c: h = (h XOR c) × 0x01000193  (mod 2^32)
```

`p50` → `2e119f3a`, `p50#1` → `4e3e42de`, `Q1` → `9bfcdfb7`,
`2026-03-03T14:00:00Z` → `f9457bc2`. `Q1149599` and `Q1312382` both hash to
`e0864ec2`; in one key set the second becomes `Q1312382#1` → `eb6f4726`
(§4.1).

## Appendix B: an exchange, and helpers in Python

A poll of a Level 2 chart, then an unchanged poll:

```http
GET /charts/latency?hp-w=800 HTTP/1.1
HX-Request: true
HX-PTag: "hp-1a2b3c4d5e6f7a8b"

HTTP/1.1 200 OK
Content-Type: text/html; charset=utf-8
HX-PTag: "hp-9f8e7d6c5b4a3921"
Vary: HX-Request, HX-Request-Type

<svg xmlns="http://www.w3.org/2000/svg" id="lt" class="hp hp-anim" … data-hp="1"
     data-hp-plot="24 8 788 298" data-hp-x="time 1772546400000 1772547600000"
     data-hp-y="linear 40 46" …>…</svg>
<details><summary>View as table</summary><table class="hp-table" id="lt-table">…</table></details>

GET /charts/latency?hp-w=800 HTTP/1.1
HX-Request: true
HX-PTag: "hp-9f8e7d6c5b4a3921"

HTTP/1.1 304 Not Modified
HX-PTag: "hp-9f8e7d6c5b4a3921"
```

The same chart over server-sent events (one message per frame):

```
retry: 2000
data: <svg xmlns="http://www.w3.org/2000/svg" id="lt" class="hp hp-anim" … data-hp="1" …>
data: …</svg>
data: <details><summary>View as table</summary><table class="hp-table" id="lt-table">…</table></details>

```

The value forms, the escaping and the parsing in Python (standard library
only, Python 3.8+; `number` agrees with JavaScript's `String(v)` and Go's
`encoding/json`, and `xml_text`/`xml_attr` reproduce the reference bytes of
the `escaping` fixture):

```python
import json
import math
import re
from datetime import datetime, timedelta, timezone
from decimal import Decimal


def number(v: float) -> str:
    """A finite float written like ECMAScript's Number.prototype.toString."""
    if not math.isfinite(v):
        raise ValueError(v)
    if v == 0:
        return '0'
    sign = '-' if v < 0 else ''
    t = Decimal(repr(abs(v))).normalize().as_tuple()  # shortest round-trip digits
    d = ''.join(map(str, t.digits))
    k, n = len(d), len(d) + t.exponent  # the value is 0.d × 10**n
    if k <= n <= 21:
        return sign + d + '0' * (n - k)
    if 0 < n <= 21:
        return sign + d[:n] + '.' + d[n:]
    if -6 < n <= 0:
        return sign + '0.' + '0' * -n + d
    e = n - 1
    return sign + d[0] + ('.' + d[1:] if k > 1 else '') + 'e' + ('+' if e > 0 else '-') + str(abs(e))


def time_value(t: datetime) -> str:
    """RFC 3339 in UTC; a fraction only when non-zero, without trailing zeros."""
    if t.utcoffset() is None:
        raise ValueError('naive datetime: attach its time zone first')
    t = t.astimezone(timezone.utc)
    s = t.strftime('%Y-%m-%dT%H:%M:%S')
    if t.microsecond:
        s += ('.%06d' % t.microsecond).rstrip('0')
    return s + 'Z'


_EPOCH = datetime(1970, 1, 1, tzinfo=timezone.utc)


def epoch_ms(t: datetime) -> str:
    """Milliseconds since the epoch, from integer microseconds."""
    if t.utcoffset() is None:
        raise ValueError('naive datetime: attach its time zone first')
    d = t - _EPOCH
    return number(((d.days * 86400 + d.seconds) * 1_000_000 + d.microseconds) / 1000)


_TIME = re.compile(r'([0-9]{4})-([0-9]{2})-([0-9]{2})[Tt]([0-9]{2}):([0-9]{2})'
                   r'(?::([0-9]{2})(?:\.([0-9]+))?)?([Zz]|[+-][0-9]{2}:[0-9]{2})?')


def parse_time_value(s: str, tz=timezone.utc) -> datetime:
    """An RFC 3339 date-time (any offset, any number of fraction digits) or the
    HTML datetime-local form (no offset, seconds optional), which is read in
    tz. Returns an aware UTC datetime; digits past microseconds are dropped.
    Works on Python 3.8+ (fromisoformat rejects 'Z' before 3.11)."""
    m = _TIME.fullmatch(s)
    if not m or (m[8] and not m[6]):  # an offset needs seconds (RFC 3339)
        raise ValueError(s)
    y, mo, d, h, mi, sec, frac, off = m.groups()
    t = datetime(int(y), int(mo), int(d), int(h), int(mi), int(sec or 0), int((frac or '0')[:6].ljust(6, '0')))
    if off is None:
        t = t.replace(tzinfo=tz)
    elif off in 'Zz':
        t = t.replace(tzinfo=timezone.utc)
    else:
        if int(off[4:6]) > 59:  # RFC 3339: offset minutes run to 59
            raise ValueError(s)
        sign = 1 if off[0] == '+' else -1
        t = t.replace(tzinfo=timezone(sign * timedelta(hours=int(off[1:3]), minutes=int(off[4:6]))))
    try:
        return t.astimezone(timezone.utc)
    except OverflowError:  # outside years 1 to 9999 once moved to UTC
        raise ValueError(s) from None


_NUMBER = re.compile(r'-?(?:0|[1-9][0-9]*)(?:\.[0-9]+)?(?:[eE][+-]?[0-9]+)?')


def parse_number(s: str) -> float:
    """A finite JSON number. float() alone also takes 'inf', 'nan', '1_0'
    and surrounding whitespace."""
    if not _NUMBER.fullmatch(s):
        raise ValueError(s)
    v = float(s)
    if not math.isfinite(v):  # 1e999
        raise ValueError(s)
    return v


def _xml_chars(s: str) -> str:
    """Drop what XML 1.0 cannot carry: C0 controls other than TAB, LF and CR,
    lone surrogates, U+FFFE and U+FFFF. (Decode bytes with errors='replace'
    first, so invalid UTF-8 becomes U+FFFD.)"""
    return ''.join(c for c in s if (o := ord(c)) in (9, 10, 13) or 0x20 <= o <= 0xD7FF
                   or 0xE000 <= o <= 0xFFFD or o >= 0x10000)


def xml_text(s: str) -> str:
    """Element text. HTML escapers (Django's escape, Jinja's autoescape) do
    not drop control characters, so they are not enough."""
    s = _xml_chars(s).replace('&', '&amp;').replace('<', '&lt;').replace('>', '&gt;')
    return s.replace('\r', '&#13;')


def xml_attr(s: str) -> str:
    """A double-quoted attribute value."""
    return xml_text(s).replace('"', '&quot;').replace('\t', '&#9;').replace('\n', '&#10;')


def wire_text(s: str) -> str:
    """s with each lone surrogate replaced by U+FFFD (§3.5). Python strings
    can hold them (json.loads of an escaped surrogate, or bytes decoded with
    errors='surrogateescape'), and str.encode() raises on them. Apply it to
    every string you hash into a key; tips_json applies it itself."""
    return ''.join(chr(0xFFFD) if 0xD800 <= ord(c) <= 0xDFFF else c for c in s)


def tips_json(payload: dict) -> str:
    """The hp-tips text: compact JSON with <, > and & escaped (plus U+FFFE/F),
    and lone surrogates replaced by U+FFFD."""
    s = wire_text(json.dumps(payload, ensure_ascii=False, separators=(',', ':'), allow_nan=False))
    for c in '<>&' + chr(0xFFFE) + chr(0xFFFF):
        s = s.replace(c, '\\u%04x' % ord(c))
    return s


def sse_message(fragment: str) -> str:
    """One unnamed SSE message: a data: line per line of the fragment, split
    on CRLF, CR and LF as hx-sse splits them."""
    return ''.join('data: ' + line + '\n' for line in re.split(r'\r\n|\r|\n', fragment)) + '\n'
```

Python's `str(2.0)` is `"2.0"` and `datetime.isoformat()` writes
`+00:00` and six fraction digits; neither is the wire form, so use `number`
and `time_value` for every `k`, `data-hp-*` value and parameter. Python
strings can also hold lone surrogates, which neither UTF-8 nor XML can carry:
`xml_text` and `xml_attr` drop them, `tips_json` replaces them with U+FFFD,
and a key's input needs `wire_text` before it is encoded and hashed.

## Appendix C: class names (informative)

The classes of the reference server's output. The reference stylesheet
(`hx-plot.css`) styles many of them, so a server that serves that
stylesheet should use them for the same things. Those marked † are also
read by the client (§3.3, §6.8.3).

| Class | Element | What |
|---|---|---|
| `hp` † | `svg` | every chart |
| `hp-anim` † | `svg` | animated updates (§6.8) |
| `hp-s1` … `hp-s8` † | series groups, keys, end dots | the series colour |
| `hp-legend`, `hp-legend-item`, `hp-legend-label`, `hp-key` | `g`, `g`, `text`, `rect` | the legend and its colour keys |
| `hp-grid` | `g` of `line` | gridlines |
| `hp-axis`, `hp-axis-x` †, `hp-axis-y` | `g` | axes |
| `hp-tick` † | `text` | tick labels |
| `hp-baseline` | `line` in `hp-axis-x` | the x axis line |
| `hp-axis-label`, `hp-ylabel` | `text` | axis titles |
| `hp-marks` † | `g` | the clipped data group |
| `hp-series` † | `g` | one series |
| `hp-line`, `hp-area`, `hp-bar`, `hp-dot` | `path`, `path`, `path`, `circle` | data marks |
| `hp-rules`, `hp-rule`, `hp-rule-label` | `g`, `line`, `text` | reference lines |
| `hp-labels` † | `g` | text over the plot |
| `hp-label`, `hp-direct` †, `hp-dot-label` † | `text` | labels: every label, a series label at its end, a dot's label |
| `hp-end` † | `circle` | the newest point of a line |
| `hp-empty` † | `text` | the "No data" message |
| `hp-band-hl` † | `rect` | the band highlight |
| `hp-hover`, `hp-crosshair` †, `hp-tips` † | `g`, `line`, `metadata` | hover machinery |
| `hp-focus`, `hp-brush` | `circle`, `rect` | written by the client only |
| `hp-xa` † | any | an extra x-anchored element (§6.8.3) |
| `hp-title`, `hp-bg` | `text`, `rect` | a visible title; a standalone file's background |
| `hp-tip`, `hp-tip-t`, `hp-tip-row`, `hp-tip-v`, `hp-tip-n` | `div`, `div`, `div`, `b`, `span` | the tooltip (written by the client) |
| `hp-live` | `div` | the status region (written by the client) |
| `hp-table`, `hp-sr` | `table`, `span` | the table equivalent; text for screen readers only |
| `hp-spark` | `svg` | a sparkline: a small decorative line with an `aria-label`, not a chart (it is not `svg.hp`, so clients and validators leave it alone); its line and end dot use `hp-s1`, `hp-line`, `hp-area` and `hp-end` |
