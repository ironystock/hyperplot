// A minimal fake browser for testing the DOM and htmx wiring in hyperplot.js
// with node --test and no dependencies. It implements only what the script
// touches: elements with attributes, classes and a tree, the small selector
// subset the script uses, event listeners on document and window, getScreenCTM
// and DOMPoint, ResizeObserver, timers, animation frames, and an htmx stub
// that records registered extensions.
//
// Each load() runs the real hyperplot.js source in a fresh vm context, so
// tests never share state.
import { readFileSync } from 'node:fs'
import vm from 'node:vm'

const SRC = readFileSync(new URL('./hyperplot.js', import.meta.url), 'utf8')

// Compound selectors only: tag, .class, #id and [attr], comma separated.
const PART = /^([a-z][\w-]*)?((?:[.#][\w-]+|\[[\w-]+\])*)$/i
const TOKEN = /([.#])([\w-]+)|\[([\w-]+)\]/g

function compile(sel) {
  return sel.split(',').map((s) => {
    const m = PART.exec(s.trim())
    if (!m) throw new Error(`fakedom: unsupported selector ${JSON.stringify(s)}`)
    const tests = []
    if (m[1]) tests.push((el) => el.localName === m[1].toLowerCase())
    for (const [, kind, name, attr] of m[2].matchAll(TOKEN)) {
      if (attr) tests.push((el) => el.hasAttribute(attr))
      else if (kind === '#') tests.push((el) => el.id === name)
      else tests.push((el) => el.className.split(/\s+/).includes(name))
    }
    return (el) => tests.every((t) => t(el))
  })
}

/** Matrix is a 2D affine matrix, like DOMMatrix: x' = a·x + c·y + e. */
export class Matrix {
  constructor(a = 1, b = 0, c = 0, d = 1, e = 0, f = 0) {
    Object.assign(this, { a, b, c, d, e, f })
  }

  inverse() {
    const { a, b, c, d, e, f } = this
    const det = a * d - b * c
    return new Matrix(d / det, -b / det, -c / det, a / det, (c * f - d * e) / det, (b * e - a * f) / det)
  }
}

class DOMPoint {
  constructor(x = 0, y = 0) {
    this.x = x
    this.y = y
  }

  matrixTransform(m) {
    return new DOMPoint(m.a * this.x + m.c * this.y + m.e, m.b * this.x + m.d * this.y + m.f)
  }
}

class El {
  constructor(doc, tag) {
    this.ownerDocument = doc
    this.localName = tag.toLowerCase()
    this.attrs = new Map()
    this.children = []
    this.parentNode = null
    this.style = {}
    this.text = ''
    this.listeners = {}
    this.mutations = 0 // child or text changes, like a MutationObserver count
    this.clientWidth = 0
    this.clientHeight = 0
    this.offsetWidth = 120
    this.offsetHeight = 40
    this.padding = [0, 0, 0, 0] // top, right, bottom, left in px
    this.ctm = null
  }

  get className() {
    return this.getAttribute('class') ?? ''
  }

  set className(v) {
    this.setAttribute('class', v)
  }

  get id() {
    return this.getAttribute('id') ?? ''
  }

  set id(v) {
    this.setAttribute('id', v)
  }

  setAttribute(k, v) {
    this.attrs.set(k, String(v))
  }

  getAttribute(k) {
    return this.attrs.has(k) ? this.attrs.get(k) : null
  }

  hasAttribute(k) {
    return this.attrs.has(k)
  }

  get textContent() {
    return this.children.length ? this.children.map((c) => c.textContent).join('') : this.text
  }

  set textContent(v) {
    this.replaceChildren()
    this.text = String(v)
  }

  append(...kids) {
    for (const k of kids) {
      k.remove()
      k.parentNode = this
      this.children.push(k)
    }
    this.mutations++
  }

  replaceChildren(...kids) {
    for (const k of this.children) k.parentNode = null
    this.children = []
    this.text = ''
    this.append(...kids)
  }

  remove() {
    const p = this.parentNode
    if (!p) return
    p.children.splice(p.children.indexOf(this), 1)
    p.mutations++
    this.parentNode = null
  }

  get isConnected() {
    let n = this
    while (n.parentNode) n = n.parentNode
    return n === this.ownerDocument.documentElement
  }

  matches(sel) {
    return compile(sel).some((t) => t(this))
  }

  closest(sel) {
    const tests = compile(sel)
    for (let n = this; n; n = n.parentNode) if (tests.some((t) => t(n))) return n
    return null
  }

  contains(other) {
    for (let n = other; n; n = n.parentNode) if (n === this) return true
    return false
  }

  *descendants() {
    for (const c of this.children) {
      yield c
      yield* c.descendants()
    }
  }

  querySelectorAll(sel) {
    const tests = compile(sel)
    return [...this.descendants()].filter((el) => tests.some((t) => t(el)))
  }

  querySelector(sel) {
    return this.querySelectorAll(sel)[0] ?? null
  }

  getScreenCTM() {
    return this.isConnected ? this.ctm : null
  }

  focus() {
    this.ownerDocument.activeElement = this
  }

  addEventListener(type, fn) {
    ;(this.listeners[type] ??= []).push(fn)
  }

  dispatchEvent(e) {
    for (const fn of this.listeners[e.type] ?? []) fn(e)
    return true
  }
}

class Doc {
  constructor() {
    this.documentElement = new El(this, 'html')
    this.documentElement.clientWidth = 1024
    this.documentElement.clientHeight = 768
    this.body = new El(this, 'body')
    this.documentElement.append(this.body)
    this.readyState = 'complete'
    this.activeElement = this.body
    this.listeners = {}
    this.baseURI = 'https://example.test/dashboard'
  }

  createElement(tag) {
    return new El(this, tag)
  }

  createElementNS(ns, tag) {
    const el = new El(this, tag)
    el.namespaceURI = ns
    return el
  }

  getElementById(id) {
    return [...this.documentElement.descendants()].find((el) => el.id === id) ?? null
  }

  querySelectorAll(sel) {
    return this.documentElement.querySelectorAll(sel)
  }

  querySelector(sel) {
    return this.documentElement.querySelector(sel)
  }

  addEventListener(type, fn) {
    ;(this.listeners[type] ??= []).push(fn)
  }
}

/**
 * load runs hyperplot.js in a fresh fake browser and returns its environment.
 * Options: htmx (default true) installs the htmx stub; reducedMotion sets the
 * prefers-reduced-motion media query.
 */
export function load({ htmx = true, reducedMotion = false } = {}) {
  const doc = new Doc()
  let timers = []
  let frames = []
  let nextId = 1
  const exts = new Map()
  const observed = new Set()
  const win = {}

  class ResizeObserver {
    constructor(cb) {
      env.roCallback = cb
    }

    observe(el) {
      observed.add(el)
    }

    unobserve(el) {
      observed.delete(el)
    }
  }

  class CustomEvent {
    constructor(type, init) {
      this.type = type
      this.detail = init?.detail ?? null
    }
  }

  const sandbox = {
    document: doc,
    DOMPoint,
    ResizeObserver,
    CustomEvent,
    FormData,
    URL,
    console,
    getComputedStyle: (el) => {
      const [t, r, b, l] = el.padding
      return { paddingTop: t + 'px', paddingRight: r + 'px', paddingBottom: b + 'px', paddingLeft: l + 'px' }
    },
    matchMedia: () => ({
      get matches() {
        return env.reducedMotion
      },
    }),
    requestAnimationFrame: (fn) => {
      frames.push(fn)
      return frames.length
    },
    setTimeout: (fn, ms) => {
      const id = nextId++
      timers.push({ id, fn, ms })
      return id
    },
    clearTimeout: (id) => {
      timers = timers.filter((t) => t.id !== id)
    },
    addEventListener: (type, fn) => {
      ;(win[type] ??= []).push(fn)
    },
  }
  if (htmx) {
    sandbox.htmx = {
      // Like htmx 4, a second registration under the same name is ignored.
      registerExtension(name, ext) {
        if (!exts.has(name)) exts.set(name, ext)
      },
    }
  }

  const ctx = vm.createContext(sandbox)
  const run = () => vm.runInContext(SRC, ctx, { filename: 'hyperplot.js' })

  const env = {
    doc,
    window: ctx,
    exts,
    observed,
    reducedMotion,
    roCallback: null,
    run,

    /** ext returns the registered hyperplot extension. */
    get ext() {
      return exts.get('hyperplot')
    },

    /** live returns the role="status" region. */
    get live() {
      return doc.querySelector('[role]')
    },

    /** tip returns the visual tooltip, or null before it is first shown. */
    get tip() {
      return doc.querySelector('div.hp-tip')
    },

    /** el creates an element with attributes, appended to parent if given. */
    el(tag, attrs = {}, parent = null) {
      const e = doc.createElement(tag)
      for (const [k, v] of Object.entries(attrs)) e.setAttribute(k, v)
      if (parent) parent.append(e)
      return e
    },

    /**
     * chart builds an svg.hp with a band highlight and, as the server writes
     * them, a g.hp-hover holding a crosshair and a metadata.hp-tips payload.
     * It is mapped to the screen by ctm (by default a translation by
     * (100, 50)) and appended to parent (default body).
     */
    chart({ id = 'c1', tips, parent = doc.body, ctm = new Matrix(1, 0, 0, 1, 100, 50) } = {}) {
      const svg = env.el('svg', { id, class: 'hp', tabindex: '0' }, parent)
      svg.ctm = ctm
      env.el('rect', { class: 'hp-band-hl', visibility: 'hidden' }, svg)
      const hover = env.el('g', { class: 'hp-hover', 'aria-hidden': 'true' }, svg)
      env.el('line', { class: 'hp-crosshair', visibility: 'hidden' }, hover)
      env.el('metadata', { class: 'hp-tips' }, hover).textContent = JSON.stringify(tips)
      return svg
    },

    /** setTips replaces a chart's tooltip payload, as a morph swap would. */
    setTips(svg, tips) {
      svg.querySelector('metadata.hp-tips').textContent = JSON.stringify(tips)
    },

    /** fire dispatches a document event to the script's listeners. */
    fire(type, init = {}) {
      const e = {
        type,
        defaultPrevented: false,
        preventDefault() {
          this.defaultPrevented = true
        },
        ...init,
      }
      for (const fn of doc.listeners[type] ?? []) fn(e)
      return e
    },

    /** key sends a keydown to target (default: the focused element). */
    key(key, target = doc.activeElement) {
      return env.fire('keydown', { key, target })
    },

    /** frame runs the pending animation frame callbacks. */
    frame() {
      const fs = frames
      frames = []
      for (const fn of fs) fn(0)
    },

    /** timers runs the pending timeouts. */
    timers() {
      const ts = timers
      timers = []
      for (const t of ts) t.fn()
    },

    /** pendingTimers returns how many timeouts are waiting. */
    get pendingTimers() {
      return timers.length
    },

    /** resize sets an observed element's size and notifies the observer. */
    resize(el, w, h = el.clientHeight) {
      el.clientWidth = w
      el.clientHeight = h
      if (observed.has(el)) env.roCallback([{ target: el }])
    },
  }

  run()
  return env
}
