/*! hx-plot-tween 0.2.0 | MIT */
// hx-plot-tween: JavaScript path tweens for hx-plot.js (spec/spec.md
// §6.8.4). When a live chart's values change but its x window does not, the
// stylesheet's CSS transition on d animates its lines, areas and bars, but
// only in browsers where CSS can set d: Safari parses the property and
// ignores it, so there the new frame snaps. In such a browser this script
// tweens each changed path's d in JavaScript instead, for paths whose old
// and new d have the same commands (others snap), with the settings'
// duration and easing. Where CSS d works it does nothing.
//
// It is an optional companion of hx-plot.js and does nothing on its own.
// Load it after hx-plot.js:
//
//	<script src="/hx-plot.js"></script>
//	<script src="/hx-plot-tween.js"></script>
//
// Test hook: set globalThis.hyperplot.tween.force = true to tween in every
// browser.
(function tween() {
  'use strict'
  const G = globalThis
  // Only alongside a started hx-plot.js (htmx may have refused it), once.
  // (An own property only: an element named "hyperplot" is not the script.)
  const H = Object.prototype.hasOwnProperty.call(G, 'hyperplot') ? G.hyperplot : null
  if (typeof document === 'undefined' || !H || H.tween) return
  // (hx-plot.js runs this script again when it starts, should it decide its
  // mode only once the page is parsed.)
  if (!H._) return void (H.later ||= []).push(tween)
  const doc = document
  const NUM = /-?(?:\d*\.)?\d+(?:e[-+]?\d+)?/gi
  const runs = new WeakMap() // chart → its running tween: {a (the clock), jobs, k (its last progress), end}
  const owner = new WeakMap() // path → the tween that set its transition
  let cssD // whether CSS can set a path's d, tested once

  // css reports whether CSS can set a path's d: a path whose d attribute
  // is a point gets a CSS d with width, and its box tells which won.
  function css() {
    if (cssD == null) {
      const svg = doc.createElementNS('http://www.w3.org/2000/svg', 'svg')
      const p = doc.createElementNS(svg.namespaceURI, 'path')
      p.setAttribute('d', 'M0 0')
      p.style.setProperty('d', 'path("M0 0H9")')
      svg.style.position = 'absolute'
      svg.append(p)
      doc.body.append(svg)
      try {
        cssD = p.getBBox().width > 0
      } catch {
        cssD = false
      }
      svg.remove()
    }
    return cssD
  }

  // at interpolates a job [a, b, parts, d1] at progress k (1: d1 itself).
  const at = ([a, b, parts, d1], k) => (k < 1 ? parts.map((s, i) => s + (i < a.length ? +(a[i] + (b[i] - a[i]) * k).toFixed(2) : '')).join('') : d1)

  const T = (H.tween = {
    force: false,
    // capture records the d of every series path before a swap, or returns
    // nothing where CSS tweens d itself. It changes nothing: htmx or the
    // page can still cancel the swap, and then a running tween goes on.
    capture(I, svg) {
      if (!T.force && css()) return
      const pre = new Map()
      for (const p of svg.querySelectorAll('g.hp-series path')) pre.set(p, p.getAttribute('d'))
      return pre
    },
    // play ends the chart's running tween and tweens the paths whose d the
    // morph changed, from their captured d (or from where the running tween
    // has got to), right after the morph and before the browser renders.
    // Without pre (a slide, a snap, a static frame) it only ends the
    // running tween.
    play(I, svg, pre, conf) {
      const old = runs.get(svg)
      runs.delete(svg)
      old?.a.cancel()
      old?.end()
      const paths = []
      const run = {
        jobs: new Map(), // path → [a, b, parts, d1]
        // end gives the paths back to the stylesheet, except those a newer
        // tween has taken over.
        end() {
          for (const p of paths) {
            if (owner.get(p) !== run) continue
            owner.delete(p)
            p.style.removeProperty('transition')
            if (!p.style.length) p.removeAttribute('style')
          }
        },
      }
      for (const [p, d] of pre || []) {
        // A path the running tween holds starts where it last drew it.
        const j = old?.jobs.get(p)
        const d0 = j ? at(j, old.k) : d
        const d1 = p.getAttribute('d')
        if (d0 === d1 || !svg.contains(p)) continue
        // No CSS transition: the path snaps, or this script tweens it.
        p.style.transition = 'none'
        owner.set(p, run)
        paths.push(p)
        const a = d0?.match(NUM)
        const b = d1?.match(NUM)
        if (a && b && a.length === b.length && d0.replace(NUM, '') === d1.replace(NUM, '')) {
          run.jobs.set(p, [a.map(Number), b.map(Number), d1.split(NUM), d1])
        }
      }
      if (!paths.length) return
      // The clock is an empty animation, so the easing is the browser's own.
      const clock = (run.a = svg.animate(null, { duration: conf.duration, easing: conf.easing }))
      runs.set(svg, run)
      const tick = () => {
        if (runs.get(svg) !== run) return
        const done = clock.playState === 'finished'
        const k = (run.k = done ? 1 : clock.effect.getComputedTiming().progress || 0)
        for (const [p, j] of run.jobs) p.setAttribute('d', at(j, k))
        if (!done) return requestAnimationFrame(tick)
        runs.delete(svg)
        // Two frames later, once the browser has drawn the final d with no
        // transition, so that giving the transition back starts none.
        requestAnimationFrame(() => requestAnimationFrame(run.end))
      }
      tick()
    },
  })
})()
