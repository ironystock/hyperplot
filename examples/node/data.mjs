// data.mjs: the example's stand-in data, and its clock.
//
// Every value is a pure function of (series, bucket index): the same bucket
// always has the same value, whatever the request order. That is what makes
// polling cheap. An unchanged window renders the same bytes, hashes to the
// same HX-PTag and costs a 304 (spec/spec.md §4.4). A real server would
// query its own store here; the wire details do not change.

export const SECOND = 1000
export const MINUTE = 60 * SECOND

// noise returns a value in [0, 1) for (series, i): a small integer hash
// (murmur3's finalizer), so neighbouring buckets look unrelated.
export function noise(series, i) {
  let h = Math.imul(i | 0, 0x9e3779b1) ^ Math.imul(series + 1, 0x85ebca6b)
  h ^= h >>> 16
  h = Math.imul(h, 0x7feb352d)
  h ^= h >>> 15
  h = Math.imul(h, 0x846ca68b)
  h ^= h >>> 16
  return (h >>> 0) / 2 ** 32
}

// ---- Latency: a polled line chart -------------------------------------------

export const LATENCY_STEP = 5 * SECOND // one point per 5 s bucket
export const LATENCY_LIVE = 5 * MINUTE // the live window
export const LATENCY_NAMES = ['p50', 'p95']

/** latency returns [p50, p95] in ms for the 5 s bucket with index i (epoch ms / 5000). */
export function latency(i) {
  const p50 = 110 + 25 * Math.sin(i / 9) + 20 * noise(0, i)
  const spike = noise(2, i) > 0.93 ? 140 * noise(3, i) : 0
  return [round1(p50), round1(p50 * 1.7 + 45 * noise(1, i) + spike)]
}

// ---- Throughput: a streamed line chart --------------------------------------

export const THROUGHPUT_STEP = SECOND
export const THROUGHPUT_LIVE = MINUTE
export const THROUGHPUT_NAMES = ['api', 'worker']

/** throughput returns [api, worker] in requests per second for the second with index i. */
export function throughput(i) {
  return [
    Math.round(820 + 160 * Math.sin(i / 17) + 90 * noise(10, i)),
    Math.round(310 + 70 * Math.sin(i / 11 + 1) + 40 * noise(11, i)),
  ]
}

// ---- Requests: grouped bars by minute, with a drill-down --------------------

export const REGIONS = ['us-east', 'eu-west', 'ap-south']
export const REQUEST_MINUTES = 6 // closed minutes shown as bars
export const REQUEST_HISTORY = 60 // closed minutes a select may name

/** requests returns the request count of region r in the minute with index m (epoch ms / 60000). */
export function requests(r, m) {
  let n = 0
  for (let k = 0; k < 6; k++) n += requestsIn(r, m * 6 + k)
  return n
}

/** requestsIn returns the request count of region r in the 10 s slice with index j (epoch ms / 10000). */
export function requestsIn(r, j) {
  const base = [5200, 3900, 2100][r]
  return Math.round(base * (1 + 0.18 * Math.sin(j / 23 + r * 2)) * (0.9 + 0.2 * noise(20 + r, j)))
}

function round1(v) {
  return Math.round(v * 10) / 10
}

// ---- The clock ----------------------------------------------------------------

/**
 * fastClock returns a clock that starts at now() and runs speed times
 * faster, so --speed 10 shows a minute of data in six seconds.
 */
export function fastClock(now, speed) {
  const start = now()
  return () => start + (now() - start) * speed
}
