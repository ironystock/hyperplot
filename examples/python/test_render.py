"""Tests of render.py against the conformance fixtures in
spec/conformance/. This is not the checker (spec/hpcheck runs every
assertion of every checks.json); it checks the spec rules this renderer
could get wrong, reading the expected values from each fixture's
checks.json where it can.
"""

from __future__ import annotations

import json
import os
import re
import unittest
import xml.etree.ElementTree as ET

from hp_wire import parse_time_value
from render import Chart, Mark, keys, render
from render_fixtures import FIXTURES, chart_from_input

SVG = '{http://www.w3.org/2000/svg}'
NUMBER = r'-?(0|[1-9][0-9]*)(\.[0-9]*[1-9])?(e[+-][1-9][0-9]*)?'  # spec §1.1
SUPPORTED = ['line-time', 'bars-band', 'empty', 'escaping', 'collisions']


def fixture(name: str) -> tuple[dict, dict]:
    with open(os.path.join(FIXTURES, name, 'input.json'), encoding='utf-8') as f:
        doc = json.load(f)
    with open(os.path.join(FIXTURES, name, 'checks.json'), encoding='utf-8') as f:
        checks = json.load(f)
    return doc, checks


def local(el: ET.Element) -> str:
    return el.tag.rsplit('}', 1)[-1]


def tips(root: ET.Element) -> dict:
    """The parsed hp-tips payload. The XML parser decodes the text first,
    so this also proves the JSON needs no XML escaping (spec §3.5)."""
    (meta,) = [el for el in root.iter() if el.get('class') == 'hp-tips']
    return json.loads(meta.text)


class Fixtures(unittest.TestCase):
    def render(self, name: str) -> tuple[str, str, ET.Element, ET.Element]:
        doc, _ = fixture(name)
        svg, table = render(chart_from_input(doc['chart']))
        return svg, table, ET.fromstring(svg), ET.fromstring(table)

    def test_documents(self):
        """The root contract (spec §3.2, §3.3), safe markup (§3.6) and the
        number form of the geometry attributes (§1.1, §4.2)."""
        for name in SUPPORTED:
            with self.subTest(fixture=name):
                svg, table, root, _ = self.render(name)
                self.assertNotIn('\r', svg + table)
                self.assertEqual(root.tag, SVG + 'svg')
                cid = root.get('id')
                for attr, want in {'role': 'img', 'tabindex': '0', 'data-hp': '1',
                                   'aria-labelledby': cid + '-title', 'aria-describedby': cid + '-desc'}.items():
                    self.assertEqual(root.get(attr), want, attr)
                self.assertIn('hp', root.get('class').split())
                first, second = list(root)[:2]
                self.assertEqual((local(first), first.get('id')), ('title', cid + '-title'))
                self.assertEqual((local(second), second.get('id')), ('desc', cid + '-desc'))
                self.assertRegex(root.get('data-hp-plot'), f'^{NUMBER}( {NUMBER}){{3}}$')
                self.assertRegex(root.get('data-hp-y'), f'^linear {NUMBER} {NUMBER}$')
                for el in root.iter():
                    self.assertNotIn(local(el), ('script', 'foreignObject', 'svg', 'symbol', 'use')
                                     if el is not root else ())
                    for attr in el.attrib:
                        self.assertFalse(attr == 'style' or attr.startswith('on') or attr == 'transform',
                                         f'{local(el)} has {attr}')
                hover = [el for el in root if el.get('class') == 'hp-hover']
                self.assertEqual(len(hover), 1)

    def test_server_assertions(self):
        """The fixture's own expectations: root attributes, tips payloads
        and the ids its selectors name (FNV-1a keys, spec §4.1)."""
        for name in SUPPORTED:
            doc, checks = fixture(name)
            svg, table = render(chart_from_input(doc['chart']))
            trees = {'chart': ET.fromstring(svg), 'table': ET.fromstring(table)}
            for a in checks['server']:
                with self.subTest(fixture=name, sel=a['sel']):
                    tree = trees[a.get('in', 'chart')]
                    if a['sel'] == 'svg':
                        for attr, want in a.get('attrs', {}).items():
                            self.assertEqual(tree.get(attr), want, attr)
                        self.assertLessEqual(set(a.get('classes', [])), set(tree.get('class').split()))
                    if 'tips' in a:
                        self.check_tips(tips(tree), a['tips'])
                    if '#' in a['sel'] and a.get('count', 1):
                        want = re.search(r'#([\w:.-]+)', a['sel']).group(1)
                        self.assertIn(want, [el.get('id') for el in tree.iter()])

    def check_tips(self, got: dict, want: dict):
        if 'mode' in want:
            self.assertEqual(got['mode'], want['mode'])
        if 'count' in want:
            self.assertEqual(len(got['rows']), want['count'])
        if 'k' in want:
            self.assertEqual([r['k'] for r in got['rows']], want['k'])
        entries = [(e[0], e[1]) for r in got['rows'] for e in r['v']]
        if 'names' in want:
            self.assertEqual(list(dict.fromkeys(n for _, n in entries)), want['names'])
        if 'slots' in want:
            self.assertEqual([list(p) for p in dict.fromkeys(entries)], want['slots'])

    def test_ids_are_unique(self):
        for name in SUPPORTED:
            with self.subTest(fixture=name):
                _, _, root, table = self.render(name)
                for tree in (root, table):
                    ids = [el.get('id') for el in tree.iter() if el.get('id')]
                    self.assertEqual(len(ids), len(set(ids)))

    def test_table_matches_the_payload(self):
        """One row per payload row, in order, with the row's key as its id
        (spec §3.8, §4.1), and the en dash written for screen readers."""
        for name in ('line-time', 'bars-band', 'escaping', 'collisions'):
            with self.subTest(fixture=name):
                _, _, root, table = self.render(name)
                rows = tips(root)['rows']
                trs = [el for el in table.iter('tr') if el.get('id')]
                cid = root.get('id')
                self.assertEqual([tr.get('id') for tr in trs], [f'{cid}-r-{k}' for k in keys(r['k'] for r in rows)])
        _, _, _, table = self.render('bars-band')
        self.assertEqual([el.text for el in table.iter('span') if el.get('class') == 'hp-sr'], [' no data'])

    def test_escaping_round_trips(self):
        doc, _ = fixture('escaping')
        name = doc['chart']['marks'][0]['name']
        _, _, root, _ = self.render('escaping')
        self.assertEqual(root.find(SVG + 'title').text, 'Q&A <1>')
        series = [el for el in root.iter() if el.get('data-hp-series') is not None]
        self.assertEqual(series[0].get('data-hp-series'), name.replace(chr(1), ''))  # XML drops U+0001
        self.assertEqual(tips(root)['rows'][0]['v'][0][1], name)  # the JSON keeps it

    def test_slide_frames(self):
        """frame-b's overscan point (14:00) is drawn but has no row (§3.5),
        and the series keeps its id in both frames (§4.1)."""
        with open(os.path.join(FIXTURES, 'slide', 'input.json'), encoding='utf-8') as f:
            frames = json.load(f)['frames']
        a, b = (ET.fromstring(render(chart_from_input(frames[n]))[0]) for n in ('frame-a', 'frame-b'))
        self.assertEqual(b.get('data-hp-x'), 'time 1772546700000 1772548500000')
        self.assertEqual(b.get('data-hp-y'), 'linear 0 100')
        self.assertEqual(len(tips(b)['rows']), 7)
        self.assertEqual(tips(b)['rows'][0]['k'], '2026-03-03T14:05:00Z')
        sid = [el.get('id') for el in a.iter() if el.get('class', '').startswith('hp-series')]
        self.assertEqual(sid, [el.get('id') for el in b.iter() if el.get('class', '').startswith('hp-series')])
        self.assertEqual(sid, ['sl-s-9f07c3c1'])


class Renderer(unittest.TestCase):
    def test_same_chart_same_bytes(self):
        doc, _ = fixture('line-time')
        self.assertEqual(render(chart_from_input(doc['chart'])), render(chart_from_input(doc['chart'])))

    def test_gaps_and_missing_values(self):
        t = [parse_time_value(f'2026-03-03T14:0{n}:00Z') for n in range(4)]
        svg, table = render(Chart('g', 'Gaps', [Mark('line', 'a', t, [1, None, 3, float('nan')]),
                                                Mark('line', None, t, [None, None, None, 4])]))
        root = ET.fromstring(svg)
        rows = tips(root)['rows']
        self.assertEqual([r['k'][11:16] for r in rows], ['14:00', '14:02', '14:03'])  # 14:01 has no value
        self.assertEqual(rows[0]['v'], [[1, 'a', '1'], [2, 'Series 2', chr(0x2013)]])
        self.assertIn('d="M', svg)
        self.assertEqual(svg.count('class="hp-line"'), 2)
        ET.fromstring(table)

    def test_narrow_charts_still_have_a_plot(self):
        t = [parse_time_value('2026-03-03T14:00:00Z'), parse_time_value('2026-03-03T14:10:00Z')]
        for w in (50, 51.5, 4096):
            with self.subTest(width=w):
                root = ET.fromstring(render(Chart('n', 'N', [Mark('line', 'a', t, [1, 2])], width=w))[0])
                left, _, right, _ = map(float, root.get('data-hp-plot').split())
                self.assertLess(left, right)

    def test_runs_without_int_is_integer(self):
        """The README promises Python 3.10, where int has no is_integer
        (added in 3.12). Every int that round() hands back here lacks it,
        so a render that calls it on an int fails on any version."""
        import builtins
        from unittest import mock

        import render as render_module

        class Int310(int):
            def is_integer(self):
                raise AttributeError("'int' object has no attribute 'is_integer'")

        def round310(v, *nd):
            r = builtins.round(v, *nd)
            return Int310(r) if type(r) is int else r

        with mock.patch.object(render_module, 'round', round310, create=True):
            for name in SUPPORTED:
                with self.subTest(fixture=name):
                    doc, _ = fixture(name)
                    svg, _ = render(chart_from_input(doc['chart']))
                    ET.fromstring(svg)

    def test_rejects_what_it_cannot_draw(self):
        t = [parse_time_value('2026-03-03T14:00:00Z')]
        bad = [Chart('bad id', 'x'),
               Chart('m', 'x', [Mark('line', 'a', t, [1]), Mark('bar', 'b', ['c'], [1])]),
               Chart('n', 'x', [Mark('line', str(n), t, [1]) for n in range(9)]),
               Chart('o', 'x', [Mark('line', 'a', t, [1, 2])])]
        for c in bad:
            with self.subTest(chart=c.id):
                with self.assertRaises(ValueError):
                    render(c)


if __name__ == '__main__':
    unittest.main()
