// A minimal fake browser for testing the DOM and htmx wiring in hx-plot.js
// and its companions with node --test and no dependencies. It implements
// only what the scripts touch: elements with attributes, classes and a tree,
// the small selector subset the scripts use, events that bubble to
// document, listeners on document and window, getScreenCTM and DOMPoint,
// pointer capture, ResizeObserver, timers, animation frames, a clock,
// inline styles, Element.animate (recorded, finished by the test), scroll
// boxes, an htmx stub that honours htmx.config.extensions and dispatches
// events like htmx 4's api.triggerHtmxEvent, and an XML parser for the
// conformance fixtures.
//
// Each load() runs the real script sources in a fresh vm context, so tests
// never share state.
import { readFileSync } from 'node:fs'
import vm from 'node:vm'

const source = (name) => readFileSync(new URL(`./${name}`, import.meta.url), 'utf8')
const SRC = { core: source('hx-plot.js'), brush: source('hx-plot-brush.js'), tween: source('hx-plot-tween.js') }
const FILES = { core: 'hx-plot.js', brush: 'hx-plot-brush.js', tween: 'hx-plot-tween.js' }

// Compound selectors (tag or *, .class, #id, [attr] and [attr="value"],
// with backslash escapes in attribute names), joined by the child
// combinator > or the descendant combinator (a space), comma separated.
const PART = /^(\*|[a-z][\w-]*)?((?:[.#][\w-]+|\[(?:\\.|[\w-])+(?:="[^"]*")?\])*)$/i
const TOKEN = /([.#])([\w-]+)|\[((?:\\.|[\w-])+)(?:="([^"]*)")?\]/g

function compound(s) {
  const m = PART.exec(s.trim())
  if (!s.trim() || !m) throw new Error(`fakedom: unsupported selector ${JSON.stringify(s)}`)
  const tests = []
  if (m[1] && m[1] !== '*') tests.push((el) => el.localName === m[1].toLowerCase())
  for (const [, kind, name, attr, value] of m[2].matchAll(TOKEN)) {
    if (attr) {
      const a = attr.replace(/\\(.)/g, '$1')
      tests.push(value == null ? (el) => el.hasAttribute(a) : (el) => el.getAttribute(a) === value)
    } else if (kind === '#') tests.push((el) => el.id === name)
    else tests.push((el) => el.className.split(/\s+/).includes(name))
  }
  return (el) => tests.every((t) => t(el))
}

// complex compiles one selector of compounds and combinators, matched from
// the right like a browser does.
function complex(s) {
  const steps = s
    .trim()
    .replace(/\s*>\s*/g, '>')
    .split(/(>|\s+)/)
    .filter((x) => x !== '')
  const parts = []
  for (let i = 0; i < steps.length; i += 2) parts.push([i ? (steps[i - 1] === '>' ? '>' : ' ') : '', compound(steps[i])])
  const match = (el, k) => {
    if (!el || !parts[k][1](el)) return false
    if (k === 0) return true
    if (parts[k][0] === '>') return match(el.parentElement, k - 1)
    for (let n = el.parentElement; n; n = n.parentElement) if (match(n, k - 1)) return true
    return false
  }
  return (el) => match(el, parts.length - 1)
}

const compile = (sel) => sel.split(',').map(complex)

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

const camel = (name) => name.replace(/-(\w)/g, (_, c) => c.toUpperCase())

/** Style is an element's inline style: plain properties plus the CSSOM bits the scripts use. */
class Style {
  removeProperty(name) {
    delete this[camel(name)]
  }

  setProperty(name, value) {
    this[camel(name)] = value
  }

  set cssText(text) {
    for (const k of Object.keys(this)) delete this[k]
    for (const decl of String(text).split(';')) {
      const i = decl.indexOf(':')
      if (i > 0) this[camel(decl.slice(0, i).trim())] = decl.slice(i + 1).trim()
    }
  }

  get length() {
    return Object.values(this).filter((v) => v !== '' && v != null).length
  }
}

/** Animation records one Element.animate call; the test finishes it. */
class Animation {
  constructor(el, keyframes, options) {
    Object.assign(this, { el, keyframes, options, playState: 'running', progress: 0 })
    this.finished = new Promise((resolve, reject) => {
      this.resolve = resolve
      this.reject = reject
    })
    this.finished.catch(() => {})
    // The tween companion reads its eased progress here; tests set progress.
    this.effect = { getComputedTiming: () => ({ progress: this.playState === 'running' ? this.progress : null }) }
  }

  cancel() {
    if (this.playState !== 'running') return
    this.playState = 'idle'
    this.reject(new Error('AbortError'))
  }

  finish() {
    if (this.playState !== 'running') return
    this.playState = 'finished'
    this.resolve(this)
  }
}

class El {
  constructor(doc, tag) {
    this.ownerDocument = doc
    this.localName = tag.toLowerCase()
    this.attrs = new Map()
    this.children = []
    this.parentNode = null
    this.style = new Style()
    this.animations = []
    this.text = ''
    this.listeners = {}
    this.mutations = 0 // child or text changes, like a MutationObserver count
    this.clientWidth = 0
    this.clientHeight = 0
    this.offsetWidth = 120
    this.offsetHeight = 40
    this.padding = [0, 0, 0, 0] // top, right, bottom, left in px
    this.overflowY = 'visible' // the computed overflow-y
    this.scrollTop = 0
    this.scrollHeight = 0
    this.ctm = null
  }

  get parentElement() {
    return this.parentNode instanceof El ? this.parentNode : null
  }

  get classList() {
    return { contains: (c) => this.className.split(/\s+/).includes(c) }
  }

  removeAttribute(k) {
    this.attrs.delete(k)
  }

  animate(keyframes, options) {
    const a = new Animation(this, keyframes, options)
    this.animations.push(a)
    this.ownerDocument.animations.push(a)
    return a
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

  insertBefore(kid, ref) {
    if (!ref) return this.append(kid)
    kid.remove()
    kid.parentNode = this
    this.children.splice(this.children.indexOf(ref), 0, kid)
    this.mutations++
    return kid
  }

  get nextSibling() {
    const sibs = this.parentNode?.children
    return sibs ? (sibs[sibs.indexOf(this) + 1] ?? null) : null
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
    while (n.parentNode instanceof El) n = n.parentNode
    return n === this.ownerDocument.documentElement
  }

  matches(sel) {
    return compile(sel).some((t) => t(this))
  }

  closest(sel) {
    const tests = compile(sel)
    for (let n = this; n instanceof El; n = n.parentNode) if (tests.some((t) => t(n))) return n
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

  getBBox() {
    // No layout here: CSS never sets a path's d, as in Safari.
    return { x: 0, y: 0, width: 0, height: 0 }
  }

  focus() {
    this.ownerDocument.activeElement = this
  }

  setPointerCapture(id) {
    this.ownerDocument.captures.set(id, this)
  }

  releasePointerCapture(id) {
    if (this.ownerDocument.captures.get(id) === this) this.ownerDocument.captures.delete(id)
  }

  hasPointerCapture(id) {
    return this.ownerDocument.captures.get(id) === this
  }

  addEventListener(type, fn) {
    ;(this.listeners[type] ??= []).push(fn)
  }

  // dispatchEvent runs the listeners on this element and, for a bubbling
  // event, on its ancestors and then on document.
  dispatchEvent(e) {
    e.target ??= this
    for (let n = this; n; n = e.bubbles ? n.parentNode : null) {
      for (const fn of n.listeners[e.type] ?? []) fn(e)
    }
    return !e.defaultPrevented
  }
}

class Doc {
  constructor() {
    this.documentElement = new El(this, 'html')
    this.documentElement.clientWidth = 1024
    this.documentElement.clientHeight = 768
    this.body = new El(this, 'body')
    this.documentElement.append(this.body)
    this.documentElement.parentNode = this
    this.readyState = 'complete'
    this.activeElement = this.body
    this.listeners = {}
    this.animations = [] // every Element.animate call, in order
    this.captures = new Map() // pointer id → the element that captured it
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

const ENTITY = { amp: '&', lt: '<', gt: '>', quot: '"', apos: "'" }
const decode = (s) =>
  s.replace(/&(?:#x([\da-f]+)|#(\d+)|(\w+));/gi, (m, hex, dec, name) =>
    hex ? String.fromCodePoint(parseInt(hex, 16)) : dec ? String.fromCodePoint(+dec) : (ENTITY[name] ?? m),
  )

/**
 * parseXML builds the elements of a well-formed XML document (such as a
 * reference chart from testdata/conformance) in doc, and returns the root.
 * Attribute values are normalized as XML requires (raw TAB, LF and CR read
 * as spaces before character references are decoded). Text between child
 * elements is dropped; an element without child elements keeps its text.
 */
export function parseXML(doc, xml) {
  const top = new El(doc, '#root')
  const stack = [top]
  const TAG = /<!--[\s\S]*?-->|<\?[\s\S]*?\?>|<\/([\w:.-]+)\s*>|<([\w:.-]+)((?:\s+[\w:.-]+\s*=\s*"[^"]*")*)\s*(\/?)>|([^<]+)/g
  for (const [, close, open, attrs, empty, text] of xml.matchAll(TAG)) {
    const parent = stack[stack.length - 1]
    if (close) stack.pop()
    else if (open) {
      const el = doc.createElementNS('http://www.w3.org/2000/svg', open)
      for (const [, k, v] of attrs.matchAll(/([\w:.-]+)\s*=\s*"([^"]*)"/g)) el.setAttribute(k, decode(v.replace(/[\t\n\r]/g, ' ')))
      parent.text = ''
      parent.append(el)
      if (!empty) stack.push(el)
    } else if (text != null && !parent.children.length) parent.text += decode(text)
  }
  const root = top.children[0]
  root.remove()
  return root
}

/**
 * load runs hx-plot.js in a fresh fake browser and returns its environment.
 * Options: htmx (default true) installs the htmx stub, with config as its
 * htmx.config (config.extensions is the allow-list, config.metaCharacter
 * renames events); reducedMotion sets the prefers-reduced-motion media
 * query; companions lists the companion scripts to run after it ("brush",
 * "tween"); before(env), if given, runs before the scripts (to build a page
 * that exists when they start).
 */
export function load({ htmx = true, reducedMotion = false, config = {}, companions = [], before } = {}) {
  const doc = new Doc()
  let timers = []
  let frames = []
  let nextId = 1
  const exts = new Map()
  const observed = new Set()
  const win = {}
  const events = [] // every event an extension dispatched through htmx

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
    constructor(type, init = {}) {
      this.type = type
      this.detail = init.detail ?? null
      this.bubbles = !!init.bubbles
      this.cancelable = !!init.cancelable
      this.defaultPrevented = false
    }

    preventDefault() {
      if (this.cancelable) this.defaultPrevented = true
    }
  }

  // api is the part of htmx 4's extension API that the client uses.
  const api = {
    triggerHtmxEvent(elt, name, detail = {}, bubbles = true) {
      const type = config.metaCharacter ? name.replace(/:/g, config.metaCharacter) : name
      events.push({ elt, type, detail, bubbles })
      return elt.dispatchEvent(new CustomEvent(type, { detail, bubbles, cancelable: true }))
    },
    htmxProp(el) {
      return (el._htmx ??= {})
    },
  }

  const sandbox = {
    document: doc,
    DOMPoint,
    ResizeObserver,
    CustomEvent,
    CSS: {
      escape: (s) => String(s).replace(/[^\w-]/g, '\\$&'),
      // Only what the client asks: is a value a CSS easing function?
      supports: (prop, v) =>
        prop === 'transition-timing-function' && /^(ease(-in|-out|-in-out)?|linear|step-(start|end)|(cubic-bezier|steps|linear)\([^()]*\))$/.test(String(v).trim()),
    },
    FormData,
    URL,
    console,
    getComputedStyle: (el) => {
      const [t, r, b, l] = el.padding
      return { paddingTop: t + 'px', paddingRight: r + 'px', paddingBottom: b + 'px', paddingLeft: l + 'px', overflowY: el.overflowY }
    },
    performance: {
      now: () => env.clock,
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
      config,
      // Like htmx 4.0.2: false when the allow-list excludes the name or it
      // is already registered; otherwise init(api) runs and the hooks are
      // kept, and the call returns undefined.
      registerExtension(name, ext) {
        if (config.extensions && !config.extensions.split(/,\s*/).includes(name)) return false
        if (exts.has(name)) return false
        ext.init?.(api)
        exts.set(name, ext)
      },
    }
  }

  const ctx = vm.createContext(sandbox)
  const run = (name = 'core') => vm.runInContext(SRC[name], ctx, { filename: FILES[name] })

  const env = {
    doc,
    window: ctx,
    exts,
    observed,
    events,
    reducedMotion,
    roCallback: null,
    clock: 1000, // what performance.now() returns, in ms
    run,

    /** ext returns the registered plot extension. */
    get ext() {
      return exts.get('plot')
    },

    /** internal returns the core's internal API for companions (hyperplot._). */
    get internal() {
      return ctx.hyperplot?._
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

    /** parse builds the elements of an XML document, appended to parent if given. */
    parse(xml, parent = null) {
      const root = parseXML(doc, xml)
      if (parent) parent.append(root)
      return root
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

    /**
     * fire dispatches an event to the document's listeners, as if it had
     * bubbled up from init.target (a browser event the scripts delegate).
     */
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

    /** fireWindow dispatches an event to the window's listeners (scroll, resize). */
    fireWindow(type, init = {}) {
      const e = { type, target: ctx, ...init }
      for (const fn of win[type] ?? []) fn(e)
      return e
    },

    /** key sends a keydown to target (default: the focused element); "Shift+End" holds Shift. */
    key(key, target = doc.activeElement) {
      const shift = key.startsWith('Shift+')
      return env.fire('keydown', { key: shift ? key.slice(6) : key, shiftKey: shift, target })
    },

    /** click sends a click at client (x, y) on target; detail 0 is a click from assistive technology. */
    click(target, x, y, detail = 1) {
      return env.fire('click', { target, clientX: x, clientY: y, detail })
    },

    /**
     * drag presses the primary button of a mouse at client x on target,
     * moves to x2 and releases there, firing the pointer events and the
     * click a browser fires; between(), if given, runs after the move.
     */
    drag(target, x, x2, y, { between, pointerType = 'mouse' } = {}) {
      const p = { pointerId: 7, pointerType, button: 0, clientY: y }
      const down = env.fire('pointerdown', { ...p, target, clientX: x })
      env.fire('pointermove', { ...p, target, clientX: x2 })
      between?.()
      const captured = doc.captures.get(7) ?? target
      env.fire('pointerup', { ...p, target: captured, clientX: x2 })
      doc.captures.delete(7)
      env.fire('lostpointercapture', { ...p, target: captured })
      env.click(captured, x2, y)
      return down
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

  before?.(env)
  run()
  for (const name of companions) run(name)
  return env
}
