# Governance

hyperplot is a young project with a small team, and its governance is kept
deliberately simple. This document will change as the community grows.

## Roles

- **Project lead.** The project currently follows a "benevolent dictator"
  model: the project lead ([@ironystock](https://github.com/ironystock)) sets
  the direction, has final say on design decisions, and is the default
  reviewer for every change (see [CODEOWNERS](CODEOWNERS)).
- **Maintainers** can review and merge pull requests, triage issues, and cut
  releases. Today the project lead is the only maintainer.
- **Contributors** are everyone who files an issue, reviews a pull request,
  improves documentation, or contributes code. Thank you.

## How decisions are made

1. Most decisions happen in issues and pull requests, by rough consensus
   among the people involved.
2. Changes to the public API, the DOM output structure, the CSS contract or
   the visual defaults must update [docs/DESIGN.md](../docs/DESIGN.md) in the same
   pull request. Changes to the wire between servers and the `hx-plot`
   extension (markup, attributes, request parameters, events, swap
   behaviour) must also update the [wire spec](../spec/spec.md) and its
   conformance fixtures, and say whether they change the spec version.
   Because servers in other languages implement the spec, such changes are
   proposed in an issue first and get time for comment.
3. Significant or hard-to-reverse decisions are recorded as
   [architecture decision records](../docs/adr/), proposed in a pull request so
   anyone can comment before they are accepted.
4. When consensus isn't reached, the project lead decides and records the
   reasoning in the issue, pull request or ADR.

## Becoming a maintainer

Contributors who have made sustained, high-quality contributions (code,
reviews, triage or documentation) and who show good judgment about the
project's goals may be invited to become maintainers by the project lead.
Maintainers who are inactive for a long period may be moved to emeritus
status, with thanks.

## Releases

Releases follow [Semantic Versioning](https://semver.org/). Before v1.0.0,
minor versions may contain breaking changes, which are called out in
[CHANGELOG.md](../CHANGELOG.md). Maintainers tag releases from `main` after CI
passes. The Go module (tags `vX.Y.Z`) and the npm package `htmx-ext-plot`
are versioned separately; the wire spec has its own version number, carried
in every chart's `data-hp` attribute.

## Code of conduct

Everyone participating in the project is expected to follow the
[code of conduct](CODE_OF_CONDUCT.md). The project lead is responsible for
enforcing it.

## Changes to this document

Changes to governance are proposed by pull request and approved by the
project lead.
