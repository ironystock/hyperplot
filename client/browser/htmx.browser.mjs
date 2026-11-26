// The extension's htmx hooks under the real htmx 4.0.2 (the node suites
// drive them through a stub), on static test pages (no Go): Back with htmx's
// hx-history-cache and with core history after a boosted link, a focused
// chart swapped with innerHTML and outerHTML, innerMorph with
// transition:true, hx-ptag 304 polls, the data-hx-plot spelling with
// metaCharacter "-", live tables (follow from a box the table still fits,
// and a pointer tooltip that survives the follow scroll), a chart in a modal
// <dialog>, a click on a line of a brushable chart, Escape during a drag,
// and an element named "hyperplot". Every test fails on a page error.
//
// Run with `npm run test:browser` (see harness.mjs).
import { after, before, test } from 'node:test'
import assert from 'node:assert/strict'
import { cleanup, fixture, launch, startStatic, waitFor } from './harness.mjs'

let server
let browser
let page

const LINE = fixture('line-time/chart.svg') // id lt, plot 24..228 × 8..98, 14:00 to 14:20 UTC, series p50
const FRAME_A = fixture('slide/frame-a.svg') // id sl; frame-b slides it by 5 minutes
const FRAME_B = fixture('slide/frame-b.svg')

const CONFIG = { noSwap: [204, 304, '4xx', '5xx'] }
const head = (scripts, config = CONFIG, style = '') =>
  `<!doctype html><html lang="en"><head><meta charset="utf-8"><title>hx-plot htmx test</title>` +
  `<meta name="htmx-config" content='${JSON.stringify(config)}'>` +
  `<link rel="stylesheet" href="/plot.css">${style}` +
  scripts.map((s) => `<script src="/${s}.js"></script>`).join('') +
  `</head><body>`

// rows returns a live table with n rows, as a server would write it.
const rows = (n) =>
  `<table class="hp-table" id="g-table"><caption>Log</caption><thead><tr><th scope="col">Row</th></tr></thead><tbody>` +
  Array.from({ length: n }, (_, i) => `<tr id="g-r-${i}"><th scope="row">Row ${i}</th></tr>`).join('') +
  `</tbody></table>`

before(async () => {
  server = await startStatic()
  const core = ['htmx', 'hx-plot']

  // Back with hx-history-cache, after a select that pushed a URL.
  server.page(
    '/hc',
    head(['htmx', 'ext/hx-history-cache', 'hx-plot']) +
      `<div id="w" hx-get="/hc2" hx-trigger="htmx:plot:select" hx-target="body" hx-push-url="/hc2">${LINE}</div></body></html>`,
  )
  server.frames('/hc2', ['<p id="p2">page 2</p>'])

  // A boosted link, then Back (core history: the page is fetched again).
  server.page('/boost', head(core) + `<div>${LINE}</div><a id="go" href="/boost2" hx-boost="true">next</a></body></html>`)
  server.page('/boost2', head(core) + `<p id="b2">page 2</p></body></html>`)

  // A focused chart that innerHTML and outerHTML swaps replace.
  server.page('/inner', head(core) + `<div id="w" hx-get="/same" hx-trigger="every 600ms" hx-swap="innerHTML">${LINE}</div></body></html>`)
  server.frames('/same', [LINE])
  const outer = `<div id="w" hx-get="/outer" hx-trigger="every 600ms" hx-swap="outerHTML">${LINE}</div>`
  server.page('/outer-page', head(core) + outer + `</body></html>`)
  server.frames('/outer', [outer])

  // innerMorph with a view transition: no slide on top of the cross-fade.
  server.page('/vt', head(core) + `<div id="w" hx-get="/vt-frames" hx-trigger="every 700ms" hx-swap="innerMorph transition:true">${FRAME_A}</div></body></html>`)
  server.frames('/vt-frames', [FRAME_B, FRAME_A, FRAME_B, FRAME_A, FRAME_B])

  // hx-ptag: the first poll answers 200 with a ptag, every later one 304.
  server.page(
    '/ptag',
    head(['htmx', 'ext/hx-ptag', 'hx-plot']) +
      `<div id="w" hx-get="/ptag-frame" hx-trigger="every 400ms" hx-swap="innerMorph">${LINE}</div></body></html>`,
  )
  server.handle('/ptag-frame', (req, res) => {
    if (req.headers['hx-ptag'] === 'v1') return res.writeHead(304, { 'cache-control': 'no-store' }).end()
    res.writeHead(200, { 'content-type': 'text/html; charset=utf-8', 'cache-control': 'no-store', 'hx-ptag': 'v1' })
    res.end(LINE)
  })

  // The data- prefixed attribute with metaCharacter "-".
  server.page(
    '/meta',
    head(core, { ...CONFIG, metaCharacter: '-' }) +
      `<div id="w" data-hx-plot-autosize hx-get="/meta-frag" hx-trigger="htmx-plot-select" hx-swap="none">${LINE}</div></body></html>`,
  )
  server.frames('/meta-frag', ['<b>ok</b>'])

  // Live tables. The pages need a <style> element, so no strict policy.
  const growing = (n) => LINE + `<div class="box">${rows(n)}</div>`
  server.page(
    '/follow-tip',
    head(core, CONFIG, '<style>.box{overflow-y:auto;height:60px}</style>') +
      `<div id="w" hx-get="/follow-tip-frames" hx-trigger="every 600ms" hx-swap="innerMorph">${growing(10)}</div></body></html>`,
    { csp: false },
  )
  server.frames('/follow-tip-frames', Array.from({ length: 30 }, (_, i) => growing(11 + i)))
  server.page(
    '/follow-fit',
    head(core, CONFIG, '<style>.box{overflow-y:auto;max-height:150px}</style>') +
      `<div id="w" hx-get="/follow-fit-frames" hx-trigger="every 300ms" hx-swap="innerMorph"><div class="box">${rows(1)}</div></div></body></html>`,
    { csp: false },
  )
  server.frames(
    '/follow-fit-frames',
    Array.from({ length: 30 }, (_, i) => `<div class="box">${rows(2 + i)}</div>`),
  )

  // A chart in a modal dialog.
  server.page('/dialog', head(core) + `<dialog id="dlg">${LINE}</dialog><script src="/open-dialog.js"></script></body></html>`)
  server.handle('/open-dialog.js', (req, res) => {
    res.writeHead(200, { 'content-type': 'text/javascript; charset=utf-8', 'cache-control': 'no-store' })
    res.end('document.getElementById("dlg").showModal()')
  })

  // Gestures on a brushable chart, without requests.
  server.page('/brush', head(['htmx', 'hx-plot', 'hx-plot-brush']) + `<div hx-plot:brush>${LINE}</div></body></html>`)

  // An element that clobbers window.hyperplot, before the scripts.
  server.page(
    '/clobber',
    `<!doctype html><html lang="en"><head><meta charset="utf-8"><title>clobber</title></head><body>` +
      `<p>comment: <img name="hyperplot" alt="" src="data:,"></p><div id="hyperplot"></div><div hx-plot:brush>${LINE}</div>` +
      `<script src="/htmx.js"></script><script src="/hx-plot.js"></script><script src="/hx-plot-brush.js"></script></body></html>`,
  )

  browser = await launch(server.url)
  page = browser.page
})

after(cleanup)

const noErrors = () => assert.deepEqual(browser.errors.splice(0).map((e) => e.message), [], 'page errors')
const ready = (what = 'the extension') => waitFor(page, () => window.hyperplot?._ && window.htmx, null, { what })

// at returns the client coordinates of viewBox point (x, y) on chart id.
const at = (id, x, y) =>
  page.evaluate(
    ([id, x, y]) => {
      const p = new DOMPoint(x, y).matrixTransform(document.getElementById(id).getScreenCTM())
      return [p.x, p.y]
    },
    [id, x, y],
  )

// state returns what the client owns on the page: every tooltip (computed
// display and text), the status regions' texts, and the crosshair.
const state = () =>
  page.evaluate(() => ({
    tips: [...document.querySelectorAll('.hp-tip')].map((t) => [getComputedStyle(t).display, t.textContent]),
    lives: [...document.querySelectorAll('[role="status"].hp-live')].map((l) => l.textContent),
    cross: document.querySelector('#lt .hp-crosshair')?.getAttribute('visibility') ?? null,
    active: document.activeElement?.id ?? '',
  }))

// shown counts the tooltips on screen.
const shown = (s) => s.tips.filter(([d]) => d !== 'none').length

// swaps waits for n more htmx:after:swap events.
async function swaps(n, timeout = 15000) {
  await page.evaluate(() => {
    window.__swaps = 0
    document.addEventListener('htmx:after:swap', () => window.__swaps++)
  })
  await waitFor(page, (n) => window.__swaps >= n, n, { what: `${n} swaps`, timeout })
}

const hover = async (id, x, y) => {
  const [px, py] = await at(id, x, y)
  await page.mouse.move(px, py)
  await waitFor(page, () => [...document.querySelectorAll('.hp-tip')].some((t) => t.style.display === ''), null, { what: 'the tooltip' })
}

test('hx-history-cache: Back restores the page without a stale tooltip, crosshair or second status region', async () => {
  await page.goto(server.url + '/hc')
  await ready()
  await hover('lt', 126, 50)
  const [x, y] = await at('lt', 126, 50)
  await page.mouse.click(x, y)
  await page.waitForSelector('#p2')
  assert.equal(new URL(page.url()).pathname, '/hc2')
  await page.goBack()
  await page.waitForSelector('#lt')
  assert.equal(new URL(page.url()).pathname, '/hc')
  // Restored from the cache, not fetched again.
  assert.equal(server.log.filter((r) => r.path === '/hc').length, 1, 'restored from hx-history-cache')
  await page.mouse.move(5, 590)
  await page.waitForTimeout(300)
  let s = await state()
  assert.equal(shown(s), 0, `no tooltip on screen: ${JSON.stringify(s.tips)}`)
  assert.equal(s.lives.length, 1, 'one status region')
  assert.equal(s.cross, 'hidden')

  // The restored chart works like a fresh one: one tooltip, then none.
  await hover('lt', 228, 50)
  s = await state()
  assert.equal(s.tips.length, 1)
  assert.equal(shown(s), 1)
  await page.mouse.move(5, 590)
  await waitFor(page, () => document.querySelector('.hp-tip').style.display === 'none', null, { what: 'the tooltip to hide' })
  await page.focus('#lt')
  await page.keyboard.press('End')
  s = await state()
  assert.equal(s.lives.length, 1)
  assert.match(s.lives[0], /^Mar 3, 2026 2:20 PM: /)
  noErrors()
})

test('a boosted link, then Back (core history): one tooltip and one status region', async () => {
  await page.goto(server.url + '/boost')
  await ready()
  await hover('lt', 126, 50)
  await page.click('#go')
  await page.waitForSelector('#b2')
  await page.goBack()
  await page.waitForSelector('#lt')
  await ready()
  await page.mouse.move(5, 590)
  await page.waitForTimeout(300)
  let s = await state()
  assert.equal(shown(s), 0)
  assert.ok(s.lives.length <= 1, `at most one status region: ${s.lives.length}`)
  await hover('lt', 24, 50)
  s = await state()
  assert.equal(shown(s), 1)
  noErrors()
})

for (const [name, path] of [
  ['innerHTML', '/inner'],
  ['outerHTML', '/outer-page'],
]) {
  test(`a focused chart replaced by ${name} keeps its keyboard point and its tooltip`, async () => {
    await page.goto(server.url + path)
    await ready()
    await page.focus('#lt')
    await page.keyboard.press('Home')
    await page.keyboard.press('ArrowRight')
    assert.match((await state()).lives[0], /^Mar 3, 2026 2:10 PM: /)
    const before = await page.evaluate(() => (window.__old = document.getElementById('lt')) && 1)
    assert.equal(before, 1)
    await swaps(2)
    await page.waitForTimeout(100)
    const s = await state()
    assert.ok(await page.evaluate(() => !window.__old.isConnected), 'the chart node was replaced')
    assert.equal(s.active, 'lt', 'focus is on the new chart')
    assert.equal(shown(s), 1, `the tooltip stays: ${JSON.stringify(s.tips)}`)
    assert.match(s.tips[0][1], /^Mar 3, 2026 2:10 PM/)
    assert.equal(s.cross, 'visible')
    await page.keyboard.press('ArrowRight')
    assert.match((await state()).lives[0], /^Mar 3, 2026 2:20 PM: /, 'the next step continues from the kept point')
    noErrors()
  })
}

test('innerMorph with transition:true swaps without a slide on top of the view transition', async () => {
  await page.goto(server.url + '/vt')
  await ready()
  await page.evaluate(() => {
    window.__slid = 0
    const tick = () => {
      for (const el of document.querySelectorAll('#sl *')) {
        if (el.getAnimations().some((a) => a.effect?.getKeyframes().some((k) => /translateX/.test(k.transform ?? '')))) window.__slid++
      }
      requestAnimationFrame(tick)
    }
    requestAnimationFrame(tick)
  })
  await swaps(3)
  assert.equal(await page.evaluate(() => window.__slid), 0, 'no slide')
  // The window did move: the frames alternate.
  assert.ok(await page.evaluate(() => document.getElementById('sl')?.getAttribute('data-hp-x')))
  noErrors()
})

test('hx-ptag: 304 polls leave the chart, its tooltip and crosshair alone', async () => {
  await page.goto(server.url + '/ptag')
  await ready()
  const start = server.log.length
  await hover('lt', 126, 50)
  const end = Date.now() + 10000
  while (server.log.slice(start).filter((r) => r.path === '/ptag-frame' && r.headers['hx-ptag'] === 'v1').length < 3) {
    assert.ok(Date.now() < end, 'three conditional polls')
    await page.waitForTimeout(100)
  }
  const s = await state()
  assert.equal(shown(s), 1, 'the tooltip stays through the 304s')
  assert.equal(s.cross, 'visible')
  noErrors()
})

test('data-hx-plot-autosize with metaCharacter "-": a click sends hp-w and the select parameters', async () => {
  await page.goto(server.url + '/meta')
  await ready()
  const [x, y] = await at('lt', 126, 50)
  await page.mouse.click(x, y)
  const end = Date.now() + 10000
  while (!server.log.some((r) => r.path === '/meta-frag')) {
    assert.ok(Date.now() < end, 'the select request')
    await page.waitForTimeout(50)
  }
  await page.waitForTimeout(200)
  const req = server.log.filter((r) => r.path === '/meta-frag').map((r) => r.params)
  assert.equal(req.length, 1)
  assert.equal(req[0]['hp-chart'], 'lt')
  assert.equal(req[0]['hp-x'], '2026-03-03T14:10:00Z')
  assert.match(req[0]['hp-w'] ?? '', /^\d+$/)
  noErrors()
})

test('a followed live table scrolls without hiding the pointer tooltip', async () => {
  await page.goto(server.url + '/follow-tip')
  await ready()
  await page.evaluate(() => {
    const box = document.querySelector('.box')
    box.scrollTop = box.scrollHeight
  })
  await hover('lt', 126, 50)
  await page.evaluate(() => {
    window.__seen = []
    document.addEventListener('htmx:after:swap', () =>
      setTimeout(() => {
        const box = document.querySelector('.box')
        window.__seen.push([document.querySelector('.hp-tip').style.display, box.scrollHeight - box.clientHeight - box.scrollTop])
      }, 100),
    )
  })
  await waitFor(page, () => window.__seen.length >= 4, null, { what: 'four swaps', timeout: 15000 })
  const seen = await page.evaluate(() => window.__seen)
  assert.ok(
    seen.every(([d, left]) => d === '' && left <= 2),
    `the table followed and the tooltip stayed: ${JSON.stringify(seen)}`,
  )
  noErrors()
})

test('a live table that starts shorter than its box follows its end once it overflows', async () => {
  await page.goto(server.url + '/follow-fit')
  await ready()
  await page.evaluate(() => {
    window.__trace = []
    document.addEventListener('htmx:after:swap', () =>
      setTimeout(() => {
        const box = document.querySelector('.box')
        window.__trace.push([box.querySelectorAll('tbody tr').length, box.scrollTop, box.scrollHeight - box.clientHeight])
      }, 50),
    )
  })
  await waitFor(page, () => window.__trace.length >= 10, null, { what: 'ten swaps', timeout: 15000 })
  const trace = await page.evaluate(() => window.__trace)
  const last = trace.at(-1)
  assert.ok(last[2] > 50, `the table overflows its box by now: ${JSON.stringify(trace)}`)
  assert.ok(Math.abs(last[1] - last[2]) <= 2, `scrolled to its end: ${JSON.stringify(trace)}`)
  noErrors()
})

test('a chart in a modal dialog: the tooltip is painted on top, and steps are announced', async () => {
  await page.goto(server.url + '/dialog')
  await ready()
  assert.ok(await page.evaluate(() => document.getElementById('dlg').matches(':modal')))
  await hover('lt', 126, 50)
  const parent = await page.evaluate(() => document.querySelector('.hp-tip').parentElement.id)
  assert.equal(parent, 'dlg', 'the tooltip is in the dialog, in the top layer')
  // Painted: hiding it changes the pixels where it overlaps the dialog.
  const clip = await page.evaluate(() => {
    const t = document.querySelector('.hp-tip').getBoundingClientRect()
    const d = document.getElementById('dlg').getBoundingClientRect()
    const x = Math.max(t.left, d.left)
    const y = Math.max(t.top, d.top)
    return { x, y, width: Math.min(t.right, d.right) - x, height: Math.min(t.bottom, d.bottom) - y }
  })
  assert.ok(clip.width > 4 && clip.height > 4, `the tooltip overlaps the dialog: ${JSON.stringify(clip)}`)
  const visible = await page.screenshot({ clip })
  await page.evaluate(() => (document.querySelector('.hp-tip').style.visibility = 'hidden'))
  const hidden = await page.screenshot({ clip })
  await page.evaluate(() => (document.querySelector('.hp-tip').style.visibility = ''))
  assert.ok(!visible.equals(hidden), 'the tooltip is visible over the dialog')

  await page.mouse.move(5, 5)
  await page.focus('#lt')
  await page.keyboard.press('ArrowRight')
  assert.match((await state()).lives[0] ?? '', /^Mar 3, 2026 2:00 PM: /)
  const cdp = await page.context().newCDPSession(page)
  try {
    const { nodes } = await cdp.send('Accessibility.getFullAXTree')
    const status = nodes.filter((n) => n.role?.value === 'status' && !n.ignored)
    assert.equal(status.length, 1, 'the status region is exposed')
  } finally {
    await cdp.detach()
  }
  noErrors()
})

test('a click on a line of a brushable chart names its series; Escape during a drag selects nothing', async () => {
  await page.goto(server.url + '/brush')
  await ready()
  await page.evaluate(() => {
    window.__sel = []
    window.__brush = []
    document.addEventListener('htmx:plot:select', (e) => window.__sel.push(e.detail))
    document.addEventListener('htmx:plot:brush', (e) => window.__brush.push(e.detail))
  })
  // A point on the p50 line, a third of the way along it.
  const [lx, ly] = await page.evaluate(() => {
    const path = document.querySelector('#lt path.hp-line')
    const p = path.getPointAtLength(path.getTotalLength() / 3).matrixTransform(path.getScreenCTM())
    return [p.x, p.y]
  })
  assert.equal(await page.evaluate(([x, y]) => document.elementFromPoint(x, y)?.getAttribute('class'), [lx, ly]), 'hp-line')
  await page.mouse.move(lx, ly)
  await page.mouse.down()
  await page.mouse.up()
  let sel = await page.evaluate(() => window.__sel)
  assert.equal(sel.length, 1)
  assert.equal(sel[0].series, 'p50', 'pointer capture moved the click to the svg; the press named the series')

  // Escape mid-drag: no brush, and the release selects nothing.
  const [x0, y] = await at('lt', 60, 50)
  const [x1] = await at('lt', 180, 50)
  await page.mouse.move(x0, y)
  await page.mouse.down()
  await page.mouse.move(x1, y, { steps: 5 })
  await page.keyboard.press('Escape')
  await page.mouse.up()
  await page.waitForTimeout(200)
  sel = await page.evaluate(() => window.__sel)
  assert.equal(sel.length, 1, 'no select from the cancelled drag')
  assert.deepEqual(await page.evaluate(() => window.__brush), [])
  assert.equal(await page.locator('#lt rect.hp-brush').count(), 0)
  noErrors()
})

test('elements named or with the id "hyperplot" do not stop the extension', async () => {
  await page.goto(server.url + '/clobber')
  await ready()
  assert.equal(await page.evaluate(() => window.hyperplot.version), '0.2.0')
  assert.ok(await page.evaluate(() => !!window.hyperplot.brush), 'the brush companion started')
  await page.focus('#lt')
  await page.keyboard.press('ArrowRight')
  assert.match((await state()).lives[0] ?? '', /^Mar 3, 2026 2:00 PM: /)
  noErrors()
})
