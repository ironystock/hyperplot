// render-fixtures.mjs renders the conformance fixtures this example's
// renderer supports, so a checker can test the output against each
// fixture's checks.json (spec/conformance/README.md).
//
//   node examples/node/render-fixtures.mjs OUT
//
// writes OUT/<fixture>/chart.svg and table.html (or frame-*.svg for the
// slide fixture). scatter-xy is skipped: this renderer draws no dots.
import { mkdirSync, readFileSync, readdirSync, writeFileSync, existsSync } from 'node:fs'
import { join, resolve } from 'node:path'
import { fileURLToPath } from 'node:url'
import { render } from './render.mjs'
import { parseTimeValue } from './wire.mjs'

const fixtures = fileURLToPath(new URL('../../spec/conformance/', import.meta.url))

/** chartFromInput turns a fixture's chart description into render()'s input: RFC 3339 times become epoch ms. */
export function chartFromInput(c) {
  const ms = (list) => list.map((s) => parseTimeValue(s))
  return {
    ...c,
    x: c.x?.timeDomain ? { ...c.x, timeDomain: ms(c.x.timeDomain) } : c.x,
    marks: (c.marks ?? []).map((m) => ({ ...m, x: m.x.times ? { times: ms(m.x.times) } : m.x })),
  }
}

/** supported reports whether render() can draw a fixture's chart (lines on time, or bars). */
export function supported(c) {
  return (c.marks ?? []).every((m) => (m.type === 'line' && m.x.times) || (m.type === 'bar' && m.x.categories))
}

/** renderFixtures writes every supported fixture under out and returns their names. */
export function renderFixtures(out) {
  const done = []
  for (const name of readdirSync(fixtures).sort()) {
    const file = join(fixtures, name, 'input.json')
    if (!existsSync(file)) continue
    const input = JSON.parse(readFileSync(file, 'utf8'))
    const charts = input.frames ? Object.entries(input.frames) : [['chart', input.chart]]
    if (!charts.every(([, c]) => supported(c))) continue
    mkdirSync(join(out, name), { recursive: true })
    for (const [frame, c] of charts) {
      const { svg, table } = render(chartFromInput(c))
      writeFileSync(join(out, name, `${frame}.svg`), svg)
      if (input.table) writeFileSync(join(out, name, 'table.html'), table)
    }
    done.push(name)
  }
  return done
}

if (process.argv[1] && resolve(process.argv[1]) === fileURLToPath(import.meta.url)) {
  const out = process.argv[2]
  if (!out) {
    console.error('usage: node render-fixtures.mjs OUT')
    process.exit(2)
  }
  console.log(`rendered ${renderFixtures(resolve(out)).join(', ')} into ${resolve(out)}`)
}
