---
status: proposed
date: 2026-11-26
decision-makers: hyperplot maintainers
---

# Lay the repository out as a monorepo; the Go library moves to plot/

## Context and Problem Statement

After [ADR 0005](0005-htmx-community-first.md), the repository holds
several products: the htmx extension (`client/`), a language-neutral wire
spec and its fixtures and checker, the Go reference server, and example
servers in Go, Python and Node. The Go library still lived in the
repository root: 19 source files and 14 test files beside the README. Go
requires a package's tests to sit beside its code, so the root could only
be tidied by moving the package. The root looked like a Go project with
some extras, which contradicts ADR 0005: the extension and the spec are the
product.

## Decision Drivers

* Every product gets its own top-level directory, so a reader of any
  language finds their part.
* The spec, its fixtures and its checker belong together, outside the Go
  library.
* Nothing has been tagged or published, so a breaking import path costs
  nothing now and a lot later.
* The import path should read well and avoid stutter.

## Considered Options

1. **Move the package to `plot/`** (package `plot`, import
   `github.com/ironystock/hyperplot/plot`). The module root keeps only
   `go.mod`.
2. Keep the package at the root and tidy around it. The root would still
   hold about 27 `.go` files.
3. Put a separate Go module in `go/`. Its path,
   `github.com/ironystock/hyperplot/go`, ends in a name that differs from
   its package name, which Go tooling and readers find odd.

## Decision Outcome

Chosen: **option 1**. The layout is now:

| Path | Contents |
|---|---|
| `client/` | the htmx extension (npm `htmx-ext-plot`) |
| `spec/` | `spec.md`, `conformance/` fixtures, `hpcheck/` checker |
| `plot/` | the Go reference server (`plot/testdata/`: goldens) |
| `hx/`, `scale/`, `internal/` | Go helpers, unchanged |
| `examples/` | Go, Python and Node examples |
| `docs/` | DESIGN.md, guides, ADRs, brand |
| `.github/` | CI, CONTRIBUTING, CODE_OF_CONDUCT, SECURITY, GOVERNANCE |

Go code now writes `plot.Chart{...}` and `plot.Stylesheet()`. The module
path, `go.mod` and the `hx`, `scale` and `client` packages are unchanged.

### Consequences

* Good: the root holds only `go.mod`, the README and project files, and the
  spec stands as a first-class part of the repository.
* Good: a package named `plot` reads naturally at call sites.
* Bad: every importer of the old root package must change its import path
  and its `hyperplot.` qualifiers. Nothing outside this repository depended
  on them yet.
* Bad: `spec/spec.md` and `spec/conformance/` paths in older notes and
  commits point to their old homes, `docs/spec.md` and
  `testdata/conformance/`.
