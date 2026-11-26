/*! hx-plot-brush 0.2.0 | MIT */
// hx-plot-brush: brushing for hx-plot.js (spec/spec.md §6.6). Inside an
// element with hx-plot:brush, a mouse or pen drag across a chart's plot, or
// Shift+Arrow, Shift+Home and Shift+End from a keyboard point and then
// Enter, marks an x range and dispatches htmx:plot:brush with detail
// {chart, x0, x1}; hx-plot.js then adds hp-chart, hp-x0 and hp-x1 to the
// request that the event triggers. Only charts whose x axis is a time or
// linear scale can be brushed.
//
// It is a companion of hx-plot.js, kept in its own file so that the core
// stays within its size budget, and it does nothing on its own. Load it
// after hx-plot.js:
//
//	<script src="/hx-plot.js"></script>
//	<script src="/hx-plot-brush.js"></script>
(function brush() {
  'use strict'
  const G = globalThis

  // ---- Pure helpers (no DOM). Exported for tests. ----

  /**
   * timeValue writes epoch milliseconds as a spec time value: RFC 3339 in
   * UTC with Z, with a fraction only when it is not zero and without
   * trailing zeros ("2026-03-03T14:05:00Z", "2026-03-03T14:07:27.059Z").
   */
  const timeValue = (ms) => new Date(ms).toISOString().replace(/\.?0*Z$/, 'Z')

  /**
   * invert returns the data value drawn at viewBox x position px on frame
   * f (from hx-plot.js frameOf), with px clamped to the plot.
   */
  const invert = ({ plot: [l, , r], d0, d1 }, px) => d0 + ((Math.min(r, Math.max(l, px)) - l) / (r - l)) * (d1 - d0)

  /**
   * project returns the viewBox x position of data value v on frame f.
   */
  const project = ({ plot: [l, , r], d0, d1 }, v) => l + ((v - d0) / (d1 - d0)) * (r - l)

  /**
   * texts orders the data values vs and writes them as frame f's x axis
   * requires (spec/spec.md §6.6): time values rounded to whole
   * milliseconds, or numbers rounded to the fewest decimals (0 to 15) that
   * still resolve a tenth of a pixel.
   */
  function texts(f, vs) {
    vs = [...vs].sort((a, b) => a - b)
    if (f.kind === 'time') return vs.map((v) => timeValue(Math.round(v)))
    const step = (f.d1 - f.d0) / (10 * (f.plot[2] - f.plot[0]))
    let d = 0
    while (d < 15 && 10 ** -d > step) d++
    return vs.map((v) => String(+v.toFixed(d)))
  }

  /**
   * brushValues returns the two ends of a brush from viewBox x positions
   * px0 and px1 on frame f: inverted through the plot and the x domain,
   * ordered, rounded and written as texts does.
   */
  const brushValues = (f, px0, px1) => texts(f, [invert(f, px0), invert(f, px1)])

  if (globalThis.__HYPERPLOT_TEST__) G.__hyperplotTest = { ...G.__hyperplotTest, timeValue, brushValues }
  // Only alongside a started hx-plot.js (htmx may have refused it), once.
  // (An own property only: an element named "hyperplot" is not the script.)
  const H = Object.prototype.hasOwnProperty.call(G, 'hyperplot') ? G.hyperplot : null
  if (typeof document === 'undefined' || !H || H.brush) return
  // hx-plot.js may decide its mode only once the page is parsed (it waits
  // for an htmx loaded later); it runs this script again when it starts.
  if (!H._) return void (H.later ||= []).push(brush)

  // ---- Browser wiring. ----

  const doc = document
  const on = (type, fn, opts) => doc.addEventListener(type, fn, opts)
  let drag = null // a pointer brush in progress: {svg, id, p (pointer id), v (the press, a data value), cx, x}
  let range = null // a keyboard range in progress: {svg, k (the anchor row's key), i (its index)}
  let ended = null // the chart whose pointer brush just ended: its click selects nothing

  // frame returns the chart's geometry when it can be brushed: inside an
  // hx-plot:brush element, with a time or linear x axis.
  const frame = (I, svg) => {
    const f = svg && I.attr(svg, 'brush') != null && I.frame(svg)
    return f?.ok ? f : null
  }

  // draw shows rect.hp-brush across the plot's full height between viewBox
  // x positions x0 and x1, clamped to the plot.
  function draw(I, svg, f, x0, x1) {
    const [l, t, r, b] = f.plot
    const c = (x) => Math.min(r, Math.max(l, x))
    const lo = c(Math.min(x0, x1))
    I.attrs(I.own(svg, 'rect', 'hp-brush', { fill: 'currentColor', 'fill-opacity': 0.1 }), {
      x: lo,
      y: t,
      width: c(Math.max(x0, x1)) - lo,
      height: b - t,
    })
  }

  const clear = (svg) => svg?.querySelector('.hp-brush')?.remove()

  // stop ends a pointer drag. A drag cancelled before its release (Escape,
  // or a swap that made the chart unbrushable) still holds pointer capture,
  // so the release clicks the chart; that click must select nothing either.
  function stop(cancelled) {
    if (drag) {
      const st = drag.svg.style
      st.removeProperty?.('user-select')
      // An emptied style attribute would stay in the page (and in copies of it).
      if (!st.length) drag.svg.removeAttribute('style')
    }
    if (drag && cancelled) ended = drag.svg
    drag = null
  }

  // track redraws a pointer drag on the chart's current frame: the press
  // keeps its data value, so a swap during the drag moves its anchor with
  // the data. It cancels the drag when the chart is gone or cannot be
  // brushed any more.
  function track(I) {
    const d = drag
    const f = frame(I, d.svg.isConnected && d.svg)
    const p = f && I.vb(d.svg, d.x, 0)
    if (!p) return clear(d.svg), stop(true)
    draw(I, d.svg, f, project(f, d.v), p.x)
  }

  // mine reports whether a pointer event belongs to the drag in progress.
  const mine = (e) => drag && drag.p === e.pointerId

  // A primary-button mouse or pen press on a brushable chart, within its
  // plot's vertical span, starts a drag; a press beside the plot starts at
  // the plot's edge. Any press on a chart removes its previous brush.
  on('pointerdown', (e) => {
    const I = H._
    const svg = I?.chart(e.target)
    if (!svg) return
    ended = null
    clear(svg)
    if (range?.svg === svg) range = null
    const f = !e.button && /mouse|pen/.test(e.pointerType) && frame(I, svg)
    const p = f && I.vb(svg, e.clientX, e.clientY)
    if (!p || p.y < f.plot[1] || p.y > f.plot[3]) return
    drag = { svg, p: e.pointerId, v: invert(f, p.x), cx: e.clientX, x: e.clientX }
    try {
      svg.setPointerCapture(e.pointerId)
    } catch {
      // A synthetic press has no active pointer to capture.
    }
    // No text selection during a drag.
    svg.style.userSelect = 'none'
    e.preventDefault()
  })
  on(
    'pointermove',
    (e) => {
      if (!mine(e)) return
      drag.x = e.clientX
      track(H._)
    },
    { passive: true },
  )
  // Releasing after 4 CSS px or more of horizontal travel dispatches the
  // brush, with the release read through the frame current then; less
  // travel is a click. So is a drag whose two ends are the same value once
  // clamped to the plot (one that stayed beside it): an empty range is no
  // brush.
  on('pointerup', (e) => {
    const d = drag
    if (!mine(e)) return
    stop()
    const I = H._
    const f = frame(I, d.svg)
    const p = f && I.vb(d.svg, e.clientX, e.clientY)
    const [x0, x1] = p ? texts(f, [d.v, invert(f, p.x)]) : []
    if (!p || Math.abs(e.clientX - d.cx) < 4 || x0 === x1) return clear(d.svg)
    ended = d.svg
    draw(I, d.svg, f, project(f, d.v), p.x)
    I.emit(d.svg, 'htmx:plot:brush', { chart: d.svg.id, x0, x1 })
  })
  const lost = (e) => {
    if (mine(e)) clear(drag.svg), stop()
  }
  on('pointercancel', lost)
  on('lostpointercapture', lost)

  const rowKey = (row) => row?.k ?? row?.t

  // spans resolves the keyboard range on its chart's current frame: the
  // anchor row (by key, nearest its old index) and the active row, in
  // viewBox x order, with their x positions. It returns null, and cancels
  // the range, when the anchor's row or the keyboard point is gone.
  function spans(I) {
    const c = I.cur()
    const svg = c?.src === 'k' && c.svg
    const t = svg && range.svg.id === svg.id && I.tips(svg)
    let a = -1
    for (let j = 0; t && j < t.rows.length; j++) {
      if (rowKey(t.rows[j]) === range.k && (a < 0 || Math.abs(j - range.i) < Math.abs(a - range.i))) a = j
    }
    // (Malformed metadata can have fewer rows than points.)
    const f = a >= 0 && t.rows[c.i] && frame(I, svg)
    if (!f) return clear(range.svg), (range = null)
    range.svg = svg
    range.i = a
    const x = (j) => (t.mode === 'xy' ? t.pts[j][0] : t.xs[j])
    const ends = [a, c.i].sort((p, q) => x(p) - x(q) || p - q)
    return { svg, t, f, ends, xs: ends.map(x) }
  }

  // The hooks hx-plot.js calls (with its internal API I first).
  H.brush = {
    // key extends a keyboard range after a Shift+Arrow, Shift+Home or
    // Shift+End step (prev is the keyboard point before the step, the
    // anchor of a new range), draws it and announces it. Any other step
    // ends the range. It returns true when it announced.
    key(I, e, svg, prev) {
      if (!e.shiftKey || !frame(I, svg)) {
        if (range) clear(range.svg), (range = null)
        return false
      }
      const a = prev || I.cur()
      if (!range || range.svg !== svg || !prev) range = { svg, k: rowKey(I.tips(svg)?.rows[a.i]), i: a.i }
      const s = spans(I)
      if (!s) return false
      draw(I, svg, s.f, ...s.xs)
      const [t0, t1] = s.ends.map((j) => s.t.rows[j].t)
      I.say(I.settings().strings.range.replace(/\{([01])\}/g, (_, n) => (n > 0 ? t1 : t0)))
      return true
    },
    // enter commits a keyboard range on Enter: htmx:plot:brush with the two
    // rows' keys, lower x first. It returns true when it did, so that Enter
    // does not also select.
    enter(I, svg) {
      const s = range?.svg === svg && spans(I)
      const [k0, k1] = s ? s.ends.map((j) => s.t.rows[j].k) : []
      if (!s || s.ends[0] === s.ends[1] || k0 == null || k1 == null) return false
      range = null
      I.emit(svg, 'htmx:plot:brush', { chart: svg.id, x0: k0, x1: k1 })
      return true
    },
    // cancel ends any brush and removes every brush rectangle (Escape).
    cancel() {
      stop(true)
      range = null
      for (const el of doc.querySelectorAll('rect.hp-brush')) el.remove()
    },
    // click reports whether a click ends a pointer brush, so that it does
    // not also select.
    click(I, e, svg) {
      const s = ended
      ended = null
      return s === svg && e.detail > 0
    },
    // swap redraws a brush in progress after htmx changed the DOM (the morph
    // removed the rectangle): a pointer drag is re-projected through the new
    // frame, a keyboard range follows its anchor's key. A finished brush's
    // rectangle stays removed.
    swap(I) {
      if (drag) track(I)
      const s = range && spans(I)
      if (s) draw(I, s.svg, s.f, ...s.xs)
    },
  }
})()
