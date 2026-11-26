"""server.py: an hx-plot server in Python, standard library only.

    python3 examples/python/server.py                       # http://127.0.0.1:8080
    python3 examples/python/server.py --addr 127.0.0.1:9000
    python3 examples/python/server.py --speed 5             # time runs 5x faster

It serves one page of live charts and implements the hx-plot wire spec
(spec/spec.md) at Level 3 without any Go: it is the proof that the htmx
extension does not care what language the server is written in.

The htmx pattern is the same as in any htmx app: every chart is HTML that
the server renders, and htmx swaps it in with innerMorph.

- Latency polls every 2 s (hx-trigger "every 2s"). The server hashes each
  body into an HX-PTag; the hx-ptag extension sends it back, and an
  unchanged chart costs a bodiless 304 (spec §4.5).
- Throughput streams over server-sent events (hx-sse:connect), one frame a
  second, inside the container that spec §4.5 recommends.
- Requests by region is a bar chart; a click on a bar (htmx:plot:select)
  becomes a GET with hp-chart, hp-x and hp-series, answered with a detail
  chart (spec §5.2). Plain links under the chart send the same request.
- A drag across the latency chart (htmx:plot:brush) becomes a GET with
  hp-x0 and hp-x1; the server re-renders that range and pushes its URL with
  HX-Push-Url, so Back undoes the zoom (spec §5.3, §5.4).
- Every chart re-renders at its container's width: the extension adds
  hp-w to the requests of an hx-plot:autosize element (spec §4.6).

render.py draws the charts, data.py makes up the data, and hp_wire.py is
the spec's Appendix B. The scripts and the stylesheet are read from the
repository's client/ directory; a real app gets them from npm or a CDN.
"""

from __future__ import annotations

import argparse
import hashlib
import math
import os
import string
import sys
import threading
import time
import traceback
from datetime import datetime, timedelta, timezone
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer
from typing import Callable, Optional
from urllib.parse import parse_qsl, urlencode, urlsplit

import data
from hp_wire import parse_number, parse_time_value, sse_message, time_value, xml_attr, xml_text
from render import Chart, Mark, keys, render

HERE = os.path.dirname(os.path.abspath(__file__))
CLIENT = os.path.join(HERE, '..', '..', 'client')
UTC = timezone.utc

# The same policy as examples/dashboard: scripts from this origin and the
# pinned htmx release only, and no inline script or style at all. Charts
# carry no style="" or on* attributes (spec §3.6, §7), so they need none.
CSP = ("default-src 'self'; "
       "script-src 'self' https://cdn.jsdelivr.net/npm/htmx.org@4.0.2/dist/; "
       "style-src 'self'; img-src 'self' data:; connect-src 'self'; "
       "base-uri 'none'; form-action 'self'; frame-ancestors 'none'")

DEFAULT_WIDTH = 640  # until the extension reports the container's width
LIVE = 300  # the live latency chart shows five minutes
HISTORY = 86400  # how far back a zoom may reach: the data this server "keeps"
MIN_ZOOM = 60  # a shorter zoom is widened to this: six latency buckets
MAX_POINTS = 120  # a zoomed latency chart uses coarser buckets beyond this
STREAM_SPAN = 60  # the throughput chart shows one minute
MAX_STREAMS = 32  # open SSE streams; more get a 503 (spec §4.5, "Starting")
WINDOWS = 6  # closed windows in the requests chart
MINUTE = 60

# The files the page loads from /static/. hx-plot.js, its brush companion
# and the reference stylesheet come from the repository's client/
# directory; a real app installs htmx-ext-plot from npm or a CDN instead.
STATIC = {
    'hx-plot.js': (os.path.join(CLIENT, 'hx-plot.js'), 'text/javascript; charset=utf-8'),
    'hx-plot-brush.js': (os.path.join(CLIENT, 'hx-plot-brush.js'), 'text/javascript; charset=utf-8'),
    'hx-plot.css': (os.path.join(CLIENT, 'hx-plot.css'), 'text/css; charset=utf-8'),
    'app.css': (os.path.join(HERE, 'static', 'app.css'), 'text/css; charset=utf-8'),
}


# ---- Request parameters (spec §4.6, §5) ------------------------------------
# hp-* values come from the browser and can be anything (spec §7). These
# helpers read them strictly; test_wire.py runs them against the server
# cases of spec/conformance/params.json.

def query(url: str) -> dict[str, str]:
    """The query string's parameters, first value of each, blanks kept."""
    q: dict[str, str] = {}
    for k, v in parse_qsl(urlsplit(url).query, keep_blank_values=True):
        q.setdefault(k, v)
    return q


def size(value: Optional[str], default: float) -> float:
    """hp-w or hp-h in CSS px (spec §4.6): clamped to [50, 4096]; the default
    for a missing, non-numeric, non-finite, zero or negative value."""
    try:
        v = parse_number((value or '').strip())
    except ValueError:
        return default
    return min(max(v, 50.0), 4096.0) if v > 0 else default


def selected(q: dict[str, str]) -> Optional[tuple[str, str, str]]:
    """(hp-chart, hp-x, hp-series) of a select, or None when hp-x is
    missing or empty: no selection (spec §5.2)."""
    if not q.get('hp-x'):
        return None
    return q.get('hp-chart', ''), q['hp-x'], q.get('hp-series', '')


def brushed(q: dict[str, str]) -> Optional[tuple[str, str, str]]:
    """(hp-chart, hp-x0, hp-x1) of a brush, raw, or None when either end is
    missing. The values still have to be parsed by the axis' kind."""
    if not q.get('hp-x0') or not q.get('hp-x1'):
        return None
    return q.get('hp-chart', ''), q['hp-x0'], q['hp-x1']


def brush_times(x0: str, x1: str, tz=UTC) -> tuple[datetime, datetime]:
    """A brush on a time axis, ordered (spec §5.3: never assume x0 <= x1).
    Any RFC 3339 time; a datetime-local value (no offset) is read in tz.
    Raises ValueError when either end does not parse."""
    t0, t1 = parse_time_value(x0, tz), parse_time_value(x1, tz)
    return (t0, t1) if t0 <= t1 else (t1, t0)


def brush_floats(x0: str, x1: str) -> tuple[float, float]:
    """A brush on a linear axis, ordered: finite JSON numbers only."""
    v0, v1 = parse_number(x0), parse_number(x1)
    return (v0, v1) if v0 <= v1 else (v1, v0)


def ptag(body: str) -> str:
    """A content hash of a response body, as an HX-PTag value (spec §4.5)."""
    return '"hp-%s"' % hashlib.sha256(body.encode()).hexdigest()[:16]


def bare(tag: str) -> str:
    """A tag as hx-ptag compares it: trimmed, no W/ prefix, no quotes."""
    tag = tag.strip()
    tag = tag[2:] if tag.startswith('W/') else tag
    return tag[1:-1] if len(tag) >= 2 and tag[0] == tag[-1] == '"' else tag


def utc(s: float) -> datetime:
    return datetime.fromtimestamp(s, UTC)


def hhmm(t: datetime) -> str:
    return t.strftime('%H:%M')


def partial(target: str, text: str) -> str:
    """An <hx-partial>: htmx 4 swaps its content into target after the main
    swap, so one response can also update a live region (spec §4.5)."""
    return f'<hx-partial hx-target="{xml_attr(target)}" hx-swap="innerHTML">{xml_text(text)}</hx-partial>'


def with_table(svg: str, table: str, title: str) -> str:
    """A chart and its table equivalent (spec §3.8) in a disclosure. The
    extension keeps it open or closed across morphs (spec §6.8.7)."""
    return (f'{svg}<details class="table-view"><summary>View as table</summary>'
            f'<div class="table-scroll" tabindex="0" role="region" aria-label="{xml_attr(title)}: data table">'
            f'{table}</div></details>')


# ---- The application ---------------------------------------------------------

class App:
    """What the handlers share: the clock, the page template and the static
    files. Nothing in it changes after construction, so threads share it."""

    def __init__(self, now: Callable[[], float] = time.time, tick: float = 1.0, quiet: bool = False):
        self.now = now  # epoch seconds; tests pass a fixed clock
        self.tick = tick  # seconds between SSE frames
        self.quiet = quiet  # no request log (tests)
        self.stopping = threading.Event()  # set on shutdown: open streams end
        self.streams = threading.BoundedSemaphore(MAX_STREAMS)
        with open(os.path.join(HERE, 'page.html'), encoding='utf-8') as f:
            self.page = string.Template(f.read())
        self.files = {}
        for name, (path, ctype) in STATIC.items():
            with open(path, 'rb') as f:
                self.files[name] = (f.read(), ctype)

    # -- Latency: polled, zoomable --------------------------------------------

    def live_window(self) -> tuple[datetime, datetime]:
        """The live window ends at the last closed bucket, so every poll
        inside one bucket renders the same bytes and gets a 304."""
        end = int(self.now()) // data.LATENCY_STEP * data.LATENCY_STEP
        return utc(end - LIVE), utc(end)

    def zoom_range(self, q: dict[str, str]) -> Optional[tuple[datetime, datetime]]:
        """The range a brush (hp-x0, hp-x1), the zoom form or a bookmark
        (from, to) asks for. None when there is none, or when it does not
        parse or lies outside the data: such a brush is ignored (spec §5.3),
        and a range that merely parses (year 1 to 9999) must never reach a
        query or date arithmetic (spec §7). A range shorter than MIN_ZOOM
        is widened to it, as the Go and Node examples do."""
        for a, b in (('hp-x0', 'hp-x1'), ('from', 'to')):
            if q.get(a) and q.get(b):
                try:
                    t0, t1 = brush_times(q[a], q[b])  # datetime-local is read in UTC
                except ValueError:
                    return None
                s0, s1, now = t0.timestamp(), t1.timestamp(), self.now()
                if not (0 < s1 - s0 <= HISTORY and now - HISTORY <= s0 and s1 <= now + data.LATENCY_STEP):
                    return None
                # Whole seconds, so the zoom's URL (from, to) asks for the
                # same range on every poll, and its polls get 304s.
                s0, s1 = math.floor(s0), math.ceil(s1)
                if s1 - s0 < MIN_ZOOM:
                    # A drag of a few pixels is a brush of a few seconds,
                    # which can fall between two buckets and hold no point
                    # at all. Widen it around its middle, and slide it back
                    # when it would end after the last closed bucket.
                    s0 = math.floor((s0 + s1) / 2 - MIN_ZOOM / 2)
                    s1 = s0 + MIN_ZOOM
                    last = int(now) // data.LATENCY_STEP * data.LATENCY_STEP
                    if s1 > last:
                        s0, s1 = last - MIN_ZOOM, last
                    oldest = math.ceil(now - HISTORY)
                    if s0 < oldest:
                        s0, s1 = oldest, oldest + MIN_ZOOM
                return utc(s0), utc(s1)
        return None

    def latency_body(self, t0: datetime, t1: datetime, width: float) -> str:
        """The latency chart from t0 to t1 and its table."""
        span = (t1 - t0).total_seconds()
        step = data.LATENCY_STEP * max(1, math.ceil(span / data.LATENCY_STEP / MAX_POINTS))
        last = int(self.now()) // data.LATENCY_STEP * data.LATENCY_STEP  # no data from the future
        # From the last bucket before t0 (drawn but clipped, so the line
        # reaches the left edge while the window slides, spec §4.4) to the
        # first bucket at or after t1.
        first = math.ceil(t0.timestamp() / step) * step - step
        times = [s for s in range(first, math.ceil(t1.timestamp() / step) * step + 1, step) if s <= last]
        values = [data.latency(s // data.LATENCY_STEP) for s in times]
        xs = [utc(s) for s in times]
        chart = Chart(id='latency', title='Request latency', width=width, height=260, animate=True,
                      x_domain=(t0, t1), unit=' ms', decimals=1, heading='%H:%M:%S UTC',
                      marks=[Mark('line', 'p50', xs, [v[0] for v in values]),
                             Mark('line', 'p95', xs, [v[1] for v in values])])
        svg, table = render(chart)
        return with_table(svg, table, chart.title)

    def latency_poller(self, zoom: Optional[tuple[datetime, datetime]], width: float) -> str:
        """The polling element. Its hx-get carries the zoom, so later polls
        stay zoomed; hx-ptag seeds the tag of exactly what the first poll
        returns at this width."""
        t0, t1 = zoom or self.live_window()
        body = self.latency_body(t0, t1, width)
        url = '/charts/latency'
        if zoom:
            url += '?' + urlencode({'from': time_value(t0), 'to': time_value(t1)})
        # "load delay:1ms": the first poll fits the chart to the container's
        # real width. htmx 4.0.2 fires a bare "load" before hx-ptag has read
        # the seeded tag; one tick later it is sent. htmx:plot:resize fires
        # when the container's width changes (spec §6.9).
        return (f'<div id="latency-body" class="chart" hx-plot:autosize hx-get="{xml_attr(url)}" '
                'hx-trigger="load delay:1ms, every 2s, htmx:plot:resize" hx-swap="innerMorph" '
                f'hx-sync="closest .zoom:drop" hx-ptag="{xml_attr(ptag(body))}">{body}</div>')

    def zoom_status(self, zoom: Optional[tuple[datetime, datetime]]) -> str:
        if not zoom:
            return 'Live: the last five minutes.'
        f = '%Y-%m-%d %H:%M:%S'
        return f'Showing {zoom[0].strftime(f)} to {zoom[1].strftime(f)} UTC.'

    # -- Throughput: streamed ----------------------------------------------------

    def throughput_body(self, width: float) -> str:
        """The throughput chart: the last minute, one point per second, plus
        the second before the window (spec §4.4)."""
        end = int(self.now())
        times = range(end - STREAM_SPAN - 1, end + 1)
        values = [data.throughput(s) for s in times]
        xs = [utc(s) for s in times]
        chart = Chart(id='throughput', title='Throughput', width=width, height=220, animate=True,
                      x_domain=(utc(end - STREAM_SPAN), utc(end)), unit=' req/s', decimals=0,
                      heading='%H:%M:%S UTC',
                      marks=[Mark('line', m, xs, [v[n] for v in values]) for n, m in enumerate(data.METHODS)])
        svg, table = render(chart)
        return with_table(svg, table, chart.title)

    def stream_element(self, width: float) -> str:
        """The element that holds the stream. Its URL carries the width,
        which is fixed once the stream connects (spec §4.5)."""
        url = '/stream/throughput?' + urlencode({'hp-w': f'{width:g}'})
        return (f'<div class="stream" hx-sse:connect="{xml_attr(url)}" hx-swap="innerMorph">'
                f'{self.throughput_body(width)}</div>')

    # -- Requests: bars and drill-down -------------------------------------------

    def windows(self) -> list[tuple[str, datetime]]:
        """The closed windows the bar chart shows, oldest first, each with
        its category: the start time, "HH:MM" in UTC."""
        end = int(self.now()) // data.WINDOW * data.WINDOW
        starts = [utc(end - data.WINDOW * n) for n in range(WINDOWS, 0, -1)]
        return [(hhmm(t), t) for t in starts]

    def requests_body(self, width: float) -> str:
        """Grouped bars, one group per window, then a link per window (the
        select gesture as plain links, spec §5.5), then the table. A link
        is for the whole window, so it sends no hp-series and its detail
        shows every region."""
        wins = self.windows()
        cats = [c for c, _ in wins]
        chart = Chart(id='requests', title='Requests by region', width=width, height=240, animate=True,
                      unit=' requests', decimals=0, x_label='Window (UTC)',
                      marks=[Mark('bar', r, cats, [data.window_total(r, int(t.timestamp())) for _, t in wins])
                             for r in data.REGIONS])
        svg, table = render(chart)
        # The links carry ids from the category keys, so the morph keeps a
        # focused link on its window while windows come and go (spec §4.1).
        links = []
        for (cat, _), key in zip(wins, keys(cats)):
            q = xml_attr(urlencode({'hp-chart': 'requests', 'hp-x': cat}))
            links.append(f'<a id="requests-w-{key}" href="/?{q}" hx-get="/detail/requests?{q}" '
                         f'hx-target="#requests-detail" hx-swap="innerMorph">{xml_text(cat)}</a>')
        nav = f'<nav class="drill-links" aria-label="Window details">Details: {" ".join(links)}</nav>'
        return with_table(svg + nav, table, chart.title)

    def requests_poller(self, width: float) -> str:
        body = self.requests_body(width)
        return (f'<div id="requests-body" class="chart" hx-plot:autosize hx-get="/charts/requests" '
                'hx-trigger="load delay:1ms, every 5s, htmx:plot:resize" hx-swap="innerMorph" '
                f'hx-ptag="{xml_attr(ptag(body))}">{body}</div>')

    def detail(self, q: dict[str, str], width: float) -> tuple[str, str]:
        """The detail for a select of the requests chart, or a window link:
        (markup, status for the live region). A missing or unknown hp-x is
        no selection (spec §5.2): the detail closes, with a note when a
        window was asked for. hp-x is a category this server wrote, so it
        is matched exactly; hp-series must name a region, or is ignored."""
        sel = selected(q)
        if sel is None or sel[0] not in ('', 'requests'):
            return '', 'Detail closed.'
        _, cat, series = sel
        start = dict(self.windows()).get(cat)
        if start is None:
            note = 'That window is no longer shown. Pick one of the bars.'
            return f'<p class="note">{xml_text(note)}</p>', note
        regions = [series] if series in data.REGIONS else list(data.REGIONS)
        minutes = [start + timedelta(seconds=MINUTE * n) for n in range(data.WINDOW // MINUTE)]
        first = int(start.timestamp()) // MINUTE
        span = f'{hhmm(start)} to {hhmm(start + timedelta(seconds=data.WINDOW))} UTC'
        chart = Chart(id='detail', title=f'Requests per minute, {span}', width=width, height=200,
                      x_domain=(minutes[0], minutes[-1]), unit=' requests', decimals=0, heading='%H:%M UTC',
                      marks=[Mark('line', r, minutes,
                                  [data.requests_per_minute(r, first + n) for n in range(len(minutes))])
                             for r in regions])
        svg, table = render(chart)
        total = sum(data.window_total(r, int(start.timestamp())) for r in regions)
        who = ', '.join(regions)
        status = f'Showing requests per minute for {who}, {span}.'
        return (f'<article class="detail-card" aria-labelledby="detail-heading">'
                f'<h3 id="detail-heading">{xml_text(who)}: {xml_text(span)}</h3>'
                f'<p class="sub">{total} requests in the window. '
                f'<a href="/" hx-get="/detail/requests" hx-target="#requests-detail" hx-swap="innerMorph">Close</a></p>'
                f'{with_table(svg, table, chart.title)}</article>'), status

    # -- The page -----------------------------------------------------------------

    def page_html(self, q: dict[str, str]) -> str:
        """The whole page. Its URL can carry a theme, a zoom (from the zoom
        form without script, or a pushed zoom URL) and an open detail (a
        window link without script)."""
        theme = q.get('theme') if q.get('theme') in ('light', 'dark') else ''
        links = []
        for value, label in (('light', 'Light'), ('dark', 'Dark'), ('', 'System')):
            href = '/?theme=' + value if value else '/'
            current = ' aria-current="page"' if value == theme else ''
            links.append(f'<a href="{href}"{current}>{label}</a>')
        zoom = self.zoom_range(q)
        detail, status = self.detail(q, DEFAULT_WIDTH)
        return self.page.substitute(
            theme=f' data-theme="{theme}"' if theme else '',
            theme_links=' '.join(links),
            zoom_status=xml_text(self.zoom_status(zoom)),
            latency=self.latency_poller(zoom, DEFAULT_WIDTH),
            throughput=self.throughput_body(DEFAULT_WIDTH),
            requests=self.requests_poller(DEFAULT_WIDTH),
            detail=detail,
            detail_status=xml_text(status) if detail else '',
        )


# ---- HTTP ---------------------------------------------------------------------

class Handler(BaseHTTPRequestHandler):
    """Routes requests to App. Every route answers HTML (or a stream);
    there is no JSON API. Error paths answer 204 to htmx, which keeps what
    is on screen, because htmx 4 would swap an error body over a chart."""

    app: App  # set by make_server
    protocol_version = 'HTTP/1.1'  # keep-alive: polls reuse connections
    timeout = 60  # seconds a quiet connection may stay open

    def do_GET(self) -> None:
        try:
            path = urlsplit(self.path).path
            q = query(self.path)
        except ValueError:  # a request target that is not a URL, such as "http://[::1"
            return self.send(400, 'bad request', {'Content-Type': 'text/plain; charset=utf-8'})
        routes = {
            '/': self.page,
            '/charts/latency': self.latency,
            '/charts/requests': self.requests,
            '/detail/requests': self.requests_detail,
            '/cards/throughput': self.throughput_card,
            '/stream/throughput': self.throughput_stream,
        }
        try:
            if path in routes:
                routes[path](q)
            elif path.startswith('/static/') and path[len('/static/'):] in self.app.files:
                body, ctype = self.app.files[path[len('/static/'):]]
                self.send(200, body, {'Content-Type': ctype, 'Cache-Control': 'no-cache'})
            else:
                self.send(404, 'not found', {'Content-Type': 'text/plain; charset=utf-8'})
        except (BrokenPipeError, ConnectionResetError, ConnectionAbortedError):
            pass  # the browser went away
        except Exception:
            traceback.print_exc()
            # An htmx request gets 204: the chart keeps its last good frame.
            self.send(204 if self.headers.get('HX-Request') else 500, '' if self.headers.get('HX-Request')
                      else 'internal error', {'Content-Type': 'text/plain; charset=utf-8'})

    def page(self, q: dict[str, str]) -> None:
        self.send(200, self.app.page_html(q), {'Cache-Control': 'no-cache'})

    def latency(self, q: dict[str, str]) -> None:
        """One URL, three kinds of request (spec §5.4): a brush or the zoom
        form (hp-x0 and hp-x1), "Back to live" (zoom=out), or a poll, zoomed
        when the polling element's URL carries from and to."""
        app, width = self.app, size(q.get('hp-w'), DEFAULT_WIDTH)
        brush = brushed(q)
        if brush or q.get('zoom') == 'out':
            zoom = app.zoom_range(q) if brush else None
            if brush and (zoom is None or brush[0] not in ('', 'latency')):
                return self.send(204)  # a brush that does not parse, or of another chart: ignore it
            # The answer replaces the whole polling element (outerMorph),
            # because only that can change its hx-get. HX-Push-Url names the
            # page URL of this view (spec §5.3), so Back undoes the zoom; the
            # live region says what happened.
            page = '/?' + urlencode({'from': time_value(zoom[0]), 'to': time_value(zoom[1])}) if zoom else '/'
            body = app.latency_poller(zoom, width) + partial('#zoom-status', app.zoom_status(zoom))
            return self.send(200, body, {'HX-Push-Url': page, 'Cache-Control': 'no-cache'})
        zoom = app.zoom_range(q)
        if (q.get('from') or q.get('to')) and zoom is None:
            return self.send(204)  # a zoomed poll whose range left the data: keep the frame
        t0, t1 = zoom or app.live_window()
        self.tagged(app.latency_body(t0, t1, width))

    def requests(self, q: dict[str, str]) -> None:
        self.tagged(self.app.requests_body(size(q.get('hp-w'), DEFAULT_WIDTH)))

    def requests_detail(self, q: dict[str, str]) -> None:
        markup, status = self.app.detail(q, size(q.get('hp-w'), DEFAULT_WIDTH))
        self.send(200, markup + partial('#detail-status', status), {'Cache-Control': 'no-cache'})

    def throughput_card(self, q: dict[str, str]) -> None:
        self.send(200, self.app.stream_element(size(q.get('hp-w'), DEFAULT_WIDTH)), {'Cache-Control': 'no-cache'})

    def throughput_stream(self, q: dict[str, str]) -> None:
        """Server-sent events (spec §4.5): a frame per tick, each one
        unnamed message whose data: lines are the fragment's lines."""
        app, width = self.app, size(q.get('hp-w'), DEFAULT_WIDTH)
        if not app.streams.acquire(blocking=False):
            # A stream that cannot start: a non-2xx status makes a
            # reconnecting hx-sse back off, and HX-Reswap: none keeps htmx
            # from swapping this answer over the chart. Never a 204, which
            # hx-sse would retry about twice a second forever.
            return self.send(503, 'too many streams', {'Content-Type': 'text/plain; charset=utf-8',
                                                       'HX-Reswap': 'none'})
        try:
            self.send_response(200)
            self.security_headers()
            self.send_header('Content-Type', 'text/event-stream')
            self.send_header('Cache-Control', 'no-cache')
            self.send_header('X-Accel-Buffering', 'no')  # a proxy must not buffer the stream
            self.end_headers()
            self.close_connection = True  # no Content-Length: the stream ends with the connection
            # retry: reconnect after 2 s if the stream drops. It rides on
            # the first message with data: hx-sse (htmx 4.0.2) ignores a
            # retry: that arrives in a message of its own.
            retry = b'retry: 2000\n'
            last = None
            while not app.stopping.is_set():
                try:
                    body = app.throughput_body(width)
                except Exception:
                    traceback.print_exc()
                    body = last  # skip the frame; ending the stream would make every page reconnect
                # An unchanged frame is skipped; the comment line still
                # notices a browser that has gone away.
                if body and body != last:
                    self.wfile.write(retry + sse_message(body).encode())
                    retry = b''
                else:
                    self.wfile.write(b': \n\n')
                last = body
                app.stopping.wait(app.tick)
        except OSError:
            pass  # the browser went away (BrokenPipe, ConnectionReset, ...)
        finally:
            app.streams.release()

    # -- Responses ------------------------------------------------------------------

    def tagged(self, body: str) -> None:
        """A polled fragment with its HX-PTag. When the request's HX-PTag
        already names this body, the browser shows it: 304, no body, and
        htmx skips the swap (spec §4.5)."""
        tag = ptag(body)
        if bare(self.headers.get('HX-PTag', '')) == bare(tag):
            return self.send(304, headers={'HX-PTag': tag, 'Cache-Control': 'no-cache'})
        self.send(200, body, {'HX-PTag': tag, 'Cache-Control': 'no-cache'})

    def send(self, code: int, body: str | bytes = b'', headers: Optional[dict[str, str]] = None) -> None:
        """A complete response. HTML unless headers say otherwise; 204 and
        304 carry no body and no Content-Type."""
        raw = body.encode() if isinstance(body, str) else body
        h = {} if code in (204, 304) else {'Content-Type': 'text/html; charset=utf-8'}
        h.update(headers or {})
        self.send_response(code)
        self.security_headers()
        for k, v in h.items():
            self.send_header(k, v)
        if code not in (204, 304):
            self.send_header('Content-Length', str(len(raw)))
        self.end_headers()
        if code not in (204, 304):
            self.wfile.write(raw)

    def security_headers(self) -> None:
        self.send_header('Content-Security-Policy', CSP)
        self.send_header('X-Content-Type-Options', 'nosniff')
        self.send_header('Referrer-Policy', 'no-referrer')

    def log_message(self, format: str, *args) -> None:
        if not self.app.quiet:
            super().log_message(format, *args)


def make_server(app: App, host: str, port: int) -> ThreadingHTTPServer:
    """An HTTP server for app. Each request runs in its own daemon thread,
    so an open stream never keeps the process alive at exit."""
    handler = type('AppHandler', (Handler,), {'app': app})
    server = ThreadingHTTPServer((host, port), handler)
    server.daemon_threads = True
    return server


def main(argv: list[str] | None = None) -> int:
    p = argparse.ArgumentParser(description='An hx-plot server in Python (standard library only).')
    p.add_argument('--addr', default='127.0.0.1:8080', help='host:port to listen on (default 127.0.0.1:8080)')
    p.add_argument('--speed', type=float, default=1.0,
                   help='how many times faster than real time the simulated clock runs, in (0, 1000]')
    args = p.parse_args(argv)
    if not 0 < args.speed <= 1000:
        p.error(f'--speed {args.speed}: want a number in (0, 1000]')
    host, sep, port = args.addr.rpartition(':')
    if not sep or not port.isdigit():
        p.error(f'--addr {args.addr!r}: want host:port')
    app = App(now=data.fast_clock(args.speed) if args.speed != 1 else time.time)
    server = make_server(app, host or '127.0.0.1', int(port))
    shown = host if host not in ('', '0.0.0.0', '::') else 'localhost'
    print(f'listening on http://{shown}:{server.server_address[1]}', file=sys.stderr, flush=True)
    try:
        server.serve_forever()
    except KeyboardInterrupt:
        pass  # Ctrl-C
    finally:
        app.stopping.set()  # end open streams
        server.server_close()
    return 0


if __name__ == '__main__':
    sys.exit(main())
