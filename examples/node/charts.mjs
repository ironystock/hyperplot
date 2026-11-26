// charts.mjs: the example's four charts, as render() descriptions built
// from data.mjs for a time window. Ids are explicit and stable ('latency',
// 'throughput', 'requests', 'detail', as in the Python example): htmx's
// morph matches the old frame to the new one by id, and a gesture sends it
// as hp-chart, so an id is part of the wire (spec/spec.md §4.1, §5.1).
import { clock, heading, render } from './render.mjs'
import {
  LATENCY_LIVE, LATENCY_NAMES, LATENCY_STEP, MINUTE, REGIONS, REQUEST_HISTORY, REQUEST_MINUTES,
  SECOND, THROUGHPUT_LIVE, THROUGHPUT_NAMES, THROUGHPUT_STEP, latency, requests, requestsIn, throughput,
} from './data.mjs'

export const HEIGHT = 260
export const DETAIL_HEIGHT = 200

// Zooms: a zoomed latency chart uses the smallest bucket that keeps it at
// or under ZOOM_POINTS points, so zooming in reveals the 5 s buckets.
export const MIN_ZOOM = 30 * SECOND
export const MAX_ZOOM = 6 * 60 * MINUTE
export const HISTORY = 24 * 60 * MINUTE // how far back a zoom may reach: the data "kept"
const ZOOM_POINTS = 120
const ZOOM_STEPS = [5, 10, 15, 30, 60, 120, 300, 600].map((s) => s * SECOND)

/** liveWindow is the latency chart's sliding window at now: the last 5 minutes, ending on a bucket. */
export function liveWindow(now) {
  const end = Math.floor(now / LATENCY_STEP) * LATENCY_STEP
  return [end - LATENCY_LIVE, end]
}

/**
 * zoomWindow turns a requested range into one this chart can show, or
 * null. A range that parses is still input (spec/spec.md §7): it is
 * bounded by the data before it drives any date arithmetic. Whole seconds,
 * at least MIN_ZOOM (widened around its middle, but never past the last
 * whole second), at most MAX_ZOOM (keeping its newest end), and inside the
 * last HISTORY up to now.
 *
 * A window this returns comes back unchanged when fed in again later
 * (until it ages out of HISTORY). The zoom's poller URL carries it as
 * from/to, so its polls draw the same window and get 304s from the first
 * one on.
 */
export function zoomWindow(t0, t1, now) {
  if (!(t0 < now && t1 > now - HISTORY)) return null // nothing of it is in the data
  const last = Math.floor(now / SECOND) * SECOND
  t0 = Math.max(Math.floor(t0 / SECOND) * SECOND, Math.ceil((now - HISTORY) / SECOND) * SECOND)
  t1 = Math.min(Math.ceil(t1 / SECOND) * SECOND, last)
  if (t1 - t0 < MIN_ZOOM) {
    t0 = Math.floor(((t0 + t1) / 2 - MIN_ZOOM / 2) / SECOND) * SECOND
    t1 = t0 + MIN_ZOOM
    // A short brush at the live edge would reach into the future: slide
    // it back so it ends now.
    if (t1 > last) [t0, t1] = [last - MIN_ZOOM, last]
  }
  if (t1 - t0 > MAX_ZOOM) t0 = t1 - MAX_ZOOM
  return [t0, t1]
}

/** latencyChart is the polled line chart for the window [t0, t1]. */
export function latencyChart(t0, t1, width, zoomed) {
  const step = zoomed ? ZOOM_STEPS.find((s) => (t1 - t0) / s <= ZOOM_POINTS) ?? ZOOM_STEPS.at(-1) : LATENCY_STEP
  // From the last point before the window (overscan, drawn and clipped,
  // so the line reaches the left edge while it slides; spec §4.4) to the
  // first at or after its end.
  const times = []
  for (let t = Math.floor(t0 / step) * step - (t0 % step === 0 ? step : 0); t <= Math.ceil(t1 / step) * step; t += step) times.push(t)
  const values = times.map((t) => latency(t / LATENCY_STEP))
  return {
    id: 'latency',
    title: 'Request latency',
    width,
    height: HEIGHT,
    animate: true, // hp-anim: slide moved windows (spec §6.8)
    x: { timeDomain: [t0, t1] },
    y: { unit: ' ms', zero: true },
    marks: LATENCY_NAMES.map((name, s) => ({ type: 'line', name, x: { times }, y: values.map((v) => v[s]) })),
  }
}

/** throughputChart is the streamed line chart for the minute ending at the second end. */
export function throughputChart(end, width) {
  const t0 = end - THROUGHPUT_LIVE
  const times = []
  for (let t = t0 - THROUGHPUT_STEP; t <= end; t += THROUGHPUT_STEP) times.push(t)
  const values = times.map((t) => throughput(t / SECOND))
  return {
    id: 'throughput',
    title: 'Throughput',
    width,
    height: HEIGHT,
    animate: true,
    x: { timeDomain: [t0, end] },
    y: { unit: ' req/s', zero: true, format: (v) => `${v} req/s` },
    marks: THROUGHPUT_NAMES.map((name, s) => ({ type: 'line', name, x: { times }, y: values.map((v) => v[s]) })),
  }
}

/** closedMinutes returns the indexes (epoch ms / 60000) of the last n closed minutes at now, oldest first. */
export function closedMinutes(now, n) {
  const open = Math.floor(now / MINUTE)
  return Array.from({ length: n }, (_, k) => open - n + k)
}

/** minuteLabel is a minute's category: "14:05". Categories are keys the server matches on select. */
export const minuteLabel = (m) => clock(m * MINUTE, false)

/** requestsChart is the polled grouped bars: requests per region in each of the last closed minutes. */
export function requestsChart(now, width) {
  const minutes = closedMinutes(now, REQUEST_MINUTES)
  const categories = minutes.map(minuteLabel)
  return {
    id: 'requests',
    title: 'Requests by region',
    width,
    height: HEIGHT,
    x: { label: 'Minute (UTC)' },
    y: { format: (v) => String(v) },
    // Each region pins its colour slot, so the detail below shows it in the same colour.
    marks: REGIONS.map((name, r) => ({ type: 'bar', name, slot: r + 1, x: { categories }, y: minutes.map((m) => requests(r, m)) })),
  }
}

/**
 * findMinute matches a select's hp-x against the categories this server
 * wrote (spec/spec.md §5.2): one of the last REQUEST_HISTORY closed
 * minutes, so a stale page can still drill into a minute that has scrolled
 * off. Returns the minute's index, or null for an unknown value.
 */
export function findMinute(label, now) {
  return closedMinutes(now, REQUEST_HISTORY).find((m) => minuteLabel(m) === label) ?? null
}

/** detailChart is the drill-down: requests per 10 s within minute m, for the given regions. */
export function detailChart(m, regions, width) {
  const times = Array.from({ length: 6 }, (_, k) => m * MINUTE + k * 10 * SECOND)
  return {
    id: 'detail',
    title: `Requests per 10 s, ${heading(m * MINUTE).slice(0, -3)}`,
    width,
    height: DETAIL_HEIGHT,
    x: { timeDomain: [times[0], times.at(-1)] },
    y: { zero: true, format: (v) => String(v) },
    marks: regions.map((name) => {
      const r = REGIONS.indexOf(name)
      return { type: 'line', name, slot: r + 1, x: { times }, y: times.map((t) => requestsIn(r, t / (10 * SECOND))) }
    }),
  }
}

export { render }
