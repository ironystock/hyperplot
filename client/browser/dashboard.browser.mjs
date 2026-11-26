// The dashboard's gesture demos in a real browser (examples/dashboard): a
// drag across the latency chart zooms it through the server, polls stay
// zoomed, Back in the browser undoes the zoom, the from/to form and "Back
// to live" do the same without a pointer; a click on a bar, or Enter on a
// focused one, opens that window's detail, and Close closes it. Each step
// checks the live region the server updates, and every test fails on a page
// error (the page runs under a strict Content-Security-Policy).
//
// Run with `npm run test:browser` (see harness.mjs).
import { after, before, test } from 'node:test'
import assert from 'node:assert/strict'
import { cleanup, launch, shot, startDashboard, waitFor } from './harness.mjs'

let server
let browser

const HOUR = 3600e3

before(async () => {
  server = await startDashboard()
  browser = await launch(server.url)
  const { page } = browser
  await page.goto(server.url + '/?range=1h')
  await waitFor(page, () => window.htmx && document.querySelector('#latency')?.getAttribute('width') !== '640', null, {
    what: 'the latency chart at its real width',
  })
})

after(cleanup)

// domain returns the latency chart's x domain in epoch ms, from data-hp-x.
const domain = (page) =>
  page.evaluate(() => {
    const [kind, d0, d1] = document.getElementById('latency').getAttribute('data-hp-x').split(' ')
    return { kind, d0: +d0, d1: +d1 }
  })

const text = (page, sel) => page.evaluate((sel) => document.querySelector(sel)?.textContent.trim() ?? null, sel)

// waitZoomed waits until the latency chart's domain spans less than span ms.
const waitZoomed = (page, span) =>
  waitFor(
    page,
    (span) => {
      const [, d0, d1] = document.getElementById('latency').getAttribute('data-hp-x').split(' ')
      return d1 - d0 < span && location.search.includes('from=')
    },
    span,
    { what: 'the zoomed latency chart' },
  )

// waitLive waits until the latency chart shows the live hour again.
const waitLive = (page) =>
  waitFor(
    page,
    (hour) => {
      const el = document.getElementById('latency')
      if (!el) return false
      const [, d0, d1] = el.getAttribute('data-hp-x').split(' ')
      return d1 - d0 === hour && !location.search.includes('from=')
    },
    HOUR,
    { what: 'the live latency chart' },
  )

test('a drag across the latency chart zooms it; polls stay zoomed; Back undoes it', async () => {
  const { page, errors } = browser
  const live = await domain(page)
  assert.equal(live.d1 - live.d0, HOUR, 'the live window is an hour')
  const box = await page.locator('#latency').boundingBox()
  const [left, top, right, bottom] = await page.evaluate(() =>
    document.getElementById('latency').getAttribute('data-hp-plot').split(' ').map(Number),
  )
  const vb = await page.evaluate(() => document.getElementById('latency').viewBox.baseVal.width)
  const k = box.width / vb
  const x = (f) => box.x + (left + f * (right - left)) * k
  const y = box.y + ((top + bottom) / 2) * k
  await page.mouse.move(x(0.4), y)
  await page.mouse.down()
  for (let i = 1; i <= 10; i++) await page.mouse.move(x(0.4 + (0.3 * i) / 10), y)
  await page.mouse.up()
  await waitZoomed(page, HOUR / 2)

  const zoomed = await domain(page)
  const want = 0.3 * HOUR
  assert.equal(zoomed.kind, 'time')
  assert.ok(Math.abs(zoomed.d1 - zoomed.d0 - want) < 5000, `the zoom spans the dragged 18 minutes (got ${(zoomed.d1 - zoomed.d0) / 1000} s)`)
  assert.ok(Math.abs(zoomed.d0 - (live.d0 + 0.4 * HOUR)) < 60e3, 'the zoom starts where the drag started')
  assert.match(await text(page, '#latency-zoom-status'), /^Zoomed to .+ \(1[78] minutes(?: \d+ seconds?)?, 10-second buckets\)\.$/)
  const pollURL = await page.getAttribute('#latency-body', 'hx-get')
  assert.match(pollURL, /&from=.+&to=/, 'the polling element carries the zoom')
  assert.equal(await page.locator('#latency rect.hp-brush').count(), 0, 'the brush rectangle is gone after the zoom')
  await shot(page.locator('.card.wide'), 'dashboard-zoomed')

  // Polls keep coming (every 2 s), carry the zoom, and keep it: count two
  // that finish after the zoom (a fixed sleep would pass with no poll).
  const pollPath = new URL(pollURL, server.url).pathname
  const polls = []
  const onPoll = (req) => {
    const u = new URL(req.url())
    if (u.pathname === pollPath) polls.push(Object.fromEntries(u.searchParams))
  }
  page.on('requestfinished', onPoll)
  try {
    const end = Date.now() + 10000
    while (polls.length < 2 && Date.now() < end) await page.waitForTimeout(100)
  } finally {
    page.off('requestfinished', onPoll)
  }
  assert.ok(polls.length >= 2, `two polls after the zoom (got ${polls.length})`)
  for (const p of polls) assert.ok(p.from && p.to, `each poll carries the zoom: ${JSON.stringify(p)}`)
  assert.deepEqual(await domain(page), zoomed, 'still zoomed after two polls')
  assert.equal(await page.getAttribute('#latency-body', 'hx-get'), pollURL)

  await page.goBack()
  await waitLive(page)
  await waitFor(page, () => window.htmx && document.querySelector('#latency')?.getAttribute('width') !== '640', null, {
    what: 'the restored page at its real width',
  })
  assert.match(await text(page, '#latency-zoom-status'), /^Live: last hour\.$/)
  assert.deepEqual(errors, [])
})

test('a keyboard zoom (Shift+arrows, Enter) undone with Back keeps focus on the chart', async () => {
  const { page, errors } = browser
  // Twice: with the keyboard point still shown when Back is pressed (the
  // extension finds the chart again by id), and after Escape closed it, so
  // only the page's own focus return (app.js) brings focus back.
  for (const escape of [false, true]) {
    await waitLive(page)
    await page.evaluate(() => (window.__sameDocument = true))
    await page.focus('#latency')
    await page.keyboard.press('End')
    for (let i = 0; i < 8; i++) await page.keyboard.press('Shift+ArrowLeft')
    await page.keyboard.press('Enter')
    await waitZoomed(page, HOUR / 2)
    assert.equal(await page.evaluate(() => document.activeElement?.id), 'latency', 'the zoomed chart keeps focus')
    await page.waitForTimeout(2500) // a reader takes a look; polls land
    if (escape) await page.keyboard.press('Escape')

    // htmx restores the live page in the same document, replacing the
    // children of <body>; focus used to fall to <body>.
    await page.goBack()
    await waitLive(page)
    assert.equal(await page.evaluate(() => window.__sameDocument), true, 'Back restored the page without a reload')
    await waitFor(page, () => document.activeElement?.id === 'latency', null, { what: `focus back on the latency chart (escape: ${escape})` })
    // The keyboard reader works again: a key shows a point.
    await page.keyboard.press('Home')
    await waitFor(page, () => document.querySelector('div.hp-tip')?.style.display !== 'none' && document.querySelector('div.hp-tip')?.textContent, null, {
      what: 'a keyboard tooltip on the restored chart',
    })
    await page.keyboard.press('Escape')
  }
  assert.deepEqual(errors, [])
})

test('the from/to form zooms without a pointer, and "Back to live" returns', async () => {
  const { page, errors } = browser
  const live = await domain(page)
  // The form holds the live window in the chart's time zone; keep its date
  // and take the ten minutes that end five minutes before the window does.
  const local = (ms) => {
    const d = new Date(ms)
    const p = (n) => String(n).padStart(2, '0')
    return `${d.getFullYear()}-${p(d.getMonth() + 1)}-${p(d.getDate())}T${p(d.getHours())}:${p(d.getMinutes())}:${p(d.getSeconds())}`
  }
  // The dashboard's zone is the machine's, as is the browser's.
  const t1 = Math.floor((live.d1 - 5 * 60e3) / 1000) * 1000
  await page.fill('input[name="hp-x0"]', local(t1 - 10 * 60e3))
  await page.fill('input[name="hp-x1"]', local(t1))
  await page.click('.zoom-form button[type="submit"]')
  await waitZoomed(page, HOUR / 2)
  const z = await domain(page)
  assert.deepEqual([z.d0, z.d1], [t1 - 10 * 60e3, t1], 'the chart shows exactly the typed range')
  assert.match(await text(page, '#latency-zoom-status'), /^Zoomed to .+ \(10 minutes, 5-second buckets\)\.$/)
  await page.click('text=Back to live')
  await waitLive(page)
  assert.match(await text(page, '#latency-zoom-status'), /^Live: last hour\.$/)
  assert.equal(await page.locator('text=Back to live').count(), 0)
  assert.deepEqual(errors, [])
})

test('a click on a bar opens its window for that region; Enter opens it for all; Close closes it', async () => {
  const { page, errors } = browser
  const bar = page.locator('#requests g.hp-series[data-hp-series="eu-west"] > path.hp-bar').nth(2)
  await bar.scrollIntoViewIfNeeded()
  const b = await bar.boundingBox()
  await page.mouse.move(b.x + b.width / 2, b.y + b.height / 2)
  await page.mouse.click(b.x + b.width / 2, b.y + b.height / 2)
  await waitFor(page, () => document.querySelector('#requests-detail .detail-card'), null, { what: 'the window detail' })
  const label = await page.evaluate(() => JSON.parse(document.querySelector('#requests metadata.hp-tips').textContent).rows[2].k)
  assert.equal(await text(page, '#requests-detail-heading'), `Window from ${label}`)
  assert.match(await text(page, '#requests-detail .card-sub'), /^eu-west · /)
  assert.match(await text(page, '#requests-detail-status'), new RegExp(`^Showing requests for eu-west from ${label} to `))
  assert.deepEqual(
    await page.evaluate(() => [...document.querySelectorAll('#drill g.hp-series')].map((g) => g.dataset.hpSeries)),
    ['eu-west'],
  )
  // The detail was rendered at the detail container's width (hp-w).
  const [w, cw] = await page.evaluate(() => {
    const el = document.getElementById('requests-detail')
    return [+document.getElementById('drill').getAttribute('width'), el.clientWidth]
  })
  assert.ok(Math.abs(w - cw) <= 1, `the detail chart is ${w} px wide in a ${cw} px container`)
  await shot(page.locator('#requests-detail'), 'dashboard-detail')

  await page.click('#requests-detail a:text("Close")')
  await waitFor(page, () => !document.querySelector('#requests-detail .detail-card'), null, { what: 'the detail to close' })

  // Keyboard: focus the chart, step to the last window, Enter.
  await page.focus('#requests')
  await page.keyboard.press('End')
  await page.keyboard.press('Enter')
  await waitFor(page, () => document.querySelector('#requests-detail .detail-card'), null, { what: 'the keyboard detail' })
  const last = await page.evaluate(() => JSON.parse(document.querySelector('#requests metadata.hp-tips').textContent).rows.at(-1).k)
  assert.equal(await text(page, '#requests-detail-heading'), `Window from ${last}`)
  assert.match(await text(page, '#requests-detail .card-sub'), /^us-east, eu-west and ap-south · /)
  // Close from the keyboard: the link removes itself, and focus goes back
  // to the chart instead of falling to <body> (app.js, data-focus-return).
  await page.focus('#requests-detail a:text("Close")')
  await page.keyboard.press('Enter')
  await waitFor(page, () => !document.querySelector('#requests-detail .detail-card'), null, { what: 'the detail to close' })
  await waitFor(page, () => document.activeElement?.id === 'requests', null, { what: 'focus back on the requests chart' })
  assert.deepEqual(errors, [])
})

test('every chart is drawn at its container’s width, also at phone width; one stream is open', async () => {
  const { context } = browser
  const page = await context.newPage()
  const errors = []
  page.on('pageerror', (e) => errors.push(e))
  page.on('console', (m) => m.type() === 'error' && errors.push(new Error(m.text())))
  const opened = []
  const ended = []
  page.on('request', (r) => r.url().includes('/stream/throughput') && opened.push(r.url()))
  page.on('requestfinished', (r) => r.url().includes('/stream/throughput') && ended.push(r.url()))
  page.on('requestfailed', (r) => r.url().includes('/stream/throughput') && ended.push(r.url()))
  try {
    await page.setViewportSize({ width: 390, height: 844 })
    await page.goto(server.url + '/?range=1h')
    const fitted = () => {
      const out = []
      for (const id of ['latency', 'throughput', 'requests', 'scatter']) {
        const svg = document.getElementById(id)
        const box = svg?.closest('.chart')
        if (!svg || !box) return null
        const cs = getComputedStyle(box)
        const w = Math.round(box.clientWidth - parseFloat(cs.paddingLeft) - parseFloat(cs.paddingRight))
        out.push([id, +svg.getAttribute('viewBox').split(' ')[2], w])
      }
      return out.every(([, a, b]) => Math.abs(a - b) <= 1) && out
    }
    // The throughput chart used to stay at the page's default width (640),
    // scaled down to half size here, because htmx:plot:resize fires only
    // when the container's width changes.
    const widths = await waitFor(page, fitted, null, { what: 'every chart at its container’s width' })
    assert.ok(widths, 'fitted')
    await waitFor(page, () => document.querySelector('#throughput-body .stream[hx-sse\\:connect]'), null, { what: 'the stream' })
    assert.equal(opened.length - ended.length, 1, `streams open: ${opened.length} opened, ${ended.length} ended`)
    assert.deepEqual(errors, [])
  } finally {
    await page.close()
  }
})

test('the window links open the same detail', async () => {
  const { page, errors } = browser
  const link = page.locator('#requests-body .drill-links a').first()
  const label = (await link.textContent()).trim()
  await link.click()
  await waitFor(page, () => document.querySelector('#requests-detail .detail-card'), null, { what: 'the linked detail' })
  assert.equal(await text(page, '#requests-detail-heading'), `Window from ${label}`)
  assert.ok(!page.url().includes('hp-x'), 'htmx handled the link; the page did not navigate')
  await page.click('#requests-detail a:text("Close")')
  assert.deepEqual(errors, [])
})
