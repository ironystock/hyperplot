# Implementing an hx-plot server

The `hx-plot` htmx extension doesn't care what language your server is
written in. Your server sends a chart as SVG markup, answers ordinary htmx
requests and reads a few request parameters, and the extension does the rest.
The contract between the two is the [wire spec](../spec/spec.md). This guide walks
through it by building a working server in Python with only the standard
library: one live line chart that polls, slides as its time window moves,
drills down on a click and zooms on a drag. It meets
[Level 3](../spec/spec.md#21-server-levels), the highest level.

The code below is complete. Put the blocks in one file, in order, and it
runs. It was tested with Python 3.13, htmx 4.0.2 and `hx-plot.js` 0.2.0 in
Chrome. It's about 320 lines, half of them drawing the chart. A real server
would use its own templates and data, but the wire details are the same.
For more than one chart, the [Python](../examples/python/) and
[Node](../examples/node/) examples are complete servers on the same
standard libraries, and each [Go recipe](../examples/recipes/README.md)
shows one pattern's HTML and headers alone, whatever language you port
it to.

**Contents**

- [Who does what](#who-does-what)
- [Setup](#setup)
- [Step 1: value forms, escaping and keys](#step-1-value-forms-escaping-and-keys)
- [Step 2: draw a chart](#step-2-draw-a-chart)
- [Step 3: data and the time window](#step-3-data-and-the-time-window)
- [Step 4: the page and the live element](#step-4-the-page-and-the-live-element)
- [Step 5: answer requests](#step-5-answer-requests)
- [Run it](#run-it)
- [Check your output](#check-your-output)
- [Conformance checklist](#conformance-checklist)
- [Mistakes that are easy to make](#mistakes-that-are-easy-to-make)

## Who does what

| The server | The extension |
|---|---|
| draws the chart as SVG, with its axes, ticks and labels | reads the chart's markup; it never draws or formats data |
| formats every value and heading, with units | shows the server's text in a tooltip, with `textContent` |
| writes a JSON payload: where the chart can be hovered and what each position reads | hit-tests the pointer and keyboard against that payload |
| gives every chart, series, bar and table row a stable id | keeps hover and keyboard state across `innerMorph` swaps |
| describes its scales in three root attributes | slides a moved time window instead of letting it wobble |
| answers polls, server-sent events and gesture requests with HTML | turns clicks and drags into htmx requests with `hp-*` parameters |

There is no JSON data API. Every response is HTML, and htmx swaps it in.

## Setup

You need five files from the npm package
[`htmx-ext-plot`](../client/README.md), plus htmx 4. Put them in a `static/`
directory next to `server.py`:

| File | What it is |
|---|---|
| `htmx.min.js`, `hx-ptag.js` | htmx 4.0.2 and its polling-tag extension, from `htmx.org@4.0.2/dist/` |
| `hx-sse.js` | htmx's server-sent events extension; only for the SSE variant |
| `hx-plot.js` | the extension |
| `hx-plot-brush.js` | its brushing companion, for drag to zoom |
| `hx-plot.css` | the reference stylesheet. It colours series by slot and sets the `touch-action` that touch scrubbing needs ([§3.4](../spec/spec.md#34-presentation)) |

The package isn't on npm yet. Until it is, take the scripts from
[`client/`](../client/) in this repository. `hx-plot.css` is byte for byte the
Go library's `plot.Stylesheet()`; in a checkout, `go test ./plot -run
StylesheetFile -update` writes it to `client/hx-plot.css`.

Then save the Python block of the spec's
[Appendix B](../spec/spec.md#appendix-b-an-exchange-and-helpers-in-python) as
`hp_wire.py`. Those helpers are part of the spec, and the rest of this guide
imports them.

## Step 1: value forms, escaping and keys

A few small things trip up most first implementations, so the spec gives
them exact rules:

- **Numbers and times are compared as text.** The extension compares a
  chart's x domain between frames to tell a moved window from a value change,
  and a select sends a row's key back for the server to match. Python's
  `str(2.0)` is `"2.0"` and `isoformat()` writes `+00:00`; the wire wants
  `"2"` and `Z` ([§1.1](../spec/spec.md#11-value-forms)). Appendix B's `number`,
  `time_value` and `epoch_ms` write the wire forms.
- **HTML escaping isn't enough.** A chart must be well-formed XML with no
  raw CR byte, so `xml_text` and `xml_attr` also drop control characters and
  write CR, TAB and LF as character references ([§3.6](../spec/spec.md#36-text-and-safety)).
  Series names and categories are often user input; escape every one.
- **Ids come from keys.** A key is the FNV-1a hash of a string. When two
  strings collide, or two series share a name, the second gets the hash of
  `name#1` ([§4.1](../spec/spec.md#41-identity), [Appendix A](../spec/spec.md#appendix-a-fnv-1a-keys)).
- **Python strings can hold what UTF-8 can't.** A lone surrogate (from
  `json.loads`, or a `surrogateescape` decode) makes `str.encode()` raise, so
  one odd series name would take down the route. `xml_text` and `xml_attr`
  drop such characters, `tips_json` writes U+FFFD for them, and `fnv1a` below
  hashes `wire_text(s)`, which does the same.

```python
# server.py, part 1: imports and helpers
import hashlib
import math
import os
import sys
import time
from datetime import datetime, timezone
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer
from urllib.parse import parse_qs, urlencode, urlparse

from hp_wire import (epoch_ms, number, parse_time_value, sse_message,
                     time_value, tips_json, wire_text, xml_attr, xml_text)


def fnv1a(s):
    """32-bit FNV-1a of the UTF-8 bytes, as 8 hex digits (spec Appendix A).
    wire_text replaces lone surrogates, which encode() would raise on."""
    h = 0x811C9DC5
    for c in wire_text(s).encode():
        h = (h ^ c) * 0x01000193 & 0xFFFFFFFF
    return '%08x' % h


def keys(strings):
    """Keys for one key set, in order: a taken hash becomes hash(s#n) (§4.1)."""
    taken, out = set(), []
    for s in strings:
        k, n = fnv1a(s), 0
        while k in taken:
            n += 1
            k = fnv1a('%s#%d' % (s, n))
        taken.add(k)
        out.append(k)
    return out


def utc(s):
    """Epoch seconds as an aware UTC datetime."""
    return datetime.fromtimestamp(s, timezone.utc)


def when(s):
    """A row heading. Headings and values are text the server formats."""
    return utc(s).strftime('%H:%M:%S UTC')


def num(v):
    """A coordinate: rounded to 2 decimals, in the spec's number form."""
    return number(round(v, 2))


def nice(span, count):
    """A 1, 2 or 5 step that cuts span into about count intervals."""
    p = 10 ** math.floor(math.log10(span / count))
    return next(m * p for m in (1, 2, 5, 10) if m * p >= span / count)
```

`fnv1a('p50')` is `2e119f3a`, and `keys(['p50', 'p50'])` is
`['2e119f3a', '4e3e42de']`, as in the spec's examples.

## Step 2: draw a chart

`chart` draws one frame of a line chart on a time axis, and the table that
goes with it. Everything the extension reads is here:

```python
# server.py, part 2: one frame of a chart, and its table
def chart(cid, title, names, points, t0, t1, width, height=240, unit=' ms'):
    """One frame of a line chart on a time axis, and its table.

    names: the series names, in series order. points: (epoch seconds,
    [one value or None per series]) in time order; points outside
    [t0, t1] are drawn but are not hoverable and not in the table (§3.5).
    t0, t1: the x domain, aware datetimes. Returns (svg, table)."""
    s0, s1 = t0.timestamp(), t1.timestamp()
    L, T, R, B = 40, 10, width - 16, height - 26  # the plot, in viewBox units
    vals = [v for _, vs in points for v in vs if v is not None]
    lo, hi = (min(vals), max(vals)) if vals else (0, 1)
    if lo == hi:  # a degenerate domain is widened (§4.2)
        lo, hi = lo - 1, hi + 1
    step = nice(hi - lo, 4)
    y0, y1 = math.floor(lo / step) * step, math.ceil(hi / step) * step

    def x(s):
        return L + (s - s0) / (s1 - s0) * (R - L)

    def y(v):
        return B - (v - y0) / (y1 - y0) * (B - T)

    def fmt(v):
        return '–' if v is None else '%.0f%s' % (v, unit)

    shown = [(s, vs) for s, vs in points if s0 <= s <= s1 and any(v is not None for v in vs)]
    row_keys = [time_value(utc(s)) for s, _ in shown]
    tips = {'mode': 'x', 'xs': [round(x(s), 2) for s, _ in shown], 'bw': 0, 'top': T, 'bottom': B,
            'rows': [{'t': when(s), 'k': k, 'v': [[i + 1, n, fmt(v)] for i, (n, v) in enumerate(zip(names, vs))]}
                     for k, (s, vs) in zip(row_keys, shown)]}

    a, i = xml_attr, xml_attr(cid)
    out = [f'<svg xmlns="http://www.w3.org/2000/svg" id="{i}" class="hp hp-anim" viewBox="0 0 {width} {height}" '
           f'width="{width}" height="{height}" role="img" aria-labelledby="{i}-title" aria-describedby="{i}-desc" tabindex="0" '
           f'data-hp="1" data-hp-plot="{L} {T} {R} {B}" data-hp-x="time {epoch_ms(t0)} {epoch_ms(t1)}" '
           f'data-hp-y="linear {number(y0)} {number(y1)}" font-family="system-ui, sans-serif" font-size="12">'
           f'<title id="{i}-title">{xml_text(title)}</title>'
           f'<desc id="{i}-desc">Line chart of {xml_text(", ".join(names))} from {when(s0)} to {when(s1)}; '
           f'y axis {number(y0)} to {number(y1)}{xml_text(unit)}.</desc>'
           f'<defs><clipPath id="{i}-clip"><rect x="{L}" y="{T - 4}" width="{R - L}" height="{B - T + 8}"/></clipPath></defs>']
    ticks = [y0 + n * step for n in range(round((y1 - y0) / step) + 1)]
    out.append('<g class="hp-grid" aria-hidden="true" stroke="currentColor" stroke-opacity="0.12">')
    out += [f'<line x1="{L}" x2="{R}" y1="{num(y(v))}" y2="{num(y(v))}"/>' for v in ticks]
    out.append(f'</g><g class="hp-marks" clip-path="url(#{i}-clip)">')
    for n, (name, key) in enumerate(zip(names, keys(names))):
        d, pen = [], 'M'
        for s, vs in points:
            if vs[n] is None:
                pen = 'M'  # a gap
                continue
            d.append(f'{pen}{num(x(s))},{num(y(vs[n]))}')
            pen = 'L'
        out.append(f'<g id="{i}-s-{key}" class="hp-series hp-s{n + 1}" data-hp-series="{a(name)}">'
                   f'<path class="hp-line" d="{"".join(d) or "M0,0"}" fill="none" stroke="currentColor" stroke-width="2"/></g>')
    span = s1 - s0
    tstep = next((t for t in (10, 30, 60, 120, 300, 600, 1800, 3600, 7200, 21600, 43200)
                  if span / t <= (R - L) / 80), 86400)
    out.append('</g><g class="hp-axis hp-axis-x" aria-hidden="true">')
    out += [f'<text class="hp-tick" x="{num(x(s))}" y="{B + 18}" text-anchor="middle" fill="currentColor">'
            f'{utc(s).strftime("%H:%M:%S" if tstep < 60 else "%H:%M")}</text>'
            for s in range(math.ceil(s0 / tstep) * tstep, math.floor(s1) + 1, tstep)]
    out.append('</g><g class="hp-axis hp-axis-y" aria-hidden="true">')
    out += [f'<text class="hp-tick" x="{L - 6}" y="{num(y(v))}" dy="0.32em" text-anchor="end" fill="currentColor">'
            f'{number(round(v, 9))}</text>' for v in ticks]
    out.append(f'</g><g class="hp-hover" aria-hidden="true"><line class="hp-crosshair" visibility="hidden" '
               f'x1="{L}" x2="{L}" y1="{T}" y2="{B}" stroke="currentColor" stroke-opacity="0.4"/>'
               f'<metadata class="hp-tips">{tips_json(tips)}</metadata></g></svg>')

    def cell(v):
        if v is None:
            return '<td><span aria-hidden="true">–</span><span class="hp-sr"> no data</span></td>'
        return f'<td>{xml_text(fmt(v))}</td>'

    table = (f'<table class="hp-table" id="{i}-table"><caption>{xml_text(title)}</caption><thead><tr>'
             '<th scope="col">Time</th>' + ''.join(f'<th scope="col">{xml_text(n)}</th>' for n in names) +
             '</tr></thead><tbody>' +
             ''.join(f'<tr id="{i}-r-{rk}"><th scope="row">{when(s)}</th>{"".join(cell(v) for v in vs)}</tr>'
                     for rk, (s, vs) in zip(keys(row_keys), shown)) +
             '</tbody></table>')
    return ''.join(out), table
```

Going through it against the spec:

- **The root** ([§3.2](../spec/spec.md#32-the-root-element)) has the chart's id,
  class `hp` (and `hp-anim`, which turns on animated updates), `role="img"`,
  `tabindex="0"`, `aria-labelledby` pointing at `<title>` and
  `aria-describedby` at `<desc>`, which must be its first two children ([§3.3](../spec/spec.md#33-required-and-conventional-descendants)).
- **`data-hp="1"`** is the spec version. **`data-hp-plot`**, **`data-hp-x`**
  and **`data-hp-y`** describe the scales actually used to draw this frame:
  the plot rectangle in viewBox units, the x domain in epoch milliseconds,
  and the y domain ([§4.2](../spec/spec.md#42-geometry-attributes)). Write the domain
  exactly, unrounded: the extension compares it between frames, and a moved
  window slides.
- **Series groups** sit inside `g.hp-marks`, one per series, with the id
  `{id}-s-{key}`, a colour slot class `hp-s1` to `hp-s8`, and the full name in
  `data-hp-series`. Ids are how htmx's morph matches the old frame's elements
  to the new frame's, so they must stay the same from frame to frame.
- **No `transform` attributes** on the marks, the x axis, the labels or the
  hover group ([§3.3](../spec/spec.md#33-required-and-conventional-descendants)).
  The extension slides those elements with a CSS transform, which would
  replace yours. Position with `x`, `y` and `d` instead.
- **`g.hp-hover`** holds a hidden `line.hp-crosshair` and the payload in
  `metadata.hp-tips` ([§3.5](../spec/spec.md#35-hover-metadata)). In mode `x` the
  payload lists each x position (`xs`), the plot's top and bottom, and one
  row per position: a heading `t`, the row key `k` (the x value as a time
  value), and `[slot, name, value]` for every series, with `"–"` for a
  missing value. `tips_json` escapes `<`, `>` and `&`, so the JSON can't
  close the element.
- **Points outside the x domain** are drawn (and clipped), but they are left
  out of `xs`, the rows and the table. Step 3 uses this for the point just
  before the window's start, so the line reaches the left edge while it
  slides.
- **The table** ([§3.8](../spec/spec.md#38-the-table-equivalent)) is the chart's
  accessible equivalent, and for a line chart the only way to read values
  without script. It has one row per payload row, in the same order and with
  the same formatted values. Its id is `{id}-table`, and each row's id is
  `{id}-r-{key}`. A missing value is written for screen readers, which skip a
  bare dash.

## Step 3: data and the time window

The data here is a stand-in: a deterministic function of time, so the same
10-second bucket always renders the same bytes. Determinism is what makes
polling cheap: an unchanged frame hashes to the same tag and costs a `304`.

```python
# server.py, part 3: data and the time window
STEP, LIVE = 10, 300  # a point every 10 s; the live chart shows 5 minutes
HISTORY = 30 * 86400  # how far back a range may start: the data "kept"
NAMES = ['p50', 'p99']


def latency(s):
    """Stand-in data: p50 and p99 of the 10 s bucket that starts at s."""
    n = s // STEP
    return [40 + 8 * math.sin(n / 5), 95 + 25 * math.sin(n / 7) + n * 37 % 11]


def load(t0, t1):
    """The points from the last one before t0 (overscan, §4.4) to the first at or after t1."""
    first = math.ceil(t0.timestamp() / STEP) * STEP - STEP
    last = math.ceil(t1.timestamp() / STEP) * STEP
    return [(s, latency(s)) for s in range(first, last + 1, STEP)]


def window(q):
    """The x domain: a range from a brush (hp-x0, hp-x1) or a link (from, to),
    or the live window. None when a range is given but does not parse, or
    falls outside the data: parameters are input (§7)."""
    for a, b in (('hp-x0', 'hp-x1'), ('from', 'to')):
        if q.get(a) and q.get(b):
            try:  # any RFC 3339 time, or datetime-local read in UTC (§5.3)
                t0, t1 = sorted([parse_time_value(q[a]), parse_time_value(q[b])])
            except ValueError:
                return None
            s0, s1, now = t0.timestamp(), t1.timestamp(), time.time()
            if not (0 < s1 - s0 <= 86400 and now - HISTORY <= s0 and s1 <= now + STEP):
                return None  # year 1 or 9999 would break utc() and the queries
            return t0, t1, True
    end = int(time.time()) // STEP * STEP
    return utc(end - LIVE), utc(end), False


def width(q):
    """hp-w, clamped (§4.6); this layout needs at least 160 px."""
    try:
        w = float(q.get('hp-w', ''))
    except ValueError:
        return 640
    return round(min(max(w, 160), 4096)) if math.isfinite(w) and w > 0 else 640
```

`window` reads a range in three spellings: `hp-x0` and `hp-x1` from a brush
or the zoom form, and `from` and `to` from a bookmarked URL. A brush sends
time values in UTC, and `<input type="datetime-local">` sends a time with no
offset, which `parse_time_value` reads in the zone you pass (UTC here). The
ends may arrive in either order ([§5.3](../spec/spec.md#53-brush)).

A range that parses is still input. `parse_time_value` accepts years 1 to
9999, and `datetime.fromtimestamp` raises on many of them (on Windows, on
anything before 1970), so an unbounded range would make the handler raise and
the request end with no response at all. `window` therefore checks the range
against the data it has, the last `HISTORY` seconds up to now, before the
range drives a query or a `datetime`, and a range outside it gets a `204` like
one that doesn't parse. Bound yours by what your data store holds.

`width` reads `hp-w`, the width of the chart's container in CSS pixels,
which the extension adds to requests ([§4.6](../spec/spec.md#46-size-parameters)).
The height stays on the server: a container with no CSS height is as tall
as the chart inside it, so a chart rendered at its container's height would
grow on every poll.

## Step 4: the page and the live element

```python
# server.py, part 4: the page and the live element
def frame(t0, t1, w):
    """The polling element's content: the chart and its table."""
    svg, table = chart('lt', 'Request latency', NAMES, load(t0, t1), t0, t1, w)
    return svg + '<details><summary>Table</summary>' + table + '</details>'


def ptag(body):
    return '"hp-%s"' % hashlib.sha256(body.encode()).hexdigest()[:16]


def bare(tag):
    """A tag as hx-ptag compares it: no space, no W/, no quotes (§4.5)."""
    tag = tag.strip()
    tag = tag[2:] if tag.startswith('W/') else tag
    return tag[1:-1] if len(tag) > 1 and tag[0] == tag[-1] == '"' else tag


def poller(t0, t1, zoomed, w):
    """The polling element. A zoom replaces it whole, so its hx-get keeps the range."""
    src = '/chart?' + urlencode({'from': time_value(t0), 'to': time_value(t1)}) if zoomed else '/chart'
    body = frame(t0, t1, w)
    return (f'<div id="latency" hx-get="{xml_attr(src)}" hx-trigger="load, every 2s, htmx:plot:resize" '
            f'hx-swap="innerMorph" hx-sync="closest .zoom:drop" hx-ptag="{xml_attr(ptag(body))}" '
            f'hx-plot:autosize>{body}</div>')


def status(t0, t1, zoomed):
    return f'Showing {when(t0.timestamp())} to {when(t1.timestamp())}.' if zoomed else 'Live: the last 5 minutes.'


PAGE = '''<!doctype html>
<html lang="en">
<meta charset="utf-8">
<title>Latency</title>
<meta name="htmx-config" content='{"noSwap":[204,304,"4xx","5xx"]}'>
<link rel="stylesheet" href="/static/hx-plot.css">
<script src="/static/htmx.min.js"></script>
<script src="/static/hx-ptag.js"></script>
<script src="/static/hx-plot.js"></script>
<script src="/static/hx-plot-brush.js"></script>
<h1>Request latency</h1>
<p id="status" aria-live="polite">%s</p>
<section hx-get="/detail" hx-trigger="htmx:plot:select" hx-target="#detail">
  <div class="zoom" hx-get="/chart" hx-trigger="htmx:plot:brush" hx-target="#latency"
       hx-swap="outerMorph" hx-sync="replace" hx-plot:brush>
    %s
    <form action="/" hx-get="/chart" hx-target="#latency" hx-swap="outerMorph" hx-sync="closest .zoom:replace">
      <label>From <input type="datetime-local" name="hp-x0" step="1"></label>
      <label>To <input type="datetime-local" name="hp-x1" step="1"></label>
      <button>Zoom (UTC)</button> <a href="/">Back to live</a>
    </form>
  </div>
</section>
<p id="detail" aria-live="polite"></p>
'''
```

The page is plain htmx. Reading it from the inside out:

- **`#latency` polls.** `hx-get="/chart"` with `every 2s` and
  `hx-swap="innerMorph"` re-renders the chart on the server and morphs it in.
  `hx-plot:autosize` makes the extension add `hp-w` to the requests that
  target this element, and dispatch `htmx:plot:resize` when its width
  changes, which the trigger turns into a request. The `load` trigger is
  there because the page can't know the container's width: the first paint
  is 640 px wide, and the `load` poll fetches the chart at the real width
  right away (so the chart may visibly resize once). That first poll is
  always a full `200`. After it, hx-ptag sends the tag of the last
  response, and an unchanged chart costs a `304`
  ([§4.5](../spec/spec.md#45-transports)). The `hx-ptag` attribute seeds the tag
  with the first paint's, which pays off only on a page that polls without
  `load` and renders the chart at its container's width: htmx 4.0.2 fires
  `load` before hx-ptag reads the seed, so the `load` poll never sends it.
- **`.zoom` brushes.** `hx-plot:brush` lets the reader drag across the
  chart (or use Shift+Arrow keys, then Enter), and `hx-trigger="htmx:plot:brush"`
  turns the gesture into a request with `hp-chart`, `hp-x0` and `hp-x1`. The
  response replaces `#latency` whole (`outerMorph`), because a zoomed chart
  needs a polling element whose `hx-get` carries the range. The two `hx-sync`
  attributes put the brush and the polls on one queue, so a poll that was
  already in flight can't swap the unzoomed chart back
  ([§5.4](../spec/spec.md#54-example-brush-to-zoom-a-live-chart)).
- **`section` selects.** A click (or Enter, or a second tap on touch) on a
  point dispatches `htmx:plot:select`, which bubbles up to the `section`; its
  request carries `hp-chart`, `hp-x` (the row's key) and, when the pointer
  was on a series, `hp-series`.
- **The form is the plain HTML equivalent** of the brush
  ([§5.5](../spec/spec.md#55-equivalents-in-plain-html)): keyboard and screen-reader
  users, and touch users, who can't brush in spec 1, can zoom with it. It
  sends the same parameters. Without JavaScript it submits to `/`, which
  reads them too. The equivalent for select is a link per table row to
  `/detail?hp-chart=lt&hp-x={key}`; this guide leaves it out for brevity.
- **`#status` and `#detail` are polite live regions.** The extension can't
  know what a request did, so the server says it: the zoom response updates
  `#status` with an `<hx-partial>`, and the detail lands in `#detail`.
- **The `htmx-config` meta tag** stops htmx from swapping error pages (a
  proxy's 502, say) over the chart; htmx 4 swaps every status that `noSwap`
  doesn't list.

The scripts load without `defer`, htmx first. No `hx-ext` attribute is
needed: htmx 4 activates an extension when its script loads.

## Step 5: answer requests

```python
# server.py, part 5: the request handler
class Handler(BaseHTTPRequestHandler):
    def do_GET(self):
        url = urlparse(self.path)
        q = {k: v[0] for k, v in parse_qs(url.query).items()}
        if url.path == '/':
            t0, t1, zoomed = window(q) or window({})
            self.send(200, PAGE % (status(t0, t1, zoomed), poller(t0, t1, zoomed, width(q))))
        elif url.path == '/chart':
            self.chart(q)
        elif url.path == '/detail':
            self.detail(q)
        elif url.path == '/stream':
            self.stream(q)
        elif url.path.startswith('/static/'):
            self.static(url.path[len('/static/'):])
        else:
            self.send(404, 'not found')

    def chart(self, q):
        w = window(q)
        if w is None:
            return self.send(204)  # a range that does not parse: ignore it (§5.3)
        t0, t1, zoomed = w
        if q.get('hp-x0') and q.get('hp-x1'):  # a brush or the zoom form, not a poll (§5.4)
            page = '/?' + urlencode({'from': time_value(t0), 'to': time_value(t1)})
            note = f'<hx-partial hx-target="#status" hx-swap="innerHTML">{status(t0, t1, True)}</hx-partial>'
            return self.send(200, poller(t0, t1, True, width(q)) + note, {'HX-Push-Url': page})
        body = frame(t0, t1, width(q))
        tag = ptag(body)
        if bare(self.headers.get('HX-PTag', '')) == bare(tag):
            return self.send(304, headers={'HX-PTag': tag})  # unchanged: no body, no swap
        self.send(200, body, {'HX-PTag': tag})

    def detail(self, q):
        try:  # hp-x is a row key this server wrote: a time value (§5.2)
            s = parse_time_value(q.get('hp-x', '')).timestamp()
        except ValueError:
            return self.send(204)  # no selection
        now = time.time()
        if s % STEP or not now - HISTORY - STEP <= s <= now + STEP:
            return self.send(204)  # not one of our points (year 1 would break when())
        values = ', '.join(f'{n} {v:.0f} ms' for n, v in zip(NAMES, latency(int(s))))
        self.send(200, f'<b>{when(s)}</b>: {values}')

    def stream(self, q):
        """The same frames over server-sent events (§4.5)."""
        self.send_response(200)
        self.send_header('Content-Type', 'text/event-stream')
        self.send_header('Cache-Control', 'no-cache')
        self.end_headers()
        last = None
        try:
            self.wfile.write(b'retry: 2000\n\n')
            while True:
                t0, t1, _ = window({})
                try:
                    body = frame(t0, t1, width(q))
                except Exception:  # skip the frame: ending the stream makes every page reconnect (§4.5)
                    body = last
                self.wfile.write(sse_message(body).encode() if body != last else b': ping\n\n')
                self.wfile.flush()
                last = body
                time.sleep(1)
        except OSError:
            pass  # the browser went away

    def static(self, name):
        """hx-plot.css, hx-plot.js and htmx, copied next to this file."""
        static = os.path.join(os.path.dirname(os.path.abspath(__file__)), 'static')
        if name not in os.listdir(static):
            return self.send(404, 'not found')
        with open(os.path.join(static, name), 'rb') as f:
            ctype = 'text/css' if name.endswith('.css') else 'text/javascript'
            self.send(200, f.read(), {'Content-Type': ctype + '; charset=utf-8'})

    def send(self, code, body=b'', headers=None):
        body = body.encode() if isinstance(body, str) else body
        self.send_response(code)
        for k, v in {'Content-Type': 'text/html; charset=utf-8', **(headers or {})}.items():
            self.send_header(k, v)
        if body:
            self.send_header('Content-Length', str(len(body)))
        self.end_headers()
        self.wfile.write(body)


if __name__ == '__main__':
    port = int(sys.argv[1]) if len(sys.argv) > 1 else 8000
    print(f'http://127.0.0.1:{port}/')
    ThreadingHTTPServer(('127.0.0.1', port), Handler).serve_forever()
```

What each route does:

- **`/chart` answers a poll** with the chart and its table. It hashes the
  body into a polling tag; when the request's `HX-PTag` header already holds
  that tag, it answers `304` with no body and htmx skips the swap. A resize
  changes the bytes, so it always swaps.
- **`/chart` answers a brush** (it tells one from a poll by `hp-x0` and
  `hp-x1`) with the whole polling element, re-rendered with its x domain fixed
  to the range, so the axes and labels are the server's again. `HX-Push-Url`
  names the page URL for that range, so the zoom can be bookmarked and Back
  undoes it ([§5.3](../spec/spec.md#53-brush)). A range that doesn't parse gets a
  `204`, which swaps nothing.
- **`/detail` answers a select.** `hp-x` is a key the server wrote into the
  chart, so it can be matched exactly. A missing or unknown key is no
  selection, not an error ([§5.2](../spec/spec.md#52-select)). "Unknown" includes a
  well-formed time outside the data: like `window`, `detail` checks the key
  against the `HISTORY` it has before the key reaches `when` and
  `datetime.fromtimestamp`, which raises on year 1 or 9999 on Windows.
- **`/stream` sends the same frames over server-sent events.** `sse_message`
  splits a fragment into `data:` lines at every line break, as hx-sse reads
  them. To use it, load `hx-sse.js` and put the chart on its own page (chart
  ids must be unique on a page):

  ```html
  <div hx-sse:connect="/stream" hx-swap="innerMorph"></div>
  ```

  hx-sse never retries a stream whose first connection fails, and backs
  off only on a non-2xx status when it reconnects one that dropped, so a
  stream route that can't serve answers `503` with `HX-Reswap: none`, never
  a `204`, and a frame that fails to render is skipped, not a reason to end
  the stream. To resize or zoom a streamed chart, swap in a new stream
  element with `innerHTML` or `outerHTML`; a morph leaves the old stream
  reconnecting. [§4.5](../spec/spec.md#45-transports) has the details and a
  container that does both.

Every route answers HTML. Errors are a `204` (keep what's on screen) rather
than a `500` with a body, because htmx 4 swaps error bodies too, and the
`noSwap` setting only protects against errors your handler never sees.

## Run it

```sh
python3 server.py 8000
```

(On Windows the interpreter is usually `python` or `py`, here and below.)

Open http://127.0.0.1:8000/ and try:

- Hover the chart: a tooltip and a crosshair follow the pointer. Tab to the
  chart and use the arrow keys, Home and End; each step is announced.
- Wait ten seconds: the window moves one bucket and the line slides left
  instead of wobbling in place. Most polls in between are `304`s; the
  server's log shows them.
- Click a point: `#detail` shows its values.
- Drag across the chart: it zooms, the address bar gets the range, and Back
  returns to the live view. Polls keep the zoom.
- Open the table under the chart: it updates with the chart, and stays open.
- Make the window narrower: the chart re-renders at the new width.

## Check your output

The fixtures in [`spec/conformance/`](../spec/conformance/README.md)
pin the spec to bytes. Each fixture has an `input.json` that describes a
chart independently of any language, and a `checks.json` whose `server`
assertions test what the spec requires of any server's output, not the
reference server's exact bytes. Render each `input.json` your renderer
supports and run the assertions against your output; the
[README](../spec/conformance/README.md#checksjson) defines the selector
subset and every field. `line-time` and `empty` fit the renderer in this
guide, and its output passes their assertions, including the `should` ones.

[hpcheck](../spec/hpcheck/README.md) runs them for you: one
standard-library Python file, the language-neutral form of the Go
reference's own spec tests. Write your renderer's output for each fixture
to `OUT/<fixture>/chart.svg` (and `table.html`), then, from the repository
root:

```sh
python3 spec/hpcheck/hpcheck.py fixtures --out OUT --require line-time,empty
```

It also checks a running server against the spec's wire rules (content
tags and `304`s, `hp-w`, gesture parameters, SSE frames), as a small
`hpcheck.json` describes it:

```sh
python3 spec/hpcheck/hpcheck.py server http://127.0.0.1:8000 --config hpcheck.json
```

Every failure names the spec section it breaks. The complete servers in
[`examples/python`](../examples/python/) and [`examples/node`](../examples/node/)
go further than this guide (grouped bars, SSE, a drill-down panel) and
show both runs: a fixture renderer, an `hpcheck.json`, and the commands in
their READMEs.

Then check in a browser. Open the developer tools and watch for:

- requests carrying `hp-w`, and for gestures `hp-chart`, `hp-x` or `hp-x0`
  and `hp-x1`;
- polls answered `304` while nothing changed;
- a moved window that slides (the anchored elements get Web Animations)
  rather than a line whose points tween vertically;
- no console errors under a strict Content Security Policy: charts carry no
  `style` attributes or inline handlers.

[`client/browser/examples.browser.mjs`](../client/browser/examples.browser.mjs)
makes the same checks automatically for every example in this repository,
the Python and Node servers included: it starts each one on a free port,
opens it in Chrome, and fails on a console error, a failed request, a
chart that never updates or a gesture with no answer. Point a copy of it
at your server for the same smoke test.

## Conformance checklist

Levels are cumulative. State the level your server meets
([§2.1](../spec/spec.md#21-server-levels)).

### Level 1: static

The page gets native tooltips on bars and dots, and with the extension,
tooltips, a crosshair and keyboard reading on any page.

- [ ] Each chart is an inline `<svg>` that is well-formed XML, contains no
      raw CR byte, and has the root attributes of [§3.2](../spec/spec.md#32-the-root-element):
      `xmlns`, `id`, class `hp`, `viewBox`, `role="img"`, `aria-labelledby`,
      `aria-describedby`, `tabindex="0"`, `data-hp="1"`.
- [ ] `<title id="{id}-title">` and `<desc id="{id}-desc">` are its first two
      child elements ([§3.3](../spec/spec.md#33-required-and-conventional-descendants)).
- [ ] Exactly one `g.hp-hover` holding one `metadata.hp-tips`; a hidden
      `line.hp-crosshair` in mode `x`, a hidden `rect.hp-band-hl` before the
      marks in mode `band`.
- [ ] One `g.hp-series` per data series (at most 8) inside `g.hp-marks`,
      with a unique slot class `hp-s1` to `hp-s8` and `data-hp-series`.
- [ ] No `transform` on the root, the marks, labels, x axis and hover
      groups or anything inside them; no `svg`, `symbol` or `use` there.
- [ ] The payload follows [§3.5](../spec/spec.md#35-hover-metadata): the mode rule,
      `xs` non-decreasing, one row per entry, every series in every row in
      modes `x` and `band`, preformatted values, `"–"` for missing ones,
      `<`, `>` and `&` escaped in the JSON.
- [ ] Every string from data is escaped per [§3.6](../spec/spec.md#36-text-and-safety),
      including control characters; no `<script>`, `<foreignObject>`,
      `style` or `on*` attributes, and no external references.
- [ ] Bars and dots have a native `<title>` (SHOULD,
      [§3.7](../spec/spec.md#37-native-fallback)).
- [ ] A table equivalent per [§3.8](../spec/spec.md#38-the-table-equivalent) next to
      each chart (SHOULD; the MUSTs apply when you offer one).
- [ ] The page serves `hx-plot.css`, or a stylesheet that keeps
      `.hp { touch-action: pan-y pinch-zoom }` ([§3.4](../spec/spec.md#34-presentation)).

### Level 2: live

The page gets morph updates that keep hover and keyboard state, sliding
windows, tables that keep their place and stay open, and `304`s for
unchanged polls.

- [ ] Ids per [§4.1](../spec/spec.md#41-identity): the chart (stable across
      frames), `{id}-s-{key}` per series, `{id}-b-{seriesKey}-{catKey}` per
      bar, `{id}-table` and `{id}-r-{rowKey}` per table row, all unique, with
      the collision rule.
- [ ] `data-hp-plot`, `data-hp-x` and `data-hp-y` describe the scales used to
      draw the frame, in the number form, with unrounded domains
      ([§4.2](../spec/spec.md#42-geometry-attributes)).
- [ ] Every payload row has a key `k` in the key form of
      [§1.1](../spec/spec.md#11-value-forms), unique in modes `x` and `band`
      ([§4.3](../spec/spec.md#43-row-keys)).
- [ ] Points outside the x domain are drawn but are not in `xs`, `pts`,
      `rows` or the table.
- [ ] The same data gives the same bytes; structure and element order stay
      the same between frames ([§4.4](../spec/spec.md#44-frame-stability)).
- [ ] Charts update through htmx polling or SSE with `innerMorph`; polls
      answer `304` to a matching `HX-PTag`; SSE fragments are split into
      `data:` lines at every line break ([§4.5](../spec/spec.md#45-transports)).
- [ ] A stream that can't serve answers `503` with `HX-Reswap: none` (never
      `204`), skips frames that fail to render, and is replaced with
      `innerHTML` or `outerHTML`, never morphed, when its URL changes.
- [ ] Every chart URL accepts `hp-w` and `hp-h`, and clamps them
      ([§4.6](../spec/spec.md#46-size-parameters)).
- [ ] Error paths answer `204` (or a deliberate error fragment), and pages
      set `"noSwap":[204,304,"4xx","5xx"]`.

### Level 3: interactive

The page gets drill-down on click or Enter, and brush-to-zoom with history.

- [ ] Select: `hp-x` is parsed by the chart's x kind and matched against
      your own keys; a missing or unknown value is no selection
      ([§5.2](../spec/spec.md#52-select)).
- [ ] Brush: `hp-x0` and `hp-x1` are ordered by the server; any RFC 3339
      time is accepted on a time axis (and SHOULD the `datetime-local` form,
      read in the chart's zone); any finite JSON number on a linear axis,
      and anything else (`1e999`, `inf`, `1_0`) is rejected, as
      `params.json` checks; a range that doesn't parse is
      ignored ([§5.3](../spec/spec.md#53-brush)).
- [ ] A zoom re-renders with the x domain fixed to the range, and sends
      `HX-Push-Url` with the page URL for it (SHOULD).
- [ ] Plain HTML equivalents (SHOULD, [§5.5](../spec/spec.md#55-equivalents-in-plain-html)):
      a link per table row for select, a from/to form for zoom (sharing the
      brush's `hx-sync` queue), and a polite live region the server updates
      after a gesture.
- [ ] Every `hp-*` value is validated like any input, a parsed range or
      select key is bounded by the data before it drives a query or a date
      computation, and values are escaped when echoed
      ([§7](../spec/spec.md#7-security-considerations)).

## Mistakes that are easy to make

| Mistake | What happens | Fix |
|---|---|---|
| Keys or domains written with the language's default float or date formatting (`2.0`, `+00:00`) | A select's `hp-x` doesn't match your data; the extension sees a changed domain where there is none | `number` and `time_value` from Appendix B |
| Rounding the domain in `data-hp-x` | A sliding window looks like a zoom and snaps instead of sliding | Write the exact domain; round only coordinates |
| Ids that change between frames (a counter, a hash of the data) | The morph replaces everything: nothing animates, hover state resets | Ids from the chart id and the series name or x value |
| The point before the window start in the payload or the table | The pointer can land on a point that isn't visible | Draw it, but leave it out of `xs`, `rows` and the table |
| Rotating tick labels with `transform` | The slide replaces the rotation | Thin the ticks instead |
| An HTML escaper alone (Jinja autoescape, Django `escape`) | Control characters and raw CRs break the XML and SSE lines | `xml_text` and `xml_attr` |
| Rendering at the reported height | The chart grows on every poll | Send only `hp-w` (the default), keep the height |
| A `500` with a body on error | htmx swaps the error text over the chart | `204`, and `noSwap` for errors you don't write |
| A brush or bookmark range used as soon as it parses | Year 1 or 9999 makes date code raise, and the request ends with no response | Bound the range by the data you have; `204` outside it |
| `s.encode()` on a user-supplied name | A lone surrogate raises `UnicodeEncodeError` and takes down the route | `wire_text` before hashing; `xml_text`, `xml_attr` and `tips_json` handle it |
| Using the same chart id twice on a page | The extension and the morph confuse the two | One id per chart per page |

Questions and corrections are welcome in the
[issue tracker](https://github.com/ironystock/hyperplot/issues).
