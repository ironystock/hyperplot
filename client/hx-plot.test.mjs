// Tests for the pure helpers in hx-plot.js and hx-plot-brush.js. Run with:
// node --test
// The scripts export their helpers only when __HYPERPLOT_TEST__ is set, and
// they skip all DOM wiring when there is no document. dom.test.mjs covers
// that wiring against a small fake browser.
import { test } from 'node:test'
import assert from 'node:assert/strict'
import { readFileSync } from 'node:fs'

globalThis.__HYPERPLOT_TEST__ = true
await import('./hx-plot.js')
await import('./hx-plot-brush.js')
await import('./hx-plot-tween.js')
const {
  nearestIndex,
  nearestPoint,
  placeTooltip,
  keyOrder,
  parseTips,
  keepIndex,
  announcement,
  frameOf,
  slideOf,
  startTransform,
  gestureParams,
  timeValue,
  brushValues,
} = globalThis.__hyperplotTest

test('the scripts define no browser globals without a document', () => {
  assert.equal(globalThis.hyperplot, undefined)
})

test('nearestIndex', () => {
  const xs = [0, 10, 20, 30]
  const cases = [
    ['empty', [], 5, -1],
    ['nil', null, 5, -1],
    ['NaN x', xs, NaN, -1],
    ['infinite x', xs, Infinity, -1],
    ['single', [7], -100, 0],
    ['before first', xs, -5, 0],
    ['exact first', xs, 0, 0],
    ['exact middle', xs, 20, 2],
    ['exact last', xs, 30, 3],
    ['after last', xs, 99, 3],
    ['closer to left', xs, 14, 1],
    ['closer to right', xs, 16, 2],
    ['tie goes left', xs, 15, 1],
    ['uneven spacing', [0, 1, 2, 100], 60, 3],
    ['duplicates', [0, 5, 5, 10], 5, 1],
    ['duplicates from the right', [0, 5, 5, 10], 6, 1],
    ['fractional', [0.25, 0.5, 0.75], 0.6, 1],
  ]
  for (const [name, arr, x, want] of cases) {
    assert.equal(nearestIndex(arr, x), want, name)
  }
})

test('nearestIndex agrees with a linear scan', () => {
  const xs = Array.from({ length: 257 }, (_, i) => i * 3 + (i % 7) * 0.5)
  for (let x = -10; x < 800; x += 0.75) {
    let best = 0
    for (let i = 1; i < xs.length; i++) {
      if (Math.abs(xs[i] - x) < Math.abs(xs[best] - x)) best = i
    }
    assert.equal(nearestIndex(xs, x), best, `x=${x}`)
  }
})

test('nearestPoint', () => {
  const pts = [
    [0, 0],
    [10, 0],
    [10, 10],
    [NaN, NaN],
  ]
  const cases = [
    ['empty', [], 0, 0, 12, -1],
    ['nil', null, 0, 0, 12, -1],
    ['exact hit', pts, 10, 10, 12, 2],
    ['nearest of several', pts, 9, 2, 12, 1],
    ['outside radius', pts, 50, 50, 12, -1],
    ['on the radius counts', pts, 0, -12, 12, 0],
    ['just outside radius', pts, 0, -12.01, 12, -1],
    ['tie goes to lower index', pts, 5, 0, 12, 0],
    ['NaN point never matches', [[NaN, NaN]], 0, 0, 1e9, -1],
    ['zero radius exact', pts, 10, 0, 0, 1],
  ]
  for (const [name, arr, x, y, r, want] of cases) {
    assert.equal(nearestPoint(arr, x, y, r), want, name)
  }
})

test('placeTooltip', () => {
  // A 100×40 box in a 800×600 viewport; GAP is 12 and EDGE is 8.
  const cases = [
    ['above right by default', [400, 300], { left: 412, top: 248 }],
    ['flips left at the right edge', [750, 300], { left: 638, top: 248 }],
    ['flips below at the top edge', [400, 30], { left: 412, top: 42 }],
    ['flips both in the top-right corner', [790, 10], { left: 678, top: 22 }],
    ['clamps at the left edge after flipping', [5, 300], { left: 17, top: 248 }],
    ['stays above near the bottom edge', [400, 590], { left: 412, top: 538 }],
  ]
  for (const [name, [ax, ay], want] of cases) {
    assert.deepEqual(placeTooltip(ax, ay, 100, 40, 800, 600), want, name)
  }
})

test('placeTooltip clamps to the bottom edge when neither side fits', () => {
  // 60px tall viewport: above overflows the top, below overflows the bottom.
  assert.deepEqual(placeTooltip(400, 20, 100, 40, 800, 60), { left: 412, top: 12 })
})

test('placeTooltip keeps an oversized box at the top-left edge', () => {
  assert.deepEqual(placeTooltip(100, 100, 2000, 2000, 800, 600), { left: 8, top: 8 })
})

test('placeTooltip always stays inside the viewport when the box fits', () => {
  for (let ax = -50; ax <= 850; ax += 25) {
    for (let ay = -50; ay <= 650; ay += 25) {
      const { left, top } = placeTooltip(ax, ay, 180, 90, 800, 600)
      assert.ok(left >= 8 && left + 180 <= 792, `left ${left} at ${ax},${ay}`)
      assert.ok(top >= 8 && top + 90 <= 592, `top ${top} at ${ax},${ay}`)
    }
  }
})

test('parseTips reads every mode', () => {
  const x = parseTips(
    JSON.stringify({
      mode: 'x',
      xs: [10, 20],
      bw: 0,
      top: 8,
      bottom: 200,
      rows: [
        { t: 'Mar 3, 14:05', k: '2026-03-03T14:05:00Z', v: [[1, 'p50', '12.3 ms'], [2, 'p99', '–']] },
        { t: 'Mar 3, 14:06', v: [[1, 'p50', '11 ms'], [2, 'p99', '40 ms']] },
      ],
    }),
  )
  assert.deepEqual(x, {
    mode: 'x',
    xs: [10, 20],
    pts: [],
    ord: [0, 1],
    bw: 0,
    top: 8,
    bottom: 200,
    rows: [
      { t: 'Mar 3, 14:05', k: '2026-03-03T14:05:00Z', v: [[1, 'p50', '12.3 ms'], [2, 'p99', '–']] },
      { t: 'Mar 3, 14:06', k: null, v: [[1, 'p50', '11 ms'], [2, 'p99', '40 ms']] },
    ],
  })

  const band = parseTips('{"mode":"band","xs":[30,90],"bw":60,"top":0,"bottom":100,"rows":[]}')
  assert.equal(band.mode, 'band')
  assert.equal(band.bw, 60)

  const xy = parseTips('{"mode":"xy","pts":[[1,2],[3,4]],"rows":[{"t":"a","v":[[3,"s","5"]]}]}')
  assert.deepEqual(xy.pts, [
    [1, 2],
    [3, 4],
  ])
  assert.equal(xy.top, null)
  assert.equal(xy.bottom, null)
  assert.deepEqual(xy.rows, [{ t: 'a', k: null, v: [[3, 's', '5']] }])
  assert.deepEqual(band.ord, [0, 1])
  assert.deepEqual(parseTips('{"mode":"xy","pts":[[3,4],[1,2]]}').ord, [1, 0], 'xy points in keyOrder')
})

test('keyOrder', () => {
  const cases = [
    ['empty', [], []],
    ['already in order', [[1, 5], [2, 5], [3, 5]], [0, 1, 2]],
    ['series by series', [[30, 5], [10, 8], [20, 4], [10, 2]], [1, 3, 2, 0]],
    ['same x: bottom (larger y) first', [[0, 2], [0, 9], [0, 5]], [1, 2, 0]],
    ['identical points keep index order', [[4, 4], [1, 1], [4, 4]], [1, 0, 2]],
    ['NaN points are left out', [[NaN, 1], [2, 2], [1, NaN], [1, 1]], [3, 1]],
    ['negative coordinates', [[-5, 0], [-10, 0]], [1, 0]],
  ]
  for (const [name, pts, want] of cases) {
    assert.deepEqual(keyOrder(pts), want, name)
  }
})

test('parseTips rejects unusable payloads without throwing', () => {
  const bad = [
    ['empty string', ''],
    ['not JSON', '{mode: x}'],
    ['truncated', '{"mode":"x","xs":[1,2'],
    ['null', 'null'],
    ['number', '42'],
    ['string', '"x"'],
    ['array', '[1,2]'],
    ['no mode', '{"xs":[1]}'],
    ['unknown mode', '{"mode":"pie","xs":[1]}'],
    ['mode not a string', '{"mode":1}'],
    ['non-numeric x', '{"mode":"x","xs":[1,"2"]}'],
    ['null x', '{"mode":"x","xs":[1,null]}'],
    ['undefined text', undefined],
  ]
  for (const [name, text] of bad) {
    assert.equal(parseTips(text), null, name)
  }
})

test('parseTips defaults and repairs optional fields', () => {
  const t = parseTips(
    JSON.stringify({
      mode: 'x',
      xs: 'nope',
      bw: -3,
      top: 'a',
      bottom: 9,
      pts: [[1, 2], 'bad', [1], [1, 'y']],
      rows: [null, 7, { t: 5, k: 7, v: 'no' }, { v: [[0, 'a', 1], [9, null, '2'], [2.5, 'b', 'c'], 'skip', [4]] }],
      extra: { ignored: true },
    }),
  )
  assert.deepEqual(t.xs, [])
  assert.equal(t.bw, 0)
  assert.equal(t.top, null, 'top needs both bounds')
  assert.equal(t.bottom, null)
  assert.equal(t.pts.length, 4, 'points keep their row alignment')
  assert.deepEqual(t.pts[0], [1, 2])
  for (const p of t.pts.slice(1)) assert.ok(Number.isNaN(p[0]) || Number.isNaN(p[1]), `${p} has a NaN`)
  assert.equal(nearestPoint(t.pts.slice(1), 1, 2, 1e9), -1, 'malformed points never match')
  assert.deepEqual(t.rows, [
    { t: '', k: null, v: [] },
    { t: '', k: null, v: [] },
    { t: '5', k: '7', v: [] },
    {
      t: '',
      k: null,
      v: [
        [0, 'a', '1'],
        [0, '', '2'],
        [0, 'b', 'c'],
        [4, '', ''],
      ],
    },
  ])
  assert.equal('extra' in t, false)
})

test('parseTips never throws on parsed objects that cannot become text', () => {
  // JSON.parse makes toString an own, non-callable property, so String()
  // on such an object throws; the fields become empty instead.
  const evil = '{"toString":1}'
  for (const [name, row] of [
    ['heading', `{"t":${evil}}`],
    ['key', `{"t":"a","k":${evil}}`],
    ['value', `{"t":"a","v":[[1,${evil},${evil}]]}`],
  ]) {
    const t = parseTips(`{"mode":"x","xs":[1],"rows":[${row}]}`)
    assert.ok(t, name)
    assert.equal(typeof t.rows[0].t, 'string', name)
    assert.ok(t.rows[0].k === null || typeof t.rows[0].k === 'string', name)
    for (const v of t.rows[0].v) assert.deepEqual([typeof v[1], typeof v[2]], ['string', 'string'], name)
  }
  const t = parseTips(`{"mode":"x","xs":[1],"rows":[{"t":${evil},"k":${evil},"v":[[1,${evil},"2"]]}]}`)
  assert.deepEqual(JSON.parse(JSON.stringify(t.rows)), [{ t: '', k: null, v: [[1, '', '2']] }])
  // Objects and arrays are no text; numbers and booleans are.
  assert.deepEqual(
    JSON.parse(JSON.stringify(parseTips('{"mode":"x","xs":[1],"rows":[{"t":{"a":1},"k":true,"v":[[1,[1],false]]}]}').rows)),
    [{ t: '', k: null, v: [[1, '', 'false']] }],
  )
})

test('parseTips orders top and bottom', () => {
  const t = parseTips('{"mode":"band","xs":[1],"top":200,"bottom":10}')
  assert.equal(t.top, 10)
  assert.equal(t.bottom, 200)
})

test('parseTips keeps markup as plain text', () => {
  const t = parseTips(JSON.stringify({ mode: 'x', xs: [1], rows: [{ t: '<img src=x onerror=alert(1)>', v: [[1, '<b>', '&amp;']] }] }))
  assert.equal(t.rows[0].t, '<img src=x onerror=alert(1)>')
  assert.deepEqual(t.rows[0].v[0], [1, '<b>', '&amp;'])
})

test('keepIndex', () => {
  const rows = ['a', 'b', 'c', 'b', ''].map((t) => ({ t, v: [] }))
  const cases = [
    ['same place', 'a', 0, 5, 0],
    ['moved left', 'c', 3, 5, 2],
    ['duplicate nearest the old index', 'b', 4, 5, 3],
    ['duplicate tie goes left', 'b', 2, 5, 1],
    ['gone: clamped index', 'z', 9, 5, 4],
    ['gone: same index', 'z', 2, 5, 2],
    ['no key', undefined, 2, 5, 2],
    ['empty key never matches', '', 0, 5, 0],
    ['rows beyond n are ignored', 'b', 3, 2, 1],
    ['no points', 'a', 0, 0, -1],
  ]
  for (const [name, key, i, n, want] of cases) {
    assert.equal(keepIndex(rows, key, i, n), want, name)
  }
})

test('announcement', () => {
  const cases = [
    ['heading and series', { t: 'Mar 3', v: [[1, 'p50', '12 ms'], [2, 'p99', '40 ms']] }, 'Mar 3: p50 12 ms, p99 40 ms'],
    ['no heading', { t: '', v: [[1, 'p50', '12 ms']] }, 'p50 12 ms'],
    ['heading only', { t: 'Mar 3', v: [] }, 'Mar 3'],
    ['unnamed value', { t: 'a', v: [[0, '', '5']] }, 'a: 5'],
    ['empty entries are skipped', { t: 'a', v: [[0, '', ''], [1, 'n', '']] }, 'a: n'],
    ['empty row', { t: '', v: [] }, ''],
    ['missing value is spoken', { t: '2 PM', v: [[1, 'p50', '12 ms'], [2, 'p99', '–']] }, '2 PM: p50 12 ms, p99 no data'],
  ]
  for (const [name, row, want] of cases) {
    assert.equal(announcement(row), want, name)
  }
  // strings.noData translates the spoken en dash.
  assert.equal(announcement({ t: '14 h', v: [[1, 'p99', '–']] }, 'pas de données'), '14 h: p99 pas de données')
})

test('gestureParams', () => {
  const sel = { chart: 'bb', x: 'Q1', series: '2026', index: 0 }
  const cases = [
    ['select', { type: 'htmx:plot:select', detail: sel }, undefined, [['hp-chart', 'bb'], ['hp-x', 'Q1'], ['hp-series', '2026']]],
    ['select, no series', { type: 'htmx:plot:select', detail: { ...sel, series: '' } }, undefined, [['hp-chart', 'bb'], ['hp-x', 'Q1']]],
    ['select, empty key', { type: 'htmx:plot:select', detail: { chart: 'c', x: '', series: '' } }, undefined, [['hp-chart', 'c']]],
    ['brush', { type: 'htmx:plot:brush', detail: { chart: 'lt', x0: '1', x1: '2' } }, undefined, [['hp-chart', 'lt'], ['hp-x0', '1'], ['hp-x1', '2']]],
    ['metaCharacter', { type: 'htmx-plot-select', detail: sel }, '-', [['hp-chart', 'bb'], ['hp-x', 'Q1'], ['hp-series', '2026']]],
    ['unadjusted name under a metaCharacter', { type: 'htmx:plot:select', detail: sel }, '-', []],
    ['pre-spec name', { type: 'plot:select', detail: sel }, undefined, []],
    ['resize', { type: 'htmx:plot:resize', detail: { width: 5 } }, undefined, []],
    ['click', { type: 'click', detail: 1 }, undefined, []],
    ['no event', undefined, undefined, []],
    ['no detail', { type: 'htmx:plot:brush' }, undefined, []],
  ]
  for (const [name, ev, mc, want] of cases) assert.deepEqual(gestureParams(ev, mc), want, name)
})

test('timeValue writes the canonical time value', () => {
  const cases = [
    [Date.UTC(2026, 2, 3, 14, 5), '2026-03-03T14:05:00Z'],
    [Date.UTC(2026, 2, 3, 14, 7, 27, 59), '2026-03-03T14:07:27.059Z'],
    [Date.UTC(2026, 2, 3, 14, 15, 0, 250), '2026-03-03T14:15:00.25Z'],
    [Date.UTC(2026, 2, 3, 14, 15, 10, 200), '2026-03-03T14:15:10.2Z'],
    [Date.UTC(2026, 2, 3, 14, 10, 10), '2026-03-03T14:10:10Z'],
    [0, '1970-01-01T00:00:00Z'],
  ]
  for (const [ms, want] of cases) assert.equal(timeValue(ms), want, want)
})

test('brushValues inverts, orders and rounds by the axis kind', () => {
  const time = frameOf((k) => ({ 'data-hp': '1', 'data-hp-plot': '24 8 228 98', 'data-hp-x': 'time 1772546400000 1772547600000' })[k] ?? null)
  assert.deepEqual(brushValues(time, 75, 177), ['2026-03-03T14:05:00Z', '2026-03-03T14:15:00Z'])
  assert.deepEqual(brushValues(time, 177, 75), ['2026-03-03T14:05:00Z', '2026-03-03T14:15:00Z'], 'ordered')
  assert.deepEqual(brushValues(time, 100, 300), ['2026-03-03T14:07:27.059Z', '2026-03-03T14:20:00Z'], 'whole ms; clamped')
  const lin = frameOf((k) => ({ 'data-hp': '1', 'data-hp-plot': '24 26 228 98', 'data-hp-x': 'linear 1 3' })[k] ?? null)
  assert.deepEqual(brushValues(lin, 75, 100), ['1.5', '1.7451'], 'a tenth of a pixel needs 4 decimals')
  assert.deepEqual(brushValues(lin, 0, 228), ['1', '3'])
  const big = frameOf((k) => ({ 'data-hp': '1', 'data-hp-plot': '0 0 100 10', 'data-hp-x': 'linear 0 1e9' })[k] ?? null)
  assert.deepEqual(brushValues(big, 33.333, 50), ['333330000', '500000000'], 'no decimals for a wide span')
  const tiny = frameOf((k) => ({ 'data-hp': '1', 'data-hp-plot': '0 0 100 10', 'data-hp-x': 'linear 0 1e-20' })[k] ?? null)
  assert.deepEqual(brushValues(tiny, 50, 100), ['0', '0'], 'at most 15 decimals')
})

test('keepIndex follows the row key k before the heading', () => {
  // Two rows read "2:00 PM" (different days); the key tells them apart.
  const rows = [
    { t: '2:00 PM', k: '2026-03-02T14:00:00Z', v: [] },
    { t: '2:00 PM', k: '2026-03-03T14:00:00Z', v: [] },
    { t: '2:10 PM', k: '2026-03-03T14:10:00Z', v: [] },
  ]
  assert.equal(keepIndex(rows, '2026-03-03T14:00:00Z', 0, 3), 1)
  assert.equal(keepIndex(rows, '2:00 PM', 2, 3), 2, 'a heading is not a key when rows have keys')
})

// ---- Sliding windows: the slide fixture (testdata/conformance/slide). ----

const FIXTURES = new URL('../testdata/conformance/', import.meta.url)
const slideChecks = JSON.parse(readFileSync(new URL('slide/checks.json', FIXTURES), 'utf8'))

// rootAttrs reads the attributes of a reference file's root <svg>.
function rootAttrs(file) {
  const svg = readFileSync(new URL(file, new URL('slide/', FIXTURES)), 'utf8')
  const tag = /^<svg\b([^>]*)>/.exec(svg)[1]
  return Object.fromEntries([...tag.matchAll(/([\w:-]+)="([^"]*)"/g)].map((m) => [m[1], m[2]]))
}

function fixtureFrame(name) {
  const c = slideChecks.client.frames[name]
  const attrs = c ? (c.file ? rootAttrs(c.file) : c) : rootAttrs(slideChecks.reference[name])
  return frameOf((k) => attrs[k] ?? null)
}

test('slideOf decides every case of the slide fixture', () => {
  const tol = slideChecks.client.tolerance
  for (const c of slideChecks.client.slide) {
    const name = `${c.from} → ${c.to}${c.config ? ' ' + JSON.stringify(c.config) : ''}${c.scale ? ' at scale ' + c.scale : ''}`
    const r = slideOf(fixtureFrame(c.from), fixtureFrame(c.to), { ...c.config, scale: c.scale ?? 1 })
    assert.equal(r.outcome, c.result, name)
    if (c.reason) assert.equal(r.reason, c.reason, name)
    for (const k of ['s', 'a', 'b']) {
      if (c[k] != null && c.result === 'slide') assert.ok(Math.abs(r[k] - c[k]) <= tol, `${name}: ${k} ${r[k]} != ${c[k]}`)
    }
    if (c.result === 'slide') assert.equal(r.duration, 350, name)
  }
})

test('slideOf shortens the slide for fast streams and snaps under 60 ms', () => {
  const a = fixtureFrame('frame-a')
  const b = fixtureFrame('frame-b')
  assert.equal(slideOf(a, b, { interval: 1000 }).duration, 350)
  assert.equal(slideOf(a, b, { interval: 200 }).duration, 160)
  assert.deepEqual([slideOf(a, b, { interval: 50 }).outcome, slideOf(a, b, { interval: 50 }).reason], ['snap', 'fast'])
  assert.equal(slideOf(a, b, { motion: false }).reason, 'static', 'reduced motion or a view transition')
})

test('frameOf rejects frames that cannot slide', () => {
  const base = { 'data-hp': '1', class: 'hp hp-anim', viewBox: '0 0 240 120', 'data-hp-plot': '24 8 228 98', 'data-hp-x': 'time 0 1000' }
  const ok = (over) => frameOf((k) => ({ ...base, ...over })[k] ?? null).ok
  assert.equal(ok({}), true)
  for (const [name, over] of [
    ['pre-spec', { 'data-hp': '' }],
    ['unknown version', { 'data-hp': '2' }],
    ['band', { 'data-hp-x': 'band' }],
    ['unknown kind', { 'data-hp-x': 'log 1 10' }],
    ['no x', { 'data-hp-x': null }],
    ['reversed domain', { 'data-hp-x': 'time 1000 0' }],
    ['degenerate domain', { 'data-hp-x': 'linear 5 5' }],
    ['non-numeric domain', { 'data-hp-x': 'linear a 5' }],
    ['three plot numbers', { 'data-hp-plot': '24 8 228' }],
    ['no plot', { 'data-hp-plot': null }],
    ['reversed plot', { 'data-hp-plot': '228 8 24 98' }],
  ]) {
    assert.equal(ok(over), false, name)
  }
})

test('startTransform squeezes paths only', () => {
  const { anchored } = slideChecks.client
  const [path, tick] = anchored.effects
  const nums = (s) => s.match(/-?[\d.]+/g).map(Number)
  const { a, b } = slideChecks.client.slide[0]
  const got = startTransform('path', NaN, a, b)
  assert.ok(nums(got).every((v, i) => Math.abs(v - nums(path.transform)[i]) < 1e-5), `${got} vs ${path.transform}`)
  assert.match(got, /^translateX\([\d.]+px\) scaleX\([\d.]+\)$/)
  const text = startTransform('text', anchored.positions[0].to, a, b)
  assert.match(text, /^translateX\([\d.]+px\)$/)
  assert.ok(Math.abs(nums(text)[0] - nums(tick.transform)[0]) < 1e-3, `${text} vs ${tick.transform}`)
  for (const p of anchored.positions) {
    const [dx] = nums(startTransform('text', p.to, a, b))
    assert.ok(Math.abs(p.to + dx - p.from) <= anchored.positionTolerance, `${p.text}: ${p.to + dx} != ${p.from}`)
  }
  assert.equal(startTransform('circle', NaN, 1, 5), 'translateX(5px)', 'no x: translate by b')
})
