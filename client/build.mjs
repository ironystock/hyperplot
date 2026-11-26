// Size check and minified build for the client scripts (docs/DESIGN.md,
// "Size budget"). The sources are the product: plain ES2020 that needs no
// build step, served as they are by the Go package client. The minified
// copies are only for the npm package (built by prepack, not committed).
//
//	npm run size    minify and gzip every script; fail when one is over budget
//	npm run build   also write hx-plot.min.js (and the companions) with source maps
//
// The package has no dependency on a minifier: this runs terser 5.36.0 as a
// throwaway through npx (which downloads it into npx's cache once), or the
// terser command named by the TERSER environment variable. The minified
// build drops the test-only exports.
import { existsSync, readFileSync } from 'node:fs'
import { spawnSync } from 'node:child_process'
import { gzipSync } from 'node:zlib'
import { fileURLToPath } from 'node:url'

// Budgets in bytes: minified, and minified + gzip -9 (the headline).
const SCRIPTS = [
  { file: 'hx-plot.js', min: 13312, gz: 5632 },
  { file: 'hx-plot-brush.js', min: 4096, gz: 2048 },
  { file: 'hx-plot-tween.js', min: 2048, gz: 1024 },
]

const write = process.argv.includes('--write')
const dir = fileURLToPath(new URL('./', import.meta.url))
const terser = process.env.TERSER ? [process.env.TERSER] : ['npx', '--yes', 'terser@5.36.0']

// On Windows npx is a .cmd file, which only runs through the shell, and
// cmd.exe needs the source-map option quoted; elsewhere there is no shell,
// so the program gets each argument as it is and quotes would reach terser.
const shell = process.platform === 'win32'

function minify(file, out) {
  const args = [file, '--ecma', '2020', '-c', '-m', '--define', 'globalThis.__HYPERPLOT_TEST__=false', '--comments', 'some']
  const map = `url='${out}.map'`
  if (out) args.push('--source-map', shell ? `"${map}"` : map, '-o', out)
  const r = spawnSync(terser[0], [...terser.slice(1), ...args], { cwd: dir, encoding: 'utf8', shell })
  if (r.status !== 0) throw new Error(`terser failed on ${file}:\n${r.stderr || r.error}`)
  return out ? readFileSync(dir + out, 'utf8') : r.stdout.replace(/\n$/, '')
}

let over = false
for (const s of SCRIPTS) {
  const src = readFileSync(dir + s.file, 'utf8')
  const code = minify(s.file, write && s.file.replace(/\.js$/, '.min.js'))
  const gz = gzipSync(code, { level: 9 }).length
  const bad = code.length > s.min || gz > s.gz
  over ||= bad
  const n = (v, w) => String(v).padStart(w)
  console.log(
    `${s.file.padEnd(17)} ${n(src.length, 6)} source ${n(code.length, 6)} min (budget ${s.min}) ${n(gz, 5)} gzip (budget ${s.gz})${bad ? '  OVER BUDGET' : ''}`,
  )
}
if (over && !write) {
  console.error('A script is over its size budget (docs/DESIGN.md, "Size budget").')
  process.exitCode = 1
}

// npm leaves a missing "files" entry out of the package without a word, so
// check them: all of them once the minified copies are written (prepack
// then refuses to pack), and the ones that are not built otherwise.
const pkg = JSON.parse(readFileSync(dir + 'package.json', 'utf8'))
const missing = pkg.files.filter((f) => (write || !/\.min\.js(\.map)?$/.test(f)) && !existsSync(dir + f))
if (missing.length) {
  console.error(`package.json lists files that do not exist: ${missing.join(', ')}`)
  process.exitCode = 1
}
