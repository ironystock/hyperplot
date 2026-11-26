"""Tests of the wire helpers: hp_wire.py against docs/spec.md, the key
derivation against Appendix A, and the server's parameter readers against
the server cases of testdata/conformance/params.json.

    python3 -m unittest discover examples/python     # from the repository root
"""

from __future__ import annotations

import json
import os
import re
import unittest

import hp_wire
import server
from hp_wire import number, parse_time_value, time_value
from render import fnv1a, keys

ROOT = os.path.join(os.path.dirname(os.path.abspath(__file__)), '..', '..')


def read(*path: str) -> str:
    with open(os.path.join(ROOT, *path), encoding='utf-8', newline='') as f:
        return f.read()


class AppendixB(unittest.TestCase):
    def test_hp_wire_is_the_spec_block(self):
        """Below its comment header, hp_wire.py is Appendix B's Python block,
        byte for byte: the spec, not this example, owns those helpers."""
        spec = read('docs', 'spec.md')
        appendix = spec[spec.index('## Appendix B'):]
        block = re.search(r'```python\n(.*?)```', appendix, re.S).group(1)
        source = read('examples', 'python', 'hp_wire.py')
        header, _, body = source.partition('\n\n')
        self.assertTrue(all(line.startswith('#') for line in header.split('\n')), 'the header is comments only')
        self.assertEqual(body, block)

    def test_number(self):
        cases = [(0.0, '0'), (2.0, '2'), (-0.0, '0'), (0.1, '0.1'), (2.5, '2.5'), (0.001, '0.001'),
                 (1e21, '1e+21'), (1e-7, '1e-7'), (123.456, '123.456'), (-40.5, '-40.5'),
                 (1772546400000.0, '1772546400000'), (0.1 + 0.2, '0.30000000000000004')]
        for v, want in cases:
            with self.subTest(v=v):
                self.assertEqual(number(v), want)

    def test_time_value(self):
        self.assertEqual(time_value(parse_time_value('2026-03-03T15:05:00+01:00')), '2026-03-03T14:05:00Z')
        self.assertEqual(time_value(parse_time_value('2026-03-03T14:05:00.250Z')), '2026-03-03T14:05:00.25Z')

    def test_escaping_drops_what_xml_cannot_carry(self):
        s = 'a<b>&"' + chr(1) + chr(0xD800) + '\t\n\r'
        self.assertEqual(hp_wire.xml_text(s), 'a&lt;b&gt;&amp;"\t\n&#13;')
        self.assertEqual(hp_wire.xml_attr(s), 'a&lt;b&gt;&amp;&quot;&#9;&#10;&#13;')
        tips = hp_wire.tips_json({'t': '</metadata>' + chr(1)})
        self.assertNotIn('<', tips)
        self.assertEqual(json.loads(tips), {'t': '</metadata>' + chr(1)})


class Keys(unittest.TestCase):
    def test_appendix_a(self):
        cases = {'p50': '2e119f3a', 'p50#1': '4e3e42de', 'Q1': '9bfcdfb7',
                 '2026-03-03T14:00:00Z': 'f9457bc2', 'Q1149599': 'e0864ec2', 'Q1312382': 'e0864ec2'}
        for s, want in cases.items():
            with self.subTest(s=s):
                self.assertEqual(fnv1a(s), want)

    def test_collisions_get_the_next_free_key(self):
        self.assertEqual(keys(['p50', 'p50']), ['2e119f3a', '4e3e42de'])
        self.assertEqual(keys(['Q1149599', 'Q1312382']), ['e0864ec2', 'eb6f4726'])

    def test_lone_surrogates_hash_as_replacement_characters(self):
        self.assertEqual(fnv1a('a' + chr(0xDC80)), fnv1a('a' + chr(0xFFFD)))


class Params(unittest.TestCase):
    """The server cases of params.json (spec §4.6, §5)."""

    cases = json.loads(read('testdata', 'conformance', 'params.json'))['server']

    def test_size(self):
        for c in self.cases:
            if 'size' not in c:
                continue
            with self.subTest(query=c['query']):
                q = server.query('/?' + c['query'])
                (dw, dh), want = c['size']['default'], c['size']['want']
                self.assertEqual([server.size(q.get('hp-w'), dw), server.size(q.get('hp-h'), dh)], want)

    def test_select(self):
        for c in self.cases:
            if 'select' not in c:
                continue
            want = c['select']
            with self.subTest(query=c['query']):
                got = server.selected(server.query('/?' + c['query']))
                self.assertEqual(got is not None, want['ok'])
                if got:
                    self.assertEqual(got, (want['chart'], want['x'], want['series']))
                if 'time' in want:
                    self.assertEqual(time_value(parse_time_value(got[1])), want['time'])

    def test_brush(self):
        for c in self.cases:
            if 'brush' not in c:
                continue
            want = c['brush']
            with self.subTest(query=c['query']):
                got = server.brushed(server.query('/?' + c['query']))
                self.assertEqual(got is not None, want['ok'])
                if not got:
                    continue
                self.assertEqual(got, (want['chart'], want['x0'], want['x1']))
                x0, x1 = got[1], got[2]
                if 'times' in want:
                    self.check(lambda: [time_value(t) for t in server.brush_times(x0, x1)], want['times'])
                if 'floats' in want:
                    self.check(lambda: list(server.brush_floats(x0, x1)), want['floats'])

    def test_brush_in_a_zone(self):
        """datetime-local values read in a zone with clock changes (§5.3)."""
        try:
            from zoneinfo import ZoneInfo
            ZoneInfo('America/New_York')
        except Exception:  # Python on Windows has no tz database without the tzdata package
            self.skipTest('no time zone database (pip install tzdata)')
        for c in self.cases:
            want = c.get('brush', {}).get('timesIn')
            if not want:
                continue
            with self.subTest(query=c['query']):
                _, x0, x1 = server.brushed(server.query('/?' + c['query']))
                tz = ZoneInfo(want['zone'])
                self.check(lambda: [time_value(t) for t in server.brush_times(x0, x1, tz)], want['times'])

    def check(self, parse, want):
        """parse() equals want, or raises ValueError when want is "error"."""
        if want == 'error':
            with self.assertRaises(ValueError):
                parse()
        else:
            self.assertEqual(parse(), want)


if __name__ == '__main__':
    unittest.main()
