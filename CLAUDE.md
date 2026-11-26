# CLAUDE.md

Guidance for AI agents working in this repository. Humans: see
[CONTRIBUTING.md](CONTRIBUTING.md).

hyperplot (`github.com/ironystock/hyperplot`) renders charts as server-side
SVG in Go; htmx 4 keeps them live through `innerMorph` swaps, the `hx-sse`
extension and the `hx-ptag` extension.

## The one rule: docs/DESIGN.md is the contract

[docs/DESIGN.md](docs/DESIGN.md) defines the public API, the DOM output
structure, the layout, the visual spec and palette, the CSS, the htmx facts
and the test standards. Read it before changing code. If your change makes
code and DESIGN.md disagree, **update DESIGN.md in the same change** and say
why. Don't reorder the palette, rename DOM classes or ids, or change the
output structure casually: the client, the CSS and users' code depend on them.

## Layout

| Path | Contents |
|---|---|
| `*.go` (root) | package `hyperplot`: `Chart`, marks (`marks.go`), data columns (`data.go`), render, theme/CSS, `Table`, `Sparkline` |
| `scale/` | public scales (`Linear`, `Time`, `Band`), ticks, number formatting. Done and tested |
| `hx/` | htmx 4 server helpers: `Tag`, `CheckPTag`, SSE `Stream`, `Partial`, `Size` |
| `client/` | `hyperplot.js` (htmx 4 extension, plain ES2020, no deps, no build) + Go package embedding it |
| `internal/svg/` | deterministic SVG writer and path builder. Done, tested, fuzzed |
| `internal/textwidth/` | text width estimation (Helvetica/Arial advances) |
| `examples/dashboard/` | runnable live demo (`make demo`); also the end-to-end test of root, `hx` and `client` |
| `examples/basic/` | the README quick start; identical apart from its package comment (`docs/quickstart_test.go` checks) |
| `testdata/` | golden SVG files; `testdata/preview/` holds standalone copies of some |
| `docs/` | `DESIGN.md` (contract, status, roadmap), `htmx.md` (integration guide), `adr/` (decision records), and tests that compile and check the docs' Go samples |

## Commands

Run Go commands from Git Bash on Windows. Each make target wraps one command.

```sh
make test     # go test ./... && (cd client && node --test)
make client   # cd client && node --test  (from the root, node --test finds nothing)
make race     # go test -race ./...
make lint     # hygiene, then gofumpt -l, go vet, staticcheck, golangci-lint run
make hygiene  # LF line endings, zero dependencies, go mod tidy -diff
make check    # what CI runs, on one machine: lint, race tests, goldens unchanged, benchmarks once, client
make fmt      # gofumpt -w .
make golden   # go test . -run Golden -update  (then review the diff)
make fuzz     # every Fuzz* target, FUZZTIME=30s each
make bench    # benchmarks with -benchmem
make cover    # coverage.out + cover.html
make demo     # go run ./examples/dashboard
```

Single package or test: `go test ./scale -run TestTicks -v`.
Lint config check: `golangci-lint config verify`.

## Conventions

- **Standard library only.** Never add a `require` to `go.mod`; CI fails on
  any dependency. The client has no runtime dependencies.
- **Go 1.24.** `go.mod` says `go 1.24`; don't use newer language or library
  features even if a newer toolchain is installed.
- **Format with `gofumpt -w`**, keep `go vet`, staticcheck and golangci-lint
  clean.
- **Godoc** on every exported identifier, starting with its name.
- **Errors** start with `hyperplot: `. `Render` returns errors; it never
  panics. Validation errors name the mark by index and name.
- **Deterministic output.** No map iteration when writing, no clock reads,
  numbers through `svg.Num`. Same input, same bytes.
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
2. `make golden` (that is, `go test . -run Golden -update`; the `-update` flag
   exists only in the root package, so don't pass it to `./...`).
3. Review every changed file: `git diff --text testdata/` (diffs are
   collapsed by `.gitattributes`). Open changed SVGs in a browser if the
   change is visual.
4. Explain in the change summary why the output changed.

Never update goldens to make a failing test pass without understanding why
the output changed.

## Testing standards

Table-driven tests; `Example*` functions for public API; a well-formedness
fuzz test over chart inputs; benchmarks (1 line × 1,000 points, 8 series ×
200 points, 50 bars; target well under 1 ms for typical charts). Client
helpers and wiring are tested with `node --test` in `client/` (no
dependencies; `dom.test.mjs` runs the script against `fakedom.mjs`).

Go code blocks in `README.md`, `docs/htmx.md`, `client/README.md` and the
`hx` package doc are compiled by `docs/snippets_test.go`. A sample that
calls a new reader-supplied helper needs it added to the test's `prelude`.

## Known gaps

The "Roadmap" section of DESIGN.md lists known gaps and planned work. Check
it before starting a feature, and update it when you close or find a gap.
