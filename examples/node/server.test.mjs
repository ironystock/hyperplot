// server.test.mjs: the server end to end, on a real port, with a clock the
// tests control. Every response's charts must parse, the page must carry
// the strict CSP, and each htmx behaviour the example teaches must work:
// 304s on a matching HX-PTag, zoom by brush, drill-down by select, SSE
// frames, and hp-w. Bad parameters must never cause a 5xx.
import assert from 'node:assert/strict'
import { readFileSync } from 'node:fs'
import { createServer } from 'node:http'
import net from 'node:net'
import { after, before, test } from 'node:test'
import { MINUTE } from './data.mjs'
import { CSP, createApp } from './server.mjs'
import { fixture, parseXml, select } from './testing.mjs'
import { timeValue } from './wire.mjs'

const T0 = Date.UTC(2026, 2, 3, 14, 0, 2) // 2 s into a 5 s bucket
let clock = T0
let base
let server

async function listen(app) {
  const s = createServer(app)
  await new Promise((ok) => s.listen(0, '127.0.0.1', ok))
  return [s, `http://127.0.0.1:${s.address().port}`]
}

async function stop(s) {
  s.closeAllConnections()
  await new Promise((ok) => s.close(ok))
}

before(async () => {
  [server, base] = await listen(createApp({ now: () => clock }))
})
after(() => stop(server))

const get = async (path, headers = {}) => {
  const res = await fetch(base + path, { headers: { 'HX-Request': 'true', ...headers } })
  return { res, body: await res.text() }
}

// charts returns every <svg> and hp-table in an HTML response, parsed.
function charts(body) {
  const svgs = body.match(/<svg[\s\S]*?<\/svg>/g) ?? []
  const tables = body.match(/<table class="hp-table"[\s\S]*?<\/table>/g) ?? []
  return [...svgs, ...tables].map((s) => parseXml(s))
}

// assertSafe checks what the CSP and spec §3.6 forbid in markup.
function assertSafe(body) {
  assert.doesNotMatch(body, /<[^>]*\s(style|on[a-z]+)=/i, 'no style= or on*= attributes')
  assert.doesNotMatch(body, /<style|<script>|<script(?![^>]*\ssrc=)/i, 'no inline script or style')
  assert.ok(!body.includes('\r'), 'no raw CR')
}

test('the page: 200, strict CSP, pinned htmx, charts that parse', async () => {
  const { res, body } = await get('/', { 'HX-Request': '' })
  assert.equal(res.status, 200)
  assert.equal(res.headers.get('content-type'), 'text/html; charset=utf-8')
  assert.equal(res.headers.get('content-security-policy'), CSP)
  assert.match(res.headers.get('vary'), /HX-Request/)
  assertSafe(body)
  // The same pinned htmx files, with the same SRI, as the Go dashboard.
  const dashboard = fixtureDashboard()
  for (const tag of dashboard) assert.ok(body.includes(tag), tag)
  const parsed = charts(body)
  assert.deepEqual(parsed.filter((el) => el.name === 'svg').map((el) => el.attrs.id), ['latency', 'throughput', 'requests'])
  assert.equal(parsed.filter((el) => el.name === 'table').length, 3)
  for (const svg of parsed.filter((el) => el.name === 'svg')) assert.equal(svg.attrs['data-hp'], '1')
})

test('the charts have the Python example\'s ids, so gestures send the same hp-chart', async () => {
  // The two examples are meant to be read side by side, and a gesture
  // sends the chart's id as hp-chart (spec §5.1): with the same ids,
  // Python's zoom form and select links work against this server too.
  const py = readFileSync(new URL('../python/server.py', import.meta.url), 'utf8')
  const pyPage = readFileSync(new URL('../python/page.html', import.meta.url), 'utf8')
  const pyIds = [...py.matchAll(/Chart\(id='([^']+)'/g)].map((m) => m[1])
  assert.deepEqual(pyIds, ['latency', 'throughput', 'requests', 'detail'])
  const { body } = await get('/?hp-chart=requests&hp-x=13:57', { 'HX-Request': '' })
  const ids = charts(body).filter((el) => el.name === 'svg').map((el) => el.attrs.id)
  assert.deepEqual(ids, pyIds)
  const hidden = (s) => s.match(/name="hp-chart" value="([^"]+)"/)[1]
  assert.equal(hidden(body), hidden(pyPage))
  assert.match(py, /'hp-chart': 'requests'/)
  assert.match(body, /hx-get="\/detail\/requests\?hp-chart=requests&amp;/)
  // Python's values are accepted here, and the old short ids are not ours.
  const brush = 'hp-x0=2026-03-03T13%3A50%3A00Z&hp-x1=2026-03-03T13%3A55%3A00Z'
  assert.equal((await get(`/charts/latency?hp-chart=${hidden(pyPage)}&${brush}`)).res.status, 200)
  assert.equal((await get(`/charts/latency?hp-chart=lt&${brush}`)).res.status, 204)
  assert.equal((await get('/detail/requests?hp-chart=requests&hp-x=13:57')).res.status, 200)
  assert.equal((await get('/detail/requests?hp-chart=rq&hp-x=13:57')).res.status, 204)
})

// fixtureDashboard returns the Go dashboard's pinned htmx script tags.
function fixtureDashboard() {
  const page = readFileSync(new URL('../dashboard/templates/page.html', import.meta.url), 'utf8')
  return page.match(/<script src="https:\/\/cdn[^>]*><\/script>/g)
}

test('the theme is a plain link; only known values are written', async () => {
  assert.match((await get('/?theme=dark')).body, /<html lang="en" data-theme="dark">/)
  assert.match((await get('/?theme=%22%3E%3Cb%3E')).body, /<html lang="en">/)
})

test('a poll: HX-PTag, then a bodiless 304 for the same tag in any spelling', async () => {
  const first = await get('/charts/latency?hp-w=800')
  assert.equal(first.res.status, 200)
  const tag = first.res.headers.get('hx-ptag')
  assert.match(tag, /^"hp-[0-9a-f]{16}"$/)
  assertSafe(first.body)
  assert.equal(charts(first.body)[0].attrs.viewBox, '0 0 800 260') // hp-w sets the width
  const bare = tag.slice(1, -1)
  for (const sent of [tag, `W/${tag}`, ` ${tag} `, bare]) {
    const again = await get('/charts/latency?hp-w=800', { 'HX-PTag': sent })
    assert.equal(again.res.status, 304, sent)
    assert.equal(again.body, '')
    assert.equal(again.res.headers.get('hx-ptag'), tag)
  }
  assert.equal((await get('/charts/latency?hp-w=800', { 'HX-PTag': '"hp-0000000000000000"' })).res.status, 200)
  // A new bucket is new content: the old tag no longer matches.
  clock += 5000
  assert.equal((await get('/charts/latency?hp-w=800', { 'HX-PTag': tag })).res.status, 200)
  clock = T0
})

test('hp-w is clamped, and junk gets the default', async () => {
  const width = async (q) => charts((await get(`/charts/requests?${q}`)).body)[0].attrs.width
  assert.equal(await width('hp-w=500'), '500')
  assert.equal(await width('hp-w=99999'), '4096')
  assert.equal(await width('hp-w=abc'), '640')
  assert.equal(await width('hp-w=120'), '120') // as wide as the box that asked (spec §4.6)
  assert.equal(await width('hp-w=10'), '50') // the spec's floor
  const viewBox = charts((await get('/charts/latency?hp-w=50')).body)[0].attrs.viewBox
  assert.equal(viewBox, '0 0 50 260')
})

// raw sends one request line over a plain socket (fetch would normalise
// the target) and returns the status line of the answer.
async function raw(target) {
  const { port } = new URL(base)
  const sock = net.connect(Number(port), '127.0.0.1')
  sock.write(`GET ${target} HTTP/1.1\r\nHost: x\r\nConnection: close\r\n\r\n`)
  let text = ''
  for await (const chunk of sock) text += chunk
  return text.split('\r\n')[0]
}

test('a request target new URL cannot parse is a 400, not a crash', async () => {
  for (const target of ['//', 'http://x:99999/', '//x:1:2/']) {
    assert.equal(await raw(target), 'HTTP/1.1 400 Bad Request', target)
  }
  assert.equal((await get('/charts/requests')).res.status, 200) // still serving
})

test('a brush zooms: the whole poller, the live region and HX-Push-Url', async () => {
  const x0 = timeValue(T0 - 2 * MINUTE)
  const x1 = timeValue(T0 - MINUTE)
  for (const q of [`hp-x0=${x0}&hp-x1=${x1}`, `hp-x1=${x0}&hp-x0=${x1}`]) { // either order (spec §5.3)
    const { res, body } = await get(`/charts/latency?hp-chart=latency&${q.replaceAll(':', '%3A')}&hp-w=700`)
    assert.equal(res.status, 200)
    const want = `from=${encodeURIComponent(x0)}&to=${encodeURIComponent(x1)}`
    assert.equal(res.headers.get('hx-push-url'), `/?${want}`)
    assert.ok(body.startsWith('<div id="latency-body"'))
    assert.ok(body.includes(`hx-get="/charts/latency?${want.replace('&', '&amp;')}"`))
    assert.match(body, /<hx-partial hx-target="#latency-status" hx-swap="innerHTML">Showing 13:58:02 to 13:59:02 UTC/)
    assert.equal(charts(body)[0].attrs['data-hp-x'], `time ${T0 - 2 * MINUTE} ${T0 - MINUTE}`)
    assertSafe(body)
  }
  // A form without script sends datetime-local values, read in UTC.
  const form = await get('/charts/latency?hp-chart=latency&hp-x0=2026-03-03T13%3A50&hp-x1=2026-03-03T13%3A55')
  assert.equal(form.res.status, 200)
  assert.equal(charts(form.body)[0].attrs['data-hp-x'], `time ${Date.UTC(2026, 2, 3, 13, 50)} ${Date.UTC(2026, 2, 3, 13, 55)}`)
  // Back to live.
  const out = await get('/charts/latency?zoom=out')
  assert.equal(out.res.headers.get('hx-push-url'), '/')
})

test('a zoomed poll is a fixed window: 304 as time goes by', async () => {
  const q = `from=${encodeURIComponent(timeValue(T0 - 2 * MINUTE))}&to=${encodeURIComponent(timeValue(T0 - MINUTE))}`
  const first = await get(`/charts/latency?${q}`)
  clock += 30_000
  const later = await get(`/charts/latency?${q}`, { 'HX-PTag': first.res.headers.get('hx-ptag') })
  clock = T0
  assert.equal(later.res.status, 304)
})

test('a short brush at the live edge ends now, and its polls match the seeded tag', async () => {
  clock = T0 + 400 // mid-second
  try {
    const q = `hp-chart=latency&hp-x0=${encodeURIComponent(timeValue(clock - 2000))}&hp-x1=${encodeURIComponent(timeValue(clock))}&hp-w=700`
    const zoom = await get(`/charts/latency?${q}`)
    assert.equal(zoom.res.status, 200)
    const [, from, to] = zoom.res.headers.get('hx-push-url').match(/from=([^&]+)&to=(.+)$/)
    const last = Math.floor(clock / 1000) * 1000
    assert.equal(decodeURIComponent(to), timeValue(last)) // not in the future
    assert.equal(decodeURIComponent(from), timeValue(last - 30_000)) // MIN_ZOOM wide
    // The new poller's hx-get and its seeded hx-ptag: every later poll of
    // that URL draws the same window, so it gets a 304.
    const src = zoom.body.match(/hx-get="([^"]+)"/)[1].replaceAll('&amp;', '&')
    const seeded = zoom.body.match(/hx-ptag="([^"]+)"/)[1].replaceAll('&#34;', '"').replaceAll('&quot;', '"')
    for (const dt of [0, 2000, 6000, 20_000]) {
      clock = T0 + 400 + dt
      const poll = await get(`${src}&hp-w=700`, { 'HX-PTag': seeded })
      assert.equal(poll.res.status, 304, `+${dt} ms`)
    }
  } finally {
    clock = T0
  }
})

test('brushes that do not parse, fall outside the data or name another chart are ignored', async () => {
  for (const q of ['hp-x0=bad&hp-x1=2', 'hp-x0=0001-01-01T00%3A00%3A00Z&hp-x1=0001-01-02T00%3A00%3A00Z',
    'hp-x0=9999-01-01T00%3A00%3A00Z&hp-x1=9999-12-31T00%3A00%3A00Z', 'hp-chart=throughput&hp-x0=2026-03-03T13%3A50%3A00Z&hp-x1=2026-03-03T13%3A55%3A00Z']) {
    assert.equal((await get(`/charts/latency?${q}`)).res.status, 204, q)
  }
})

test('a select drills down: hp-x is matched against our own categories', async () => {
  const label = '13:57' // a closed minute at T0
  const { res, body } = await get(`/detail/requests?hp-chart=requests&hp-x=${label}&hp-series=eu-west&hp-w=500`)
  assert.equal(res.status, 200)
  assert.match(body, /<h3>13:57 UTC, eu-west<\/h3>/)
  assert.match(body, /<hx-partial hx-target="#detail-status" hx-swap="innerHTML">Showing 13:57 for eu-west\.<\/hx-partial>/)
  const [svg] = charts(body)
  assert.equal(svg.attrs.id, 'detail')
  assert.equal(svg.attrs.width, '500')
  assert.equal(select(svg, 'g.hp-series.hp-s2').length, 1) // eu-west keeps its bar's colour slot
  // An unknown series is a hint we ignore; unknown minutes are no selection.
  assert.equal(select(charts((await get(`/detail/requests?hp-chart=requests&hp-x=${label}&hp-series=mars`)).body)[0], 'g.hp-series').length, 3)
  for (const q of ['hp-chart=requests&hp-x=25:99', 'hp-chart=requests', 'hp-chart=latency&hp-x=13:57', 'hp-chart=requests&hp-x=12:00']) {
    assert.equal((await get(`/detail/requests?${q}`)).res.status, 204, q)
  }
  // The plain-HTML equivalent: the page renders the same detail without script.
  assert.match((await get(`/?hp-chart=requests&hp-x=${label}`, { 'HX-Request': '' })).body, /<h3>13:57 UTC, all regions<\/h3>/)
})

test('the bars poll with a tag and carry one link per minute', async () => {
  const first = await get('/charts/requests')
  const tag = first.res.headers.get('hx-ptag')
  assert.equal((await get('/charts/requests', { 'HX-PTag': tag })).res.status, 304)
  assert.equal((first.body.match(/hx-get="\/detail\/requests\?hp-chart=requests&amp;hp-x=/g) ?? []).length, 6)
  const [svg] = charts(first.body)
  assert.equal(svg.attrs['data-hp-x'], 'band')
  assert.equal(select(svg, 'path.hp-bar').length, 18)
})

test('the stream card carries the stream URL at the requested width', async () => {
  const { body } = await get('/cards/throughput?hp-w=520')
  assert.ok(body.startsWith('<div class="stream" hx-sse:connect="/stream/throughput?hp-w=520" hx-swap="innerMorph">'))
  assert.equal(charts(body)[0].attrs.width, '520')
})

test('no parameter, however odd, causes a 5xx', async () => {
  const queries = JSON.parse(fixture('params.json')).server.map((c) => c.query)
  for (const path of ['/', '/charts/latency', '/charts/requests', '/detail/requests', '/cards/throughput']) {
    for (const q of [...queries, 'from=x&to=y', 'zoom=out&hp-x0=1', 'theme=dark&hp-w=-1']) {
      const { res } = await get(`${path}?${q}`)
      assert.ok(res.status < 500, `${path}?${q}: ${res.status}`)
    }
  }
})

test('static files, 404 and 405', async () => {
  const js = await get('/static/hx-plot.js')
  assert.equal(js.res.headers.get('content-type'), 'text/javascript; charset=utf-8')
  assert.match(js.body, /registerExtension/)
  assert.equal((await get('/static/hx-plot.css')).res.status, 200)
  assert.equal((await get('/nope')).res.status, 404)
  assert.equal((await fetch(base + '/', { method: 'POST' })).status, 405)
})

// A stream test fails within 10 s instead of hanging when frames stop
// coming (node --test has no default per-test timeout).
const STREAM_TEST = { timeout: 10_000 }

// readUntil reads a response body until enough messages have arrived. It
// fails if the stream ends first, and aborts the request (so the test's
// finally block still runs) if they have not arrived within 5 s.
async function readUntil(res, messages, ac) {
  const reader = res.body.getReader()
  const dec = new TextDecoder()
  const timer = setTimeout(() => ac.abort(new Error(`no ${messages} messages within 5 s`)), 5000)
  let buf = ''
  try {
    while ((buf.match(/\n\n/g) ?? []).length < messages) {
      const { done, value } = await reader.read()
      assert.ok(!done, `the stream ended after: ${JSON.stringify(buf.slice(0, 200))}`)
      buf += dec.decode(value, { stream: true })
    }
  } finally {
    clearTimeout(timer)
  }
  return buf
}

test('SSE: streams are capped; past the cap a 503 the page does not swap', STREAM_TEST, async () => {
  const [s, url] = await listen(createApp({ now: () => clock, tick: 10, maxStreams: 1 }))
  const ac = new AbortController()
  try {
    const first = await fetch(`${url}/stream/throughput`, { signal: ac.signal })
    assert.equal(first.status, 200)
    const second = await fetch(`${url}/stream/throughput`)
    assert.equal(second.status, 503)
    assert.equal(second.headers.get('hx-reswap'), 'none')
    ac.abort() // the first page goes away: its slot frees up
    let again
    for (let i = 0; i < 100; i++) {
      again = await fetch(`${url}/stream/throughput`)
      if (again.status === 200) break
      await new Promise((ok) => setTimeout(ok, 20))
    }
    assert.equal(again.status, 200)
    await again.body.cancel()
  } finally {
    ac.abort()
    await stop(s)
  }
})

test('SSE: a reader that stops reading is dropped, not buffered for', STREAM_TEST, async () => {
  // Frames every few ms from a fast clock; a stalled socket fills its
  // buffers within a second or so, and is dropped stallMs after that.
  const start = Date.now()
  const [s, url] = await listen(createApp({
    now: () => T0 + (Date.now() - start) * 1000, tick: 5, maxStreams: 1, stallMs: 200,
  }))
  const sock = net.connect(Number(new URL(url).port), '127.0.0.1')
  try {
    sock.write('GET /stream/throughput?hp-w=4096 HTTP/1.1\r\nHost: x\r\n\r\n')
    sock.pause() // never read
    sock.on('error', () => {}) // the server's reset
    // The slot frees up once the server drops the stalled stream.
    let status = 0
    for (let i = 0; i < 300 && status !== 200; i++) {
      await new Promise((ok) => setTimeout(ok, 25))
      const res = await fetch(`${url}/stream/throughput`)
      status = res.status
      await res.body.cancel()
    }
    assert.equal(status, 200)
  } finally {
    sock.destroy()
    await stop(s)
  }
})

test('SSE: one fragment per new second, split into data: lines; retry: on the first', STREAM_TEST, async () => {
  // A clock that runs 1000 times faster, so frames come every few ms.
  const start = Date.now()
  const [s, url] = await listen(createApp({ now: () => T0 + (Date.now() - start) * 1000, tick: 10 }))
  const ac = new AbortController()
  try {
    const res = await fetch(`${url}/stream/throughput?hp-w=480`, { signal: ac.signal })
    assert.equal(res.headers.get('content-type'), 'text/event-stream')
    const buf = await readUntil(res, 2, ac)
    assert.ok(!buf.includes('\r'))
    const messages = buf.split('\n\n').slice(0, 2)
    // hx-sse ignores a retry: in a message without data, so it comes with
    // the first frame.
    assert.ok(messages[0].startsWith('retry: 2000\nid: '), messages[0].slice(0, 40))
    messages[0] = messages[0].slice('retry: 2000\n'.length)
    assert.ok(!messages[1].includes('retry:'))
    const ids = []
    for (const m of messages) {
      const lines = m.split('\n')
      ids.push(lines[0])
      assert.match(lines[0], /^id: \d+$/)
      const fragment = lines.slice(1).map((l) => l.replace(/^data: /, '')).join('\n')
      assert.ok(lines.slice(1).every((l) => l.startsWith('data: ')))
      const [svg, table] = charts(fragment)
      assert.equal(svg.attrs.id, 'throughput')
      assert.equal(svg.attrs.width, '480')
      assert.equal(table.attrs.id, 'throughput-table')
    }
    assert.notEqual(ids[0], ids[1])
  } finally {
    ac.abort()
    await stop(s)
  }
})
