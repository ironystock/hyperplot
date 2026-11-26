// Two accessibility checks of the dashboard's own page (examples/dashboard)
// in a real browser, against a dashboard whose simulated clock runs 60
// times faster, so a 15-minute range closes a window every 3 seconds:
//
//   - The "Details" window links under the requests chart keep their
//     window across polls. They used to have no ids, so the innerMorph poll
//     paired them by position: a focused link kept focus but took the next
//     window's text and URL, and Enter opened a window the reader never
//     chose.
//   - The page reflows at 320 CSS px (WCAG 1.4.10, a 1280 px window at
//     400% zoom) without sideways scrolling; the header's theme switcher
//     used to stick out past the viewport.
//
// Run with `npm run test:browser` (see harness.mjs).
import { after, before, test } from 'node:test'
import assert from 'node:assert/strict'
import { cleanup, launch, startDashboard, waitFor } from './harness.mjs'

let server
let browser

before(async () => {
  server = await startDashboard({ speed: 60 })
  browser = await launch(server.url)
})

after(cleanup)

// labels returns the window links' labels, oldest first.
const labels = (page) => page.evaluate(() => [...document.querySelectorAll('#requests-body .drill-links a')].map((a) => a.textContent))

// waitSlide waits until the oldest window link is no longer first.
const waitSlide = (page, first, what) =>
  waitFor(page, (first) => {
    const a = document.querySelector('#requests-body .drill-links a')
    return a && a.textContent !== first
  }, first, { what })

test('a focused window link stays on its window as windows slide, and focus survives its removal', async () => {
  const { page, errors } = browser
  await page.goto(server.url + '/?range=15m')
  await waitFor(page, () => window.htmx && document.querySelectorAll('#requests-body .drill-links a').length === 5, null, {
    what: 'five window links',
  })

  // Focus the newest window: it stays listed through the next slides.
  const before = await page.evaluate(() => {
    const links = [...document.querySelectorAll('#requests-body .drill-links a')]
    for (const a of links) a.__window = [a.id, a.textContent, a.getAttribute('hx-get')]
    window.__links = links
    window.__focused = links.at(-1)
    window.__focused.focus()
    return links.map((a) => a.textContent)
  })
  assert.equal(await page.evaluate(() => document.activeElement === window.__focused), true, 'the link has focus')

  await waitSlide(page, before[0], 'the windows to slide')
  const after = await labels(page)
  assert.notDeepEqual(after, before, 'the windows slid')
  const state = await page.evaluate(() => {
    const a = window.__focused
    return {
      same: document.activeElement === a,
      connected: a.isConnected,
      now: [a.id, a.textContent, a.getAttribute('hx-get')],
      was: a.__window,
      last: a === [...document.querySelectorAll('#requests-body .drill-links a')].at(-1),
      // Every original node that is still in the page shows its own window.
      moved: window.__links.filter((l) => l.isConnected && l.textContent !== l.__window[1]).map((l) => `${l.__window[1]} -> ${l.textContent}`),
    }
  })
  assert.ok(state.same && state.connected, 'focus stays on the same link node')
  assert.deepEqual(state.now, state.was, 'the focused link still names the window it was focused on')
  assert.equal(state.last, false, 'a newer window was added after it')
  assert.deepEqual(state.moved, [], 'no link node took another window')

  // Focus the oldest window. When it slides out, its link is removed, and
  // focus moves to the oldest window still listed instead of <body>.
  const oldest = await page.evaluate(() => {
    const a = document.querySelector('#requests-body .drill-links a')
    window.__oldest = a
    a.focus()
    return a.textContent
  })
  await waitSlide(page, oldest, 'the oldest window to slide out')
  await waitFor(page, () => document.activeElement !== document.body, null, { what: 'focus to land after the slide' })
  const landed = await page.evaluate(() => ({
    removed: !window.__oldest.isConnected,
    first: document.activeElement === document.querySelector('#requests-body .drill-links a'),
  }))
  assert.deepEqual(landed, { removed: true, first: true }, 'the link that slid out is gone and focus is on the oldest window left')
  assert.deepEqual(errors, [])
})

test('the page reflows at 320 CSS px without sideways scrolling', async () => {
  const { context } = browser
  const page = await context.newPage()
  const errors = []
  page.on('pageerror', (e) => errors.push(e))
  page.on('console', (m) => m.type() === 'error' && errors.push(new Error(m.text())))
  try {
    await page.setViewportSize({ width: 320, height: 700 })
    await page.goto(server.url + '/?range=1h')
    // The theme switcher starts hidden until app.js shows it, and the charts
    // start at the page's default width until the extension sizes them.
    await waitFor(
      page,
      () => !document.querySelector('.seg.theme')?.hidden && ['latency', 'throughput', 'requests', 'scatter'].every((id) => document.getElementById(id)?.getAttribute('width') !== '640'),
      null,
      { what: 'the theme switcher and every chart at its real width' },
    )
    const m = await page.evaluate(() => {
      const de = document.documentElement
      const cw = de.clientWidth
      const over = []
      for (const el of document.body.querySelectorAll('*')) {
        // A chart's marks reach past its plot (the sliding window's
        // overscan) but are clipped there; the <svg> itself is checked.
        if (el.ownerSVGElement) continue
        const r = el.getBoundingClientRect()
        if (r.width > 0 && r.height > 0 && r.right > cw + 0.5) over.push(`${el.tagName}.${el.className?.baseVal ?? el.className} right=${Math.round(r.right)}`)
      }
      window.scrollTo(50, 0)
      return { scrollWidth: de.scrollWidth, clientWidth: cw, over, scrollX: window.scrollX }
    })
    assert.deepEqual(m.over, [], `nothing sticks out past ${m.clientWidth} px`)
    assert.ok(m.scrollWidth <= m.clientWidth, `no sideways scrolling: scrollWidth ${m.scrollWidth}, clientWidth ${m.clientWidth}`)
    assert.equal(m.scrollX, 0, 'the page cannot scroll sideways')
    assert.deepEqual(errors, [])
  } finally {
    await page.close()
  }
})
