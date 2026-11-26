# Architecture decision records

This directory records the significant decisions behind hyperplot, in the
[MADR](https://adr.github.io/madr/) format. Each record explains the context,
the options considered, the decision, and its consequences, so that future
contributors can see *why* the code is the way it is.

| ADR | Title | Status |
|---|---|---|
| [0001](0001-server-rendered-svg.md) | Render charts as SVG on the server | Accepted |
| [0002](0002-go-zero-dependencies.md) | Go with zero dependencies | Accepted |
| [0003](0003-htmx4-morph-live-updates.md) | Live updates through htmx 4 morph swaps | Accepted |
| [0004](0004-css-theming-and-graceful-degradation.md) | CSS theming with graceful degradation | Accepted |
| [0005](0005-htmx-community-first.md) | The htmx extension is the product; Go is its reference server | Proposed |

## Adding a record

1. Copy the most recent record and give it the next number:
   `NNNN-short-title-with-dashes.md`.
2. Fill in every section. Keep it short; link to [DESIGN.md](../DESIGN.md) for
   details rather than repeating them.
3. Propose it in a pull request with status `proposed`. It becomes `accepted`
   when merged.
4. Don't rewrite accepted records. To change a decision, write a new record
   and mark the old one `superseded by ADR-NNNN`.
