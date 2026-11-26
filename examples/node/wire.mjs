// wire.mjs: the hx-plot wire helpers for Node, a port of the Python helpers
// in docs/spec.md Appendix B (same names in camelCase), plus the key
// derivation of Appendix A and readers for the hp-* request parameters.
//
// Everything a server writes that the extension compares as text goes
// through these: row keys, the data-hp-* geometry attributes, escaped names
// and the hover JSON. Get them wrong and selects stop matching, windows
// snap instead of sliding, or a series name breaks the markup.
//
// Times are epoch milliseconds (plain numbers) throughout, as in Date.

// ---- Value forms (spec §1.1) ----------------------------------------------

/**
 * number writes a finite number in the spec's number form. JavaScript's
 * String(v) *is* that form (the spec defines it as Number.prototype.toString),
 * so this only refuses NaN and the infinities. String(-0) is already "0".
 */
export function number(v) {
  if (!Number.isFinite(v)) throw new RangeError(`not a finite number: ${v}`)
  return String(v)
}

/**
 * timeValue writes epoch ms as an RFC 3339 time in UTC with Z: a fraction
 * only when non-zero, without trailing zeros (spec §1.1). Sub-millisecond
 * fractions are kept to the microsecond, as Python's datetime keeps them.
 */
export function timeValue(ms) {
  if (!Number.isFinite(ms)) throw new RangeError(`not a time: ${ms}`)
  const us = Math.round(ms * 1000) // integer microseconds; exact for years 1 to 9999
  const sec = Math.floor(us / 1e6)
  const d = new Date(sec * 1000)
  const year = d.getUTCFullYear()
  if (year < 1 || year > 9999) throw new RangeError(`year ${year} is outside 1 to 9999`)
  let s = d.toISOString().slice(0, 19) // YYYY-MM-DDTHH:MM:SS
  const frac = us - sec * 1e6
  if (frac) s += '.' + String(frac).padStart(6, '0').replace(/0+$/, '')
  return s + 'Z'
}

/** epochMs writes a time (epoch ms or a Date) as a number of epoch ms, for data-hp-x. */
export function epochMs(t) {
  return number(t instanceof Date ? t.getTime() : t)
}

// ---- Parsing (spec §5.3) --------------------------------------------------

const TIME = /^([0-9]{4})-([0-9]{2})-([0-9]{2})[Tt]([0-9]{2}):([0-9]{2})(?::([0-9]{2})(?:\.([0-9]+))?)?([Zz]|[+-][0-9]{2}:[0-9]{2})?$/
const MINUTE = 60_000
const DAY = 86_400_000

/**
 * parseTimeValue reads an RFC 3339 date-time (any offset, any number of
 * fraction digits) or the HTML datetime-local form (no offset, seconds
 * optional), which is read in the IANA time zone tz. It returns epoch ms;
 * digits past microseconds are dropped. It throws a RangeError for anything
 * else, including dates that do not exist (Feb 30, 24:00) and years outside
 * 1 to 9999, as Python's datetime does.
 */
export function parseTimeValue(s, tz = 'UTC') {
  const m = TIME.exec(s)
  if (!m || (m[8] && !m[6])) throw new RangeError(`not a time value: ${s}`) // an offset needs seconds
  const [y, mo, d, h, mi, sec] = m.slice(1, 7).map((v) => Number(v ?? 0))
  const us = Number((m[7] ?? '0').slice(0, 6).padEnd(6, '0'))
  const wall = utcFields(y, mo, d, h, mi, sec)
  if (wall === null) throw new RangeError(`no such date or time: ${s}`)
  let t
  const off = m[8]
  if (off === undefined) {
    t = tz === 'UTC' ? wall : zonedToUtc(wall, tz)
  } else if (off === 'Z' || off === 'z') {
    t = wall
  } else {
    const oh = Number(off.slice(1, 3))
    const om = Number(off.slice(4, 6))
    if (oh > 23 || om > 59) throw new RangeError(`bad offset: ${s}`) // Python: |offset| < 24 h
    t = wall - (off[0] === '-' ? -1 : 1) * (oh * 60 + om) * MINUTE
  }
  const year = new Date(t).getUTCFullYear()
  if (year < 1 || year > 9999) throw new RangeError(`outside years 1 to 9999 in UTC: ${s}`)
  return t + us / 1000
}

// utcFields returns the epoch ms of a UTC wall time, or null when a field
// is out of range. Date.UTC would read years 0 to 99 as 1900 to 1999, so
// the year is set separately.
function utcFields(y, mo, d, h, mi, s) {
  if (y < 1 || mo < 1 || mo > 12 || d < 1 || h > 23 || mi > 59 || s > 59) return null
  const date = new Date(0)
  date.setUTCFullYear(y, mo - 1, d)
  date.setUTCHours(h, mi, s, 0)
  if (date.getUTCMonth() !== mo - 1 || date.getUTCDate() !== d) return null // Feb 30 rolled over
  return date.getTime()
}

const zoneFormats = new Map()

// offsetAt returns tz's UTC offset in ms at the instant t.
function offsetAt(t, tz) {
  let f = zoneFormats.get(tz)
  if (!f) {
    f = new Intl.DateTimeFormat('en-US', {
      timeZone: tz, hourCycle: 'h23', year: 'numeric', month: 'numeric', day: 'numeric',
      hour: 'numeric', minute: 'numeric', second: 'numeric',
    })
    zoneFormats.set(tz, f)
  }
  const whole = Math.floor(t / 1000) * 1000
  const p = Object.fromEntries(f.formatToParts(whole).map(({ type, value }) => [type, Number(value)]))
  return utcFields(p.year, p.month, p.day, p.hour, p.minute, p.second) - whole
}

// zonedToUtc reads a wall time (as UTC fields, in ms) in tz, the way
// JavaScript's Date and Python's fold=0 do (spec §5.3): a wall time the
// clocks repeat is the first of its two instants, and one they skip is read
// with the offset in effect before the change. It assumes no zone changes
// its clocks twice within two days.
function zonedToUtc(wall, tz) {
  const before = offsetAt(wall - DAY, tz)
  const after = offsetAt(wall + DAY, tz)
  const fits = [before, after].map((o) => wall - o).filter((t) => wall - offsetAt(t, tz) === t)
  return fits.length ? Math.min(...fits) : wall - before
}

const NUMBER = /^-?(?:0|[1-9][0-9]*)(?:\.[0-9]+)?(?:[eE][+-]?[0-9]+)?$/

/**
 * parseNumber reads a finite JSON number. Number() alone also takes
 * 'Infinity', '0x10', ' 2', '' (as 0) and '.5', which are not JSON numbers.
 */
export function parseNumber(s) {
  if (!NUMBER.test(s)) throw new RangeError(`not a JSON number: ${s}`)
  const v = Number(s)
  if (!Number.isFinite(v)) throw new RangeError(`not finite: ${s}`) // 1e999
  return v
}

// ---- Escaping (spec §3.6) -------------------------------------------------

// xmlChars drops what XML 1.0 cannot carry: C0 controls other than TAB,
// LF and CR, lone surrogates, U+FFFE and U+FFFF. Iterating a string with
// for...of walks code points, so a lone surrogate arrives on its own.
function xmlChars(s) {
  let out = ''
  for (const c of s) {
    const o = c.codePointAt(0)
    if (o === 9 || o === 10 || o === 13 || (o >= 0x20 && o <= 0xd7ff) || (o >= 0xe000 && o <= 0xfffd) || o >= 0x10000) out += c
  }
  return out
}

/**
 * xmlText escapes element text. An HTML escaper alone is not enough: it
 * leaves control characters and raw CRs, which break the XML and SSE lines.
 */
export function xmlText(s) {
  return xmlChars(String(s)).replaceAll('&', '&amp;').replaceAll('<', '&lt;').replaceAll('>', '&gt;').replaceAll('\r', '&#13;')
}

/** xmlAttr escapes a double-quoted attribute value; TAB and LF survive attribute normalization as references. */
export function xmlAttr(s) {
  return xmlText(s).replaceAll('"', '&quot;').replaceAll('\t', '&#9;').replaceAll('\n', '&#10;')
}

/**
 * wireText replaces each lone surrogate with U+FFFD (spec §3.5). JavaScript
 * strings can hold them (a JSON.parse of an escaped surrogate, say), and
 * neither UTF-8 nor XML can. Apply it to every string you hash into a key;
 * tipsJson applies it itself.
 */
export function wireText(s) {
  return String(s).toWellFormed()
}

const JSON_ESCAPES = ['<', '>', '&', String.fromCharCode(0xfffe), String.fromCharCode(0xffff)]

/**
 * tipsJson writes the hp-tips payload: compact JSON with <, > and &
 * escaped, so the text needs no XML escaping and can never close the
 * element (spec §3.5). JSON.stringify already escapes C0 controls.
 */
export function tipsJson(payload) {
  let s = JSON.stringify(payload, (k, v) => (typeof v === 'string' ? wireText(v) : v))
  for (const c of JSON_ESCAPES) s = s.replaceAll(c, '\\u' + c.charCodeAt(0).toString(16).padStart(4, '0'))
  return s
}

/**
 * sseMessage frames one unnamed server-sent event: a data: line per line of
 * the fragment, split at CRLF, CR and LF as hx-sse splits them (spec §4.5).
 */
export function sseMessage(fragment) {
  return fragment.split(/\r\n|\r|\n/).map((line) => 'data: ' + line + '\n').join('') + '\n'
}

// ---- Keys (spec §4.1, Appendix A) -----------------------------------------

const utf8 = new TextEncoder() // writes U+FFFD for a lone surrogate, like wireText

/** fnv1a is the 32-bit FNV-1a hash of s's UTF-8 bytes, as 8 lowercase hex digits. */
export function fnv1a(s) {
  let h = 0x811c9dc5
  for (const c of utf8.encode(wireText(s))) h = Math.imul(h ^ c, 0x01000193) >>> 0
  return h.toString(16).padStart(8, '0')
}

/** keys returns the keys of one key set, in order: a hash already taken becomes the hash of "s#n" (spec §4.1). */
export function keys(strings) {
  const taken = new Set()
  return strings.map((s) => {
    let k = fnv1a(s)
    for (let n = 1; taken.has(k); n++) k = fnv1a(`${s}#${n}`)
    taken.add(k)
    return k
  })
}

// ---- Request parameters (spec §4.5, §4.6, §5.1) -----------------------------

export const MIN_SIZE = 50
export const MAX_SIZE = 4096

// sizeParam reads hp-w or hp-h: unusable values give the default, usable
// ones are clamped to [50, 4096] (spec §4.6).
function sizeParam(s, def) {
  let v
  try {
    v = parseNumber((s ?? '').trim())
  } catch {
    return def
  }
  return v > 0 ? Math.min(Math.max(v, MIN_SIZE), MAX_SIZE) : def
}

/** readSize returns [width, height] from hp-w and hp-h (URLSearchParams), or the defaults. */
export function readSize(params, defW, defH) {
  return [sizeParam(params.get('hp-w'), defW), sizeParam(params.get('hp-h'), defH)]
}

/** readSelect returns a select's raw values (spec §5.2); ok is false when hp-x is missing or empty. */
export function readSelect(params) {
  const x = params.get('hp-x') ?? ''
  return { ok: x !== '', chart: params.get('hp-chart') ?? '', x, series: params.get('hp-series') ?? '' }
}

/** readBrush returns a brush's raw values (spec §5.3); ok is false unless both hp-x0 and hp-x1 are present. */
export function readBrush(params) {
  const x0 = params.get('hp-x0') ?? ''
  const x1 = params.get('hp-x1') ?? ''
  return { ok: x0 !== '' && x1 !== '', chart: params.get('hp-chart') ?? '', x0, x1 }
}

/** brushTimes parses a brush on a time axis into ordered epoch ms; it throws when either end does not parse. */
export function brushTimes(b, tz = 'UTC') {
  return [parseTimeValue(b.x0, tz), parseTimeValue(b.x1, tz)].sort((p, q) => p - q)
}

/** brushNumbers parses a brush on a linear axis into ordered numbers; it throws when either end does not parse. */
export function brushNumbers(b) {
  return [parseNumber(b.x0), parseNumber(b.x1)].sort((p, q) => p - q)
}

/** bareTag reduces a polling tag as hx-ptag compares it: trimmed, no W/, no surrounding quotes (spec §4.5). */
export function bareTag(tag) {
  let s = (tag ?? '').trim()
  if (s.startsWith('W/')) s = s.slice(2)
  if (s.length >= 2 && s[0] === '"' && s.at(-1) === '"') s = s.slice(1, -1)
  return s
}
