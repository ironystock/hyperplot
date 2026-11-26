// Tests for hx-plot-brush.js beyond the conformance fixtures: how pointer
// and keyboard brushes start, end, survive swaps and get cancelled, and the
// announcements of a keyboard range. They run the real scripts against the
// fake browser in fakedom.mjs, on the line-time reference chart (plot
// 24..228 × 8..98, x 14:00 to 14:20 UTC, rows at 14:00, 14:10 and 14:20)
// drawn 1:1. Run with: node --test
import { test } from 'node:test'
import assert from 'node:assert/strict'
import { readFileSync } from 'node:fs'
import { load, Matrix } from './fakedom.mjs'

const XML = readFileSync(new URL('../testdata/conformance/line-time/chart.svg', import.meta.url), 'utf8')
const BANDS = readFileSync(new URL('../testdata/conformance/bars-band/chart.svg', import.meta.url), 'utf8')
const plain = (v) => JSON.parse(JSON.stringify(v))

function page({ brush = true, htmx = true, config = {}, companions = ['brush'] } = {}) {
  const env = load({ htmx, config, companions })
  const wrap = env.el('div', brush ? { 'hx-plot:brush': '' } : {}, env.doc.body)
  const svg = env.parse(XML, wrap)
  svg.ctm = new Matrix()
  return { env, svg, wrap }
}

const brushes = (env) => plain(env.events.filter((e) => /brush/.test(e.type)).map((e) => e.detail))
const selects = (env) => env.events.filter((e) => /select/.test(e.type)).length
const rect = (svg) => svg.querySelector('rect.hp-brush')
const press = (env, svg, x, more = {}) =>
  env.fire('pointerdown', { target: svg, clientX: x, clientY: 50, pointerId: 3, pointerType: 'mouse', button: 0, ...more })
const moveTo = (env, svg, x) => env.fire('pointermove', { target: svg, clientX: x, clientY: 50, pointerId: 3, pointerType: 'mouse' })

// swapTo morphs the chart to new attributes and content.
function swapTo(env, wrap, svg, xml) {
  const task = { type: 'main', target: wrap, swapSpec: { style: 'innerMorph' } }
  env.ext.htmx_before_swap(wrap, { ctx: {}, tasks: [task] })
  const next = env.parse(xml)
  for (const k of [...svg.attrs.keys()]) svg.removeAttribute(k)
  for (const [k, v] of next.attrs) svg.setAttribute(k, v)
  svg.replaceChildren(...next.children)
  env.ext.htmx_before_settle(wrap, { task })
  env.ext.htmx_after_swap(wrap, { ctx: {} })
}

test('a press captures the pointer, blocks text selection and draws as it moves', () => {
  const { env, svg } = page()
  const e = press(env, svg, 75)
  assert.ok(e.defaultPrevented, 'no text selection, no native drag')
  assert.ok(svg.hasPointerCapture(3))
  assert.equal(svg.style.userSelect, 'none')
  moveTo(env, svg, 177)
  const r = rect(svg)
  assert.deepEqual(['x', 'y', 'width', 'height', 'fill', 'fill-opacity', 'pointer-events', 'aria-hidden'].map((a) => r.getAttribute(a)), [
    '75',
    '8',
    '102',
    '90',
    'currentColor',
    '0.1',
    'none',
    'true',
  ])
  assert.equal(r.parentNode, svg.querySelector('g.hp-hover'))
  env.fire('pointerup', { target: svg, clientX: 177, clientY: 50, pointerId: 3 })
  assert.equal(svg.style.userSelect, undefined, 'restored')
  assert.equal(brushes(env).length, 1)
})

test('less than 4 CSS px of travel is a click, which selects', () => {
  const { env, svg } = page()
  env.drag(svg, 126, 129, 50)
  assert.deepEqual(brushes(env), [])
  assert.equal(rect(svg), null)
  assert.equal(selects(env), 1)
})

test('touch, other buttons, presses off the plot and unbrushable charts do not brush', () => {
  for (const [name, opts, more] of [
    ['touch', {}, { pointerType: 'touch' }],
    ['secondary button', {}, { button: 2 }],
    ['below the plot', {}, { clientY: 110 }],
    ['no hx-plot:brush', { brush: false }, {}],
    ['brush script not loaded', { companions: [] }, {}],
  ]) {
    const { env, svg } = page(opts)
    const e = press(env, svg, 75, more)
    moveTo(env, svg, 177)
    env.fire('pointerup', { target: svg, clientX: 177, clientY: 50, pointerId: 3 })
    assert.equal(e.defaultPrevented, false, name)
    assert.deepEqual(brushes(env), [], name)
    assert.equal(rect(svg), null, name)
  }
  // A band axis cannot be brushed.
  const env = load({ companions: ['brush'] })
  const svg = env.parse(BANDS, env.el('div', { 'hx-plot:brush': '' }, env.doc.body))
  svg.ctm = new Matrix()
  assert.equal(press(env, svg, 60).defaultPrevented, false, 'band')
})

test('lost capture or a cancelled pointer ends the drag without a brush', () => {
  for (const type of ['lostpointercapture', 'pointercancel']) {
    const { env, svg } = page()
    press(env, svg, 75)
    moveTo(env, svg, 177)
    env.fire(type, { target: svg, pointerId: 3 })
    assert.equal(rect(svg), null, type)
    env.fire('pointerup', { target: svg, clientX: 177, clientY: 50, pointerId: 3 })
    assert.deepEqual(brushes(env), [], type)
  }
})

test('a drag cancelled by Escape, or by a swap to an unbrushable frame, selects nothing on release', () => {
  for (const name of ['Escape', 'swap']) {
    const { env, svg, wrap } = page()
    env.drag(svg, 75, 177, 50, {
      between: () => (name === 'Escape' ? env.key('Escape', env.doc.body) : swapTo(env, wrap, svg, BANDS)),
    })
    assert.deepEqual(brushes(env), [], name)
    assert.equal(selects(env), 0, `${name}: the release's click selects nothing`)
    // The next press starts afresh: a plain click selects again.
    env.drag(svg, name === 'Escape' ? 126 : 60, name === 'Escape' ? 126 : 60, 50)
    assert.equal(selects(env), 1, `${name}: the next click selects`)
  }
})

test('a drag that stays beside the plot is no brush (x0 = x1) but a click', () => {
  for (const [a, b, x] of [
    [232, 250, '2026-03-03T14:20:00Z'],
    [232, 300, '2026-03-03T14:20:00Z'],
    [10, 20, '2026-03-03T14:00:00Z'],
  ]) {
    const { env, svg } = page()
    env.drag(svg, a, b, 50)
    assert.deepEqual(brushes(env), [], `${a}..${b}`)
    assert.equal(rect(svg), null, `${a}..${b}`)
    // In mode x the pointer activates the nearest point anywhere in the
    // plot's vertical span (§6.5), so the click selects the end point.
    const sel = env.events.filter((e) => /select/.test(e.type)).map((e) => e.detail.x)
    assert.deepEqual(sel, [x], `${a}..${b}`)
  }
})

test('a click on a line of a brushable chart names its series, though pointer capture moved the click', () => {
  const { env, svg } = page()
  const line = svg.querySelector('path.hp-line')
  // The press lands on the line; the brush captures the pointer, so the
  // release and the click go to the svg (env.drag sends them there).
  env.drag(line, 126, 127, 50)
  assert.ok(svg.hasPointerCapture(7) === false)
  const sel = plain(env.events.filter((e) => /select/.test(e.type)).map((e) => e.detail))
  assert.deepEqual(sel, [{ chart: 'lt', x: '2026-03-03T14:10:00Z', series: 'p50', index: 1 }])
  // A press off the series names none.
  env.drag(svg, 126, 127, 50)
  assert.equal(plain(env.events.filter((e) => /select/.test(e.type)).at(-1).detail).series, '')
})

test('a keyboard range over metadata with fewer rows than points neither throws nor brushes', () => {
  const { env, svg } = page()
  env.setTips(svg, { mode: 'x', xs: [24, 126, 228], top: 8, bottom: 98, rows: [{ t: 'a', k: 'ka', v: [] }] })
  svg.focus()
  env.key('Home')
  env.key('Shift+ArrowRight')
  env.key('Shift+ArrowRight')
  env.key('Enter')
  assert.deepEqual(brushes(env), [])
  assert.equal(rect(svg), null)
})

test('the rectangle stays until Escape or the next press', () => {
  const { env, svg } = page()
  env.drag(svg, 75, 177, 50)
  assert.ok(rect(svg))
  press(env, svg, 100)
  assert.equal(rect(svg), null, 'next press')
  env.fire('pointerup', { target: svg, clientX: 100, clientY: 50, pointerId: 3 })
  env.drag(svg, 75, 177, 50)
  env.key('Escape', env.doc.body)
  assert.equal(rect(svg), null, 'Escape')
})

test('a finished brush’s rectangle is not redrawn after a swap; one in progress is', () => {
  const { env, svg, wrap } = page()
  env.drag(svg, 75, 177, 50)
  swapTo(env, wrap, svg, XML)
  assert.equal(rect(svg), null)
  press(env, svg, 75)
  moveTo(env, svg, 177)
  swapTo(env, wrap, svg, XML)
  assert.equal(rect(svg).getAttribute('width'), '102')
})

test('a slide leaves a brush being dragged under the pointer; a keyboard range slides with its data', () => {
  const read = (f) => readFileSync(new URL(`../testdata/conformance/slide/${f}`, import.meta.url), 'utf8')
  const [A, B] = [read('frame-a.svg'), read('frame-b.svg')]
  const env = load({ companions: ['brush'] })
  const wrap = env.el('div', { 'hx-plot:brush': '' }, env.doc.body)
  const svg = env.parse(A, wrap)
  svg.ctm = new Matrix()
  press(env, svg, 60)
  moveTo(env, svg, 150)
  env.frame()
  swapTo(env, wrap, svg, B)
  assert.ok(rect(svg), 'the drag is redrawn on the new frame')
  assert.equal(rect(svg).animations.length, 0, 'its dragged edge stays under the pointer')
  assert.equal(Number(rect(svg).getAttribute('x')) + Number(rect(svg).getAttribute('width')), 150)
  env.fire('pointerup', { target: svg, clientX: 150, clientY: 50, pointerId: 3 })
  env.key('Escape')

  svg.focus()
  env.key('Home')
  env.key('Shift+ArrowRight')
  env.clock += 1000
  swapTo(env, wrap, svg, A)
  assert.ok(rect(svg))
  assert.equal(rect(svg).animations.length, 1, 'the range slides')
  assert.equal(svg.querySelector('.hp-crosshair').animations.length, 1, 'so does the keyboard crosshair')
})

test('a swap to a frame that cannot be brushed cancels the drag', () => {
  const { env, svg, wrap } = page()
  press(env, svg, 75)
  moveTo(env, svg, 177)
  swapTo(env, wrap, svg, BANDS)
  assert.equal(rect(svg), null)
  env.fire('pointerup', { target: svg, clientX: 177, clientY: 50, pointerId: 3 })
  assert.deepEqual(brushes(env), [])
})

test('a keyboard range is announced with strings.range, and a plain step ends it', () => {
  const { env, svg } = page({ config: { plot: { strings: { range: 'de {0} à {1}' } } } })
  svg.focus()
  env.key('End')
  const said = env.live.textContent
  env.key('Shift+ArrowLeft')
  assert.match(env.live.textContent, /^de .+ à .+$/)
  assert.notEqual(env.live.textContent, said)
  const [from, to] = env.live.textContent.slice(3).split(' à ')
  assert.ok(from.length && to.length && from !== to, 'lower x first, the two row headings')
  assert.ok(rect(svg))
  env.key('ArrowLeft')
  assert.equal(rect(svg), null)
  assert.doesNotMatch(env.live.textContent, /^de /, 'the row is read again')
  env.key('Enter')
  assert.deepEqual(brushes(env), [], 'Enter selects instead')
  assert.equal(selects(env), 1)
})

test('Shift+Arrow on a chart that cannot be brushed just steps', () => {
  const { env, svg } = page({ brush: false })
  svg.focus()
  env.key('Home')
  env.key('Shift+ArrowRight')
  assert.equal(rect(svg), null)
  assert.match(env.live.textContent, /: /, 'the row is read')
})

test('a keyboard range keeps its anchor across a swap, and ends when the anchor row leaves', () => {
  const { env, svg, wrap } = page()
  svg.focus()
  env.key('Home')
  env.key('Shift+ArrowRight')
  swapTo(env, wrap, svg, XML)
  assert.equal(rect(svg).getAttribute('x'), '24', 'redrawn from the anchor')
  env.key('Shift+ArrowRight')
  env.key('Enter')
  assert.deepEqual(brushes(env), [{ chart: 'lt', x0: '2026-03-03T14:00:00Z', x1: '2026-03-03T14:20:00Z' }])

  // The anchor's row (14:00) slides out of the window: the range ends.
  env.key('Home')
  env.key('Shift+ArrowRight')
  const moved = XML.replace('"k":"2026-03-03T14:00:00Z"', '"k":"2026-03-03T14:30:00Z"')
  swapTo(env, wrap, svg, moved)
  assert.equal(rect(svg), null)
  env.key('Enter')
  assert.equal(brushes(env).length, 1, 'no second brush')
})

test('without htmx a brush is a bubbling CustomEvent', () => {
  const { env, svg, wrap } = page({ htmx: false })
  const got = []
  wrap.addEventListener('htmx:plot:brush', (e) => got.push([e.bubbles, plain(e.detail)]))
  env.drag(svg, 75, 177, 50)
  assert.deepEqual(got, [[true, { chart: 'lt', x0: '2026-03-03T14:05:00Z', x1: '2026-03-03T14:15:00Z' }]])
})

test('brush events take the metaCharacter spelling, and the attribute its spellings', () => {
  const env = load({ config: { metaCharacter: '-' }, companions: ['brush'] })
  const svg = env.parse(XML, env.el('div', { 'data-hx-plot-brush': '' }, env.doc.body))
  svg.ctm = new Matrix()
  env.drag(svg, 75, 177, 50)
  assert.deepEqual(
    env.events.map((e) => e.type),
    ['htmx-plot-brush'],
  )
})
