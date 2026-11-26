// Tests for hx-plot-tween.js, the JavaScript d tween for browsers whose CSS
// cannot transition d (docs/spec.md §6.8.4). The fake browser has no
// layout, so its feature test finds no CSS d, as Safari would. They drive
// the swap hooks of hx-plot.js the way htmx 4 calls them. Run with:
// node --test
import { test } from 'node:test'
import assert from 'node:assert/strict'
import { load, Matrix } from './fakedom.mjs'

const OLD = 'M0,50L100,20L200,80'
const NEW = 'M0,60L100,40L200,10'

// chart builds a spec-1 chart frame whose window never moves, with one line.
function chart(env, { cls = 'hp hp-anim' } = {}) {
  const wrap = env.el('div', {}, env.doc.body)
  const svg = env.el(
    'svg',
    { id: 'tw', class: cls, viewBox: '0 0 240 120', 'data-hp': '1', 'data-hp-plot': '0 8 200 98', 'data-hp-x': 'linear 0 2', 'data-hp-y': 'linear 0 100' },
    wrap,
  )
  svg.ctm = new Matrix()
  const series = env.el('g', { class: 'hp-series hp-s1' }, env.el('g', { class: 'hp-marks' }, svg))
  const path = env.el('path', { class: 'hp-line', d: OLD }, series)
  env.el('metadata', { class: 'hp-tips' }, env.el('g', { class: 'hp-hover' }, svg)).textContent = '{"mode":"x","xs":[],"rows":[]}'
  return { wrap, svg, path }
}

// swap runs one htmx swap of wrap whose morph sets the path's d.
function swap(env, wrap, morph, ctx = {}) {
  const task = { type: 'main', target: wrap, swapSpec: { style: 'innerMorph' } }
  env.ext.htmx_before_swap(wrap, { ctx, tasks: [task] })
  morph()
  env.ext.htmx_before_settle(wrap, { task })
  env.ext.htmx_after_swap(wrap, { ctx })
}

const clockOf = (env, svg) => env.doc.animations.filter((a) => a.el === svg && a.keyframes == null).at(-1)

test('a value change tweens d from the old path, with the settings’ timing', () => {
  const env = load({ companions: ['tween'], config: { plot: { duration: 500, easing: 'linear' } } })
  const { wrap, svg, path } = chart(env)
  swap(env, wrap, () => path.setAttribute('d', NEW))
  const clock = clockOf(env, svg)
  assert.deepEqual({ ...clock.options }, { duration: 500, easing: 'linear' })
  assert.equal(path.getAttribute('d'), OLD, 'the first frame draws the old path')
  assert.equal(path.style.transition, 'none', 'no CSS transition on d')
  clock.progress = 0.5 // the clock's eased progress
  env.frame()
  assert.equal(path.getAttribute('d'), 'M0,55L100,30L200,45')
  clock.finish()
  env.frame()
  assert.equal(path.getAttribute('d'), NEW, 'ends on the server’s exact d')
  // The transition comes back once the final d has been drawn without one,
  // so that giving it back starts no CSS transition.
  env.frame()
  assert.equal(path.style.transition, 'none', 'one frame is too early')
  env.frame()
  assert.equal(path.hasAttribute('style'), false, 'inline styles removed')
})

test('paths whose commands differ snap', () => {
  const env = load({ companions: ['tween'] })
  const { wrap, svg, path } = chart(env)
  swap(env, wrap, () => path.setAttribute('d', 'M0,60L100,40L200,10L300,0'))
  const clock = clockOf(env, svg)
  assert.equal(path.getAttribute('d'), 'M0,60L100,40L200,10L300,0')
  assert.equal(path.style.transition, 'none')
  clock.finish()
  env.frame()
  env.frame()
  env.frame()
  assert.equal(path.hasAttribute('style'), false)
})

test('a tween that ends late never gives back a path a newer tween holds', () => {
  const env = load({ companions: ['tween'] })
  const { wrap, svg, path } = chart(env)
  swap(env, wrap, () => path.setAttribute('d', NEW))
  clockOf(env, svg).finish()
  env.frame() // the first tween ends; it gives the transition back two frames later
  swap(env, wrap, () => path.setAttribute('d', OLD))
  env.frame()
  env.frame()
  assert.equal(path.style.transition, 'none', 'the second tween still holds it')
})

test('a swap during a tween starts the next one from what is drawn', () => {
  const env = load({ companions: ['tween'] })
  const { wrap, svg, path } = chart(env)
  swap(env, wrap, () => path.setAttribute('d', NEW))
  const first = clockOf(env, svg)
  first.progress = 0.5
  env.frame()
  const mid = path.getAttribute('d')
  swap(env, wrap, () => path.setAttribute('d', OLD))
  assert.equal(first.playState, 'idle', 'the first tween stopped')
  assert.equal(path.getAttribute('d'), mid, 'starts where the first one was')
  clockOf(env, svg).progress = 1
  env.frame()
  assert.equal(path.getAttribute('d'), OLD)
})

test('no tween under reduced motion, without hp-anim, or when the window slides', () => {
  for (const [name, opts, cls, morph] of [
    ['reduced motion', { reducedMotion: true }, 'hp hp-anim', null],
    ['no hp-anim', {}, 'hp', null],
    ['slide', {}, 'hp hp-anim', (svg) => svg.setAttribute('data-hp-x', 'linear 0.1 2.1')],
  ]) {
    const env = load({ companions: ['tween'], ...opts })
    const { wrap, svg, path } = chart(env, { cls })
    swap(env, wrap, () => {
      path.setAttribute('d', NEW)
      morph?.(svg)
    })
    assert.equal(clockOf(env, svg), undefined, name)
    assert.equal(path.getAttribute('d'), NEW, name)
  }
})

test('where CSS can set d it does nothing, unless forced', () => {
  // A browser with CSS d: the feature test's path gets a box.
  const cssD = (env) => {
    const make = env.doc.createElementNS.bind(env.doc)
    env.doc.createElementNS = (ns, tag) => Object.assign(make(ns, tag), { getBBox: () => ({ width: 9 }) })
  }
  const env = load({ companions: ['tween'], before: cssD })
  const { wrap, svg, path } = chart(env)
  swap(env, wrap, () => path.setAttribute('d', NEW))
  assert.equal(clockOf(env, svg), undefined)
  assert.equal(path.style.transition, undefined, 'the stylesheet’s transition plays')

  const env2 = load({ companions: ['tween'], before: cssD })
  env2.window.hyperplot.tween.force = true
  const c = chart(env2)
  swap(env2, c.wrap, () => c.path.setAttribute('d', NEW))
  assert.ok(clockOf(env2, c.svg), 'forced')
  assert.equal(c.path.getAttribute('d'), OLD)
})

test('a swap cancelled after before_swap leaves a running tween to finish', () => {
  // htmx runs the extension's htmx_before_swap before the page's
  // htmx:before:swap listener, which can still cancel the swap.
  const env = load({ companions: ['tween'] })
  const { wrap, svg, path } = chart(env)
  swap(env, wrap, () => path.setAttribute('d', NEW))
  const clock = clockOf(env, svg)
  clock.progress = 0.3
  env.frame()
  env.ext.htmx_before_swap(wrap, { ctx: {}, tasks: [{ type: 'main', target: wrap, swapSpec: { style: 'innerMorph' } }] })
  assert.equal(clock.playState, 'running', 'the tween goes on')
  clock.progress = 1
  clock.finish()
  env.frame()
  assert.equal(path.getAttribute('d'), NEW, 'and ends on the frame it was heading for')
})

test('a tween stops when the next frame slides or cannot tween', () => {
  const env = load({ companions: ['tween'] })
  const { wrap, svg, path } = chart(env)
  swap(env, wrap, () => path.setAttribute('d', NEW))
  const clock = clockOf(env, svg)
  clock.progress = 0.5
  env.frame()
  svg.setAttribute('class', 'hp') // a static frame: no tween, no CSS transition
  swap(env, wrap, () => path.setAttribute('d', OLD))
  assert.equal(clock.playState, 'idle')
  env.frame()
  assert.equal(path.getAttribute('d'), OLD, 'the morph’s d stays')
})

test('without the core started, the companions do nothing', () => {
  const env = load({ companions: ['tween', 'brush'], config: { extensions: 'sse' } })
  assert.equal(env.window.hyperplot.tween, undefined)
  assert.equal(env.window.hyperplot.brush, undefined)
})
