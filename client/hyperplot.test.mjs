// Tests for the pure helpers in hyperplot.js. Run with: node --test
// The script exports its helpers only when __HYPERPLOT_TEST__ is set, and it
// skips all DOM wiring when there is no document. dom.test.mjs covers that
// wiring against a small fake browser.
import { test } from 'node:test'
import assert from 'node:assert/strict'

globalThis.__HYPERPLOT_TEST__ = true
await import('./hyperplot.js')
const { nearestIndex, nearestPoint, placeTooltip, keyOrder, parseTips, keepIndex, announcement } = globalThis.__hyperplotTest

test('the script defines no browser globals without a document', () => {
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
        { t: 'Mar 3, 14:05', v: [[1, 'p50', '12.3 ms'], [2, 'p99', '–']] },
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
      { t: 'Mar 3, 14:05', v: [[1, 'p50', '12.3 ms'], [2, 'p99', '–']] },
      { t: 'Mar 3, 14:06', v: [[1, 'p50', '11 ms'], [2, 'p99', '40 ms']] },
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
  assert.deepEqual(xy.rows, [{ t: 'a', v: [[3, 's', '5']] }])
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
      rows: [null, 7, { t: 5, v: 'no' }, { v: [[0, 'a', 1], [9, null, '2'], [2.5, 'b', 'c'], 'skip', [4]] }],
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
    { t: '', v: [] },
    { t: '', v: [] },
    { t: '5', v: [] },
    {
      t: '',
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
})
