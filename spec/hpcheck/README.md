# hpcheck

A conformance checker for [hx-plot](../../spec/spec.md) servers in any
language: one Python file, standard library only (Python 3.9+). It checks a
renderer's charts against the [conformance fixtures](../../spec/conformance/README.md),
and a running server against the spec's wire rules.

It is the language-neutral form of the Go reference's own spec tests
(`speccheck_test.go`, `specdom_test.go` and `conformance_test.go` in the
repository root): the same universal checks, with the same `§` in each
message, and the same reading of each fixture's `checks.json`.

## Run

From the repository root (on Windows, type `python` or `py` for `python3`):

```sh
# The fixtures' reference files (a self-check of the checker).
python3 spec/hpcheck/hpcheck.py fixtures

# Your renderer's output for the fixtures: OUT/<fixture>/chart.svg,
# table.html, or frame-a.svg and frame-b.svg for slide.
python3 spec/hpcheck/hpcheck.py fixtures --out OUT --require line-time,empty,bars-band,escaping,collisions

# Charts saved as files, or HTML pages and fragments that hold charts.
# A .svg file, or a file that is one <svg> element, is one chart; anything
# else (a poll response that is a chart and then its table, say) is a page.
python3 spec/hpcheck/hpcheck.py file chart.svg --table table.html
python3 spec/hpcheck/hpcheck.py file page.html

# A running server, as its hpcheck.json describes it.
python3 spec/hpcheck/hpcheck.py server http://127.0.0.1:8080 --config hpcheck.json

# The same, starting the server on a free port first and stopping it after.
python3 spec/hpcheck/hpcheck.py server http://127.0.0.1:8080 --config examples/python/hpcheck.json \
    --spawn "python3 examples/python/server.py --addr 127.0.0.1:{port}"
```

`--spawn` checks only the server it started. A `--port` that something
already listens on is a usage error (exit 2). A server that exits during
start-up, or a moment after the port first answers (it could not bind, and
another process answered), is a usage error that shows the server's
output; one that exits during the run is a `FAIL §2`. On every way out,
Ctrl-C included, the spawned server and its child processes are stopped.

The Go dashboard's config is [`targets/dashboard.json`](targets/dashboard.json).
Build it first, so that stopping the server stops the server and not only
`go run`:

```sh
go build -o /tmp/dashboard ./examples/dashboard
python3 spec/hpcheck/hpcheck.py server http://127.0.0.1:8080 \
    --config spec/hpcheck/targets/dashboard.json --spawn "/tmp/dashboard -addr 127.0.0.1:{port}"
```

## What it prints

One line per problem, then a summary:

```text
FAIL §3.2: root data-hp = '', want '1', line-time/chart.svg
WARN §4.6: hp-w asks for width 120, the chart is 200.0, /charts/latency?hp-w=120
hpcheck: 1 failure(s), 1 warning(s)
```

`FAIL` is a MUST, `WARN` a SHOULD (and a `should` assertion of a
fixture); `--strict` makes warnings fail too. A fixture assertion names its
entry in `checks.json`, as in `FAIL checks.json server[3] g.hp-marks > g.hp-series: ...`.
Exit status: 0 pass (warnings allowed), 1 failures, 2 a usage or I/O error.

## What it checks

**Universal checks**, on every chart it sees (spec §3 and §4, at Level 2).
In an HTML page or fragment a chart is an `<svg … data-hp>`, and also any
other `svg.hp`: the client treats every `svg.hp` as a chart (§6.4), so a
legend swatch or icon given the class `hp` fails (use another class, such
as `hp-spark`, which still picks up the `hp-sN` series colors). It finds
them as the browser's HTML parser does, so the text `<svg` in a comment,
in another tag's attribute value, in `<script>`, `<textarea>` or `<title>`
text, or left open inside a `<template>`, does not hide the charts after
it. The checks:
well-formed XML without a raw CR, and every element and attribute name
spelled as the HTML parser leaves it (lowercase, or the SVG camelCase
names such as `viewBox` and `clipPath`), since inlined charts are never
read as XML (§3.1); the root element's attributes,
`viewBox` and leading `<title>` and `<desc>` (§3.2, §3.3); no `script`,
`foreignObject` or SMIL animation element (`set`, `animate`,
`animateMotion`, `animateTransform`), which could set any attribute at run
time, no `style` or `on*` attribute, in any letter case, no `href`,
`xlink:href` or `src` other than `#…`, every `url(…)` in an attribute
other than `data-*` and `aria-*` a `url(#id)` of an id in the chart, and
a `<style>` element (a standalone file's theme) with no `@import` and no
`url(…)` to anything else (§3.6); a `<style>` element in a chart on a page
warns, as a strict CSP refuses it (§7); no
`transform` where positions must be absolute (§3.3); ids unique and in the
chart's namespace, series and bar ids (§4.1); across an HTML page or
fragment, each chart id and each `{id}-table` once, and no id shared by
two charts or tables (§4.1); series groups and slots (§3.3); the geometry
attributes in the number form (§1.1, §4.2); the hover metadata: its raw
text, its JSON, its mode, its rows and their keys, and that each key mapped
through the geometry lands where its row is drawn (§3.5, §4.2, §4.3); and,
with the table, its caption, headers, row ids, and rows that match the
metadata row for row (§3.8).

**fixtures** adds each fixture's `checks.json`: `forbid`, `raw.absent`,
`tipsRaw.absent`, the `server` assertions, `unique` and `stable`. The
`client` part is for the extension and is not read.

**server** reads a config (`hpcheck.json`, kept next to the server):

```json
{ "level": 3,
  "pages": ["/"],
  "charts": [
    {"url": "/charts/latency", "ptag": true, "autosize": true, "table": true,
     "brush": "/charts/latency", "select": null},
    {"url": "/charts/requests", "ptag": true, "autosize": true, "table": true,
     "select": "/detail/requests"}],
  "sse": [{"url": "/stream/throughput", "frames": 2, "timeout": 10}] }
```

| Key | Checks |
|---|---|
| `pages` | `200`, `text/html; charset=utf-8`, and the universal checks on every chart on the page |
| `charts[].url` | the same for the fragment, requested with `HX-Request: true`; no parameter of [`params.json`](../../spec/conformance/params.json) gets a `5xx`, with or without `HX-Request` (§7), and every `200` holds charts that pass the universal checks (an `hp-w=abc` must not reach the markup, §4.6) |
| `table` | every chart in the fragment has its `{id}-table` (§3.8) |
| `ptag` | an `HX-PTag` on each frame; the current tag, also as `W/"…"`, with spaces and without quotes, gets a bodiless `304`; another tag gets a `200` (§4.5) |
| `autosize` | every size case of `params.json` is accepted with charts that pass the universal checks (MUST), and a usable `hp-w` sets the chart's width, clamped to [50, 4096] (SHOULD, §4.6) |
| `brush` | every brush case of `params.json` sent to this URL: no `5xx`; one that does not parse is not answered as a zoom (no `HX-Push-Url`, SHOULD); a brush across the middle of the chart's own domain is answered `200` with the zoomed chart and `HX-Push-Url` (SHOULD; a `204` or any other status warns) (§5.3) |
| `select` | every select case of `params.json`, and the chart's first row key: no `5xx`, and the charts in a `200` pass (§5.2) |
| `sse` | `text/event-stream`; `frames` messages within `timeout` seconds, each one's `data:` lines a fragment that passes the universal checks; lines may end in CRLF, LF or CR, but no raw CR inside a fragment's line (§3.6), which hx-sse would read as a line break; a `retry:` in a message that also carries data (SHOULD; hx-sse 4.0.2 ignores a `retry:` on its own) (§4.5) |

`level` (or `--level`) below 3 skips the gesture checks, and below 2 the
`ptag`, `autosize` and `sse` checks. The universal checks are those of a
Level 2 chart, so a Level 1 server fails the geometry checks.

## Tests

```sh
python3 -m unittest discover spec/hpcheck
```

[`test_hpcheck.py`](test_hpcheck.py) checks that every reference fixture
passes and that the checker is not vacuous: mutations of a valid chart and
table (each with the `§` it must report), of fixture output, and of a small
fake server (no `304`, a `5xx` on a junk brush, a CR in a stream, no
`retry:`, a fixed width, a brush that is ignored, a `NaN` width for junk
`hp-w`), a stream with CRLF line ends that must pass, pages with a chart
twice or an id shared by two charts, a self-closing `<svg/>` icon or the
text `<svg` in a comment, an attribute value, raw text or a template before
a broken chart, a fragment that starts with its chart, plus `--spawn` starting and stopping a
server, refusing a port that is taken, reporting a server that dies, and
stopping the server on Ctrl-C.
