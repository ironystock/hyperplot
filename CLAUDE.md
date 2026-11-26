# CLAUDE.md

Guidance for AI agents working in this repository. Humans: see
[CONTRIBUTING.md](CONTRIBUTING.md).

hyperplot (`github.com/ironystock/hyperplot`) is two things. The product is
`hx-plot`, an htmx 4 extension (npm `htmx-ext-plot`, file
`client/hx-plot.js`, registered as `plot`) for charts that a server renders
as SVG. The Go module is its reference server: it renders the charts, and
its `hx` package speaks htmx (polling tags, SSE, sizes, gestures). Any
server language can implement the wire spec ([ADR 0005](docs/adr/0005-htmx-community-first.md)).

## The rule: the spec and DESIGN.md are the contract

- [docs/spec.md](docs/spec.md) is the **wire spec** (spec version 1): the
  markup a server sends, the `hx-plot:` attributes, the `hp-*` request
  parameters, the `htmx:plot:*` events, and what the extension does in a
  swap. Servers in other languages implement it from that document alone,
  and `testdata/conformance/` pins it to bytes.
- [docs/DESIGN.md](docs/DESIGN.md) is how this repository implements it:
  the Go API, layout, visual spec and palette, CSS, htmx facts, the
  reference client's behaviour, test standards and the roadmap.

Read both before changing code. **spec.md and DESIGN.md change together**:
if your change makes code and either document disagree, update the
document(s) in the same change and say why. A change to the wire (markup,
attributes, parameters, events, swap behaviour) also updates the fixtures
and needs a version decision (spec.md §2.3): additive changes keep spec 1,
anything a version-1 client would misread bumps it. Don't reorder the
palette, rename DOM classes, ids or `hp-*` names, or change the output
structure casually: the extension, the stylesheet, other servers and users'
code depend on them. Authors write `plot` (attributes, events, config);
servers read and write `hp` (classes, `data-hp*`, ids, parameters).

## Layout

| Path | Contents |
|---|---|
| `*.go` (root) | package `hyperplot`: `Chart`, marks (`marks.go`), data columns (`data.go`), render, theme/CSS, `Table`, `Sparkline` |
| `scale/` | public scales (`Linear`, `Time`, `Band`), ticks, number formatting. Done and tested |
| `wire.go` | the spec's number and time value forms, geometry attributes and row keys |
| `hx/` | htmx 4 server helpers: `Tag`, `CheckPTag`, SSE `Stream`, `Partial`, `Size`, and the gesture readers `Selected`, `Brushed` (`gesture.go`) |
| `client/` | the extension: `hx-plot.js` (plain ES2020, no runtime deps, no build), companions `hx-plot-brush.js` and `hx-plot-tween.js`, `hx-plot.css` (generated; see Commands), `package.json` (npm `htmx-ext-plot`), `build.mjs` (size check and minified build), `*.test.mjs` and `fakedom.mjs` (node tests), `browser/` (real-browser tests), and the Go package embedding the scripts |
| `internal/svg/` | deterministic SVG writer and path builder. Done, tested, fuzzed |
| `internal/textwidth/` | text width estimation (Helvetica/Arial advances) |
| `examples/dashboard/` | runnable live demo (`make demo`); also the end-to-end test of root, `hx` and `client` |
| `examples/basic/` | the README quick start; identical apart from its package comment (`docs/quickstart_test.go` checks) |
| `examples/sensors/`, `examples/builds/` | two more live Go demos, shaped like the dashboard (`-addr`, `-speed`, `main_test.go`) |
| `examples/gallery/` | every mark and variant beside the Go code that drew it (`charts.go`, embedded and parsed at startup); `-out DIR` writes static files |
| `examples/recipes/` | ten one-file Go recipes: live (polling-ptag, sse-stream, filters-form, select-drilldown, brush-zoom) and static (static-page, sizing, theming, sparkline-table, standalone-svg); the static ones' `preview/` SVGs are rewritten by `make golden`; `README.md` indexes them |
| `examples/python/`, `examples/node/` | Level 3 servers on Python's and Node's standard libraries (no dependencies, no `package.json`); not Go packages, so `make py` / `make nodeex` test them |
| `examples/README.md` | the index of every example (`docs/examples_test.go` fails when one is missing) |
| `tools/hpcheck/` | the language-neutral conformance checker (one standard-library Python file, `test_hpcheck.py`, `targets/dashboard.json`); run by `make conformance` |
| `testdata/` | golden SVG files; `testdata/preview/` holds standalone copies of some; `testdata/conformance/` holds the wire-spec fixtures (inputs, reference output, assertions) for servers in any language |
| `conformance_test.go`, `speccheck_test.go`, `specdom_test.go` | the fixtures' runner, the spec's universal MUSTs (`checkSpec`, also run by `FuzzRender` and on every golden), and the selector matcher |
| `docs/` | `spec.md` (wire spec), `DESIGN.md` (implementation contract, status, roadmap), `htmx.md` (integration guide), `implementing-a-server.md` (Python server walk-through), `adr/` (decision records), and tests that check the docs (Go samples compiled, Python run, `htmx-config` tags, links, the quick start) |

## Commands

Run Go commands from Git Bash on Windows. Each make target wraps one command.

```sh
make test     # go test ./..., then make client, make py, make nodeex and make conformance
make client   # cd client && node --test  (from the root, node --test finds nothing)
make py       # python3 -m unittest discover examples/python, then tools/hpcheck  (on Windows: make py PYTHON=python)
make nodeex   # cd examples/node && node --test
make conformance  # hpcheck on the Python and Node fixture output, the gallery export, and the dashboard, Python and Node servers
make race     # go test -race ./...
make lint     # hygiene, then gofumpt -l, go vet, staticcheck, golangci-lint run
make hygiene  # LF line endings, zero dependencies, go mod tidy -diff
make check    # what CI runs, on one machine: lint, race tests, goldens unchanged, benchmarks once, client, py, nodeex, conformance, browser (needs Chrome and npm ci in client/)
make fmt      # gofumpt -w .
make golden   # go test . -run Golden -update, then -run Preview -update on the examples with preview/  (then review the diff)
make fuzz     # every Fuzz* target, FUZZTIME=30s each
make bench    # benchmarks with -benchmem
make cover    # coverage.out + cover.html
make demo     # go run ./examples/dashboard
make browser  # cd client && npm run test:browser  (needs Chrome, Python and npm ci in client/; examples.browser.mjs opens every example)
```

Single package or test: `go test ./scale -run TestTicks -v`.
Lint config check: `golangci-lint config verify`.

- **Conformance:** `go test . -run Conformance -v` re-renders every fixture
  in `testdata/conformance/` and compares it byte for byte;
  `go test ./hx -run ConformanceParams` runs the `params.json` server cases;
  `cd client && node --test` includes `conformance.test.mjs`.
- **Stylesheet file:** `go test . -run StylesheetFile -update` writes
  `client/hx-plot.css` from `Stylesheet()`; never edit that file by hand.
- **Client size:** `cd client && npm run size` checks the minified and
  gzipped sizes against the budget in DESIGN.md (it runs terser through
  `npx`); `npm run build` writes the `.min.js` files, which are not
  committed. The core `hx-plot.js` is over budget for now, so `npm run
  size` fails; it is not part of `make check` or CI yet (DESIGN.md,
  "Size budget"). Don't grow the core without a reason.
- **Browser tests** (`client/browser/`) use `playwright-core` and an
  installed Chrome (`CHROME_PATH`); `HP_BROWSER_TMP` sets where Chrome
  profiles go. They build and run the dashboard, so they need Go too.

## Conventions

- **Standard library only.** Never add a `require` to `go.mod`; CI fails on
  any dependency. The client has no runtime dependencies; dev-only
  dependencies in `client/package.json` (`playwright-core`, `htmx.org`)
  serve the browser tests, and `node --test` needs none of them.
- **Go 1.24.** `go.mod` says `go 1.24`; don't use newer language or library
  features even if a newer toolchain is installed.
- **Format with `gofumpt -w`**, keep `go vet`, staticcheck and golangci-lint
  clean.
- **Godoc** on every exported identifier, starting with its name.
- **Errors** start with `hyperplot: `. `Render` returns errors; it never
  panics. Validation errors name the mark by index and name.
- **Deterministic output.** No map iteration when writing, no clock reads,
  coordinates through `svg.Num`, and values the spec compares as text
  (domains, row keys) through `wire.go`'s number and time value forms.
  Same input, same bytes.
- **Safety.** Escape all text (series names and categories are often
  user-controlled). No `style=""` attributes, no inline event handlers. The
  client inserts text with `textContent` only.
- **Concurrency.** `Render` must be safe to call concurrently and must not
  mutate the `Chart` or its slices. Tests run with `-race`.
- **Commits** follow Conventional Commits (`feat(hx): ...`). Agents don't
  commit, push or create branches; the maintainer does.

## Line endings: LF only

All files use LF. `.gitattributes` enforces it and CI rejects CRLF. On
Windows, create and edit files with the editor tools (Write/Edit), **never**
with shell redirection or Python text mode, which produce CRLF or stray `\r`.
Check with `git ls-files --eol` or `grep -l $'\r' <files>`.

## Golden files

Rendered charts are compared byte for byte with `testdata/*.svg`, and each
golden file is parsed with `encoding/xml` to check well-formedness.

1. Change the renderer intentionally.
2. `make golden` (that is, `go test . -run Golden -update`, then the same
   with `-run Preview` on each example package that has a `preview/`
   directory; `-update` exists only in those packages, so don't pass it to
   `./...`).
3. Review every changed file: `git diff --text testdata/ examples/` (diffs are
   collapsed by `.gitattributes`). Open changed SVGs in a browser if the
   change is visual.
4. Explain in the change summary why the output changed.

Never update goldens to make a failing test pass without understanding why
the output changed.

The conformance reference files in `testdata/conformance/` work the same way
(`go test . -run Conformance -update`), with one more rule: they are what
the spec promises other servers and the client tests, so a change to them
is a change to the wire and needs the spec change described above.

## Testing standards

Table-driven tests; `Example*` functions for public API; a well-formedness
fuzz test over chart inputs that also runs the spec checks; benchmarks
(1 line × 1,000 points, 8 series × 200 points, 50 bars; target well under
1 ms for typical charts). The client is tested with `node --test` in
`client/` (no dependencies; `dom.test.mjs`, `swap.test.mjs`,
`brush.test.mjs` and `tween.test.mjs` run the scripts against
`fakedom.mjs`, and `conformance.test.mjs` runs the fixtures' client
expectations). Behaviour only a real browser shows (scrolling, animation,
the morph itself) goes in `client/browser/`.

Go code blocks in `README.md`, `docs/htmx.md`, `client/README.md` and the
`hx` package doc are compiled by `docs/snippets_test.go`. A sample that
calls a new reader-supplied helper needs it added to the test's `prelude`
(and a new package name to `knownImports`). `docs/wire_test.go` runs the
Python of the spec's Appendix B and of `docs/implementing-a-server.md` on
edge cases and the `params.json` brush cases when Python 3.8+ is on PATH
(skipped otherwise, and under `-short`); nothing renders the guide's
charts automatically, so after changing that Python, also run the guide's
server against the extension in a browser. `docs/links_test.go` checks
every relative link and anchor in the Markdown files.

## Known gaps

The "Roadmap" section of DESIGN.md lists known gaps and planned work. Check
it before starting a feature, and update it when you close or find a gap.
