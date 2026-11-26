/*! hx-plot 0.2.0 | MIT */
// hx-plot: the htmx 4 extension for server-rendered SVG charts (npm
// htmx-ext-plot). Charts are complete without it; this file adds hover and
// keyboard tooltips, a crosshair, a band highlight, state that survives htmx
// morph swaps and SSE messages, sliding windows that slide instead of
// wobbling, live data tables that stay at their newest row and stay open,
// responsive re-rendering, and select gestures that become htmx requests.
// The wire contract is spec/spec.md (spec 1): it reads the
// <metadata class="hp-tips"> JSON and the root's geometry attributes that a
// server writes into every chart.
//
// Plain ES2020, no dependencies, no build step. Pointer and keyboard features
// use event delegation on document, so they work with or without htmx. With
// htmx 4 the file registers the extension "plot" and starts only when htmx
// accepts it (htmx.config.extensions). Load it after htmx:
//
//	<script src="https://cdn.jsdelivr.net/npm/htmx.org@4.0.2/dist/htmx.min.js"></script>
//	<script src="/hx-plot.js"></script>
//
// Two optional companions load after it with their own script tags:
// hx-plot-brush.js (brush gestures, hx-plot:brush) and hx-plot-tween.js
// (JavaScript path tweens where CSS cannot transition d). They find this
// file through globalThis.hyperplot.
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
  // Only primitives become text: String() throws on a parsed object whose
  // own toString is not a function ({"toString": 1}).
  const str = (s) => (typeof s === 'string' ? s : isNum(s) || typeof s === 'boolean' ? String(s) : '')
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
   * no rows; a row without a key k gets null), a malformed point gets NaN
   * coordinates so that it never matches but keeps its row aligned, and
   * unknown fields are ignored. The result's ord lists the point indices in
   * keyboard order: index order for modes x and band, and keyOrder for mode
   * xy.
   */
  function parseTips(text) {
    try {
      const o = JSON.parse(text)
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
          // A key is a string or a number; anything else is no key.
          k: typeof r?.k === 'string' || isNum(r?.k) ? String(r.k) : null,
          v: arr(r?.v)
            .filter(Array.isArray)
            .map((e) => [Number.isInteger(e[0]) && e[0] > 0 && e[0] < 9 ? e[0] : 0, str(e[1]), str(e[2])]),
        })),
      }
    } catch {
      return null
    }
  }

  /** rowKey returns what identifies a row across frames: its key k, or its heading. */
  const rowKey = (row) => row?.k ?? row?.t

  /**
   * keepIndex returns the index a keyboard reader should stay on after the
   * chart's data changed: the row whose key (k, or the heading t for rows
   * without one) is key, nearest the old index i, so a sliding window keeps
   * the reader on the same x. When key is empty or no row has it, it returns
   * i clamped to the n points (-1 when n is 0).
   */
  function keepIndex(rows, key, i, n) {
    let best = -1
    for (let j = 0; key && j < Math.min(n, rows.length); j++) {
      if (rowKey(rows[j]) === key && (best < 0 || Math.abs(j - i) < Math.abs(best - i))) best = j
    }
    return best >= 0 ? best : Math.min(i, n - 1)
  }

  /**
   * frameOf reads a chart frame's geometry from its root attributes
   * (spec/spec.md §4.2); get(name) returns an attribute's value or null. It
   * returns {ok, anim, vb, plot: [left, top, right, bottom], kind, d0, d1}.
   * ok is false when the frame cannot slide or be brushed: it is not spec
   * version 1, its plot is not four finite numbers, or its x scale is not a
   * time or linear scale with finite d0 < d1.
   */
  function frameOf(get) {
    const words = (name) => str(get(name)).trim().split(/\s+/)
    const plot = words('data-hp-plot').map(Number)
    const [kind, d0, d1] = words('data-hp-x')
    const f = { anim: words('class').includes('hp-anim'), vb: words('viewBox').join(' '), plot, kind, d0: +d0, d1: +d1 }
    f.ok =
      get('data-hp') === '1' &&
      plot.length === 4 &&
      plot.every(isNum) &&
      plot[0] < plot[2] &&
      ['time', 'linear'].includes(kind) &&
      isNum(f.d0) &&
      isNum(f.d1) &&
      f.d0 < f.d1
    return f
  }

  /**
   * slideOf decides what a swap from frame o to frame n (both from frameOf)
   * does, by the rules of spec/spec.md §6.8.3: {outcome, reason} with outcome
   * "slide", "snap" or "none". A slide also carries a and b, which map a new
   * x to where the same data was drawn in o (x_old = a·x + b), s, the content
   * shift, and duration, the effective duration. Options: slide (the
   * setting), motion (false under reduced motion or in a view transition),
   * scale (CSS px per viewBox unit), interval (ms since the chart's last
   * swap) and duration (ms).
   */
  function slideOf(o, n, { slide = true, motion = true, scale = 1, interval = Infinity, duration = 350 } = {}) {
    const end = (outcome, reason, geo) => ({ outcome, reason, ...geo })
    if (!o.ok || !n.ok) return end('none', 'unsupported')
    if (!n.anim || !motion) return end('none', 'static')
    if (o.kind === n.kind && o.d0 === n.d0 && o.d1 === n.d1) return end('none', 'still')
    if (!slide) return end('snap', 'off')
    if (o.kind !== n.kind) return end('snap', 'kind')
    if (o.vb !== n.vb) return end('snap', 'size')
    const [ol, ot, or, ob] = o.plot
    const [nl, nt, nr, nb] = n.plot
    if (ot !== nt || ob !== nb) return end('snap', 'plot')
    const ko = (or - ol) / (o.d1 - o.d0)
    const s = (n.d0 - o.d0) * ko
    const a = ko / ((nr - nl) / (n.d1 - n.d0))
    const geo = { a, b: ol + s - a * nl, s }
    const shift = (x) => (a - 1) * x + geo.b
    if (!(a >= 0.9 && a <= 1.1)) return end('snap', 'scale', geo)
    if (Math.abs(s) > (or - ol) / 4) return end('snap', 'far', geo)
    if (Math.max(Math.abs(shift(nl)), Math.abs(shift(nr))) * scale < 0.5) return end('snap', 'tiny', geo)
    geo.duration = Math.min(duration, 0.8 * interval)
    if (!(geo.duration >= 60)) return end('snap', 'fast', geo)
    return end('slide', '', geo)
  }

  /**
   * startTransform returns the CSS transform that puts an element of the
   * new frame where its data was drawn in the old one, for a slide with a
   * and b (spec/spec.md §6.8.3). Paths, rects, polylines, polygons and
   * whole series groups take the affine map; anything else (text, circles,
   * lines) only moves, by (a - 1)·x + b with x its own x position, so
   * glyphs and dots are never squeezed.
   */
  function startTransform(tag, x, a, b) {
    const n = (v) => +v.toFixed(6)
    if (['path', 'rect', 'polyline', 'polygon', 'g'].includes(tag)) return `translateX(${n(b)}px) scaleX(${n(a)})`
    return `translateX(${n((a - 1) * (isNum(x) ? x : 0) + b)}px)`
  }

  /**
   * announcement returns the text a screen reader hears for a tooltip row:
   * the heading, then each series as "name value", for example
   * "Mar 3, 2:05 PM: p50 12.3 ms, p99 40 ms". A missing value, shown as an
   * en dash that screen readers skip, is spoken as noData.
   */
  function announcement(row, noData = 'no data') {
    const vals = row.v
      .map(([, name, value]) => [name, value === '–' ? noData : value])
      .map(([name, value]) => (name && value ? name + ' ' + value : name || value))
      .filter(Boolean)
    return [row.t, vals.join(', ')].filter(Boolean).join(': ')
  }

  /**
   * gestureParams returns the request parameters ([[name, value], …]) that
   * the request's triggering event ev adds (spec/spec.md §6.7): hp-chart,
   * hp-x and hp-series for htmx:plot:select, hp-chart, hp-x0 and hp-x1 for
   * htmx:plot:brush, and nothing for any other event. Event names are
   * compared as htmx dispatches them, with the metaCharacter mc applied.
   * Empty values are left out.
   */
  function gestureParams(ev, mc) {
    const is = (name) => ev?.type === (mc ? name.replace(/:/g, mc) : name)
    const d = ev?.detail || {}
    const p = is('htmx:plot:select')
      ? [['hp-chart', d.chart], ['hp-x', d.x], ['hp-series', d.series]]
      : is('htmx:plot:brush')
        ? [['hp-chart', d.chart], ['hp-x0', d.x0], ['hp-x1', d.x1]]
        : []
    return p.filter(([, v]) => v != null && v !== '').map(([k, v]) => [k, String(v)])
  }

  // (Written out in full so that the minified build can drop it.)
  if (globalThis.__HYPERPLOT_TEST__) {
    G.__hyperplotTest = {
      ...G.__hyperplotTest,
      nearestIndex,
      nearestPoint,
      placeTooltip,
      keyOrder,
      parseTips,
      keepIndex,
      announcement,
      frameOf,
      slideOf,
      startTransform,
      gestureParams,
    }
  }
  // The one global: the guard against loading the script twice, and how the
  // companion scripts find this one (H._ is their internal API, present only
  // once the extension has started; it is not a public interface). Only an
  // own property counts: an element with id or name "hyperplot" shows up as
  // window.hyperplot too, from the window's prototype chain.
  if (typeof document === 'undefined' || Object.prototype.hasOwnProperty.call(G, 'hyperplot')) return
  const H = (G.hyperplot = { version: '0.2.0' })

  // ---- Browser wiring. ----

  const doc = document
  // htmx (or null without it) and its config, set when the client decides
  // its mode (see the end of this file).
  let htmx = null
  let conf0 = {}
  const NS = 'http://www.w3.org/2000/svg'
  const on = (type, fn, opts) => doc.addEventListener(type, fn, opts)
  const passive = { passive: true }
  const HIT = 12 // px hit radius for dots (doubled for touch)
  const reduced = G.matchMedia?.('(prefers-reduced-motion: reduce)')
  const tipsCache = new WeakMap() // svg → {text, tips}
  const sizes = new WeakMap() // autosize element → {w, h, t}
  let api = null // htmx's extension API, from init

  // Settings (spec/spec.md §6.3): the defaults, then htmx.config.plot, then
  // the request's hx-config "plot" object; strings merge one level deep.
  const DEFAULTS = {
    slide: true,
    follow: true,
    keepOpen: true,
    duration: 350,
    easing: 'ease-out',
    strings: { noData: 'no data', range: '{0} to {1}' },
  }
  // A duration or easing that Element.animate() would refuse (a negative
  // duration, a JavaScript library's easing name) falls back to the
  // default, so that a configuration mistake never breaks a swap.
  const validEasing = (e) => typeof e === 'string' && (!G.CSS?.supports || CSS.supports('transition-timing-function', e))
  const settings = (ctx) => {
    const s = [conf0.plot, ctx?.request?.plot].reduce((a, o) => ({ ...a, ...o, strings: { ...a.strings, ...o?.strings } }), DEFAULTS)
    if (!(s.duration >= 0 && isNum(s.duration))) s.duration = DEFAULTS.duration
    if (!validEasing(s.easing)) s.easing = DEFAULTS.easing
    return s
  }

  // The container attributes hx-plot:autosize and hx-plot:brush in every
  // spelling htmx accepts: hx-plot and the prefixed data-hx-plot, joined to
  // the name by ":" or htmx.config.metaCharacter (spec/spec.md §6.2). Set
  // with the mode (spellings).
  let NAMES, SEL, IGNORE
  const selOf = (names) => names.map((n) => `[${CSS.escape(n)}]`).join()
  function spellings() {
    const prefix = conf0.prefix ?? 'data-hx-'
    // (A name may repeat; that is harmless in a selector list.)
    const spell = (name) =>
      ['hx-' + name, prefix && prefix + name].filter(Boolean).flatMap((s) => [s, conf0.metaCharacter ? s.replace(/:/g, conf0.metaCharacter) : s])
    NAMES = { autosize: spell('plot:autosize'), brush: spell('plot:brush') }
    SEL = { autosize: selOf(NAMES.autosize), brush: selOf(NAMES.brush) }
    // In htmx mode, charts and autosize elements inside hx-ignore are skipped.
    IGNORE = htmx && selOf(spell('ignore'))
  }
  const ignored = (el) => !!IGNORE && !!el.closest(IGNORE)

  /** attrOn returns the value of the container attribute name at or above el, or null. */
  function attrOn(el, name) {
    const box = el?.closest?.(SEL[name])
    return box ? NAMES[name].map((n) => box.getAttribute(n)).find((v) => v != null) : null
  }

  /** chartOf returns the chart that holds el, or null. */
  function chartOf(el) {
    const svg = el?.closest?.('svg.hp')
    return svg && !ignored(svg) ? svg : null
  }

  // emit dispatches one of the extension's events: through htmx, which
  // applies its metaCharacter, logs it and runs other extensions' hooks, or
  // as a plain CustomEvent without htmx (or with a pre-release htmx 4 whose
  // extension API lacks triggerHtmxEvent).
  const emit = (el, name, detail, bubbles = true) =>
    api?.triggerHtmxEvent
      ? api.triggerHtmxEvent(el, name, detail, bubbles)
      : el.dispatchEvent(new CustomEvent(name, { bubbles, cancelable: true, detail }))

  // cur is the one active chart: {svg, id, src, i, key, x, y, touch}. src is
  // "p" for pointer (x, y are client coordinates) or "k" for keyboard (i is
  // the point and key its row key, see rowKey). One tooltip element serves every
  // chart on the page.
  let cur = null
  let shown = null // the svg whose crosshair or band highlight is visible
  let tip = null
  let tipFor = null // [tips, i] the tooltip was last filled for
  let live = null // the status region that announces keyboard steps
  let pending = null
  let frame = 0
  let down = null // {svg, i, touch, series}: the active point (and series) when the last press began
  let saved = null // what the history cache's copy of the page left out (see unsave)

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

  const MARK = ['.hp-crosshair', '.hp-band-hl', '.hp-focus']
  const VISIBLE = MARK.map((s) => s + '[visibility="visible"]').join()

  function clearMarks(svg) {
    for (const el of svg.querySelectorAll(MARK.join())) el.setAttribute('visibility', 'hidden')
  }

  // own returns a client-owned element of class cls in the chart's
  // g.hp-hover (the ring that marks the active scatter point, or the brush
  // rectangle), adding it on first use with the attributes a. The server
  // does not write these, so a morph swap removes them and the next render
  // adds them again. Their look comes from presentation attributes, which
  // any stylesheet rule overrides; currentColor follows the chart's text
  // color, including in forced colors. They never animate.
  function own(svg, tag, cls, a) {
    let el = svg.querySelector('.' + cls)
    if (!el) {
      el = doc.createElementNS(NS, tag)
      attrs(el, { class: cls, 'pointer-events': 'none', 'aria-hidden': 'true', ...a })
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

  // host returns where the tooltip and the status region go for chart svg:
  // the open dialog or popover that holds it, or else the body. A modal
  // dialog or a popover is in the top layer and makes the rest of the page
  // inert, so a tooltip outside it would be painted under it and a status
  // region outside it would not be exposed.
  function host(svg) {
    let h = null
    try {
      h = svg?.closest('dialog[open],:popover-open')
    } catch {
      // A browser without popovers does not know the selector.
      h = svg?.closest('dialog[open]')
    }
    return h || doc.body
  }

  // place appends el to the host of chart svg unless it is there already.
  function place(el, svg) {
    const h = host(svg)
    if (h && el.parentNode !== h) h.append(el)
    return el
  }

  // tipEl returns the visual tooltip, placed for chart svg. It is always
  // hidden from assistive technology: keyboard steps are announced through
  // liveEl instead, and pointer hover is not announced at all. The pointer
  // can rest on it (WCAG 1.4.13, see toward). Its width is its content's,
  // not what is left of a dialog that contains it.
  function tipEl(svg) {
    if (!tip?.isConnected) {
      tip = make('div', 'hp-tip')
      tip.setAttribute('aria-hidden', 'true')
      tip.style.cssText = 'position:fixed;pointer-events:auto;width:max-content;display:none'
      tipFor = null
    }
    return place(tip, svg)
  }

  // liveEl returns the visually hidden role="status" region that announces
  // keyboard steps, placed for chart svg: by default the keyboard reader's
  // chart, else the focused chart (no step yet, or after Escape), else the
  // active one. Screen readers announce changes only in a live region that
  // was already exposed before the change, so it is created empty at
  // startup (and again after every swap, should one have replaced the
  // body), moved into a dialog when a chart there takes focus and never out
  // of it while that chart has focus, is never display:none or aria-hidden,
  // and is written only when the reader moves.
  function liveEl(svg = (cur?.src === 'k' && cur.svg) || chartOf(doc.activeElement) || cur?.svg) {
    if (!doc.body) return null
    if (!live?.isConnected) {
      live = make('div', 'hp-live')
      live.setAttribute('role', 'status')
      // Set through the CSSOM, which a strict style-src allows.
      live.style.cssText = 'position:absolute;width:1px;height:1px;margin:-1px;padding:0;border:0;overflow:hidden;clip-path:inset(50%);white-space:nowrap'
    }
    return place(live, svg)
  }

  // say writes text to the status region.
  function say(text) {
    const el = liveEl()
    if (el) el.textContent = text
  }

  // announce reads the active keyboard point aloud.
  function announce() {
    const row = cur?.src === 'k' ? tipsOf(cur.svg)?.rows[cur.i] : null
    say(row ? announcement(row, settings().strings.noData) : '')
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

  // hit returns the point that a pointer at client (x, y) activates on a
  // chart with payload t and screen matrix m, or -1 (spec/spec.md §6.5).
  function hit(t, m, x, y, touch) {
    const p = new DOMPoint(x, y).matrixTransform(m.inverse())
    if (t.mode === 'xy') return nearestPoint(t.pts, p.x, p.y, (touch ? 2 * HIT : HIT) / Math.hypot(m.a, m.b))
    // Hover counts inside the plot's vertical span and, for bands, inside
    // the outer bands.
    const half = t.mode === 'band' ? t.bw / 2 : Infinity
    const inside = (t.top == null || (p.y >= t.top && p.y <= t.bottom)) && p.x >= t.xs[0] - half && p.x <= t.xs[t.xs.length - 1] + half
    return inside ? nearestIndex(t.xs, p.x) : -1
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
    const i = c.src === 'p' ? hit(t, m, c.x, c.y, c.touch) : keepIndex(t.rows, c.key, c.i, n)
    // A kept index can land on a point with no position after an update.
    if (i < 0 || (xy && !(isNum(t.pts[i][0]) && isNum(t.pts[i][1])))) return hide()
    if (c.i !== i) c.since = now()
    c.i = i
    c.key = rowKey(t.rows[i])
    if (shown && shown !== svg) clearMarks(shown)
    shown = svg

    const ax = xy ? t.pts[i][0] : t.xs[i]
    const ay = xy ? t.pts[i][1] : t.top || 0
    const cross = svg.querySelector('.hp-crosshair')
    const x = t.mode === 'x'
    const b = t.mode === 'band' && t.top != null
    attrs(cross, x ? { x1: ax, x2: ax, visibility: 'visible' } : { visibility: 'hidden' })
    if (x && t.top != null) attrs(cross, { y1: t.top, y2: t.bottom })
    // The band highlight is a rect, which the stylesheet's value transitions
    // would animate; like the ring, it keeps up with the tooltip instead.
    const hl = svg.querySelector('.hp-band-hl')
    if (hl) hl.style.transition = 'none'
    attrs(
      hl,
      b ?{ x: ax - t.bw / 2, width: t.bw, y: t.top, height: t.bottom - t.top, visibility: 'visible' } : { visibility: 'hidden' },
    )
    // In mode xy the tooltip alone cannot tell neighboring dots apart, so a
    // ring marks the active one, for the pointer and the keyboard alike.
    attrs(
      xy ? own(svg, 'circle', 'hp-focus', { r: 7, fill: 'none', stroke: 'currentColor', 'stroke-width': 2 }) : svg.querySelector('.hp-focus'),
      xy ? { cx: ax, cy: ay, visibility: 'visible' } : { visibility: 'hidden' },
    )

    const el = tipEl(svg)
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
    const put = (l, t) => {
      el.style.left = l + 'px'
      el.style.top = t + 'px'
    }
    put(pos.left, pos.top)
    // In a dialog or popover with a transform, filter or contain, that
    // element, not the viewport, positions the fixed tooltip: measure where
    // it went and move it back by the difference.
    const r = el.parentNode !== doc.body && el.getBoundingClientRect?.()
    if (r && Math.abs(r.left - pos.left) + Math.abs(r.top - pos.top) > 0.5) put(2 * pos.left - r.left, 2 * pos.top - r.top)
  }

  let grace = 0 // the timer of a pointer on its way to the tooltip

  // toward reports whether pointer event e lies on the way from where the
  // pointer last chose the active point to the tooltip: inside the convex
  // hull of that position and the tooltip's box. A pointer that rested on a
  // point (400 ms: a reader, not a pointer scanning the chart) and then
  // moves there is heading for the tooltip (WCAG 1.4.13: hover content can
  // be hovered), so the tooltip stays as it is.
  function toward(e) {
    const c = cur
    const r = c?.src === 'p' && !c.on && !c.touch && now() - c.since >= 400 && tip?.style.display === '' && tip.getBoundingClientRect?.()
    if (!r) return false
    const q = [
      [r.left, r.top],
      [r.right, r.top],
      [r.right, r.bottom],
      [r.left, r.bottom],
    ]
    const side = (a, b) => (b[0] - a[0]) * (e.clientY - a[1]) - (b[1] - a[1]) * (e.clientX - a[0])
    const p = [c.x, c.y]
    // The hull is the union of the triangles from the position to each side.
    return q.some((a, i) => {
      const b = q[(i + 1) % 4]
      const s = [side(p, a), side(a, b), side(b, p)]
      return s.every((v) => v >= 0) || s.every((v) => v <= 0)
    })
  }

  // point makes the chart under pointer event e active at the pointer, or
  // hides a pointer tooltip off the charts. A pointer on the tooltip keeps
  // the point; one heading for it keeps it while it moves (300 ms grace).
  function point(e, late) {
    clearTimeout(grace)
    if (tip?.contains(e.target)) {
      if (cur) cur.on = true
      return
    }
    if (!late && toward(e)) {
      grace = setTimeout(() => point(e, true), 300)
      return
    }
    const svg = chartOf(e.target)
    if (svg) {
      // (Same chart: the active point and how long it has been active carry over.)
      const o = cur?.src === 'p' && cur.svg === svg ? cur : {}
      cur = { svg, id: svg.id, src: 'p', i: o.i ?? -1, since: o.since, x: e.clientX, y: e.clientY, touch: e.pointerType === 'touch' }
      render()
    } else if (cur?.src === 'p' && !vt) hide()
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

  // B calls the brush companion (hx-plot-brush.js), when it is loaded.
  const B = (fn, ...a) => H.brush?.[fn]?.(I, ...a)

  // seriesOf returns the data-hp-series of the series group holding el, or null.
  const seriesOf = (el) => el?.closest?.('g.hp-series')?.getAttribute('data-hp-series')

  // select dispatches htmx:plot:select for point i of a chart
  // (spec/spec.md §6.6). In mode xy the series is the point's own, found by
  // its slot; otherwise it is the series the reader clicked, if any.
  function select(svg, t, i, series) {
    const row = t?.rows[i]
    if (row?.k == null) return
    if (t.mode === 'xy') series = svg.querySelector('g.hp-series.hp-s' + row.v[0]?.[0])?.getAttribute('data-hp-series') ?? ''
    emit(svg, 'htmx:plot:select', { chart: svg.id, x: row.k, series, index: i })
  }

  // A click selects the point under it. A click from assistive technology
  // or script (detail 0) selects the active keyboard point instead, and a
  // tap selects only the point that was already active when it began, so
  // the first tap just shows the tooltip.
  function onClick(e) {
    const svg = chartOf(e.target)
    if (!svg || B('click', e, svg)) return
    const t = tipsOf(svg)
    const m = svg.getScreenCTM()
    let i = -1
    if (!e.detail) i = cur?.svg === svg && cur.src === 'k' ? cur.i : -1
    else if (t && m) {
      i = hit(t, m, e.clientX, e.clientY, down?.touch)
      if (down?.touch && (down.svg !== svg || down.i !== i)) i = -1
    }
    // The series under the click, or, when pointer capture (a brushable
    // chart's) moved the click to the svg, the one under the press.
    if (i >= 0) select(svg, t, i, (e.detail && (seriesOf(e.target) ?? (down?.svg === svg ? down.series : ''))) || '')
  }

  function onKey(e) {
    if (e.key === 'Escape') {
      B('cancel')
      return hide()
    }
    const svg = chartOf(e.target)
    if (!svg || svg !== e.target || e.altKey || e.ctrlKey || e.metaKey) return
    const t = tipsOf(svg)
    const k = cur?.svg === svg && cur.src === 'k' ? cur : null
    if (e.key === 'Enter') {
      if (k) {
        e.preventDefault()
        if (!B('enter', svg)) select(svg, t, k.i, '')
      }
      return
    }
    // The keys move through ord, the keyboard order of the points: index
    // order, or left to right for a scatter plot, whose payload lists the
    // points series by series.
    const ord = t?.ord || []
    const n = ord.length
    // Without an active point, the arrows start from the nearer end.
    const r = cur?.svg === svg ? ord.indexOf(cur.i) : -1
    const j = { ArrowRight: r < 0 ? 0 : r + 1, ArrowLeft: r < 0 ? n - 1 : r - 1, Home: 0, End: n - 1 }[e.key]
    if (!n || j == null) return
    e.preventDefault()
    const prev = k && { i: k.i, key: k.key }
    cur = { svg, id: svg.id, src: 'k', i: ord[Math.max(0, Math.min(j, n - 1))] }
    render()
    // With Shift on a brushable chart the brush companion extends a range
    // and announces it instead.
    if (!B('key', e, svg, prev)) announce()
  }

  // refresh re-applies the active state after htmx changed the DOM, so live
  // updates neither drop nor freeze an open tooltip, and starts observing new
  // autosize elements. It never announces: a focused live chart would
  // otherwise read its row again on every poll or SSE message.
  function refresh() {
    unsave() // (in case another extension cancelled the history cache's save)
    // A swap that restores a snapshot of the page (htmx's hx-history-cache
    // on Back) brings back copies of the tooltip, the status region and the
    // marks as they were when it was taken. The client tracks none of them.
    for (const el of doc.querySelectorAll('div.hp-tip,div.hp-live')) if (el !== tip && el !== live) el.remove()
    for (const el of doc.querySelectorAll(VISIBLE)) if (!shown?.contains(el)) el.setAttribute('visibility', 'hidden')
    liveEl()
    scan(doc.body)
    const old = cur?.svg
    render()
    if (cur?.src === 'k' && cur.svg !== old) cur.svg.focus({ preventScroll: true })
    B('swap')
  }

  // ---- Swaps: sliding windows and live tables (spec/spec.md §6.8). ----
  //
  // htmx runs htmx_before_swap with the old DOM, then morphs each task's
  // target and runs htmx_before_settle for it before the browser renders,
  // then htmx_after_swap once per response. Polls and SSE messages alike go
  // through these hooks. The client records each chart's old geometry and
  // each live table's scroll position before the swap, slides or snaps the
  // charts whose x window moved right after the morph, and scrolls tables
  // that were at their end back to it afterwards.

  const MARKS = 'path,circle,rect,line,text'
  // Elements that move with their data: the marks, dot labels, x tick labels
  // and server-added x-anchored elements, at any depth of their group. End
  // dots and direct labels sit at the newest point and move there at once.
  const ANCHORED = 'g.hp-labels text.hp-dot-label,g.hp-axis-x text.hp-tick,.hp-xa'
  const now = () => G.performance?.now?.() ?? Date.now()
  // A swap's record (see beforeSwap) rides on each of its htmx tasks under
  // this key. An extension that replaces a task with copies ({...task},
  // as htmx's hx-targets does) copies the key too; one that builds new
  // tasks gets the latest record.
  const REC = Symbol('hx-plot')
  let last = null // the latest swap's record
  const responses = new WeakMap() // htmx request context → its swap's record
  const lastSwap = new Map() // chart id → time of its last swap, for the effective duration
  const slides = new Map() // chart id → the animations of its running slide
  let morphing = DEFAULTS // the settings of the latest swap, for the morph hooks
  let vt = false // a view transition is running (its snapshot hides the page from the pointer)
  // T calls the tween companion (hx-plot-tween.js), when it is loaded.
  const T = (fn, ...a) => H.tween?.[fn]?.(I, ...a)

  const within = (root, sel) => [...(root.matches?.(sel) ? [root] : []), ...root.querySelectorAll(sel)]
  const attrOf = (el) => (name) => el.getAttribute(name)
  const overflows = (n) => /auto|scroll/.test(G.getComputedStyle?.(n)?.overflowY)

  // scroller returns the nearest element at or above el that scrolls
  // vertically: overflow-y auto or scroll, with content taller than its box.
  // When none has overflowing content yet, it returns the nearest with
  // overflow-y auto or scroll: a table that still fits its box is seen to
  // its end, and must stay so once it grows past it.
  function scroller(el) {
    let fits = null
    for (let n = el; n; n = n.parentElement) {
      if (overflows(n)) {
        if (n.scrollHeight > n.clientHeight) return n
        fits ||= n
      }
    }
    return fits
  }

  // beforeSwap records, before htmx changes the DOM, every chart's geometry
  // (by id) and every live table's scroll boxes, with the swap's settings.
  // It records the whole document rather than the tasks' targets, because
  // another extension may still change the targets (hx-targets).
  function beforeSwap(ctx, list) {
    const conf = settings(ctx)
    morphing = conf
    // (A 304 from hx-ptag swaps nothing: style none.)
    if (!list?.some((t) => t.swapSpec?.style !== 'none')) return
    const rec = {
      conf,
      // A view transition cross-fades snapshots; a slide under it would
      // show twice. beforeSettle applies the precedence htmx itself uses.
      main: list?.find((t) => t.type === 'main')?.transition,
      ctx: ctx?.transition,
      charts: new Map(),
      tables: new Map(),
      follow: [],
    }
    for (const svg of doc.querySelectorAll('svg.hp')) {
      if (svg.id) rec.charts.set(svg.id, { f: frameOf(attrOf(svg)), scale: svg.getScreenCTM?.()?.a || 1, pre: T('capture', svg) })
    }
    // Follow: each table's scroll boxes, innermost first, and whether each
    // is at its end (see follow).
    if (conf.follow) {
      for (const table of doc.querySelectorAll('table.hp-table')) {
        const boxes = []
        for (let n = table; n; n = n.parentElement) {
          if (overflows(n)) boxes.push([n, n.scrollHeight > n.clientHeight, n.scrollHeight - n.scrollTop - n.clientHeight <= 2])
        }
        rec.tables.set(table.id || table, boxes)
      }
    }
    for (const task of list || []) task[REC] = rec
    last = rec
    if (ctx) responses.set(ctx, rec)
  }

  // stopSlide ends a chart's running slide at once.
  function stopSlide(id) {
    for (const a of slides.get(id) || []) a?.cancel?.()
    slides.delete(id)
  }

  // unpin removes the inline styles a slide or snap set, once the browser
  // has computed the new frame's style, so no CSS transition starts.
  function unpin(svg, anims) {
    if (slides.get(svg.id) !== anims) return
    slides.delete(svg.id)
    for (const el of svg.querySelectorAll(MARKS)) {
      if (el.matches('.hp-focus,.hp-brush,.hp-band-hl')) continue
      el.style.removeProperty?.('transition')
      if (el.style.length === 0) el.removeAttribute('style')
    }
  }

  // move animates els from where their data was drawn in the old frame.
  function move(els, r, conf, anims) {
    for (const el of els) {
      const x = parseFloat(el.getAttribute('x') ?? el.getAttribute('cx') ?? el.getAttribute('x1'))
      const start = startTransform(el.localName, x, r.a, r.b)
      anims.push(el.animate?.([{ transform: start }, { transform: 'none' }], { duration: r.duration, easing: conf.easing }))
    }
  }

  // beforeSettle runs for each swap task right after htmx changed the DOM,
  // before the browser renders, and before any settle delay or the end of a
  // view transition: it slides or snaps the task's charts, redraws the
  // active chart's marks there, and notes the live tables to follow.
  function beforeSettle(task) {
    const rec = task?.[REC] || last
    const target = task?.target
    if (!rec || !target?.querySelectorAll) return
    const spec = typeof task.swapSpec === 'object' ? task.swapSpec : {}
    const t = now()
    const motion = !reduced?.matches && !(spec.transition ?? rec.main ?? rec.ctx)
    for (const svg of within(target, 'svg.hp')) {
      const old = rec.charts.get(svg.id)
      const prev = lastSwap.get(svg.id)
      lastSwap.set(svg.id, t)
      if (!old) continue
      const f = frameOf(attrOf(svg))
      const r = slideOf(old.f, f, { ...rec.conf, motion, scale: old.scale, interval: prev == null ? Infinity : t - prev })
      if (r.outcome === 'none' && f.anim && motion) {
        // Value changes: the stylesheet's CSS transitions, or the tween
        // companion where CSS cannot transition d.
        T('play', svg, old.pre, rec.conf)
      } else T('play', svg)
      if (r.outcome !== 'none') {
        // Snap and slide: no CSS transition plays for this swap. Set after
        // the morph, this also cancels one a style recalculation started.
        stopSlide(svg.id)
        for (const el of svg.querySelectorAll(MARKS)) el.style.transition = 'none'
        const anims = []
        slides.set(svg.id, anims)
        if (r.outcome === 'slide') {
          // A plain shift can move each series as one, unless the group
          // carries a clip path or mask (a transform moves those too);
          // g.hp-marks holds the clip path, so that clip stays put.
          const whole = Math.abs(r.a - 1) < 0.001
          const series = [...svg.querySelectorAll('g.hp-series')].flatMap((g) =>
            whole && !g.hasAttribute('clip-path') && !g.hasAttribute('mask') ? [g] : [...g.children],
          )
          move([...series, ...svg.querySelectorAll(ANCHORED)], r, rec.conf, anims)
          Promise.all(anims.map((a) => a?.finished)).then(
            () => unpin(svg, anims),
            () => {},
          )
        } else {
          requestAnimationFrame(() => requestAnimationFrame(() => unpin(svg, anims)))
        }
      }
      // The active chart's marks, which the swap reset or replaced, are
      // redrawn now, so no frame (and no view transition's snapshot) shows
      // the chart without them. A keyboard reader's crosshair or ring and
      // its brush range slide with their data; a pointer's crosshair and a
      // brush being dragged stay under the pointer.
      if (cur && cur.id === svg.id) {
        const moved = cur.svg !== svg
        cur.svg = svg
        render()
        B('swap')
        if (cur?.src !== 'k') continue
        if (moved) svg.focus({ preventScroll: true })
        if (r.outcome === 'slide') move(svg.querySelectorAll('.hp-crosshair,.hp-focus,.hp-brush'), r, rec.conf, slides.get(svg.id))
      }
    }
    // Follow: a table that was scrolled to its end in its nearest scroll box
    // inside the target (one that overflows, else one it still fits), is
    // scrolled to its end again after the swap, unless the swap says where
    // to scroll. A box the swap replaced counts as inside.
    if (rec.conf.follow && !spec.scroll && !spec.show) {
      for (const table of within(target, 'table.hp-table')) {
        const boxes = (rec.tables.get(table.id || table) || []).filter(([n]) => !n.isConnected || target.contains(n))
        const [box, , end] = boxes.find((b) => b[1]) || boxes[0] || []
        if (end) rec.follow.push([box, table])
      }
    }
  }

  function afterSwap(ctx) {
    vt = false
    refresh()
    const rec = responses.get(ctx)
    responses.delete(ctx)
    if (last === rec) last = null
    for (const [box, table] of rec?.follow || []) {
      const el = box.isConnected ? box : scroller(table.isConnected ? table : doc.getElementById(table.id))
      if (el) el.scrollTop = el.scrollHeight
    }
  }

  // ---- Autosize: htmx:plot:resize events and hp-w/hp-h request parameters. ----

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
  // tracking with hx-plot:autosize="both". A wrapper's height usually comes
  // from the chart inside it, so measuring it by default would ask for a
  // slightly taller chart on every request; only a wrapper whose height CSS
  // sets on its own (height, or aspect-ratio with overflow hidden) can opt in.
  const fixedHeight = (el) => attrOn(el, 'autosize') === 'both'

  // scan starts observing the autosize elements at or below root. With
  // htmx, each gets htmx state so that htmx_before_cleanup runs for it.
  function scan(root) {
    if (!ro || !root) return
    for (const el of within(root, SEL.autosize)) {
      if (!sizes.has(el) && !ignored(el)) {
        sizes.set(el, { ...boxSize(el), t: 0 })
        ro.observe(el)
        const prop = api?.htmxProp?.(el)
        if (prop) prop.plot = true
      }
    }
  }

  function unobserve(el) {
    if (!sizes.has(el)) return
    clearTimeout(sizes.get(el).t)
    ro.unobserve(el)
    sizes.delete(el)
  }

  function resized(el) {
    const s = sizes.get(el)
    if (!s) return
    clearTimeout(s.t)
    if (!el.isConnected) return unobserve(el)
    s.t = setTimeout(() => {
      if (!el.isConnected) return
      const { w, h } = boxSize(el)
      const tall = fixedHeight(el) && h > 0 && Math.abs(h - s.h) >= 8
      if (!w || (Math.abs(w - s.w) < 8 && !tall)) return
      s.w = w
      s.h = h
      emit(el, 'htmx:plot:resize', { width: w, height: h }, false)
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

  // params adds the extension's parameters to a request (spec/spec.md
  // §6.7): hp-w (and, for hx-plot:autosize="both", hp-h) when the request
  // targets an autosize element or something inside one, and the gesture
  // parameters when a select or brush triggered it. It never replaces a
  // value the request already has in its body or its URL. ctx.request.body
  // is a FormData here; for GET and DELETE htmx then copies it into the
  // URL, replacing any URL parameter of the same name, so a value the author
  // wrote into hx-get or hx-sse:connect must be checked here or it would be
  // lost.
  function params(ctx) {
    const body = ctx?.request?.body
    if (typeof body?.set !== 'function') return
    const query = queryOf(ctx.request.action)
    const put = (k, v) => v && !body.has(k) && !query?.has(k) && body.set(k, v)
    const box = ctx.target?.closest?.(SEL.autosize)
    if (box) {
      const { w, h } = boxSize(box)
      put('hp-w', w)
      if (fixedHeight(box)) put('hp-h', h)
    }
    for (const [k, v] of gestureParams(ctx.sourceEvent, conf0.metaCharacter)) put(k, v)
  }

  // I is the internal API of the companion scripts. It is not public.
  const I = { attrs, own, emit, say, render, settings, tips: tipsOf, chart: chartOf, attr: attrOn, cur: () => cur }
  I.frame = (svg) => frameOf(attrOf(svg))
  I.vb = (svg, x, y) => {
    const m = svg.getScreenCTM()
    return m && new DOMPoint(x, y).matrixTransform(m.inverse())
  }

  // start attaches everything; with htmx it runs from init, which htmx
  // calls only when it accepts the extension.
  function start(a) {
    api = a
    H._ = I
    on('pointermove', onPointer, passive)
    on(
      'pointerdown',
      (e) => {
        // The active point when this press began: a tap selects only it.
        const svg = chartOf(e.target)
        down = { svg, i: cur?.src === 'p' && cur.svg === svg ? cur.i : -1, touch: e.pointerType === 'touch', series: seriesOf(e.target) }
        onPointer(e)
      },
      passive,
    )
    on('pointerout', (e) => {
      // Touch pointers leave after every tap; their tooltip stays until the
      // next tap outside the chart. During a view transition the browser
      // hit-tests its snapshot, so the pointer seems to leave the page.
      if (cur?.src !== 'p' || e.pointerType === 'touch' || vt) return
      // The chart and its tooltip count as one (see toward).
      const inside = (n) => cur.svg.contains(n) || tip?.contains(n)
      if (inside(e.target) && !inside(e.relatedTarget) && !toward(e)) {
        pending = null
        hide()
      }
    })
    on('pointercancel', () => cur?.src === 'p' && hide())
    on('click', onClick)
    on('keydown', onKey)
    // A chart that takes focus gets the status region next to it, before
    // its first step is announced (see liveEl).
    on('focusin', (e) => {
      const svg = chartOf(e.target)
      if (svg) liveEl(svg)
    })
    on('focusout', (e) => {
      const c = cur
      if (c?.src !== 'k' || e.target !== c.svg) return
      // A swap that removes the focused chart blurs it while it still
      // reports isConnected, and htmx then focuses its replacement; so the
      // client decides a frame later. A chart that a swap replaced is found
      // again by id (refresh), and keeps its point; one that really lost
      // focus hides its tooltip.
      requestAnimationFrame(() => {
        if (cur !== c) return
        if (!c.svg.isConnected) refresh()
        else if (doc.activeElement !== c.svg) hide()
      })
    })
    // Keyboard tooltips follow their point when the page scrolls or resizes;
    // pointer tooltips close until the pointer moves again. Scrolling an
    // element that does not hold the chart (a followed live table, a log)
    // leaves them alone.
    const reflow = (e) => cur && (cur.src === 'k' ? render() : (!e || e.target === doc || e.target.contains?.(cur.svg)) && hide())
    G.addEventListener('scroll', reflow, { capture: true, passive: true })
    G.addEventListener('resize', () => reflow(), passive)
    const ready = () => {
      liveEl()
      scan(doc.body)
    }
    if (doc.readyState === 'loading') on('DOMContentLoaded', ready, { once: true })
    else ready()
    // Without htmx, nothing tells the client about content added later
    // (page script, another library), so it watches for autosize elements.
    if (!api && G.MutationObserver && ro) {
      new MutationObserver((ms) => {
        for (const m of ms) for (const n of m.addedNodes) if (n.nodeType === 1) scan(n)
      }).observe(doc.documentElement, { childList: true, subtree: true })
    }
    // The companions that loaded while the mode was undecided start now.
    for (const fn of H.later?.splice(0) || []) fn()
  }

  // mode decides between htmx mode and standalone mode, once.
  function mode() {
    htmx = G.htmx?.registerExtension ? G.htmx : null
    conf0 = htmx?.config || {}
    spellings()
    if (!htmx) {
      // Most likely htmx loads later than this script (a module, async
      // script or bundle): then its requests would carry no hp-*
      // parameters and nothing would slide. Say so.
      if (doc.querySelector('[hx-get],[hx-post],[hx-trigger],[data-hx-get],[data-hx-post],[data-hx-trigger]')) {
        console.warn('hx-plot: no htmx, so no hp-* parameters or slides. Load htmx first (module and async scripts run late).')
      }
      return start(null)
    }
    htmx.registerExtension('plot', ext)
  }

  const ext = {
    init: start,
    // A view transition runs between these two (see the pointerout listener).
    htmx_before_viewTransition() {
      vt = true
    },
    htmx_after_viewTransition() {
      vt = false
    },
    htmx_config_request: (elt, d) => params(d?.ctx),
    htmx_before_swap: (elt, d) => beforeSwap(d?.ctx, d?.tasks),
    // Keep open: the server's frame does not know whether the reader
    // expanded a table's <details>, so a morph never changes it.
    htmx_before_morph_attr(elt, d) {
      if (d?.attrName === 'open' && morphing.keepOpen && elt?.localName === 'details' && elt.querySelector('table.hp-table')) return false
    },
    htmx_before_settle: (elt, d) => beforeSettle(d?.task),
    // SSE messages run the same swap hooks, so no hx-sse hook is needed.
    htmx_after_swap: (elt, d) => afterSwap(d?.ctx),
    htmx_after_process: (elt) => scan(elt),
    htmx_before_cleanup: (elt) => unobserve(elt),
    // htmx's hx-history-cache extension saves a copy of the page that Back
    // swaps in again. What the client owns is left out of it: the tooltip,
    // the status region, the ring, the brush and the inline styles on chart
    // elements (a slide's or snap's transition: none, a tween's, the band
    // highlight's), which a strict style-src would refuse in the copy and
    // which would leave restored marks without their transitions. The copy
    // is taken synchronously between the two hooks, so the page never
    // renders without them.
    htmx_history_cache_before_save() {
      unsave()
      saved = [...doc.querySelectorAll('div.hp-tip,div.hp-live,.hp-focus,.hp-brush')].map((el) => [el, el.parentNode, el.nextSibling])
      for (const [el] of saved) el.remove()
      // (Put back through the CSSOM, which a strict style-src allows.)
      for (const el of doc.querySelectorAll('svg.hp[style],svg.hp [style]')) {
        saved.push([el, el.style.cssText])
        el.removeAttribute('style')
      }
    },
    htmx_history_cache_after_save: () => unsave(),
  }

  // unsave puts back what htmx_history_cache_before_save left out.
  function unsave() {
    for (const [el, p, next] of saved || []) {
      if (typeof p !== 'string') {
        if (p.isConnected) p.insertBefore(el, next?.parentNode === p ? next : null)
      } else if (p) el.style.cssText = p
    }
    saved = null
  }

  // Loaded before htmx, in a page that is still loading (htmx in a module or
  // deferred script runs after this classic script), the client waits until
  // the document is parsed to decide.
  if (!G.htmx && doc.readyState === 'loading') on('DOMContentLoaded', mode, { once: true })
  else mode()
})()
