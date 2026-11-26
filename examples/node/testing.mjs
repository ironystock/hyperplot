// testing.mjs: helpers for this example's tests (not a test file itself).
//
// Node has no XML parser built in, so parseXml is a small strict one: it
// is enough to prove that a chart is well-formed XML (spec/spec.md §3.1)
// and to query it. select() implements the selector subset of
// spec/conformance/README.md, and fixtureProblems() runs a fixture's
// checks.json server assertions on this renderer's output, so the tests
// need neither Go nor the Python checker.
import { readFileSync } from 'node:fs'
import { join } from 'node:path'

const ENTITIES = { amp: '&', lt: '<', gt: '>', quot: '"', apos: "'" }
const NAME = /^[A-Za-z_:][-A-Za-z0-9_:.]*/

function decode(s, where) {
  return s.replace(/&([^;]*);?/g, (m, e) => {
    if (!m.endsWith(';')) throw new SyntaxError(`bare & ${where}`)
    if (ENTITIES[e]) return ENTITIES[e]
    const n = /^#x([0-9a-fA-F]+)$/.exec(e)?.[1]
    const d = /^#([0-9]+)$/.exec(e)?.[1]
    if (n === undefined && d === undefined) throw new SyntaxError(`unknown entity &${e}; ${where}`)
    return String.fromCodePoint(n !== undefined ? parseInt(n, 16) : Number(d))
  })
}

function checkChars(s, where) {
  for (const c of s) {
    const o = c.codePointAt(0)
    const ok = o === 9 || o === 10 || o === 13 || (o >= 0x20 && o <= 0xd7ff) || (o >= 0xe000 && o <= 0xfffd) || o >= 0x10000
    if (!ok) throw new SyntaxError(`character ${o.toString(16)} not allowed in XML ${where}`)
  }
}

/**
 * parseXml parses one element (with surrounding whitespace allowed) into
 * {name, attrs, children, parent, text()}. It throws a SyntaxError on
 * anything that is not well-formed: unclosed or mismatched tags, unquoted
 * or repeated attributes, bare & or <, unknown entities, forbidden
 * characters, or more than one root.
 */
export function parseXml(src) {
  checkChars(src, 'in document')
  let i = 0
  const root = { name: '#document', attrs: {}, children: [] }
  let cur = root
  const textNode = (s) => cur.children.push({ text: decode(s, `in <${cur.name}>`) })
  while (i < src.length) {
    const lt = src.indexOf('<', i)
    const text = src.slice(i, lt < 0 ? src.length : lt)
    if (text) {
      if (cur === root && text.trim()) throw new SyntaxError('text outside the root element')
      if (cur !== root) textNode(text)
    }
    if (lt < 0) break
    i = lt
    if (src.startsWith('<!--', i)) {
      const end = src.indexOf('-->', i)
      if (end < 0) throw new SyntaxError('unclosed comment')
      i = end + 3
      continue
    }
    if (src.startsWith('</', i)) {
      const m = /^<\/([^\s>]+)\s*>/.exec(src.slice(i))
      if (!m || m[1] !== cur.name) throw new SyntaxError(`</${m?.[1]}> does not close <${cur.name}>`)
      cur = cur.parent
      i += m[0].length
      continue
    }
    const name = NAME.exec(src.slice(i + 1))?.[0]
    if (!name) throw new SyntaxError(`bad tag at ${i}`)
    if (cur === root && root.children.length) throw new SyntaxError('more than one root element')
    i += 1 + name.length
    const el = { name, attrs: {}, children: [], parent: cur }
    for (;;) {
      const ws = /^\s*/.exec(src.slice(i))[0]
      i += ws.length
      if (src.startsWith('/>', i)) {
        i += 2
        cur.children.push(el)
        break
      }
      if (src[i] === '>') {
        i += 1
        cur.children.push(el)
        cur = el
        break
      }
      if (!ws) throw new SyntaxError(`attributes of <${name}> need spaces between them`)
      const a = /^([A-Za-z_:][-A-Za-z0-9_:.]*)\s*=\s*("([^"<]*)"|'([^'<]*)')/.exec(src.slice(i))
      if (!a) throw new SyntaxError(`bad attribute in <${name}> at ${i}: ${src.slice(i, i + 20)}`)
      if (a[1] in el.attrs) throw new SyntaxError(`repeated attribute ${a[1]} in <${name}>`)
      // Attribute-value normalization: literal TAB, LF and CR become spaces.
      el.attrs[a[1]] = decode((a[3] ?? a[4]).replace(/[\t\n\r]/g, ' '), `in ${a[1]}`)
      i += a[0].length
    }
  }
  if (cur !== root) throw new SyntaxError(`unclosed <${cur.name}>`)
  if (root.children.length !== 1) throw new SyntaxError('no root element')
  return root.children[0]
}

/** text returns an element's text content. */
export function text(el) {
  return el.text ?? el.children.map(text).join('')
}

/** elements returns el and all its descendant elements, in document order. */
export function elements(el) {
  return el.text !== undefined ? [] : [el, ...el.children.flatMap(elements)]
}

const localName = (n) => n.slice(n.indexOf(':') + 1)

function compound(s) {
  const parts = s.match(/^(\*|[A-Za-z][-\w]*)?((?:\.[-\w]+|#[-\w.:]+|\[[-\w:]+(?:="[^"]*")?\])*)$/)
  if (!parts) throw new Error(`selector not in the subset: ${s}`)
  const tests = []
  if (parts[1] && parts[1] !== '*') tests.push((el) => localName(el.name) === parts[1])
  for (const t of parts[2].match(/\.[-\w]+|#[-\w.:]+|\[[^\]]+\]/g) ?? []) {
    if (t[0] === '.') tests.push((el) => (el.attrs.class ?? '').split(/\s+/).includes(t.slice(1)))
    else if (t[0] === '#') tests.push((el) => el.attrs.id === t.slice(1))
    else {
      const [, name, value] = /^\[([^=\]]+)(?:="(.*)")?\]$/.exec(t)
      tests.push((el) => name in el.attrs && (value === undefined || el.attrs[name] === value))
    }
  }
  return (el) => tests.every((f) => f(el))
}

/** select returns the elements under root (root included) that match sel, in document order. */
export function select(root, sel) {
  // "a b > c" becomes [{test: a}, {test: b, comb: ' '}, {test: c, comb: '>'}].
  const steps = []
  let comb = ' '
  for (const tok of sel.trim().replace(/\s*>\s*/g, ' > ').split(/\s+/)) {
    if (tok === '>') comb = '>'
    else {
      steps.push({ test: compound(tok), comb })
      comb = ' '
    }
  }
  // Match right to left: the subject, then its parent (>) or any ancestor.
  const matchAt = (el, j) => {
    if (!el || el.name === '#document' || !steps[j].test(el)) return false
    if (j === 0) return true
    if (steps[j].comb === '>') return matchAt(el.parent, j - 1)
    for (let a = el.parent; a && a.name !== '#document'; a = a.parent) if (matchAt(a, j - 1)) return true
    return false
  }
  return elements(root).filter((el) => matchAt(el, steps.length - 1))
}

const fixtures = new URL('../../spec/conformance/', import.meta.url)

/** fixture reads a conformance fixture file as text. */
export const fixture = (path) => readFileSync(new URL(path, fixtures), 'utf8')

/**
 * fixtureProblems runs the checks.json of fixture against docs, a map of
 * {chart, table, 'frame-a', ...} to the raw text this renderer wrote. It
 * returns a list of problems (empty when everything passes), including the
 * `should` assertions, which this example meets.
 */
export function fixtureProblems(name, docs) {
  const checks = JSON.parse(fixture(join(name, 'checks.json')))
  const problems = []
  const parsed = {}
  for (const [k, raw] of Object.entries(docs)) {
    try {
      parsed[k] = parseXml(raw)
    } catch (err) {
      problems.push(`${k}: not well-formed: ${err.message}`)
    }
  }
  const svgs = Object.keys(docs).filter((k) => k !== 'table')
  const f = checks.forbid ?? {}
  for (const k of Object.keys(parsed)) {
    for (const el of elements(parsed[k])) {
      if (f.elements?.includes(localName(el.name))) problems.push(`${k}: forbidden <${el.name}>`)
      for (const a of Object.keys(el.attrs)) {
        if (f.attrs?.includes(a) || f.attrPrefixes?.some((p) => a.startsWith(p))) problems.push(`${k}: forbidden ${a}= on <${el.name}>`)
      }
    }
    for (const w of f.where ?? []) {
      for (const el of select(parsed[k], w.sel)) {
        for (const d of w.deep ? elements(el) : [el]) {
          for (const a of w.attrs ?? []) if (a in d.attrs) problems.push(`${k}: ${a}= inside ${w.sel}`)
          if (d !== el && w.elements?.includes(localName(d.name))) problems.push(`${k}: <${d.name}> inside ${w.sel}`)
        }
      }
    }
    for (const s of checks.raw?.absent ?? []) if (docs[k].includes(s)) problems.push(`${k}: raw ${JSON.stringify(s)} present`)
  }
  for (const k of svgs) {
    for (const m of parsed[k] ? select(parsed[k], 'metadata.hp-tips') : []) {
      for (const s of checks.tipsRaw?.absent ?? []) if (text(m).includes(s)) problems.push(`${k}: hp-tips holds raw ${s}`)
    }
  }
  for (const a of checks.server ?? []) {
    const doc = parsed[a.in === 'table' ? 'table' : a.frame ?? (svgs.includes('chart') ? 'chart' : svgs[0])]
    if (!doc) continue
    const where = `${a.frame ?? a.in ?? 'chart'} ${a.sel}`
    const els = select(doc, a.sel)
    if (a.count !== undefined ? els.length !== a.count : els.length === 0) {
      problems.push(`${where}: ${els.length} matches, want ${a.count ?? 'some'}`)
      continue
    }
    for (const el of els) {
      for (const c of a.classes ?? []) if (!(el.attrs.class ?? '').split(/\s+/).includes(c)) problems.push(`${where}: no class ${c}`)
      for (const [n, v] of Object.entries(a.attrs ?? {})) if (el.attrs[n] !== v) problems.push(`${where}: ${n}=${JSON.stringify(el.attrs[n])}, want ${JSON.stringify(v)}`)
      for (const [n, re] of Object.entries(a.match ?? {})) if (!new RegExp(re).test(el.attrs[n] ?? '')) problems.push(`${where}: ${n}=${JSON.stringify(el.attrs[n])} does not match ${re}`)
      if (a.text !== undefined && text(el) !== a.text) problems.push(`${where}: text ${JSON.stringify(text(el))}, want ${JSON.stringify(a.text)}`)
      if (a.tips) {
        const tips = JSON.parse(text(el))
        const entries = tips.rows.flatMap((r) => r.v)
        const got = {
          mode: tips.mode,
          count: tips.rows.length,
          k: tips.rows.map((r) => r.k),
          names: [...new Set(entries.map((e) => e[1]))],
          slots: [...new Set(entries.map((e) => JSON.stringify([e[0], e[1]])))].map((s) => JSON.parse(s)),
        }
        for (const key of Object.keys(a.tips)) {
          if (JSON.stringify(got[key]) !== JSON.stringify(a.tips[key])) problems.push(`${where}: tips ${key} ${JSON.stringify(got[key])}, want ${JSON.stringify(a.tips[key])}`)
        }
      }
    }
  }
  for (const u of checks.unique ?? []) {
    const doc = parsed[u.in === 'table' ? 'table' : 'chart']
    if (!doc) continue
    const vals = select(doc, u.sel).map((el) => el.attrs[u.attr])
    if (new Set(vals).size !== vals.length) problems.push(`${u.in ?? 'chart'}: ${u.attr} values repeat`)
  }
  return problems
}
