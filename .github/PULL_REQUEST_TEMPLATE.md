<!--
Thanks for contributing to hyperplot! Please read CONTRIBUTING.md first.
Title format (Conventional Commits): feat(bar): round negative bar ends
-->

## What and why

<!-- What does this change do, and why? Link the issue: "Fixes #123". -->

## How it was tested

<!-- Commands you ran and what you checked by eye (browser, dark mode, keyboard, screen reader). -->

## Checklist

- [ ] `make check` passes locally (it runs lint, the race tests and the client tests).
- [ ] New behavior has tests; rendering changes have updated golden files (`make golden`) that I reviewed.
- [ ] Exported identifiers have godoc that starts with their name.
- [ ] If this changes the public API, DOM structure, CSS, or visual defaults, `docs/DESIGN.md` is updated in this PR.
- [ ] User-visible changes are noted under "Unreleased" in `CHANGELOG.md`.
- [ ] No new dependencies (the core is standard library only).
