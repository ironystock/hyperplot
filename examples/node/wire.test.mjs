// wire.test.mjs: the wire helpers against the spec's own examples and the
// server cases of spec/conformance/params.json.
import assert from 'node:assert/strict'
import { test } from 'node:test'
import { html, raw } from './page.mjs'
import { fixture } from './testing.mjs'
import {
  bareTag, brushNumbers, brushTimes, fnv1a, keys, number, parseNumber, parseTimeValue, readBrush, readSelect,
  readSize, sseMessage, timeValue, tipsJson, xmlAttr, xmlText,
} from './wire.mjs'

const params = JSON.parse(fixture('params.json'))

test('number writes the number form (spec §1.1)', () => {
  const cases = [[1, '1'], [2.0, '2'], [2.5, '2.5'], [0.001, '0.001'], [1e21, '1e+21'], [1e-7, '1e-7'], [-0, '0'],
    [1772546400000, '1772546400000'], [0.1 + 0.2, '0.30000000000000004'], [1000, '1000']]
  for (const [v, want] of cases) assert.equal(number(v), want)
  for (const bad of [NaN, Infinity, -Infinity]) assert.throws(() => number(bad))
})

test('timeValue writes RFC 3339 in UTC with a minimal fraction', () => {
  assert.equal(timeValue(Date.UTC(2026, 2, 3, 14, 5)), '2026-03-03T14:05:00Z')
  assert.equal(timeValue(Date.UTC(2026, 2, 3, 14, 5) + 250), '2026-03-03T14:05:00.25Z')
  assert.equal(timeValue(Date.UTC(2026, 2, 3, 14, 5) + 0.001), '2026-03-03T14:05:00.000001Z')
  assert.throws(() => timeValue(Date.UTC(10000, 0, 1)))
})

test('keys follow Appendix A, collisions included', () => {
  assert.equal(fnv1a('p50'), '2e119f3a')
  assert.equal(fnv1a('p50#1'), '4e3e42de')
  assert.equal(fnv1a('Q1'), '9bfcdfb7')
  assert.equal(fnv1a('2026-03-03T14:00:00Z'), 'f9457bc2')
  assert.deepEqual(keys(['p50', 'p50']), ['2e119f3a', '4e3e42de'])
  assert.deepEqual(keys(['Q1149599', 'Q1312382']), ['e0864ec2', 'eb6f4726'])
  // A lone surrogate hashes as U+FFFD (spec §3.5), so it never throws.
  assert.equal(fnv1a('a' + String.fromCharCode(0xdc00)), fnv1a('a' + String.fromCharCode(0xfffd)))
})

test('escaping: text, attributes and the hover JSON (spec §3.5, §3.6)', () => {
  const nasty = '<a href="x">&</a>\t\n\r' + String.fromCharCode(1) + 'z'
  assert.equal(xmlText(nasty), '&lt;a href="x"&gt;&amp;&lt;/a&gt;\t\n&#13;z')
  assert.equal(xmlAttr(nasty), '&lt;a href=&quot;x&quot;&gt;&amp;&lt;/a&gt;&#9;&#10;&#13;z')
  const json = tipsJson({ t: '</metadata><b>&', u: String.fromCharCode(0xd800) })
  assert.ok(!/[<>&]/.test(json))
  assert.deepEqual(JSON.parse(json), { t: '</metadata><b>&', u: String.fromCharCode(0xfffd) })
})

test('sseMessage splits at every line break (spec §4.5)', () => {
  assert.equal(sseMessage('a\r\nb\rc\nd'), 'data: a\ndata: b\ndata: c\ndata: d\n\n')
})

test('the html`` template escapes what it interpolates', () => {
  const name = '"><script>x</script>'
  assert.equal(html`<p title="${name}">${name}</p>`.toString(),
    '<p title="&quot;&gt;&lt;script&gt;x&lt;/script&gt;">&quot;&gt;&lt;script&gt;x&lt;/script&gt;</p>')
  assert.equal(html`${raw('<b>ok</b>')}${[1, 2]}${null}${false}`.toString(), '<b>ok</b>12')
})

test('bareTag compares tags as hx-ptag does', () => {
  for (const t of ['"hp-1a"', 'hp-1a', 'W/"hp-1a"', '  "hp-1a" ']) assert.equal(bareTag(t), 'hp-1a')
  assert.equal(bareTag(undefined), '')
})

test('params.json: every server case', () => {
  for (const c of params.server) {
    const q = new URLSearchParams(c.query)
    const at = c.query
    if (c.size) assert.deepEqual(readSize(q, ...c.size.default), c.size.want, at)
    if (c.select) {
      const s = readSelect(q)
      assert.equal(s.ok, c.select.ok, at)
      if (c.select.ok) {
        assert.deepEqual({ chart: s.chart, x: s.x, series: s.series }, { chart: c.select.chart, x: c.select.x, series: c.select.series }, at)
        if (c.select.time) assert.equal(timeValue(parseTimeValue(s.x)), c.select.time, at)
      }
    }
    if (c.brush) {
      const b = readBrush(q)
      assert.equal(b.ok, c.brush.ok, at)
      if (!c.brush.ok) continue
      assert.deepEqual({ chart: b.chart, x0: b.x0, x1: b.x1 }, { chart: c.brush.chart, x0: c.brush.x0, x1: c.brush.x1 }, at)
      if (c.brush.times === 'error') assert.throws(() => brushTimes(b), RangeError, at)
      else if (c.brush.times) assert.deepEqual(brushTimes(b).map(timeValue), c.brush.times, at)
      if (c.brush.timesIn) assert.deepEqual(brushTimes(b, c.brush.timesIn.zone).map(timeValue), c.brush.timesIn.times, at)
      if (c.brush.floats === 'error') assert.throws(() => brushNumbers(b), RangeError, at)
      else if (c.brush.floats) assert.deepEqual(brushNumbers(b), c.brush.floats, at)
    }
  }
})

test('parseNumber takes JSON numbers only', () => {
  assert.equal(parseNumber('-0.5e-2'), -0.005)
  for (const bad of ['', ' 1', '1.', '.5', '+1', '0x10', '1_0', 'NaN', 'Infinity', '1e999', '01']) {
    assert.throws(() => parseNumber(bad), RangeError, bad)
  }
})
