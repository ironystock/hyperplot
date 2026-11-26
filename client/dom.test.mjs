// Tests for the DOM and htmx wiring in hyperplot.js: the extension hooks,
// autosize parameters and events, pointer and keyboard tooltips, the status
// region, and state across live updates. They run the real script against
// the small fake browser in fakedom.mjs. Run with: node --test
import { test } from 'node:test'
import assert from 'node:assert/strict'
import { readFileSync } from 'node:fs'
import { load, Matrix } from './fakedom.mjs'

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

test('registers the hyperplot extension once with its hooks', () => {
  const env = load()
  assert.deepEqual([...env.exts.keys()], ['hyperplot'])
  for (const hook of ['htmx_config_request', 'htmx_after_swap', 'htmx_sse_after_message']) {
    assert.equal(typeof env.ext[hook], 'function', hook)
  }
  const before = Object.values(env.doc.listeners).flat().length
  env.run() // a second <script> tag
  assert.equal(Object.values(env.doc.listeners).flat().length, before, 'listeners added twice')
})

test('the script reports the package.json version in its banner and at runtime', () => {
  // A release bumps package.json; these must follow it.
  assert.match(PKG.version, /^\d+\.\d+\.\d+/)
  assert.equal(load().window.hyperplot.version, PKG.version)
  assert.equal(read('./hyperplot.js').split('\n', 1)[0], `/*! hyperplot ${PKG.version} | MIT */`)
})

test('works without htmx', () => {
  const { env } = focused({ htmx: false })
  assert.equal(env.exts.size, 0)
  assert.ok(env.key('ArrowRight').defaultPrevented)
  assert.equal(env.live.textContent, '12:00:01: p50 10 ms, p99 40 ms')
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

test('focusout hides a keyboard tooltip', () => {
  const { env, svg } = focused()
  env.key('Home')
  env.fire('focusout', { target: svg })
  assert.equal(env.tip.style.display, 'none')
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
  for (const hook of ['htmx_after_swap', 'htmx_sse_after_message']) {
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
  env.ext.htmx_sse_after_message()
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
function request(env, elt, { target = elt, body = new FormData(), action = '/charts/latency' } = {}) {
  env.ext.htmx_config_request(elt, { ctx: { request: { action, body }, target } })
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

function autosized(env, { mode = '', w = 640, h = 324, padding = [0, 0, 0, 0] } = {}) {
  const box = env.el('div', { 'data-hp-autosize': mode }, env.doc.body)
  Object.assign(box, { clientWidth: w, clientHeight: h, padding })
  return box
}

test('requests from an autosize element carry its content width but no height', () => {
  const env = load()
  // A 320 px chart in an inline formatting context makes its wrapper 324 px
  // tall. Sending that back as hp-h would grow the chart on every poll.
  const box = autosized(env, { w: 664, h: 348, padding: [12, 12, 12, 12] })
  assert.deepEqual(request(env, box), { 'hp-w': '640' })
  const inner = env.el('button', {}, box)
  assert.deepEqual(request(env, inner), { 'hp-w': '640' }, 'from inside the element')
  assert.deepEqual(request(env, env.el('a', {}, env.doc.body), { target: inner }), { 'hp-w': '640' }, 'targeting it')
})

test('data-hp-autosize="both" also sends the content height', () => {
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

test('hp:resize fires once the content width changes by 8px, debounced', () => {
  const env = load()
  const box = autosized(env, { w: 640 })
  env.ext.htmx_after_swap() // observes new autosize elements
  assert.ok(env.observed.has(box))
  const events = []
  // The spread copies the detail out of the script's realm for deepEqual.
  box.addEventListener('hp:resize', (e) => events.push({ ...e.detail }))

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

  box.remove()
  env.resize(box, 100)
  assert.ok(!env.observed.has(box), 'disconnected elements are unobserved')
})

test('hp:resize follows the height too with data-hp-autosize="both"', () => {
  const env = load()
  const box = autosized(env, { mode: 'both', w: 640, h: 320 })
  env.ext.htmx_after_swap()
  const events = []
  box.addEventListener('hp:resize', (e) => events.push({ ...e.detail }))
  env.resize(box, 640, 360)
  env.timers()
  assert.deepEqual(events, [{ width: 640, height: 360 }])
})
