"""render.py: draw hx-plot charts as SVG, and their data tables, in Python.

This is the part of a server that the wire spec (docs/spec.md) is about. It
draws two kinds of chart, which is what the example page needs:

- line charts on a time axis (hover mode "x", spec §3.5), which can slide
  as their window moves (spec §4.4);
- grouped bars on a band axis (hover mode "band").

A chart with no marks at all is drawn as an empty mode "x" chart. render()
returns the <svg> and the <table> that goes with it (spec §3.8). Nothing in
here knows about HTTP or htmx: server.py decides what to draw, this module
only draws it, and render_fixtures.py feeds it the conformance fixtures.

The output meets spec Level 2: ids for every chart, series, bar and table
row (§4.1), the geometry attributes (§4.2) and row keys (§4.3). Every string
that comes from data goes through hp_wire's xml_text, xml_attr or tips_json
(§3.6), and every number that is compared as text goes through number,
epoch_ms or time_value (§1.1). The same Chart always gives the same bytes
(§4.4), which is what lets polls be answered with 304.
"""

from __future__ import annotations

import math
import re
from dataclasses import dataclass, field
from datetime import datetime, timedelta, timezone, tzinfo
from typing import Iterable, Optional, Sequence

from hp_wire import epoch_ms, number, time_value, tips_json, wire_text, xml_attr, xml_text

FONT = "system-ui, -apple-system, 'Segoe UI', sans-serif"
DASH = chr(0x2013)  # the en dash that stands for a missing value (spec §3.5)
MISSING_CELL = f'<td><span aria-hidden="true">{DASH}</span><span class="hp-sr"> no data</span></td>'  # spec §3.8
MAX_SERIES = 8  # the stylesheet has eight colour slots (spec §3.3)


@dataclass
class Mark:
    """One data series. kind is 'line' (xs are aware datetimes) or 'bar'
    (xs are category strings). ys holds one value per x; None is missing."""
    kind: str
    name: Optional[str]  # None: an unnamed series
    xs: Sequence
    ys: Sequence[Optional[float]]


@dataclass
class Chart:
    """Everything render() needs. Sizes are in viewBox units, which the page
    shows 1:1 as CSS px."""
    id: str  # unique on the page and stable across frames (spec §4.1)
    title: str
    marks: list[Mark] = field(default_factory=list)
    width: float = 640
    height: float = 320
    animate: bool = False  # class hp-anim: slide and tween updates (spec §6.8)
    x_domain: Optional[tuple[datetime, datetime]] = None  # a time window, used exactly
    y_domain: Optional[tuple[float, float]] = None  # used exactly; else a nice extent
    unit: str = ''  # appended to every formatted value
    decimals: int = 2  # values are rounded to this many decimals for display
    tz: tzinfo = timezone.utc  # the zone of tick labels and row headings
    heading: str = '%H:%M:%S'  # strftime format of a row heading on a time axis
    x_label: str = ''  # the table's first column; default "Time" or "Category"


# ---- Keys and ids (spec §4.1, Appendix A) -----------------------------------

def fnv1a(s: str) -> str:
    """32-bit FNV-1a of the UTF-8 bytes, as 8 lowercase hex digits. wire_text
    replaces lone surrogates first, which str.encode() would raise on."""
    h = 0x811C9DC5
    for c in wire_text(s).encode():
        h = (h ^ c) * 0x01000193 & 0xFFFFFFFF
    return '%08x' % h


def keys(strings: Iterable[str]) -> list[str]:
    """The keys of one key set, in order. A hash that is already taken (a
    repeated string or a collision) becomes the hash of 's#n' for the
    smallest free n >= 1, so every id stays unique."""
    taken: set[str] = set()
    out = []
    for s in strings:
        k, n = fnv1a(s), 0
        while k in taken:
            n += 1
            k = fnv1a('%s#%d' % (s, n))
        taken.add(k)
        out.append(k)
    return out


# ---- Small helpers -----------------------------------------------------------

def num(v: float) -> str:
    """A coordinate in markup: rounded to 2 decimals, in the number form."""
    return number(round(v, 2))


def coord(v: float):
    """A coordinate in the hp-tips JSON: rounded to 2 decimals, and an int
    when whole, so json.dumps writes 24 rather than 24.0.

    float(v) first: round() of an int returns an int, and int.is_integer
    only exists from Python 3.12 (this example supports 3.10)."""
    r = round(float(v), 2)
    return int(r) if r.is_integer() else r


def nice(span: float, count: float) -> float:
    """A 1, 2 or 5 step that cuts span into about count intervals."""
    p = 10 ** math.floor(math.log10(span / count))
    return next(m * p for m in (1, 2, 5, 10) if m * p >= span / count)


def finite(v) -> Optional[float]:
    """v as a float, or None when it is missing or not finite."""
    return float(v) if v is not None and math.isfinite(v) else None


_ID = re.compile(r'[A-Za-z0-9_:.-]+')  # spec §3.2: the id appears in url(#...)
_TIME_STEPS = (1, 2, 5, 10, 15, 30, 60, 120, 300, 600, 900, 1800, 3600,
               7200, 10800, 21600, 43200, 86400)


# ---- The chart ----------------------------------------------------------------

def render(c: Chart) -> tuple[str, str]:
    """One frame of chart c: (svg, table)."""
    if not _ID.fullmatch(c.id):
        raise ValueError(f'chart id {c.id!r}: use ASCII letters, digits, "-", "_", ":" and "."')
    if len(c.marks) > MAX_SERIES:
        raise ValueError(f'{len(c.marks)} series: at most {MAX_SERIES}')
    kinds = {m.kind for m in c.marks}
    if not kinds <= {'line', 'bar'} or len(kinds) > 1:
        raise ValueError(f'mark kinds {sorted(kinds)}: draw lines or bars, not both')
    for m in c.marks:
        if len(m.xs) != len(m.ys):
            raise ValueError(f'series {m.name!r}: {len(m.xs)} x values but {len(m.ys)} y values')
    band = kinds == {'bar'}
    mode = 'band' if band else 'x'  # the mode rule of spec §3.5 (no dots here)

    # Series: display names, keys and colour slots, in mark order (§4.1).
    names = [m.name if m.name is not None else f'Series {i + 1}' for i, m in enumerate(c.marks)]
    skeys = keys(m.name if m.name is not None else f'#{i}' for i, m in enumerate(c.marks))
    slots = range(1, len(c.marks) + 1)

    def fmt(v: Optional[float]) -> str:
        return DASH if v is None else number(round(v, c.decimals)) + c.unit

    # Rows: one per x value the reader can hover, in axis order. Each is
    # (heading, key, [value per series]). On a band axis every category
    # is a row; on a time axis, every x inside the domain where some series
    # has a value (§3.5).
    W, H = c.width, c.height
    if band:
        cats = list(dict.fromkeys(x for m in c.marks for x in m.xs))  # first appearance, mark order
        by_cat = [_first_values(m.xs, m.ys) for m in c.marks]
        rows = [(cat, cat, [vals.get(cat) for vals in by_cat]) for cat in cats]
        domain = None
    else:
        times = sorted({t for m in c.marks for t in m.xs})
        domain = c.x_domain or ((times[0], times[-1]) if times else None)
        if domain and domain[0] == domain[1]:  # a degenerate domain is widened (§4.2)
            domain = (domain[0] - timedelta(seconds=30), domain[1] + timedelta(seconds=30))
        if domain and not domain[0] < domain[1]:
            raise ValueError('time domain: the start must come before the end')
        by_time = [_first_values([time_value(t) for t in m.xs], m.ys) for m in c.marks]
        rows, row_times = [], []
        for t in times:
            k = time_value(t)  # the row key: a time value (§1.1)
            vals = [v.get(k) for v in by_time]
            if domain[0] <= t <= domain[1] and any(v is not None for v in vals):
                rows.append((t.astimezone(c.tz).strftime(c.heading), k, vals))
                row_times.append(t)

    # The y scale: the given domain, or the data's extent widened to nice
    # ticks. Bars always include zero.
    ys = [y for m in c.marks for v in m.ys if (y := finite(v)) is not None]
    if c.y_domain:
        y0, y1 = c.y_domain
        if not y0 < y1:
            raise ValueError('y domain: y0 must be below y1')
        ystep = nice(y1 - y0, 4)
    else:
        lo, hi = (min(ys), max(ys)) if ys else (0.0, 1.0)
        if band:
            lo, hi = min(lo, 0.0), max(hi, 0.0)
        if lo == hi:
            lo, hi = (lo - 1, hi + 1) if lo else (0.0, 1.0)
        ystep = nice(hi - lo, 4)
        y0, y1 = math.floor(lo / ystep) * ystep, math.ceil(hi / ystep) * ystep
    yticks = [n * ystep for n in range(math.ceil(y0 / ystep - 1e-9), math.floor(y1 / ystep + 1e-9) + 1)]
    ylabels = [number(round(v, 9)) for v in yticks]

    # The plot rectangle (§4.2): room for the y tick labels on the left, the
    # x tick labels below and, with several series, a legend above. A time
    # axis keeps room on the right for a tick label centred on the edge.
    legend = len(c.marks) > 1
    L = min(max(len(s) for s in ylabels) * 7 + 12, W * 0.4)
    R = W - (24 if domain else 12)
    T = 26 if legend else 8
    B = H - 22
    if not (L < R and T < B):
        raise ValueError(f'{W} x {H} is too small to draw a plot')

    def y(v: float) -> float:
        return B - (v - y0) / (y1 - y0) * (B - T)

    if domain:
        s0, s1 = domain[0].timestamp(), domain[1].timestamp()

        def x(t: datetime) -> float:
            return L + (t.timestamp() - s0) / (s1 - s0) * (R - L)

    # The hover payload (§3.5). xs are positions in viewBox units.
    if band:
        bw = (R - L) / max(len(cats), 1)  # the band step
        xs = [L + bw * (i + 0.5) for i in range(len(cats))]
    else:
        bw = 0
        xs = [x(t) for t in row_times]
    tips = {'mode': mode, 'xs': [coord(v) for v in xs], 'bw': coord(bw), 'top': coord(T), 'bottom': coord(B),
            'rows': [{'t': t, 'k': k, 'v': [[s, n, fmt(v)] for s, n, v in zip(slots, names, vals)]}
                     for t, k, vals in rows]}

    i = c.id  # checked above: safe in attributes and url(#...)
    a = [f'<svg xmlns="http://www.w3.org/2000/svg" id="{i}" class="hp{" hp-anim" if c.animate else ""}"',
         f' viewBox="0 0 {number(W)} {number(H)}" width="{number(W)}" height="{number(H)}"',
         f' role="img" aria-labelledby="{i}-title" aria-describedby="{i}-desc" tabindex="0"',
         # spec §4.2: the scales actually used to draw this frame. The time
         # domain is written exactly, never rounded: the client compares it
         # between frames to tell a moved window from changed values.
         f' data-hp="1" data-hp-plot="{num(L)} {num(T)} {num(R)} {num(B)}"']
    if band:
        a.append(' data-hp-x="band"')
    elif domain:
        a.append(f' data-hp-x="time {epoch_ms(domain[0])} {epoch_ms(domain[1])}"')
    a.append(f' data-hp-y="linear {number(y0)} {number(y1)}" font-family="{FONT}" font-size="12">')
    out = [''.join(a),
           # spec §3.3: <title> and <desc> are the first two children.
           f'<title id="{i}-title">{xml_text(c.title)}</title>',
           f'<desc id="{i}-desc">{xml_text(_describe(c, names, band, domain, rows, y0, y1))}</desc>',
           # The clip is padded a little, so strokes and end dots on the
           # plot's edge are not cut. The plot above is the unpadded area.
           f'<defs><clipPath id="{i}-clip"><rect x="{num(L - 6)}" y="{num(T - 6)}" '
           f'width="{num(R - L + 12)}" height="{num(B - T + 12)}"/></clipPath></defs>']

    if legend:
        out.append('<g class="hp-legend" aria-hidden="true">')
        lx = 0.0
        for s, n in zip(slots, names):
            out.append(f'<g class="hp-legend-item"><rect class="hp-key hp-s{s}" x="{num(lx)}" y="4" width="10" '
                       f'height="10" rx="2" fill="currentColor"/><text class="hp-legend-label" x="{num(lx + 16)}" '
                       f'y="9" dy="0.32em" fill="currentColor" fill-opacity="0.7">{xml_text(n)}</text></g>')
            lx += 16 + len(n) * 6.5 + 14
        out.append('</g>')

    out.append('<g class="hp-grid" aria-hidden="true" stroke="currentColor" stroke-opacity="0.12">')
    out += [f'<line x1="{num(L)}" x2="{num(R)}" y1="{num(y(v))}" y2="{num(y(v))}"/>' for v in yticks if v != y0]
    out.append('</g>')
    if band:  # spec §3.3: the band highlight sits under the bars, hidden
        out.append(f'<rect class="hp-band-hl" visibility="hidden" x="{num(L)}" y="{num(T)}" width="{num(bw)}" '
                   f'height="{num(B - T)}" fill="currentColor" fill-opacity="0.06"/>')

    # The marks: one group per series, its id from the series key (§4.1).
    # No transform attributes in here: the client slides these with CSS.
    out.append(f'<g class="hp-marks" clip-path="url(#{i}-clip)">')
    ends = []  # the newest point of each line, for its end dot
    for j, (m, n, sk, s) in enumerate(zip(c.marks, names, skeys, slots)):
        out.append(f'<g id="{i}-s-{sk}" class="hp-series hp-s{s}" data-hp-series="{xml_attr(n)}">')
        if band:
            out += _bars(i, j, len(c.marks), m, n, sk, cats, xs, bw, y, y0, y1, fmt)
        else:
            d, end = _line_path(m, x, y, domain)
            out.append(f'<path class="hp-line" d="{d}" fill="none" stroke="currentColor" stroke-width="2" '
                       'stroke-linejoin="round" stroke-linecap="round"/>')
            if end:
                ends.append((s, end))
        out.append('</g>')
    out.append('</g>')

    # The x axis. Tick labels are positioned with x and y only (§3.3): a
    # rotated label would need a transform, which the slide would replace.
    out.append(f'<g class="hp-axis hp-axis-x" aria-hidden="true"><line class="hp-baseline" x1="{num(L)}" '
               f'x2="{num(R)}" y1="{num(B + 0.5)}" y2="{num(B + 0.5)}" stroke="currentColor" stroke-opacity="0.3"/>')
    tick = ('<text class="hp-tick" x="{}" y="' + num(H - 6) + '" text-anchor="middle" fill="currentColor" '
            'fill-opacity="0.7">{}</text>')
    if band:
        every = max(1, math.ceil(len(cats) * 60 / (R - L)))  # thin crowded labels
        out += [tick.format(num(cx), xml_text(cat)) for n, (cat, cx) in enumerate(zip(cats, xs)) if n % every == 0]
    elif domain:
        out += [tick.format(num(x(t)), xml_text(label)) for t, label in _time_ticks(domain, R - L, c.tz)]
    out.append('</g><g class="hp-axis hp-axis-y" aria-hidden="true">')
    out += [f'<text class="hp-tick" x="{num(L - 8)}" y="{num(y(v))}" dy="0.32em" text-anchor="end" '
            f'fill="currentColor" fill-opacity="0.7">{label}</text>' for v, label in zip(yticks, ylabels)]
    out.append('</g>')

    out.append('<g class="hp-labels" aria-hidden="true">')
    out += [f'<circle class="hp-end hp-s{s}" cx="{num(px)}" cy="{num(py)}" r="4" fill="currentColor" '
            'stroke="#fff" stroke-width="2"/>' for s, (px, py) in ends]
    if not c.marks:
        out.append(f'<text class="hp-label hp-empty" x="{num((L + R) / 2)}" y="{num((T + B) / 2)}" dy="0.32em" '
                   'text-anchor="middle" fill="currentColor" fill-opacity="0.7">No data</text>')
    out.append('</g>')

    # The hover group (§3.3): the crosshair in mode x, and the payload.
    # tips_json escapes <, > and &, so the JSON can never close the element.
    out.append('<g class="hp-hover" aria-hidden="true">')
    if not band:
        out.append(f'<line class="hp-crosshair" visibility="hidden" x1="{num(L)}" x2="{num(L)}" y1="{num(T)}" '
                   f'y2="{num(B)}" stroke="currentColor" stroke-opacity="0.4"/>')
    out.append(f'<metadata class="hp-tips">{tips_json(tips)}</metadata></g></svg>')

    return ''.join(out), _table(c, names, rows, band, fmt)


def _first_values(xs: Sequence, ys: Sequence) -> dict:
    """{x: y} for one series, keeping the first value of a repeated x."""
    out: dict = {}
    for k, v in zip(xs, ys):
        out.setdefault(k, finite(v))
    return out


def _line_path(m: Mark, x, y, domain) -> tuple[str, Optional[tuple[float, float]]]:
    """The path of one line, broken at missing values, and its newest point
    inside the domain. Points outside the domain (the overscan point before
    a sliding window, spec §4.4) are drawn and then clipped."""
    d, pen, end = [], 'M', None
    for t, v in sorted(zip(m.xs, m.ys), key=lambda p: p[0]):
        v = finite(v)
        if v is None:
            pen = 'M'  # a gap
            continue
        px, py = x(t), y(v)
        d.append(f'{pen}{num(px)},{num(py)}')
        pen = 'L'
        if domain[0] <= t <= domain[1]:
            end = (px, py)
    return ''.join(d), end


def _bars(cid, j, k, m, name, sk, cats, centres, bw, y, y0, y1, fmt) -> list[str]:
    """Series j of k in a grouped bar chart: one path per present value,
    with the id {chart}-b-{seriesKey}-{catKey} (§4.1) and a native <title>
    tooltip that works without script (§3.7)."""
    ckeys = dict(zip(cats, keys(cats)))
    vals = _first_values(m.xs, m.ys)
    gap = 2
    w = max(1.0, min(28.0, (bw * 0.8 - gap * (k - 1)) / k))
    base = y(min(max(0.0, y0), y1))  # bars grow from zero, or the domain's edge
    out = []
    for cat, cx in zip(cats, centres):
        v = vals.get(cat)
        if v is None:
            continue  # a missing value has no bar
        bx = cx - (k * w + (k - 1) * gap) / 2 + j * (w + gap)
        top = y(min(max(v, y0), y1))
        out.append(f'<path id="{cid}-b-{sk}-{ckeys[cat]}" class="hp-bar" '
                   f'd="M{num(bx)},{num(base)}V{num(top)}H{num(bx + w)}V{num(base)}Z" fill="currentColor">'
                   f'<title>{xml_text(f"{name} · {cat}: {fmt(v)}")}</title></path>')
    return out


def _time_ticks(domain, width: float, tz: tzinfo) -> list[tuple[datetime, str]]:
    """Ticks on round times, about 70 px apart, with labels in tz."""
    s0, s1 = domain[0].timestamp(), domain[1].timestamp()
    step = next((s for s in _TIME_STEPS if (s1 - s0) / s <= max(1.0, width / 70)), 86400)
    label = '%b %d' if step >= 86400 else '%H:%M' if step >= 60 else '%H:%M:%S'
    ticks = []
    for s in range(math.ceil(s0 / step) * step, math.floor(s1) + 1, step):
        t = datetime.fromtimestamp(s, timezone.utc)
        ticks.append((t, t.astimezone(tz).strftime(label)))
    return ticks


def _describe(c: Chart, names, band, domain, rows, y0, y1) -> str:
    """The chart's <desc>: its type, series and the range of each axis (§3.3)."""
    if not c.marks:
        return 'Empty chart.'
    kind = 'Bar chart' if band else 'Line chart'
    yrange = f'Y axis {number(round(y0, 9))} to {number(round(y1, 9))}{c.unit}.'
    if band:
        xrange = f'{len(rows)} categories from {rows[0][0]} to {rows[-1][0]}.' if rows else 'No categories.'
    else:
        f = c.heading
        xrange = f'Time from {domain[0].astimezone(c.tz).strftime(f)} to {domain[1].astimezone(c.tz).strftime(f)}.'
    return f'{kind} of {", ".join(names)}. {xrange} {yrange}'


def _table(c: Chart, names, rows, band, fmt) -> str:
    """The table equivalent (§3.8): one row per hover row, in the same order
    and with the same formatted values, and the ids of §4.1. A band row
    shares its key with the category's bars."""
    first = c.x_label or ('Category' if band else 'Time')
    head = ''.join(f'<th scope="col">{xml_text(n)}</th>' for n in [first, *names])
    body = []
    for rk, (t, _, vals) in zip(keys(k for _, k, _ in rows), rows):
        cells = ''.join(MISSING_CELL if v is None else f'<td>{xml_text(fmt(v))}</td>' for v in vals)
        body.append(f'<tr id="{c.id}-r-{rk}"><th scope="row">{xml_text(t)}</th>{cells}</tr>')
    return (f'<table class="hp-table" id="{c.id}-table"><caption>{xml_text(c.title)}</caption>'
            f'<thead><tr>{head}</tr></thead><tbody>{"".join(body)}</tbody></table>')
