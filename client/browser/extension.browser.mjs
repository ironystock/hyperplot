// The htmx extension in a real browser, on static test pages (no Go): select
// and brush gestures becoming htmx requests with their parameters, the
// keyboard equivalents and their announcements, the client without htmx
// (an inline chart and an inline standalone chart), a slide on SSE messages
// with an open <details> table that stays open, and the JavaScript d tween
// (forced, since Chrome's CSS transitions d itself). Pages run under a
// strict Content-Security-Policy, and every test fails on a page error.
//
// Run with `npm run test:browser` (see harness.mjs).
import { after, before, test } from 'node:test'
import assert from 'node:assert/strict'
import { readFileSync } from 'node:fs'
import { cleanup, fixture, launch, startStatic, waitFor } from './harness.mjs'

let server
let browser
let page

const LINE = fixture('line-time/chart.svg') // id lt, plot 24..228 × 8..98, 14:00 to 14:20 UTC
const TABLE = fixture('line-time/table.html')
const FRAME_A = fixture('slide/frame-a.svg') // id sl; frame-b slides it by 5 minutes
const FRAME_B = fixture('slide/frame-b.svg')
const PREVIEW = readFileSync(new URL('../../testdata/preview/lines_direct_labels.svg', import.meta.url), 'utf8')

// bump returns the line-time chart with its line's values changed and its
// x window kept: an update that a CSS d transition (or the tween) animates.
const bump = (svg, dy) => svg.replace(/(class="hp-line" d=")([^"]+)/, (_, a, d) => a + d.replace(/,(\d+)/g, (m, y) => ',' + (+y + dy)))

const head = (scripts) =>
  `<!doctype html><html lang="en"><head><meta charset="utf-8"><title>hx-plot test</title>` +
  `<meta name="htmx-config" content='{"noSwap":[204,304,"4xx","5xx"]}'>` +
  `<link rel="stylesheet" href="/plot.css">` +
  scripts.map((s) => `<script src="/${s}.js"></script>`).join('') +
  `</head><body>`

before(async () => {
  server = await startStatic()
  server.page(
    '/gestures',
    head(['htmx', 'hx-plot', 'hx-plot-brush']) +
      `<div class="zoom" hx-get="/zoom" hx-trigger="htmx:plot:brush" hx-target="#latency" hx-swap="innerMorph" hx-sync="replace" hx-plot:brush>` +
      `<div id="latency" hx-plot:autosize hx-get="/drill" hx-trigger="htmx:plot:select" hx-target="#detail" hx-swap="innerHTML">${LINE}</div>` +
      `</div><p id="detail"></p></body></html>`,
  )
  server.frames('/drill', ['<b>selected</b>'])
  server.frames('/zoom', [LINE])
  server.page(
    '/alone',
    head(['hx-plot', 'hx-plot-brush']) + `<div hx-plot:brush>${LINE}</div><div id="preview">${PREVIEW}</div></body></html>`,
    // The standalone chart carries its own <style>, which a strict policy blocks.
    { csp: false },
  )
  server.page(
    '/live',
    head(['htmx', 'hx-sse', 'hx-plot']) + `<div id="stream" hx-sse:connect="/sse" hx-swap="innerMorph">${FRAME_A}<details><summary>Table</summary>${TABLE}</details></div></body></html>`,
  )
  const live = (svg) => `${svg}<details><summary>Table</summary>${TABLE}</details>`
  server.sse('/sse', [live(FRAME_B), live(FRAME_A), live(FRAME_B), live(FRAME_A)], 700)
  server.page(
    '/tween',
    head(['htmx', 'hx-plot', 'hx-plot-tween']) + `<div id="poll" hx-get="/values" hx-trigger="every 800ms" hx-swap="innerMorph">${LINE}</div></body></html>`,
  )
  browser = await launch(server.url)
  page = browser.page
})

after(cleanup)

const noErrors = () => assert.deepEqual(browser.errors.map((e) => e.message), [], 'page errors')

// at returns the client coordinates of viewBox point (x, y) on chart id.
const at = (id, x, y) =>
  page.evaluate(
    ([id, x, y]) => {
      const p = new DOMPoint(x, y).matrixTransform(document.getElementById(id).getScreenCTM())
      return [p.x, p.y]
    },
    [id, x, y],
  )

const requests = (path) => server.log.filter((r) => r.path === path).map((r) => r.params)

// until waits for the test server to have answered n requests for path.
async function until(path, n, what) {
  const end = Date.now() + 10000
  while (requests(path).length < n) {
    if (Date.now() > end) throw new Error(`timed out waiting for ${what}`)
    await new Promise((r) => setTimeout(r, 50))
  }
  // Give a stray second request (there must be none) time to arrive.
  await new Promise((r) => setTimeout(r, 300))
}

test('a click and Enter select; the request carries the key, never the size of an unrelated target', async () => {
  await page.goto(server.url + '/gestures')
  await waitFor(page, () => window.hyperplot?._ && window.htmx, null, { what: 'the extension' })
  const [x, y] = await at('lt', 130, 50)
  await page.mouse.move(x, y)
  await page.mouse.click(x, y)
  await waitFor(page, () => document.querySelector('#detail b'), null, { what: 'the drill-down response' })
  await until('/drill', 1, 'the select request')
  assert.deepEqual(requests('/drill'), [{ 'hp-chart': 'lt', 'hp-x': '2026-03-03T14:10:00Z' }])

  await page.focus('#lt')
  await page.keyboard.press('End')
  await page.keyboard.press('Enter')
  await until('/drill', 2, 'the keyboard select request')
  assert.deepEqual(requests('/drill'), [
    { 'hp-chart': 'lt', 'hp-x': '2026-03-03T14:10:00Z' },
    { 'hp-chart': 'lt', 'hp-x': '2026-03-03T14:20:00Z' },
  ])
  noErrors()
})

test('a pointer drag brushes: one request with the range and the target’s width, no select, no text selection', async () => {
  await page.goto(server.url + '/gestures')
  await waitFor(page, () => window.hyperplot?._ && window.htmx, null, { what: 'the extension' })
  await page.evaluate(() => {
    window.__brush = []
    document.addEventListener('htmx:plot:brush', (e) => {
      const r = document.querySelector('#lt rect.hp-brush')
      window.__brush.push({ detail: e.detail, rect: r && ['x', 'width'].map((a) => +r.getAttribute(a)) })
    })
  })
  const drills = requests('/drill').length
  const [x0, y] = await at('lt', 75, 50)
  const [x1] = await at('lt', 177, 50)
  await page.mouse.move(x0, y)
  await page.mouse.down()
  for (let i = 1; i <= 8; i++) await page.mouse.move(x0 + ((x1 - x0) * i) / 8, y)
  await page.mouse.up()
  await waitFor(page, () => window.__brush.length, null, { what: 'htmx:plot:brush' })
  const [got] = await page.evaluate(() => window.__brush)
  assert.deepEqual(got.detail, { chart: 'lt', x0: '2026-03-03T14:05:00Z', x1: '2026-03-03T14:15:00Z' })
  assert.deepEqual(got.rect, [75, 102])
  await until('/zoom', 1, 'the brush request')
  const zoom = requests('/zoom')
  assert.equal(zoom.length, 1)
  const width = await page.evaluate(() => {
    const el = document.getElementById('latency')
    const cs = getComputedStyle(el)
    return String(Math.round(el.clientWidth - parseFloat(cs.paddingLeft) - parseFloat(cs.paddingRight)))
  })
  assert.deepEqual(zoom[0], { 'hp-w': width, 'hp-chart': 'lt', 'hp-x0': '2026-03-03T14:05:00Z', 'hp-x1': '2026-03-03T14:15:00Z' })
  assert.equal(requests('/drill').length, drills, 'the click that ended the drag selected nothing')
  assert.equal(await page.evaluate(() => getSelection().toString()), '', 'no text selection')
  noErrors()
})

test('Shift+Arrow marks a range, announces it, and Enter brushes it', async () => {
  await page.goto(server.url + '/gestures')
  await waitFor(page, () => window.hyperplot?._ && window.htmx, null, { what: 'the extension' })
  const before = requests('/zoom').length
  await page.focus('#lt')
  await page.keyboard.press('Home')
  await page.keyboard.press('Shift+ArrowRight')
  await page.keyboard.press('Shift+ArrowRight')
  const state = await page.evaluate(() => {
    const r = document.querySelector('#lt rect.hp-brush')
    return { live: document.querySelector('.hp-live').textContent, rect: ['x', 'width'].map((a) => +r.getAttribute(a)) }
  })
  assert.equal(state.live, 'Mar 3, 2026 2:00 PM to Mar 3, 2026 2:20 PM')
  assert.deepEqual(state.rect, [24, 204])
  await page.keyboard.press('Enter')
  await until('/zoom', before + 1, 'the keyboard brush request')
  assert.deepEqual(requests('/zoom').slice(before).map((p) => [p['hp-x0'], p['hp-x1']]), [['2026-03-03T14:00:00Z', '2026-03-03T14:20:00Z']])
  noErrors()
})

test('without htmx: tooltips, crosshair, keyboard reading and gesture events, also on a standalone chart', async () => {
  await page.goto(server.url + '/alone')
  await waitFor(page, () => window.hyperplot?._, null, { what: 'the script' })
  assert.equal(await page.evaluate(() => typeof window.htmx), 'undefined')
  await page.evaluate(() => {
    window.__events = []
    for (const t of ['htmx:plot:select', 'htmx:plot:brush']) document.addEventListener(t, (e) => window.__events.push([e.type, e.detail]))
  })
  const [x, y] = await at('lt', 130, 50)
  await page.mouse.move(x, y)
  await waitFor(page, () => document.querySelector('.hp-tip')?.style.display === '', null, { what: 'the tooltip' })
  const shown = await page.evaluate(() => ({
    tip: document.querySelector('.hp-tip .hp-tip-t').textContent,
    cross: document.querySelector('#lt .hp-crosshair').getAttribute('visibility'),
  }))
  assert.deepEqual(shown, { tip: 'Mar 3, 2026 2:10 PM', cross: 'visible' })
  await page.mouse.click(x, y)

  await page.focus('#lt')
  await page.keyboard.press('End')
  assert.match(await page.evaluate(() => document.querySelector('.hp-live').textContent), /^Mar 3, 2026 2:20 PM: /)

  const [b0, by] = await at('lt', 75, 50)
  const [b1] = await at('lt', 177, 50)
  await page.mouse.move(b0, by)
  await page.mouse.down()
  await page.mouse.move(b1, by, { steps: 6 })
  await page.mouse.up()
  const events = await page.evaluate(() => window.__events)
  assert.deepEqual(events, [
    ['htmx:plot:select', { chart: 'lt', x: '2026-03-03T14:10:00Z', series: '', index: 1 }],
    ['htmx:plot:brush', { chart: 'lt', x0: '2026-03-03T14:05:00Z', x1: '2026-03-03T14:15:00Z' }],
  ])

  // The standalone chart, inline: hover and keyboard work the same.
  const id = await page.evaluate(() => document.querySelector('#preview svg').id)
  const box = await page.evaluate((id) => {
    const t = JSON.parse(document.getElementById(id).querySelector('metadata.hp-tips').textContent)
    return [t.xs[1], (t.top + t.bottom) / 2, t.rows[1].t]
  }, id)
  const [px, py] = await at(id, box[0], box[1])
  await page.mouse.move(px, py)
  await waitFor(page, (t) => document.querySelector('.hp-tip .hp-tip-t')?.textContent === t, box[2], { what: 'the standalone chart’s tooltip' })
  await page.focus('#' + id)
  await page.keyboard.press('Home')
  assert.notEqual(await page.evaluate(() => document.querySelector('.hp-live').textContent), '')
  noErrors()
})

test('SSE messages slide the chart, and an opened <details> stays open', async () => {
  await page.goto(server.url + '/live')
  await waitFor(page, () => window.hyperplot?._ && window.htmx, null, { what: 'the extension' })
  await page.click('#stream summary')
  // Record, on every frame, whether a slide runs on the line and whether the
  // drawn path is its own d (no d transition between windows).
  await page.evaluate(() => {
    window.__live = { slides: 0, dDrift: 0, closed: 0, swaps: 0 }
    document.addEventListener('htmx:after:swap', () => window.__live.swaps++)
    const tick = () => {
      const path = document.querySelector('#sl path.hp-line')
      const anims = path?.getAnimations() ?? []
      if (anims.some((a) => a.effect.getKeyframes().some((k) => /translateX/.test(k.transform)))) window.__live.slides++
      const drawn = path && getComputedStyle(path).d.match(/-?[\d.]+/g).map(Number)
      const attr = path?.getAttribute('d').match(/-?[\d.]+/g).map(Number)
      if (drawn && attr && drawn.some((v, i) => Math.abs(v - attr[i]) > 0.01)) window.__live.dDrift++
      if (!document.querySelector('#stream details').open) window.__live.closed++
      requestAnimationFrame(tick)
    }
    requestAnimationFrame(tick)
  })
  await waitFor(page, () => window.__live.swaps >= 3, null, { what: 'three SSE messages', timeout: 15000 })
  const live = await page.evaluate(() => window.__live)
  assert.ok(live.slides > 0, 'the line slid')
  assert.equal(live.dDrift, 0, 'no frame drew a d between windows')
  assert.equal(live.closed, 0, 'the details stayed open')
  noErrors()
})

// sampleTween records, on every animation frame, the line's d attribute and
// whether CSS draws exactly that d, until n swaps have happened.
async function sampleTween(n) {
  await page.evaluate((n) => {
    window.__tw = { frames: [], swaps: 0, done: false }
    document.addEventListener('htmx:after:swap', () => window.__tw.swaps++)
    const tick = () => {
      const path = document.querySelector('#lt path.hp-line')
      const attr = path.getAttribute('d')
      const drawn = getComputedStyle(path).d.match(/-?[\d.]+/g).map(Number)
      const same = attr.match(/-?[\d.]+/g).every((v, i) => Math.abs(v - drawn[i]) <= 0.01)
      window.__tw.frames.push({ attr, same, swaps: window.__tw.swaps })
      if (window.__tw.swaps < n) requestAnimationFrame(tick)
      else window.__tw.done = true
    }
    requestAnimationFrame(tick)
  }, n)
  await waitFor(page, () => window.__tw.done, null, { what: 'the polls', timeout: 20000 })
  return page.evaluate(() => window.__tw.frames)
}

test('the JavaScript tween, forced: d moves through intermediate paths with the commands kept, and CSS draws each', async () => {
  server.frames('/values', [bump(LINE, 20), LINE, bump(LINE, -15), LINE, bump(LINE, 20), LINE])
  await page.goto(server.url + '/tween')
  await waitFor(page, () => window.hyperplot?.tween && window.htmx, null, { what: 'the tween companion' })
  await page.evaluate(() => {
    window.hyperplot.tween.force = true
    window.htmx.config.plot = { duration: 500 }
  })
  const frames = await sampleTween(3)
  const ends = new Set([bump(LINE, 20), LINE, bump(LINE, -15)].map((s) => /class="hp-line" d="([^"]+)"/.exec(s)[1]))
  const between = frames.filter((f) => !ends.has(f.attr))
  assert.ok(between.length >= 5, `intermediate paths: ${between.length}`)
  for (const f of between) assert.match(f.attr, /^M[\d.]+,[\d.]+L[\d.]+,[\d.]+L[\d.]+,[\d.]+$/)
  // x never moves in a value tween; y is between the two ends.
  assert.ok(between.every((f) => /^M24,[\d.]+L126,[\d.]+L228,/.test(f.attr)))
  assert.ok(frames.every((f) => f.same), 'the stylesheet’s d transition never draws something else')
  noErrors()
})

test('where CSS transitions d, the tween stays out of the way', async () => {
  server.frames('/values', [bump(LINE, 20), LINE, bump(LINE, -15), LINE, bump(LINE, 20), LINE])
  await page.goto(server.url + '/tween')
  await waitFor(page, () => window.hyperplot?.tween && window.htmx, null, { what: 'the tween companion' })
  const frames = await sampleTween(2)
  const attrs = new Set(frames.map((f) => f.attr))
  assert.ok(attrs.size <= 3, `the d attribute only takes the frames’ values: ${[...attrs].join(' | ')}`)
  assert.ok(
    frames.some((f) => !f.same),
    'CSS draws a transition',
  )
  noErrors()
})
