#!/usr/bin/env python3
"""hpcheck: check charts and servers against the hx-plot wire spec.

    python3 tools/hpcheck/hpcheck.py fixtures [--fixtures DIR] [--out DIR]
             [--require NAME,...] [--strict]
    python3 tools/hpcheck/hpcheck.py file PATH... [--table PATH] [--strict]
    python3 tools/hpcheck/hpcheck.py server BASEURL --config hpcheck.json
             [--spawn "CMD with {port}"] [--level N] [--strict]

It is the language-neutral form of the Go reference's own spec tests
(speccheck_test.go, specdom_test.go and conformance_test.go in the
repository root), for servers written in any language:

- "fixtures" runs the conformance fixtures of testdata/conformance/. With
  no --out it checks the reference files themselves. With --out DIR it
  checks your renderer's output, DIR/<fixture>/chart.svg, table.html or
  frame-*.svg (the file names of each fixture's "reference" map), against
  the universal checks and the fixture's checks.json. A fixture missing
  from DIR is skipped unless --require names it.
- "file" runs the universal checks on .svg files, and on HTML pages or
  fragments: every <svg ... data-hp> in them, with its {id}-table, and
  every other svg.hp, which the client treats as a chart too (§6.4).
- "server" checks a running server, as its hpcheck.json describes it (see
  README.md). --spawn starts the server on a free port first and stops it
  (and its child processes) afterwards.

The universal checks are the MUSTs of docs/spec.md that hold for every
chart at Level 2, whatever its data. A failure prints
"FAIL §x.y: what, where"; a broken SHOULD prints "WARN ..." and fails only
under --strict. Exit status: 0 pass (warnings allowed), 1 failures, 2 usage
or I/O error. Python 3.9+, standard library only.
"""

from __future__ import annotations

import argparse
import calendar
import http.client
import json
import math
import os
import re
import shlex
import signal
import socket
import subprocess
import sys
import tempfile
import time
import xml.parsers.expat
from datetime import datetime
from decimal import Decimal
from fractions import Fraction
from html.parser import HTMLParser
from urllib.parse import quote, urlsplit

ROOT = os.path.normpath(os.path.join(os.path.dirname(os.path.abspath(__file__)), '..', '..'))
FIXTURES = os.path.join(ROOT, 'testdata', 'conformance')
SVG_NS = 'http://www.w3.org/2000/svg'
MISSING = '\N{EN DASH}'  # how the metadata writes a missing value (spec §3.8)

CHART_ID = re.compile(r'[A-Za-z0-9_:.-]+')
VIEWBOX = re.compile(r'0 0 (\S+) (\S+)')
SLOT = re.compile(r'hp-s([0-9]+)')
TIME_KEY = re.compile(r'([0-9]{4})-([0-9]{2})-([0-9]{2})T([0-9]{2}):([0-9]{2}):([0-9]{2})(?:\.([0-9]+))?Z')


class Usage(Exception):
    """A usage or I/O error: exit status 2."""


# ---- A tiny DOM ---------------------------------------------------------------


class Node:
    """An element, or a text node when name is ''."""

    __slots__ = ('name', 'attrs', 'children', 'parent', 'data')

    def __init__(self, name: str, attrs=None, parent: Node | None = None, data: str = ''):
        self.name = name
        self.attrs: list[tuple[str, str]] = attrs or []
        self.children: list[Node] = []
        self.parent = parent
        self.data = data

    def attr(self, name: str) -> str | None:
        for k, v in self.attrs:
            if k == name:
                return v
        return None

    def get(self, name: str) -> str:
        v = self.attr(name)
        return '' if v is None else v

    def has_class(self, c: str) -> bool:
        return c in self.get('class').split()

    def elements(self) -> list[Node]:
        return [c for c in self.children if c.name]

    def text(self) -> str:
        if not self.name:
            return self.data
        return ''.join(c.text() for c in self.children)

    def walk(self):
        """This element and every element below it, in document order."""
        if not self.name:
            return
        yield self
        for c in self.children:
            yield from c.walk()

    def descendants(self) -> list[Node]:
        return [e for e in self.walk() if e is not self]

    def path(self) -> str:
        parts = []
        e = self
        while e is not None:
            s = e.name
            if e.get('id'):
                s += '#' + e.get('id')
            elif e.get('class'):
                s += '.' + e.get('class').replace(' ', '.')
            parts.append(s)
            e = e.parent
        return ' > '.join(reversed(parts))


def _local(name: str) -> str:
    """The local part of a qualified name; xmlns:p keeps its prefix (as the
    Go checker names it)."""
    if name.startswith('xmlns:'):
        return name
    return name.rsplit(':', 1)[-1]


def parse_xml(b: bytes) -> Node:
    """Parse one XML document (a chart, or a table, which is well-formed
    too) and return its root element. Raises ValueError."""
    doc = Node('#document')
    cur = [doc]
    p = xml.parsers.expat.ParserCreate()
    p.ordered_attributes = True

    def start(name, attrs):
        n = Node(_local(name), [(_local(attrs[i]), attrs[i + 1]) for i in range(0, len(attrs), 2)], cur[-1])
        cur[-1].children.append(n)
        cur.append(n)

    def end(_name):
        cur.pop()

    def chars(data):
        cur[-1].children.append(Node('', parent=cur[-1], data=data))

    p.StartElementHandler, p.EndElementHandler, p.CharacterDataHandler = start, end, chars
    try:
        p.Parse(b, True)
    except xml.parsers.expat.ExpatError as e:
        raise ValueError(f'not well-formed XML: {e}') from None
    roots = doc.elements()
    if len(roots) != 1:
        raise ValueError('no root element' if not roots else 'more than one root element')
    roots[0].parent = None
    return roots[0]


class _HTMLTree(HTMLParser):
    """A forgiving tree builder for HTML tables that are not XML (an HTML5
    table may use void elements or entities XML does not know)."""

    VOID = {'br', 'hr', 'img', 'input', 'meta', 'link', 'col', 'wbr', 'source', 'area', 'base', 'embed', 'track'}

    def __init__(self):
        super().__init__(convert_charrefs=True)
        self.doc = Node('#document')
        self.stack = [self.doc]

    def handle_starttag(self, tag, attrs):
        n = Node(tag, [(k, v or '') for k, v in attrs], self.stack[-1])
        self.stack[-1].children.append(n)
        if tag not in self.VOID:
            self.stack.append(n)

    def handle_startendtag(self, tag, attrs):
        self.handle_starttag(tag, attrs)
        if tag not in self.VOID:
            self.stack.pop()

    def handle_endtag(self, tag):
        for i in range(len(self.stack) - 1, 0, -1):
            if self.stack[i].name == tag:
                del self.stack[i:]
                return

    def handle_data(self, data):
        self.stack[-1].children.append(Node('', parent=self.stack[-1], data=data))


def parse_table(b: bytes) -> Node:
    """A table as XML, or failing that as HTML."""
    try:
        return parse_xml(b)
    except ValueError:
        t = _HTMLTree()
        t.feed(b.decode('utf-8', 'replace'))
        t.close()
        roots = t.doc.elements()
        if len(roots) != 1:
            raise ValueError('the table is not one element') from None
        roots[0].parent = None
        return roots[0]


# ---- Selectors (the subset of testdata/conformance/README.md) -----------------

_IDENT = re.compile(r'[-_0-9A-Za-z]*')


def parse_selector(s: str):
    """Type selectors and *, .class, #id, [attr] and [attr="value"],
    compounds of these, and the descendant and child combinators. Returns
    (parts, combinators); each part is (tag, id, classes, attrs)."""
    parts, comb, i, pending = [], [], 0, ''

    def ident():
        nonlocal i
        m = _IDENT.match(s, i)
        i = m.end()
        return m.group(0)

    while True:
        ws = False
        while i < len(s) and s[i] == ' ':
            i += 1
            ws = True
        if i == len(s):
            break
        if s[i] == '>':
            pending = '>'
            i += 1
            continue
        if parts:
            if pending:
                comb.append(pending)
            elif ws:
                comb.append(' ')
            else:
                raise ValueError(f'selector {s!r}: unexpected {s[i]!r}')
        elif pending:
            raise ValueError(f'selector {s!r} starts with a combinator')
        pending = ''
        tag, cid, classes, attrs = '', '', [], []
        if s[i] == '*':
            tag = '*'
            i += 1
        else:
            tag = ident()
        while i < len(s) and s[i] in '.#[':
            c = s[i]
            i += 1
            if c == '.':
                name = ident()
                if not name:
                    raise ValueError(f'selector {s!r}: empty class')
                classes.append(name)
            elif c == '#':
                cid = ident()
                if not cid:
                    raise ValueError(f'selector {s!r}: empty id')
            else:
                name, value, eq = ident(), '', False
                if not name:
                    raise ValueError(f'selector {s!r}: empty attribute name')
                if i < len(s) and s[i] == '=':
                    if i + 1 >= len(s) or s[i + 1] != '"':
                        raise ValueError(f'selector {s!r}: attribute values must be double-quoted')
                    end = s.find('"', i + 2)
                    if end < 0:
                        raise ValueError(f'selector {s!r}: unterminated value')
                    value, eq = s[i + 2:end], True
                    i = end + 1
                if i >= len(s) or s[i] != ']':
                    raise ValueError(f'selector {s!r}: expected ]')
                i += 1
                attrs.append((name, value, eq))
        if not (tag or cid or classes or attrs):
            raise ValueError(f'selector {s!r}: unexpected {s[i]!r}')
        parts.append((tag, cid, classes, attrs))
    if not parts or pending:
        raise ValueError(f'selector {s!r} is incomplete')
    return parts, comb


def _matches(part, n: Node) -> bool:
    tag, cid, classes, attrs = part
    if tag and tag != '*' and tag != n.name:
        return False
    if cid and n.get('id') != cid:
        return False
    if any(not n.has_class(c) for c in classes):
        return False
    for name, value, eq in attrs:
        v = n.attr(name)
        if v is None or (eq and v != value):
            return False
    return True


def _match_at(sel, n: Node, i: int) -> bool:
    parts, comb = sel
    if not _matches(parts[i], n):
        return False
    if i == 0:
        return True
    if comb[i - 1] == '>':
        return n.parent is not None and _match_at(sel, n.parent, i - 1)
    p = n.parent
    while p is not None:
        if _match_at(sel, p, i - 1):
            return True
        p = p.parent
    return False


def query(root: Node, s: str) -> list[Node]:
    """The elements of the tree at root (root included) matching s."""
    sel = parse_selector(s)
    return [n for n in root.walk() if _match_at(sel, n, len(sel[0]) - 1)]


# ---- Value forms (spec §1.1) ----------------------------------------------------


def number(v: float) -> str:
    """The spec's number form (Appendix B's number): ECMAScript's
    Number.prototype.toString."""
    if not math.isfinite(v):
        raise ValueError(v)
    if v == 0:
        return '0'
    sign = '-' if v < 0 else ''
    t = Decimal(repr(abs(v))).normalize().as_tuple()
    d = ''.join(map(str, t.digits))
    k, n = len(d), len(d) + t.exponent
    if k <= n <= 21:
        return sign + d + '0' * (n - k)
    if 0 < n <= 21:
        return sign + d[:n] + '.' + d[n:]
    if -6 < n <= 0:
        return sign + '0.' + '0' * -n + d
    e = n - 1
    return sign + d[0] + ('.' + d[1:] if k > 1 else '') + 'e' + ('+' if e > 0 else '-') + str(abs(e))


def time_key_ms(k: str) -> Fraction | None:
    """The epoch milliseconds of a time value in its key form (UTC, Z, a
    fraction only when non-zero and without trailing zeros, years 1 to
    9999), exactly; None for anything else."""
    m = TIME_KEY.fullmatch(k)
    if not m:
        return None
    y, mo, d, h, mi, s, frac = m.groups()
    if frac is not None and (frac.endswith('0') or len(frac) > 9):
        return None
    fields = tuple(int(v) for v in (y, mo, d, h, mi, s))
    try:
        datetime(*fields)  # years 1 to 9999, real dates, no second 60
    except ValueError:
        return None
    ms = Fraction(calendar.timegm(fields) * 1000)
    if frac:
        ms += Fraction(int(frac), 10 ** len(frac)) * 1000
    return ms


def xml_chars(s: str) -> str:
    """Drop what XML 1.0 cannot carry, as renderers do in markup (§3.6)."""
    return ''.join(c for c in s if _xml_char(ord(c)))


def _xml_char(o: int) -> bool:
    return o in (0x9, 0xA, 0xD) or 0x20 <= o <= 0xD7FF or 0xE000 <= o <= 0xFFFD or o >= 0x10000


def _reject_constant(name):
    raise ValueError(f'{name} is not JSON')


def loads(text: str):
    """JSON as the spec means it: no NaN or Infinity."""
    return json.loads(text, parse_constant=_reject_constant)


# ---- The universal checks ----------------------------------------------------------


class Geometry:
    def __init__(self):
        self.ok = False
        self.l = self.t = self.r = self.b = 0.0
        self.x_kind = ''
        self.x0 = self.x1 = 0.0


def _is_num(v) -> bool:
    return isinstance(v, (int, float)) and not isinstance(v, bool)


def check_spec(chart: bytes, table: bytes | None = None) -> list[str]:
    """Check a chart, and its table when given, against the spec's
    universal MUSTs. Returns the violations, each starting with its §."""
    r: list[str] = []
    if b'\r' in chart:
        r.append('§3.1: the chart holds a raw CR byte')
    try:
        root = parse_xml(chart)
    except ValueError as e:
        r.append(f'§3.1: {e}')
        return r
    cid = _check_root(r, root)
    _check_safety(r, root, 'chart')
    _check_no_transform(r, root)

    seen = set()
    for n in root.walk():
        v = n.attr('id')
        if v is None:
            continue
        if v in seen:
            r.append(f'§4.1: duplicate id {v!r}')
        seen.add(v)
        if n is not root and not v.startswith(cid + '-'):
            r.append(f"§4.1: id {v!r} is not in the chart's {{id}}-* namespace")

    series = query(root, 'g.hp-series')
    if len(series) > 8:
        r.append(f'§3.3: {len(series)} series groups, at most 8')
    slots: set[int] = set()
    for g in series:
        if g.parent is None or g.parent.name != 'g' or not g.parent.has_class('hp-marks'):
            r.append(f'§3.3: {g.path()} is not a child of g.hp-marks')
        n = 0
        for c in g.get('class').split():
            m = SLOT.fullmatch(c)
            if m:
                s = int(m.group(1))
                if s < 1 or s > 8 or s in slots:
                    r.append(f'§3.3: {g.path()} has slot {s} (1 to 8, unique)')
                slots.add(s)
                n += 1
        if n != 1:
            r.append(f'§3.3: {g.path()} has {n} hp-sN classes, want 1')
        gid = g.get('id')
        key = gid[len(cid) + 3:] if gid.startswith(cid + '-s-') else ''
        if not key:
            r.append(f'§4.1: series group id {gid!r} is not {cid}-s-{{key}}')
        for bar in query(g, 'path.hp-bar'):
            if not bar.get('id').startswith(f'{cid}-b-{key}-'):
                r.append(f'§4.1: bar id {bar.get("id")!r} is not {cid}-b-{key}-{{catKey}}')
    for s in ('circle.hp-focus', 'rect.hp-brush'):
        if query(root, s):
            r.append(f'§3.3: the server emitted {s}, which only the client writes')

    geo = _check_geometry(r, root)
    tips = _check_tips(r, root, chart, geo, len(series), slots)
    if table is not None:
        _check_table(r, table, cid, tips)
    return r


def _check_root(r: list[str], root: Node) -> str:
    if root.name != 'svg':
        r.append(f'§3.1: root is <{root.name}>, want <svg>')
    cid = root.get('id')
    if not CHART_ID.fullmatch(cid):
        r.append(f'§3.2: chart id {cid!r} has characters outside [A-Za-z0-9-_:.]')
    want = {'xmlns': SVG_NS, 'role': 'img', 'tabindex': '0', 'data-hp': '1',
            'aria-labelledby': cid + '-title', 'aria-describedby': cid + '-desc'}
    for k, w in want.items():
        v = root.attr(k)
        if v != w:
            r.append(f'§3.2: root {k} = {v or ""!r}, want {w!r}')
    if not root.has_class('hp'):
        r.append(f'§3.2: root class {root.get("class")!r} lacks hp')
    m = VIEWBOX.fullmatch(root.get('viewBox'))
    if not m:
        r.append(f'§3.2: viewBox {root.get("viewBox")!r} is not "0 0 W H"')
    else:
        for s in m.groups():
            try:
                v = float(s)
            except ValueError:
                v = float('nan')
            if not (v > 0) or math.isinf(v):
                r.append(f'§3.2: viewBox size {s!r} is not a positive number')
    for a in ('style', 'transform'):
        if root.attr(a) is not None:
            r.append(f'§3.2: root carries {a}')
    kids = root.elements()
    if (len(kids) < 2 or kids[0].name != 'title' or kids[0].get('id') != cid + '-title'
            or kids[1].name != 'desc' or kids[1].get('id') != cid + '-desc'):
        r.append(f'§3.3: the first two children are not <title id="{cid}-title"> and <desc id="{cid}-desc">')
    return cid


# Browsers never read a chart as XML when it is inlined: pages, innerMorph
# swaps and SSE frames all go through the HTML parser, which lowercases tag
# and attribute names (in SVG content too) and then restores the camelCase
# SVG names below. So the safety checks compare names as the HTML parser
# reads them: <SCRIPT> is a live script, STYLE= a style attribute and
# HREF="javascript:..." a link. And a chart spells every name the way the
# HTML parser leaves it, or the two parsers build different trees (§3.1).
# The tables are those of the HTML standard's "adjust SVG attributes" and
# its SVG tag-name table (tree construction, foreign content).
_SVG_ELEMENTS = {n.lower(): n for n in (
    'altGlyph', 'altGlyphDef', 'altGlyphItem', 'animateColor', 'animateMotion', 'animateTransform',
    'clipPath', 'feBlend', 'feColorMatrix', 'feComponentTransfer', 'feComposite', 'feConvolveMatrix',
    'feDiffuseLighting', 'feDisplacementMap', 'feDistantLight', 'feDropShadow', 'feFlood', 'feFuncA',
    'feFuncB', 'feFuncG', 'feFuncR', 'feGaussianBlur', 'feImage', 'feMerge', 'feMergeNode', 'feMorphology',
    'feOffset', 'fePointLight', 'feSpecularLighting', 'feSpotLight', 'feTile', 'feTurbulence',
    'foreignObject', 'glyphRef', 'linearGradient', 'radialGradient', 'textPath')}
_SVG_ATTRS = {n.lower(): n for n in (
    'attributeName', 'attributeType', 'baseFrequency', 'baseProfile', 'calcMode', 'clipPathUnits',
    'diffuseConstant', 'edgeMode', 'filterUnits', 'glyphRef', 'gradientTransform', 'gradientUnits',
    'kernelMatrix', 'kernelUnitLength', 'keyPoints', 'keySplines', 'keyTimes', 'lengthAdjust',
    'limitingConeAngle', 'markerHeight', 'markerUnits', 'markerWidth', 'maskContentUnits', 'maskUnits',
    'numOctaves', 'pathLength', 'patternContentUnits', 'patternTransform', 'patternUnits', 'pointsAtX',
    'pointsAtY', 'pointsAtZ', 'preserveAlpha', 'preserveAspectRatio', 'primitiveUnits', 'refX', 'refY',
    'repeatCount', 'repeatDur', 'requiredExtensions', 'requiredFeatures', 'specularConstant',
    'specularExponent', 'spreadMethod', 'startOffset', 'stdDeviation', 'stitchTiles', 'surfaceScale',
    'systemLanguage', 'tableValues', 'targetX', 'targetY', 'textLength', 'viewBox', 'viewTarget',
    'xChannelSelector', 'yChannelSelector', 'zoomAndPan')}

# Elements a chart must not hold, by lowercased name: script and
# foreignObject (§3.6), and the SMIL animation elements, which can set any
# attribute at run time (<set attributeName="href" to="javascript:..."/>
# inside an <a href="#x">, an on* handler, a url() to another origin), so
# that no check of the markup could vouch for the chart.
_FORBIDDEN = {'script': 'script', 'foreignobject': 'foreignObject', 'set': 'set', 'animate': 'animate',
              'animatemotion': 'animateMotion', 'animatetransform': 'animateTransform',
              'animatecolor': 'animateColor'}
# The attributes that load something: href and xlink:href (the prefix is
# dropped by _local), and src. Any other attribute that holds url(...)
# (fill, clip-path, mask, filter, marker-*, cursor and the rest) must point
# at an id in the chart; data-* and aria-* hold text, which may be a series
# named "url(x)", and nothing loads them. checkSafety in the Go reference's
# speccheck_test.go checks the same rules.
_REF_ATTRS = ('href', 'src')
_URL = re.compile(r'url\(\s*([\'"]?)(.*?)\1\s*\)', re.I)
_IMPORT = re.compile(r'@import\b', re.I)


def _html_name(name: str, table: dict[str, str]) -> str:
    """A name as the HTML parser leaves it in SVG content."""
    low = name.lower()
    return table.get(low, low)


def _check_urls(r: list[str], where: str, v: str, ids: set):
    refs = _URL.findall(v)
    if not refs:
        r.append(f'§3.6: {where}={v!r} is not url(#id)')
    for _, ref in refs:
        if not ref.startswith('#') or ref[1:] not in ids:
            r.append(f'§3.6: {where}={v!r} does not point at an id in the chart')


def _check_safety(r: list[str], root: Node, doc: str):
    ids = {n.attr('id') for n in root.walk() if n.attr('id') is not None}
    for n in root.walk():
        name = n.name.lower()
        if doc == 'chart' and n.name != _html_name(n.name, _SVG_ELEMENTS):
            r.append(f'§3.1: {n.path()}: the HTML parser reads <{n.name}> as <{_html_name(n.name, _SVG_ELEMENTS)}>')
        if name in _FORBIDDEN:
            r.append(f'§3.6: {doc} holds <{n.name}>')
        if name == 'style' and doc == 'chart':
            # A <style> element (a standalone file's theme) loads nothing:
            # no @import, and url() only to an id in the chart (§3.6).
            css = n.text()
            if _IMPORT.search(css):
                r.append(f'§3.6: {n.path()} holds an @import')
            if 'url(' in css.lower():
                _check_urls(r, f'{n.path()} text', css, ids)
        for k, v in n.attrs:
            key = k.lower()
            if doc == 'chart' and k != _html_name(k, _SVG_ATTRS):
                r.append(f'§3.1: {n.path()}: the HTML parser reads the attribute {k} as {_html_name(k, _SVG_ATTRS)}')
            if key == 'style' or key.startswith('on'):
                r.append(f'§3.6: {n.path()} carries the attribute {k}')
            if doc != 'chart':
                continue
            # No references to external resources; url(#...) points inside
            # the chart only (§3.6). A javascript: link is one too.
            if key in _REF_ATTRS and not v.strip().startswith('#'):
                r.append(f'§3.6: {n.path()} {k}={v!r} refers outside the chart')
            elif not key.startswith(('data-', 'aria-')) and 'url(' in v.lower():
                _check_urls(r, f'{n.path()} {k}', v, ids)


def _check_no_transform(r: list[str], root: Node):
    def check(n):
        if n.attr('transform') is not None:
            r.append(f'§3.3: {n.path()} carries a transform attribute')
    check(root)
    for s in ('g.hp-marks', 'g.hp-labels', 'g.hp-axis-x', 'g.hp-hover'):
        for g in query(root, s):
            for n in g.walk():
                check(n)
                if n is not g and n.name in ('svg', 'symbol', 'use'):
                    r.append(f'§3.3: {g.path()} holds <{n.name}>')
    for s in ('rect.hp-band-hl', '.hp-xa'):
        for n in query(root, s):
            check(n)


def _canonical(r: list[str], attr: str, s: str) -> float:
    try:
        v = float(s)
    except ValueError:
        v = float('nan')
    if not math.isfinite(v):
        r.append(f'§4.2: {attr} holds {s!r}, not a finite number')
        return 0.0
    w = number(v)
    if w != s:
        r.append(f'§1.1: {attr} holds {s!r}, whose number form is {w!r}')
    return v


def _check_geometry(r: list[str], root: Node) -> Geometry:
    g = Geometry()
    plot = root.get('data-hp-plot').split(' ')
    if len(plot) != 4:
        r.append(f'§4.2: data-hp-plot {root.get("data-hp-plot")!r} is not four numbers')
        return g
    g.l, g.t, g.r, g.b = (_canonical(r, 'data-hp-plot', p) for p in plot)
    if not (g.l < g.r and g.t < g.b):
        r.append(f'§4.2: data-hp-plot {root.get("data-hp-plot")!r} is empty')

    def domain(attr, f):
        if len(f) != 3:
            r.append(f'§4.2: {attr} {root.get(attr)!r} is not "kind d0 d1"')
            return 0.0, 0.0
        d0, d1 = _canonical(r, attr, f[1]), _canonical(r, attr, f[2])
        if not d0 < d1:
            r.append(f'§4.2: {attr} {root.get(attr)!r} has d0 >= d1')
        return d0, d1

    x = root.attr('data-hp-x')
    if x is not None:
        f = x.split(' ')
        g.x_kind = f[0]
        if g.x_kind in ('time', 'linear'):
            g.x0, g.x1 = domain('data-hp-x', f)
        elif g.x_kind == 'band':
            if len(f) != 1:
                r.append(f'§4.2: data-hp-x {x!r}: band has no domain')
        else:
            r.append(f'§4.2: data-hp-x {x!r} has an unknown kind')
    y = root.get('data-hp-y').split(' ')
    if y[0] != 'linear':
        r.append(f'§4.2: data-hp-y {root.get("data-hp-y")!r} is not linear')
    domain('data-hp-y', y)
    g.ok = True
    return g


GEOMETRY_TOLERANCE = 0.05  # viewBox units: coordinates and plot are rounded to 2 decimals


def _key_x(k: str, geo: Geometry):
    """A row key mapped through data-hp-x and data-hp-plot (§4.2), exactly,
    so the check measures the renderer and not its own rounding."""
    if not geo.ok or not geo.x0 < geo.x1:
        return None
    if geo.x_kind == 'time':
        v = time_key_ms(k)
    elif geo.x_kind == 'linear':
        try:
            f = float(k)
        except ValueError:
            return None
        v = Fraction(f) if math.isfinite(f) else None
    else:
        return None
    if v is None:
        return None
    d0 = Fraction(geo.x0)
    frac = float((v - d0) / (Fraction(geo.x1) - d0))
    return geo.l + frac * (geo.r - geo.l)


def _check_key(r: list[str], k: str, geo: Geometry):
    if geo.x_kind == 'time':
        ms = time_key_ms(k)
        if ms is None:
            r.append(f'§1.1: row key {k!r} is not a time value')
        elif ms < Fraction(geo.x0) or ms > Fraction(geo.x1):
            r.append(f'§3.5: row key {k!r} is outside the x domain')
    elif geo.x_kind == 'linear':
        try:
            v = float(k)
            ok = math.isfinite(v) and number(v) == k
        except ValueError:
            ok = False
        if not ok:
            r.append(f'§1.1: row key {k!r} is not in the number form')
        elif v < geo.x0 or v > geo.x1:
            r.append(f'§3.5: row key {k!r} is outside the x domain [{geo.x0}, {geo.x1}]')


def _tips_text(raw: bytes) -> bytes | None:
    start = raw.find(b'<metadata class="hp-tips">')
    if start < 0:
        return None
    body = raw[start + len(b'<metadata class="hp-tips">'):]
    end = body.find(b'</metadata>')
    return body[:end] if end >= 0 else None


def _decode_tips(text: str) -> dict:
    """The payload, with the types the spec gives it; ValueError otherwise."""
    t = loads(text)
    if not isinstance(t, dict):
        raise ValueError('not an object')
    out = {'mode': t.get('mode', ''), 'xs': t.get('xs') or [], 'bw': t.get('bw'), 'top': t.get('top'),
           'bottom': t.get('bottom'), 'pts': t.get('pts') or [], 'rows': t.get('rows') or []}
    if not isinstance(out['mode'], str):
        raise ValueError('mode is not a string')
    if not isinstance(out['xs'], list) or not all(_is_num(x) for x in out['xs']):
        raise ValueError('xs is not a list of numbers')
    for k in ('bw', 'top', 'bottom'):
        if out[k] is not None and not _is_num(out[k]):
            raise ValueError(f'{k} is not a number')
    if not isinstance(out['pts'], list) or not all(isinstance(p, list) and all(_is_num(x) for x in p)
                                                   for p in out['pts']):
        raise ValueError('pts is not a list of number lists')
    if not isinstance(out['rows'], list):
        raise ValueError('rows is not a list')
    for row in out['rows']:
        if not isinstance(row, dict):
            raise ValueError('a row is not an object')
        for k in ('t', 'k'):
            if row.get(k) is not None and not isinstance(row[k], str):
                raise ValueError(f'a row {k} is not a string')
        v = row.get('v') or []
        if not isinstance(v, list) or not all(isinstance(e, list) for e in v):
            raise ValueError('a row v is not a list of lists')
    return out


def _entry(e) -> tuple[int, str, str] | None:
    if (len(e) == 3 and isinstance(e[0], int) and not isinstance(e[0], bool) and isinstance(e[1], str)
            and isinstance(e[2], str)):
        return e[0], e[1], e[2]
    return None


def _check_tips(r: list[str], root: Node, raw: bytes, geo: Geometry, n_series: int, slots: set[int]):
    tips = {'mode': '', 'rows': [], 'xs': [], 'pts': []}
    hovers = query(root, 'g.hp-hover')
    if len(hovers) != 1:
        r.append(f'§3.3: {len(hovers)} g.hp-hover, want exactly 1')
        return tips
    metas = query(hovers[0], 'g.hp-hover > metadata.hp-tips')
    if len(metas) != 1 or len(query(root, 'metadata.hp-tips')) != 1:
        r.append(f'§3.3: g.hp-hover holds {len(metas)} metadata.hp-tips, want exactly 1')
        return tips
    text = _tips_text(raw)
    if text is not None:
        for c in b'<>&\r':
            if bytes([c]) in text:
                r.append(f'§3.5: the raw metadata text holds {chr(c)!r}')
                break
    try:
        tips = _decode_tips(metas[0].text())
    except (ValueError, RecursionError) as e:
        r.append(f'§3.5: metadata is not the payload JSON: {e}')
        return {'mode': '', 'rows': [], 'xs': [], 'pts': []}
    mode, rows, xs, pts = tips['mode'], tips['rows'], tips['xs'], tips['pts']
    n = len(rows)
    if mode in ('x', 'band'):
        if len(xs) != n:
            r.append(f'§3.5: {len(xs)} xs for {n} rows')
        if any(xs[i] < xs[i - 1] for i in range(1, len(xs))):
            r.append(f'§3.5: xs decrease: {xs}')
        for x in xs:
            if geo.ok and (x < geo.l - 0.01 or x > geo.r + 0.01):
                r.append(f'§3.5: x {x} is outside the plot [{geo.l}, {geo.r}]')
        bw = tips['bw']
        if bw is None or bw < 0 or (mode == 'x' and bw != 0):
            r.append(f'§3.5: bw {bw} is wrong for mode {mode}')
        top, bottom = tips['top'], tips['bottom']
        if top is None or bottom is None or not top < bottom:
            r.append('§3.5: top/bottom missing or empty')
        elif geo.ok and (abs(top - geo.t) > 0.01 or abs(bottom - geo.b) > 0.01):
            r.append(f'§3.5: top/bottom {top}/{bottom} differ from the plot {geo.t}/{geo.b}')
        if mode == 'x':
            c = query(hovers[0], 'g.hp-hover > line.hp-crosshair')
            if len(c) != 1 or c[0].get('visibility') != 'hidden':
                r.append('§3.3: mode x needs exactly one hidden line.hp-crosshair in g.hp-hover')
        else:
            hl = query(root, 'rect.hp-band-hl')
            if len(hl) != 1 or hl[0].get('visibility') != 'hidden':
                r.append('§3.3: mode band needs exactly one hidden rect.hp-band-hl')
            else:
                order = list(root.walk())
                marks = query(root, 'g.hp-marks')
                if marks and order.index(hl[0]) > order.index(marks[0]):
                    r.append('§3.3: rect.hp-band-hl is not before g.hp-marks')
            if geo.x_kind not in ('band', ''):
                r.append(f'§4.2: mode band with data-hp-x kind {geo.x_kind!r}')
    elif mode == 'xy':
        if len(pts) != n:
            r.append(f'§3.5: {len(pts)} pts for {n} rows')
        for p in pts:
            if len(p) != 2:
                r.append(f'§3.5: point {p} is not [x, y]')
            elif geo.ok and (p[0] < geo.l - 0.01 or p[0] > geo.r + 0.01):
                r.append(f'§3.5: point x {p[0]} is outside the plot [{geo.l}, {geo.r}]')
    else:
        r.append(f'§3.5: unknown mode {mode!r}')
        return tips
    if n > 0 and geo.x_kind == '':
        r.append(f'§4.2: {n} rows but no data-hp-x')
    keys = set()
    for i, row in enumerate(rows):
        if row.get('t') is None:
            r.append(f'§3.5: row {i} has no t')
        k = row.get('k')
        if k is None:
            r.append(f'§4.3: row {i} has no k')
            continue
        if mode != 'xy':
            if k in keys:
                r.append(f'§4.3: row key {k!r} repeats')
            keys.add(k)
        _check_key(r, k, geo)
        v = row.get('v') or []
        want = 1 if mode == 'xy' else n_series
        if len(v) != want:
            r.append(f'§3.5: row {i} lists {len(v)} series, want {want}')
        for e in v:
            ent = _entry(e)
            if ent is None:
                r.append(f'§3.5: row {i} entry is not [slot, name, value]')
                continue
            if ent[0] != 0 and ent[0] not in slots:
                r.append(f'§3.5: row {i} names slot {ent[0]}, which no series group has')
        drawn = None
        if mode == 'x' and i < len(xs):
            drawn = xs[i]
        elif mode == 'xy' and i < len(pts) and len(pts[i]) == 2:
            drawn = pts[i][0]
        px = _key_x(k, geo)
        if px is not None and drawn is not None and abs(px - drawn) > GEOMETRY_TOLERANCE:
            r.append(f'§4.2: row {i} key {k!r} maps through data-hp-x to x {px} but is drawn at {drawn}')
    return tips


def _cell_values(cells: list[Node]) -> list[str]:
    out = []
    for c in cells:
        if c.name != 'td':
            continue
        out.append(MISSING if query(c, 'td > span.hp-sr') else c.text())
    return out


def _check_table(r: list[str], b: bytes, cid: str, tips: dict):
    if b'\r' in b:
        r.append('§3.6: the table holds a raw CR byte')
    try:
        root = parse_table(b)
    except ValueError as e:
        r.append(f'§3.8: table: {e}')
        return
    _check_safety(r, root, 'table')
    if root.name != 'table' or not root.has_class('hp-table') or root.get('id') != cid + '-table':
        r.append(f'§3.8: table root is {root.path()}, want table.hp-table#{cid}-table')
    kids = root.elements()
    if not kids or kids[0].name != 'caption':
        r.append('§3.8: the table has no leading <caption>')
    if not query(root, 'table > thead > tr > th[scope="col"]'):
        r.append('§3.8: no thead row of th[scope=col]')
    rows = query(root, 'table > tbody > tr')
    meta = tips.get('rows', [])
    if len(rows) != len(meta):
        r.append(f'§3.8: {len(rows)} table rows for {len(meta)} metadata rows')
    ids = set()
    for i, tr in enumerate(rows):
        rid = tr.get('id')
        if not rid.startswith(cid + '-r-') or rid in ids:
            r.append(f'§4.1: table row id {rid!r} is not a unique {cid}-r-{{rowKey}}')
        ids.add(rid)
        if tips.get('mode') != 'xy' and len(query(tr, 'tr > th[scope="row"]')) != 1:
            r.append(f'§3.8: row {rid} is not headed by one th[scope=row]')
        if i < len(meta):
            _check_table_row(r, tr, rid, tips.get('mode'), meta[i])
        for td in query(tr, 'td'):
            if td.text().strip() == MISSING:
                r.append(f'§3.8: row {rid} writes a missing value as a bare dash')
    for sr in query(root, 'td > span.hp-sr'):
        if sr.text() != ' no data':
            r.append(f'§3.8: missing-value text {sr.text()!r}')


def _check_table_row(r: list[str], tr: Node, rid: str, mode: str, row: dict):
    heading = xml_chars(row['t']) if row.get('t') is not None else ''
    values = [xml_chars(e[2]) for e in (row.get('v') or []) if _entry(e)]
    heads = query(tr, 'tr > th[scope="row"]')
    if mode == 'xy':
        if len(heads) == 1 and heads[0].text() not in heading:
            r.append(f'§3.8: row {rid} is headed {heads[0].text()!r}, which its metadata heading {heading!r} lacks')
        if len(values) == 1 and values[0] not in _cell_values(tr.elements()):
            r.append(f'§3.8: row {rid} does not hold its metadata value {values[0]!r}')
        return
    if len(heads) != 1:
        return
    if heads[0].text() != heading:
        r.append(f'§3.8: row {rid} is headed {heads[0].text()!r}, but its metadata row reads {heading!r}')
    cells = tr.elements()
    got = _cell_values(cells[cells.index(heads[0]) + 1:])
    if got != values:
        r.append(f'§3.8: row {rid} holds {got!r}, but its metadata row {values!r}')


# ---- Fixture assertions (checks.json) ---------------------------------------------


def _fmt(v) -> str:
    """A JSON value as Go's fmt.Sprint writes it (1.0 as 1)."""
    if _is_num(v) and float(v).is_integer():
        return str(int(v))
    return str(v)


def check_assert(a: dict, root: Node) -> list[str]:
    """One server assertion of a checks.json; returns its failures."""
    try:
        matches = query(root, a['sel'])
    except ValueError as e:
        return [str(e)]
    errs = []
    if a.get('count') is not None:
        if len(matches) != a['count']:
            errs.append(f'{len(matches)} matches, want {a["count"]}')
    elif not matches:
        errs.append('no match')
    for n in matches:
        for c in a.get('classes') or []:
            if not n.has_class(c):
                errs.append(f'{n.path()} lacks class {c}')
        for k in sorted(a.get('attrs') or {}):
            v = n.attr(k)
            if v != a['attrs'][k]:
                errs.append(f'{n.path()}: {k} = {v or ""!r}, want {a["attrs"][k]!r}')
        for k in sorted(a.get('match') or {}):
            v = n.attr(k)
            if v is None or not re.search(a['match'][k], v):
                errs.append(f'{n.path()}: {k} = {v or ""!r} does not match {a["match"][k]}')
        if a.get('text') is not None and n.text() != a['text']:
            errs.append(f'{n.path()}: text {n.text()!r}, want {a["text"]!r}')
        if a.get('tips') is not None:
            errs += _check_tips_assert(a['tips'], n)
    return errs


def _check_tips_assert(w: dict, n: Node) -> list[str]:
    try:
        tips = loads(n.text())
    except ValueError as e:
        return [f'tips: {e}']
    rows = tips.get('rows') or []
    errs = []
    if w.get('mode') is not None and tips.get('mode') != w['mode']:
        errs.append(f'tips mode {tips.get("mode")!r}, want {w["mode"]!r}')
    if w.get('count') is not None and len(rows) != w['count']:
        errs.append(f'tips: {len(rows)} rows, want {w["count"]}')
    if w.get('k') is not None:
        keys = [row.get('k') for row in rows]
        if keys != w['k']:
            errs.append(f'tips k {keys}, want {w["k"]}')
    names, slots = [], []
    for row in rows:
        for e in row.get('v') or []:
            if not isinstance(e, list) or len(e) != 3:
                errs.append(f'tips entry {e} is not [slot, name, value]')
                continue
            name = e[1] if isinstance(e[1], str) else ''
            if name not in names:
                names.append(name)
            pair = f'{_fmt(e[0])}|{name}'
            if pair not in slots:
                slots.append(pair)
    if w.get('names') is not None and names != w['names']:
        errs.append(f'tips names {names}, want {w["names"]}')
    if w.get('slots') is not None:
        want = [f'{_fmt(p[0])}|{p[1]}' for p in w['slots']]
        if slots != want:
            errs.append(f'tips slots {slots}, want {want}')
    return errs


# ---- Reporting -----------------------------------------------------------------------


class Report:
    """Collects FAIL and WARN lines and prints them as they come."""

    def __init__(self, strict: bool = False, out=None):
        self.strict = strict
        self.out = out or sys.stdout
        self.fails = 0
        self.warns = 0
        self.seen: set[str] = set()  # lines printed: each one once

    def fail(self, msg: str, where: str):
        line = f'FAIL {msg}, {where}'
        if line in self.seen:  # the same request, sent with and without HX-Request, say
            return
        self.seen.add(line)
        self.fails += 1
        print(line, file=self.out)

    def warn(self, msg: str, where: str):
        """A broken SHOULD."""
        if self.strict:
            self.fail(msg, where)
            return
        line = f'WARN {msg}, {where}'
        if line in self.seen:
            return
        self.seen.add(line)
        self.warns += 1
        print(line, file=self.out)

    def note(self, msg: str):
        print(msg, file=self.out)

    def spec(self, violations: list[str], where: str):
        for v in violations:
            self.fail(v, where)

    def summary(self) -> int:
        print(f'hpcheck: {self.fails} failure(s), {self.warns} warning(s)', file=self.out)
        return 1 if self.fails else 0


# ---- fixtures ------------------------------------------------------------------------

# Which section each kind of fixture check stands for, for the message.
FORBID_SECTION = {'elements': '§3.6', 'attrs': '§3.6', 'attrPrefixes': '§3.6', 'where': '§3.3'}


def run_fixture(rep: Report, fixtures: str, name: str, out: str | None, required: bool):
    fdir = os.path.join(fixtures, name)
    with open(os.path.join(fdir, 'checks.json'), encoding='utf-8') as f:
        checks = json.load(f)
    src = os.path.join(out, name) if out else fdir
    label = f'{name} ({"your output" if out else "reference"})'
    if out and not os.path.isdir(src):
        if required:
            rep.fail(f'§2: required fixture {name} was not rendered', src)
        else:
            rep.note(f'SKIP {name}: not in {out}')
        return
    docs = []  # (name, is_chart, raw, root)
    for doc in sorted(checks['reference']):
        path = os.path.join(src, os.path.basename(checks['reference'][doc]))
        where = f'{name}/{os.path.basename(path)}'
        try:
            with open(path, 'rb') as f:
                raw = f.read()
        except OSError:
            rep.fail(f'§2: {doc} is missing', path)
            continue
        is_chart = doc != 'table'
        try:
            root = parse_xml(raw) if is_chart else parse_table(raw)
        except ValueError as e:
            rep.fail(f'§3.1: {e}', where)
            continue
        docs.append((doc, is_chart, raw, root, where))
    by_name = {d[0]: d for d in docs}
    before = rep.fails

    for doc, is_chart, raw, _, where in docs:
        if is_chart:
            table = by_name['table'][2] if doc == 'chart' and 'table' in by_name else None
            rep.spec(check_spec(raw, table), where)

    forbid = checks.get('forbid') or {}
    for doc, is_chart, raw, root, where in docs:
        for n in root.walk():
            if n.name in (forbid.get('elements') or []):
                rep.fail(f'§3.6: forbidden <{n.name}> at {n.path()}', where)
            for k, _ in n.attrs:
                if k in (forbid.get('attrs') or []):
                    rep.fail(f'§3.6: {n.path()} carries {k}', where)
                for p in forbid.get('attrPrefixes') or []:
                    if k.startswith(p):
                        rep.fail(f'§3.6: {n.path()} carries {k} (prefix {p})', where)
        for w in forbid.get('where') or []:
            for g in query(root, w['sel']):
                targets = [g] + (g.descendants() if w.get('deep') else [])
                for n in targets:
                    for a in w.get('attrs') or []:
                        if n.attr(a) is not None:
                            rep.fail(f'§3.3: {n.path()} carries {a} (forbid.where {w["sel"]})', where)
                for n in g.descendants():
                    if n.name in (w.get('elements') or []):
                        rep.fail(f'§3.3: {w["sel"]} holds <{n.name}> at {n.path()}', where)
        for s in (checks.get('raw') or {}).get('absent') or []:
            if s.encode('utf-8') in raw:
                rep.fail(f'§3.6: the document holds {s!r}', where)
        if is_chart:
            text = _tips_text(raw)
            for s in (checks.get('tipsRaw') or {}).get('absent') or []:
                if text is not None and s.encode('utf-8') in text:
                    rep.fail(f"§3.5: the metadata's raw text holds {s!r}", where)

    for i, a in enumerate(checks.get('server') or []):
        doc = a.get('frame') or (a['in'] if a.get('in') and a['in'] != 'chart' else 'chart')
        what = f'checks.json server[{i}] {a["sel"]}'
        if doc not in by_name:
            rep.fail(f'{what}: no document {doc}', name)
            continue
        for e in check_assert(a, by_name[doc][3]):
            (rep.warn if a.get('should') else rep.fail)(f'{what}: {e}', by_name[doc][4])

    for u in checks.get('unique') or []:
        scope = u.get('in') or 'chart'
        for doc, is_chart, _, root, where in docs:
            if doc != scope and not (scope == 'chart' and is_chart):
                continue
            seen = set()
            for n in query(root, u['sel']):
                v = n.get(u['attr'])
                if v in seen:
                    rep.fail(f'§4.1: unique {u["sel"]} {u["attr"]}: {v!r} repeats', where)
                seen.add(v)

    for s in checks.get('stable') or []:
        first = None
        for doc, _, _, root, where in docs:
            vals = [n.get(s['attr']) for n in query(root, s['sel'])]
            if first is None:
                first = (doc, vals)
            elif vals != first[1]:
                rep.fail(f'§4.4: stable {s["sel"]} {s["attr"]}: {first[0]} has {first[1]}, {doc} has {vals}', where)

    if rep.fails == before:
        rep.note(f'PASS {label}')


def cmd_fixtures(args) -> int:
    rep = Report(args.strict)
    if not os.path.isdir(args.fixtures):
        raise Usage(f'no fixtures directory {args.fixtures}')
    names = sorted(d for d in os.listdir(args.fixtures)
                   if os.path.isfile(os.path.join(args.fixtures, d, 'checks.json')))
    required = [n for n in (args.require or '').split(',') if n]
    for n in required:
        if n not in names:
            raise Usage(f'--require names {n}, which is not a fixture ({", ".join(names)})')
    if args.out and not os.path.isdir(args.out):
        raise Usage(f'no directory {args.out}')
    for n in names:
        run_fixture(rep, args.fixtures, n, args.out, n in required)
    return rep.summary()


# ---- file ----------------------------------------------------------------------------

# An <svg> start tag as the HTML tokenizer reads it: quoted values may hold
# '>', and the tag may end in '/>'. Or an </svg> end tag.
_SVG_TAG = re.compile(rb'<svg(?=[\s/>])(?:[^>"\']|"[^"]*"|\'[^\']*\')*>|</svg\s*>', re.I)
# A tag whose last attribute value is unquoted and runs into the '/': in
# <svg viewBox=x/> the value is "x/", and the tag is not self-closing.
_UNQUOTED_END = re.compile(rb'=\s*[^\s"\'=<>`]+$')
_STYLE_TAG = re.compile(rb'<style[\s/>]', re.I)
_DATA_HP = re.compile(rb'\sdata-hp\s*=', re.I)
_ID = re.compile(rb'\sid\s*=\s*"([^"]*)"')
# The class attribute, double-quoted, single-quoted or bare (HTML allows
# all three). The match may start right after a quote: HTML tolerates a
# missing space between attributes, and so must a check of HTML pages.
_CLASS = re.compile(rb'[\s"\']class\s*=\s*(?:"([^"]*)"|\'([^\']*)\'|([^\s"\'=<>`]+))', re.I)


def _is_svg_hp(tag: bytes) -> bool:
    """Whether an <svg> start tag has the class hp, which makes it a chart
    to the client (§6.4) whatever else it carries."""
    m = _CLASS.search(tag)
    return bool(m) and b'hp' in (m.group(1) or m.group(2) or m.group(3) or b'').split()


def find_charts(html: bytes) -> list[tuple[str, bytes]]:
    """Every chart in an HTML page or fragment, as (id, bytes), nested
    <svg>s included in their parent. A chart is an <svg ... data-hp>, and
    also any svg.hp: the client treats every svg.hp as a chart (§6.4), so
    one that is not a valid chart (a decorative swatch or icon given the
    class hp) must be reported, not skipped."""
    out = []
    for start, end in _svg_spans(html):
        svg = html[start:end]
        tag = _SVG_TAG.match(svg).group(0)
        if _DATA_HP.search(tag) or _is_svg_hp(tag):
            idm = _ID.search(tag)
            out.append((idm.group(1).decode('utf-8', 'replace') if idm else '', svg))
    return out


def _self_closing(tag: bytes) -> bool:
    """Whether an <svg> start tag ends in a self-closing '/>', which the
    HTML parser honours on SVG elements: <svg class="icon"/> is a whole,
    empty element, and no </svg> follows it."""
    return tag.endswith(b'/>') and not _UNQUOTED_END.search(tag[:-2])


# The markup tokens that matter for finding <svg> elements, in the order
# the HTML tokenizer tries them at a '<': a comment (<!--> and <!---> are
# empty ones; an unclosed one runs to the end), a CDATA section, any other
# <!...> or <?...> (doctype, bogus comment), an end tag, and a start tag
# whose quoted values may hold '<svg' or '>'. Anything else is text.
_TOKEN = re.compile(
    rb'<!--(?:-?>|.*?(?:--!?>|\Z))'
    rb'|<!\[CDATA\['
    rb'|<[!?][^>]*(?:>|\Z)'
    rb'|</([A-Za-z][^\s/>]*)[^>]*(?:>|\Z)'
    rb'|<([A-Za-z][^\s/>]*)(?:[^>"\']|"[^"]*"|\'[^\']*\')*>',
    re.S)
# Outside SVG, the HTML tokenizer reads these elements' content as text up
# to their end tag, so a '<svg' there is not a tag (spec §6.4 reads the DOM).
_RAW_TEXT = {b'script', b'style', b'textarea', b'title', b'xmp', b'iframe',
             b'noembed', b'noframes', b'noscript'}


def _svg_spans(html: bytes) -> list[tuple[int, int]]:
    """The (start, end) of every outermost <svg> element, nested <svg>s
    included in their parent, as a browser's HTML parser finds them.

    It walks the page token by token, not <svg> by <svg>, so the text
    '<svg' in a comment, in another tag's attribute value or in a <script>
    or <textarea> does not open an element that nothing closes (which
    would hide every chart after it). A self-closing <svg .../> is an
    element of its own, and </template> closes an <svg> left open inside
    the template, as the HTML parser does."""
    out, depth, start, pos = [], 0, 0, 0
    templates, in_template = 0, False  # open <template>s; whether the open <svg> is in one
    while True:
        lt = html.find(b'<', pos)
        if lt < 0:
            return out
        m = _TOKEN.match(html, lt)
        if not m:
            pos = lt + 1
            continue
        pos = m.end()
        tok = m.group(0)
        if tok.startswith(b'<![CDATA['):
            # A CDATA section only inside SVG; elsewhere a bogus comment.
            end = html.find(b']]>' if depth else b'>', pos)
            pos = len(html) if end < 0 else end + (3 if depth else 1)
            continue
        end_name, start_name = m.group(1), m.group(2)
        name = (end_name or start_name or b'').lower()
        if start_name is not None and name == b'svg':
            if depth == 0:
                start, in_template = lt, templates > 0
            if _self_closing(tok):
                if depth == 0:
                    out.append((start, pos))
                continue
            depth += 1
        elif end_name is not None and name == b'svg':
            if depth:
                depth -= 1
                if depth == 0:
                    out.append((start, pos))
        elif end_name is not None and name == b'template' and templates:
            templates -= 1
            if depth and in_template:
                depth = 0
                out.append((start, lt))
        elif depth:
            continue  # inside SVG every other tag is foreign: no raw text
        elif start_name is not None and name in _RAW_TEXT:
            close = re.compile(rb'</' + re.escape(name) + rb'(?=[\s/>])', re.I).search(html, pos)
            pos = close.start() if close else len(html)
        elif start_name is not None and name == b'template':
            templates += 1


def _one_svg(b: bytes) -> bool:
    """Whether a file is one <svg> element and nothing else."""
    s = b.strip()
    return s[:4].lower() == b'<svg' and _svg_spans(s) == [(0, len(s))]


def find_table(html: bytes, cid: str) -> bytes | None:
    """The table whose id is {cid}-table, as bytes."""
    m = re.search(rb'<table\b[^>]*\sid\s*=\s*"' + re.escape(cid.encode()) + rb'-table"', html)
    if not m:
        return None
    end = html.find(b'</table>', m.start())
    return html[m.start():end + len(b'</table>')] if end >= 0 else html[m.start():]


def check_html(rep: Report, html: bytes, where: str, want_tables: bool = False) -> int:
    """The universal checks on every chart in an HTML page or fragment.
    Returns the number of charts found."""
    charts = find_charts(html)
    for cid, svg in charts:
        if not _DATA_HP.search(svg[:svg.find(b'>') + 1]):
            # Found only by its class: say why it is checked at all.
            rep.fail(f'§6.4: <svg id={cid!r}> has the class hp, so the client treats it as a chart, '
                     'but it has no data-hp; drop the class hp or make it a chart', where)
        if _STYLE_TAG.search(svg):
            # A standalone file may carry its theme in a <style> element;
            # inlined in a page, a strict CSP (style-src 'self') refuses it
            # and the chart loses its theme (§7).
            rep.warn(f'§7: chart {cid} holds a <style> element, which a page with style-src \'self\' refuses; '
                     'serve the theme as a stylesheet file', where)
        table = find_table(html, cid)
        if table is None and want_tables:
            rep.fail(f'§3.8: chart {cid} has no table {cid}-table', where)
        rep.spec(check_spec(svg, table), f'{where} #{cid}')
    _check_page_ids(rep, html, charts, where)
    return len(charts)


_TABLE_ID = re.compile(rb'<table\b[^>]*\sid\s*=\s*"([^"]*)-table"')
_ANY_ID = re.compile(rb'\sid\s*=\s*"([^"]*)"')


def _check_page_ids(rep: Report, html: bytes, charts: list[tuple[str, bytes]], where: str):
    """§4.1 across a page or fragment: each chart id once, each chart's
    table once, and no id shared by two charts or tables. The morph matches
    elements by id, so a repeat breaks live updates (a stream card that
    also holds the stream element's first frame, say)."""
    counts: dict[str, int] = {}
    for cid, _ in charts:
        counts[cid] = counts.get(cid, 0) + 1
    for cid, n in counts.items():
        if n > 1:
            rep.fail(f'§4.1: chart id {cid!r} appears {n} times on the page', where)
    # Every chart and every {id}-table, each as one unit of ids.
    units: list[tuple[str, bytes]] = [(f'chart {cid}', svg) for cid, svg in charts]
    tables: dict[str, int] = {}
    for m in _TABLE_ID.finditer(html):
        cid = m.group(1).decode('utf-8', 'replace')
        tables[cid] = tables.get(cid, 0) + 1
        end = html.find(b'</table>', m.start())
        unit = html[m.start():end if end >= 0 else len(html)]
        for _, svg in charts:  # a chart inside a table cell is a unit of its own
            unit = unit.replace(svg, b'')
        units.append((f'table {cid}-table', unit))
    for cid, n in tables.items():
        if n > 1 and cid in counts:
            rep.fail(f'§4.1: the table {cid}-table appears {n} times on the page', where)
    owner: dict[bytes, str] = {}
    shared: list[str] = []
    done: set[str] = set()
    for name, unit in units:
        if name in done:
            continue  # a repeat of the same chart or table, reported above
        done.add(name)
        for v in dict.fromkeys(_ANY_ID.findall(unit)):
            if v in owner and owner[v] != name:
                shared.append(f'{v.decode("utf-8", "replace")!r} ({owner[v]}, {name})')
            owner.setdefault(v, name)
    if shared:
        more = f' and {len(shared) - 3} more' if len(shared) > 3 else ''
        rep.fail(f'§4.1: ids shared by two charts or tables: {", ".join(shared[:3])}{more}', where)


def cmd_file(args) -> int:
    rep = Report(args.strict)
    for path in args.paths:
        try:
            with open(path, 'rb') as f:
                b = f.read()
        except OSError as e:
            raise Usage(str(e)) from None
        before = rep.fails
        # A .svg file, or a file that is one <svg> and nothing else, is one
        # chart. Anything else is a page or fragment, even one that starts
        # with a chart: a poll response is usually the chart, then its table.
        if path.lower().endswith('.svg') or _one_svg(b):
            table = None
            if args.table:
                with open(args.table, 'rb') as f:
                    table = f.read()
            rep.spec(check_spec(b, table), path)
        elif check_html(rep, b, path) == 0:
            rep.fail('§3.1: no chart (<svg ... data-hp>) in the file', path)
        if rep.fails == before:
            rep.note(f'PASS {path}')
    return rep.summary()


# ---- server --------------------------------------------------------------------------


class Client:
    """Plain HTTP requests to one server."""

    def __init__(self, base: str, timeout: float = 10):
        u = urlsplit(base)
        if u.scheme != 'http' or not u.hostname:
            raise Usage(f'BASEURL {base!r}: want http://host:port')
        self.host, self.port, self.prefix, self.timeout = u.hostname, u.port or 80, u.path.rstrip('/'), timeout

    def get(self, path: str, headers: dict[str, str] | None = None):
        """(status, headers with lower-case names, body bytes)."""
        conn = http.client.HTTPConnection(self.host, self.port, timeout=self.timeout)
        try:
            conn.request('GET', self.prefix + path, headers=headers or {})
            r = conn.getresponse()
            return r.status, {k.lower(): v for k, v in r.getheaders()}, r.read()
        finally:
            conn.close()


def _join(url: str, query: str) -> str:
    return url + ('&' if '?' in url else '?') + query if query else url


def _html_type(rep: Report, h: dict, where: str):
    ct = h.get('content-type', '').replace(' ', '').lower()
    if ct != 'text/html;charset=utf-8':
        rep.fail(f'§3.1: Content-Type {h.get("content-type", "")!r}, want text/html; charset=utf-8', where)


def _bare(tag: str) -> str:
    t = tag.strip()
    if t.startswith('W/'):
        t = t[2:]
    if len(t) >= 2 and t[0] == '"' and t[-1] == '"':
        t = t[1:-1]
    return t


def _chart_width(body: bytes) -> float | None:
    charts = find_charts(body)
    if not charts:
        return None
    m = re.search(rb'\sviewBox\s*=\s*"0 0 (\S+) \S+"', charts[0][1])
    try:
        return float(m.group(1)) if m else None
    except ValueError:
        return None


def _params() -> list[dict]:
    with open(os.path.join(FIXTURES, 'params.json'), encoding='utf-8') as f:
        return json.load(f)['server']


def check_ptag(rep: Report, c: Client, url: str, where: str):
    """§4.5: HX-PTag on each frame, a bodiless 304 for the current tag in
    any spelling, a 200 for another tag. A live chart may move on between
    two requests, so each check gets a few tries."""
    hx = {'HX-Request': 'true'}
    status, h, _ = c.get(url, hx)
    tag = h.get('hx-ptag')
    if not tag:
        rep.fail('§4.5: no HX-PTag header on a polled chart', where)
        return
    for form in (lambda t: t, lambda t: 'W/' + t, lambda t: f'  {t} ', _bare):
        for _ in range(4):
            status, h, body = c.get(url, {**hx, 'HX-PTag': form(tag)})
            if status == 304:
                if body:
                    rep.fail(f'§4.5: a 304 with a {len(body)}-byte body', where)
                break
            tag = h.get('hx-ptag') or tag  # the chart moved on: try its new tag
        else:
            rep.fail(f'§4.5: HX-PTag {form(tag)!r} (the current tag) did not get a 304', where)
    status, _, _ = c.get(url, {**hx, 'HX-PTag': '"hpcheck-not-a-tag"'})
    if status != 200:
        rep.fail(f'§4.5: an HX-PTag that is not the current tag got {status}, want 200', where)


def check_sizes(rep: Report, c: Client, url: str, where: str):
    """§4.6: every size case of params.json is accepted; the chart is drawn
    as wide as a usable hp-w asks, clamped to [50, 4096]."""
    for case in _params():
        if 'size' not in case:
            continue
        q = case['query']
        status, _, body = c.get(_join(url, q), {'HX-Request': 'true'})
        w = f'{where}?{q}'
        if status >= 400:
            rep.fail(f'§4.6: {status} for a size parameter', w)
            continue
        if status == 200 and check_html(rep, body, w) == 0:
            rep.fail('§4.6: no chart (<svg ... data-hp>) in the response', w)
        want, default = case['size']['want'][0], case['size']['default'][0]
        usable = want != default or re.search(r'(^|&)hp-w=' + re.escape(number(want)) + r'(&|$)', q)
        if status == 200 and usable:
            got = _chart_width(body)
            if got != want:
                rep.warn(f'§4.6: hp-w asks for width {number(want)}, the chart is {got}', w)


def _brush_is_error(b: dict) -> bool:
    if not b.get('ok'):
        return True
    typed = [b[k] for k in ('times', 'floats') if k in b]
    return bool(typed) and all(v == 'error' for v in typed)


def check_gestures(rep: Report, c: Client, chart: dict, where: str, chart_body: bytes):
    """§5: params.json's select and brush cases never get a 5xx; a brush
    that does not parse is ignored (no zoom, so no HX-Push-Url)."""
    cases = _params()
    if chart.get('brush'):
        url = chart['brush']
        for case in cases:
            if 'brush' not in case:
                continue
            status, h, _ = c.get(_join(url, case['query']), {'HX-Request': 'true'})
            w = f'{url}?{case["query"]}'
            if status >= 500:
                rep.fail(f'§5.3: {status} for a brush', w)
            elif _brush_is_error(case['brush']) and 'hx-push-url' in h:
                rep.warn('§5.3: a brush that does not parse was answered as a zoom (HX-Push-Url)', w)
        # A brush across the middle of the chart's own x domain.
        charts = find_charts(chart_body)
        m = re.search(rb'\sdata-hp-x="(time|linear) (\S+) (\S+)"', charts[0][1]) if charts else None
        if m:
            kind, d0, d1 = m.group(1), float(m.group(2)), float(m.group(3))
            a, b = d0 + (d1 - d0) / 4, d0 + 3 * (d1 - d0) / 4
            if kind == b'time':
                a, b = (time.strftime('%Y-%m-%dT%H:%M:%SZ', time.gmtime(v / 1000)) for v in (a, b))
            else:
                a, b = number(a), number(b)
            q = f'hp-chart={quote(charts[0][0])}&hp-x0={quote(str(a))}&hp-x1={quote(str(b))}'
            status, h, body = c.get(_join(url, q), {'HX-Request': 'true'})
            w = f'{url}?{q}'
            if status >= 500:
                rep.fail(f'§5.3: {status} for a brush across the chart', w)
            elif status == 200:
                if check_html(rep, body, w) == 0:
                    rep.warn('§5.3: a brush across the chart was answered without a chart', w)
                if 'hx-push-url' not in h:
                    rep.warn('§5.3: a zoom without HX-Push-Url', w)
            else:
                # A server SHOULD answer a zoom (§5.3); ignoring a brush
                # of the chart's own domain means its zoom is broken.
                rep.warn(f'§5.3: a brush across the chart was not answered as a zoom (status {status})', w)
    if chart.get('select'):
        url = chart['select']
        queries = [c2['query'] for c2 in cases if 'select' in c2]
        charts = find_charts(chart_body)
        if charts:
            m = re.search(rb'"k":"((?:[^"\\]|\\.)*)"', charts[0][1])
            if m:
                key = json.loads(b'"' + m.group(1) + b'"')
                queries.append(f'hp-chart={quote(charts[0][0])}&hp-x={quote(key)}')
        for q in queries:
            status, _, body = c.get(_join(url, q), {'HX-Request': 'true'})
            if status >= 500:
                rep.fail(f'§5.2: {status} for a select', f'{url}?{q}')
            elif status == 200:
                check_html(rep, body, f'{url}?{q}')


def check_junk(rep: Report, c: Client, url: str, where: str):
    """§7: no parameter of params.json, however odd, gets a 5xx, with or
    without HX-Request; a 200 still holds valid charts (§4.6: a size that
    is not a number falls back to the default, never into the markup)."""
    for case in _params():
        for headers in ({}, {'HX-Request': 'true'}):
            status, _, body = c.get(_join(url, case['query']), headers)
            w = f'{where}?{case["query"]}'
            if status >= 500:
                rep.fail(f'§7: {status} for {case["query"]!r}', where)
            elif status == 200 and check_html(rep, body, w) == 0 and headers:
                rep.fail('§4.6: no chart (<svg ... data-hp>) in the response', w)


# What may follow a CR in an event stream, the CR being a line break: a
# blank line, a comment, or a field the stream uses.
_SSE_LINE = re.compile(rb'$|:|(data|retry|event|id)(:|$)')


def check_sse(rep: Report, c: Client, s: dict):
    """§4.5: text/event-stream; each message's data: lines form a fragment
    that passes the universal checks; no CR; retry: (should)."""
    url, want, timeout = s['url'], int(s.get('frames', 2)), float(s.get('timeout', 10))
    conn = http.client.HTTPConnection(c.host, c.port, timeout=timeout)
    try:
        conn.request('GET', c.prefix + url, headers={'Accept': 'text/event-stream', 'HX-Request': 'true'})
        r = conn.getresponse()
        ct = (r.getheader('Content-Type') or '').split(';')[0].strip().lower()
        if r.status != 200 or ct != 'text/event-stream':
            rep.fail(f'§4.5: {r.status} {r.getheader("Content-Type")!r}, want 200 text/event-stream', url)
            return
        deadline = time.monotonic() + timeout
        # retry: counts only in a message that also has data: hx-sse
        # (htmx 4.0.2) drops a message without data, retry: and all.
        frames, data, retry, cr, pending_retry = [], [], False, False, False
        while len(frames) < want:
            if time.monotonic() > deadline:
                rep.fail(f'§4.5: {len(frames)} of {want} frames in {timeout:g} s', url)
                break
            try:
                line = r.readline()
            except (socket.timeout, OSError):
                rep.fail(f'§4.5: {len(frames)} of {want} frames before the stream went quiet', url)
                break
            if not line:
                rep.fail(f'§4.5: the stream ended after {len(frames)} of {want} frames', url)
                break
            # An event stream may end its lines with CRLF, LF or a lone CR,
            # so a CR is a line break here, as hx-sse reads it. A CR inside
            # a fragment's line would break that line in two: the part
            # after it is no field hx-sse knows, and is lost (§3.6, §4.5).
            if line.endswith(b'\r\n'):
                line = line[:-2]
            elif line.endswith(b'\n'):
                line = line[:-1]
            parts = line.split(b'\r')
            cr = cr or any(not _SSE_LINE.match(p) for p in parts[1:])
            for field in parts:
                if not field:
                    if data:
                        frames.append(b'\n'.join(data))
                        retry = retry or pending_retry
                    data, pending_retry = [], False
                elif field.startswith(b'retry:'):
                    pending_retry = True
                elif field.startswith(b'data:'):
                    v = field[5:]
                    data.append(v[1:] if v.startswith(b' ') else v)
        if cr:
            rep.fail('§3.6: a raw CR inside a line of the fragment splits it in the stream', url)
        if not retry and len(frames) == want:
            rep.warn('§4.5: no retry: in a message with data (hx-sse ignores one on its own)', url)
        for i, f in enumerate(frames):
            if check_html(rep, f, f'{url} message {i + 1}') == 0:
                rep.fail('§4.5: a message without a chart', f'{url} message {i + 1}')
    except (OSError, http.client.HTTPException) as e:
        rep.fail(f'§4.5: {e}', url)
    finally:
        conn.close()


def run_server_checks(rep: Report, c: Client, cfg: dict, level: int):
    for page in cfg.get('pages') or []:
        status, h, body = c.get(page)
        if status != 200:
            rep.fail(f'§3.1: page {status}, want 200', page)
            continue
        _html_type(rep, h, page)
        check_html(rep, body, page)
        rep.note(f'checked page {page}')
    for chart in cfg.get('charts') or []:
        url = chart['url']
        status, h, body = c.get(url, {'HX-Request': 'true'})
        if status != 200:
            rep.fail(f'§4.5: {status}, want 200', url)
            continue
        _html_type(rep, h, url)
        if check_html(rep, body, url, want_tables=bool(chart.get('table'))) == 0:
            rep.fail('§3.1: no chart (<svg ... data-hp>) in the response', url)
        if level >= 2 and chart.get('ptag'):
            check_ptag(rep, c, url, url)
        if level >= 2 and chart.get('autosize'):
            check_sizes(rep, c, url, url)
        check_junk(rep, c, url, url)
        if level >= 3:
            check_gestures(rep, c, chart, url, body)
        rep.note(f'checked chart {url}')
    if level >= 2:
        for s in cfg.get('sse') or []:
            check_sse(rep, c, s)
            rep.note(f'checked stream {s["url"]}')


def _free_port() -> int:
    with socket.socket() as s:
        s.bind(('127.0.0.1', 0))
        return s.getsockname()[1]


def _kill_tree(p: subprocess.Popen):
    if p.poll() is not None:
        return
    if os.name == 'nt':
        subprocess.run(['taskkill', '/T', '/F', '/PID', str(p.pid)], capture_output=True)
    else:
        try:
            os.killpg(p.pid, signal.SIGTERM)
            p.wait(5)
        except (ProcessLookupError, subprocess.TimeoutExpired):
            try:
                os.killpg(p.pid, signal.SIGKILL)
            except ProcessLookupError:
                pass
    try:
        p.wait(10)
    except subprocess.TimeoutExpired:
        pass


def _listening(host: str, port: int) -> bool:
    try:
        socket.create_connection((host, port), timeout=0.5).close()
        return True
    except OSError:
        return False


def _exit_report(p: subprocess.Popen, log) -> str:
    log.seek(0)
    out = log.read().decode('utf-8', 'replace')
    return f'the server exited with {p.returncode}:\n{out}'


def spawn(cmd: str, host: str, port: int, wait: float = 30, grace: float = 0.5):
    """Start cmd and wait until it accepts connections on host:port.

    The port must be free beforehand, and the server must still run a
    moment after the port answers: a server that failed to bind (the port
    taken by another process in the meantime) exits, and the checks must
    not run against whatever else is listening. On any way out but success,
    Ctrl-C included, the server is stopped."""
    if _listening(host, port):
        raise Usage(f'--spawn: something already listens on {host}:{port}; choose a free --port')
    log = tempfile.TemporaryFile()
    argv = cmd if os.name == 'nt' else shlex.split(cmd)
    kw = {} if os.name == 'nt' else {'start_new_session': True}
    try:
        p = subprocess.Popen(argv, stdout=log, stderr=subprocess.STDOUT, stdin=subprocess.DEVNULL, **kw)
    except (OSError, ValueError) as e:
        log.close()
        raise Usage(f'--spawn: {e}') from None
    try:
        deadline = time.monotonic() + wait
        while not _listening(host, port):
            if p.poll() is not None:
                raise Usage('--spawn: ' + _exit_report(p, log))
            if time.monotonic() > deadline:
                raise Usage(f'--spawn: nothing listening on {host}:{port} after {wait:g} s')
            time.sleep(0.1)
        end = time.monotonic() + grace
        while time.monotonic() < end:
            if p.poll() is not None:
                raise Usage('--spawn: something answers on the port, but ' + _exit_report(p, log))
            time.sleep(0.05)
        return p, log
    except BaseException:
        _kill_tree(p)
        log.close()
        raise


def cmd_server(args) -> int:
    try:
        with open(args.config, encoding='utf-8') as f:
            cfg = json.load(f)
    except (OSError, ValueError) as e:
        raise Usage(f'--config: {e}') from None
    level = args.level or int(cfg.get('level', 3))
    base = args.base
    proc = log = None
    rep = Report(args.strict)
    try:
        if args.spawn:
            u = urlsplit(base)
            port = args.port or _free_port()
            base = f'{u.scheme}://{u.hostname}:{port}{u.path}'
            proc, log = spawn(args.spawn.replace('{port}', str(port)), u.hostname, port)
        rep.note(f'checking {base} at Level {level}')
        try:
            run_server_checks(rep, Client(base), cfg, level)
        except (OSError, http.client.HTTPException) as e:
            rep.fail(f'§2: {e}', base)
        # A spawned server that died during the run answered nothing after
        # that; whatever did answer was not it.
        if proc is not None and proc.poll() is not None:
            rep.fail('§2: during the run, ' + _exit_report(proc, log), base)
    finally:
        if proc is not None:
            _kill_tree(proc)
            log.close()
    return rep.summary()


# ---- main ----------------------------------------------------------------------------


def main(argv: list[str] | None = None) -> int:
    for stream in (sys.stdout, sys.stderr):
        if hasattr(stream, 'reconfigure'):  # § reads as § in a pipe on Windows too
            stream.reconfigure(encoding='utf-8', errors='backslashreplace')
    ap =argparse.ArgumentParser(prog='hpcheck', description='Check charts and servers against the hx-plot wire spec.')
    sub = ap.add_subparsers(dest='cmd', required=True)
    f = sub.add_parser('fixtures', help='check the conformance fixtures, or your renderer\'s output for them')
    f.add_argument('--fixtures', default=FIXTURES, help='the fixtures directory (default: testdata/conformance)')
    f.add_argument('--out', help='your renderer\'s output: OUT/<fixture>/chart.svg, table.html, frame-*.svg')
    f.add_argument('--require', help='fixtures that must be present in --out, comma-separated')
    f.add_argument('--strict', action='store_true', help='fail on should assertions too')
    f.set_defaults(run=cmd_fixtures)
    fl = sub.add_parser('file', help='check .svg files or HTML pages and fragments')
    fl.add_argument('paths', nargs='+', metavar='PATH')
    fl.add_argument('--table', help='the table of a single .svg chart')
    fl.add_argument('--strict', action='store_true')
    fl.set_defaults(run=cmd_file)
    s = sub.add_parser('server', help='check a running server')
    s.add_argument('base', metavar='BASEURL', help='e.g. http://127.0.0.1:8080')
    s.add_argument('--config', required=True, help='the server\'s hpcheck.json')
    s.add_argument('--spawn', help='start the server first: a command with {port} in it')
    s.add_argument('--port', type=int, help='with --spawn: the port to start it on (default: a free one)')
    s.add_argument('--level', type=int, choices=(1, 2, 3), help='override the config\'s level')
    s.add_argument('--strict', action='store_true')
    s.set_defaults(run=cmd_server)
    try:
        args = ap.parse_args(argv)
    except SystemExit as e:
        return 2 if e.code else 0
    try:
        return args.run(args)
    except Usage as e:
        print(f'hpcheck: {e}', file=sys.stderr)
        return 2


if __name__ == '__main__':
    sys.exit(main())
