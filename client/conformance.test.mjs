// The client expectations of the conformance fixtures (testdata/conformance,
// docs/spec.md): every "client" section of a checks.json and the "client"
// cases of params.json, run against the reference files through the real
// scripts (hx-plot.js and hx-plot-brush.js) in the fake browser of
// fakedom.mjs. Each reference chart is drawn 1:1, so client coordinates
// are viewBox coordinates. Run with: node --test
import { test } from 'node:test'
import assert from 'node:assert/strict'
import { readFileSync } from 'node:fs'
import { load, Matrix } from './fakedom.mjs'

globalThis.__HYPERPLOT_TEST__ = true
await import('./hx-plot.js')
const { parseTips, frameOf } = globalThis.__hyperplotTest

const FIXTURES = new URL('../testdata/conformance/', import.meta.url)
const read = (path) => readFileSync(new URL(path, FIXTURES), 'utf8')
const json = (path) => JSON.parse(read(path))
const plain = (v) => JSON.parse(JSON.stringify(v))
const near = (got, want, tol, what) => assert.ok(Math.abs(got - want) <= tol, `${what}: ${got} != ${want}`)

const CHARTS = ['line-time', 'bars-band', 'scatter-xy', 'escaping', 'empty']

// page loads the client and a fixture's chart, inside an element with
// hx-plot:brush that is also the swap target.
function page(file) {
  const env = load({ companions: ['brush'] })
  const wrap = env.el('div', { 'hx-plot:brush': '' }, env.doc.body)
  const svg = env.parse(read(file), wrap)
  svg.ctm = new Matrix()
  return { env, svg, wrap }
}

const gestures = (env, type) => env.events.filter((e) => e.type === type)

// requestFor runs htmx_config_request for a request that event triggered and
// returns the parameters the extension added.
function requestFor(env, event) {
  const body = new FormData()
  env.ext.htmx_config_request(env.doc.body, {
    ctx: { request: { action: '/x', body }, target: env.doc.body, sourceEvent: { type: event.type, detail: event.detail } },
  })
  return body
}

// morph swaps target the way htmx does, with the svg's attributes and
// content replaced by the next frame's (the svg element itself stays).
function morph(env, target, svg, xml) {
  const task = { type: 'main', target, swapSpec: { style: 'innerMorph' } }
  const ctx = {}
  env.ext.htmx_before_swap(target, { ctx, tasks: [task] })
  const next = env.parse(xml)
  for (const k of [...svg.attrs.keys()]) svg.removeAttribute(k)
  for (const [k, v] of next.attrs) svg.setAttribute(k, v)
  svg.replaceChildren(...next.children)
  env.ext.htmx_before_settle(target, { task, newContent: [], settleTasks: [] })
  env.ext.htmx_after_swap(target, { ctx })
}

const midY = (svg) => {
  const [, top, , bottom] = svg.getAttribute('data-hp-plot').split(' ').map(Number)
  return (top + bottom) / 2
}

for (const name of CHARTS) {
  const checks = json(`${name}/checks.json`)
  const c = checks.client
  const file = `${name}/chart.svg`

  test(`${name}: geometry and payload`, () => {
    const { svg } = page(file)
    if (c.geometry) {
      const f = frameOf((k) => svg.getAttribute(k))
      assert.deepEqual(f.plot, c.geometry.plot)
      if (c.geometry.x) assert.deepEqual([f.kind, ...(f.ok ? [f.d0, f.d1] : [])], c.geometry.x)
      else assert.equal(svg.getAttribute('data-hp-x'), null)
      const [kind, y0, y1] = svg.getAttribute('data-hp-y').split(' ')
      assert.deepEqual([kind, +y0, +y1], c.geometry.y)
    }
    const t = parseTips(svg.querySelector('metadata.hp-tips').textContent)
    assert.equal(t.mode, c.tips.mode)
    assert.equal(t.mode === 'xy' ? t.pts.length : t.xs.length, c.tips.n)
    assert.deepEqual(t.ord, c.tips.ord)
    assert.deepEqual(
      t.rows.map((r) => r.k),
      c.tips.k,
    )
  })

  for (const h of c.hover || []) {
    test(`${name}: hover at ${h.at}`, () => {
      const { env, svg } = page(file)
      env.fire('pointermove', { target: svg, clientX: h.at[0], clientY: h.at[1], pointerType: 'mouse' })
      env.frame()
      assert.equal(env.internal.cur()?.i ?? -1, h.index, h.note)
    })
  }

  for (const h of c.tooltip || []) {
    test(`${name}: tooltip at ${h.at}`, () => {
      const { env, svg } = page(file)
      env.fire('pointermove', { target: svg, clientX: h.at[0], clientY: h.at[1], pointerType: 'mouse' })
      env.frame()
      const [head, ...rows] = env.tip.children
      assert.equal(head.className, 'hp-tip-t')
      assert.equal(head.textContent, h.heading)
      assert.deepEqual(
        rows.map((r) => {
          const [key, value, label] = r.children
          assert.deepEqual([r.className, key.className, value.localName, label.className], ['hp-tip-row', `hp-key hp-s${h.rows[0][0]}`, 'b', 'hp-tip-n'])
          return [Number(key.className.slice(-1)), label.textContent, value.textContent]
        }),
        h.rows,
      )
      // Nothing in the tooltip was made from data: only the client's elements.
      for (const el of env.tip.descendants()) assert.ok(/^(div|span|b)$/.test(el.localName) && el.children.length <= 3)
    })
  }

  for (const [i, s] of (c.select || []).entries()) {
    test(`${name}: select ${i + 1} (${[s.keys, s.click && `click ${s.click}`].filter(Boolean).join(', ')})`, () => {
      const { env, svg } = page(file)
      svg.focus()
      for (const k of s.keys || []) env.key(k)
      if (s.click) {
        const target = s.target === 'svg' ? svg : svg.querySelector(s.target)
        assert.ok(target, s.target)
        env.click(target, s.click[0], s.click[1], s.clickDetail ?? 1)
      }
      const got = gestures(env, 'htmx:plot:select')
      assert.deepEqual(plain(got.map((e) => e.detail)), s.detail ? [s.detail] : [], s.note)
      if (s.params) {
        const body = requestFor(env, got[0])
        assert.deepEqual(Object.fromEntries(body), s.params)
        if (s.query) assert.equal(new URLSearchParams(body).toString(), s.query)
      }
    })
  }

  for (const [i, b] of (c.brush || []).entries()) {
    test(`${name}: brush ${i + 1} (${b.from} to ${b.to})`, () => {
      const { env, svg } = page(file)
      env.drag(svg, b.from, b.to, midY(svg))
      const got = gestures(env, 'htmx:plot:brush')
      assert.deepEqual(plain(got.map((e) => e.detail)), b.detail ? [b.detail] : [], b.note)
      if (!b.detail) return
      assert.deepEqual(gestures(env, 'htmx:plot:select'), [], 'the click that ends a brush selects nothing')
      if (b.params) assert.deepEqual(Object.fromEntries(requestFor(env, got[0])), b.params)
      if (b.rect) {
        const r = svg.querySelector('rect.hp-brush')
        for (const [k, v] of Object.entries(b.rect)) near(+r.getAttribute(k), v, 0.01, k)
      }
    })
  }

  for (const [i, b] of (c.keyboardBrush || []).entries()) {
    test(`${name}: keyboard brush ${i + 1} (${b.keys.join(', ')})`, () => {
      const { env, svg } = page(file)
      svg.focus()
      for (const k of b.keys) env.key(k)
      const got = gestures(env, 'htmx:plot:brush')
      assert.deepEqual(plain(got.map((e) => e.detail)), b.detail ? [b.detail] : [], b.note)
      assert.deepEqual(gestures(env, 'htmx:plot:select'), [], 'Enter commits the range instead of selecting')
      if (b.rect) {
        const r = svg.querySelector('rect.hp-brush')
        for (const [k, v] of Object.entries(b.rect)) near(+r.getAttribute(k), v, 0.01, k)
      }
    })
  }
}

const slide = json('slide/checks.json').client
const frameFile = (name) => json('slide/checks.json').reference?.[name] ?? `${name}.svg`

test('slide: the anchored elements of frame-b start where frame-a drew their data', () => {
  const { env, svg, wrap } = page('slide/' + frameFile('frame-a'))
  morph(env, wrap, svg, read('slide/' + frameFile(slide.anchored.frame)))
  const anims = env.doc.animations
  assert.equal(anims.length, slide.anchored.count)
  const anchored = new Set(slide.anchored.selectors.flatMap((s) => svg.querySelectorAll(s)))
  assert.deepEqual(new Set(anims.map((a) => a.el)), anchored)
  for (const s of slide.anchored.notAnchored) {
    for (const el of svg.querySelectorAll(s)) assert.ok(!el.animations.length, s)
  }
  const nums = (s) => s.match(/-?[\d.]+(?:e-?\d+)?/g).map(Number)
  for (const e of slide.anchored.effects) {
    const el = svg.querySelectorAll(e.sel).find((x) => e.text == null || x.textContent === e.text)
    const [a] = el.animations
    assert.equal(/scaleX/.test(a.keyframes[0].transform), e.effect === 'affine', e.sel)
    nums(a.keyframes[0].transform).forEach((v, i) => near(v, nums(e.transform)[i], 1e-4, e.sel))
    assert.equal(a.keyframes[1].transform, 'none')
  }
  for (const p of slide.anchored.positions) {
    const el = svg.querySelectorAll(p.sel).find((x) => x.textContent === p.text)
    assert.equal(+el.getAttribute(p.attr), p.to)
    near(p.to + nums(el.animations[0].keyframes[0].transform)[0], p.from, slide.anchored.positionTolerance, p.text)
  }
})

test('slide: a pointer brush keeps its press as a data value across a swap', () => {
  const b = slide.brushAcrossSwap
  const { env, svg, wrap } = page('slide/' + frameFile(b.before))
  let anchor
  env.drag(svg, b.from, b.to, midY(svg), {
    between: () => {
      morph(env, wrap, svg, read('slide/' + frameFile(b.after)))
      anchor = +svg.querySelector('rect.hp-brush').getAttribute('x')
    },
  })
  near(anchor, b.anchorAfter, 0.01, 'the rectangle is redrawn from the press value')
  assert.deepEqual(plain(gestures(env, 'htmx:plot:brush').map((e) => e.detail)), [b.detail], b.note)
})

const params = json('params.json')
for (const c of params.client) {
  test(`params: ${c.name}`, () => {
    const env = load({ config: c.metaCharacter ? { metaCharacter: c.metaCharacter } : {} })
    let target = env.el('div', {}, env.doc.body)
    if (c.autosize) {
      target = env.el('div', { 'hx-plot:autosize': c.autosize.mode }, env.doc.body)
      Object.assign(target, { clientWidth: c.autosize.width, clientHeight: c.autosize.height })
    }
    const body = new FormData()
    for (const [k, v] of Object.entries(c.body)) body.set(k, v)
    const ctx = { request: { action: c.action, body }, target, sourceEvent: c.event }
    env.ext.htmx_config_request(target, { ctx })
    const added = [...body].filter(([k]) => !(k in c.body))
    assert.deepEqual(added, Object.entries(c.adds), c.note)
    if (c.url) {
      // As htmx 4.0.2 builds a GET: body keys replace URL parameters, then append.
      const url = new URL(c.action, env.doc.baseURI)
      for (const k of body.keys()) url.searchParams.delete(k)
      for (const [k, v] of body) url.searchParams.append(k, v)
      assert.equal(url.pathname + url.search, c.url)
    }
  })
}
