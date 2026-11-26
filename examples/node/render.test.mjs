// render.test.mjs: the renderer against the conformance fixtures
// (spec/conformance), and the properties the wire spec asks of every
// frame. Run with `node --test` in examples/node.
import assert from 'node:assert/strict'
import { readFileSync } from 'node:fs'
import { test } from 'node:test'
import { chartFromInput, supported } from './render-fixtures.mjs'
import { render } from './render.mjs'
import { elements, fixture, fixtureProblems, parseXml, select, text } from './testing.mjs'

const NUMBER = /^-?(0|[1-9][0-9]*)(\.[0-9]*[1-9])?(e[+-][1-9][0-9]*)?$/

// The five fixtures this example must pass, plus slide, which it also
// supports. scatter-xy needs dots, which this renderer does not draw.
for (const name of ['line-time', 'empty', 'bars-band', 'escaping', 'collisions', 'slide']) {
  test(`fixture ${name}: every server assertion passes, should included`, () => {
    const input = JSON.parse(fixture(`${name}/input.json`))
    const docs = {}
    for (const [frame, c] of input.frames ? Object.entries(input.frames) : [['chart', input.chart]]) {
      assert.ok(supported(c))
      const { svg, table } = render(chartFromInput(c))
      docs[frame] = svg
      if (input.table) docs.table = table
    }
    assert.deepEqual(fixtureProblems(name, docs), [])
  })
}

test('the checks catch a broken chart', () => {
  // A check that cannot fail proves nothing: break a good chart three ways.
  const input = JSON.parse(fixture('line-time/input.json'))
  const { svg, table } = render(chartFromInput(input.chart))
  assert.deepEqual(fixtureProblems('line-time', { chart: svg, table }), [])
  for (const bad of [
    svg.replace('data-hp="1"', 'data-hp="1" style="color:red"'),
    svg.replace(/data-hp-y="linear (\d+)/, 'data-hp-y="linear $1.0'),
    svg.replace('</title>', '</titl>'),
  ]) {
    assert.notDeepEqual(fixtureProblems('line-time', { chart: bad, table }), [], bad.slice(0, 80))
  }
})

test('the escaping fixture: names reach every channel as text', () => {
  const input = JSON.parse(fixture('escaping/input.json'))
  const { svg } = render(chartFromInput(input.chart))
  const ref = fixture('escaping/chart.svg')
  // The attribute bytes equal the reference server's (spec §3.6).
  const attr = (s) => /data-hp-series="[^"]*"/.exec(s)[0]
  assert.equal(attr(svg), attr(ref))
  assert.ok(!svg.includes('\r'))
  const tips = text(select(parseXml(svg), 'metadata.hp-tips')[0])
  assert.equal(JSON.parse(tips).rows[1].k, input.chart.marks[0].x.categories[1]) // keys round-trip exactly
})

const live = {
  id: 'latency',
  title: 'Latency',
  width: 640,
  height: 260,
  animate: true,
  x: { timeDomain: [Date.UTC(2026, 2, 3, 14, 0, 0), Date.UTC(2026, 2, 3, 14, 5, 0)] },
  y: { unit: ' ms', zero: true },
  marks: ['p50', 'p95'].map((name, s) => ({
    type: 'line',
    name,
    // 14:00 minus 5 s is overscan: drawn, but outside the domain.
    x: { times: Array.from({ length: 62 }, (_, k) => Date.UTC(2026, 2, 3, 13, 59, 55) + k * 5000) },
    y: Array.from({ length: 62 }, (_, k) => (k === 10 && s === 0 ? null : 100 + s * 50 + (k % 7))),
  })),
}

test('a live frame: geometry, overscan, rows and ids', () => {
  const { svg, table } = render(live)
  const root = parseXml(svg)
  const [, , , , plot] = /data-hp-plot="(\S+) (\S+) (\S+) (\S+)"/.exec(svg) ?? []
  assert.ok(plot)
  for (const n of /data-hp-plot="([^"]+)"/.exec(svg)[1].split(' ')) assert.match(n, NUMBER)
  assert.equal(root.attrs['data-hp-x'], `time ${live.x.timeDomain[0]} ${live.x.timeDomain[1]}`) // exact, not rounded
  const tips = JSON.parse(text(select(root, 'metadata.hp-tips')[0]))
  assert.equal(tips.mode, 'x')
  assert.equal(tips.rows.length, 61) // 14:00:00 to 14:05:00; the overscan point has no row
  assert.equal(tips.rows[0].k, '2026-03-03T14:00:00Z')
  assert.deepEqual(tips.rows[9].v.map((e) => e[2]), ['–', '153 ms']) // row 9 is 14:00:45
  assert.ok(tips.xs.every((x, j) => j === 0 || x >= tips.xs[j - 1]), 'xs non-decreasing')
  const t = parseXml(table)
  assert.equal(select(t, 'tbody > tr').length, 61)
  assert.equal(select(t, 'td > span.hp-sr').length, 1)
  const ids = elements(root).map((el) => el.attrs.id).filter(Boolean)
  assert.equal(new Set(ids).size, ids.length)
})

test('same input, same bytes; a moved window keeps the ids and the plot', () => {
  assert.equal(render(live).svg, render(structuredClone(live)).svg)
  const later = { ...live, x: { timeDomain: live.x.timeDomain.map((t) => t + 5000) } }
  const a = parseXml(render(live).svg)
  const b = parseXml(render(later).svg)
  assert.deepEqual(select(a, 'g.hp-series').map((g) => g.attrs.id), select(b, 'g.hp-series').map((g) => g.attrs.id))
  assert.equal(a.attrs['data-hp-plot'], b.attrs['data-hp-plot'])
})

test('lone surrogates never break a frame', () => {
  const name = 'p' + String.fromCharCode(0xd800) + '50'
  const { svg, table } = render({ ...live, marks: [{ ...live.marks[0], name }] })
  parseXml(svg)
  parseXml(table)
  assert.ok(Buffer.from(svg).toString() === svg, 'valid UTF-8')
})

test('bars: ids, native titles and mode band', () => {
  const chart = {
    id: 'requests', title: 'Requests', width: 400, height: 200,
    marks: ['a', 'b'].map((name, s) => ({ type: 'bar', name, x: { categories: ['14:01', '14:02'] }, y: [10 + s, s ? null : 3] })),
  }
  const root = parseXml(render(chart).svg)
  assert.equal(root.attrs['data-hp-x'], 'band')
  assert.equal(select(root, 'path.hp-bar').length, 3)
  assert.equal(select(root, 'path.hp-bar > title').length, 3)
  assert.equal(select(root, 'rect.hp-band-hl[visibility="hidden"]').length, 1)
  assert.equal(JSON.parse(text(select(root, 'metadata.hp-tips')[0])).mode, 'band')
})

test('the narrowest chart (hp-w=50) still has a plot rectangle', () => {
  // Spec §4.6 clamps hp-w to 50 px; §4.2 wants left < right, and every
  // hover x inside the plot, at any width.
  const bars = {
    id: 'requests', title: 'Requests', width: 50, height: 200,
    marks: ['a', 'b'].map((name, s) => ({ type: 'bar', name, x: { categories: ['14:01', '14:02'] }, y: [1000 + s, 2500] })),
  }
  for (const chart of [bars, { ...live, width: 50 }]) {
    const root = parseXml(render(chart).svg)
    const [L, , R] = root.attrs['data-hp-plot'].split(' ').map(Number)
    assert.ok(L < R, `plot ${root.attrs['data-hp-plot']}`)
    for (const x of JSON.parse(text(select(root, 'metadata.hp-tips')[0])).xs) {
      if (chart === bars) assert.ok(x >= L && x <= R, `x ${x} outside ${L}..${R}`)
    }
  }
})

test('reference files parse with this test parser', () => {
  // The parser itself is checked against bytes the Go reference wrote.
  for (const f of ['line-time/chart.svg', 'line-time/table.html', 'bars-band/chart.svg', 'escaping/chart.svg', 'slide/frame-b.svg']) {
    parseXml(readFileSync(new URL(`../../spec/conformance/${f}`, import.meta.url), 'utf8'))
  }
  assert.throws(() => parseXml('<svg><g></svg>'))
  assert.throws(() => parseXml('<svg a="1" a="2"/>'))
  assert.throws(() => parseXml('<svg>a & b</svg>'))
  assert.throws(() => parseXml('<svg>' + String.fromCharCode(1) + '</svg>'))
})
