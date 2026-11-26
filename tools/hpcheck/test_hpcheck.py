"""Tests of hpcheck.py: it passes every reference fixture, and it is not
vacuous: each mutation of a valid chart, table, fixture output or server
must be reported, with the right section of docs/spec.md.

    python3 -m unittest discover tools/hpcheck

Standard library only. The server tests run a small fake hx-plot server
in a thread (and once through --spawn, in a child process).
"""

from __future__ import annotations

import contextlib
import io
import json
import os
import re
import shutil
import socket
import subprocess
import sys
import tempfile
import threading
import unittest
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer
from unittest import mock
from urllib.parse import parse_qs, urlsplit

import hpcheck

HERE = os.path.dirname(os.path.abspath(__file__))
FIX = hpcheck.FIXTURES


def read(*parts: str) -> bytes:
    with open(os.path.join(FIX, *parts), 'rb') as f:
        return f.read()


CHART = read('line-time', 'chart.svg')
TABLE = read('line-time', 'table.html')


def run(*argv: str) -> tuple[int, str]:
    """hpcheck's exit status and output."""
    out = io.StringIO()
    with contextlib.redirect_stdout(out), contextlib.redirect_stderr(out):
        code = hpcheck.main(list(argv))
    return code, out.getvalue()


class ParseTest(unittest.TestCase):
    def test_selectors(self):
        root = hpcheck.parse_xml(CHART)
        self.assertEqual(len(hpcheck.query(root, 'g.hp-marks > g.hp-series')), 1)
        self.assertEqual(len(hpcheck.query(root, 'svg > title')), 1)
        self.assertEqual(len(hpcheck.query(root, '*[data-hp-series="p50"]')), 1)
        self.assertEqual(hpcheck.query(root, 'g#lt-s-2e119f3a')[0].get('data-hp-series'), 'p50')
        self.assertEqual(hpcheck.query(root, 'g.hp-hover metadata')[0].name, 'metadata')
        for bad in ('', '> g', 'g >', 'g[x=y]', 'g.', '#', 'g[x="y"'):
            with self.subTest(bad=bad), self.assertRaises(ValueError):
                hpcheck.parse_selector(bad)

    def test_html_table(self):
        """An HTML5 table that is not XML still parses."""
        root = hpcheck.parse_table(b'<table id="t"><tr><td>a<br>b&nbsp;</td></tr></table>')
        self.assertEqual(root.get('id'), 't')
        self.assertEqual(hpcheck.query(root, 'td')[0].text(), 'ab\xa0')

    def test_value_forms(self):
        self.assertIsNotNone(hpcheck.time_key_ms('2026-03-03T14:10:00Z'))
        self.assertEqual(hpcheck.time_key_ms('1970-01-01T00:00:00.25Z'), 250)
        for bad in ('2026-03-03T14:10:00.000Z', '2026-03-03T14:10:00+00:00', '2026-02-30T00:00:00Z',
                    '0000-01-01T00:00:00Z', '2026-03-03t14:10:00z', '2026-03-03T14:10:60Z'):
            with self.subTest(bad=bad):
                self.assertIsNone(hpcheck.time_key_ms(bad))
        self.assertEqual([hpcheck.number(v) for v in (24.0, 0.1 + 0.2, 1e21, -1e-7)],
                         ['24', '0.30000000000000004', '1e+21', '-1e-7'])


class SpecTest(unittest.TestCase):
    """The universal checks, on the line-time reference and mutations of it
    (the cases of the Go reference's TestSpecCheckCatchesViolations)."""

    def test_reference_passes(self):
        self.assertEqual(hpcheck.check_spec(CHART, TABLE), [])

    def mutated(self, doc: bytes, old: str, new: str) -> bytes:
        self.assertIn(old.encode(), doc)
        return doc.replace(old.encode(), new.encode(), 1)

    def test_chart_mutations(self):
        for name, old, new, section in [
            ('raw CR', '<title id="lt-title">Latency', '<title id="lt-title">Lat\rency', '§3.1'),
            ('not XML', '</title>', '', '§3.1'),
            ('no data-hp', ' data-hp="1"', '', '§3.2'),
            ('version', 'data-hp="1"', 'data-hp=""', '§3.2'),
            ('viewBox', 'viewBox="0 0 240 120"', 'viewBox="0 0 -240 120"', '§3.2'),
            ('duplicate id', 'id="lt-s-2e119f3a"', 'id="lt-title"', '§4.1'),
            ('foreign id', 'id="lt-s-2e119f3a"', 'id="other"', '§4.1'),
            ('style', '<g class="hp-hover"', '<g style="x" class="hp-hover"', '§3.6'),
            ('on* attribute', '<g class="hp-hover"', '<g onclick="x()" class="hp-hover"', '§3.6'),
            ('javascript: link', '<g class="hp-hover"',
             '<a href="javascript:x()"><text>x</text></a><g class="hp-hover"', '§3.6'),
            ('external image', '<g class="hp-hover"', '<image href="https://x.example/i.png"/><g class="hp-hover"',
             '§3.6'),
            ('external use', '<g class="hp-hover"', '<use xlink:href="https://x.example/s.svg#a"/><g class="hp-hover"',
             '§3.6'),
            ('external paint', 'class="hp-hover"', 'fill="url(https://x.example/f)" class="hp-hover"', '§3.6'),
            ('url() to no id', 'clip-path="url(#lt-clip)"', 'clip-path="url(#lt-nope)"', '§3.6'),
            ('quoted external paint', 'class="hp-hover"', 'fill="url(\'https://x.example/f\')" class="hp-hover"',
             '§3.6'),
            ('relative src', '<g class="hp-hover"', '<g src="i.png"/><g class="hp-hover"', '§3.6'),
            ('unclosed url()', 'clip-path="url(#lt-clip)"', 'clip-path="url(#lt-clip"', '§3.6'),
            ('style element @import', '<g class="hp-hover"',
             '<style>@import url(https://x.example/a.css);</style><g class="hp-hover"', '§3.6'),
            ('style element external url()', '<g class="hp-hover"',
             '<style>.hp{background:url(https://x.example/b.png)}</style><g class="hp-hover"', '§3.6'),
            ('SMIL set of href', '<g class="hp-hover"',
             '<a href="#lt"><set attributeName="href" to="javascript:x()"/><text>x</text></a><g class="hp-hover"',
             '§3.6'),
            ('SMIL animate of href', '<g class="hp-hover"',
             '<a href="#lt"><animate attributeName="href" values="javascript:x()"/><text>x</text></a>'
             '<g class="hp-hover"', '§3.6'),
            ('SMIL animateTransform', '<g class="hp-hover"',
             '<animateTransform attributeName="transform"/><g class="hp-hover"', '§3.6'),
            ('external cursor', 'class="hp-hover"', 'cursor="url(https://x.example/c.cur), auto" class="hp-hover"',
             '§3.6'),
            ('external url() in any attribute', 'class="hp-hover"',
             'x-paint="url(https://x.example/f)" class="hp-hover"',
             '§3.6'),
            ('lowercased SVG element', '<g class="hp-hover"', '<lineargradient id="lt-g"/><g class="hp-hover"',
             '§3.1'),
            ('lowercased SVG attribute', 'viewBox="0 0 240 120"', 'viewbox="0 0 240 120"', '§3.1'),
            ('uppercase attribute', 'class="hp-hover"', 'CLASS="hp-hover"', '§3.1'),
            ('second url() external', 'clip-path="url(#lt-clip)"',
             'clip-path="url(#lt-clip) url(https://x.example/c)"', '§3.6'),
            ('transform on a tick', '<text class="hp-tick"', '<text transform="rotate(-45)" class="hp-tick"', '§3.3'),
            ('transform on a series', 'class="hp-series hp-s1"', 'transform="scale(2)" class="hp-series hp-s1"',
             '§3.3'),
            ('two slots', 'class="hp-series hp-s1"', 'class="hp-series hp-s1 hp-s2"', '§3.3'),
            ('focus ring', '<metadata', '<circle class="hp-focus"/><metadata', '§3.3'),
            ('number form', 'data-hp-plot="24 8 228 98"', 'data-hp-plot="24.0 8 228 98"', '§1.1'),
            ('rounded domain', 'data-hp-y="linear 40 46"', 'data-hp-y="linear 40.0 46"', '§1.1'),
            ('reversed domain', 'data-hp-y="linear 40 46"', 'data-hp-y="linear 46 40"', '§4.2'),
            ('missing key', '"k":"2026-03-03T14:10:00Z",', '', '§4.3'),
            ('key outside domain', '"k":"2026-03-03T14:10:00Z"', '"k":"2026-03-03T15:10:00Z"', '§3.5'),
            ('non-canonical key', '"k":"2026-03-03T14:10:00Z"', '"k":"2026-03-03T14:10:00.000Z"', '§1.1'),
            ('raw < in metadata', '"k":"2026-03-03T14:10:00Z"', '"k":"2026-03-03T14:10:00Z","x":"\\u003c"', None),
            ('bad JSON', '"k":"2026-03-03T14:10:00Z"', '"k":2026', '§3.5'),
            ('NaN in JSON', '"bw":0', '"bw":NaN', '§3.5'),
        ]:
            with self.subTest(name=name):
                bad = self.mutated(CHART, old, new)
                v = hpcheck.check_spec(bad, TABLE)
                if section is None:
                    self.assertEqual(v, [], 'a \\u003c escape is fine')
                    continue
                self.assertTrue(any(m.startswith(section) for m in v), f'{name}: {v}')

    def test_internal_references(self):
        """§3.6: references to ids inside the chart are fine (the cases of
        the Go reference's TestSpecCheckExternalReferences)."""
        for name, old, new in [
            ('use of an id', '<g class="hp-hover"', '<use href="#lt-clip"/><g class="hp-hover"'),
            ('quoted url() to an id', 'clip-path="url(#lt-clip)"', 'clip-path="url(&quot;#lt-clip&quot;)"'),
            ('spaced URL() to an id', 'clip-path="url(#lt-clip)"', 'clip-path="URL( #lt-clip )"'),
            ('plain paint', '<g class="hp-hover"', '<g fill="currentColor" class="hp-hover"'),
            ('style element with url() to an id', '<g class="hp-hover"',
             '<style>.hp-line{stroke:url(#lt-clip)}</style><g class="hp-hover"'),
            ('a series named url()', 'data-hp-series="p50"', 'data-hp-series="url(https://x.example/)"'),
            ('a label naming url()', '<g class="hp-hover"', '<g aria-label="url(x)" class="hp-hover"'),
        ]:
            with self.subTest(name=name):
                v = hpcheck.check_spec(self.mutated(CHART, old, new), TABLE)
                self.assertEqual([m for m in v if m.startswith('§3.6')], [], name)

    def test_names_as_the_html_parser_reads_them(self):
        """§3.6 compares names the way the HTML parser does: inlined in a
        page, <SCRIPT> is a live script, STYLE= a style attribute and HREF=
        a link, whatever XML makes of them. Each such name is also not the
        canonical spelling, which the two parsers read differently (§3.1)."""
        for name, old, new in [
            ('SCRIPT', '<g class="hp-hover"', '<SCRIPT>x()</SCRIPT><g class="hp-hover"'),
            ('HREF', '<g class="hp-hover"', '<a HREF="javascript:x()"><text>x</text></a><g class="hp-hover"'),
            ('xlink:HREF', '<g class="hp-hover"', '<use xlink:HREF="https://x.example/s.svg#a"/><g class="hp-hover"'),
            ('STYLE', '<g class="hp-hover"', '<g STYLE="fill:red" class="hp-hover"'),
            ('FOREIGNOBJECT', '<g class="hp-hover"', '<FOREIGNOBJECT></FOREIGNOBJECT><g class="hp-hover"'),
            ('foreignobject', '<g class="hp-hover"', '<foreignobject></foreignobject><g class="hp-hover"'),
            ('ONCLICK', '<g class="hp-hover"', '<g ONCLICK="x()" class="hp-hover"'),
            ('SET', '<g class="hp-hover"', '<a href="#lt"><SET attributeName="href" to="javascript:x()"/></a>'
                                          '<g class="hp-hover"'),
            ('FILL url()', 'class="hp-hover"', 'FILL="url(https://x.example/f)" class="hp-hover"'),
        ]:
            with self.subTest(name=name):
                v = hpcheck.check_spec(self.mutated(CHART, old, new), TABLE)
                self.assertTrue(any(m.startswith('§3.6') for m in v), f'{name}: {v}')
                self.assertTrue(any(m.startswith('§3.1') for m in v), f'{name}: {v}')
        # The canonical camelCase spellings are what the HTML parser keeps.
        ok = self.mutated(CHART, '<g class="hp-hover"',
                          '<linearGradient id="lt-g" gradientUnits="userSpaceOnUse"/><g class="hp-hover"')
        self.assertEqual(hpcheck.check_spec(ok, TABLE), [])

    def test_raw_metadata_text(self):
        """§3.5: the metadata's raw text needs no XML escaping."""
        bad = self.mutated(CHART, '"k":"2026-03-03T14:10:00Z"', '"k":"2026-03-03T14:10:00Z","x":"&amp;"')
        self.assertIn("§3.5: the raw metadata text holds '&'", hpcheck.check_spec(bad, TABLE))

    def test_table_mutations(self):
        for name, old, new, section in [
            ('table id', 'id="lt-table"', 'id="x-table"', '§3.8'),
            ('row id', 'id="lt-r-f9457bc2"', 'id="lt-x-f9457bc2"', '§4.1'),
            ('duplicate row id', 'id="lt-r-f94eeddf"', 'id="lt-r-f9457bc2"', '§4.1'),
            ('row count', '</tbody>', '<tr id="lt-r-0"><th scope="row">x</th></tr></tbody>', '§3.8'),
            ('value differs', '<td>42 ms</td>', '<td>999</td>', '§3.8'),
            ('heading differs', '<th scope="row">Mar 3, 2026 2:00 PM</th>', '<th scope="row">bogus</th>', '§3.8'),
            ('extra cell', '<td>42 ms</td>', '<td>42 ms</td><td>42 ms</td>', '§3.8'),
            ('bare dash', '<td>42 ms</td>', '<td>' + hpcheck.MISSING + '</td>', '§3.8'),
            ('no caption', '<caption>Latency</caption>', '', '§3.8'),
            ('raw CR', '<caption>Latency', '<caption>Lat\rency', '§3.6'),
            ('style', '<caption>', '<caption style="x">', '§3.6'),
            ('STYLE', '<caption>', '<caption STYLE="x">', '§3.6'),
            ('SCRIPT', '<caption>', '<caption><SCRIPT>x()</SCRIPT>', '§3.6'),
        ]:
            with self.subTest(name=name):
                v = hpcheck.check_spec(CHART, self.mutated(TABLE, old, new))
                self.assertTrue(any(m.startswith(section) for m in v), f'{name}: {v}')

    def test_swapped_rows(self):
        trs = re.findall(rb'<tr id="lt-r-[^"]*">.*?</tr>', TABLE)
        swapped = TABLE.replace(trs[0] + trs[1], trs[1] + trs[0])
        self.assertNotEqual(swapped, TABLE)
        self.assertNotEqual(hpcheck.check_spec(CHART, swapped), [])

    def test_every_reference_chart_passes(self):
        for name in sorted(os.listdir(FIX)):
            for f in sorted(os.listdir(os.path.join(FIX, name))) if os.path.isdir(os.path.join(FIX, name)) else []:
                if f.endswith('.svg'):
                    with self.subTest(fixture=name, file=f):
                        table = read(name, 'table.html') if f == 'chart.svg' and os.path.exists(
                            os.path.join(FIX, name, 'table.html')) else None
                        self.assertEqual(hpcheck.check_spec(read(name, f), table), [])


class FixturesTest(unittest.TestCase):
    def setUp(self):
        self.tmp = tempfile.mkdtemp(prefix='hpcheck-')
        self.addCleanup(shutil.rmtree, self.tmp)
        for name in ('line-time', 'empty', 'slide'):
            os.mkdir(os.path.join(self.tmp, name))
            for f in os.listdir(os.path.join(FIX, name)):
                if f.endswith(('.svg', '.html')):
                    shutil.copy(os.path.join(FIX, name, f), os.path.join(self.tmp, name, f))

    def edit(self, rel: str, old: bytes, new: bytes):
        path = os.path.join(self.tmp, rel)
        with open(path, 'rb') as f:
            b = f.read()
        self.assertIn(old, b)
        with open(path, 'wb') as f:
            f.write(b.replace(old, new, 1))

    def test_references_pass(self):
        code, out = run('fixtures')
        self.assertEqual(code, 0, out)
        self.assertIn('PASS line-time (reference)', out)

    def test_output_passes_and_missing_ones_skip(self):
        code, out = run('fixtures', '--out', self.tmp, '--require', 'line-time,empty')
        self.assertEqual(code, 0, out)
        self.assertIn('SKIP bars-band', out)

    def test_required_but_missing(self):
        code, out = run('fixtures', '--out', self.tmp, '--require', 'bars-band')
        self.assertEqual(code, 1, out)
        self.assertIn('FAIL §2: required fixture bars-band was not rendered', out)

    def test_missing_document(self):
        os.remove(os.path.join(self.tmp, 'line-time', 'table.html'))
        self.assertEqual(run('fixtures', '--out', self.tmp)[0], 1)

    def test_universal_failure(self):
        self.edit('line-time/chart.svg', b' data-hp="1"', b'')
        code, out = run('fixtures', '--out', self.tmp)
        self.assertEqual(code, 1)
        self.assertRegex(out, r'FAIL §3\.2: root data-hp = .*, line-time/chart\.svg')

    def test_fixture_assertion(self):
        """checks.json server assertions: the series name is the fixture's."""
        self.edit('line-time/chart.svg', b'data-hp-series="p50"', b'data-hp-series="p95"')
        code, out = run('fixtures', '--out', self.tmp)
        self.assertEqual(code, 1)
        self.assertIn('checks.json server[3]', out)

    def test_should_warns_unless_strict(self):
        """A line drawn as something other than path.hp-line is a should."""
        self.edit('line-time/chart.svg', b'<path class="hp-line"', b'<path class="hp-curve"')
        code, out = run('fixtures', '--out', self.tmp)
        self.assertEqual(code, 0, out)
        self.assertIn('WARN checks.json server[5]', out)
        self.assertEqual(run('fixtures', '--out', self.tmp, '--strict')[0], 1)

    def test_stable_across_frames(self):
        self.edit('slide/frame-b.svg', b'id="sl-s-9f07c3c1"', b'id="sl-s-00000000"')
        code, out = run('fixtures', '--out', self.tmp)
        self.assertEqual(code, 1, out)

    def test_usage(self):
        self.assertEqual(run('fixtures', '--require', 'nope')[0], 2)
        self.assertEqual(run('fixtures', '--out', os.path.join(self.tmp, 'nope'))[0], 2)
        self.assertEqual(run()[0], 2)
        self.assertEqual(run('server', 'http://127.0.0.1:1')[0], 2)  # no --config


class FileTest(unittest.TestCase):
    def setUp(self):
        self.tmp = tempfile.mkdtemp(prefix='hpcheck-')
        self.addCleanup(shutil.rmtree, self.tmp)

    def write(self, name: str, b: bytes) -> str:
        path = os.path.join(self.tmp, name)
        with open(path, 'wb') as f:
            f.write(b)
        return path

    def test_page(self):
        page = b'<!doctype html><p><svg class="icon"><path/></svg></p><div>' + CHART + TABLE + b'</div>'
        code, out = run('file', self.write('page.html', page))
        self.assertEqual(code, 0, out)
        bad = page.replace(b'<td>42 ms</td>', b'<td>1</td>')
        code, out = run('file', self.write('bad.html', bad))
        self.assertEqual(code, 1, out)
        self.assertIn('FAIL §3.8: row lt-r-f9457bc2 holds', out)

    def test_svg_and_table(self):
        svg, table = self.write('c.svg', CHART), self.write('t.html', TABLE)
        self.assertEqual(run('file', svg, '--table', table)[0], 0)
        self.assertEqual(run('file', self.write('x.svg', CHART.replace(b'data-hp="1"', b'data-hp="2"')))[0], 1)

    def test_page_ids(self):
        """§4.1 across a page: two charts pass; the same chart twice, or two
        charts that share an id, fail."""
        other = CHART.replace(b'id="lt"', b'id="lu"').replace(b'lt-', b'lu-')
        other_table = TABLE.replace(b'lt-', b'lu-')
        two = b'<!doctype html><body>' + CHART + TABLE + other + other_table
        code, out = run('file', self.write('two.html', two))
        self.assertEqual(code, 0, out)
        code, out = run('file', self.write('dup.html', b'<!doctype html><body>' + (CHART + TABLE) * 2))
        self.assertEqual(code, 1, out)
        self.assertIn("FAIL §4.1: chart id 'lt' appears 2 times on the page", out)
        self.assertIn('FAIL §4.1: the table lt-table appears 2 times on the page', out)
        shared = two.replace(b'id="lu-r-', b'id="lt-r-', 1)
        code, out = run('file', self.write('shared.html', shared))
        self.assertEqual(code, 1, out)
        self.assertIn('FAIL §4.1: ids shared by two charts or tables', out)

    def test_svg_hp_that_is_not_a_chart(self):
        """§6.4: the client treats every svg.hp as a chart, so a page swatch
        or icon with the class hp fails even without data-hp, whatever the
        attribute quoting. Other hp-* classes (hp-spark) are left alone."""
        ok = b'<!doctype html><body>' + CHART + TABLE
        for name, swatch in [
            ('quoted', b'<svg class="hp swatch" viewBox="0 0 10 10"><rect class="hp-s1"/></svg>'),
            ('single-quoted', b"<svg viewBox='0 0 10 10' class='swatch hp'><rect/></svg>"),
            ('bare', b'<svg class=hp viewBox="0 0 10 10"><rect/></svg>'),
            ('no space before class', b'<svg viewBox="0 0 10 10"class="hp"><rect/></svg>'),
        ]:
            with self.subTest(name=name):
                code, out = run('file', self.write('swatch.html', ok + swatch))
                self.assertEqual(code, 1, out)
                self.assertIn('FAIL §6.4: <svg id=', out)
                # Then the chart checks; the last swatch is not even XML.
                self.assertTrue('FAIL §3.2: root data-hp' in out or 'FAIL §3.1: not well-formed' in out, out)
        for name, other in [
            ('sparkline', b'<svg class="hp-spark" viewBox="0 0 10 10"><path/></svg>'),
            ('swatch', b'<svg class="hp-spark swatch"viewBox="0 0 10 10"><rect class="hp-s1"/></svg>'),
            ('hp in another attribute', b'<svg class="icon" aria-label="hp"><path/></svg>'),
        ]:
            with self.subTest(name=name):
                code, out = run('file', self.write('other.html', ok + other))
                self.assertEqual(code, 0, out)

    def test_self_closing_svg(self):
        """A self-closing <svg .../> is a whole element in HTML, so an icon
        written that way does not hide the charts after it."""
        broken = CHART.replace(b'<g class="hp-hover"', b'<g style="x" class="hp-hover"', 1)
        for name, icon in [
            ('self-closing', b'<svg class="icon" viewBox="0 0 1 1" aria-hidden="true"/>'),
            ('self-closing, no attributes', b'<svg/>'),
            ('quoted > and />', b'<svg class="icon" aria-label="a > b />"/>'),
            ('unquoted value then />', b'<svg class="icon" viewBox=0/><path/></svg>'),
            ('closed', b'<svg class="icon" aria-label="a > b"><path/></svg>'),
        ]:
            with self.subTest(name=name):
                page = b'<!doctype html><p>' + icon + b'</p>' + broken + TABLE
                self.assertEqual([cid for cid, _ in hpcheck.find_charts(page)], ['lt'])
                code, out = run('file', self.write('page.html', page))
                self.assertEqual(code, 1, out)
                self.assertIn('FAIL §3.6: svg#lt > g.hp-hover carries the attribute style', out)
        # A self-closing svg that claims to be a chart is reported, not skipped.
        code, out = run('file', self.write('empty.html', b'<p><svg id="e" class="hp" data-hp="1"/></p>' + CHART))
        self.assertEqual(code, 1, out)
        self.assertIn(', ' + os.path.join(self.tmp, 'empty.html') + ' #e', out)

    def test_svg_text_that_is_not_a_tag(self):
        """The text '<svg' in a comment, in another tag's attribute value,
        in raw text or in a template does not hide the charts after it: a
        second chart that holds <script> still fails §3.6."""
        bad = (CHART.replace(b'id="lt"', b'id="lu"').replace(b'lt-', b'lu-')
               .replace(b'<metadata', b'<script>alert(1)</script><metadata', 1))
        bad_table = TABLE.replace(b'lt-', b'lu-')
        for name, stray in [
            ('comment', b'<!-- old: <svg class="hp-icon"> -->'),
            ('unclosed comment marker in a comment', b'<!-- a <!-- <svg> --!>'),
            ('empty comments', b'<!--><!---><svg class="icon"><path/></svg>'),
            ('attribute value', b'<button data-icon="<svg>">x</button>'),
            ('single-quoted attribute value', b"<i data-x='<svg class=\"hp\">'></i>"),
            ('script', b'<script type="text/template"><svg class="hp-icon"></script>'),
            ('textarea', b'<textarea><svg></textarea>'),
            ('title, upper case', b'<TITLE>a <svg> b</TITLE>'),
            ('bogus comment', b'<![CDATA[ <svg> ]]><? <svg> ?>'),
            ('template', b'<template><svg class="hp-icon"></template>'),
        ]:
            with self.subTest(name=name):
                page = b'<!doctype html><body>' + CHART + TABLE + stray + bad + bad_table
                self.assertEqual([cid for cid, _ in hpcheck.find_charts(page)], ['lt', 'lu'])
                code, out = run('file', self.write('page.html', page))
                self.assertEqual(code, 1, out)
                self.assertIn('FAIL §3.6: chart holds <script>', out)
                # With the stray text before the only chart, the failure is
                # still §3.6, not a missing chart.
                code, out = run('file', self.write('one.html', stray + bad + bad_table))
                self.assertEqual(code, 1, out)
                self.assertIn('FAIL §3.6: chart holds <script>', out)
                self.assertNotIn('§3.1: no chart', out)
        # Inside SVG a <title> or <style> is not raw text, and a CDATA
        # section runs to ']]>', so the charts around them are still found.
        inner = CHART.replace(b'<g class="hp-hover"', b'<style><![CDATA[ </svg> <svg> ]]></style><g class="hp-hover"', 1)
        page = b'<!doctype html><body>' + inner + TABLE + bad + bad_table
        self.assertEqual([cid for cid, _ in hpcheck.find_charts(page)], ['lt', 'lu'])

    def test_fragment_that_starts_with_a_chart(self):
        """A poll response is the chart, then its table: a page fragment,
        whatever its first bytes. A file that is one <svg> is one chart."""
        frag = self.write('frag.html', CHART + b'\n<details><summary>Table</summary>' + TABLE + b'</details>\n')
        code, out = run('file', frag)
        self.assertEqual(code, 0, out)
        bad = self.write('bad.html', CHART + TABLE.replace(b'<td>42 ms</td>', b'<td>1</td>'))
        code, out = run('file', bad)
        self.assertEqual(code, 1, out)
        self.assertIn('FAIL §3.8: row lt-r-f9457bc2 holds', out)
        self.assertEqual(run('file', self.write('chart.html', b'\n' + CHART + b'\n'), '--table',
                             self.write('t.html', TABLE))[0], 0)
        # A .svg file is one chart, so anything after it is not XML.
        code, out = run('file', self.write('frag.svg', CHART + TABLE))
        self.assertEqual(code, 1, out)
        self.assertIn('FAIL §3.1: not well-formed XML', out)

    def test_style_element_in_a_page_warns(self):
        """A standalone file may carry its theme in <style>; inlined in a
        page, a strict CSP refuses it (§7), which is a warning."""
        styled = CHART.replace(b'<g class="hp-hover"', b'<style>.hp{color:red}</style><g class="hp-hover"', 1)
        self.assertEqual(run('file', self.write('c.svg', styled)), (0, f'PASS {self.tmp}{os.sep}c.svg\n'
                                                                       'hpcheck: 0 failure(s), 0 warning(s)\n'))
        code, out = run('file', self.write('p.html', b'<!doctype html><body>' + styled + TABLE))
        self.assertEqual(code, 0, out)
        self.assertIn('WARN §7: chart lt holds a <style> element', out)
        self.assertEqual(run('file', '--strict', self.write('p.html', b'<body>' + styled))[0], 1)

    def test_no_chart(self):
        self.assertEqual(run('file', self.write('p.html', b'<p>hello</p>'))[0], 1)
        self.assertEqual(run('file', os.path.join(self.tmp, 'missing.svg'))[0], 2)


# ---- A fake server ---------------------------------------------------------------------


class Fake(BaseHTTPRequestHandler):
    """Serves the line-time reference as a live chart. Faults (a set on the
    server) break one behaviour each."""

    protocol_version = 'HTTP/1.0'

    def log_message(self, *args):
        pass

    def chart(self, width: int) -> bytes:
        return CHART.replace(b'viewBox="0 0 240 120"', b'viewBox="0 0 %d 120"' % width) + TABLE

    def send(self, status: int, body: bytes = b'', headers: dict | None = None):
        self.send_response(status)
        self.send_header('Content-Type', 'text/html; charset=utf-8')
        for k, v in (headers or {}).items():
            self.send_header(k, v)
        self.send_header('Content-Length', str(len(body)))
        self.end_headers()
        self.wfile.write(body)

    def do_GET(self):
        faults = self.server.faults
        u = urlsplit(self.path)
        q = {k: v[0] for k, v in parse_qs(u.query, keep_blank_values=True).items()}
        if 'crash' in faults and q.get('hp-x0') == '1_0':
            return self.send(500)
        if u.path == '/':
            return self.send(200, b'<!doctype html><main>' + self.chart(240) + b'</main>')
        if u.path == '/stream':
            self.send_response(200)
            self.send_header('Content-Type', 'text/event-stream')
            self.end_headers()
            frame = self.chart(240).decode()
            if 'cr' in faults:
                frame = frame.replace('Latency', 'Lat\rency', 1)
            eol = '\r\n' if 'crlf' in faults else '\n'  # CRLF framing is fine
            retry = '' if 'no-retry' in faults else 'retry: 2000' + eol
            for i in range(2):
                lines = ''.join(f'data: {line}{eol}' for line in frame.split('\n'))
                self.wfile.write(f'{retry if i == 0 else ""}id: {i}{eol}{lines}{eol}'.encode())
            return
        if u.path != '/chart':
            return self.send(404)
        try:
            w = float(q.get('hp-w', ''))
            width = 240 if not (w > 0 and w < float('inf')) else int(min(max(w, 50), 4096))
        except ValueError:
            width = 240
        if 'fixed-width' in faults:
            width = 240
        body = self.chart(width)
        if 'nan-width' in faults and not re.fullmatch(r'[0-9.]+', q.get('hp-w', '0')):
            body = body.replace(b'viewBox="0 0 240 ', b'viewBox="0 0 NaN ')
        # A brush zooms only when both ends are times in the key form (this
        # fake reads no other form); anything else is a poll.
        ends = [hpcheck.time_key_ms(q.get(k, '')) for k in ('hp-x0', 'hp-x1')]
        if None not in ends:
            if 'brush-ignored' in faults:
                return self.send(204)
            return self.send(200, body, {'HX-Push-Url': '/?zoomed'})
        tag = '"t%d"' % width
        sent = self.headers.get('HX-PTag', '').strip()
        sent = sent[2:] if sent.startswith('W/') else sent
        if 'no-304' not in faults and sent.strip('"') == tag.strip('"'):
            return self.send(304, b'', {'HX-PTag': tag})
        self.send(200, body, {'HX-PTag': tag})


CONFIG = {
    'level': 3, 'pages': ['/'],
    'charts': [{'url': '/chart', 'ptag': True, 'autosize': True, 'table': True,
                'brush': '/chart', 'select': '/chart'}],
    'sse': [{'url': '/stream', 'frames': 2, 'timeout': 5}],
}


class ServerTest(unittest.TestCase):
    def setUp(self):
        self.tmp = tempfile.mkdtemp(prefix='hpcheck-')
        self.addCleanup(shutil.rmtree, self.tmp)
        self.config = os.path.join(self.tmp, 'hpcheck.json')
        with open(self.config, 'w', encoding='utf-8', newline='\n') as f:

            json.dump(CONFIG, f)

    def check(self, *faults: str, strict: bool = False) -> tuple[int, str]:
        srv = ThreadingHTTPServer(('127.0.0.1', 0), Fake)
        srv.daemon_threads = True
        srv.faults = set(faults)
        t = threading.Thread(target=srv.serve_forever, daemon=True)
        t.start()
        try:
            args = ['server', f'http://127.0.0.1:{srv.server_address[1]}', '--config', self.config]
            return run(*args, *(['--strict'] if strict else []))
        finally:
            srv.shutdown()
            srv.server_close()

    def test_good_server(self):
        code, out = self.check()
        self.assertEqual(code, 0, out)
        self.assertNotIn('WARN', out)

    def test_faults(self):
        for fault, code, want in [
            ('no-304', 1, 'FAIL §4.5: HX-PTag'),
            ('crash', 1, 'FAIL §5.3: 500 for a brush'),
            ('cr', 1, 'FAIL §3.6: a raw CR inside a line'),
            ('no-retry', 0, 'WARN §4.5: no retry:'),
            ('fixed-width', 0, 'WARN §4.6: hp-w asks for width'),
            ('brush-ignored', 0, 'WARN §5.3: a brush across the chart was not answered as a zoom (status 204)'),
            ('nan-width', 1, "FAIL §3.2: viewBox size 'NaN' is not a positive number"),
        ]:
            with self.subTest(fault=fault):
                got, out = self.check(fault)
                self.assertEqual(got, code, out)
                self.assertIn(want, out)
        self.assertEqual(self.check('no-retry', strict=True)[0], 1)
        self.assertEqual(self.check('brush-ignored', strict=True)[0], 1)

    def test_crlf_stream(self):
        """An event stream may end its lines with CRLF; only a CR inside a
        fragment's line is a fault."""
        code, out = self.check('crlf')
        self.assertEqual(code, 0, out)
        code, out = self.check('crlf', 'cr')
        self.assertEqual(code, 1, out)
        self.assertIn('FAIL §3.6: a raw CR inside a line', out)

    def test_level_1_skips_live_checks(self):
        self.assertEqual(self.check('no-304', 'cr')[0], 1)
        with open(self.config, 'w', encoding='utf-8', newline='\n') as f:
            json.dump({**CONFIG, 'level': 1}, f)
        code, out = self.check('no-304', 'cr')
        self.assertEqual(code, 0, out)

    def test_spawn(self):
        """--spawn starts the server on a port, checks it and stops it."""
        cmd = f'"{sys.executable}" "{os.path.abspath(__file__)}" --serve {{port}}'
        code, out = run('server', 'http://127.0.0.1:8080', '--config', self.config, '--spawn', cmd)
        self.assertEqual(code, 0, out)
        port = int(re.search(r'checking http://127\.0\.0\.1:(\d+)', out).group(1))

        with self.assertRaises(OSError):
            socket.create_connection(('127.0.0.1', port), timeout=1).close()

    def test_spawn_failure(self):
        cmd = f'"{sys.executable}" -c "raise SystemExit(3)"'
        code, out = run('server', 'http://127.0.0.1:8080', '--config', self.config, '--spawn', cmd)
        self.assertEqual(code, 2, out)
        self.assertIn('exited with 3', out)

    def test_spawn_port_taken(self):
        """--port names a port another server holds: a usage error, never
        a check of that other server."""
        with socket.socket() as busy:
            busy.bind(('127.0.0.1', 0))
            busy.listen()
            port = busy.getsockname()[1]
            cmd = f'"{sys.executable}" "{os.path.abspath(__file__)}" --serve {{port}}'
            code, out = run('server', 'http://127.0.0.1:8080', '--config', self.config, '--spawn', cmd,
                            '--port', str(port))
        self.assertEqual(code, 2, out)
        self.assertIn('already listens', out)
        self.assertNotIn('checking', out)

    def test_spawned_server_dies(self):
        """The port is taken after the check that it is free (a race), so
        the spawned server cannot bind and exits, while another server
        answers. hpcheck must not pass: it reports the exit."""
        other = ThreadingHTTPServer(('127.0.0.1', 0), Fake)
        other.daemon_threads = True
        other.faults = set()
        port = other.server_address[1]
        threading.Thread(target=other.serve_forever, daemon=True).start()
        # A server that fails to bind: it exits a moment after it starts.
        cmd = f'"{sys.executable}" -c "import time; time.sleep(0.3); raise SystemExit(1)"'
        real = hpcheck._listening
        first = [True]

        def listening(host, p):  # the free-port check ran before the other server took the port
            if first[0]:
                first[0] = False
                return False
            return real(host, p)

        try:
            with mock.patch.object(hpcheck, '_listening', listening):
                code, out = run('server', 'http://127.0.0.1:8080', '--config', self.config, '--spawn', cmd,
                                '--port', str(port))
        finally:
            other.shutdown()
            other.server_close()
        self.assertNotEqual(code, 0, out)
        self.assertIn('exited with 1', out)

    def test_spawn_interrupted(self):
        """Ctrl-C while the server starts up stops the server too."""
        children = []
        real_popen = subprocess.Popen

        def popen(*a, **kw):
            p = real_popen(*a, **kw)
            children.append(p)
            return p

        calls = [0]

        def sleep(_s):
            calls[0] += 1
            if calls[0] >= 3:
                raise KeyboardInterrupt

        cmd = f'"{sys.executable}" -c "import time; time.sleep(60)"'
        with mock.patch.object(hpcheck.subprocess, 'Popen', popen), \
                mock.patch.object(hpcheck.time, 'sleep', sleep), self.assertRaises(KeyboardInterrupt):
            hpcheck.spawn(cmd, '127.0.0.1', hpcheck._free_port())
        p = children[0]  # the server; on Windows taskkill follows
        self.assertIsNotNone(p.poll(), 'the server is still running')


if __name__ == '__main__':
    if sys.argv[1:2] == ['--serve']:  # the --spawn test's child: the fake server on a port
        srv = ThreadingHTTPServer(('127.0.0.1', int(sys.argv[2])), Fake)
        srv.daemon_threads = True
        srv.faults = set()
        srv.serve_forever()
    else:
        unittest.main()
