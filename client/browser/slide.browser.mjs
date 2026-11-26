// Bug 2, "the sliding latency line wobbles instead of scrolling" (docs/DESIGN.md,
// Roadmap): in a real browser, when a live chart's x window moves, its line
// moves sideways as one piece and every vertex keeps its height, instead of
// each vertex tweening vertically towards its neighbour's value. Under
// reduced motion, or with the slide turned off, the new frame appears at
// once.
//
// Run with `npm run test:browser` (see harness.mjs).
import { after, before, test } from 'node:test'
import assert from 'node:assert/strict'
import { cleanup, launch, shot, shots, startDashboard, waitFor } from './harness.mjs'

let server
let browser

before(async () => {
  server = await startDashboard()
  browser = await launch(server.url)
  const { page } = browser
  await page.goto(server.url + '/?range=15m')
  await waitFor(page, () => window.htmx && document.querySelector('#latency')?.getAttribute('width') !== '640', null, {
    what: 'the latency chart at its real width',
  })
  await page.evaluate(installSampler)
})

after(cleanup)

// installSampler defines window.__hpSlide(id, series, ms) in the page. It
// waits for the chart's next update that brings new data at the same size,
// then records, on every animation frame for ms milliseconds, the series
// line's d attribute, its computed d (what is drawn, CSS transitions
// included) and the product of the computed transforms of the series group
// and the path (what moves it, slides included), plus the drawn x of every
// x tick label. Frame 0 is the last frame before the update. (The page's
// CSP forbids eval, so the tests call it by name.)
function installSampler() {
  window.__hpSlide = (id, series, ms) => {
    const svg = () => document.getElementById(id)
    const newest = () => JSON.parse(svg().querySelector('metadata.hp-tips').textContent).rows.at(-1).t
    const matrix = (el) => {
      const t = getComputedStyle(el).transform
      return t && t !== 'none' ? new DOMMatrix(t) : new DOMMatrix()
    }
    const sample = () => {
      const s = svg()
      const g = s.querySelector(`g.hp-series[data-hp-series="${series}"]`)
      const path = g.querySelector('path.hp-line')
      const m = matrix(g).multiply(matrix(path))
      return {
        t: performance.now(),
        newest: newest(),
        attr: path.getAttribute('d'),
        drawn: getComputedStyle(path).d,
        m: [m.a, m.b, m.c, m.d, m.e, m.f],
        ticks: [...s.querySelectorAll('g.hp-axis-x > text.hp-tick')].map((e) => [e.textContent, +e.getAttribute('x') + matrix(e).e]),
        geo: ['data-hp-plot', 'data-hp-x'].map((a) => s.getAttribute(a)),
        vb: s.getAttribute('viewBox'),
      }
    }
    return new Promise((resolve, reject) => {
      const first = sample()
      let last = first
      let t0 = 0
      const frames = []
      const deadline = performance.now() + 40000
      const tick = () => {
        const f = sample()
        if (!t0 && f.newest !== first.newest) {
          if (f.vb !== first.vb) return reject(new Error('the chart was resized'))
          t0 = f.t
          frames.push(last)
        }
        if (t0) frames.push(f)
        else last = f
        if (t0 && f.t - t0 > ms) return resolve(frames)
        if (!t0 && f.t > deadline) return reject(new Error('no update in 40 s'))
        requestAnimationFrame(tick)
      }
      requestAnimationFrame(tick)
    })
  }
}

// vertices returns the end points of the segments of a path's data, as
// written in a d attribute or in a computed d (path("M 1 2 C …")).
function vertices(d) {
  const tokens = String(d).match(/[MLCZmlcz]|-?\d*\.?\d+(?:e[-+]?\d+)?/g) ?? []
  const out = []
  let cmd = ''
  let nums = []
  const flush = () => {
    const step = { M: 2, L: 2, C: 6 }[cmd] ?? 0
    for (let i = 0; step && i + step <= nums.length; i += step) out.push([nums[i + step - 2], nums[i + step - 1]])
    nums = []
  }
  for (const t of tokens) {
    if (/[A-Za-z]/.test(t)) {
      flush()
      cmd = t.toUpperCase()
    } else nums.push(Number(t))
  }
  flush()
  return out
}

// drawn returns where a frame draws each vertex of the line, in viewBox units.
function drawn(f) {
  const [a, b, c, d, e, g] = f.m
  return vertices(f.drawn === 'none' ? f.attr : f.drawn).map(([x, y]) => [a * x + c * y + e, b * x + d * y + g])
}

// travel measures, over the frames from the update on, how far each vertex
// strays from where the last frame draws it.
function travel(frames) {
  const end = drawn(frames.at(-1))
  let maxY = 0
  let sumY = 0
  let maxX = 0
  for (const f of frames.slice(1)) {
    const v = drawn(f)
    if (v.length !== end.length) continue
    for (let k = 0; k < v.length; k++) {
      const dy = Math.abs(v[k][1] - end[k][1])
      maxY = Math.max(maxY, dy)
      sumY += dy / v.length
      maxX = Math.max(maxX, Math.abs(v[k][0] - end[k][0]))
    }
  }
  return { maxY, meanY: sumY / Math.max(1, frames.length - 1), maxX }
}

const p99 = (page, ms = 700) => page.evaluate(([ms]) => window.__hpSlide('latency', 'p99', ms), [ms])

test('a moved window slides the line sideways; no vertex moves vertically', async (t) => {
  const { page, errors } = browser
  const frames = await p99(page)
  const moving = frames.slice(1).filter((f) => Math.abs(f.m[4]) > 0.01)
  const tr = travel(frames)
  t.diagnostic(
    `${frames.length - 1} frames after the update, ${moving.length} of them moving; ` +
      `vertex travel: max ${tr.maxY.toFixed(2)} vertical (mean ${tr.meanY.toFixed(2)}), max ${tr.maxX.toFixed(2)} horizontal`,
  )
  assert.notDeepEqual(frames[1].geo, frames[0].geo, 'the x window moved')
  assert.ok(moving.length >= 5, `the slide lasts several frames (${moving.length})`)

  for (const [i, f] of frames.slice(1).entries()) {
    // Nothing tweens the path's shape: what is drawn is the new d itself.
    const want = vertices(f.attr)
    const got = vertices(f.drawn === 'none' ? f.attr : f.drawn)
    assert.equal(got.length, want.length, `frame ${i}: vertex count`)
    for (let k = 0; k < got.length; k++) {
      assert.ok(Math.abs(got[k][0] - want[k][0]) < 0.01 && Math.abs(got[k][1] - want[k][1]) < 0.01, `frame ${i}: vertex ${k} is tweened`)
    }
    // The only motion is horizontal: matrix(a, 0, 0, 1, e, 0).
    const [, b, c, d, , g] = f.m
    assert.ok(Math.abs(b) < 1e-9 && Math.abs(c) < 1e-9 && Math.abs(d - 1) < 1e-9 && Math.abs(g) < 1e-9, `frame ${i}: transform ${f.m}`)
  }
  assert.ok(tr.maxY < 0.5, `vertical travel ${tr.maxY}`)
  assert.ok(tr.maxX > 1, `the line moved sideways (${tr.maxX})`)

  // A vertex in the middle of the line moves left, steadily, to its place.
  const mid = Math.floor(drawn(frames.at(-1)).length / 2)
  const xs = frames.slice(1).map((f) => drawn(f)[mid][0])
  for (let i = 1; i < xs.length; i++) assert.ok(xs[i] <= xs[i - 1] + 0.01, `vertex ${mid} moved right: ${xs[i - 1]} → ${xs[i]}`)

  // Tick labels move with the data and never jump right.
  for (let i = 2; i < frames.length; i++) {
    const before = new Map(frames[i - 1].ticks)
    for (const [label, x] of frames[i].ticks) {
      if (before.has(label)) assert.ok(x <= before.get(label) + 0.01, `tick ${label} moved right: ${before.get(label)} → ${x}`)
    }
  }
  assert.deepEqual(errors, [])
})

test('every slide in a row is a sideways move', async () => {
  const { page } = browser
  for (let i = 0; i < 2; i++) {
    const frames = await p99(page, 500)
    assert.ok(travel(frames).maxY < 0.5, `update ${i}: vertical travel ${travel(frames).maxY}`)
  }
})

test('with slide off the new frame appears at once', async () => {
  const { page } = browser
  await page.evaluate(() => (htmx.config.plot = { slide: false }))
  try {
    const frames = await p99(page, 500)
    for (const f of frames.slice(1)) assert.deepEqual(f.m, [1, 0, 0, 1, 0, 0], 'no slide')
    const tr = travel(frames)
    assert.equal(tr.maxY, 0, 'no tween')
    assert.equal(tr.maxX, 0, 'no tween')
  } finally {
    await page.evaluate(() => delete htmx.config.plot)
  }
})

test('under reduced motion the new frame appears at once', async () => {
  const { page } = browser
  await page.emulateMedia({ reducedMotion: 'reduce' })
  try {
    const frames = await p99(page, 500)
    for (const f of frames.slice(1)) assert.deepEqual(f.m, [1, 0, 0, 1, 0, 0], 'no slide')
    assert.equal(travel(frames).maxY, 0, 'no tween')
  } finally {
    await page.emulateMedia({ reducedMotion: 'no-preference' })
  }
})

// With HP_SHOTS set, freeze the next update's animations and save the chart
// at 0%, 30% and 100% of their duration.
test('screenshots of one update', { skip: !shots && 'HP_SHOTS is not set' }, async () => {
  const { page } = browser
  const durations = await page.evaluate(
    () =>
      new Promise((resolve) => {
        const svg = () => document.getElementById('latency')
        const newest = () => JSON.parse(svg().querySelector('metadata.hp-tips').textContent).rows.at(-1).t
        const was = newest()
        const check = () => {
          if (newest() === was) return requestAnimationFrame(check)
          // Computing a style starts any CSS transition the update caused.
          getComputedStyle(svg().querySelector('path.hp-line')).d
          window.__hpFrozen = svg().getAnimations({ subtree: true })
          for (const a of window.__hpFrozen) a.pause()
          resolve(window.__hpFrozen.map((a) => a.effect.getTiming().duration))
        }
        requestAnimationFrame(check)
      }),
  )
  assert.ok(durations.length > 0, 'the update animates')
  for (const [name, p] of [
    ['0', 0],
    ['30', 0.3],
    ['100', 1],
  ]) {
    await page.evaluate((p) => {
      for (const a of window.__hpFrozen) a.currentTime = Math.min(p * a.effect.getTiming().duration, a.effect.getTiming().duration - 0.001)
    }, p)
    await shot(page.locator('#latency'), `bug2-latency-${name}pct`)
  }
  await page.evaluate(() => window.__hpFrozen.forEach((a) => a.play()))
})
