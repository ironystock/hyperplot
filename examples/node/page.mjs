// page.mjs: the page and its fragments, written with an html`` tagged
// template that escapes every interpolated value.
//
// html`<p>${name}</p>` escapes name with xmlAttr (which is safe in text and
// in double-quoted attributes alike), so data can never become markup.
// Markup that is already safe (a rendered chart, another html`` result) is
// passed through by wrapping it in raw(), or by being an html`` result.
// Arrays are joined, and null, undefined and false render as nothing.
//
// Nothing here writes a style="" attribute, an on* handler or an inline
// <script> or <style>: the page runs under a strict Content-Security-Policy
// (see server.mjs), and the theme switch is plain links.
import { xmlAttr } from './wire.mjs'

class Safe {
  constructor(s) {
    this.s = s
  }

  toString() {
    return this.s
  }
}

/** raw marks a string as markup that is already safe, such as a rendered chart. */
export const raw = (s) => new Safe(String(s))

function part(v) {
  if (v instanceof Safe) return v.s
  if (Array.isArray(v)) return v.map(part).join('')
  if (v == null || v === false) return ''
  return xmlAttr(String(v))
}

/** html is a tagged template that escapes every interpolated value. */
export function html(strings, ...values) {
  return new Safe(strings.reduce((out, s, i) => out + part(values[i - 1]) + s))
}

// The pinned htmx 4.0.2 files, with Subresource Integrity, exactly as in
// examples/dashboard/templates/page.html. They load in order and without
// defer: a deferred htmx 4.0.2 starts on a timer, which can fire while a
// deferred extension is still downloading, and that extension then misses
// the first pass (the stream would never connect).
const HTMX = raw(`<script src="https://cdn.jsdelivr.net/npm/htmx.org@4.0.2/dist/htmx.min.js" integrity="sha384-6NzMyCdh08UZ4b4M17SJJKQLdDijx4z2IqqKLKD0Q/PzCFQLpFuk3konY0WgqbTy" crossorigin="anonymous"></script>
<script src="https://cdn.jsdelivr.net/npm/htmx.org@4.0.2/dist/ext/hx-sse.min.js" integrity="sha384-5zdqOOgEI+w1bvDhFjUeS1VYpPKkc1DFc4+WnvUe849JkM4u0s7JSeGpLon7/sRU" crossorigin="anonymous"></script>
<script src="https://cdn.jsdelivr.net/npm/htmx.org@4.0.2/dist/ext/hx-ptag.js" integrity="sha384-R20vL2ppRMMKzN1SVZdVpoeW1yH54eSH8rX3jz9NqpuWNqzwR5ngAqhCcKnK/b1s" crossorigin="anonymous"></script>`)

/**
 * page is the whole document. theme is '', 'light' or 'dark': the server
 * writes it into <html data-theme>, and without it the stylesheets follow
 * prefers-color-scheme.
 */
export function page({ theme, latency, latencyStatus, throughput, requests, detail, detailStatus }) {
  return html`<!doctype html>
<html lang="en"${theme ? html` data-theme="${theme}"` : ''}>
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<meta name="color-scheme" content="light dark">
<!-- noSwap: an error page from a proxy never replaces a chart, and a 204
     (an ignored gesture) or a 304 (an unchanged poll) swaps nothing. -->
<meta name="htmx-config" content='{"noSwap":[204,304,"4xx","5xx"]}'>
<title>Service latency · hx-plot on Node</title>
<link rel="icon" href="data:,">
<link rel="stylesheet" href="/static/hx-plot.css">
<link rel="stylesheet" href="/static/app.css">
${HTMX}
<!-- hx-plot.js and its brush companion may defer: htmx waits for
     DOMContentLoaded, which runs after deferred scripts, in order. -->
<script defer src="/static/hx-plot.js"></script>
<script defer src="/static/hx-plot-brush.js"></script>
</head>
<body>
<header class="top">
  <a class="brand" href="/">hx-plot <span>on Node</span></a>
  <nav class="theme" aria-label="Color theme">
    <a href="/?theme=light"${theme === 'light' ? raw(' aria-current="true"') : ''}>Light</a>
    <a href="/?theme=dark"${theme === 'dark' ? raw(' aria-current="true"') : ''}>Dark</a>
    <a href="/"${theme ? '' : raw(' aria-current="true"')}>System</a>
  </nav>
</header>
<main class="wrap">
  <h1>Service latency</h1>
  <p class="lede">A Node server with no dependencies draws every chart as SVG; htmx 4 and the hx-plot extension keep them live. Times are UTC.</p>

  <section class="card" aria-labelledby="latency-heading">
    <h2 id="latency-heading">Latency</h2>
    <p class="sub">p50 and p95 per 5 s, polled every 2 s. Drag across the chart to zoom (or focus it, Shift+arrows, Enter).</p>
    <!-- Brush to zoom (docs/spec.md §5.4). hx-plot:brush lets the reader
         mark a range; the extension dispatches htmx:plot:brush, which this
         element turns into a GET with hp-chart, hp-x0 and hp-x1. The answer
         replaces the polling element whole (outerMorph), because its hx-get
         has to carry the zoom. hx-sync="replace" here and "closest .zoom:drop"
         on the poller put both on one queue: a zoom aborts a poll in
         flight, and polls are dropped while a zoom is pending. -->
    <div class="zoom" hx-plot:brush hx-get="/charts/latency" hx-trigger="htmx:plot:brush"
         hx-target="#latency-body" hx-swap="outerMorph" hx-sync="replace">
      <p id="latency-status" class="status" role="status">${latencyStatus}</p>
      ${latency}
      <!-- The plain HTML equivalent of the brush (spec §5.5): the same
           parameters from a form, for keyboard, screen-reader and touch
           users. Without script it submits to /, which reads them too. -->
      <form class="zoom-form" action="/" hx-get="/charts/latency" hx-target="#latency-body"
            hx-swap="outerMorph" hx-sync="closest .zoom:replace">
        <!-- hp-chart names the chart, as a brush on it would (spec §5.1). -->
        <input type="hidden" name="hp-chart" value="latency">
        <label>From <input type="datetime-local" name="hp-x0" step="1" required></label>
        <label>To <input type="datetime-local" name="hp-x1" step="1" required></label>
        <button>Zoom (UTC)</button>
      </form>
    </div>
  </section>

  <section class="card" aria-labelledby="throughput-heading">
    <h2 id="throughput-heading">Throughput</h2>
    <p class="sub">Requests per second, streamed over server-sent events once a second.</p>
    <!-- The stream container (docs/spec.md §4.5). The page renders the first
         frame without a stream; "load" fetches the stream element at the
         container's real width (hp-w, from hx-plot:autosize), so only one
         stream opens. A resize fetches a new one, and innerHTML removes the
         old element, which ends its stream. Never morph a new
         hx-sse:connect URL into a live element: the old stream would keep
         reconnecting. -->
    <div id="throughput-body" class="chart" hx-plot:autosize hx-get="/cards/throughput"
         hx-trigger="load, htmx:plot:resize" hx-swap="innerHTML">${throughput}</div>
  </section>

  <section class="card" aria-labelledby="requests-heading">
    <h2 id="requests-heading">Requests by region</h2>
    <p class="sub">Closed minutes only, polled every 5 s. Click a bar (or focus the chart, arrows, Enter) for that minute in detail.</p>
    <!-- Select to drill down (docs/spec.md §5.2). A click on a bar makes
         the extension dispatch htmx:plot:select, which bubbles here and
         becomes a GET with hp-chart, hp-x (the bar's category) and
         hp-series (its region). Enter on a focused chart selects the
         minute only: hp-x without hp-series, so every region. The answer
         goes into #requests-detail. -->
    <div hx-get="/detail/requests" hx-trigger="htmx:plot:select" hx-target="#requests-detail" hx-swap="innerHTML">
      ${requests}
    </div>
    <!-- A polite live region the server updates after a select (spec §5.5),
         through an <hx-partial> in the detail response. -->
    <p id="detail-status" class="sr-only" role="status">${detailStatus}</p>
    <!-- hx-plot:autosize on the target: select requests carry its width. -->
    <div id="requests-detail" class="detail" hx-plot:autosize>${detail}</div>
  </section>
</main>
<footer class="wrap foot">The data is simulated: each value is a pure function of its time bucket, so an unchanged window renders the same bytes and polls cost a 304.</footer>
</body>
</html>
`
}

/**
 * poller is the latency polling element. A zoom replaces it whole, so its
 * hx-get carries the zoom and later polls stay zoomed (docs/spec.md §5.4).
 * src is its URL, tag the HX-PTag of body.
 */
export function poller({ src, tag, body }) {
  // "load delay:1ms" fits the chart to the container's real width at once.
  // The delay lets hx-ptag read the seeded hx-ptag attribute first (htmx
  // 4.0.2 fires load before it does), so an unchanged chart costs a 304.
  return html`<div id="latency-body" class="chart" hx-plot:autosize hx-get="${src}"
     hx-trigger="load delay:1ms, every 2s, htmx:plot:resize" hx-swap="innerMorph"
     hx-sync="closest .zoom:drop" hx-ptag="${tag}">${body}</div>`
}

/** chartBody is a chart followed by its table, inside a disclosure the client keeps open across morphs. */
export function chartBody({ svg, table, title }) {
  return html`${raw(svg)}<details class="table-view"><summary>View as table</summary><div class="table-scroll" tabindex="0" role="region" aria-label="${title}: data table">${raw(table)}</div></details>`
}

/** zoomStatus is the polite live region's text after a zoom (spec §5.5): the client cannot know what a request did. */
export function zoomStatus({ from, to }) {
  if (!from) return html`Live: the last 5 minutes.`
  // "Back to live" sends zoom=out to the same queue as the brush.
  return html`Showing ${from} to ${to} UTC. <a href="/" hx-get="/charts/latency?zoom=out" hx-target="#latency-body"
     hx-swap="outerMorph" hx-sync="closest .zoom:replace">Back to live</a>`
}

/** stream is the throughput stream element; url is null for the first paint, which has no stream yet. */
export function stream({ url, body }) {
  return html`<div class="stream"${url ? html` hx-sse:connect="${url}" hx-swap="innerMorph"` : ''}>${body}</div>`
}

/**
 * requestsBody is the polled bars chart plus one link per minute: the
 * plain HTML equivalent of select (spec §5.5). Each link sends what a
 * click on the minute's bars sends, minus hp-series (a link is for the
 * whole minute, so the detail shows every region); without script it
 * opens the page with the detail already filled in.
 */
export function requestsBody({ chart, minutes }) {
  return html`${chart}<nav class="drill" aria-label="Minute details">Details:
${minutes.map((m) => {
    const q = new URLSearchParams({ 'hp-chart': 'requests', 'hp-x': m }).toString()
    return html` <a href="/?${q}" hx-get="/detail/requests?${q}" hx-target="#requests-detail" hx-swap="innerHTML">${m}</a>`
  })}</nav>`
}

/** requestsPoller wraps the bars in their polling element. */
export function requestsPoller({ tag, body }) {
  return html`<div id="requests-body" class="chart" hx-plot:autosize hx-get="/charts/requests"
     hx-trigger="load delay:1ms, every 5s, htmx:plot:resize" hx-swap="innerMorph" hx-ptag="${tag}">${body}</div>`
}

/** detailCard is the drill-down: a heading and the minute's chart. */
export function detailCard({ heading, chart }) {
  return html`<h3>${heading}</h3>${chart}`
}

/**
 * partial updates another element from the same response (docs/spec.md
 * §4.5, "Several targets"): htmx 4 swaps each <hx-partial> into its own
 * target. The live regions use it, so they stay in place and announce.
 */
export function partial(target, content) {
  return html`<hx-partial hx-target="${target}" hx-swap="innerHTML">${content}</hx-partial>`
}
