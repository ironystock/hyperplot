---
status: accepted
date: 2026-11-25
decision-makers: hyperplot maintainers
---

# Live updates through htmx 4 morph swaps

## Context and Problem Statement

Dashboards need charts that update: on a timer, when the server pushes new
data, or when a user changes a filter. With server-rendered SVG
([ADR 0001](0001-server-rendered-svg.md)), an update means replacing the
chart with a newly rendered one. A plain `innerHTML` swap replaces every
node: the chart flickers, transitions can't run, and hover state and focus
are lost.

How should server-rendered charts stay live?

## Decision Drivers

* No client-side chart state and no JavaScript framework.
* Smooth, animated transitions between states.
* Keep focus, hover and tooltips stable across updates.
* Cheap when nothing changed.
* Use standard web primitives: HTTP polling, server-sent events, forms.

## Considered Options

* htmx 4 with `innerMorph` swaps, the `hx-sse` and `hx-ptag` extensions, and
  a small hyperplot extension
* htmx 4 with plain `innerHTML` swaps
* A custom client that fetches JSON and patches the SVG
* WebSockets with a custom diffing protocol

## Decision Outcome

Chosen option: "htmx 4 with `innerMorph` swaps", because htmx 4 ships morphing
(idiomorph) in core, which preserves node identity, so CSS transitions can
animate attribute changes between two server-rendered charts.

This works only if the renderer cooperates, so hyperplot commits to:

* **Determinism.** The same input gives byte-identical output.
* **Stable ids.** The SVG root has a stable `id`; each series group is
  `{ID}-s-{key}` and each bar `{ID}-b-{key}-{catkey}`, where the keys are
  FNV-1a hashes of the series name and category. Idiomorph matches nodes by
  id, so a bar morphs to its new height instead of being replaced.
* **Animatable geometry.** Bars and areas are `<path>` elements whose `d`
  attribute transitions in CSS (Chromium and Firefox; Safari snaps, which is
  acceptable). `Chart.Animate` adds the `hp-anim` class that enables the
  transitions, guarded by `prefers-reduced-motion`.

The `hx` package provides the server side of each live pattern: content tags
(`Tag`, `CheckPTag`) so an unchanged poll costs one `304`, an SSE `Stream`
that is safe against field injection, `Partial` for multi-target responses,
and `Size` for responsive rendering. The client extension re-applies hover
and focus state after each swap and SSE message.

### Consequences

* Good, because live charts need only HTML attributes and a few lines of Go
  per handler.
* Good, because transitions are smooth and identity-preserving, and tooltips
  don't flicker during polling.
* Good, because `304` responses make idle polling nearly free, and SSE gives
  push updates with replay through `Last-Event-ID`.
* Bad, because hyperplot is coupled to htmx 4 behavior (morph semantics,
  event names, extension hooks). htmx 4 is a new major version; changes in it
  may require changes here. The `hx` helpers and the client are kept thin to
  limit this.
* Bad, because determinism and stable ids are now part of the public
  contract: a change to id generation breaks morph matching and users' CSS.
  The DOM structure in [DESIGN.md](../DESIGN.md) changes only deliberately.
* Neutral, because charts remain plain SVG: users of other hypermedia tools
  (or none) can still swap them however they like; they just lose the
  morph-specific benefits.

### Confirmation

Golden-file tests pin the output including ids. Unit tests cover the `hx`
helpers (`304` behavior, SSE framing and injection resistance). The example
dashboard exercises every pattern in a browser.

## Pros and Cons of the Options

### htmx 4 morph swaps

* Good, because morphing is in htmx core, and the extensions cover ptag and
  SSE.
* Bad, because it ties the live features to one front-end library.

### htmx 4 with `innerHTML` swaps

* Good, because it is the simplest.
* Bad, because every update replaces the nodes: flicker, no transitions, lost
  hover state.

### A custom JSON-patching client

* Good, because the smallest payloads.
* Bad, because it reintroduces client-side rendering logic, which
  [ADR 0001](0001-server-rendered-svg.md) rejected.

### WebSockets with a diffing protocol

* Good, because bidirectional and low latency.
* Bad, because it needs a custom protocol and reconnection logic; SSE plus
  morphing covers the server-push case with standard tools.

## More Information

* [docs/htmx.md](../htmx.md): the integration guide for every pattern.
* [DESIGN.md](../DESIGN.md#htmx-4-integration-package-hx): verified htmx 4
  facts and the `hx` API.
