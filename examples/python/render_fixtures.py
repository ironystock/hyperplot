"""render_fixtures.py: render the conformance fixtures with render.py.

    python3 examples/python/render_fixtures.py OUT [--fixtures testdata/conformance]

For every fixture in testdata/conformance/ whose chart this renderer can
draw (lines on a time axis, bars on a band axis, no marks), it reads
input.json and writes OUT/<fixture>/chart.svg and table.html, or one
OUT/slide/<frame>.svg per frame. The language-neutral checker then runs each
fixture's checks.json against them:

    python3 tools/hpcheck/hpcheck.py fixtures --out OUT --require line-time,empty,bars-band,escaping,collisions

scatter-xy (dots, hover mode "xy") is skipped: the example page has no
scatter chart, so this renderer does not draw one.
"""

from __future__ import annotations

import argparse
import json
import os
import sys

from hp_wire import parse_time_value
from render import Chart, Mark, render

HERE = os.path.dirname(os.path.abspath(__file__))
FIXTURES = os.path.join(HERE, '..', '..', 'testdata', 'conformance')


def chart_from_input(c: dict) -> Chart:
    """A Chart from a fixture's language-neutral chart description (see
    testdata/conformance/README.md). Raises ValueError for marks this
    renderer does not draw."""
    marks = []
    for m in c.get('marks', []):
        x = m.get('x', {})
        if m.get('type') == 'line' and 'times' in x:
            xs = [parse_time_value(t) for t in x['times']]
        elif m.get('type') == 'bar' and 'categories' in x:
            xs = list(x['categories'])
        else:
            raise ValueError(f"a {m.get('type')} mark on {sorted(x)} is not supported")
        marks.append(Mark(m['type'], m.get('name'), xs, m.get('y', [])))
    xd, yd = c.get('x', {}).get('timeDomain'), c.get('y', {}).get('domain')
    return Chart(
        id=c['id'],
        title=c.get('title', ''),
        marks=marks,
        width=c.get('width', 640),
        height=c.get('height', 320),
        animate=c.get('animate', False),
        x_domain=tuple(parse_time_value(t) for t in xd) if xd else None,
        y_domain=tuple(yd) if yd else None,
        unit=c.get('y', {}).get('unit', ''),
    )


def write(path: str, text: str) -> None:
    """Write text as UTF-8 bytes, exactly: no newline translation, which on
    Windows would turn every LF into CRLF (spec §3.1 forbids raw CRs)."""
    os.makedirs(os.path.dirname(path), exist_ok=True)
    with open(path, 'wb') as f:
        f.write(text.encode('utf-8'))


def main(argv: list[str] | None = None) -> int:
    p = argparse.ArgumentParser(description=__doc__.split('\n\n')[0])
    p.add_argument('out', help='directory to write OUT/<fixture>/... into')
    p.add_argument('--fixtures', default=FIXTURES, help='the conformance fixtures directory')
    args = p.parse_args(argv)
    for name in sorted(os.listdir(args.fixtures)):
        path = os.path.join(args.fixtures, name, 'input.json')
        if not os.path.isfile(path):
            continue
        with open(path, encoding='utf-8') as f:
            doc = json.load(f)
        try:
            if 'frames' in doc:  # slide: several frames of one chart
                for frame, c in doc['frames'].items():
                    svg, _ = render(chart_from_input(c))
                    write(os.path.join(args.out, name, frame + '.svg'), svg)
            else:
                svg, table = render(chart_from_input(doc['chart']))
                write(os.path.join(args.out, name, 'chart.svg'), svg)
                if doc.get('table'):
                    write(os.path.join(args.out, name, 'table.html'), table)
        except ValueError as e:
            print(f'skip {name}: {e}')
            continue
        print(f'rendered {name}')
    return 0


if __name__ == '__main__':
    sys.exit(main())
