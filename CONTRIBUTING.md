# Contributing to hyperplot

Thanks for your interest in hyperplot! Bug reports, documentation fixes, tests
and features are all welcome. This guide explains how to set up the project,
what we expect from a change, and how a pull request gets merged.

The repository holds three things that move together: the `hx-plot` htmx
extension in [`client/`](client/) (npm `htmx-ext-plot`), the
[wire spec](docs/spec.md) it shares with servers in any language, and the Go
library that is the spec's reference server. If you implement the spec in
another language, reports of anything unclear or wrong in it are among the
most useful contributions.

By participating you agree to follow the [code of conduct](CODE_OF_CONDUCT.md).

## Before you start

- **Small fixes** (typos, docs, an obvious bug with a test): open a pull
  request directly.
- **Anything larger** (a new option, mark, attribute, event, or change to
  output): open an issue first so we can agree on the approach before you
  invest time. hyperplot keeps a small API with strong defaults, and some
  features are out of scope on purpose (see the non-goals in
  [docs/DESIGN.md](docs/DESIGN.md)). Anything that changes the wire (markup,
  attributes, request parameters, events, swap behaviour) is a spec change;
  see [the spec and design document rule](#the-spec-and-design-document-rule).
- **Security issues**: don't open a public issue. Follow
  [SECURITY.md](SECURITY.md).

## Setup

You need:

- **Go 1.24 or later.** CI tests on 1.24.x and the latest stable release.
- **Node.js 22 or later**, to run the client tests (`node --test`). The
  client has no runtime dependencies, and its unit tests need nothing
  installed. The real-browser tests (`make browser`) also need Chrome and
  the dev dependencies (`npm ci` in `client/`), and `npm run size` fetches
  terser through `npx`. The Node example (`examples/node`) needs Node 22
  or later and nothing installed.
- **Python 3.10 or later**, standard library only, for the Python example
  and the conformance checker (`make py`, `make conformance`). Make calls
  `python3`; where the interpreter is `python` (often on Windows), run
  `make test PYTHON=python`.
- **Tools** used by `make lint` and the git hooks:

  ```sh
  go install mvdan.cc/gofumpt@latest
  go install honnef.co/go/tools/cmd/staticcheck@2026.2.2   # the version CI pins
  # golangci-lint v2: see https://golangci-lint.run/docs/welcome/install/
  go install github.com/evilmartians/lefthook@latest   # optional git hooks
  ```

Then clone and check that everything passes:

```sh
git clone https://github.com/ironystock/hyperplot
cd hyperplot
lefthook install   # optional: gofumpt and go vet before each commit
(cd client && npm ci)   # dev-only deps for the browser tests that make check runs last
make check
```

`make check` ends with the real-browser tests, which drive an installed
Chrome or Chromium. If yours isn't in the usual install path, set
`CHROME_PATH` to the binary. Without Chrome, run `make lint race client`
instead, and let CI run the browser tests.

On Windows, run `make` from Git Bash (or run the underlying commands
directly; each target is a one-line wrapper).

## Make targets

| Target | What it does |
|---|---|
| `make test` | `go test ./...`, then `client`, `py`, `nodeex` and `conformance` |
| `make client` | The client tests: `node --test`, run inside `client/` |
| `make py` | The Python example's and the checker's tests (`unittest`) |
| `make nodeex` | The Node example's tests: `node --test`, run inside `examples/node/` |
| `make conformance` | `tools/hpcheck` on the Python and Node fixture output, on the gallery's static export, and on the dashboard, Python and Node servers (each started on a free port and stopped) |
| `make browser` | The real-browser tests in `client/browser/`, including one that opens every example (needs Chrome, Python and `npm ci` in `client/`) |
| `make race` | `go test -race ./...` |
| `make hygiene` | LF line endings only, zero dependencies, `go mod tidy -diff` |
| `make lint` | `hygiene`, gofumpt check, `go vet`, `staticcheck`, `golangci-lint run` |
| `make fmt` | `gofumpt -w .` |
| `make fuzz` | Runs every `Fuzz*` target for `FUZZTIME` (default 30s each) |
| `make golden` | Regenerates golden SVG files in `testdata/` and the examples' `preview/` SVGs |
| `make bench` | Benchmarks with `-benchmem` |
| `make cover` | Coverage profile plus `cover.html` |
| `make demo` | Runs the live dashboard example on http://localhost:8080 |
| `make check` | `lint`, `race` (failing if tests touch `testdata/`), one run of every benchmark, `client`, `py`, `nodeex`, `conformance` and the browser tests (needs Chrome and `npm ci` in `client/`): the CI checks on one OS and one Go version, minus fuzzing |

`make check` can't reproduce CI's matrix (Linux, Windows and macOS, with Go
1.24.x and stable). If you use language or library features, build with Go
1.24 too, for example `GOTOOLCHAIN=go1.24.0 go test ./...`.

The Go code blocks in `README.md`, `docs/htmx.md`, `client/README.md` and the
`hx` package documentation are compiled against the current API by
`go test ./docs` (skipped with `-short`), so update the docs in the same
change as the API.

## Code standards

- **Standard library only.** The Go packages must not import anything outside
  the standard library, and `go.mod` has no `require` lines. CI enforces this.
  The client has no runtime dependencies and no build step.
- **Format with gofumpt** and keep `go vet`, `staticcheck` and `golangci-lint`
  clean. `make fmt` fixes formatting.
- **Godoc on every exported identifier**, starting with its name:
  `// Render returns ...`.
- **Errors** are prefixed with `hyperplot: ` and name the offending mark by
  index and name when validating input. `Render` returns errors; it never
  panics on bad input.
- **Determinism.** The same input must give byte-identical output. Don't
  iterate over maps when writing output, don't read the clock, and format
  numbers through the SVG writer.
- **Safety.** Escape every piece of text that reaches the SVG; never add
  `style=""` attributes or inline event handlers; in the client, insert text
  with `textContent` only.
- **LF line endings** everywhere. `.gitattributes` enforces this on commit,
  and CI rejects CRLF. If your editor writes CRLF, set it to LF (the
  `.editorconfig` does this for most editors).
- **Go version.** The module supports Go 1.24, so don't use language or
  library features from newer releases.

## Tests

- Write table-driven tests, and keep them passing under `-race`.
- Add an `Example*` function when you add or change public API; examples are
  the documentation people read first.
- Rendering changes need a golden test (see below). Logic changes (scales,
  formatting, validation) need unit tests.
- Add fuzz seeds for inputs that broke something.
- Client changes need a `node --test` test in `client/`: `hx-plot.test.mjs`
  for the pure helpers; `dom.test.mjs`, `swap.test.mjs`, `brush.test.mjs` or
  `tween.test.mjs` (against the fake browser in `fakedom.mjs`) for the DOM
  and htmx wiring; `conformance.test.mjs` when a fixture's `client`
  expectations change. Keep the scripts within their size budget
  (`npm run size` in `client/`). The core `hx-plot.js` is over its budget
  for now (see "Size budget" in [DESIGN.md](docs/DESIGN.md#size-budget)),
  so the check fails; a change should not make it larger without a reason.
- Behaviour that only a real browser shows (scrolling, animation, the morph
  itself, pointer capture) belongs in `client/browser/` too.

### Browser tests

`client/browser/*.browser.mjs` drive an installed Chrome through
`playwright-core`, a dev-only dependency that downloads no browser. Most of
them build and run the example dashboard, so they need Go as well.
`examples.browser.mjs` opens every example in turn, so it also needs
Python 3.10+ (set `PYTHON` when it isn't `python3`, or `python` on
Windows) and Node; when you add an example, add it to that file's list
(`go test ./docs` fails until you do).

```sh
cd client
npm ci
npm run test:browser   # or, from the repository root: make browser
```

Set `CHROME_PATH` when Chrome isn't at its usual install path, and
`HP_BROWSER_TMP` to choose where the temporary Chrome profiles go;
`client/browser/harness.mjs` lists the other settings. The tests need no
network once `npm ci` has run: they serve htmx from `node_modules`. Every
test closes its browser and server, also on failure; if you write a new
one, use the harness so that stays true.

### Conformance fixtures

`testdata/conformance/` pins the wire spec to bytes (format in its
[README](testdata/conformance/README.md)). `go test . -run Conformance`
re-renders each fixture with the Go renderer and runs every assertion;
`node --test` in `client/` runs the client expectations. A change to a
reference file is a change to what the spec promises other servers, so
regenerate them only together with a spec change
(`go test . -run Conformance -update`) and review the diff like a golden
file.

### Golden files

Rendered charts are compared byte for byte against SVG files in `testdata/`.
Each golden file is also parsed with `encoding/xml` to check it is
well-formed. When you intentionally change the output:

1. Make your change.
2. Regenerate: `make golden` (which runs `go test . -run Golden -update`,
   then rewrites the `preview/` SVGs of the examples that have them).
3. **Review the diff** of every changed file: `git diff --text testdata/ examples/`.
   Golden diffs are collapsed on GitHub by `.gitattributes`, so reviewers rely
   on you to have looked. Open the changed SVGs in a browser, in light and dark
   mode, and check them by eye.
4. Commit the regenerated files together with the code that changed them, and
   say in the pull request description why the output changed.

CI fails if running the tests modifies anything under `testdata/`.

## The spec and design document rule

Two documents are the contract:

- [docs/spec.md](docs/spec.md), the **wire spec**: the markup a server
  sends, the attributes page authors write, the request parameters, the
  events and the swap behaviour. Servers in other languages are built from
  it, so it must be right on its own.
- [docs/DESIGN.md](docs/DESIGN.md), the **implementation contract**: the Go
  API, the DOM output in detail, the CSS, the visual defaults, the htmx
  facts and how the reference client behaves. The extension, the
  stylesheet and users' own CSS depend on it.

**spec.md and DESIGN.md change together.** If a change makes the code
disagree with either, update the document in the same pull request, and
explain the change in the description. A change to the wire also needs:

- the conformance fixtures updated to match;
- a version decision (spec §2.3): a change a version-1 client can ignore (a
  new optional attribute or JSON field) keeps spec 1; anything a version-1
  client would misread bumps the version;
- a line in the changelog, since servers in other languages will have to
  follow it.

Significant architectural decisions also get an [ADR](docs/adr/README.md)
(copy the format of an existing one and number it sequentially).

## Commit messages

We use [Conventional Commits](https://www.conventionalcommits.org/en/v1.0.0/):

```
<type>(<optional scope>): <summary in the imperative, lower case>

<optional body: what and why, wrapped at 72 columns>

<optional footer: Fixes #123, BREAKING CHANGE: ...>
```

Types: `feat`, `fix`, `docs`, `test`, `refactor`, `perf`, `build`, `ci`,
`chore`. Scopes are usually a package or area: `spec`, `client`, `scale`,
`hx`, `bar`, `theme`, `a11y`, `examples`.

Examples:

```
feat(hx): add Stream.Retry to set the SSE reconnect delay
fix(scale): keep weekly time ticks on Sundays across DST changes
docs(htmx): explain why error responses replace the chart
```

Mark breaking API changes with `!` (`feat(hx)!: ...`) and a
`BREAKING CHANGE:` footer. Before v1.0.0 these are allowed but must be called
out in [CHANGELOG.md](CHANGELOG.md).

## Pull requests

1. Fork the repository and create a branch from `main`.
2. Make focused commits. One logical change per pull request is easiest to
   review.
3. Run `make check` (and `make golden` if output changed).
4. Add a line under **Unreleased** in [CHANGELOG.md](CHANGELOG.md) for any
   user-visible change.
5. Open the pull request and fill in the template.

There is **no CLA and no DCO sign-off**: by opening a pull request you agree
that your contribution is licensed under the project's [MIT license](LICENSE).

A maintainer reviews every pull request. CI must be green before merging. We
usually squash-merge, using the pull request title as the commit message, so
please give it a Conventional Commits title. Expect review comments; they are
about the code, not about you.

## Questions

Open a [discussion](https://github.com/ironystock/hyperplot/discussions) or an
issue. Thanks for helping make hyperplot better!
