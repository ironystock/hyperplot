// Every example in a real browser (examples/README.md): the Go dashboard,
// sensors, builds and gallery, the ten Go recipes, and the Python and Node
// servers. Each one is started on a free port in turn, opened in one Chrome
// (reused from example to example), and checked for what any reader would
// notice first:
//
//   - the page answers 200 and draws all its charts, each with a size;
//   - nothing fails: no page error, no console error (a Content-Security-
//     Policy violation is one; every example sends a strict policy), no
//     failed request and no 4xx or 5xx response;
//   - the page loads hx-plot.js (all but sparkline-table, which runs no
//     script) and keyboard reading works on a chart;
//   - a live example updates a chart on its own (a poll or an SSE message
//     with new data), and a gesture example answers its gesture (a brush
//     zooms, a select opens the detail, a filter change redraws).
//
// The per-example behaviour is tested in depth by each example's own tests
// (go test, unittest, node --test) and the dashboard's browser tests; this
// file checks that every example still works end to end in a browser.
// standalone-svg writes files rather than serving, so its files are served
// here and opened directly and through <img>.
//
// Run with `npm run test:browser` (see harness.mjs). The Python example
// needs Python 3.10+ (PYTHON, default python3; python on Windows).
import { after, before, test } from 'node:test'
import assert from 'node:assert/strict'
import { spawnSync } from 'node:child_process'
import { readdirSync, readFileSync } from 'node:fs'
import { join } from 'node:path'
import { buildGo, cleanup, launch, python, shot, startServer, startStatic, tempDir, waitFor } from './harness.mjs'

// Simulated time runs this many times faster in the examples that take a
// speed flag, so a live chart's window moves within a few seconds.
const SPEED = '30'

// What counts as a chart: a chart (svg.hp, docs/spec.md §1) or a
// sparkline (svg.hp-spark with role="img", as Sparkline draws it). The
// 10x10 color-key swatches of the dashboard and builds pages are
// svg.hp-spark too, but aria-hidden decorations with no role, so they
// cannot stand in for a chart that failed to draw.
const CHARTS = 'svg.hp[data-hp], svg.hp-spark[role="img"]'

// The examples, in the order of examples/README.md. go: a main package
// under examples/; cmd: any other command. charts: how many charts
// (CHARTS) the page draws on load; it must draw exactly that many, each
// with a size. kbd: false for a page that loads no hx-plot.js; every
// other page must load it and answer the keyboard. live: a chart that
// must change on its own. check: the example's gesture or feature.
const examples = [
  { name: 'python', cmd: [python, 'examples/python/server.py', '--speed', SPEED], addr: '--addr', charts: 3, live: '#throughput-body svg.hp', shot: true },
  { name: 'node', cmd: ['node', 'examples/node/server.mjs', '--speed', SPEED], addr: '--addr', charts: 3, live: '#throughput-body svg.hp', shot: true },
  { name: 'dashboard', go: 'dashboard', args: ['-speed', SPEED], charts: 8, live: '#throughput' },
  { name: 'sensors', go: 'sensors', args: ['-speed', SPEED], charts: 14, live: '#tiles svg.hp-spark', shot: true },
  { name: 'builds', go: 'builds', args: ['-speed', SPEED], charts: 5, check: selectDay, shot: true },
  { name: 'gallery', go: 'gallery', charts: 25, check: darkTheme, shot: true },
  { name: 'polling-ptag', go: 'recipes/polling-ptag', charts: 1, live: '#connections', liveTimeout: 25000, check: notModified },
  { name: 'sse-stream', go: 'recipes/sse-stream', charts: 1, live: '#throughput' },
  { name: 'filters-form', go: 'recipes/filters-form', charts: 3, check: filterChange },
  { name: 'select-drilldown', go: 'recipes/select-drilldown', charts: 1, check: selectService },
  { name: 'brush-zoom', go: 'recipes/brush-zoom', charts: 1, check: brushZoom },
  { name: 'static-page', go: 'recipes/static-page', charts: 2, check: hover },
  { name: 'sizing', go: 'recipes/sizing', charts: 3, check: autosized },
  { name: 'theming', go: 'recipes/theming', charts: 2, check: darkTheme },
  { name: 'sparkline-table', go: 'recipes/sparkline-table', charts: 10, kbd: false },
  // standalone-svg writes files: its own test below.
]

let bins
let browser
// The current example's requests that failed or answered 4xx/5xx, and its
// responses. Recording stops before the page is left, since leaving
// aborts the open stream and any poll in flight.
const seen = { on: false, failed: [], responses: [] }

before(async () => {
  bins = buildGo([...examples.filter((e) => e.go).map((e) => './examples/' + e.go), './examples/recipes/standalone-svg'])
  // Every example listens on 127.0.0.1; requests anywhere else are
  // refused, apart from the pinned htmx files (see launch).
  browser = await launch('http://127.0.0.1:')
  const { page } = browser
  page.on('requestfailed', (r) => seen.on && seen.failed.push(`${r.url()}: ${r.failure()?.errorText}`))
  page.on('response', (r) => {
    if (!seen.on) return
    seen.responses.push({ url: r.url(), status: r.status() })
    if (r.status() >= 400) seen.failed.push(`${r.url()}: HTTP ${r.status()}`)
  })
})

after(cleanup)

for (const ex of examples) {
  test(ex.name, async () => {
    const { page } = browser
    const [cmd, ...args] = ex.go ? [bins.bin('./examples/' + ex.go), ...(ex.args ?? [])] : ex.cmd
    const server = await startServer(cmd, [...args, ex.addr ?? '-addr', '127.0.0.1:0'], { name: ex.name })
    try {
      await open(page, server.url + '/', ex)
      if (ex.live) await updates(page, ex.live, ex.liveTimeout)
      await ex.check?.(page, server.url)
      if (ex.shot) await shot(page.locator('body'), 'example-' + ex.name)
      assertClean(ex.name)
    } finally {
      await leave(page)
      await server.stop()
    }
  })
}

test('standalone-svg', async () => {
  const { page } = browser
  const out = tempDir('hp-standalone-')
  const run = spawnSync(bins.bin('./examples/recipes/standalone-svg'), ['-out', out], { encoding: 'utf8' })
  assert.equal(run.status, 0, run.stderr)
  const files = readdirSync(out)
  const svgs = files.filter((f) => f.endsWith('.svg'))
  assert.ok(svgs.length >= 2, `expected SVG files, got ${files}`)
  // Served as a static host would serve them; index.html gets the strict
  // policy of the other examples, so its <img> tags must be same-origin.
  const server = await startStatic()
  const types = { '.svg': 'image/svg+xml', '.css': 'text/css; charset=utf-8' }
  for (const f of files) {
    const type = types[f.slice(f.lastIndexOf('.'))]
    if (type) server.handle('/' + f, (req, res) => res.writeHead(200, { 'content-type': type }).end(readFileSync(join(out, f))))
  }
  server.page('/index.html', readFileSync(join(out, 'index.html'), 'utf8'))
  try {
    record()
    await page.goto(server.url + '/index.html')
    const images = await waitFor(
      page,
      (n) => {
        const imgs = [...document.images]
        return imgs.length >= n && imgs.every((i) => i.complete) && imgs.map((i) => [i.getAttribute('src'), i.naturalWidth])
      },
      svgs.length,
      { what: 'the images on index.html' },
    )
    for (const [src, w] of images) assert.ok(w > 0, `${src} did not render`)
    for (const f of svgs) {
      await page.goto(server.url + '/' + f)
      const root = await page.evaluate(() => {
        const r = document.documentElement
        const box = r.getBoundingClientRect()
        return { name: r.localName, hp: r.getAttribute('data-hp'), w: box.width, h: box.height }
      })
      assert.equal(root.name, 'svg', f)
      assert.equal(root.hp, '1', `${f} is a chart (data-hp)`)
      assert.ok(root.w > 0 && root.h > 0, `${f} has a size`)
    }
    assertClean('standalone-svg')
  } finally {
    await leave(page)
    await server.close()
  }
})

// record forgets the last example's errors and requests and starts
// recording the next one's.
function record() {
  browser.errors.length = 0
  seen.failed.length = 0
  seen.responses.length = 0
  seen.on = true
}

// open loads url and checks what every example must do.
async function open(page, url, ex) {
  record()
  const res = await page.goto(url)
  assert.equal(res.status(), 200, `${url} answered ${res.status()}`)
  await page.evaluate(installSignature)
  // Every chart the page has must be drawn with a size: one hidden by a
  // CSS rule or a failed swap has a zero box.
  const count = () =>
    page.evaluate((sel) => {
      const all = [...document.querySelectorAll(sel)]
      const drawn = all.filter((s) => {
        const r = s.getBoundingClientRect()
        return r.width > 0 && r.height > 0
      })
      const name = (s) => (s.id ? '#' + s.id : (s.getAttribute('aria-label') ?? s.outerHTML.slice(0, 60)))
      return { all: all.length, drawn: drawn.length, hidden: all.filter((s) => !drawn.includes(s)).map(name) }
    }, CHARTS)
  try {
    await waitFor(
      page,
      ([sel, n]) =>
        [...document.querySelectorAll(sel)].filter((s) => {
          const r = s.getBoundingClientRect()
          return r.width > 0 && r.height > 0
        }).length >= n,
      [CHARTS, ex.charts],
      { what: `${ex.charts} drawn charts`, timeout: 15000 },
    )
  } finally {
    const c = await count()
    assert.deepEqual(
      { charts: c.all, drawn: c.drawn },
      { charts: ex.charts, drawn: ex.charts },
      `${ex.name}: expected ${ex.charts} charts, all drawn; hidden: ${c.hidden.join(', ') || 'none'}`,
    )
  }
  // The page loads hx-plot.js unless the example says it has no script,
  // so a page that drops its script tag fails here rather than skipping
  // the check below.
  const loaded = await page.evaluate(() => Boolean(window.hyperplot))
  assert.equal(loaded, ex.kbd !== false, ex.kbd === false ? `${ex.name} should load no hx-plot.js` : `${ex.name} does not load hx-plot.js`)
  if (ex.kbd === false) return
  // Keyboard reading (spec §6.5): Home on a chart with data announces its
  // first row in the live region.
  const id = await page.evaluate(() => {
    const chart = [...document.querySelectorAll('svg.hp[data-hp][id]')].find((s) => {
      const tips = s.querySelector('metadata.hp-tips')
      return tips && JSON.parse(tips.textContent).rows.length > 0
    })
    return chart?.id ?? null
  })
  assert.ok(id, `${ex.name}: no chart with an id and data to read with the keyboard`)
  await page.focus(`[id="${id}"]`)
  await page.keyboard.press('Home')
  await waitFor(page, () => [...document.querySelectorAll('.hp-live')].some((l) => l.textContent.trim() !== ''), null, {
    what: `keyboard reading on #${id}`,
    timeout: 5000,
  })
  await page.keyboard.press('Escape')
}

// leave stops recording and leaves the page, which closes its streams.
async function leave(page) {
  seen.on = false
  await page.goto('about:blank').catch(() => {})
}

function assertClean(name) {
  assert.deepEqual(browser.errors.map((e) => e.message), [], `${name}: page or console errors`)
  assert.deepEqual(seen.failed, [], `${name}: failed requests`)
}

// installSignature defines window.__hpSignature(sel) in the page: what the
// chart sel shows, independent of its size: the rows of its hover payload
// (spec §3.5), or the markup of a chart without one (a sparkline). It is
// installed by an evaluate, since the pages' policy forbids eval.
function installSignature() {
  window.__hpSignature = (sel) => {
    const el = document.querySelector(sel)
    if (!el) return null
    const tips = el.querySelector('metadata.hp-tips')
    return tips ? JSON.stringify(JSON.parse(tips.textContent).rows) : el.innerHTML
  }
}

// changed waits until the chart sel shows different data than first.
const changed = (page, sel, first, opts) =>
  waitFor(
    page,
    ([sel, first]) => {
      const sig = window.__hpSignature(sel)
      return sig && sig !== first
    },
    [sel, first],
    opts,
  )

// updates waits until the chart sel shows different data on its own.
async function updates(page, sel, timeout = 15000) {
  const first = await page.evaluate((sel) => window.__hpSignature(sel), sel)
  assert.ok(first, `no ${sel}`)
  await changed(page, sel, first, { what: `a live update of ${sel}`, timeout })
}

// plotPoint returns page coordinates at fraction f across the plot of
// chart id, halfway down.
async function plotPoint(page, id, f) {
  const box = await page.locator('#' + id).boundingBox()
  const [vb, left, top, right, bottom] = await page.evaluate((id) => {
    const el = document.getElementById(id)
    return [el.viewBox.baseVal.width, ...el.getAttribute('data-hp-plot').split(' ').map(Number)]
  }, id)
  const k = box.width / vb
  return [box.x + (left + f * (right - left)) * k, box.y + ((top + bottom) / 2) * k]
}

// polling-ptag: the polls go out, and unchanged data answers 304. The data
// moves in real 10-second buckets and the page polls every 2 s, so a poll
// answers 304 only when it falls in the same bucket as the render it
// echoes the tag of. Which polls those are depends on the wall clock: if
// the page loads in the last 2 s of a bucket, the first poll already
// crosses into the next one and answers 200 with new data. So wait for a
// 304 rather than expecting one among the polls so far: the poll after
// any 200 is in that 200's bucket, so one comes within one poll interval;
// 12 s (more than a bucket plus a poll) leaves room for a slow machine.
async function notModified() {
  const polls = () => seen.responses.filter((r) => r.url.includes('/charts/connections'))
  for (let waited = 0; waited < 12000 && !polls().some((r) => r.status === 304); waited += 100) {
    await new Promise((r) => setTimeout(r, 100))
  }
  assert.ok(
    polls().some((r) => r.status === 304),
    `expected a 304 among the polls within 12 s, got ${polls().map((r) => r.status)}`,
  )
}

// brush-zoom: a drag across the chart zooms it through the server and
// pushes the range into the URL (spec §5.3).
async function brushZoom(page) {
  const span = () =>
    page.evaluate(() => {
      const [, d0, d1] = document.getElementById('latency').getAttribute('data-hp-x').split(' ')
      return d1 - d0
    })
  const before = await span()
  const [x0, y] = await plotPoint(page, 'latency', 0.3)
  const [x1] = await plotPoint(page, 'latency', 0.7)
  await page.mouse.move(x0, y)
  await page.mouse.down()
  await page.mouse.move(x1, y, { steps: 10 })
  await page.mouse.up()
  await waitFor(
    page,
    (before) => {
      const [, d0, d1] = document.getElementById('latency').getAttribute('data-hp-x').split(' ')
      return d1 - d0 < before * 0.7 && location.search.includes('from=')
    },
    before,
    { what: 'the zoomed chart' },
  )
}

// select-drilldown: Enter on a keyboard point selects that bar (spec
// §6.6), and the server answers with the service's detail chart.
async function selectService(page) {
  await page.focus('#errors')
  await page.keyboard.press('Home')
  await page.keyboard.press('Enter')
  await waitFor(page, () => document.getElementById('service-errors'), null, { what: 'the detail chart' })
}

// builds: the same drill-down on the days chart.
async function selectDay(page) {
  await page.focus('#days')
  await page.keyboard.press('End')
  await page.keyboard.press('Enter')
  await waitFor(page, () => document.getElementById('day-detail-heading'), null, { what: 'the day detail' })
}

// filters-form: one change to the form redraws the charts and pushes the
// filtered view into the URL.
async function filterChange(page) {
  const first = await page.evaluate(() => window.__hpSignature('#latency'))
  await page.locator('#filters input[type="checkbox"]').first().click()
  await changed(page, '#latency', first, { what: 'the filtered charts' })
  assert.match(await page.evaluate(() => location.search), /region=/, 'the filtered view is in the URL')
}

// sizing: hx-plot:autosize fetches the chart at its box's width (hp-w,
// spec §6.7), and the server draws it at that width.
async function autosized(page) {
  const sent = () => {
    const r = seen.responses.findLast((r) => r.status === 200 && /\/charts\/width\?(?:.*&)?hp-w=\d+/.test(r.url))
    return r && new URL(r.url).searchParams.get('hp-w')
  }
  for (let i = 0; i < 100 && !sent(); i++) await new Promise((r) => setTimeout(r, 100))
  const w = sent()
  assert.ok(w, 'expected a request for /charts/width with hp-w')
  await waitFor(page, (w) => document.getElementById('width')?.getAttribute('width') === w, w, {
    what: `the autosized chart at width ${w}`,
  })
}

// static-page: hovering a chart shows hx-plot.js's tooltip without htmx.
async function hover(page) {
  const [x, y] = await plotPoint(page, 'latency', 0.5)
  await page.mouse.move(x, y)
  await waitFor(page, () => document.querySelector('.hp-tip')?.style.display === '' && document.querySelector('.hp-tip-t')?.textContent, null, {
    what: 'the tooltip',
  })
  await page.mouse.move(0, 0)
}

// gallery, theming: the dark link is a plain link; the server writes
// data-theme on <html> and the charts draw in the dark palette.
async function darkTheme(page, url) {
  const ink = () => page.evaluate(() => getComputedStyle(document.querySelector('svg.hp')).getPropertyValue('--hp-series-1'))
  const light = await ink()
  const res = await page.goto(url + '/?theme=dark')
  assert.equal(res.status(), 200)
  assert.equal(await page.evaluate(() => document.documentElement.dataset.theme), 'dark')
  assert.notEqual(await ink(), light, 'the dark page looks different')
}
