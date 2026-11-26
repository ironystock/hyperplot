// Bug 1, "View as table does not update with its chart" (docs/DESIGN.md,
// Roadmap): in a real browser, a live table that the reader scrolled to its
// end keeps showing the newest rows as the chart's window slides, a reader
// in the middle keeps their rows, the DOM matches the chart after every
// update, and an opened <details> stays open across morphs.
//
// Run with `npm run test:browser` (see harness.mjs).
import { after, before, test } from 'node:test'
import assert from 'node:assert/strict'
import { cleanup, launch, shot, startDashboard, waitFor } from './harness.mjs'

let server
let browser

before(async () => {
  server = await startDashboard()
  browser = await launch(server.url)
  const { page } = browser
  await page.goto(server.url + '/?range=15m')
  // The first poll re-renders each chart at its container's real width.
  await waitFor(page, () => window.htmx && document.querySelector('#latency')?.getAttribute('width') !== '640', null, {
    what: 'the latency chart at its real width',
  })
  await page.evaluate(installView)
})

after(cleanup)

// installView defines window.__hpView(id) in the page. It reports, for the
// chart with that id, its newest data (from the chart's own payload) and
// what its table holds and shows. (The page's CSP forbids eval, so the
// tests call it by name.)
function installView() {
  window.__hpView = view
  function view(id) {
  const svg = document.getElementById(id)
  const tips = JSON.parse(svg.querySelector('metadata.hp-tips').textContent)
  const table = svg.parentElement.querySelector('table.hp-table')
  const box = table.closest('.table-scroll')
  const rows = [...table.tBodies[0].rows]
  const text = (r) => [...r.cells].map((c) => c.textContent.replace(' no data', '').trim())
  const b = box.getBoundingClientRect()
  const shown = rows.filter((r) => {
    const rb = r.getBoundingClientRect()
    return rb.top >= b.top - 1 && rb.bottom <= b.bottom + 1
  })
  const last = tips.rows.at(-1)
  return {
    chart: [last.t, ...last.v.map((e) => e[2])],
    table: text(rows.at(-1)),
    rows: rows.length,
    payloadRows: tips.rows.length,
    open: table.closest('details').open,
    atEnd: box.scrollHeight - box.scrollTop - box.clientHeight <= 2,
    first: shown.length ? text(shown[0])[0] : null,
    last: shown.length ? text(shown.at(-1))[0] : null,
  }
  }
}

const view = (page, id) => page.evaluate((id) => window.__hpView(id), id)

// open opens the table under a chart with a real click on its summary and
// scrolls it to where = "end" or "middle".
async function open(page, id, where) {
  await page.locator(`#${id}-body summary`).click()
  await page.evaluate(
    ([id, where]) => {
      const box = document.getElementById(id).parentElement.querySelector('.table-scroll')
      box.scrollIntoView({ block: 'center' })
      box.scrollTop = where === 'end' ? box.scrollHeight : Math.round((box.scrollHeight - box.clientHeight) / 2)
    },
    [id, where],
  )
}

// nextUpdate waits until the chart's newest row changes (a poll or SSE
// message that brought new data) and returns view(id) at the next frame.
async function nextUpdate(page, id) {
  const was = await view(page, id)
  await waitFor(page, ([id, was]) => JSON.stringify(window.__hpView(id).chart) !== was, [id, JSON.stringify(was.chart)], {
    what: `an update of #${id}`,
  })
  return view(page, id)
}

test('a latency table scrolled to its end shows each new row as the window slides', async () => {
  const { page, errors } = browser
  await open(page, 'latency', 'end')
  const views = []
  for (let i = 0; i < 3; i++) views.push(await nextUpdate(page, 'latency'))
  // The screenshot comes first, so a failing run shows what went wrong.
  await shot(page.locator('#latency-body'), 'bug1-latency-table')
  for (const [i, v] of views.entries()) {
    assert.deepEqual(v.table, v.chart, `update ${i}: the table's last row is the chart's newest data`)
    assert.equal(v.rows, v.payloadRows, `update ${i}: one row per payload row`)
    assert.equal(v.open, true, `update ${i}: the reader's <details> stays open`)
    assert.equal(v.atEnd, true, `update ${i}: still scrolled to the end`)
    assert.equal(v.last, v.chart[0], `update ${i}: the newest row is on screen`)
  }
  assert.deepEqual(errors, [])
})

test('a reader in the middle of the table keeps their rows', async () => {
  const { page } = browser
  await open(page, 'latency', 'middle')
  const start = await view(page, 'latency')
  for (let i = 0; i < 2; i++) {
    const v = await nextUpdate(page, 'latency')
    assert.deepEqual(v.table, v.chart, `update ${i}: the table is current`)
    assert.equal(v.first, start.first, `update ${i}: the first visible row stays`)
  }
})

test('the streamed throughput table follows its live row', async () => {
  const { page, errors } = browser
  await open(page, 'throughput', 'end')
  const seen = new Set()
  const views = []
  for (let i = 0; i < 4; i++) views.push(await nextUpdate(page, 'throughput'))
  await shot(page.locator('#throughput-body'), 'bug1-throughput-table')
  for (const [i, v] of views.entries()) {
    seen.add(v.chart.join('|'))
    assert.deepEqual(v.table, v.chart, `message ${i}: the table's last row is the chart's newest data`)
    assert.equal(v.open, true, `message ${i}: <details> stays open`)
    assert.equal(v.atEnd, true, `message ${i}: still scrolled to the end`)
    assert.equal(v.last, v.chart[0], `message ${i}: the live row is on screen`)
  }
  assert.ok(seen.size >= 4, 'every message brought new values')
  assert.deepEqual(errors, [])
})
