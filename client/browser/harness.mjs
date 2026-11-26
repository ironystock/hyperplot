// Shared set-up for the real-browser tests in this directory: it builds and
// starts the Go dashboard (examples/dashboard), any other example server
// (buildGo and startServer, for Go, Python or Node), or a static test server
// that needs no Go (startStatic), launches Chrome through playwright-core, serves
// the page's pinned htmx files from node_modules instead of the CDN (the
// tests need no network), and cleans everything up, also when a test fails
// or times out.
//
// Run with `npm run test:browser` in client/ after `npm ci`. Environment:
//
//	CHROME_PATH     the Chrome or Chromium binary (default: the usual install path)
//	HP_BROWSER_TMP  where Chrome profiles and the dashboard binary go (default: the OS temp dir)
//	HP_REPO         the Go module to build the dashboard from (default: this repository)
//	HP_SPEED        the dashboard's -speed, how fast simulated time runs (default 5)
//	PYTHON          the Python interpreter for the Python example (default python3, python on Windows)
//	HP_SHOTS        a directory to save screenshots in (default: none)
//	HP_HEADED       set to 1 to watch the browser
import { spawn, spawnSync } from 'node:child_process'
import { existsSync, mkdirSync, mkdtempSync, readFileSync, rmSync } from 'node:fs'
import { createServer } from 'node:http'
import { tmpdir } from 'node:os'
import { join, resolve } from 'node:path'
import { fileURLToPath } from 'node:url'
import { chromium } from 'playwright-core'

const here = fileURLToPath(new URL('.', import.meta.url))
const root = resolve(process.env.HP_REPO || join(here, '..', '..'))
const htmxDist = join(here, '..', 'node_modules', 'htmx.org', 'dist')
const CDN = 'https://cdn.jsdelivr.net/npm/htmx.org@4.0.2/dist/'
const win = process.platform === 'win32'

export const speed = Number(process.env.HP_SPEED || 5)
export const shots = process.env.HP_SHOTS ? resolve(process.env.HP_SHOTS) : ''
// shotTag ends every screenshot's name, so runs against two builds can
// save side by side ("before" and "after").
export const shotTag = process.env.HP_SHOT_TAG || 'run'

/** chromePath returns the browser binary: CHROME_PATH, or the first usual install path that exists. */
export function chromePath() {
  if (process.env.CHROME_PATH) return process.env.CHROME_PATH
  const candidates = {
    win32: ['C:/Program Files/Google/Chrome/Application/chrome.exe', 'C:/Program Files (x86)/Google/Chrome/Application/chrome.exe'],
    darwin: ['/Applications/Google Chrome.app/Contents/MacOS/Google Chrome', '/Applications/Chromium.app/Contents/MacOS/Chromium'],
  }[process.platform] ?? ['/usr/bin/google-chrome', '/usr/bin/google-chrome-stable', '/usr/bin/chromium', '/usr/bin/chromium-browser']
  const found = candidates.find((p) => existsSync(p))
  if (!found) throw new Error(`no Chrome found; set CHROME_PATH (looked in ${candidates.join(', ')})`)
  return found
}

// scratch returns a new private directory for this run.
function scratch(prefix) {
  const base = resolve(process.env.HP_BROWSER_TMP || tmpdir())
  mkdirSync(base, { recursive: true })
  return mkdtempSync(join(base, prefix))
}

// removeDir deletes dir, retrying while Windows still holds files of a
// process that just exited.
async function removeDir(dir) {
  for (let i = 0; i < 20; i++) {
    try {
      rmSync(dir, { recursive: true, force: true })
      return
    } catch {
      await new Promise((r) => setTimeout(r, 250))
    }
  }
  rmSync(dir, { recursive: true, force: true })
}

// killTree ends a process and every process it started.
function killTree(child) {
  if (!child || child.exitCode != null || child.signalCode != null) return
  if (win) spawnSync('taskkill', ['/pid', String(child.pid), '/T', '/F'], { stdio: 'ignore' })
  else {
    try {
      process.kill(-child.pid, 'SIGKILL') // the process group: detached children lead their own
    } catch {
      child.kill('SIGKILL')
    }
  }
}

// cleanups run at the end of the test file, and again on exit as a backstop.
const cleanups = []
process.on('exit', () => {
  for (const fn of cleanups.splice(0)) {
    try {
      fn(true)
    } catch {
      // best effort while exiting
    }
  }
})

/** tempDir returns a new scratch directory under HP_BROWSER_TMP, removed when the test file ends. */
export function tempDir(prefix) {
  const dir = scratch(prefix)
  cleanups.push((exiting) => (exiting === true ? rmSync(dir, { recursive: true, force: true }) : removeDir(dir)))
  return dir
}

/** python is the Python interpreter: PYTHON, else `python` on Windows and `python3` elsewhere. */
export const python = process.env.PYTHON || (win ? 'python' : 'python3')

/**
 * buildGo builds the main packages pkgs (such as './examples/dashboard')
 * of HP_REPO with one `go build`, into a new scratch directory. It returns
 * {bin(pkg), remove}: bin gives the path of a package's binary, named
 * after the last element of its path, and remove deletes the directory.
 * The directory is also removed when the test file ends.
 */
export function buildGo(pkgs) {
  const dir = scratch('hp-bin-')
  const remove = async (exiting) => (exiting === true ? rmSync(dir, { recursive: true, force: true }) : removeDir(dir))
  cleanups.push(remove)
  // With several packages, -o names a directory and each binary is named
  // after its package's directory.
  const out = pkgs.length === 1 ? join(dir, binName(pkgs[0])) : dir + '/'
  const build = spawnSync('go', ['build', '-o', out, ...pkgs], { cwd: root, encoding: 'utf8' })
  if (build.status !== 0) throw new Error(`go build failed:\n${build.stderr || build.error}`)
  return { bin: (pkg) => join(dir, binName(pkg)), remove }
}

const binName = (pkg) => pkg.replace(/\/+$/, '').split('/').pop() + (win ? '.exe' : '')

/**
 * startServer runs cmd with args in HP_REPO and waits for the line every
 * example logs when it is ready, "listening on http://…", on stdout or
 * stderr. Pass an address of 127.0.0.1:0 in args so the server takes a
 * free port. It returns {url, stop, log}: url has no trailing slash, stop
 * kills the process tree and waits for it to exit, and log() returns
 * what the server printed. The server is also stopped when the test file
 * ends, or the process exits.
 */
export async function startServer(cmd, args, { name = cmd, timeout = 20000 } = {}) {
  const child = spawn(cmd, args, { cwd: root, stdio: ['ignore', 'pipe', 'pipe'], detached: !win })
  const exited = new Promise((r) => child.once('exit', r))
  const stop = async (exiting) => {
    killTree(child)
    if (exiting !== true && child.exitCode == null && child.signalCode == null) await exited
  }
  cleanups.push(stop)
  let out = ''
  const url = await new Promise((ok, fail) => {
    const timer = setTimeout(() => fail(new Error(`${name} did not start:\n${out}`)), timeout)
    const read = (s) => {
      out += s
      const m = /listening on (http:\/\/[^\s]+)/.exec(out)
      if (m) {
        clearTimeout(timer)
        ok(m[1].replace('localhost', '127.0.0.1').replace(/\/+$/, ''))
      }
    }
    for (const stream of [child.stdout, child.stderr]) {
      stream.setEncoding('utf8')
      stream.on('data', read)
    }
    child.once('error', (e) => {
      clearTimeout(timer)
      fail(new Error(`${name} could not start: ${e.message}`))
    })
    child.once('exit', (code) => {
      clearTimeout(timer)
      fail(new Error(`${name} exited (${code}):\n${out}`))
    })
  }).catch(async (e) => {
    await stop()
    throw e
  })
  return { url, stop, log: () => out }
}

/**
 * startDashboard builds the dashboard from HP_REPO and runs it on a free
 * port, at the given -speed (default HP_SPEED). It returns {url, stop}.
 */
export async function startDashboard({ speed: s = speed } = {}) {
  const pkg = './examples/dashboard'
  const build = buildGo([pkg])
  const args = ['-addr', '127.0.0.1:0']
  if (s !== 1) args.push('-speed', String(s))
  const server = await startServer(build.bin(pkg), args, { name: 'dashboard' })
  const stop = async (exiting) => {
    await server.stop(exiting)
    await build.remove(exiting)
  }
  return { url: server.url, stop }
}

const client = join(here, '..')
const fixtures = join(root, 'spec', 'conformance')

/** fixture returns a file of spec/conformance as text. */
export const fixture = (path) => readFileSync(join(fixtures, path), 'utf8')

// stylesheet returns the reference stylesheet: client/hx-plot.css once the
// Go tests have written it, else the copy inside a standalone preview chart.
function stylesheet() {
  const css = join(client, 'hx-plot.css')
  if (existsSync(css)) return readFileSync(css, 'utf8')
  const svg = readFileSync(join(root, 'plot', 'testdata', 'preview', 'lines_direct_labels.svg'), 'utf8')
  return /<style>([\s\S]*?)<\/style>/.exec(svg)[1]
}

/**
 * startStatic starts a test server on a free port that needs no Go. It
 * serves the client scripts from client/, htmx 4.0.2 and hx-sse from
 * node_modules/htmx.org, the reference stylesheet at /plot.css, and what a
 * test registers: page(path, html, {csp}) a page (with a strict
 * Content-Security-Policy unless csp is false), frames(path, list) a
 * fragment that answers each request with the next of list (the last one
 * repeats), handle(path, fn) a route that fn(req, res) answers itself,
 * sse(path, list, ms) a stream that sends list as unnamed messages every
 * ms. It also serves htmx's own extensions at /ext/hx-*.js. Every request
 * is logged in log as {path, params, headers}. It returns {url, log, page,
 * frames, handle, sse, close}.
 */
export async function startStatic() {
  const routes = new Map()
  const log = []
  const streams = new Set()
  const send = (res, type, body, headers = {}) => {
    res.writeHead(200, { 'content-type': type, 'cache-control': 'no-store', ...headers })
    res.end(body)
  }
  const file = (path) => (req, res) => send(res, 'text/javascript; charset=utf-8', readFileSync(path))
  routes.set('/hx-plot.js', file(join(client, 'hx-plot.js')))
  routes.set('/hx-plot-brush.js', file(join(client, 'hx-plot-brush.js')))
  routes.set('/hx-plot-tween.js', file(join(client, 'hx-plot-tween.js')))
  routes.set('/htmx.js', file(join(htmxDist, 'htmx.js')))
  routes.set('/hx-sse.js', file(join(htmxDist, 'ext', 'hx-sse.js')))
  routes.set('/plot.css', (req, res) => send(res, 'text/css; charset=utf-8', stylesheet()))
  const server = createServer((req, res) => {
    const u = new URL(req.url, 'http://test')
    log.push({ path: u.pathname, params: Object.fromEntries(u.searchParams), headers: req.headers })
    const route = routes.get(u.pathname)
    if (route) return route(req, res)
    // htmx's own extensions: /ext/hx-history-cache.js, /ext/hx-ptag.js, …
    const ext = /^\/ext\/(hx-[\w-]+\.js)$/.exec(u.pathname)
    if (ext && existsSync(join(htmxDist, 'ext', ext[1]))) return file(join(htmxDist, 'ext', ext[1]))(req, res)
    res.writeHead(404).end()
  })
  await new Promise((ok) => server.listen(0, '127.0.0.1', ok))
  const close = async () => {
    for (const res of streams) res.destroy()
    server.closeAllConnections?.()
    await new Promise((ok) => server.close(() => ok()))
  }
  cleanups.push(close)
  const CSP = "default-src 'self'; script-src 'self'; style-src 'self'; img-src 'self' data:"
  return {
    url: `http://127.0.0.1:${server.address().port}`,
    log,
    page(path, html, { csp = true } = {}) {
      routes.set(path, (req, res) => send(res, 'text/html; charset=utf-8', html, csp ? { 'content-security-policy': CSP } : {}))
    },
    frames(path, list) {
      let n = 0
      routes.set(path, (req, res) => send(res, 'text/html; charset=utf-8', list[Math.min(n++, list.length - 1)]))
    },
    handle(path, fn) {
      routes.set(path, fn)
    },
    sse(path, list, ms) {
      routes.set(path, (req, res) => {
        res.writeHead(200, { 'content-type': 'text/event-stream', 'cache-control': 'no-store' })
        streams.add(res)
        let n = 0
        const next = () => {
          if (res.destroyed || n >= list.length) return
          res.write(list[n++].split(/\r\n|\r|\n/).map((l) => 'data: ' + l + '\n').join('') + '\n')
          setTimeout(next, ms)
        }
        res.write('retry: 60000\n\n')
        setTimeout(next, ms)
        req.on('close', () => streams.delete(res))
      })
    },
    close,
  }
}

/**
 * launch starts Chrome with a fresh profile in its own directory and returns
 * {context, page, close}. Requests for the page's pinned htmx files are
 * answered from node_modules/htmx.org (same bytes, so the page's Subresource
 * Integrity hashes still match); every other request off the test server
 * is refused, so a run never depends on the network.
 */
export async function launch(origin, { reducedMotion = 'no-preference' } = {}) {
  const profile = scratch('chrome-')
  let context
  const close = async (exiting) => {
    if (exiting === true) return // the process is going away and takes Chrome with it
    try {
      await context?.close()
    } finally {
      await removeDir(profile)
    }
  }
  cleanups.push(close)
  context = await chromium.launchPersistentContext(profile, {
    executablePath: chromePath(),
    headless: process.env.HP_HEADED !== '1',
    viewport: { width: 1280, height: 900 },
    deviceScaleFactor: 1,
    reducedMotion,
    args: ['--no-first-run', '--no-default-browser-check', '--disable-extensions'],
  })
  // Only requests off the test server are intercepted, so its responses
  // (including the SSE stream) reach the page untouched.
  await context.route((u) => !u.href.startsWith(origin), async (route) => {
    const u = route.request().url()
    if (u.startsWith(CDN)) {
      const file = join(htmxDist, u.slice(CDN.length).split('?')[0])
      if (file.startsWith(htmxDist) && existsSync(file)) {
        return route.fulfill({
          status: 200,
          contentType: 'text/javascript; charset=utf-8',
          headers: { 'access-control-allow-origin': '*' },
          body: readFileSync(file),
        })
      }
    }
    return route.abort()
  })
  const page = context.pages()[0] ?? (await context.newPage())
  const errors = []
  page.on('pageerror', (e) => errors.push(e))
  page.on('console', (m) => m.type() === 'error' && errors.push(new Error(m.text())))
  return { context, page, errors, close }
}

/** cleanup runs every pending cleanup (call it from after()). */
export async function cleanup() {
  for (const fn of cleanups.splice(0).reverse()) await fn()
}

/** shot saves a screenshot of locator as HP_SHOTS/name-HP_SHOT_TAG.png when HP_SHOTS is set. */
export async function shot(locator, name) {
  if (!shots) return ''
  mkdirSync(shots, { recursive: true })
  const path = join(shots, `${name}-${shotTag}.png`)
  await locator.screenshot({ path, animations: 'allow' })
  return path
}

/** waitFor polls fn in the page until it returns a truthy value. */
export async function waitFor(page, fn, arg, { timeout = 30000, what = 'condition' } = {}) {
  const handle = await page.waitForFunction(fn, arg, { timeout, polling: 'raf' }).catch((e) => {
    throw new Error(`timed out waiting for ${what}: ${e.message.split('\n')[0]}`)
  })
  return handle.jsonValue()
}
