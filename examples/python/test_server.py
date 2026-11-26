"""Tests of server.py through real HTTP: an in-process server on a free
port, with a fixed clock, so every response is reproducible.

They check what the example teaches: the page is safe under its CSP, every
chart parses as XML, polls get 304 when nothing changed, hp-w sizes the
charts, brush and select change the output, the stream sends frames, and no
parameter, however odd, gets a 5xx.
"""

from __future__ import annotations

import http.client
import itertools
import json
import os
import re
import socket
import threading
import time
import unittest
import xml.etree.ElementTree as ET
from urllib.parse import quote

import server

NOW = 1772547605.0  # 2026-03-03T14:20:05Z
ROOT = os.path.join(os.path.dirname(os.path.abspath(__file__)), '..', '..')
SVG = re.compile(r'<svg\b.*?</svg>', re.S)
TABLE = re.compile(r'<table\b.*?</table>', re.S)


class Server:
    """An App served on 127.0.0.1 and a free port, in a thread."""

    def __init__(self, app: server.App):
        self.app = app
        self.httpd = server.make_server(app, '127.0.0.1', 0)
        self.port = self.httpd.server_address[1]
        self.thread = threading.Thread(target=self.httpd.serve_forever, daemon=True)
        self.thread.start()

    def get(self, path: str, headers: dict[str, str] | None = None) -> tuple[int, http.client.HTTPMessage, str]:
        conn = http.client.HTTPConnection('127.0.0.1', self.port, timeout=10)
        try:
            conn.request('GET', path, headers=headers or {})
            r = conn.getresponse()
            return r.status, r.headers, r.read().decode('utf-8')
        finally:
            conn.close()

    def stop(self):
        self.app.stopping.set()
        self.httpd.shutdown()
        self.httpd.server_close()


def charts(body: str) -> list[ET.Element]:
    """Every <svg> in a response, parsed as XML (spec §3.1)."""
    return [ET.fromstring(s) for s in SVG.findall(body)]


class ServerTest(unittest.TestCase):
    @classmethod
    def setUpClass(cls):
        cls.s = Server(server.App(now=lambda: NOW, quiet=True))

    @classmethod
    def tearDownClass(cls):
        cls.s.stop()

    def get(self, path, headers=None):
        return self.s.get(path, headers)

    def assertSafe(self, body: str):
        """No inline style, handler or script, and every chart and table
        parses as XML."""
        self.assertIsNone(re.search(r'\sstyle\s*=', body), 'a style attribute')
        self.assertIsNone(re.search(r'\son[a-z]+\s*=', body), 'an on* attribute')
        self.assertIsNone(re.search(r'<script(?![^>]*\ssrc=)', body), 'an inline script')
        self.assertNotIn('<style', body)
        self.assertNotIn('\r', body)
        for t in TABLE.findall(body):
            ET.fromstring(t)
        return charts(body)

    def test_page(self):
        status, h, body = self.get('/')
        self.assertEqual(status, 200)
        self.assertEqual(h['Content-Type'], 'text/html; charset=utf-8')
        self.assertEqual(h['Content-Security-Policy'], server.CSP)
        svgs = self.assertSafe(body)
        self.assertEqual([c.get('id') for c in svgs], ['latency', 'throughput', 'requests'])
        # The pinned htmx files, as the dashboard loads them (not deferred).
        with open(os.path.join(ROOT, 'examples', 'dashboard', 'templates', 'page.html'), encoding='utf-8') as f:
            pinned = re.findall(r'<script src="https://cdn[^>]*></script>', f.read())
        for tag in pinned:
            self.assertIn(tag, body)
        # The first frame has no stream; "load" fetches the stream element.
        self.assertNotIn('hx-sse:connect="', body)

    def test_theme(self):
        _, _, body = self.get('/?theme=dark')
        self.assertIn('<html lang="en" data-theme="dark">', body)
        self.assertIn('<a href="/?theme=dark" aria-current="page">', body)
        _, _, body = self.get('/?theme=%22%3E%3Cb%3E')
        self.assertIn('<html lang="en">', body)

    def test_poll_answers_304_when_unchanged(self):
        status, h, body = self.get('/charts/latency?hp-w=700')
        self.assertEqual(status, 200)
        tag = h['HX-PTag']
        self.assertRegex(tag, r'^"hp-[0-9a-f]{16}"$')
        self.assertSafe(body)
        bare = tag.strip('"')
        for sent in (tag, 'W/' + tag, f'  {tag} ', bare):
            with self.subTest(sent=sent):
                status, h, body = self.get('/charts/latency?hp-w=700', {'HX-PTag': sent})
                self.assertEqual((status, body), (304, ''))
                self.assertEqual(h['HX-PTag'], tag)
        status, _, _ = self.get('/charts/latency?hp-w=700', {'HX-PTag': '"hp-0000000000000000"'})
        self.assertEqual(status, 200)
        status, _, _ = self.get('/charts/latency?hp-w=701', {'HX-PTag': tag})
        self.assertEqual(status, 200, 'another width is another body')

    def test_page_seeds_the_tag_of_the_first_poll(self):
        _, _, page = self.get('/')
        seeded = re.search(r'id="latency-body"[^>]*hx-ptag="([^"]*)"', page).group(1).replace('&quot;', '"')
        status, _, _ = self.get('/charts/latency', {'HX-PTag': seeded})
        self.assertEqual(status, 304)

    def test_width(self):
        for query, want in (('hp-w=900', '900'), ('hp-w=10', '50'), ('hp-w=99999', '4096'),
                            ('hp-w=abc', '640'), ('hp-w=-5&hp-h=7', '640'), ('', '640')):
            for url in ('/charts/latency', '/charts/requests', '/cards/throughput'):
                with self.subTest(url=url, query=query):
                    status, _, body = self.get(f'{url}?{query}')
                    self.assertEqual(status, 200)
                    self.assertEqual(charts(body)[0].get('width'), want)

    def test_brush_zooms(self):
        q = 'hp-chart=latency&hp-x0=2026-03-03T14%3A16%3A00Z&hp-x1=2026-03-03T14%3A18%3A30Z'
        status, h, body = self.get('/charts/latency?hp-w=800&' + q)
        self.assertEqual(status, 200)
        self.assertEqual(h['HX-Push-Url'], '/?from=2026-03-03T14%3A16%3A00Z&to=2026-03-03T14%3A18%3A30Z')
        self.assertSafe(body)
        (chart,) = charts(body)
        self.assertEqual(chart.get('data-hp-x'), 'time 1772547360000 1772547510000')
        # The whole polling element comes back, its hx-get carrying the
        # range, and the live region rides along as an <hx-partial>.
        self.assertIn('<div id="latency-body" class="chart" hx-plot:autosize '
                      'hx-get="/charts/latency?from=2026-03-03T14%3A16%3A00Z&amp;to=2026-03-03T14%3A18%3A30Z"', body)
        self.assertIn('<hx-partial hx-target="#zoom-status" hx-swap="innerHTML">Showing', body)
        # Later polls of that URL stay zoomed.
        _, _, poll = self.get('/charts/latency?from=2026-03-03T14%3A16%3A00Z&to=2026-03-03T14%3A18%3A30Z')
        self.assertEqual(charts(poll)[0].get('data-hp-x'), 'time 1772547360000 1772547510000')

    def test_brush_forms(self):
        """Either order, any offset, and the datetime-local form, read in UTC."""
        for x0, x1 in (('2026-03-03T14:18:30Z', '2026-03-03T14:16:00Z'),
                       ('2026-03-03T15:16:00+01:00', '2026-03-03T14:18:30Z'),
                       ('2026-03-03T14:16', '2026-03-03T14:18:30')):
            with self.subTest(x0=x0, x1=x1):
                status, _, body = self.get(f'/charts/latency?hp-x0={quote(x0)}&hp-x1={quote(x1)}')
                self.assertEqual(status, 200)
                self.assertEqual(charts(body)[0].get('data-hp-x'), 'time 1772547360000 1772547510000')

    def test_short_brush_is_widened(self):
        """A drag of a few pixels is a brush of a second or less. It widens
        to MIN_ZOOM around its middle, in whole seconds, and slides back
        when it would end after the last closed bucket (14:20:00)."""
        for x0, x1, want in (
                ('2026-03-03T14:16:00.5Z', '2026-03-03T14:16:00.9Z', ('14:15:30', '14:16:30')),
                ('2026-03-03T14:16:03Z', '2026-03-03T14:16:05Z', ('14:15:34', '14:16:34')),
                ('2026-03-03T14:20:03Z', '2026-03-03T14:20:04Z', ('14:19:00', '14:20:00'))):
            with self.subTest(x0=x0, x1=x1):
                status, h, body = self.get(f'/charts/latency?hp-chart=latency&hp-x0={quote(x0)}&hp-x1={quote(x1)}')
                self.assertEqual(status, 200)
                t0, t1 = (f'2026-03-03T{t}Z' for t in want)
                self.assertEqual(h['HX-Push-Url'], f'/?from={quote(t0)}&to={quote(t1)}')
                self.assertSafe(body)
                ms = [int(server.parse_time_value(t).timestamp()) * 1000 for t in (t0, t1)]
                self.assertEqual(charts(body)[0].get('data-hp-x'), 'time %d %d' % tuple(ms))
                self.assertGreaterEqual(body.count('<tr id='), 5, 'the zoom shows data')
                self.assertNotIn('"rows":[]', body)

    def test_malformed_request_target(self):
        """A request target that is not a URL gets a 400, not a dropped
        connection (spec §7)."""
        with socket.create_connection(('127.0.0.1', self.s.port), timeout=10) as conn:
            conn.sendall(b'GET http://[::1 HTTP/1.1\r\nHost: x\r\nConnection: close\r\n\r\n')
            reply = b''
            while chunk := conn.recv(4096):
                reply += chunk
        self.assertTrue(reply.startswith(b'HTTP/1.1 400 '), reply[:80])

    def test_bad_brushes_are_ignored(self):
        for q in ('hp-x0=abc&hp-x1=2026-03-03T14%3A18%3A30Z',
                  'hp-x0=0001-01-01T00%3A00%3A00Z&hp-x1=9999-12-31T23%3A59%3A59Z',  # parses, but no data there
                  'hp-x0=2026-03-03T14%3A16%3A00Z&hp-x1=2026-03-03T14%3A16%3A00Z',  # empty
                  'hp-chart=requests&hp-x0=2026-03-03T14%3A16%3A00Z&hp-x1=2026-03-03T14%3A18%3A30Z',
                  'hp-x0=1.5&hp-x1=2'):
            with self.subTest(q=q):
                status, h, body = self.get('/charts/latency?' + q)
                self.assertEqual((status, body), (204, ''))
                self.assertIsNone(h['HX-Push-Url'])

    def test_back_to_live(self):
        status, h, body = self.get('/charts/latency?zoom=out')
        self.assertEqual(status, 200)
        self.assertEqual(h['HX-Push-Url'], '/')
        self.assertIn('hx-get="/charts/latency"', body)
        self.assertEqual(charts(body)[0].get('data-hp-x'), 'time 1772547300000 1772547600000')

    def test_zoomed_page(self):
        _, _, body = self.get('/?from=2026-03-03T14%3A16%3A00Z&to=2026-03-03T14%3A18%3A30Z')
        self.assertEqual(charts(body)[0].get('data-hp-x'), 'time 1772547360000 1772547510000')
        self.assertIn('Showing 2026-03-03 14:16:00 to 2026-03-03 14:18:30 UTC.', body)

    def test_requests_bars(self):
        _, _, body = self.get('/charts/requests')
        (chart,) = self.assertSafe(body)
        tips = json.loads(chart.find('.//{http://www.w3.org/2000/svg}metadata').text)
        self.assertEqual(tips['mode'], 'band')
        self.assertEqual([r['k'] for r in tips['rows']], ['13:50', '13:55', '14:00', '14:05', '14:10', '14:15'])
        self.assertEqual(body.count('class="hp-bar"'), 18)
        # A link per window: the select gesture without a pointer (§5.5).
        self.assertIn('hx-get="/detail/requests?hp-chart=requests&amp;hp-x=14%3A05"', body)

    def test_select_drills_down(self):
        status, _, body = self.get('/detail/requests?hp-chart=requests&hp-x=14%3A05&hp-series=eu-west&hp-w=500')
        self.assertEqual(status, 200)
        (chart,) = self.assertSafe(body)
        self.assertEqual((chart.get('id'), chart.get('width')), ('detail', '500'))
        names = [el.get('data-hp-series') for el in chart.iter() if el.get('data-hp-series')]
        self.assertEqual(names, ['eu-west'])
        self.assertIn('<hx-partial hx-target="#detail-status" hx-swap="innerHTML">Showing requests per minute '
                      'for eu-west, 14:05 to 14:10 UTC.</hx-partial>', body)
        # Without hp-series, or with one that is not a region: all regions.
        for q in ('hp-x=14%3A05', 'hp-x=14%3A05&hp-series=%3Cb%3E'):
            _, _, body = self.get('/detail/requests?' + q)
            self.assertEqual(body.count('class="hp-series'), 3)

    def test_select_without_a_known_window(self):
        for q, want in (('hp-x=09%3A00', 'That window is no longer shown.'),
                        ('hp-x=%3Cscript%3E', 'That window is no longer shown.'),
                        ('', 'Detail closed.'),
                        ('hp-chart=latency&hp-x=14%3A05', 'Detail closed.')):
            with self.subTest(q=q):
                status, _, body = self.get('/detail/requests?' + q)
                self.assertEqual(status, 200)
                self.assertIn(want, body)
                self.assertNotIn('<script', body)

    def test_detail_without_script(self):
        _, _, body = self.get('/?hp-chart=requests&hp-x=14%3A05')
        self.assertIn('id="detail"', body)
        self.assertEqual([c.get('id') for c in self.assertSafe(body)], ['latency', 'throughput', 'requests', 'detail'])

    def test_stream_card(self):
        _, _, body = self.get('/cards/throughput?hp-w=500')
        self.assertIn('<div class="stream" hx-sse:connect="/stream/throughput?hp-w=500" hx-swap="innerMorph">', body)
        self.assertEqual(charts(body)[0].get('width'), '500')

    def test_static_files(self):
        for name, ctype in (('hx-plot.js', 'text/javascript'), ('hx-plot-brush.js', 'text/javascript'),
                            ('hx-plot.css', 'text/css'), ('app.css', 'text/css')):
            with self.subTest(name=name):
                status, h, _ = self.get('/static/' + name)
                self.assertEqual(status, 200)
                self.assertEqual(h['Content-Type'], ctype + '; charset=utf-8')
        for path in ('/static/../server.py', '/static/page.html', '/static/', '/nope'):
            with self.subTest(path=path):
                self.assertEqual(self.get(path)[0], 404)

    def test_odd_parameters_never_fail(self):
        """Every server case of params.json, on every chart URL (spec §7)."""
        with open(os.path.join(ROOT, 'spec', 'conformance', 'params.json'), encoding='utf-8') as f:
            cases = json.load(f)['server']
        extra = ['hp-w=1e999', 'hp-w=%00', 'from=x&to=y', 'zoom=out&hp-x0=a&hp-x1=b', 'hp-x=%ff%fe', 'theme=dark']
        for q in [c['query'] for c in cases] + extra:
            for url in ('/', '/charts/latency', '/charts/requests', '/detail/requests', '/cards/throughput'):
                # With HX-Request, the server answers a crash with an empty
                # 204 (so htmx keeps the chart), which would hide it here;
                # without the header a crash is a 500.
                for headers in ({}, {'HX-Request': 'true'}):
                    with self.subTest(url=url, q=q, htmx=bool(headers)):
                        status, _, body = self.get(f'{url}?{q}', headers)
                        self.assertLess(status, 500)
                        self.assertSafe(body)


class StreamTest(unittest.TestCase):
    def test_frames(self):
        """Each tick sends one unnamed message whose data: lines are the
        fragment's lines (spec §4.5). The first message also carries
        retry:, since hx-sse ignores a retry: in a message without data."""
        counter = itertools.count()
        s = Server(server.App(now=lambda: NOW + next(counter), tick=0.05, quiet=True))
        try:
            conn = http.client.HTTPConnection('127.0.0.1', s.port, timeout=10)
            conn.request('GET', '/stream/throughput?hp-w=480')
            r = conn.getresponse()
            self.assertEqual(r.status, 200)
            self.assertEqual(r.headers['Content-Type'], 'text/event-stream')
            self.assertEqual(r.headers['Content-Security-Policy'], server.CSP)
            self.assertEqual(r.readline(), b'retry: 2000\n')
            first = r.readline()
            self.assertTrue(first.startswith(b'data: '), first)  # the same message
            # Heartbeats (": ") arrive every tick, so the socket timeout
            # never fires when frames stop coming: bound the loop itself,
            # and fail on EOF rather than spin on it.
            frames, lines = [], [first[6:-1].decode()]
            deadline = time.monotonic() + 10
            while len(frames) < 2:
                self.assertLess(time.monotonic(), deadline, f'only {len(frames)} frames in 10 s')
                line = r.readline()
                self.assertNotEqual(line, b'', 'the stream ended early')
                self.assertNotIn(b'\r', line)
                if line == b'\n':
                    if lines:
                        frames.append('\n'.join(lines))
                    lines = []
                elif line.startswith(b'data: '):
                    lines.append(line[6:-1].decode())
            conn.close()
        finally:
            s.stop()
        domains = []
        for frame in frames:
            (chart,) = charts(frame)
            self.assertEqual((chart.get('id'), chart.get('width')), ('throughput', '480'))
            domains.append(chart.get('data-hp-x'))
            ET.fromstring(TABLE.search(frame).group(0))
        self.assertNotEqual(domains[0], domains[1], 'the window moves with the clock')

    def test_closed_stream_frees_its_slot(self):
        """A page that goes away gives its stream slot back; otherwise the
        throughput chart would get 503s for good once MAX_STREAMS pages had
        come and gone."""
        app = server.App(now=lambda: NOW, tick=0.05, quiet=True)
        app.streams = threading.BoundedSemaphore(1)
        s = Server(app)
        try:
            conn = http.client.HTTPConnection('127.0.0.1', s.port, timeout=10)
            conn.request('GET', '/stream/throughput')
            r = conn.getresponse()
            self.assertEqual(r.status, 200)
            r.readline()
            self.assertEqual(s.get('/stream/throughput')[0], 503, 'the one slot is taken')
            r.close()  # the response holds the socket too
            conn.close()
            # The server notices on its next write (a frame or a heartbeat).
            deadline, status = time.monotonic() + 5, 0
            while status != 200 and time.monotonic() < deadline:
                time.sleep(0.05)
                conn = http.client.HTTPConnection('127.0.0.1', s.port, timeout=10)
                conn.request('GET', '/stream/throughput')
                r = conn.getresponse()
                status = r.status
                r.close()
                conn.close()
            self.assertEqual(status, 200, 'the slot never came back')
        finally:
            s.stop()

    def test_too_many_streams(self):
        """A stream that cannot start answers 503 with HX-Reswap: none."""
        app = server.App(now=lambda: NOW, quiet=True)
        for _ in range(server.MAX_STREAMS):
            app.streams.acquire()
        s = Server(app)
        try:
            status, h, _ = s.get('/stream/throughput')
            self.assertEqual((status, h['HX-Reswap']), (503, 'none'))
        finally:
            s.stop()


if __name__ == '__main__':
    unittest.main()
