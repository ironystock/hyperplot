// Edge cases of loading, swapping and hovering under the real htmx 4.0.2 in
// Chrome, on static test pages (no Go), each found by review and verified in
// a browser before it was fixed:
//
//   - the crosshair and the scatter ring stay drawn through settle delays,
//     innerHTML swaps and view transitions;
//   - htmx loaded as a module (after this classic script): requests still
//     carry the hp-* parameters;
//   - htmx's hx-targets registered after the extension: slides and table
//     follow still work;
//   - a polled chart in a modal dialog keeps its status region inside it;
//   - an easing Element.animate() refuses does not break swaps;
//   - hx-history-cache copies carry no inline styles (strict CSP on Back);
//   - the tooltip in a dialog with a transform sits by its point;
//   - without htmx, autosize elements added later are observed;
//   - a swap cancelled during a path tween lets the tween finish;
//   - a brush dragged across a slide keeps its edge under the pointer;
//   - the band highlight keeps up with keyboard steps;
//   - the pointer can rest on the tooltip (WCAG 1.4.13).
//
// Every test fails on a page error. Run with `npm run test:browser` (see
// harness.mjs).
import { after, before, test } from 'node:test'
import assert from 'node:assert/strict'
import { readFileSync } from 'node:fs'
import { fileURLToPath } from 'node:url'
import { cleanup, fixture, launch, startStatic, waitFor } from './harness.mjs'

let server
let browser
let page

const LINE = fixture('line-time/chart.svg') // id lt, plot 24..228 × 8..98, points at x 24, 126, 228
const SCATTER = fixture('scatter-xy/chart.svg') // id sx
const BANDS = fixture('bars-band/chart.svg').replace('class="hp"', 'class="hp hp-anim"') // id bb, bands at 78.61 and 178.2
const FRAME_A = fixture('slide/frame-a.svg') // id sl; frame-b slides it by 5 minutes
const FRAME_B = fixture('slide/frame-b.svg')
const ESM = readFileSync(fileURLToPath(new URL('../node_modules/htmx.org/dist/htmx.esm.js', import.meta.url)))

const CONFIG = { noSwap: [204, 304, '4xx', '5xx'] }
const head = (scripts, config = CONFIG, extra = '') =>
  `<!doctype html><html lang="en"><head><meta charset="utf-8"><title>hx-plot edge test</title>` +
  `<meta name="htmx-config" content='${JSON.stringify(config)}'>` +
  `<link rel="stylesheet" href="/plot.css">${extra}` +
  scripts.map((s) => `<script src="/${s}.js"></script>`).join('') +
  `</head><body>`

const rows = (n) =>
  `<table class="hp-table" id="g-table"><caption>Log</caption><tbody>` +
  Array.from({ length: n }, (_, i) => `<tr id="g-r-${i}"><th scope="row">Row ${i}</th></tr>`).join('') +
  `</tbody></table>`

// script serves a small same-origin script (the pages' CSP refuses inline ones).
const script = (path, body) =>
  server.handle(path, (req, res) => {
    res.writeHead(200, { 'content-type': 'text/javascript; charset=utf-8', 'cache-control': 'no-store' })
    res.end(body)
  })

const core = ['htmx', 'hx-plot']
const POLLS = [
  ['innerMorph transition:true', 500],
  ['innerHTML settle:300ms', 500],
  ['innerHTML', 100],
  ['outerHTML', 100],
]

before(async () => {
  server = await startStatic()

  // Polled charts, one page per swap style.
  for (const [i, [swap, ms]] of POLLS.entries()) {
    for (const [name, chart] of [
      ['line', LINE],
      ['scatter', SCATTER],
    ]) {
      const w = `<div id="w" hx-get="/poll-${name}-${i}" hx-trigger="every ${ms}ms" hx-swap="${swap}">${chart}</div>`
      server.page(`/poll-${name}-${i}`.replace('poll', 'page'), head(core) + w + `</body></html>`)
      server.frames(`/poll-${name}-${i}`, [swap.startsWith('outer') ? w : chart])
    }
  }

  // htmx as a module script, which runs after the classic extension script.
  server.handle('/htmx-esm.js', (req, res) => {
    res.writeHead(200, { 'content-type': 'text/javascript; charset=utf-8', 'cache-control': 'no-store' })
    res.end(ESM)
  })
  server.page(
    '/module',
    `<!doctype html><html lang="en"><head><meta charset="utf-8"><title>module</title>` +
      `<script type="module" src="/htmx-esm.js"></script><script src="/hx-plot.js"></script><script src="/hx-plot-brush.js"></script></head><body>` +
      `<div hx-plot:autosize hx-get="/module-frag" hx-trigger="htmx:plot:select" hx-swap="none">${LINE}</div></body></html>`,
  )
  server.frames('/module-frag', ['<b>ok</b>'])

  // hx-targets registered after the extension.
  server.page(
    '/targets',
    head(['htmx', 'hx-plot', 'ext/hx-targets']) +
      `<div class="c" id="w">${FRAME_A}</div><button id="go" hx-get="/targets-frame" hx-targets=".c" hx-swap="innerMorph">go</button></body></html>`,
  )
  server.frames('/targets-frame', [FRAME_B])
  server.page(
    '/targets-table',
    head(['htmx', 'hx-plot', 'ext/hx-targets'], CONFIG, '<style>.c{height:120px;overflow-y:auto}</style>') +
      `<div class="c" id="w">${rows(30)}</div><button id="go" hx-get="/targets-rows" hx-targets=".c" hx-swap="innerMorph">go</button></body></html>`,
    { csp: false },
  )
  server.frames('/targets-rows', [rows(40)])

  // A polled chart in a modal dialog (showModal() autofocuses the chart).
  server.page(
    '/dialog-poll',
    head(core) +
      `<dialog id="dlg"><div hx-get="/dialog-same" hx-trigger="every 300ms" hx-swap="innerMorph">${LINE}</div></dialog>` +
      `<script src="/open-dialog.js"></script></body></html>`,
  )
  server.frames('/dialog-same', [LINE])
  script('/open-dialog.js', 'document.getElementById("dlg").showModal()')

  // An easing that Element.animate() refuses.
  server.page(
    '/easing',
    head(core, { ...CONFIG, plot: { easing: 'easeOutCubic' } }) +
      `<div id="w" hx-get="/easing-frame" hx-trigger="go" hx-swap="innerMorph">${FRAME_A}</div></body></html>`,
  )
  server.frames('/easing-frame', [FRAME_B + '<button id="b2" hx-get="/nowhere" hx-trigger="click">b2</button>'])

  // hx-history-cache under a strict CSP: Back during a slide, and after a brush.
  const hc = ['htmx', 'ext/hx-history-cache', 'hx-plot', 'hx-plot-brush']
  server.page(
    '/hc-slide',
    head(hc) +
      `<div id="w" hx-get="/hc-slide-b" hx-trigger="go" hx-swap="innerMorph">${FRAME_A}</div>` +
      `<button id="go2" hx-get="/hc-p2" hx-target="body" hx-push-url="/hc-p2">next</button></body></html>`,
  )
  server.frames('/hc-slide-b', [FRAME_B])
  server.frames('/hc-p2', ['<p id="p2">page 2</p>'])
  server.page(
    '/hc-brush',
    head(hc) + `<div id="w" hx-plot:brush hx-get="/hc-p3" hx-trigger="htmx:plot:brush" hx-target="body" hx-push-url="/hc-p3">${LINE}</div></body></html>`,
  )
  server.frames('/hc-p3', ['<p id="p3">page 3</p>'])

  // Dialogs with a transform or a backdrop filter (inline <style>: no CSP).
  for (const [name, css] of [
    ['plain', '#dlg{margin:120px auto auto 300px}'],
    ['xform', '#dlg{transform:translate(0,0);margin:120px auto auto 300px}'],
    ['center', '#dlg{position:fixed;top:50%;left:50%;margin:0;transform:translate(-50%,-50%)}'],
    ['blur', '#dlg{backdrop-filter:blur(4px);margin:120px auto auto 300px}'],
  ]) {
    server.page(`/tipdlg-${name}`, head(core, CONFIG, `<style>${css}</style>`) + `<dialog id="dlg">${LINE}</dialog><script src="/open-dialog.js"></script></body></html>`, {
      csp: false,
    })
  }

  // Without htmx: an autosize element added after load.
  server.page(
    '/stand',
    `<!doctype html><html lang="en"><head><meta charset="utf-8"><title>standalone</title><script src="/hx-plot.js"></script></head><body>` +
      `<div id="a0" hx-plot:autosize style="width:400px;height:100px"></div><script src="/stand.js"></script></body></html>`,
    { csp: false },
  )
  script(
    '/stand.js',
    `window.__rs = []
document.addEventListener('htmx:plot:resize', (e) => window.__rs.push(e.target.id + ':' + e.detail.width), true)
addEventListener('load', () => {
  const a1 = document.createElement('div')
  a1.id = 'a1'
  a1.setAttribute('hx-plot:autosize', '')
  a1.style.cssText = 'width:400px;height:100px'
  document.body.append(a1)
  setTimeout(() => { for (const id of ['a0', 'a1']) document.getElementById(id).style.width = '200px' }, 300)
})`,
  )

  // The tween companion (forced, as in Safari), on a page that cancels
  // swaps on demand.
  script('/tw-force.js', `hyperplot.tween.force = true
document.addEventListener('htmx:before:swap', (e) => { if (window.__cancel) e.preventDefault() })`)
  server.page(
    '/tween',
    head(['htmx', 'hx-plot', 'hx-plot-tween', 'tw-force'], { ...CONFIG, plot: { duration: 1500, easing: 'linear' } }) +
      `<div id="w" hx-get="/tween-frame" hx-trigger="every 600ms" hx-swap="innerMorph">${LINE}</div></body></html>`,
  )
  server.frames('/tween-frame', [LINE.replace('M24,68L126,23L228,83', 'M24,38L126,53L228,23')])

  // A brushable sliding chart.
  server.page(
    '/brush-slide',
    head(['htmx', 'hx-plot', 'hx-plot-brush']) +
      `<div hx-plot:brush><div id="w" hx-get="/brush-slide-b" hx-trigger="go" hx-swap="innerMorph">${FRAME_A}</div></div></body></html>`,
  )
  server.frames('/brush-slide-b', [FRAME_B])

  // A band chart, and a plain line chart to hover.
  server.page('/bands', head(core) + `${BANDS}</body></html>`)
  server.page('/hover', head(core) + `<div style="padding:40px">${LINE}</div></body></html>`, { csp: false })

  browser = await launch(server.url)
  page = browser.page
})

after(cleanup)

const noErrors = () => assert.deepEqual(browser.errors.splice(0).map((e) => e.message), [], 'page errors')
const ready = () => waitFor(page, () => window.hyperplot?._ && window.htmx, null, { what: 'the extension' })

const at = (id, x, y) =>
  page.evaluate(
    ([id, x, y]) => {
      const p = new DOMPoint(x, y).matrixTransform(document.getElementById(id).getScreenCTM())
      return [p.x, p.y]
    },
    [id, x, y],
  )

const hover = async (id, x, y) => {
  const [px, py] = await at(id, x, y)
  await page.mouse.move(px, py)
  await waitFor(page, () => document.querySelector('.hp-tip')?.style.display === '', null, { what: 'the tooltip' })
  return [px, py]
}

// hiddenFrames counts, over ms milliseconds of animation frames, the frames
// on which the element sel is not visible, and the swaps in that time.
const hiddenFrames = (sel, ms) =>
  page.evaluate(
    ([sel, ms]) =>
      new Promise((done) => {
        let n = 0
        let hidden = 0
        let swaps = 0
        document.addEventListener('htmx:after:swap', () => swaps++)
        const end = performance.now() + ms
        const tick = () => {
          const el = document.querySelector(sel)
          n++
          if (!el || getComputedStyle(el).visibility !== 'visible') hidden++
          if (performance.now() < end) requestAnimationFrame(tick)
          else done({ n, hidden, swaps })
        }
        requestAnimationFrame(tick)
      }),
    [sel, ms],
  )

for (const [i, [swap, ms]] of POLLS.entries()) {
  test(`a hovered crosshair stays drawn through "${swap}" polls every ${ms} ms`, async () => {
    await page.goto(server.url + `/page-line-${i}`)
    await ready()
    await hover('lt', 126, 50)
    await waitFor(page, () => getComputedStyle(document.querySelector('#lt .hp-crosshair')).visibility === 'visible', null, { what: 'the crosshair' })
    const r = await hiddenFrames('#lt .hp-crosshair', 2500)
    assert.ok(r.swaps >= 3, `swaps: ${JSON.stringify(r)}`)
    assert.equal(r.hidden, 0, `frames without the crosshair: ${JSON.stringify(r)}`)
    assert.equal(await page.evaluate(() => document.querySelector('.hp-tip').style.display), '', 'the tooltip stays')
    noErrors()
  })

  test(`the keyboard reader's ring stays drawn through "${swap}" polls every ${ms} ms`, async () => {
    await page.goto(server.url + `/page-scatter-${i}`)
    await ready()
    await page.mouse.move(5, 890)
    await page.focus('#sx')
    await page.keyboard.press('ArrowRight')
    await page.keyboard.press('ArrowRight')
    const r = await hiddenFrames('#sx .hp-focus', 2500)
    assert.ok(r.swaps >= 3, `swaps: ${JSON.stringify(r)}`)
    assert.equal(r.hidden, 0, `frames without the ring: ${JSON.stringify(r)}`)
    noErrors()
  })
}

test('htmx loaded as a module after the extension: a select request carries the hp-* parameters', async () => {
  await page.goto(server.url + '/module')
  await ready()
  const [x, y] = await at('lt', 126, 50)
  await page.mouse.click(x, y)
  const end = Date.now() + 10000
  while (!server.log.some((r) => r.path === '/module-frag')) {
    assert.ok(Date.now() < end, 'the select request')
    await page.waitForTimeout(50)
  }
  const req = server.log.filter((r) => r.path === '/module-frag').map((r) => r.params)
  assert.equal(req[0]['hp-chart'], 'lt')
  assert.equal(req[0]['hp-x'], '2026-03-03T14:10:00Z')
  assert.match(req[0]['hp-w'] ?? '', /^\d+$/)
  assert.ok(await page.evaluate(() => !!window.hyperplot.brush), 'the brush companion started too')
  noErrors()
})

test('hx-targets registered after the extension: the chart slides', async () => {
  await page.goto(server.url + '/targets')
  await ready()
  await page.evaluate(() => {
    document.addEventListener('htmx:after:swap', () => {
      window.__anims = document.getAnimations().map((a) => a.constructor.name + ':' + a.effect.target.localName)
    })
  })
  await page.click('#go')
  const anims = await waitFor(page, () => window.__anims, null, { what: 'the swap' })
  assert.ok(anims.includes('Animation:path'), `the slide runs: ${JSON.stringify(anims)}`)
  assert.ok(!anims.some((a) => a.startsWith('CSSTransition')), `no CSS transition: ${JSON.stringify(anims)}`)
  noErrors()
})

test('hx-targets registered after the extension: a table at its end follows', async () => {
  await page.goto(server.url + '/targets-table')
  await ready()
  await page.evaluate(() => {
    const c = document.querySelector('.c')
    c.scrollTop = c.scrollHeight
    document.addEventListener('htmx:after:swap', () => setTimeout(() => (window.__gap = c.scrollHeight - c.scrollTop - c.clientHeight), 50))
  })
  await page.click('#go')
  const [gap] = await waitFor(page, () => window.__gap != null && [window.__gap], null, { what: 'the swap' })
  assert.ok(gap <= 2, `scrolled to the end: ${gap}`)
  assert.equal(await page.locator('.c tbody tr').count(), 40)
  noErrors()
})

test('a polled chart in a modal dialog keeps its status region exposed inside it', async () => {
  await page.goto(server.url + '/dialog-poll')
  await ready()
  // showModal() focused the chart before the extension was ready.
  assert.equal(await page.evaluate(() => document.activeElement?.id), 'lt')
  const where = () => page.evaluate(() => document.querySelector('.hp-live')?.parentElement?.id ?? '')
  await waitFor(page, () => document.querySelector('.hp-live')?.parentElement?.id === 'dlg', null, { what: 'the region in the dialog' })
  await page.evaluate(() => {
    window.__swaps = 0
    document.addEventListener('htmx:after:swap', () => window.__swaps++)
  })
  await waitFor(page, () => window.__swaps >= 2, null, { what: 'two swaps' })
  assert.equal(await where(), 'dlg', 'still in the dialog after swaps, before any step')
  const cdp = await page.context().newCDPSession(page)
  try {
    const { nodes } = await cdp.send('Accessibility.getFullAXTree')
    assert.equal(nodes.filter((n) => n.role?.value === 'status' && !n.ignored).length, 1, 'exposed before the first step')
  } finally {
    await cdp.detach()
  }
  await page.keyboard.press('ArrowRight')
  assert.match(await page.evaluate(() => document.querySelector('.hp-live').textContent), /^Mar 3, 2026 2:00 PM: /)
  assert.equal(await where(), 'dlg')
  noErrors()
})

test('an easing that Element.animate refuses falls back, and the swap completes', async () => {
  await page.goto(server.url + '/easing')
  await ready()
  await page.evaluate(() => {
    window.__ev = []
    for (const n of ['htmx:after:swap', 'htmx:error']) document.addEventListener(n, () => window.__ev.push(n))
    htmx.trigger('#w', 'go')
  })
  await waitFor(page, () => window.__ev.length, null, { what: 'the swap' })
  await page.waitForTimeout(600)
  assert.deepEqual(await page.evaluate(() => window.__ev), ['htmx:after:swap'])
  assert.ok(await page.evaluate(() => document.getElementById('b2').hasAttribute('data-htmx-powered')), 'the new content is processed')
  assert.equal(await page.locator('#sl [style]').count(), 0, 'no inline styles left behind')
  noErrors()
})

test('hx-history-cache: Back after leaving during a slide restores marks without inline styles', async () => {
  await page.goto(server.url + '/hc-slide')
  await ready()
  await page.evaluate(() => {
    document.addEventListener(
      'htmx:after:swap',
      () =>
        requestAnimationFrame(() => {
          window.__pinned = document.querySelectorAll('#sl [style]').length
          document.getElementById('go2').click()
        }),
      { once: true },
    )
    htmx.trigger('#w', 'go')
  })
  await page.waitForSelector('#p2')
  assert.ok((await page.evaluate(() => window.__pinned)) > 0, 'the navigation came during the slide')
  await page.goBack()
  await page.waitForSelector('#sl')
  await page.waitForTimeout(500)
  assert.equal(server.log.filter((r) => r.path === '/hc-slide').length, 1, 'restored from hx-history-cache')
  assert.equal(await page.locator('#sl [style], #sl[style]').count(), 0)
  noErrors()
})

test('hx-history-cache: Back after a brush restores the chart without a style attribute', async () => {
  await page.goto(server.url + '/hc-brush')
  await ready()
  const [x0, y] = await at('lt', 60, 50)
  const [x1] = await at('lt', 180, 50)
  await page.mouse.move(x0, y)
  await page.mouse.down()
  await page.mouse.move(x1, y, { steps: 5 })
  await page.mouse.up()
  await page.waitForSelector('#p3')
  await page.goBack()
  await page.waitForSelector('#lt')
  await page.waitForTimeout(300)
  assert.equal(server.log.filter((r) => r.path === '/hc-brush').length, 1, 'restored from hx-history-cache')
  assert.equal(await page.evaluate(() => document.getElementById('lt').getAttribute('style')), null)
  noErrors()
})

for (const name of ['plain', 'xform', 'center', 'blur']) {
  test(`the tooltip sits by its point in a ${name} modal dialog`, async () => {
    await page.goto(server.url + `/tipdlg-${name}`)
    await ready()
    await hover('lt', 126, 50)
    const [ax] = await at('lt', 126, 50)
    const r = await page.evaluate(() => {
      const b = document.querySelector('.hp-tip').getBoundingClientRect()
      return { left: b.left, width: b.width, height: b.height, parent: document.querySelector('.hp-tip').parentElement.id }
    })
    assert.equal(r.parent, 'dlg')
    assert.ok(Math.abs(r.left - (ax + 12)) < 1 || Math.abs(r.left + r.width - (ax - 12)) < 1, `12 px beside its point at ${ax}: ${JSON.stringify(r)}`)
    assert.ok(r.height < 80, `not squeezed by the dialog: ${JSON.stringify(r)}`)
    noErrors()
  })
}

test('without htmx, an autosize element added after load dispatches htmx:plot:resize', async () => {
  await page.goto(server.url + '/stand')
  await waitFor(page, () => window.hyperplot?._, null, { what: 'the extension' })
  await waitFor(page, () => window.__rs.length >= 2, null, { what: 'two resizes', timeout: 5000 }).catch(() => {})
  assert.deepEqual((await page.evaluate(() => window.__rs)).sort(), ['a0:200', 'a1:200'])
  noErrors()
})

test('a swap cancelled during a path tween lets the tween finish on its frame', async () => {
  await page.goto(server.url + '/tween')
  await ready()
  await waitFor(page, () => !/^M24,(68|38)L/.test(document.querySelector('#lt path.hp-line').getAttribute('d')), null, { what: 'a tween' })
  await page.evaluate(() => (window.__cancel = true))
  await page.waitForTimeout(2500)
  assert.equal(await page.evaluate(() => document.querySelector('#lt path.hp-line').getAttribute('d')), 'M24,38L126,53L228,23', 'not frozen half-way')
  noErrors()
})

test('a brush dragged across a slide keeps its dragged edge under the pointer', async () => {
  await page.goto(server.url + '/brush-slide')
  await ready()
  const [x0, y] = await at('sl', 70, 50)
  const [x1] = await at('sl', 150, 50)
  await page.mouse.move(x0, y)
  await page.mouse.down()
  await page.mouse.move(x1, y, { steps: 5 })
  const right = await page.evaluate(
    () =>
      new Promise((done) => {
        const out = []
        document.addEventListener('htmx:after:swap', () => {
          const t0 = performance.now()
          const tick = () => {
            out.push(document.querySelector('#sl rect.hp-brush').getBoundingClientRect().right)
            if (performance.now() - t0 < 300) requestAnimationFrame(tick)
            else done(out)
          }
          tick()
        })
        htmx.trigger('#w', 'go')
      }),
  )
  await page.mouse.up()
  assert.ok(right.every((r) => Math.abs(r - x1) < 1), `the dragged edge stays at ${x1}: ${right.map(Math.round)}`)
  noErrors()
})

test('the band highlight moves with each keyboard step at once', async () => {
  await page.goto(server.url + '/bands')
  await ready()
  await page.focus('#bb')
  await page.keyboard.press('ArrowRight')
  await page.waitForTimeout(500)
  const got = await page.evaluate(
    () =>
      new Promise((done) => {
        const hl = document.querySelector('#bb .hp-band-hl')
        document.getElementById('bb').dispatchEvent(new KeyboardEvent('keydown', { key: 'ArrowRight', bubbles: true }))
        requestAnimationFrame(() => done([hl.getAttribute('x'), parseFloat(getComputedStyle(hl).x)]))
      }),
  )
  assert.ok(Math.abs(Number(got[0]) - got[1]) < 0.01, `drawn where the attribute says: ${got}`)
  noErrors()
})

test('the pointer can move onto the tooltip and rest there; it hides once the pointer leaves both', async () => {
  await page.goto(server.url + '/hover')
  await ready()
  const [px, py] = await hover('lt', 126, 50)
  const head = () => page.evaluate(() => document.querySelector('.hp-tip .hp-tip-t')?.textContent)
  const first = await head()
  await page.waitForTimeout(500) // a reader, not a scan
  const box = await page.evaluate(() => {
    const b = document.querySelector('.hp-tip').getBoundingClientRect()
    return [b.left + b.width / 2, b.top + b.height / 2]
  })
  for (let k = 1; k <= 12; k++) {
    await page.mouse.move(px + ((box[0] - px) * k) / 12, py + ((box[1] - py) * k) / 12)
    await page.waitForTimeout(30)
    assert.equal(await head(), first, `step ${k}: the tooltip keeps its point`)
  }
  assert.ok(
    await page.evaluate(([x, y]) => !!document.elementFromPoint(x, y)?.closest('.hp-tip'), box),
    'the pointer is on the tooltip',
  )
  await page.waitForTimeout(600)
  assert.equal(await page.evaluate(() => document.querySelector('.hp-tip').style.display), '', 'it stays while the pointer rests on it')
  await page.mouse.move(5, 890, { steps: 4 })
  await waitFor(page, () => document.querySelector('.hp-tip').style.display === 'none', null, { what: 'the tooltip to hide' })

  // Scanning the chart is not heading for the tooltip: it follows at once.
  await hover('lt', 24, 50)
  const [x2, y2] = await at('lt', 228, 50)
  await page.mouse.move(x2, y2, { steps: 8 })
  await waitFor(page, () => /2:20 PM/.test(document.querySelector('.hp-tip .hp-tip-t')?.textContent), null, { what: 'the last point', timeout: 2000 })
  noErrors()
})
