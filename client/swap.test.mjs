// Tests for what the client does around htmx swaps (spec/spec.md §6.8):
// sliding windows, snaps, live tables that follow their end, and <details>
// that stay open. They drive the extension's swap hooks the way htmx 4 calls
// them (before_swap, the morph, before_settle, after_swap) against the fake
// browser in fakedom.mjs. Run with: node --test
import { test } from 'node:test'
import assert from 'node:assert/strict'
import { load, Matrix } from './fakedom.mjs'

// The slide fixture's first two frames (spec/conformance/slide): the
// window moves 5 of its 30 minutes, and the plot's right edge moves too.
const A = { plot: '28.82 8 216.76 98', x: 'time 1772546400000 1772548200000' }
const B = { plot: '28.82 8 228 98', x: 'time 1772546700000 1772548500000' }
const AB = { a: 0.94356863, b: 32.94968538 }

const near = (got, want, what, tol = 1e-5) => assert.ok(Math.abs(got - want) <= tol, `${what}: ${got} != ${want}`)

// transformOf parses translateX(…px) [scaleX(…)] into [b, a].
function transformOf(s) {
  const m = /^translateX\((-?[\d.e+-]+)px\)(?: scaleX\((-?[\d.e+-]+)\))?$/.exec(s)
  assert.ok(m, `transform ${s}`)
  return [Number(m[1]), m[2] == null ? 1 : Number(m[2])]
}

// liveChart builds a spec-1 chart frame with one series (a line and a dot),
// an end dot, two x tick labels and the hover group, inside parent.
function liveChart(env, { id = 'sl', frame = A, cls = 'hp hp-anim', parent = env.doc.body, ticks = [95.21, 194.8] } = {}) {
  const svg = env.el('svg', { id, class: cls, viewBox: '0 0 240 120', 'data-hp': '1', 'data-hp-y': 'linear 0 100' }, parent)
  setFrame(svg, frame)
  svg.ctm = new Matrix(1, 0, 0, 1, 0, 0)
  const marks = env.el('g', { class: 'hp-marks', 'clip-path': `url(#${id}-clip)` }, svg)
  const series = env.el('g', { id: `${id}-s-1`, class: 'hp-series hp-s1' }, marks)
  env.el('path', { class: 'hp-line', d: 'M28.82,50L228,40' }, series)
  env.el('circle', { class: 'hp-dot', cx: '100', cy: '50', r: '4' }, series)
  const axis = env.el('g', { class: 'hp-axis hp-axis-x' }, svg)
  env.el('line', { class: 'hp-baseline', x1: '28.82', x2: '228' }, axis)
  for (const x of ticks) env.el('text', { class: 'hp-tick', x: String(x) }, axis)
  const labels = env.el('g', { class: 'hp-labels' }, svg)
  env.el('circle', { class: 'hp-end', cx: '228', cy: '40' }, labels)
  const hover = env.el('g', { class: 'hp-hover' }, svg)
  env.el('line', { class: 'hp-crosshair', visibility: 'hidden' }, hover)
  env.el('metadata', { class: 'hp-tips' }, hover).textContent = JSON.stringify({
    mode: 'x',
    xs: [28.82, 228],
    top: 8,
    bottom: 98,
    rows: [
      { t: '2:05 PM', k: '2026-03-03T14:05:00Z', v: [[1, 'p99', '10']] },
      { t: '2:35 PM', k: '2026-03-03T14:35:00Z', v: [[1, 'p99', '70']] },
    ],
  })
  return svg
}

function setFrame(svg, f) {
  svg.setAttribute('data-hp-plot', f.plot)
  svg.setAttribute('data-hp-x', f.x)
}

// swap runs one htmx swap of target: before_swap, then morph (a function
// that changes the DOM like htmx's morph would), before_settle, after_swap.
function swap(env, target, morph, { ctx = {}, spec = { style: 'innerMorph' }, type = 'main' } = {}) {
  const task = { type, target, swapSpec: spec }
  env.ext.htmx_before_swap(target, { ctx, tasks: [task] })
  morph?.()
  env.ext.htmx_before_settle(target, { task, newContent: [], settleTasks: [] })
  env.ext.htmx_after_swap(target, { ctx })
  return task
}

const marksOf = (svg) => svg.querySelectorAll('path,circle,rect,line,text')
const animated = (env) => env.doc.animations.filter((a) => a.playState === 'running')

test('a moved x window slides the anchored elements from where their data was', async () => {
  const env = load()
  const wrap = env.el('div', {}, env.doc.body)
  const svg = liveChart(env, { parent: wrap })
  swap(env, wrap, () => setFrame(svg, B))

  for (const el of marksOf(svg)) assert.equal(el.style.transition, 'none', `${el.localName} keeps no CSS transition`)
  const byEl = new Map(env.doc.animations.map((a) => [a.el, a]))
  const path = svg.querySelector('path.hp-line')
  const [b, a] = transformOf(byEl.get(path).keyframes[0].transform)
  near(b, AB.b, 'path b')
  near(a, AB.a, 'path a')
  assert.equal(byEl.get(path).keyframes[1].transform, 'none')
  assert.deepEqual({ ...byEl.get(path).options }, { duration: 350, easing: 'ease-out' })

  // Dots and labels only move, by (a - 1)·x + b, so they are not squeezed.
  const dot = byEl.get(svg.querySelector('circle.hp-dot'))
  assert.deepEqual(transformOf(dot.keyframes[0].transform).slice(1), [1])
  near(transformOf(dot.keyframes[0].transform)[0], (AB.a - 1) * 100 + AB.b, 'dot')
  const tick = byEl.get(svg.querySelector('text.hp-tick'))
  // The fixture: tick "2:15 PM" goes from x 122.79 in frame A to 95.21.
  near(95.21 + transformOf(tick.keyframes[0].transform)[0], 122.79, 'tick start position', 0.01)
  for (const el of svg.querySelectorAll('circle.hp-end,line.hp-baseline,line.hp-crosshair')) {
    assert.equal(byEl.has(el), false, `${el.className} is not anchored`)
  }
  assert.equal(env.doc.animations.length, 4, 'line, dot and two ticks')

  // When the slide ends, the inline styles go.
  for (const an of env.doc.animations) an.finish()
  await new Promise((r) => setImmediate(r))
  for (const el of marksOf(svg)) assert.equal(el.hasAttribute('style') || el.style.length > 0, false, el.localName)
})

test('a plain shift moves each series group as one', () => {
  const env = load()
  const wrap = env.el('div', {}, env.doc.body)
  const svg = liveChart(env, { parent: wrap, frame: { plot: '0 8 600 98', x: 'time 0 600000' } })
  swap(env, wrap, () => setFrame(svg, { plot: '0 8 600 98', x: 'time 60000 660000' }))
  const targets = env.doc.animations.map((a) => a.el.localName + '.' + a.el.className.split(' ')[0])
  assert.deepEqual(targets, ['g.hp-series', 'text.hp-tick', 'text.hp-tick'])
  assert.equal(env.doc.animations[0].keyframes[0].transform, 'translateX(60px) scaleX(1)')
  assert.equal(env.doc.animations[1].keyframes[0].transform, 'translateX(60px)')
})

test('an unchanged x window leaves the CSS transitions alone', () => {
  const env = load()
  const wrap = env.el('div', {}, env.doc.body)
  const svg = liveChart(env, { parent: wrap })
  swap(env, wrap, () => svg.querySelector('path').setAttribute('d', 'M28.82,60L228,20'))
  assert.equal(env.doc.animations.length, 0)
  for (const el of marksOf(svg)) assert.equal(el.style.transition, undefined)
})

test('no slide under reduced motion, without hp-anim, or in a view transition', () => {
  const cases = [
    ['reduced motion', { reducedMotion: true }, {}, 'hp hp-anim'],
    ['no hp-anim', {}, {}, 'hp'],
    ['view transition (context)', {}, { ctx: { transition: true } }, 'hp hp-anim'],
    ['view transition (swap)', {}, { spec: { style: 'innerMorph', transition: true } }, 'hp hp-anim'],
  ]
  for (const [name, opts, swapOpts, cls] of cases) {
    const env = load(opts)
    const wrap = env.el('div', {}, env.doc.body)
    const svg = liveChart(env, { parent: wrap, cls })
    swap(env, wrap, () => setFrame(svg, B), swapOpts)
    assert.equal(env.doc.animations.length, 0, name)
    assert.equal(svg.querySelector('path').style.transition, undefined, name)
  }
  // transition:false on the swap wins over a view-transition context.
  const env = load()
  const wrap = env.el('div', {}, env.doc.body)
  const svg = liveChart(env, { parent: wrap })
  swap(env, wrap, () => setFrame(svg, B), { ctx: { transition: true }, spec: { style: 'innerMorph', transition: false } })
  assert.equal(env.doc.animations.length, 4)
})

test('slide:false snaps a moved window: no tween, no slide', () => {
  for (const [name, opts, ctx] of [
    ['htmx.config.plot', { config: { plot: { slide: false } } }, {}],
    ['hx-config', {}, { request: { plot: { slide: false } } }],
  ]) {
    const env = load(opts)
    const wrap = env.el('div', {}, env.doc.body)
    const svg = liveChart(env, { parent: wrap })
    swap(env, wrap, () => setFrame(svg, B), { ctx })
    assert.equal(env.doc.animations.length, 0, name)
    assert.equal(svg.querySelector('path').style.transition, 'none', name)
    // Cleared after the browser has computed the new frame's style.
    env.frame()
    assert.equal(svg.querySelector('path').style.transition, 'none', `${name}: one frame is too early`)
    env.frame()
    assert.equal(svg.querySelector('path').hasAttribute('style'), false, name)
    assert.equal(svg.querySelector('path').style.transition, undefined, name)
  }
})

test('a zoom, a resize and a jump snap instead of tweening the path', () => {
  const zoom = { plot: '28.82 8 225.44 98', x: 'time 1772546400000 1772550000000' }
  const far = { plot: '28.82 8 216.76 98', x: 'time 1772547300000 1772549100000' }
  for (const [name, to, vb] of [
    ['zoom', zoom, null],
    ['far', far, null],
    ['size', B, '0 0 320 120'],
  ]) {
    const env = load()
    const wrap = env.el('div', {}, env.doc.body)
    const svg = liveChart(env, { parent: wrap })
    swap(env, wrap, () => {
      setFrame(svg, to)
      if (vb) svg.setAttribute('viewBox', vb)
    })
    assert.equal(env.doc.animations.length, 0, name)
    assert.equal(svg.querySelector('path').style.transition, 'none', name)
  }
})

test('a fast stream shortens the slide and snaps under 60 ms', () => {
  const env = load()
  const wrap = env.el('div', {}, env.doc.body)
  const svg = liveChart(env, { parent: wrap })
  const frames = [
    B,
    { plot: '28.82 8 228 98', x: 'time 1772546730000 1772548530000' },
    { plot: '28.82 8 228 98', x: 'time 1772546760000 1772548560000' },
  ]
  const durations = []
  for (const [i, t] of [1000, 1200, 1250].entries()) {
    env.clock = t
    const before = env.doc.animations.length
    swap(env, wrap, () => setFrame(svg, frames[i]))
    durations.push(env.doc.animations.slice(before).map((a) => a.options.duration)[0] ?? 'snap')
  }
  // The first swap has no previous one; then 0.8 × 200 ms; then 0.8 × 50 ms.
  assert.deepEqual(durations, [350, 160, 'snap'])
  assert.equal(animated(env).length, 0, 'the snap ended the running slide')
})

test('a new swap ends the running slide at once', () => {
  const env = load()
  const wrap = env.el('div', {}, env.doc.body)
  const svg = liveChart(env, { parent: wrap })
  swap(env, wrap, () => setFrame(svg, B))
  const first = [...env.doc.animations]
  env.clock += 1000
  swap(env, wrap, () => setFrame(svg, { plot: '28.82 8 228 98', x: 'time 1772547000000 1772548800000' }))
  assert.ok(first.every((a) => a.playState === 'idle'), 'cancelled')
  // The plot keeps its width this time, so the series moves as one group.
  assert.equal(animated(env).length, 3, 'the new slide runs')
})

test('only the chart whose window moved is pinned; its sibling keeps its transitions', () => {
  const env = load()
  const wrap = env.el('div', {}, env.doc.body)
  const moving = liveChart(env, { id: 'a', parent: wrap })
  const still = liveChart(env, { id: 'b', parent: wrap })
  swap(env, wrap, () => {
    setFrame(moving, B)
    still.querySelector('path').setAttribute('d', 'M28.82,10L216.76,90')
  })
  assert.ok(env.doc.animations.every((a) => moving.contains(a.el)))
  assert.equal(moving.querySelector('path').style.transition, 'none')
  for (const el of marksOf(still)) assert.equal(el.style.transition, undefined)
})

test('charts that cannot slide are left alone', () => {
  const env = load()
  const wrap = env.el('div', {}, env.doc.body)
  const svg = liveChart(env, { parent: wrap })
  svg.setAttribute('data-hp', '') // pre-spec output
  swap(env, wrap, () => setFrame(svg, B))
  assert.equal(env.doc.animations.length, 0)
  assert.equal(svg.querySelector('path').style.transition, undefined)
  // A 304 runs the hooks with swap style none and changes nothing.
  swap(env, wrap, null, { ctx: { swap: 'none' }, spec: { style: 'none' } })
  assert.equal(env.doc.animations.length, 0)
})

test('the keyboard crosshair slides with its row', () => {
  const env = load()
  const wrap = env.el('div', {}, env.doc.body)
  const svg = liveChart(env, { parent: wrap })
  svg.focus()
  env.key('End') // the newest row, at x 228
  swap(env, wrap, () => setFrame(svg, B))
  const cross = svg.querySelector('.hp-crosshair')
  const a = env.doc.animations.find((an) => an.el === cross)
  assert.ok(a, 'the crosshair moves with the data')
  near(transformOf(a.keyframes[0].transform)[0], (AB.a - 1) * 228 + AB.b, 'crosshair')
})

test('a task copied by another extension after before_swap still slides and follows (hx-targets)', () => {
  // htmx's hx-targets, registered after plot, replaces the main task (whose
  // target is the button) with {...task, target} copies in its own
  // htmx_before_swap.
  const env = load()
  const button = env.el('button', {}, env.doc.body)
  const wrap = env.el('div', { class: 'c' }, env.doc.body)
  const svg = liveChart(env, { parent: wrap })
  const { box } = liveTable(env, wrap)
  box.scrollTop = 2400 - 320
  const ctx = {}
  const task = { type: 'main', target: button, swapSpec: { style: 'innerMorph' } }
  const tasks = [task]
  env.ext.htmx_before_swap(button, { ctx, tasks })
  tasks.splice(0, 1, { ...task, target: wrap })
  setFrame(svg, B)
  box.scrollHeight = 2427
  env.ext.htmx_before_settle(wrap, { task: tasks[0] })
  env.ext.htmx_after_swap(button, { ctx })
  assert.equal(env.doc.animations.length, 4, 'the slide runs')
  assert.equal(box.scrollTop, 2427, 'the table follows')
})

test('a series group with its own clip path or mask slides its children, not itself', () => {
  for (const attr of ['clip-path', 'mask']) {
    const env = load()
    const wrap = env.el('div', {}, env.doc.body)
    const svg = liveChart(env, { parent: wrap, frame: { plot: '0 8 600 98', x: 'time 0 600000' } })
    svg.querySelector('g.hp-series').setAttribute(attr, 'url(#sl-clip)')
    swap(env, wrap, () => setFrame(svg, { plot: '0 8 600 98', x: 'time 60000 660000' }))
    const targets = env.doc.animations.map((a) => a.el.localName)
    assert.deepEqual(targets, ['path', 'circle', 'text', 'text'], attr)
  }
})

test('x tick labels and dot labels nested in groups slide too', () => {
  const env = load()
  const wrap = env.el('div', {}, env.doc.body)
  const svg = liveChart(env, { parent: wrap, ticks: [] })
  const axis = svg.querySelector('g.hp-axis-x')
  for (const x of [95.21, 194.8]) {
    const g = env.el('g', {}, axis)
    env.el('line', { class: 'hp-tick-mark', x1: String(x), x2: String(x) }, g)
    env.el('text', { class: 'hp-tick', x: String(x) }, g)
  }
  env.el('text', { class: 'hp-label hp-dot-label', x: '100' }, env.el('g', {}, svg.querySelector('g.hp-labels')))
  swap(env, wrap, () => setFrame(svg, B))
  const moved = env.doc.animations.map((a) => a.el.className)
  assert.deepEqual(moved.filter((c) => c === 'hp-tick').length, 2, `ticks: ${moved}`)
  assert.ok(moved.includes('hp-label hp-dot-label'), `dot label: ${moved}`)
  assert.ok(!moved.includes('hp-tick-mark'), 'other elements in the tick group stay')
})

test('the active marks are redrawn right after the swap, before a settle delay or view transition ends', () => {
  // A non-morph swap replaces the chart; htmx runs before_settle at once
  // but after_swap only after the settle delay or the view transition.
  for (const transition of [false, true]) {
    const env = load()
    const wrap = env.el('div', {}, env.doc.body)
    let svg = liveChart(env, { parent: wrap })
    svg.focus()
    env.key('End')
    const task = { type: 'main', target: wrap, swapSpec: { style: 'innerHTML', transition } }
    env.ext.htmx_before_swap(wrap, { ctx: {}, tasks: [task] })
    svg.remove()
    svg = liveChart(env, { parent: wrap })
    assert.equal(svg.querySelector('.hp-crosshair').getAttribute('visibility'), 'hidden')
    env.ext.htmx_before_settle(wrap, { task })
    assert.equal(svg.querySelector('.hp-crosshair').getAttribute('visibility'), 'visible', `transition ${transition}`)
    assert.equal(env.doc.activeElement, svg, 'the replacement has focus')
    assert.equal(env.tip.style.display, '')
  }
})

test('a pointer crosshair stays where the pointer is during a slide', () => {
  const env = load()
  const wrap = env.el('div', {}, env.doc.body)
  const svg = liveChart(env, { parent: wrap })
  env.fire('pointermove', { target: svg, clientX: 200, clientY: 50 })
  env.frame()
  swap(env, wrap, () => setFrame(svg, B))
  assert.equal(svg.querySelector('.hp-crosshair').getAttribute('visibility'), 'visible')
  assert.ok(!env.doc.animations.some((a) => a.el.matches('.hp-crosshair')), 'not translated')
})

test('an easing or duration that Element.animate would refuse falls back to the default', () => {
  for (const plot of [{ easing: 'easeOutCubic' }, { easing: 7 }, { duration: -5 }, { duration: 'slow' }]) {
    const env = load({ config: { plot } })
    const wrap = env.el('div', {}, env.doc.body)
    const svg = liveChart(env, { parent: wrap })
    swap(env, wrap, () => setFrame(svg, B))
    assert.deepEqual({ ...env.doc.animations[0].options }, { duration: 350, easing: 'ease-out' }, JSON.stringify(plot))
  }
  const env = load({ config: { plot: { easing: 'cubic-bezier(0.2, 0, 0, 1)', duration: 200 } } })
  const wrap = env.el('div', {}, env.doc.body)
  const svg = liveChart(env, { parent: wrap })
  swap(env, wrap, () => setFrame(svg, B))
  assert.deepEqual({ ...env.doc.animations[0].options }, { duration: 200, easing: 'cubic-bezier(0.2, 0, 0, 1)' })
})

// ---- Tables ----

// liveTable builds <details><div class="table-scroll"><table class="hp-table">.
function liveTable(env, parent, { open = true, id = 'sl-table' } = {}) {
  const details = env.el('details', open ? { open: '' } : {}, parent)
  const box = env.el('div', { class: 'table-scroll' }, details)
  box.overflowY = 'auto'
  box.clientHeight = 320
  box.scrollHeight = 2400
  env.el('table', { class: 'hp-table', id }, box)
  return { details, box }
}

test('a live table scrolled to its end follows new rows', () => {
  const env = load()
  const wrap = env.el('div', {}, env.doc.body)
  const { box } = liveTable(env, wrap)
  box.scrollTop = 2400 - 320 - 1 // within 2 px of the end
  swap(env, wrap, () => {
    box.scrollTop -= 27 // what scroll anchoring does as the morph removes the top row
    box.scrollHeight = 2427
  })
  assert.equal(box.scrollTop, 2427)
})

test('a live table that still fits its scroll box follows once it grows past it', () => {
  const env = load()
  const outer = env.el('div', {}, env.doc.body)
  outer.overflowY = 'auto' // an outer scroller that does not overflow either
  outer.clientHeight = 900
  outer.scrollHeight = 900
  const wrap = env.el('div', {}, outer)
  const { box } = liveTable(env, wrap)
  box.scrollHeight = 300 // two rows: the reader sees every one
  swap(env, wrap, () => (box.scrollHeight = 347))
  assert.equal(box.scrollTop, 347, 'the nearest box, not the outer one')
  assert.equal(outer.scrollTop, 0)
  // An outer scroller that does overflow still wins over a box that fits.
  const env2 = load()
  const page = env2.el('div', {}, env2.doc.body)
  page.overflowY = 'auto'
  page.clientHeight = 300
  page.scrollHeight = 2000
  page.scrollTop = 1700
  const w2 = env2.el('div', {}, page)
  const t2 = liveTable(env2, w2)
  t2.box.scrollHeight = 300
  swap(env2, page, () => (page.scrollHeight = 2047))
  assert.equal(page.scrollTop, 2047)
})

test('a table scrolled elsewhere, a scroll: modifier or follow:false keep their place', () => {
  for (const [name, top, opts] of [
    ['mid-table', 1200, {}],
    ['scroll modifier', 2080, { spec: { style: 'innerMorph', scroll: 'top' } }],
    ['show modifier', 2080, { spec: { style: 'innerMorph', show: 'top' } }],
    ['follow off', 2080, { ctx: { request: { plot: { follow: false } } } }],
  ]) {
    const env = load()
    const wrap = env.el('div', {}, env.doc.body)
    const { box } = liveTable(env, wrap)
    box.scrollTop = top
    swap(env, wrap, () => (box.scrollHeight = 2427), opts)
    assert.equal(box.scrollTop, top, name)
  }
})

test('a table whose scroll box was replaced is found again by its id', () => {
  const env = load()
  const wrap = env.el('div', {}, env.doc.body)
  const { box, details } = liveTable(env, wrap)
  box.scrollTop = 2080
  let fresh
  swap(env, wrap, () => {
    details.remove()
    fresh = liveTable(env, wrap).box
  })
  assert.equal(fresh.scrollTop, 2400)
})

test('a morph keeps the reader’s <details> open or closed', () => {
  const env = load()
  const wrap = env.el('div', {}, env.doc.body)
  const { details } = liveTable(env, wrap)
  const plain = env.el('details', { open: '' }, wrap)
  const hook = (elt, attrName = 'open', newValue = null) => env.ext.htmx_before_morph_attr(elt, { attrName, newValue })

  env.ext.htmx_before_swap(wrap, { ctx: {}, tasks: [{ type: 'main', target: wrap, swapSpec: { style: 'innerMorph' } }] })
  assert.equal(hook(details), false, 'removing open is refused')
  assert.equal(hook(details, 'open', ''), false, 'adding open is refused')
  assert.equal(hook(details, 'class', 'x'), undefined, 'other attributes morph')
  assert.equal(hook(plain), undefined, 'details without a table morph as usual')

  env.ext.htmx_before_swap(wrap, { ctx: { request: { plot: { keepOpen: false } } }, tasks: [] })
  assert.equal(hook(details), undefined, 'keepOpen:false lets the server decide')
})
