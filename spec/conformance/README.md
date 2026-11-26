# Conformance fixtures (wire spec 1)

These fixtures pin [spec/spec.md](../../spec/spec.md) to concrete bytes. Three
kinds of consumers read them:

- **Servers in any language** render each `input.json` and run the `server`
  assertions in `checks.json` against their own output. Their bytes will
  differ from ours (layout and number formatting are theirs to choose); the
  assertions only test what the spec requires, and those that test a SHOULD
  are marked. [hpcheck](../../spec/hpcheck/README.md) runs them, and the
  spec's universal checks, on your renderer's output.
- **The Go reference renderer** (`go test ./plot -run Conformance`) must reproduce
  every reference file byte for byte and pass every assertion.
- **The client** (`client/` tests under `node --test`) loads the reference
  files and checks the `client` expectations: parsing, hit testing, keyboard
  order, select and brush details, the slide decision and request
  parameters.

Everything here is small enough to audit by eye. Every chart is 240 × 120
with a handful of points, and the times are all on 2026-03-03 in UTC.

## Layout

| Path | Level | What it covers |
|---|---|---|
| `line-time/` | 3 | time-x line: root contract, geometry attributes, mode `x`, row keys, table row ids, hover, select (pointer, Enter, assistive-technology clicks), brush (pointer and keyboard) and its rectangle |
| `bars-band/` | 3 | grouped bars on a band axis: bar ids, native `<title>`s, mode `band`, band highlight, missing-value cell, select on a bar; band axes never slide or brush |
| `scatter-xy/` | 3 | two-series scatter: mode `xy`, point order, keyboard order, point table with per-series row ids, select of a point, brush rounding on a linear axis |
| `escaping/` | 3 | markup characters, TAB, LF, CR and U+0001 in the title, series name and a category, through every channel (text, attributes, JSON, table, tooltip, event detail, URL) |
| `collisions/` | 2 | two series with one name and two categories whose hashes collide: every id stays unique (spec Appendix A) |
| `empty/` | 1 | no marks: still a complete document, mode `x`; no `data-hp-x`; nothing to hover or select |
| `slide/` | 2 | the sliding-window decision between frames, the elements that slide and how, an overscan point, and a pointer brush across a swap |
| `params.json` | 2, 3 | what the extension adds to requests, and how servers read `hp-*` parameters |

Each chart directory holds:

- `input.json`: the chart, described independently of any language.
- `chart.svg`, `table.html` (or `frame-*.svg`): the reference output of the
  Go renderer, byte for byte, with no final newline.
- `checks.json`: the assertions.

## `input.json`

```json
{ "spec": 1, "chart": { … }, "table": true }
```

`table: true` means the fixture also covers the table equivalent.
`slide/input.json` holds several charts under `frames`. The fields the
fixtures use:

| Field | Meaning | Default |
|---|---|---|
| `id` | the chart id | none: every fixture sets it |
| `title` | the accessible name (`<title>`, table `<caption>`) | none |
| `width`, `height` | the viewBox size | 640, 320 |
| `animate` | `true` adds class `hp-anim` | `false` |
| `x.timeDomain` | two RFC 3339 times: the x domain, exactly (`data-hp-x` is those two instants in epoch ms) | the data's extent, widened as the server likes |
| `y.domain` | two numbers: the y domain, exactly (`data-hp-y="linear y0 y1"`) | the data's extent, widened as the server likes |
| `y.unit` | a suffix on every formatted y value (`" ms"`) | none |
| `marks[].type` | `line`, `bar` or `dot` | |
| `marks[].name` | the series name | an unnamed series |
| `marks[].x` | `{"times": […]}` (RFC 3339), `{"floats": […]}` or `{"categories": […]}` | |
| `marks[].y` | numbers; `null` is a missing value | |
| `marks[].labels` | dot labels, one per point; `""` for none | none |

Formatted text (headings, values, tick labels, the description) is
presentation and is never asserted, except where an assertion quotes a
plain value such as `"1"`. Where a result depends on a server's own choices
(an automatic domain, slot order, class names the spec only recommends, the
FNV-1a ids), the assertion is marked `should`.

## `checks.json`

- `level`: the highest conformance level the fixture exercises.
- `forbid`: what must appear nowhere in the chart or table:
  - `elements`, `attrs`, `attrPrefixes`: element names, attribute names and
    attribute-name prefixes (`style`, `on*`, `script`);
  - `where`: a list of `{sel, attrs, elements, deep}`: no element matched by
    `sel` (and with `deep`, none of its descendants) carries one of `attrs`,
    and no descendant is one of `elements`. The fixtures use it for the
    no-`transform` rule of spec §3.3.
- `raw.absent`: strings that must not occur in the raw bytes (read without
  any newline translation: `"\r"` means no CR byte).
- `tipsRaw.absent`: characters that must not occur in the raw text of
  `metadata.hp-tips` (the JSON must escape them).
- `server`: a list of assertions. Each one selects elements and checks them:
  - `sel`: a selector in a small subset of CSS: type selectors (matched on
    local names, ignoring namespaces) and `*`, `.class`, `#id`, `[attr]` and
    `[attr="value"]`, compounds of these, and the descendant (space) and
    child (`>`) combinators. `svg` matches the root.
  - `in`: `"chart"` (default) or `"table"`; `frame` picks a frame in `slide/`.
    A server that offers no table (spec §3.8 is a SHOULD) skips the `table`
    assertions.
  - `count`: the exact number of matches. Without it, at least one.
  - `classes`: classes every match must have (others are allowed).
  - `attrs`: exact attribute values (after XML decoding).
  - `match`: attribute values as regular expressions (ECMAScript and RE2
    agree on the ones used). The geometry attributes are matched number by
    number with the **number** pattern of spec §1.1, so a default float
    format (`24.0`, `1772546400000.0`) fails.
  - `text`: the exact text content (after XML decoding).
  - `tips`: checks on the JSON in a `metadata.hp-tips` match; each key given
    is checked: `mode`, `count` (number of rows), `k` (every row's key, in
    order), `names` (the distinct series names of all rows' entries, in
    order of first appearance) and `slots` (the distinct `[slot, name]`
    pairs, in order of first appearance).
  - `should: true` marks an assertion of a SHOULD or a RECOMMENDED
    derivation (for example the FNV-1a ids). A server may fail it and still
    conform; the Go reference must pass it.
- `unique`: `{sel, attr, in}`: the attribute's values over all matches are
  distinct.
- `stable` (slide): attributes that must be identical across frames.
- `client`: expectations for the reference client, run against the
  reference files:
  - `geometry`: the parsed `data-hp-plot`, `data-hp-x` and `data-hp-y`.
  - `tips`: the parsed payload (`mode`, `n` points, keyboard `ord`, row keys).
  - `hover`: a pointer at viewBox coordinates `at` activates `index` (-1: none).
  - `tooltip`: the heading and rows the tooltip shows at `at`.
  - `select`: a click at viewBox coordinates `click` on `target` (`svg` or
    a selector), a key sequence `keys`, or both (the keys first), dispatches
    `htmx:plot:select` with `detail` (`null`: no event). `clickDetail` is the
    click's `detail` (default 1; 0 is a click from assistive technology or
    script). `params` are the parameters the event adds to a request;
    `query` is their `application/x-www-form-urlencoded` form.
  - `brush` and `keyboardBrush`: a drag from viewBox x `from` to `to` inside
    the plot, or a key sequence, on a chart whose ancestor carries
    `hx-plot:brush`, dispatches `htmx:plot:brush` with `detail`; `rect` is
    the `rect.hp-brush` drawn (viewBox units). After a pointer brush the
    browser's `click` dispatches no select.
  - `slide`: the decision between two frames (`result` `slide`, `snap` or
    `none`, with the `reason` of spec §6.8.3), and for a slide the shift `s`
    and the map `x_old = a · x_new + b`, within `tolerance`. `scale` is the
    chart's CSS px per viewBox unit (default 1) and `config` overrides the
    settings of spec §6.3.
  - `anchored`: the elements that move during a slide (`count`, the
    `selectors` that define them, and some that must not move), the start
    transform per kind of element (`effects`; numbers within `tolerance`),
    and where some of them start (`positions`).
  - `brushAcrossSwap`: a pointer drag that starts on one frame and ends after
    a swap to the next; the press keeps its data value (`anchorAfter` is
    where the rectangle's anchor is redrawn in the new frame).

In `params.json`, `client` cases give a request's URL (`action`), the body
already collected, the autosize box (the `hx-plot:autosize` element at or
above the request's target; `null`: none), `metaCharacter` when htmx has
one, and the triggering event, and list what the extension `adds`; `url` is
informative (htmx builds it). `server` cases give a query string and what a
server reads from it:

- `size`: `default` is the server's own `[width, height]`, `want` what it
  uses (spec §4.6; in Go, `hx.Size(r, defW, defH)`).
- `select`: `ok` is whether a selection is present (`hp-x` non-empty);
  `chart`, `x`, `series` are the raw values; `time` is `x` parsed as a time
  value and written canonically.
- `brush`: `ok` is whether both `hp-x0` and `hp-x1` are present; `chart`,
  `x0`, `x1` are the raw values; `times` are the two parsed and ordered as
  time values in UTC (a value without offset is read as UTC), `timesIn` the
  same with values without offset read in `zone`, and `floats` the two
  parsed and ordered as finite numbers. `"error"` means parsing must fail;
  a server then ignores the brush (spec §5.3).

## Changing a fixture

The reference files are the Go renderer's output, and `TestConformance`
(`conformance_test.go` in the module root) fails when the renderer no
longer reproduces them byte for byte. (They were first made by applying
exactly the spec-1 changes to the v0 output; the spec-1 renderer now
reproduces them unchanged.) After an intentional renderer change,
regenerate them with `go test ./plot -run Conformance -update` and review the
diff like a golden file. `TestConformance` also runs every `server`
assertion (including `should`) and the spec's universal checks
(`speccheck_test.go`) on them. A change to `checks.json` is a change to
the spec: change [spec/spec.md](../../spec/spec.md) in the same commit.
