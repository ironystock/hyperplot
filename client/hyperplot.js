/*! hyperplot 0.1.0 | MIT */
// The optional browser half of hyperplot. Charts are complete without it;
// this file adds hover and keyboard tooltips, a crosshair, a band highlight,
// state that survives htmx morph swaps and SSE messages, and responsive
// re-rendering. It reads the <metadata class="hp-tips"> JSON that the server
// writes into every chart (see docs/DESIGN.md, "Output structure").
//
// Plain ES2020, no dependencies, no build step. Pointer and keyboard features
// use event delegation on document, so they work with or without htmx. When
// htmx 4 is present the file registers the "hyperplot" extension. Load it
// after htmx:
//
//	<script src="https://cdn.jsdelivr.net/npm/htmx.org@4.0.2/dist/htmx.min.js"></script>
//	<script src="/hyperplot.js"></script>
(() => {
  'use strict'
  const G = globalThis
  const isNum = Number.isFinite

  // ---- Pure helpers (no DOM). Exported for tests. ----

  /**
   * nearestIndex returns the index of the value in the ascending array xs
   * that is closest to x, or -1 when xs is empty or x is not finite. On a
   * tie, including a run of equal values, it returns the lowest index.
   */
  function nearestIndex(xs, x) {
    const n = xs ? xs.length : 0
    if (!n || !isNum(x)) return -1
    let lo = 0
    let hi = n - 1
    while (lo < hi) {
      const mid = (lo + hi) >> 1
      if (xs[mid] < x) lo = mid + 1
      else hi = mid
    }
    // lo is the first value >= x (or the last value); its left neighbor may
    // be closer, and then the first of any run of equal values wins.
    if (lo > 0 && x - xs[lo - 1] <= xs[lo] - x) {
      lo--
      while (lo > 0 && xs[lo - 1] === xs[lo]) lo--
    }
    return lo
  }

  /**
   * nearestPoint returns the index of the point in pts ([[x, y], …]) closest
   * to (x, y) within distance r, or -1 when none is that close. On a tie it
   * returns the lower index. Points with NaN coordinates never match.
   */
  function nearestPoint(pts, x, y, r) {
    let best = -1
    let bd = r * r
    for (let i = 0; i < (pts ? pts.length : 0); i++) {
      const dx = pts[i][0] - x
      const dy = pts[i][1] - y
      const d = dx * dx + dy * dy
      if (d < bd || (d === bd && best < 0)) {
        best = i
        bd = d
      }
    }
    return best
  }

  const GAP = 12 // px between the anchor and the tooltip
  const EDGE = 8 // px kept clear at the viewport edges

  /**
   * placeTooltip positions a w×h box near the anchor (ax, ay) inside a vw×vh
   * viewport. It prefers above and to the right of the anchor, flips to the
   * left or below when that side would overflow, and finally clamps the box
   * inside the viewport. It returns {left, top} in the anchor's coordinates.
   */
  function placeTooltip(ax, ay, w, h, vw, vh) {
    let left = ax + GAP
    let top = ay - GAP - h
    if (left + w > vw - EDGE) left = ax - GAP - w
    if (top < EDGE) top = ay + GAP
    return {
      left: Math.max(EDGE, Math.min(left, vw - EDGE - w)),
      top: Math.max(EDGE, Math.min(top, vh - EDGE - h)),
    }
  }

  const arr = (a) => (Array.isArray(a) ? a : [])
  const str = (s) => (s == null ? '' : String(s))
  const num = (v) => (isNum(v) ? v : NaN)

  /**
   * keyOrder returns the order in which the keyboard steps through the points
   * of a scatter plot ([[x, y], …] in viewBox coordinates): left to right,
   * then bottom to top (larger viewBox y first) at the same x, then by index.
   * Points with NaN coordinates are left out, since they cannot be shown.
   */
  function keyOrder(pts) {
    const ord = []
    for (let i = 0; i < pts.length; i++) if (isNum(pts[i][0]) && isNum(pts[i][1])) ord.push(i)
    return ord.sort((a, b) => pts[a][0] - pts[b][0] || pts[b][1] - pts[a][1] || a - b)
  }

  /**
   * parseTips parses and normalizes the JSON payload of a chart's
   * <metadata class="hp-tips">. It never throws. It returns null when the
   * text is not JSON, the mode is unknown, or an x position is not a finite
   * number. Missing optional fields get defaults (bw 0, top and bottom null,
   * no rows), a malformed point gets NaN coordinates so that it never matches
   * but keeps its row aligned, and unknown fields are ignored. The result's
   * ord lists the point indices in keyboard order: index order for modes x
   * and band, and keyOrder for mode xy.
   */
  function parseTips(text) {
    let o
    try {
      o = JSON.parse(text)
    } catch {
      return null
    }
    if (!['x', 'band', 'xy'].includes(o?.mode)) return null
    const xs = arr(o.xs)
    if (!xs.every(isNum)) return null
    const span = isNum(o.top) && isNum(o.bottom)
    const pts = arr(o.pts).map((p) => [num(arr(p)[0]), num(arr(p)[1])])
    return {
      mode: o.mode,
      xs,
      pts,
      ord: o.mode === 'xy' ? keyOrder(pts) : xs.map((_, i) => i),
      bw: o.bw > 0 && isNum(o.bw) ? o.bw : 0,
      top: span ? Math.min(o.top, o.bottom) : null,
      bottom: span ? Math.max(o.top, o.bottom) : null,
      rows: arr(o.rows).map((r) => ({
        t: str(r?.t),
        v: arr(r?.v)
          .filter(Array.isArray)
          .map((e) => [Number.isInteger(e[0]) && e[0] > 0 && e[0] < 9 ? e[0] : 0, str(e[1]), str(e[2])]),
      })),
    }
  }

  /**
   * keepIndex returns the index a keyboard reader should stay on after the
   * chart's data changed: the row whose heading is key, nearest the old index
   * i, so a sliding window keeps the reader on the same x. When key is empty
   * or no row has it, it returns i clamped to the n points (-1 when n is 0).
   */
  function keepIndex(rows, key, i, n) {
    let best = -1
    for (let j = 0; key && j < Math.min(n, rows.length); j++) {
      if (rows[j].t === key && (best < 0 || Math.abs(j - i) < Math.abs(best - i))) best = j
    }
    return best >= 0 ? best : Math.min(i, n - 1)
  }

  /**
   * announcement returns the text a screen reader hears for a tooltip row:
   * the heading, then each series as "name value", for example
   * "Mar 3, 2:05 PM: p50 12.3 ms, p99 40 ms". A missing value, shown as an
   * en dash that screen readers skip, is spoken as "no data".
   */
  function announcement(row) {
    const vals = row.v
      .map(([, name, value]) => [name, value === '–' ? 'no data' : value])
      .map(([name, value]) => (name && value ? name + ' ' + value : name || value))
      .filter(Boolean)
    return [row.t, vals.join(', ')].filter(Boolean).join(': ')
  }

  if (G.__HYPERPLOT_TEST__) {
    G.__hyperplotTest = { nearestIndex, nearestPoint, placeTooltip, keyOrder, parseTips, keepIndex, announcement }
  }
  if (typeof document === 'undefined' || G.hyperplot) return
  G.hyperplot = { version: '0.1.0' }

  // ---- Browser wiring. ----

  const doc = document
  const on = (type, fn, opts) => doc.addEventListener(type, fn, opts)
  const HIT = 12 // px hit radius for dots (doubled for touch)
  const AUTO = '[data-hp-autosize]'
  const reduced = G.matchMedia?.('(prefers-reduced-motion: reduce)')
  const tipsCache = new WeakMap() // svg → {text, tips}
  const sizes = new WeakMap() // autosize element → {w, h, t}

  // cur is the one active chart: {svg, id, src, i, key, x, y, touch}. src is
  // "p" for pointer (x, y are client coordinates) or "k" for keyboard (i is
  // the point and key its row heading). One tooltip element serves every
  // chart on the page.
  let cur = null
  let shown = null // the svg whose crosshair or band highlight is visible
  let tip = null
  let tipFor = null // [tips, i] the tooltip was last filled for
  let live = null // the status region that announces keyboard steps
  let pending = null
  let frame = 0

  function tipsOf(svg) {
    const text = svg.querySelector('metadata.hp-tips')?.textContent
    if (!text) return null
    const c = tipsCache.get(svg)
    if (c?.text === text) return c.tips
    const tips = parseTips(text)
    tipsCache.set(svg, { text, tips })
    return tips
  }

  function attrs(el, a) {
    for (const k in a) el?.setAttribute(k, typeof a[k] === 'number' ? Math.round(a[k] * 100) / 100 : a[k])
  }

  function clearMarks(svg) {
    for (const el of svg.querySelectorAll('.hp-crosshair,.hp-band-hl,.hp-focus')) el.setAttribute('visibility', 'hidden')
  }

  // ringOf returns the ring that marks the active point of a scatter plot,
  // adding it to the chart's g.hp-hover on first use. The server does not
  // write it, so a morph swap removes it and the next render adds it again.
  // Its look comes from presentation attributes, which any stylesheet rule
  // overrides; currentColor follows the chart's text color, including in
  // forced colors. It never animates, so it keeps up with the tooltip.
  function ringOf(svg) {
    let el = svg.querySelector('.hp-focus')
    if (!el) {
      el = doc.createElementNS('http://www.w3.org/2000/svg', 'circle')
      attrs(el, {
        class: 'hp-focus',
        r: 7,
        fill: 'none',
        stroke: 'currentColor',
        'stroke-width': 2,
        'pointer-events': 'none',
        'aria-hidden': 'true',
        visibility: 'hidden',
      })
      el.style.transition = 'none'
      ;(svg.querySelector('.hp-hover') || svg).append(el)
    }
    return el
  }

  function make(tag, cls, text) {
    const el = doc.createElement(tag)
    el.className = cls
    if (text != null) el.textContent = text
    return el
  }

  // tipEl returns the visual tooltip. It is always hidden from assistive
  // technology: keyboard steps are announced through liveEl instead, and
  // pointer hover is not announced at all.
  function tipEl() {
    if (!tip?.isConnected) {
      tip = make('div', 'hp-tip')
      tip.setAttribute('aria-hidden', 'true')
      Object.assign(tip.style, { position: 'fixed', pointerEvents: 'none', display: 'none' })
      doc.body.append(tip)
      tipFor = null
    }
    return tip
  }

  // liveEl returns the visually hidden role="status" region that announces
  // keyboard steps. Screen readers announce changes only in a live region
  // that was already exposed before the change, so it is created empty at
  // startup (and again after every swap, should one have replaced the body),
  // is never display:none or aria-hidden, and is written only when the
  // reader moves.
  function liveEl() {
    if (!live?.isConnected && doc.body) {
      live = make('div', 'hp-live')
      live.setAttribute('role', 'status')
      Object.assign(live.style, {
        position: 'absolute',
        width: '1px',
        height: '1px',
        margin: '-1px',
        padding: '0',
        border: '0',
        overflow: 'hidden',
        clipPath: 'inset(50%)',
        whiteSpace: 'nowrap',
      })
      doc.body.append(live)
    }
    return live
  }

  // announce reads the active keyboard point aloud.
  function announce() {
    const row = cur?.src === 'k' ? tipsOf(cur.svg)?.rows[cur.i] : null
    const el = liveEl()
    if (el) el.textContent = row ? announcement(row) : ''
  }

  function fill(el, row) {
    const kids = row.t ? [make('div', 'hp-tip-t', row.t)] : []
    for (const [slot, name, value] of row.v) {
      // The key is an empty span, so it adds nothing to what is announced.
      const r = make('div', 'hp-tip-row')
      r.append(make('span', slot ? 'hp-key hp-s' + slot : 'hp-key'), make('b', 'hp-tip-v', value), make('span', 'hp-tip-n', name))
      kids.push(r)
    }
    el.replaceChildren(...kids)
  }

  function hide() {
    if (shown) clearMarks(shown)
    // Clearing the region lets the same row be announced again next time.
    if (cur?.src === 'k' && live?.textContent) live.textContent = ''
    shown = cur = null
    if (tip) tip.style.display = 'none'
  }

  // render draws the state in cur: it resolves the chart (by id when a swap
  // replaced the node), picks the point, and shows the marks and tooltip.
  function render() {
    const c = cur
    if (!c) return
    if (!c.svg.isConnected) {
      const svg = c.id ? doc.getElementById(c.id) : null
      if (!svg?.matches('svg.hp')) return hide()
      c.svg = svg
    }
    const svg = c.svg
    const t = tipsOf(svg)
    const m = svg.getScreenCTM()
    if (!t || !m) return hide()
    const xy = t.mode === 'xy'
    const n = xy ? t.pts.length : t.xs.length
    let i = c.i
    if (c.src === 'p') {
      const p = new DOMPoint(c.x, c.y).matrixTransform(m.inverse())
      if (xy) {
        i = nearestPoint(t.pts, p.x, p.y, (c.touch ? 2 * HIT : HIT) / Math.hypot(m.a, m.b))
      } else {
        // Hover counts inside the plot's vertical span and, for bands,
        // inside the outer bands.
        const half = t.mode === 'band' ? t.bw / 2 : Infinity
        const inside =
          (t.top == null || (p.y >= t.top && p.y <= t.bottom)) && p.x >= t.xs[0] - half && p.x <= t.xs[n - 1] + half
        i = inside ? nearestIndex(t.xs, p.x) : -1
      }
    } else {
      i = keepIndex(t.rows, c.key, i, n)
    }
    // A kept index can land on a point with no position after an update.
    if (i < 0 || (xy && !(isNum(t.pts[i][0]) && isNum(t.pts[i][1])))) return hide()
    c.i = i
    c.key = t.rows[i]?.t
    if (shown && shown !== svg) clearMarks(shown)
    shown = svg

    const ax = xy ? t.pts[i][0] : t.xs[i]
    const ay = xy ? t.pts[i][1] : t.top || 0
    const cross = svg.querySelector('.hp-crosshair')
    const band = svg.querySelector('.hp-band-hl')
    const x = t.mode === 'x'
    const b = t.mode === 'band' && t.top != null
    attrs(cross, x ? { x1: ax, x2: ax, visibility: 'visible' } : { visibility: 'hidden' })
    if (x && t.top != null) attrs(cross, { y1: t.top, y2: t.bottom })
    attrs(band, b ? { x: ax - t.bw / 2, width: t.bw, y: t.top, height: t.bottom - t.top, visibility: 'visible' } : { visibility: 'hidden' })
    // In mode xy the tooltip alone cannot tell neighboring dots apart, so a
    // ring marks the active one, for the pointer and the keyboard alike.
    attrs(xy ? ringOf(svg) : svg.querySelector('.hp-focus'), xy ? { cx: ax, cy: ay, visibility: 'visible' } : { visibility: 'hidden' })

    const el = tipEl()
    const row = t.rows[i]
    if (!row || (!row.t && !row.v.length)) {
      el.style.display = 'none'
      return
    }
    if (tipFor?.[0] !== t || tipFor[1] !== i) {
      fill(el, row)
      tipFor = [t, i]
    }
    el.style.display = ''
    const s = new DOMPoint(ax, ay).matrixTransform(m)
    // The tooltip follows the pointer vertically, except with reduced motion,
    // where it moves only when the active point changes.
    const sy = c.src === 'p' && !xy && !reduced?.matches ? c.y : s.y
    const root = doc.documentElement
    const pos = placeTooltip(s.x, sy, el.offsetWidth, el.offsetHeight, root.clientWidth, root.clientHeight)
    el.style.left = pos.left + 'px'
    el.style.top = pos.top + 'px'
  }

  function point(e) {
    const svg = e.target.closest?.('svg.hp')
    if (svg) {
      cur = { svg, id: svg.id, src: 'p', i: -1, x: e.clientX, y: e.clientY, touch: e.pointerType === 'touch' }
      render()
    } else if (cur?.src === 'p') hide()
  }

  // Pointer moves are coalesced to one render per animation frame.
  function onPointer(e) {
    pending = e
    if (!frame) {
      frame = requestAnimationFrame(() => {
        frame = 0
        if (pending) point(pending)
        pending = null
      })
    }
  }

  function onKey(e) {
    if (e.key === 'Escape') return hide()
    const svg = e.target
    if (!svg.matches?.('svg.hp') || e.altKey || e.ctrlKey || e.metaKey) return
    // The keys move through ord, the keyboard order of the points: index
    // order, or left to right for a scatter plot, whose payload lists the
    // points series by series.
    const ord = tipsOf(svg)?.ord || []
    const n = ord.length
    // Without an active point, the arrows start from the nearer end.
    const r = cur?.svg === svg ? ord.indexOf(cur.i) : -1
    const j = { ArrowRight: r < 0 ? 0 : r + 1, ArrowLeft: r < 0 ? n - 1 : r - 1, Home: 0, End: n - 1 }[e.key]
    if (!n || j == null) return
    e.preventDefault()
    cur = { svg, id: svg.id, src: 'k', i: ord[Math.max(0, Math.min(j, n - 1))] }
    render()
    announce()
  }

  // refresh re-applies the active state after htmx changed the DOM, so live
  // updates neither drop nor freeze an open tooltip, and starts observing new
  // autosize elements. It never announces: a focused live chart would
  // otherwise read its row again on every poll or SSE message.
  function refresh() {
    liveEl()
    scan()
    const old = cur?.svg
    render()
    if (cur?.src === 'k' && cur.svg !== old) cur.svg.focus({ preventScroll: true })
  }

  // ---- Autosize: hp:resize events and hp-w/hp-h request parameters. ----

  const ro =
    G.ResizeObserver &&
    new ResizeObserver((entries) => {
      for (const e of entries) resized(e.target)
    })

  // boxSize returns the content-box size of an autosize element, in CSS
  // pixels: its client size less padding, which is the room a chart has.
  function boxSize(el) {
    const cs = G.getComputedStyle?.(el)
    const pad = (a, b) => (cs ? (parseFloat(cs[a]) || 0) + (parseFloat(cs[b]) || 0) : 0)
    return {
      w: Math.max(0, Math.round(el.clientWidth - pad('paddingLeft', 'paddingRight'))),
      h: Math.max(0, Math.round(el.clientHeight - pad('paddingTop', 'paddingBottom'))),
    }
  }

  // fixedHeight reports whether an autosize element opted in to height
  // tracking with data-hp-autosize="both". A wrapper's height usually comes
  // from the chart inside it, so measuring it by default would ask for a
  // slightly taller chart on every request; only a wrapper whose height CSS
  // sets on its own (height, or aspect-ratio with overflow hidden) can opt in.
  const fixedHeight = (el) => el.getAttribute('data-hp-autosize') === 'both'

  function scan() {
    if (ro) {
      for (const el of doc.querySelectorAll(AUTO)) {
        if (!sizes.has(el)) {
          sizes.set(el, { ...boxSize(el), t: 0 })
          ro.observe(el)
        }
      }
    }
  }

  function resized(el) {
    const s = sizes.get(el)
    if (!s) return
    clearTimeout(s.t)
    if (!el.isConnected) {
      ro.unobserve(el)
      sizes.delete(el)
      return
    }
    s.t = setTimeout(() => {
      if (!el.isConnected) return
      const { w, h } = boxSize(el)
      const tall = fixedHeight(el) && h > 0 && Math.abs(h - s.h) >= 8
      if (!w || (Math.abs(w - s.w) < 8 && !tall)) return
      s.w = w
      s.h = h
      el.dispatchEvent(new CustomEvent('hp:resize', { detail: { width: w, height: h } }))
    }, 150)
  }

  // queryOf returns the query parameters of a request URL, or null.
  function queryOf(action) {
    if (typeof action !== 'string') return null
    try {
      return new URL(action, doc.baseURI).searchParams
    } catch {
      return null
    }
  }

  // addSize adds hp-w (and, for data-hp-autosize="both", hp-h) to a request
  // from (or targeting) an autosize element, unless the request already has
  // them in its body or its URL. ctx.request.body is a FormData here; for GET
  // and DELETE htmx then copies it into the URL, replacing any URL parameter
  // of the same name, so a value the author wrote into hx-get or
  // hx-sse:connect must be checked here or it would be lost.
  function addSize(elt, ctx) {
    const body = ctx?.request?.body
    const box = elt?.closest?.(AUTO) || ctx?.target?.closest?.(AUTO)
    if (!box || typeof body?.set !== 'function') return
    const { w, h } = boxSize(box)
    const query = queryOf(ctx.request.action)
    for (const [k, v] of [
      ['hp-w', w],
      ['hp-h', fixedHeight(box) ? h : 0],
    ]) {
      if (v > 0 && !body.has(k) && !query?.has(k)) body.set(k, v)
    }
  }

  // register is safe to call twice: htmx ignores a second registration.
  const register = () =>
    G.htmx?.registerExtension?.('hyperplot', {
      htmx_config_request(elt, detail) {
        addSize(elt, detail?.ctx)
      },
      htmx_after_swap() {
        refresh()
      },
      htmx_sse_after_message() {
        refresh()
      },
    })

  const passive = { passive: true }
  on('pointermove', onPointer, passive)
  on('pointerdown', onPointer, passive)
  on('pointerout', (e) => {
    // Touch pointers leave after every tap; their tooltip stays until the
    // next tap outside the chart.
    if (cur?.src !== 'p' || e.pointerType === 'touch') return
    if (cur.svg.contains(e.target) && !cur.svg.contains(e.relatedTarget)) {
      pending = null
      hide()
    }
  })
  on('pointercancel', () => cur?.src === 'p' && hide())
  on('keydown', onKey)
  on('focusout', (e) => {
    if (cur?.src !== 'k' || e.target !== cur.svg) return
    // A chart that lost focus because a swap replaced it is found again by id.
    if (e.target.isConnected) hide()
    else requestAnimationFrame(refresh)
  })
  // Keyboard tooltips follow their point when the page scrolls or resizes;
  // pointer tooltips close until the pointer moves again.
  const reflow = () => cur && (cur.src === 'k' ? render() : hide())
  G.addEventListener('scroll', reflow, { capture: true, passive: true })
  G.addEventListener('resize', reflow, passive)

  register()
  const ready = () => {
    register()
    liveEl()
    scan()
  }
  if (doc.readyState === 'loading') on('DOMContentLoaded', ready, { once: true })
  else ready()
})()
