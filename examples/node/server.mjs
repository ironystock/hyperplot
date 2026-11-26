// server.mjs: a live dashboard served by Node's own http module, with no
// dependencies and no framework, speaking the hx-plot wire spec
// (spec/spec.md) at Level 3.
//
//   node examples/node/server.mjs [--addr localhost:8080] [--speed 1]
//
// then open http://localhost:8080/. --speed runs the simulated clock
// faster (10 shows a minute of data in six seconds).
//
// What it shows, in htmx terms:
//   - /charts/latency: a line chart polled with hx-get every 2 s and
//     hx-swap="innerMorph". Each frame carries an HX-PTag (a hash of the
//     body); a poll that sends back the current tag gets a bodiless 304
//     (spec/spec.md §4.5). A brush (hp-x0, hp-x1) zooms it, and the zoom
//     becomes a URL with HX-Push-Url (§5.3).
//   - /cards/throughput and /stream/throughput: a chart streamed over
//     server-sent events with hx-sse:connect, using the container of §4.5.
//   - /charts/requests and /detail/requests: grouped bars polled every 5 s;
//     a click on a bar (hp-chart, hp-x, hp-series) drills down (§5.2).
//   - Every chart has its table equivalent under "View as table" (§3.8).
//
// Every response is HTML; there is no JSON API. The page loads htmx and
// the extension, and the server reads a few hp-* parameters.
import { createHash } from 'node:crypto'
import { readFileSync } from 'node:fs'
import { createServer } from 'node:http'
import { parseArgs } from 'node:util'
import { fileURLToPath } from 'node:url'
import { resolve } from 'node:path'
import {
  closedMinutes, detailChart, findMinute, latencyChart, liveWindow, minuteLabel, render,
  requestsChart, throughputChart, zoomWindow,
} from './charts.mjs'
import { REGIONS, REQUEST_MINUTES, SECOND, fastClock } from './data.mjs'
import {
  chartBody, detailCard, html, page, partial, poller, raw, requestsBody, requestsPoller, stream, zoomStatus,
} from './page.mjs'
import { bareTag, brushTimes, readBrush, readSelect, readSize, sseMessage, timeValue } from './wire.mjs'

const WIDTH = 640 // the first paint's width; the "load" poll fetches the real one

// chartWidth reads hp-w, which hx-plot:autosize adds to requests that
// target its element (spec §4.6): clamped to [50, 4096], with a default
// for junk. The chart is drawn as wide as the box that asked for it; a
// layout that wants a floor sets min-width in CSS rather than ignoring
// the parameter.
const chartWidth = (q) => readSize(q, WIDTH, 0)[0]

// Streams hold a socket each, so they are capped (past the cap a page gets
// 503 with HX-Reswap: none, keeps its last frame and tries again later),
// and a reader that stops reading is dropped after STALL_MS instead of
// buffering frames for it without limit. hx.Stream in the Go reference
// server has the same 30 s write deadline.
const MAX_STREAMS = 32
const STALL_MS = 30_000

// The page allows scripts from this origin and the one pinned htmx release,
// and no inline script or style at all. Charts carry classes and
// presentation attributes, never style="" (spec/spec.md §7). This is the
// dashboard's policy (examples/dashboard/server.go).
export const CSP = "default-src 'self'; " +
  "script-src 'self' https://cdn.jsdelivr.net/npm/htmx.org@4.0.2/dist/; " +
  "style-src 'self'; img-src 'self' data:; connect-src 'self'; " +
  "base-uri 'none'; form-action 'self'; frame-ancestors 'none'"

// The extension's scripts and stylesheet, read from this repository's
// client/ directory. A real app installs htmx-ext-plot from npm or loads it
// from a CDN instead.
const here = fileURLToPath(new URL('.', import.meta.url))
const STATIC = {
  '/static/hx-plot.js': ['../../client/hx-plot.js', 'text/javascript'],
  '/static/hx-plot-brush.js': ['../../client/hx-plot-brush.js', 'text/javascript'],
  '/static/hx-plot.css': ['../../client/hx-plot.css', 'text/css'],
  '/static/app.css': ['static/app.css', 'text/css'],
}

/** ptag is a polling tag: a content hash of the body, quoted like an ETag (spec/spec.md §4.5). */
export function ptag(body) {
  return `"hp-${createHash('sha256').update(String(body)).digest('hex').slice(0, 16)}"`
}

/**
 * createApp returns the request handler. now is the clock (epoch ms),
 * tick how often a stream checks for a new second, in ms; tests inject
 * both. log receives one line per request. maxStreams and stallMs bound
 * the event streams (tests make them small).
 */
export function createApp({
  now = Date.now, tick = 250, log = () => {}, maxStreams = MAX_STREAMS, stallMs = STALL_MS,
} = {}) {
  const files = Object.fromEntries(Object.entries(STATIC).map(([path, [file, type]]) =>
    [path, { body: readFileSync(resolve(here, file)), type: `${type}; charset=utf-8` }]))

  // ---- Latency: polling with a tag, and brush to zoom ----------------------

  // zoomFrom reads a zoom from a bookmarked page URL (from, to) or a form
  // submitted without script (hp-x0, hp-x1). Anything unusable is the live
  // window.
  function zoomFrom(q) {
    for (const [a, b] of [['from', 'to'], ['hp-x0', 'hp-x1']]) {
      if (!q.get(a) || !q.get(b)) continue
      try {
        return zoomWindow(...brushTimes({ x0: q.get(a), x1: q.get(b) }), now())
      } catch {
        return null
      }
    }
    return null
  }

  // latencyBody is the polling element's content for a zoom (or null for live).
  function latencyBody(z, width) {
    const [t0, t1] = z ?? liveWindow(now())
    return chartBody({ ...render(latencyChart(t0, t1, width, z !== null)), title: 'Request latency' }).toString()
  }

  function latencyPoller(z, width) {
    // A zoomed poller's hx-get carries the range, so its polls stay zoomed.
    const src = z ? `/charts/latency?${zoomQuery(z)}` : '/charts/latency'
    const body = latencyBody(z, width)
    return poller({ src, tag: ptag(body), body: raw(body) })
  }

  function latencyStatusFor(z) {
    return zoomStatus(z ? { from: clockOf(z[0]), to: clockOf(z[1]) } : {})
  }

  function latencyRoute(req, res, q) {
    const width = chartWidth(q)
    if (q.get('zoom') === 'out') return zoomTo(res, null, width) // "Back to live"
    const brush = readBrush(q)
    // A poll (of the live window, or of a zoom named by from and to).
    if (!brush.ok) return sendTagged(req, res, latencyBody(zoomFrom(q), width))
    // A brush or the zoom form: hp-x0 and hp-x1 tell it from a poll
    // (spec §5.4). Ignore one that does not parse, falls outside the data
    // or names another chart: htmx swaps nothing for a 204 (§5.3).
    if (brush.chart && brush.chart !== 'latency') return send(res, 204)
    let z = null
    try {
      z = zoomWindow(...brushTimes(brush), now())
    } catch {
      // does not parse: z stays null
    }
    if (z === null) return send(res, 204)
    zoomTo(res, z, width)
  }

  // zoomTo answers a zoom (or null, back to live) with the whole polling
  // element (the brush swaps it with outerMorph), the live region's new
  // text, and the page URL of this view, so Back undoes the zoom and the
  // view can be bookmarked.
  function zoomTo(res, z, width) {
    const body = html`${latencyPoller(z, width)}${partial('#latency-status', latencyStatusFor(z))}`
    send(res, 200, body, { 'HX-Push-Url': z ? `/?${zoomQuery(z)}` : '/' })
  }

  // ---- Throughput: server-sent events ---------------------------------------

  function throughputBody(width) {
    const end = Math.floor(now() / SECOND) * SECOND
    return chartBody({ ...render(throughputChart(end, width)), title: 'Throughput' }).toString()
  }

  // The stream element, fetched by the container at its real width. Its
  // URL fixes hp-w for the life of the stream (spec §4.5).
  function throughputCard(req, res, q) {
    const width = chartWidth(q)
    send(res, 200, stream({ url: `/stream/throughput?hp-w=${width}`, body: raw(throughputBody(width)) }))
  }

  let streams = 0 // open streams, for MAX_STREAMS

  function throughputStream(req, res, q) {
    if (streams >= maxStreams) return send(res, 503, '', { 'HX-Reswap': 'none', 'Retry-After': '5' })
    streams++
    const width = chartWidth(q)
    res.writeHead(200, { 'Content-Type': 'text/event-stream', 'Cache-Control': 'no-cache', 'Content-Security-Policy': CSP })
    // retry: the reconnect delay after a dropped stream (spec §4.5). It
    // rides on the first frame: hx-sse (htmx 4.0.2) ignores a retry: that
    // arrives in a message without data.
    let retry = 'retry: 2000\n'
    let last = req.headers['last-event-id'] ?? '' // after a reconnect, skip the second the page has
    let stalledSince = 0 // when the socket stopped draining, or 0
    const sendFrame = () => {
      // Backpressure: while the last write is still queued, send nothing
      // (the next frame replaces this one anyway). A reader that has not
      // drained for stallMs is gone in all but name: drop it.
      if (res.writableNeedDrain) {
        stalledSince ||= Date.now()
        if (Date.now() - stalledSince > stallMs) res.destroy()
        return
      }
      stalledSince = 0
      const id = String(Math.floor(now() / SECOND))
      if (id === last) return
      let body
      try {
        body = throughputBody(width)
      } catch (err) {
        // Skip the frame and keep the stream open: ending it would make
        // every page reconnect at hx-sse's base delay (spec §4.5).
        log(`throughput frame: ${err.message}`)
        return
      }
      last = id
      // One unnamed message per frame; its data: lines carry one fragment.
      res.write(`${retry}id: ${id}\n${sseMessage(body)}`)
      retry = ''
    }
    sendFrame()
    const timer = setInterval(sendFrame, tick)
    res.on('close', () => { // the page went away, or replaced the stream element
      clearInterval(timer)
      streams--
    })
  }

  // ---- Requests: bars, and select to drill down ------------------------------

  function requestsResponse(width) {
    const minutes = closedMinutes(now(), REQUEST_MINUTES).map(minuteLabel)
    const chart = chartBody({ ...render(requestsChart(now(), width)), title: 'Requests by region' })
    return requestsBody({ chart, minutes }).toString()
  }

  // detailFor reads a select (spec §5.2): hp-x is a category this server
  // wrote, matched exactly; a missing or unknown one is no selection (null).
  function detailFor(q, width) {
    const sel = readSelect(q)
    if (!sel.ok || (sel.chart && sel.chart !== 'requests')) return null
    const m = findMinute(sel.x, now())
    if (m === null) return null
    // hp-series is a hint: a click between bars sends none.
    const regions = REGIONS.includes(sel.series) ? [sel.series] : REGIONS
    const who = regions.length === 1 ? regions[0] : 'all regions'
    const chart = chartBody({ ...render(detailChart(m, regions, width)), title: 'Requests per 10 s' })
    return {
      card: detailCard({ heading: `${minuteLabel(m)} UTC, ${who}`, chart }),
      status: `Showing ${minuteLabel(m)} for ${who}.`,
    }
  }

  function detailRoute(req, res, q) {
    const d = detailFor(q, chartWidth(q))
    if (!d) return send(res, 204) // no selection: keep what is on screen
    send(res, 200, html`${d.card}${partial('#detail-status', d.status)}`)
  }

  // ---- The page ---------------------------------------------------------------

  function pageRoute(req, res, q) {
    const theme = ['light', 'dark'].includes(q.get('theme')) ? q.get('theme') : ''
    const z = zoomFrom(q)
    const requests = requestsResponse(WIDTH)
    const detail = detailFor(q, WIDTH) // a select link followed without script
    const body = page({
      theme,
      latency: latencyPoller(z, WIDTH),
      latencyStatus: latencyStatusFor(z),
      throughput: stream({ url: null, body: raw(throughputBody(WIDTH)) }),
      requests: requestsPoller({ tag: ptag(requests), body: raw(requests) }),
      detail: detail?.card ?? html`<p class="hint">Pick a bar, or a minute under the chart.</p>`,
      detailStatus: detail?.status ?? '',
    })
    // The page and the fragments live at different URLs, but a cache in
    // between should still key on htmx's headers (spec §4.5).
    send(res, 200, body, { Vary: 'HX-Request, HX-Request-Type' })
  }

  // ---- Plumbing -------------------------------------------------------------

  // sendTagged answers a poll: 304 with no body when the request's HX-PTag
  // already names this body, else the body with its tag. Tags compare
  // without spaces, W/ and quotes, as hx-ptag sends them (spec §4.5).
  function sendTagged(req, res, body) {
    const tag = ptag(body)
    if (bareTag(req.headers['hx-ptag']) === bareTag(tag)) {
      res.writeHead(304, { 'HX-PTag': tag, 'Cache-Control': 'no-cache', 'Content-Security-Policy': CSP })
      return res.end()
    }
    send(res, 200, body, { 'HX-PTag': tag })
  }

  const routes = {
    '/': pageRoute,
    '/charts/latency': latencyRoute,
    '/cards/throughput': throughputCard,
    '/stream/throughput': throughputStream,
    '/charts/requests': (req, res, q) => sendTagged(req, res, requestsResponse(chartWidth(q))),
    '/detail/requests': detailRoute,
  }

  return function handle(req, res) {
    // The request target is input too: "//" or "http://x:99999/" makes
    // new URL throw, and a throw here (outside any try) would end the
    // process for every user. Answer 400 instead.
    let url
    try {
      url = new URL(req.url, 'http://localhost')
    } catch {
      return send(res, 400, 'bad request', { 'Content-Type': 'text/plain; charset=utf-8' })
    }
    if (!url.pathname.startsWith('/static/')) {
      res.on('finish', () => log(`${req.method} ${url.pathname}${url.search} ${res.statusCode}`))
    }
    if (req.method !== 'GET') return send(res, 405, '', { Allow: 'GET' })
    const file = files[url.pathname]
    if (file) return send(res, 200, file.body, { 'Content-Type': file.type })
    const route = routes[url.pathname]
    if (!route) return send(res, 404, 'not found', { 'Content-Type': 'text/plain; charset=utf-8' })
    try {
      route(req, res, url.searchParams)
    } catch (err) {
      // A bug, not bad input: parameters never get here. noSwap keeps the
      // 500 off the page.
      log(`${url.pathname}: ${err.stack}`)
      if (!res.headersSent) send(res, 500)
      else res.end()
    }
  }
}

// send writes a complete response. HTML by default, never cached without
// a check, and always under the strict CSP.
function send(res, status, body = '', headers = {}) {
  const b = Buffer.isBuffer(body) ? body : Buffer.from(String(body))
  res.writeHead(status, {
    'Content-Type': 'text/html; charset=utf-8',
    'Cache-Control': 'no-cache',
    'Content-Security-Policy': CSP,
    'X-Content-Type-Options': 'nosniff',
    'Referrer-Policy': 'no-referrer',
    ...(b.length ? { 'Content-Length': b.length } : {}),
    ...headers,
  })
  res.end(status === 204 || status === 304 ? undefined : b)
}

const clockOf = (t) => timeValue(t).slice(11, 19)

// zoomQuery is a zoom's canonical form in URLs: from=…&to=… as time values.
function zoomQuery([t0, t1]) {
  return new URLSearchParams({ from: timeValue(t0), to: timeValue(t1) }).toString()
}

/** parseAddr splits "host:port" (or "[::1]:port"); an empty host listens everywhere. */
export function parseAddr(addr) {
  const i = addr.lastIndexOf(':')
  const port = Number(addr.slice(i + 1))
  if (i < 0 || !Number.isInteger(port) || port < 0 || port > 65535) throw new Error(`bad --addr ${addr}: want host:port`)
  return { host: addr.slice(0, i).replace(/^\[(.*)\]$/, '$1'), port }
}

function main() {
  const { values } = parseArgs({
    options: { addr: { type: 'string', default: 'localhost:8080' }, speed: { type: 'string', default: '1' } },
  })
  const speed = Number(values.speed)
  if (!(speed > 0 && speed <= 1000)) {
    console.error('--speed must be a number in (0, 1000]')
    process.exit(2)
  }
  let host, port
  try {
    ({ host, port } = parseAddr(values.addr))
  } catch (err) {
    console.error(err.message)
    process.exit(2)
  }
  const now = speed === 1 ? Date.now : fastClock(Date.now, speed)
  const app = createApp({ now, tick: Math.max(20, 250 / speed), log: (line) => console.log(line) })
  const server = createServer({ requestTimeout: 30_000, headersTimeout: 10_000, keepAliveTimeout: 120_000 }, app)
  server.listen(port, host || undefined, () => {
    const a = server.address()
    const shown = !host || host === '0.0.0.0' || host === '::' ? 'localhost' : host.includes(':') ? `[${host}]` : host
    console.log(`listening on http://${shown}:${a.port}/`)
  })
  // Ctrl-C: stop accepting, end open streams, exit.
  const stop = () => {
    console.log('shutting down')
    server.close(() => process.exit(0))
    server.closeAllConnections()
  }
  process.on('SIGINT', stop)
  process.on('SIGTERM', stop)
}

if (process.argv[1] && resolve(process.argv[1]) === fileURLToPath(import.meta.url)) main()
