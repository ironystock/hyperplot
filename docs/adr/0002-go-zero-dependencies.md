---
status: accepted
date: 2026-11-25
decision-makers: hyperplot maintainers
---

# Go with zero dependencies

## Context and Problem Statement

hyperplot is a library: everything it imports, its users import too. Charting
touches XML writing, HTML escaping, JSON, number formatting, time zones and
HTTP, and there are third-party packages for each. Every dependency adds
supply-chain risk, version conflicts, `go.sum` churn and review load for the
people who adopt the library.

What may hyperplot depend on?

## Decision Drivers

* Safe to adopt in security-sensitive and regulated environments.
* No version conflicts with users' own dependency graphs.
* A long support window with little maintenance.
* The standard library already covers what is needed: `encoding/xml` for
  verification, `encoding/json`, `html/template`, `hash/fnv`, `math`, `time`,
  `net/http` and `embed`.

## Considered Options

* Standard library only, for the Go packages and the JavaScript client
* Allow small, well-known dependencies (for example a templ runtime or an
  SVG builder)
* Split integrations into separate modules with their own dependencies

## Decision Outcome

Chosen option: "Standard library only", because every capability hyperplot
needs is in the standard library, and an empty dependency graph is a feature
users can verify in one second.

The root package, `scale`, `hx`, `client` and everything under `internal/`
import only the standard library, and `go.mod` has no `require` lines. The
JavaScript client has no runtime dependencies and no build step. Integrations
that would need a dependency are made structural instead: `Chart.Component()`
returns a type whose method set matches `templ.Component`, so templ users can
use it directly without hyperplot importing templ.

The module supports Go 1.24 and later, so code must not use language or
library features from newer releases.

### Consequences

* Good, because `go get` adds exactly one module, with nothing to audit
  beyond it.
* Good, because there are no transitive version conflicts and no dependency
  update churn.
* Good, because the client can be served straight from the Go binary through
  `//go:embed`, with no npm install.
* Bad, because hyperplot maintains its own SVG writer, path builder, scales,
  tick generation, number formatting and text-width estimation. These are
  small, but they are code we own and must test (they are covered by unit and
  fuzz tests).
* Bad, because some conveniences (a templ import, a YAML theme file, a
  third-party color library) are off the table or must be done structurally.
* Neutral, because development tools (gofumpt, staticcheck, golangci-lint,
  lefthook) are not module dependencies and don't affect users.

### Confirmation

CI fails if `go list -m all` lists any module besides hyperplot, and runs
`go mod tidy -diff`. Reviewers reject imports outside the standard library.
CI tests on Go 1.24.x and on the latest stable release.

## Pros and Cons of the Options

### Standard library only

* Good, because it has the smallest attack surface and no conflicts.
* Bad, because we write and maintain some utility code ourselves.

### Allow small, well-known dependencies

* Good, because less code to write.
* Bad, because each one is a permanent cost for every user, and "small"
  dependencies grow their own graphs.

### Split integrations into separate modules

* Good, because the core stays clean while integrations can import what they
  need.
* Bad, because multi-module repositories complicate releases and versioning.
  This remains an option for a future integration that truly needs a
  dependency, recorded in a new ADR.

## More Information

* [DESIGN.md](../DESIGN.md), goal 6 ("Zero dependencies") and the module
  layout.
