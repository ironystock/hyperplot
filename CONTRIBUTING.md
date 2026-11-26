# Contributing to hyperplot

Thanks for your interest in hyperplot! Bug reports, documentation fixes, tests
and features are all welcome. This guide explains how to set up the project,
what we expect from a change, and how a pull request gets merged.

By participating you agree to follow the [code of conduct](CODE_OF_CONDUCT.md).

## Before you start

- **Small fixes** (typos, docs, an obvious bug with a test): open a pull
  request directly.
- **Anything larger** (a new option, mark, or change to output): open an issue
  first so we can agree on the approach before you invest time. hyperplot
  keeps a small API with strong defaults, and some features are out of scope
  on purpose (see the non-goals in [docs/DESIGN.md](docs/DESIGN.md)).
- **Security issues**: don't open a public issue. Follow
  [SECURITY.md](SECURITY.md).

## Setup

You need:

- **Go 1.24 or later.** CI tests on 1.24.x and the latest stable release.
- **Node.js 22 or later**, only to run the client tests (`node --test`). The
  client has no npm dependencies, so there is nothing to install.
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
make check
```

On Windows, run `make` from Git Bash (or run the underlying commands
directly; each target is a one-line wrapper).

## Make targets

| Target | What it does |
|---|---|
| `make test` | `go test ./...` and the client tests |
| `make client` | The client tests: `node --test`, run inside `client/` |
| `make race` | `go test -race ./...` |
| `make hygiene` | LF line endings only, zero dependencies, `go mod tidy -diff` |
| `make lint` | `hygiene`, gofumpt check, `go vet`, `staticcheck`, `golangci-lint run` |
| `make fmt` | `gofumpt -w .` |
| `make fuzz` | Runs every `Fuzz*` target for `FUZZTIME` (default 30s each) |
| `make golden` | Regenerates golden SVG files in `testdata/` |
| `make bench` | Benchmarks with `-benchmem` |
| `make cover` | Coverage profile plus `cover.html` |
| `make demo` | Runs the live dashboard example on http://localhost:8080 |
| `make check` | `lint`, `race` (failing if tests touch `testdata/`), one run of every benchmark and the client tests: the CI checks on one OS and one Go version, minus fuzzing |

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
- Client changes need a `node --test` test in `client/`: `hyperplot.test.mjs`
  for the pure helpers, `dom.test.mjs` (against the fake browser in
  `fakedom.mjs`) for the DOM and htmx wiring.

### Golden files

Rendered charts are compared byte for byte against SVG files in `testdata/`.
Each golden file is also parsed with `encoding/xml` to check it is
well-formed. When you intentionally change the output:

1. Make your change.
2. Regenerate: `make golden` (which runs `go test . -run Golden -update`).
3. **Review the diff** of every changed file: `git diff --text testdata/`.
   Golden diffs are collapsed on GitHub by `.gitattributes`, so reviewers rely
   on you to have looked. Open the changed SVGs in a browser, in light and dark
   mode, and check them by eye.
4. Commit the regenerated files together with the code that changed them, and
   say in the pull request description why the output changed.

CI fails if running the tests modifies anything under `testdata/`.

## The design document rule

[docs/DESIGN.md](docs/DESIGN.md) is the contract for the public API, the DOM
output structure, the CSS, the visual defaults and the htmx integration. The
client, the stylesheet and users' own CSS depend on it.

**If a change makes the code and DESIGN.md disagree, update DESIGN.md in the
same pull request**, and explain the change in the description. Significant
architectural decisions also get an [ADR](docs/adr/) (copy the format of an
existing one and number it sequentially).

## Commit messages

We use [Conventional Commits](https://www.conventionalcommits.org/en/v1.0.0/):

```
<type>(<optional scope>): <summary in the imperative, lower case>

<optional body: what and why, wrapped at 72 columns>

<optional footer: Fixes #123, BREAKING CHANGE: ...>
```

Types: `feat`, `fix`, `docs`, `test`, `refactor`, `perf`, `build`, `ci`,
`chore`. Scopes are usually a package or area: `scale`, `hx`, `client`,
`bar`, `theme`, `a11y`, `examples`.

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
