// render.mjs: draws one frame of a chart as hx-plot SVG markup, and its table.
//
// A chart is described the way the conformance fixtures describe one
// (testdata/conformance/*/input.json), with times as epoch ms:
//
//   { id, title, width, height, animate,
//     x: { timeDomain: [t0, t1] },          // optional: the exact x window
//     y: { unit: ' ms', domain: [lo, hi], zero: true },
//     marks: [{ type: 'line', name: 'p50', x: { times: [...] }, y: [42, null, ...] },
//             { type: 'bar', name: '2026', x: { categories: ['Q1', ...] }, y: [...], slot: 2 }] }
//
// slot optionally pins a series' colour (hp-s1 to hp-s8).
//
// Lines take a time axis, bars a band (category) axis; a chart holds one
// kind. Everything the extension reads is here: the root attributes and
// geometry (spec §3.2, §4.2), stable ids (§4.1), the hover JSON (§3.5) and
// the table equivalent (§3.8). The visual layout is this file's own choice;
// the spec only fixes what the client reads.
import { keys, number, timeValue, tipsJson, xmlAttr, xmlText } from './wire.mjs'

const MONTHS = ['Jan', 'Feb', 'Mar', 'Apr', 'May', 'Jun', 'Jul', 'Aug', 'Sep', 'Oct', 'Nov', 'Dec']
const DASH = '–' // U+2013: a missing value (spec §3.5)
const FONT = "system-ui, -apple-system, 'Segoe UI', sans-serif"

const pad = (n) => String(n).padStart(2, '0')

/** clock formats epoch ms as UTC HH:MM:SS, or HH:MM without seconds. */
export function clock(ms, seconds = true) {
  const d = new Date(ms)
  const hm = `${pad(d.getUTCHours())}:${pad(d.getUTCMinutes())}`
  return seconds ? `${hm}:${pad(d.getUTCSeconds())}` : hm
}

/** heading is the default row heading of a time: "Mar 3, 14:10:05". Headings are text the server formats. */
export function heading(ms) {
  const d = new Date(ms)
  return `${MONTHS[d.getUTCMonth()]} ${d.getUTCDate()}, ${clock(ms)}`
}

// num writes a coordinate: rounded to 2 decimals, in the number form (spec §3.5).
const num = (v) => number(Math.round(v * 100) / 100)
const round2 = (v) => Math.round(v * 100) / 100
// clean drops float noise such as 0.30000000000000004 from tick values.
const clean = (v) => Number(v.toPrecision(12))
// textWidth estimates a label's width at 12 px; good enough for margins.
const textWidth = (s) => [...s].length * 6.6

// nice returns a 1, 2 or 5 step that cuts span into about count intervals.
function nice(span, count) {
  const p = 10 ** Math.floor(Math.log10(span / count))
  return clean([1, 2, 5, 10].map((m) => m * p).find((s) => s >= span / count))
}

const TIME_STEPS = [1, 2, 5, 10, 15, 30, 60, 120, 300, 600, 900, 1800, 3600, 7200, 10800, 21600, 43200, 86400].map((s) => s * 1000)

/**
 * render returns {svg, table} for one frame of chart. table is the
 * <table class="hp-table"> equivalent; show it next to the chart (spec §3.8).
 */
export function render(chart) {
  const { id, title = '', width = 640, height = 320, animate = false } = chart
  const marks = chart.marks ?? []
  const unit = chart.y?.unit ?? ''
  const fmt = chart.y?.format ?? ((v) => number(Math.round(v * 10) / 10) + unit)
  const show = (v) => (v == null ? DASH : fmt(v))
  if (marks.length > 8) throw new RangeError('render: at most 8 series per chart')
  const band = marks.some((m) => m.type === 'bar')
  if (band && !marks.every((m) => m.type === 'bar')) throw new RangeError('render: lines and bars do not mix')

  // Series: a colour slot (hp-sN) each, and ids from the names. An
  // unnamed series hashes "#index"; two series of one name still get
  // distinct keys (spec §4.1).
  const names = marks.map((m) => m.name ?? '')
  const seriesKeys = keys(marks.map((m, i) => m.name ?? `#${i}`))
  const slots = assignSlots(marks)

  // The y scale: the given domain, or the data's extent on nice ticks.
  const values = marks.flatMap((m) => m.y.filter((v) => v != null))
  let [lo, hi] = values.length ? [Math.min(...values), Math.max(...values)] : [0, 1]
  if (band || chart.y?.zero) [lo, hi] = [Math.min(lo, 0), Math.max(hi, 0)]
  if (lo === hi) [lo, hi] = [lo - 1, hi + 1] // a degenerate domain is widened (spec §4.2)
  const legend = marks.length > 1
  const T = legend ? 26 : 8
  const B = height - 22
  let y0, y1, step
  if (chart.y?.domain) {
    [y0, y1] = chart.y.domain
    step = nice(y1 - y0, Math.max(2, Math.floor((B - T) / 32)))
  } else {
    step = nice(hi - lo, Math.max(2, Math.floor((B - T) / 32)))
    y0 = clean(Math.floor(lo / step) * step)
    y1 = clean(Math.ceil(hi / step) * step)
  }
  const yTicks = []
  for (let v = y0; v <= y1 + step / 1e6; v = clean(v + step)) yTicks.push(v)
  // At most 40% of the width, so the plot stays a real rectangle (spec
  // §4.2: left < right) even at the smallest hp-w, 50 px (spec §4.6).
  const L = round2(Math.min(Math.max(24, Math.max(...yTicks.map((v) => textWidth(number(v)))) + 14), width * 0.4))
  const R = width - 12
  const y = (v) => B - ((v - y0) / (y1 - y0)) * (B - T)

  // The x scale and the hover rows. Mode band for bars; mode x for lines
  // and for a chart with no marks at all (spec §3.5).
  let xs, rows, xAttr, rowKeys, ticks, marksSvg, labelsSvg, bw
  if (band) {
    const cats = [...new Set(marks.flatMap((m) => m.x.categories))]
    const catKeys = keys(cats)
    const bandW = (R - L) / Math.max(cats.length, 1)
    const centre = (ci) => L + bandW * (ci + 0.5)
    const group = bandW * 0.72
    const barW = group / marks.length
    const value = marks.map((m) => new Map(m.x.categories.map((c, j) => [c, m.y[j]])))
    xAttr = 'band'
    bw = round2(bandW)
    xs = cats.map((c, ci) => round2(centre(ci)))
    rows = cats.map((c) => ({ t: c, k: c, v: marks.map((m, si) => [slots[si], names[si], show(value[si].get(c))]) }))
    rowKeys = catKeys // a band row and its bars share {catKey} (spec §4.1)
    ticks = cats.map((c, ci) => [centre(ci), c])
    marksSvg = marks.map((m, si) => {
      const bars = cats.map((c, ci) => {
        const v = value[si].get(c)
        if (v == null) return '' // a missing value has no bar
        const x0 = centre(ci) - group / 2 + si * barW + 1
        const [top, base] = [Math.min(y(v), y(0)), Math.max(y(v), y(0))]
        // Bar ids are {id}-b-{seriesKey}-{catKey} (spec §4.1); the <title>
        // child is the native tooltip without script (spec §3.7).
        return `<path id="${xmlAttr(id)}-b-${seriesKeys[si]}-${catKeys[ci]}" class="hp-bar" ` +
          `d="M${num(x0)},${num(base)}V${num(top)}H${num(x0 + Math.max(barW - 2, 1))}V${num(base)}Z" fill="currentColor">` +
          `<title>${xmlText(`${names[si]} · ${c}: ${fmt(v)}`)}</title></path>`
      })
      return seriesGroup(id, seriesKeys[si], slots[si], names[si], bars.join(''))
    })
    labelsSvg = ''
  } else {
    const times = marks.flatMap((m) => m.x.times)
    let [d0, d1] = chart.x?.timeDomain ?? (times.length ? [Math.min(...times), Math.max(...times)] : [null, null])
    if (d0 !== null && d0 === d1) [d0, d1] = [d0 - 30_000, d1 + 30_000]
    const x = (t) => L + ((t - d0) / (d1 - d0)) * (R - L)
    const inDomain = (t) => t >= d0 && t <= d1
    const value = marks.map((m) => new Map(m.x.times.map((t, j) => [t, m.y[j]])))
    // A point outside the domain (the overscan point of a sliding window)
    // is drawn but has no column and no row (spec §3.5, §4.4).
    const cols = [...new Set(times)].filter((t) => inDomain(t) && value.some((s) => s.get(t) != null)).sort((p, q) => p - q)
    xAttr = d0 === null ? null : `time ${number(d0)} ${number(d1)}` // exact, never rounded (spec §4.2)
    bw = 0
    xs = cols.map((t) => round2(x(t)))
    rows = cols.map((t) => ({ t: (chart.heading ?? heading)(t), k: timeValue(t), v: marks.map((m, si) => [slots[si], names[si], show(value[si].get(t))]) }))
    rowKeys = keys(rows.map((r) => r.k))
    ticks = timeTicks(d0, d1, R - L).map((t) => [x(t), t])
    marksSvg = marks.map((m, si) => {
      let d = ''
      let pen = 'M'
      m.x.times.forEach((t, j) => {
        if (m.y[j] == null) {
          pen = 'M' // a gap
          return
        }
        d += `${pen}${num(x(t))},${num(y(m.y[j]))}`
        pen = 'L'
      })
      const path = d && `<path class="hp-line" d="${d}" fill="none" stroke="currentColor" stroke-width="2" stroke-linejoin="round" stroke-linecap="round"/>`
      return seriesGroup(id, seriesKeys[si], slots[si], names[si], path)
    })
    // End dots mark each line's newest visible point. They sit in
    // g.hp-labels, which the slide does not move (spec §6.8.3).
    labelsSvg = marks.map((m, si) => {
      const j = m.x.times.findLastIndex((t, j) => inDomain(t) && m.y[j] != null)
      if (j < 0) return ''
      return `<circle class="hp-end hp-s${slots[si]}" cx="${num(x(m.x.times[j]))}" cy="${num(y(m.y[j]))}" r="4" fill="currentColor" stroke="#fff" stroke-width="2"/>`
    }).join('')
    if (!marks.length) {
      labelsSvg = `<text class="hp-label hp-empty" x="${num((L + R) / 2)}" y="${num((T + B) / 2)}" dy="0.32em" text-anchor="middle" fill="currentColor" fill-opacity="0.7">No data</text>`
    }
  }

  const i = xmlAttr(id)
  const tips = band ? { mode: 'band', xs, bw, top: T, bottom: B, rows } : { mode: 'x', xs, bw: 0, top: T, bottom: B, rows }
  const out = [
    `<svg xmlns="http://www.w3.org/2000/svg" id="${i}" class="hp${animate ? ' hp-anim' : ''}" viewBox="0 0 ${number(width)} ${number(height)}" ` +
      `width="${number(width)}" height="${number(height)}" role="img" aria-labelledby="${i}-title" aria-describedby="${i}-desc" tabindex="0" ` +
      `data-hp="1" data-hp-plot="${num(L)} ${num(T)} ${num(R)} ${num(B)}"${xAttr ? ` data-hp-x="${xAttr}"` : ''} ` +
      `data-hp-y="linear ${number(y0)} ${number(y1)}" font-family="${FONT}" font-size="12">`,
    `<title id="${i}-title">${xmlText(title)}</title>`,
    `<desc id="${i}-desc">${xmlText(describe(band, names, rows, y0, y1, unit))}</desc>`,
    // The clip sits on g.hp-marks, not on a series: a slide moves series
    // groups, and their own clip would move with them (spec §3.3).
    `<defs><clipPath id="${i}-clip"><rect x="${num(L - 6)}" y="${num(T - 6)}" width="${num(R - L + 12)}" height="${num(B - T + 12)}"/></clipPath></defs>`,
  ]
  if (legend) out.push(legendSvg(names, slots))
  out.push('<g class="hp-grid" aria-hidden="true" stroke="currentColor" stroke-opacity="0.12">' +
    yTicks.map((v) => `<line x1="${num(L)}" x2="${num(R)}" y1="${num(y(v) + 0.5)}" y2="${num(y(v) + 0.5)}"/>`).join('') + '</g>')
  // Mode band paints its highlight under the bars, so it comes first.
  if (band) out.push(`<rect class="hp-band-hl" visibility="hidden" x="${num(L)}" y="${num(T)}" width="${num(bw)}" height="${num(B - T)}" fill="currentColor" fill-opacity="0.06"/>`)
  out.push(`<g class="hp-marks" clip-path="url(#${i}-clip)">${marksSvg.join('')}</g>`)
  // No transform anywhere in the x axis: the client slides tick labels
  // with a CSS transform, which would replace one (spec §3.3).
  out.push('<g class="hp-axis hp-axis-x" aria-hidden="true">' +
    `<line class="hp-baseline" x1="${num(L)}" x2="${num(R)}" y1="${num(B + 0.5)}" y2="${num(B + 0.5)}" stroke="currentColor" stroke-opacity="0.3"/>` +
    ticks.map(([px, v]) => `<text class="hp-tick" x="${num(px)}" y="${num(B + 16)}" text-anchor="middle" fill="currentColor" fill-opacity="0.7">${xmlText(band ? v : tickLabel(v, ticks))}</text>`).join('') +
    '</g>')
  out.push('<g class="hp-axis hp-axis-y" aria-hidden="true">' +
    yTicks.map((v) => `<text class="hp-tick" x="${num(L - 8)}" y="${num(y(v))}" dy="0.32em" text-anchor="end" fill="currentColor" fill-opacity="0.7">${number(v)}</text>`).join('') +
    '</g>')
  out.push(`<g class="hp-labels" aria-hidden="true">${labelsSvg}</g>`)
  // The hover group: a hidden crosshair in mode x, and the JSON the client
  // hit-tests against (spec §3.5). tipsJson escapes <, > and &, so the text
  // can never close the element.
  out.push('<g class="hp-hover" aria-hidden="true">' +
    (band ? '' : `<line class="hp-crosshair" visibility="hidden" x1="${num(L)}" x2="${num(L)}" y1="${num(T)}" y2="${num(B)}" stroke="currentColor" stroke-opacity="0.4"/>`) +
    `<metadata class="hp-tips">${tipsJson(tips)}</metadata></g></svg>`)

  return { svg: out.join(''), table: tableHtml(id, title, band ? (chart.x?.label ?? 'Category') : (chart.x?.label ?? 'Time'), names, rows, rowKeys) }
}

// seriesGroup is one g.hp-series: id {id}-s-{key}, colour slot hp-sN, and
// the full name in data-hp-series, which a select sends back as hp-series.
function seriesGroup(id, key, slot, name, content) {
  return `<g id="${xmlAttr(id)}-s-${key}" class="hp-series hp-s${slot}" data-hp-series="${xmlAttr(name)}">${content}</g>`
}

// assignSlots gives each series its colour slot, 1 to 8: the one a mark
// pins (m.slot), else the next free one in series order (spec §3.3).
// Pinning keeps a series' colour when other series come and go.
function assignSlots(marks) {
  const pinned = new Set(marks.map((m) => m.slot).filter(Boolean))
  if (pinned.size !== marks.filter((m) => m.slot).length || [...pinned].some((n) => !(n >= 1 && n <= 8))) {
    throw new RangeError('render: pinned slots must be distinct, from 1 to 8')
  }
  let next = 1
  return marks.map((m) => {
    if (m.slot) return m.slot
    while (pinned.has(next)) next++
    return next++
  })
}

function legendSvg(names, slots) {
  let x = 0
  const items = names.map((n, si) => {
    const item = `<g class="hp-legend-item"><rect class="hp-key hp-s${slots[si]}" x="${num(x)}" y="4" width="10" height="10" rx="2" fill="currentColor"/>` +
      `<text class="hp-legend-label" x="${num(x + 16)}" y="9" dy="0.32em" fill="currentColor" fill-opacity="0.7">${xmlText(n)}</text></g>`
    x += 16 + textWidth(n) + 14
    return item
  })
  return `<g class="hp-legend" aria-hidden="true">${items.join('')}</g>`
}

// timeTicks returns tick times on a whole step, about one per 70 px.
function timeTicks(d0, d1, plotWidth) {
  if (d0 === null) return []
  const step = TIME_STEPS.find((s) => (d1 - d0) / s <= plotWidth / 70) ?? TIME_STEPS.at(-1)
  const out = []
  for (let t = Math.ceil(d0 / step) * step; t <= d1; t += step) out.push(t)
  return out
}

function tickLabel(t, ticks) {
  const step = ticks.length > 1 ? ticks[1][1] - ticks[0][1] : 60_000
  if (step >= 86_400_000) return heading(t).split(',')[0]
  return clock(t, step < 60_000)
}

function describe(band, names, rows, y0, y1, unit) {
  if (!names.length) return 'Empty chart.'
  const list = names.map((n) => n || 'unnamed').join(', ')
  const span = rows.length ? ` from ${rows[0].t} to ${rows.at(-1).t}` : ''
  const kind = band ? `Bar chart of ${list}. ${rows.length} categories${span}.` : `Line chart of ${list}. X axis time (UTC)${span}.`
  return `${kind} Y axis ${number(y0)} to ${number(y1)}${unit}.`
}

// tableHtml is the table equivalent (spec §3.8): one row per hover row, in
// the same order with the same values, and stable row ids (spec §4.1).
function tableHtml(id, title, xLabel, names, rows, rowKeys) {
  const i = xmlAttr(id)
  const cell = (v) => (v === DASH
    ? `<td><span aria-hidden="true">${DASH}</span><span class="hp-sr"> no data</span></td>` // screen readers skip a bare dash
    : `<td>${xmlText(v)}</td>`)
  return `<table class="hp-table" id="${i}-table"><caption>${xmlText(title)}</caption>` +
    `<thead><tr><th scope="col">${xmlText(xLabel)}</th>${names.map((n) => `<th scope="col">${xmlText(n)}</th>`).join('')}</tr></thead><tbody>` +
    rows.map((r, j) => `<tr id="${i}-r-${rowKeys[j]}"><th scope="row">${xmlText(r.t)}</th>${r.v.map(([, , v]) => cell(v)).join('')}</tr>`).join('') +
    '</tbody></table>'
}
