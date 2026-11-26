// Tests for the DOM and htmx wiring in hx-plot.js: registration and the
// extensions allow-list, the extension hooks, the container attributes,
// autosize parameters and events, pointer and keyboard tooltips, select, the
// status region, and state across live updates. They run the real script
// against the small fake browser in fakedom.mjs. Run with: node --test
import { test } from 'node:test'
import assert from 'node:assert/strict'
import { readFileSync } from 'node:fs'
import { load, Matrix } from './fakedom.mjs'

// plain copies a value out of the script's realm, for deepEqual.
const plain = (v) => JSON.parse(JSON.stringify(v))

const read = (name) => readFileSync(new URL(name, import.meta.url), 'utf8')
const PKG = JSON.parse(read('./package.json'))

const row = (t, p50, p99) => ({ t, v: [[1, 'p50', p50], [2, 'p99', p99]] })

// line returns a mode-x payload with one point per heading, 100 units apart.
function line(...heads) {
  return {
    mode: 'x',
    xs: heads.map((_, i) => i * 100),
    top: 0,
    bottom: 100,
    rows: heads.map((h, i) => row(h, `${10 + i} ms`, `${40 + i} ms`)),
  }
}

const TIPS = line('12:00:01', '12:00:02', '12:00:03')

function focused(opts) {
  const env = load(opts)
  const svg = env.chart({ tips: TIPS })
  svg.focus()
  return { env, svg }
}

const HOOKS = [
  'init',
  'htmx_config_request',
  'htmx_before_swap',
  'htmx_before_morph_attr',
  'htmx_before_settle',
  'htmx_after_swap',
  'htmx_after_process',
  'htmx_before_cleanup',
  'htmx_history_cache_before_save',
  'htmx_history_cache_after_save',
  'htmx_before_viewTransition',
  'htmx_after_viewTransition',
]

test('registers the plot extension once with its hooks, and starts from init', () => {
  const env = load({ companions: ['brush', 'tween'] })
  assert.deepEqual([...env.exts.keys()], ['plot'])
  assert.deepEqual(Object.keys(env.ext).sort(), [...HOOKS].sort())
  // SSE messages run the swap hooks; an hx-sse hook would refresh twice.
  assert.equal(env.ext.htmx_sse_after_message, undefined)
  assert.ok(env.internal, 'started')
  assert.ok(env.live?.isConnected, 'status region created')
  const before = Object.values(env.doc.listeners).flat().length
  env.run() // a second <script> tag
  env.run('brush')
  env.run('tween')
  assert.equal(Object.values(env.doc.listeners).flat().length, before, 'listeners added twice')
})

test('the scripts report the package.json version in their banners and at runtime', () => {
  // A release bumps package.json; these must follow it.
  assert.match(PKG.version, /^\d+\.\d+\.\d+/)
  assert.equal(load().window.hyperplot.version, PKG.version)
  for (const name of ['hx-plot', 'hx-plot-brush', 'hx-plot-tween']) {
    assert.equal(read(`./${name}.js`).split('\n', 1)[0], `/*! ${name} ${PKG.version} | MIT */`, name)
  }
})

test('does nothing at all when htmx.config.extensions leaves plot out', () => {
  const env = load({ config: { extensions: 'sse,ptag' }, companions: ['brush', 'tween'] })
  assert.equal(env.exts.size, 0)
  assert.deepEqual(Object.values(env.doc.listeners).flat(), [], 'no document listeners')
  assert.equal(env.live, null, 'no status region')
  assert.equal(env.internal, undefined)
  const svg = env.chart({ tips: TIPS })
  svg.focus()
  assert.equal(env.key('ArrowRight').defaultPrevented, false)
  assert.equal(env.events.length, 0)
  // The guard still stops a second copy.
  assert.equal(env.window.hyperplot.version, PKG.version)
})

test('starts when the allow-list names plot', () => {
  const env = load({ config: { extensions: 'sse, ptag, plot' } })
  assert.ok(env.ext)
  assert.ok(env.internal)
})

test('works without htmx', () => {
  const { env } = focused({ htmx: false })
  assert.equal(env.exts.size, 0)
  assert.ok(env.key('ArrowRight').defaultPrevented)
  assert.equal(env.live.textContent, '12:00:01: p50 10 ms, p99 40 ms')
})

test('the client’s words come from strings', () => {
  const env = load({ config: { plot: { strings: { noData: 'keine Daten' } } } })
  const svg = env.chart({ tips: { mode: 'x', xs: [0], top: 0, bottom: 100, rows: [{ t: '14 Uhr', v: [[1, 'p99', '–']] }] } })
  svg.focus()
  env.key('Home')
  assert.equal(env.live.textContent, '14 Uhr: p99 keine Daten')
})

test('charts inside hx-ignore are left alone with htmx, not without', () => {
  for (const [htmx, attr, want] of [
    [true, 'hx-ignore', false],
    [true, 'data-hx-ignore', false],
    [false, 'hx-ignore', true],
  ]) {
    const env = load({ htmx })
    const svg = env.chart({ tips: TIPS, parent: env.el('div', { [attr]: '' }, env.doc.body) })
    svg.focus()
    assert.equal(env.key('ArrowRight').defaultPrevented, want, `${attr}, htmx ${htmx}`)
  }
})

test('the status region exists, empty and exposed, before the first keyboard step', () => {
  const env = load()
  const live = env.live
  assert.ok(live?.isConnected, 'created at startup')
  assert.equal(live.getAttribute('role'), 'status')
  assert.equal(live.getAttribute('aria-hidden'), null)
  assert.notEqual(live.style.display, 'none')
  assert.equal(live.style.position, 'absolute', 'visually hidden, not display:none')
  assert.equal(live.textContent, '')
})

test('keyboard steps are announced through the persistent status region', () => {
  const { env } = focused()
  const live = env.live
  const steps = [
    ['ArrowRight', '12:00:01: p50 10 ms, p99 40 ms'],
    ['ArrowRight', '12:00:02: p50 11 ms, p99 41 ms'],
    ['End', '12:00:03: p50 12 ms, p99 42 ms'],
    ['ArrowRight', '12:00:03: p50 12 ms, p99 42 ms'],
    ['Home', '12:00:01: p50 10 ms, p99 40 ms'],
    ['ArrowLeft', '12:00:01: p50 10 ms, p99 40 ms'],
  ]
  for (const [key, want] of steps) {
    assert.ok(env.key(key).defaultPrevented, key)
    assert.equal(env.live, live, 'the region is never replaced')
    assert.equal(live.textContent, want, key)
  }
  assert.equal(env.tip.getAttribute('aria-hidden'), 'true', 'the visual tooltip is not announced')
  assert.equal(env.tip.getAttribute('role'), null)
  assert.equal(env.tip.style.display, '')
  assert.equal(env.tip.textContent, '12:00:0110 msp5040 msp99')
})

test('ArrowLeft without an active point starts from the last point', () => {
  const { env } = focused()
  env.key('ArrowLeft')
  assert.equal(env.live.textContent, '12:00:03: p50 12 ms, p99 42 ms')
})

test('the keyboard anchors the crosshair and tooltip to the point', () => {
  const { env, svg } = focused()
  env.key('End')
  const cross = svg.querySelector('.hp-crosshair')
  assert.equal(cross.getAttribute('visibility'), 'visible')
  assert.equal(cross.getAttribute('x1'), '200')
  assert.equal(cross.getAttribute('y2'), '100')
  // Point (200, 0) is at (300, 50) on screen. A 40 px tooltip does not fit
  // above it, so it flips below and stays to the right.
  assert.equal(env.tip.style.left, '312px')
  assert.equal(env.tip.style.top, '62px')
})

test('Escape hides the tooltip and the next step is announced again', () => {
  const { env, svg } = focused()
  const live = env.live
  env.key('Home')
  env.key('Escape')
  assert.equal(env.tip.style.display, 'none')
  assert.equal(svg.querySelector('.hp-crosshair').getAttribute('visibility'), 'hidden')
  assert.equal(live.textContent, '', 'cleared, so the same row can be read again')
  env.key('Home')
  assert.equal(env.live, live)
  assert.equal(live.textContent, '12:00:01: p50 10 ms, p99 40 ms')
})

test('focusout hides a keyboard tooltip once focus has really moved', () => {
  const { env, svg } = focused()
  env.key('Home')
  const other = env.el('button', {}, env.doc.body)
  other.focus()
  env.fire('focusout', { target: svg })
  assert.equal(env.tip.style.display, '', 'decided a frame later')
  env.frame()
  assert.equal(env.tip.style.display, 'none')
  assert.equal(svg.querySelector('.hp-crosshair').getAttribute('visibility'), 'hidden')
})

test('a swap that blurs the focused chart while removing it keeps the keyboard point', () => {
  // htmx 4.0.2 innerHTML/outerHTML: the old chart fires focusout while it
  // still reports isConnected, then the swap inserts the new one, and
  // htmx_after_swap runs; htmx focuses the replacement by id.
  const env = load()
  const wrap = env.el('div', {}, env.doc.body)
  const old = env.chart({ tips: TIPS, parent: wrap })
  old.focus()
  env.key('Home')
  env.key('ArrowRight')
  env.key('ArrowRight') // 12:00:03
  env.doc.activeElement = env.doc.body
  env.fire('focusout', { target: old })
  const fresh = env.chart({ tips: line('12:00:02', '12:00:03', '12:00:04', '12:00:05'), parent: env.el('div') })
  wrap.replaceChildren(fresh)
  env.ext.htmx_after_swap()
  env.frame()
  assert.equal(env.doc.activeElement, fresh)
  assert.equal(env.tip.style.display, '')
  assert.equal(env.tip.textContent.slice(0, 8), '12:00:03', 'same row, now at index 1')
  assert.equal(fresh.querySelector('.hp-crosshair').getAttribute('visibility'), 'visible')
  env.key('ArrowRight')
  assert.equal(env.live.textContent.slice(0, 8), '12:00:04', 'the next step continues from the kept point')
})

test('scrolling an element that does not hold the chart keeps a pointer tooltip', () => {
  const env = load()
  const svg = env.chart({ tips: TIPS })
  const log = env.el('div', {}, env.doc.body)
  env.fire('pointermove', { target: svg, clientX: 215, clientY: 80, pointerType: 'mouse' })
  env.frame()
  assert.equal(env.tip.style.display, '')
  env.fireWindow('scroll', { target: log })
  assert.equal(env.tip.style.display, '', 'a followed table or a log scrolled')
  env.fireWindow('scroll', { target: env.doc })
  assert.equal(env.tip.style.display, 'none', 'the page scrolled')
})

test('pointer hover shows the tooltip without announcing it', () => {
  const env = load()
  const svg = env.chart({ tips: TIPS })
  const live = env.live
  // Screen (215, 80) is viewBox (115, 30): nearest x is 100, the second point.
  env.fire('pointermove', { target: svg, clientX: 215, clientY: 80, pointerType: 'mouse' })
  assert.equal(env.tip, null, 'coalesced to the next animation frame')
  env.frame()
  assert.equal(env.tip.style.display, '')
  assert.equal(env.tip.getAttribute('aria-hidden'), 'true')
  assert.equal(env.tip.textContent, '12:00:0211 msp5041 msp99')
  assert.equal(svg.querySelector('.hp-crosshair').getAttribute('x1'), '100')
  assert.equal(live.mutations, 0, 'hover never writes to the status region')

  // Outside the plot's vertical span nothing is active.
  env.fire('pointermove', { target: svg, clientX: 215, clientY: 300, pointerType: 'mouse' })
  env.frame()
  assert.equal(env.tip.style.display, 'none')

  // A keyboard step right after hovering is announced.
  env.fire('pointermove', { target: svg, clientX: 215, clientY: 80, pointerType: 'mouse' })
  env.frame()
  svg.focus()
  env.key('ArrowRight')
  assert.equal(live.textContent, '12:00:03: p50 12 ms, p99 42 ms')
})

test('pointerout to outside the chart hides the tooltip', () => {
  const env = load()
  const svg = env.chart({ tips: TIPS })
  env.fire('pointermove', { target: svg, clientX: 150, clientY: 80, pointerType: 'mouse' })
  env.frame()
  env.fire('pointerout', { target: svg, relatedTarget: env.doc.body, pointerType: 'mouse' })
  assert.equal(env.tip.style.display, 'none')
})

test('xy mode hits the nearest point within the radius', () => {
  const env = load()
  const tips = { mode: 'xy', pts: [[10, 10], [50, 50]], rows: [row('a', '1', '2'), row('b', '3', '4')] }
  const svg = env.chart({ tips, ctm: new Matrix(2, 0, 0, 2, 0, 0) }) // 2× zoom
  env.fire('pointermove', { target: svg, clientX: 105, clientY: 95, pointerType: 'mouse' })
  env.frame()
  assert.equal(env.tip.textContent, 'b3p504p99')
  // The 12 px radius is 6 viewBox units at 2× zoom: 5 units away hits.
  env.fire('pointermove', { target: svg, clientX: 20, clientY: 30, pointerType: 'mouse' })
  env.frame()
  assert.equal(env.tip.textContent, 'a1p502p99')
  env.fire('pointermove', { target: svg, clientX: 20, clientY: 50, pointerType: 'mouse' })
  env.frame()
  assert.equal(env.tip.style.display, 'none', 'outside the hit radius')
})

// SCATTER lists its points series by series, as the server does, so the
// data order jumps around the plot. Point e has no position.
const SCATTER = {
  mode: 'xy',
  pts: [[300, 50], [100, 80], [200, 40], [100, 20], [null, 10]],
  rows: ['a', 'b', 'c', 'd', 'e'].map((t, i) => row(t, String(i), String(i))),
}

const ring = (svg) => svg.querySelectorAll('.hp-focus')

test('the keyboard steps through a scatter plot left to right, then bottom to top', () => {
  const env = load()
  const svg = env.chart({ tips: SCATTER })
  svg.focus()
  const heads = (keys) => keys.map((k) => (env.key(k), env.live.textContent.slice(0, 1)))
  assert.deepEqual(heads(['ArrowRight', 'ArrowRight', 'ArrowRight', 'ArrowRight', 'ArrowRight']), ['b', 'd', 'c', 'a', 'a'])
  assert.deepEqual(heads(['ArrowLeft', 'Home', 'End']), ['c', 'b', 'a'])
  env.key('Escape')
  assert.deepEqual(heads(['ArrowLeft']), ['a'], 'without an active point, from the right end')

  // From a hovered point, the arrows continue in the same order.
  env.fire('pointermove', { target: svg, clientX: 300, clientY: 90, pointerType: 'mouse' }) // c at (200, 40)
  env.frame()
  assert.deepEqual(heads(['ArrowLeft']), ['d'])
})

test('a ring marks the active scatter point for the keyboard and the pointer', () => {
  const env = load()
  const svg = env.chart({ tips: SCATTER })
  svg.focus()
  assert.equal(ring(svg).length, 0, 'added on first use')
  env.key('Home') // b at (100, 80)
  const [el] = ring(svg)
  assert.equal(el.parentNode, svg.querySelector('.hp-hover'))
  assert.equal(el.namespaceURI, 'http://www.w3.org/2000/svg')
  assert.equal(el.getAttribute('aria-hidden'), 'true')
  assert.equal(el.getAttribute('pointer-events'), 'none')
  assert.deepEqual(['cx', 'cy', 'visibility'].map((a) => el.getAttribute(a)), ['100', '80', 'visible'])
  assert.equal(svg.querySelector('.hp-crosshair').getAttribute('visibility'), 'hidden')

  env.key('ArrowRight') // d at (100, 20): same x, so only the ring tells them apart
  assert.deepEqual(['cx', 'cy'].map((a) => el.getAttribute(a)), ['100', '20'])
  assert.equal(ring(svg).length, 1, 'reused')

  env.key('Escape')
  assert.equal(el.getAttribute('visibility'), 'hidden')

  env.fire('pointermove', { target: svg, clientX: 400, clientY: 100, pointerType: 'mouse' }) // a at (300, 50)
  env.frame()
  assert.deepEqual(['cx', 'cy', 'visibility'].map((a) => el.getAttribute(a)), ['300', '50', 'visible'])
  env.fire('pointerout', { target: svg, relatedTarget: env.doc.body, pointerType: 'mouse' })
  assert.equal(el.getAttribute('visibility'), 'hidden')
})

test('a morph that drops the ring gets it back, and other modes hide it', () => {
  const env = load()
  const svg = env.chart({ tips: SCATTER })
  svg.focus()
  env.key('End')
  ring(svg)[0].remove() // the server never sends it, so a morph removes it
  env.ext.htmx_after_swap()
  assert.equal(ring(svg).length, 1)
  assert.equal(ring(svg)[0].getAttribute('visibility'), 'visible')

  env.setTips(svg, line('a', 'b'))
  env.ext.htmx_after_swap()
  assert.equal(ring(svg)[0].getAttribute('visibility'), 'hidden', 'not a scatter plot any more')
  assert.equal(svg.querySelector('.hp-crosshair').getAttribute('visibility'), 'visible')
})

test('a live update does not re-announce the focused point', () => {
  for (const hook of ['htmx_after_swap']) {
    const { env, svg } = focused()
    env.key('ArrowRight')
    env.key('ArrowRight')
    const live = env.live
    const said = live.textContent
    const muts = live.mutations

    // Unchanged data: nothing changes at all.
    const tipMuts = env.tip.mutations
    env.ext[hook]()
    assert.equal(env.tip.mutations, tipMuts, `${hook}: unchanged data refilled the tooltip`)

    // A sliding window: every point moves one index to the left.
    for (let tick = 4; tick < 8; tick++) {
      env.setTips(svg, line(`12:00:0${tick - 2}`, `12:00:0${tick - 1}`, `12:00:0${tick}`))
      env.ext[hook]()
    }
    assert.equal(live.mutations, muts, `${hook}: the status region changed on refresh`)
    assert.equal(live.textContent, said)
    assert.equal(env.doc.activeElement, svg)
    // 12:00:02 moved to index 0 on the first tick and slid out on the next;
    // from then on the reader stays on the oldest point shown.
    assert.equal(env.tip.textContent.slice(0, 8), '12:00:05', hook)
  }
})

test('the keyboard point follows its heading across a sliding window', () => {
  const { env, svg } = focused()
  env.key('Home') // 12:00:01 at index 0
  env.key('ArrowRight') // 12:00:02 at index 1
  env.setTips(svg, line('12:00:02', '12:00:03', '12:00:04'))
  env.ext.htmx_after_swap()
  assert.equal(env.tip.textContent.slice(0, 8), '12:00:02', 'same heading, now at index 0')
  assert.equal(svg.querySelector('.hp-crosshair').getAttribute('x1'), '0')
  env.key('ArrowRight')
  assert.equal(env.live.textContent, '12:00:03: p50 11 ms, p99 41 ms', 'steps from the followed point')
})

test('a chart replaced by a non-morph swap is found again by id and refocused', () => {
  const env = load()
  const wrap = env.el('div', {}, env.doc.body)
  const old = env.chart({ tips: TIPS, parent: wrap })
  old.focus()
  env.key('End')
  const fresh = env.chart({ tips: line('a', 'b'), parent: env.el('div') })
  wrap.replaceChildren(fresh)
  env.ext.htmx_after_swap()
  assert.equal(env.doc.activeElement, fresh)
  assert.equal(env.tip.textContent.slice(0, 1), 'b', 'clamped to the new last point')

  wrap.replaceChildren()
  env.ext.htmx_after_swap()
  assert.equal(env.tip.style.display, 'none', 'hidden once the chart is gone')
})

test('a swap that replaced the body recreates the status region', () => {
  const env = load()
  env.live.remove()
  env.ext.htmx_after_swap()
  assert.ok(env.live?.isConnected)
})

test('a restored page snapshot loses its stale tooltip, status region and marks', () => {
  // htmx's hx-history-cache saves body.outerHTML while a tooltip is shown
  // and swaps that copy back in on Back.
  const env = load()
  const svg = env.chart({ tips: TIPS })
  env.fire('pointermove', { target: svg, clientX: 215, clientY: 80, pointerType: 'mouse' })
  env.frame()
  assert.equal(svg.querySelector('.hp-crosshair').getAttribute('visibility'), 'visible')
  // The snapshot: copies the client does not track.
  const copyTip = env.el('div', { class: 'hp-tip', 'aria-hidden': 'true' })
  const copyLive = env.el('div', { class: 'hp-live', role: 'status' })
  const copy = env.chart({ tips: TIPS, parent: env.el('div') })
  copy.querySelector('.hp-crosshair').setAttribute('visibility', 'visible')
  env.fire('pointermove', { target: env.doc.body, clientX: 1, clientY: 1, pointerType: 'mouse' })
  env.frame()
  env.doc.body.replaceChildren(copy, copyTip, copyLive)
  env.ext.htmx_after_swap()
  assert.equal(env.doc.querySelectorAll('div.hp-tip').length, 0, 'no tooltip left over')
  assert.equal(env.doc.querySelectorAll('[role="status"]').length, 1, 'one status region')
  assert.equal(copy.querySelector('.hp-crosshair').getAttribute('visibility'), 'hidden')
})

test('hx-history-cache’s copy of the page leaves out what the client owns, which comes back after', () => {
  const env = load()
  const svg = env.chart({ tips: { mode: 'xy', pts: [[10, 10]], rows: [{ t: 'a', v: [[1, 's', '1']] }] } })
  svg.focus()
  env.key('Home') // the ring, the tooltip and an announcement
  const ring = svg.querySelector('.hp-focus')
  const hover = ring.parentNode
  const { tip, live } = env
  assert.ok(ring && tip && live)
  env.ext.htmx_history_cache_before_save(env.doc, { target: env.doc.body })
  assert.equal(env.doc.querySelectorAll('div.hp-tip,div.hp-live,.hp-focus').length, 0, 'not in the copy')
  env.ext.htmx_history_cache_after_save(env.doc, {})
  assert.equal(ring.parentNode, hover)
  assert.equal(env.tip, tip)
  assert.equal(env.live, live)
  assert.equal(live.textContent, 'a: s 1', 'the region keeps its text')
  // A save that another extension cancelled: the next swap puts them back.
  env.ext.htmx_history_cache_before_save(env.doc, { target: env.doc.body })
  env.ext.htmx_after_swap()
  assert.equal(ring.parentNode, hover)
  assert.equal(env.doc.querySelectorAll('div.hp-tip').length, 1)
})

test('in an open dialog the tooltip and the status region go inside it', () => {
  const env = load()
  const dialog = env.el('dialog', { open: '' }, env.doc.body)
  const svg = env.chart({ tips: TIPS, parent: dialog })
  const out = env.chart({ id: 'c2', tips: TIPS })
  env.fire('focusin', { target: svg })
  assert.equal(env.live.parentNode, dialog, 'moved when the chart takes focus, before the first step')
  svg.focus()
  env.key('Home')
  assert.equal(env.tip.parentNode, dialog)
  assert.equal(env.live.parentNode, dialog)
  assert.match(env.live.textContent, /^12:00:01: /)
  env.ext.htmx_after_swap()
  assert.equal(env.live.parentNode, dialog, 'a swap leaves it with the active chart')
  env.fire('focusin', { target: out })
  out.focus()
  env.key('Home')
  assert.equal(env.tip.parentNode, env.doc.body)
  assert.equal(env.live.parentNode, env.doc.body)
  assert.equal(env.doc.querySelectorAll('[role="status"]').length, 1)
})

test('a swap leaves the status region in the dialog whose chart has focus, before any step', () => {
  // A live page swaps between focus and the first arrow key, and after
  // Escape; the region must stay exposed inside the modal dialog.
  const env = load()
  const dialog = env.el('dialog', { open: '' }, env.doc.body)
  const svg = env.chart({ tips: TIPS, parent: dialog })
  env.fire('focusin', { target: svg })
  svg.focus()
  env.ext.htmx_after_swap()
  assert.equal(env.live.parentNode, dialog, 'after a swap with no keyboard point')
  env.key('Home')
  env.key('Escape')
  env.ext.htmx_after_swap()
  assert.equal(env.live.parentNode, dialog, 'after Escape and a swap')
  // A chart that already had focus when the client started (showModal()
  // autofocuses it) gets the region at once.
  const env2 = load({
    before(e) {
      const d = e.el('dialog', { open: '' }, e.doc.body)
      e.chart({ tips: TIPS, parent: d }).focus()
    },
  })
  assert.equal(env2.live.parentNode.localName, 'dialog', 'placed for the chart focused at start-up')
})

test('a payload whose values are not text neither breaks hover nor a swap', () => {
  const env = load()
  const svg = env.chart({ tips: { mode: 'x', xs: [0], top: 0, bottom: 100, rows: [{ t: { toString: 1 }, k: { toString: 1 }, v: [[1, { toString: 1 }, '1']] }] } })
  env.fire('pointermove', { target: svg, clientX: 100, clientY: 60 })
  env.frame()
  assert.equal(env.tip.style.display, '')
  env.ext.htmx_after_swap()
  svg.focus()
  env.key('Home')
  assert.equal(env.live.textContent, '1')
})

test('loaded before htmx (a module htmx), the client decides once the page is parsed', () => {
  // The scripts run while the document is loading and htmx is not there
  // yet; it arrives before DOMContentLoaded, as a module script does.
  let h
  const env = load({
    companions: ['brush', 'tween'],
    before(e) {
      e.doc.readyState = 'loading'
      h = e.window.htmx
      delete e.window.htmx
    },
  })
  assert.equal(env.internal, undefined, 'undecided')
  assert.equal(env.exts.size, 0)
  env.window.htmx = h
  env.doc.readyState = 'interactive'
  env.fire('DOMContentLoaded')
  assert.deepEqual([...env.exts.keys()], ['plot'], 'htmx mode')
  assert.ok(env.internal)
  assert.ok(env.window.hyperplot.brush, 'the brush companion started')
  assert.ok(env.window.hyperplot.tween, 'the tween companion started')
  // A gesture request carries its parameters.
  const svg = env.chart({ tips: { mode: 'x', xs: [0], rows: [{ t: 'a', k: 'ka', v: [] }] } })
  svg.focus()
  env.key('Home')
  env.key('Enter')
  const sel = env.events.find((e) => e.type === 'htmx:plot:select')
  const body = new FormData()
  env.ext.htmx_config_request(svg, { ctx: { request: { body, action: '/x' }, target: svg, sourceEvent: { type: sel.type, detail: sel.detail } } })
  assert.equal(body.get('hp-x'), 'ka')
})

test('without htmx, standalone mode warns when the page uses htmx attributes', () => {
  const warned = []
  for (const withAttrs of [false, true]) {
    load({
      htmx: false,
      before(e) {
        e.window.console = { ...console, warn: (m) => warned.push([withAttrs, m]) }
        if (withAttrs) e.el('div', { 'hx-get': '/x' }, e.doc.body)
      },
    })
  }
  assert.equal(warned.length, 1)
  assert.equal(warned[0][0], true)
  assert.match(warned[0][1], /^hx-plot: no htmx/)
})

test('an element named hyperplot does not stop the script or its companions', () => {
  const env = load({
    companions: ['brush', 'tween'],
    before(env) {
      // What a browser does for <img name="hyperplot"> or id="hyperplot":
      // a named property on the window's prototype chain, not its own.
      Object.setPrototypeOf(env.window, { hyperplot: { tagName: 'IMG' } })
    },
  })
  assert.ok(env.internal, 'started')
  assert.equal(env.window.hyperplot.version, PKG.version)
  assert.ok(env.window.hyperplot.brush && env.window.hyperplot.tween, 'companions found it')
})

test('handlers ignore events outside charts', () => {
  const env = load()
  const div = env.el('div', {}, env.doc.body)
  assert.equal(env.key('ArrowRight', div).defaultPrevented, false)
  assert.equal(env.key('a', env.chart({ tips: TIPS })).defaultPrevented, false)
  const svg = env.chart({ id: 'c2', tips: TIPS })
  assert.equal(env.fire('keydown', { key: 'ArrowRight', target: svg, ctrlKey: true }).defaultPrevented, false)
  env.fire('pointermove', { target: div, clientX: 1, clientY: 1, pointerType: 'mouse' })
  env.frame()
  assert.equal(env.tip, null)
  // A chart with a broken payload does nothing.
  const bad = env.chart({ id: 'bad', tips: { mode: 'pie' } })
  assert.equal(env.key('ArrowRight', bad).defaultPrevented, false)
  assert.equal(env.live.textContent, '')
})

// request runs htmx_config_request as htmx 4 does, with a FormData body.
function request(env, elt, { target = elt, body = new FormData(), action = '/charts/latency', sourceEvent } = {}) {
  env.ext.htmx_config_request(elt, { ctx: { request: { action, body }, target, sourceEvent } })
  return Object.fromEntries(body)
}

// getURL runs htmx_config_request for a GET, then merges the body into the
// URL exactly as htmx 4.0.2 does afterwards (htmx.js, usesQueryParams): every
// body key replaces the URL parameters of the same name.
function getURL(env, elt, action) {
  const body = new FormData()
  const ctx = { request: { action, body }, target: elt }
  env.ext.htmx_config_request(elt, { ctx })
  const url = new URL(ctx.request.action, env.doc.baseURI)
  for (const key of body.keys()) url.searchParams.delete(key)
  for (const [key, value] of body) url.searchParams.append(key, value)
  return url.pathname + url.search
}

function autosized(env, { mode = '', w = 640, h = 324, padding = [0, 0, 0, 0], attr = 'hx-plot:autosize' } = {}) {
  const box = env.el('div', { [attr]: mode }, env.doc.body)
  Object.assign(box, { clientWidth: w, clientHeight: h, padding })
  return box
}

test('requests that target an autosize element carry its content width but no height', () => {
  const env = load()
  // A 320 px chart in an inline formatting context makes its wrapper 324 px
  // tall. Sending that back as hp-h would grow the chart on every poll.
  const box = autosized(env, { w: 664, h: 348, padding: [12, 12, 12, 12] })
  assert.deepEqual(request(env, box), { 'hp-w': '640' })
  const inner = env.el('button', {}, box)
  assert.deepEqual(request(env, inner), { 'hp-w': '640' }, 'targeting something inside it')
  assert.deepEqual(request(env, env.el('a', {}, env.doc.body), { target: inner }), { 'hp-w': '640' }, 'from elsewhere')
  // A drill-down from inside a chart's box into another element gets no size.
  assert.deepEqual(request(env, inner, { target: env.el('div', {}, env.doc.body) }), {}, 'targeting elsewhere')
})

test('the attribute works in every spelling htmx accepts', () => {
  for (const [name, attr, config] of [
    ['data- prefix', 'data-hx-plot:autosize', {}],
    ['custom prefix', 'x-hx-plot:autosize', { prefix: 'x-hx-' }],
    ['metaCharacter', 'hx-plot-autosize', { metaCharacter: '-' }],
    ['metaCharacter with the prefix', 'data-hx-plot-autosize', { metaCharacter: '-' }],
    ['colon under a metaCharacter', 'hx-plot:autosize', { metaCharacter: '-' }],
  ]) {
    const env = load({ config })
    const box = autosized(env, { attr, mode: 'both', w: 300, h: 200 })
    assert.deepEqual(request(env, box), { 'hp-w': '300', 'hp-h': '200' }, name)
  }
  // Without htmx: the default spellings, observed at startup.
  let box
  const env = load({ htmx: false, before: (env) => (box = autosized(env, { attr: 'data-hx-plot:autosize', w: 300 })) })
  assert.ok(env.observed.has(box))
})

test('hx-plot:autosize="both" also sends the content height', () => {
  const env = load()
  const box = autosized(env, { mode: 'both', w: 640.4, h: 300, padding: [10, 0, 10.5, 0] })
  assert.deepEqual(request(env, box), { 'hp-w': '640', 'hp-h': '280' })
})

test('autosize never overrides parameters and skips what it cannot send', () => {
  const env = load()
  const box = autosized(env, { mode: 'both' })
  const body = new FormData()
  body.set('hp-w', '999')
  assert.deepEqual(request(env, box, { body }), { 'hp-w': '999', 'hp-h': '324' })

  assert.deepEqual(request(env, autosized(env, { w: 0, h: 0, mode: 'both' })), {}, 'zero sizes are omitted')
  assert.deepEqual(request(env, env.el('div', {}, env.doc.body)), {}, 'not in an autosize element')
  assert.doesNotThrow(() => env.ext.htmx_config_request(box, { ctx: { request: { body: 'raw' } } }))
  assert.doesNotThrow(() => env.ext.htmx_config_request(box, undefined))
})

test('autosize never overrides a size already in the request URL', () => {
  const env = load()
  const box = autosized(env, { mode: 'both', w: 812, h: 300 })
  // A pinned width in hx-get survives htmx's merge of the body into the URL.
  assert.equal(getURL(env, box, '/charts/spark?hp-w=300'), '/charts/spark?hp-w=300&hp-h=300')
  assert.equal(getURL(env, box, 'https://example.test/s?range=1h&hp-h=90'), '/s?range=1h&hp-h=90&hp-w=812')
  assert.equal(getURL(env, box, '/charts/spark?range=1h'), '/charts/spark?range=1h&hp-w=812&hp-h=300')
  // A POST keeps URL parameters too, and the body would win in FormValue.
  assert.deepEqual(request(env, box, { action: '/charts?hp-w=300' }), { 'hp-h': '300' })
  assert.deepEqual(request(env, box, { action: '/charts?hp-w-x=1' }), { 'hp-w': '812', 'hp-h': '300' }, 'exact names only')
  assert.deepEqual(request(env, box, { action: null }), { 'hp-w': '812', 'hp-h': '300' }, 'no URL')
})

test('htmx:plot:resize fires once the content width changes by 8px, debounced', () => {
  const env = load()
  const box = autosized(env, { w: 640 })
  env.ext.htmx_after_process(box) // htmx processed new content
  assert.ok(env.observed.has(box))
  assert.equal(box._htmx?.plot, true, 'htmx state, so that htmx_before_cleanup runs for it')
  const events = []
  // The spread copies the detail out of the script's realm for deepEqual.
  box.addEventListener('htmx:plot:resize', (e) => events.push({ ...e.detail }))

  env.resize(box, 645)
  env.timers()
  assert.deepEqual(events, [], 'under 8px')

  env.resize(box, 700)
  env.resize(box, 720, 200)
  assert.equal(env.pendingTimers, 1, 'debounced')
  env.timers()
  assert.deepEqual(events, [{ width: 720, height: 200 }])

  env.resize(box, 720, 500)
  env.timers()
  assert.equal(events.length, 1, 'height alone is ignored without "both"')

  // Through htmx: not bubbling, so a parent's hx-trigger never sees it.
  assert.deepEqual(
    env.events.map((e) => [e.type, e.bubbles, e.elt === box]),
    [['htmx:plot:resize', false, true]],
  )

  box.remove()
  env.resize(box, 100)
  assert.ok(!env.observed.has(box), 'disconnected elements are unobserved')
})

test('htmx_before_cleanup stops observing', () => {
  const env = load()
  const box = autosized(env)
  env.ext.htmx_after_process(env.doc.body)
  env.ext.htmx_before_cleanup(box, {})
  assert.ok(!env.observed.has(box))
  assert.doesNotThrow(() => env.ext.htmx_before_cleanup(env.doc.body, {}))
})

test('htmx:plot:resize follows the height too with hx-plot:autosize="both"', () => {
  const env = load()
  const box = autosized(env, { mode: 'both', w: 640, h: 320 })
  env.ext.htmx_after_swap()
  const events = []
  box.addEventListener('htmx:plot:resize', (e) => events.push({ ...e.detail }))
  env.resize(box, 640, 360)
  env.timers()
  assert.deepEqual(events, [{ width: 640, height: 360 }])
})

test('htmx:plot:resize takes the metaCharacter spelling, and is a CustomEvent without htmx', () => {
  const env = load({ config: { metaCharacter: '-' } })
  const box = autosized(env, { attr: 'hx-plot-autosize' })
  env.ext.htmx_after_process(box)
  env.resize(box, 700)
  env.timers()
  assert.deepEqual(env.events.map((e) => e.type), ['htmx-plot-resize'])

  let alone
  const env2 = load({ htmx: false, before: (e) => (alone = autosized(e)) })
  const got = []
  alone.addEventListener('htmx:plot:resize', (e) => got.push([e.bubbles, { ...e.detail }]))
  env2.resize(alone, 700)
  env2.timers()
  assert.deepEqual(got, [[false, { width: 700, height: 324 }]])
})

// ---- Select (docs/spec.md §6.6) ----

// selects returns the htmx:plot:select details dispatched so far.
const selects = (env) => plain(env.events.filter((e) => e.type === 'htmx:plot:select').map((e) => e.detail))

const KEYED = {
  mode: 'x',
  xs: [0, 100, 200],
  top: 0,
  bottom: 100,
  rows: ['a', 'b', 'c'].map((t, i) => ({ t, k: `k${i}`, v: [[1, 'p50', String(i)]] })),
}

test('a click selects the point under it, with the series clicked', () => {
  const env = load()
  const svg = env.chart({ tips: KEYED })
  const series = env.el('g', { class: 'hp-series hp-s1', 'data-hp-series': 'p50' }, svg)
  const line = env.el('path', { class: 'hp-line' }, series)
  env.click(svg, 215, 80) // viewBox (115, 30): the second point
  env.click(line, 290, 80) // on the line: the third point, series p50
  env.click(svg, 215, 300) // below the plot: nothing
  assert.deepEqual(selects(env), [
    { chart: 'c1', x: 'k1', series: '', index: 1 },
    { chart: 'c1', x: 'k2', series: 'p50', index: 2 },
  ])
  assert.ok(env.events.every((e) => e.bubbles && e.elt === svg))
})

test('Enter and assistive-technology clicks select the keyboard point', () => {
  const env = load()
  const svg = env.chart({ tips: KEYED })
  svg.focus()
  env.key('Enter') // no keyboard point yet
  env.click(svg, 215, 80, 0) // detail 0 and no keyboard point
  env.key('End')
  assert.ok(env.key('Enter').defaultPrevented)
  env.click(svg, 115, 80, 0) // the coordinates do not matter
  assert.deepEqual(selects(env), [
    { chart: 'c1', x: 'k2', series: '', index: 2 },
    { chart: 'c1', x: 'k2', series: '', index: 2 },
  ])
})

test('a tap selects only the point that was already active', () => {
  const env = load()
  const svg = env.chart({ tips: KEYED })
  const tap = (x) => {
    const p = { target: svg, clientX: x, clientY: 80, pointerType: 'touch', pointerId: 1 }
    env.fire('pointerdown', p)
    // The coalesced hover may run before the click: it must not count.
    env.frame()
    env.fire('pointerup', p)
    env.fire('click', { ...p, detail: 1 })
  }
  tap(215) // shows the tooltip of the second point
  assert.deepEqual(selects(env), [])
  assert.equal(env.tip.style.display, '')
  tap(215) // the second tap on it selects
  tap(290) // a tap elsewhere moves the tooltip
  assert.deepEqual(selects(env), [{ chart: 'c1', x: 'k1', series: '', index: 1 }])
})

test('rows without a key, and charts without a payload, select nothing', () => {
  const env = load()
  const svg = env.chart({ tips: TIPS }) // v0 rows have no k
  env.click(svg, 215, 80)
  const bare = env.el('svg', { class: 'hp', id: 'bare' }, env.doc.body)
  bare.ctm = new Matrix()
  env.click(bare, 5, 5)
  assert.deepEqual(selects(env), [])
})

test('in a scatter plot the selected point names its own series', () => {
  const env = load()
  const tips = {
    mode: 'xy',
    pts: [[10, 10], [50, 50]],
    rows: [
      { t: 'a', k: '1', v: [[1, 'GET', '1']] },
      { t: 'b', k: '2', v: [[2, 'POST', '2']] },
    ],
  }
  const svg = env.chart({ tips })
  for (const [slot, name] of [[1, 'GET'], [2, 'POST']]) env.el('g', { class: `hp-series hp-s${slot}`, 'data-hp-series': name }, svg)
  env.click(svg, 150, 100)
  assert.deepEqual(selects(env), [{ chart: 'c1', x: '2', series: 'POST', index: 1 }])
})

test('without htmx a select is a bubbling CustomEvent', () => {
  const env = load({ htmx: false })
  const wrap = env.el('div', {}, env.doc.body)
  const svg = env.chart({ tips: KEYED, parent: wrap })
  const got = []
  wrap.addEventListener('htmx:plot:select', (e) => got.push([e.type, e.cancelable, plain(e.detail)]))
  env.click(svg, 115, 80)
  assert.deepEqual(got, [['htmx:plot:select', true, { chart: 'c1', x: 'k0', series: '', index: 0 }]])
})

test('select events take the metaCharacter spelling', () => {
  const env = load({ config: { metaCharacter: '-' } })
  const svg = env.chart({ tips: KEYED })
  env.click(svg, 115, 80)
  assert.deepEqual(env.events.map((e) => e.type), ['htmx-plot-select'])
})

test('a gesture adds its parameters to the request it triggers, never overriding', () => {
  const env = load()
  const detail = { chart: 'lt', x: '2026-03-03T14:10:00Z', series: '', index: 1 }
  const sourceEvent = { type: 'htmx:plot:select', detail }
  const target = env.el('div', {}, env.doc.body)
  assert.deepEqual(request(env, target, { sourceEvent }), { 'hp-chart': 'lt', 'hp-x': '2026-03-03T14:10:00Z' })
  const body = new FormData()
  body.set('hp-x', 'mine')
  assert.deepEqual(request(env, target, { sourceEvent, body, action: '/d?hp-chart=pinned' }), { 'hp-x': 'mine' })
  // A brush from an autosize element's ancestor that targets it: both.
  const box = autosized(env, { w: 800 })
  const brush = { type: 'htmx:plot:brush', detail: { chart: 'lt', x0: 'a', x1: 'b' } }
  assert.deepEqual(Object.keys(request(env, env.doc.body, { target: box, sourceEvent: brush })), ['hp-w', 'hp-chart', 'hp-x0', 'hp-x1'])
})
