---
status: accepted
date: 2026-11-25
decision-makers: hyperplot maintainers
---

# CSS theming with graceful degradation

## Context and Problem Statement

A chart needs colors for its surface, text, gridlines and up to eight series,
in light and dark mode. Where those colors live decides how charts are
themed, whether dark mode can follow the user's preference without
re-rendering, whether output works under a strict Content Security Policy,
and what a chart looks like when the stylesheet fails to load (in an RSS
reader, an email client, a saved file, or a page that forgot the `<link>`).

How should hyperplot apply colors and styles?

## Decision Drivers

* Dark mode that follows `prefers-color-scheme` or an explicit page setting,
  without a server round trip.
* Strict CSP compatibility: no `style=""` attributes and no inline
  `<style>` in pages.
* Readable output when no stylesheet is present.
* Output that is identical regardless of theme, so content tags and golden
  tests stay meaningful.
* A carefully chosen palette: fixed order for colorblind safety, and a dark
  palette selected for dark surfaces rather than inverted.
* Easy customization by users, with plain CSS.

## Considered Options

* Classes plus an external stylesheet of CSS custom properties, with
  `currentColor` presentation attributes as a fallback
* Inline `fill` and `stroke` colors in the SVG
* `style=""` attributes or an inline `<style>` in every chart

## Decision Outcome

Chosen option: "Classes plus an external stylesheet, with `currentColor`
fallbacks", because it is the only option that gives live dark mode, strict
CSP compatibility and theme-independent output at the same time.

* Marks carry presentation attributes written in terms of `currentColor`, for
  example `fill="none" stroke="currentColor" stroke-width="2"` for lines and
  `stroke-opacity="0.12"` for gridlines. With no stylesheet, the chart
  renders in the surrounding text color, in legible monochrome.
* Series groups carry `hp-s1` … `hp-s8`; the stylesheet sets
  `.hp-sN { color: var(--hp-series-N) }`, so every mark in the group follows
  its series color through `currentColor`.
* `Theme.CSS()` (and `Stylesheet()` for the default theme) declares custom
  properties on `.hp, .hp-tip, .hp-table`, and a separate dark palette under
  `@media (prefers-color-scheme: dark)` and `:root[data-theme="dark"]`, with
  `data-theme="light"` to opt out.
* The stylesheet also handles `prefers-reduced-motion`, `forced-colors`, the
  tooltip card and the data table.
* `Chart.Standalone` is the exception for self-contained files: it embeds the
  theme's `<style>` and draws a background, because a file has no page
  stylesheet.

### Consequences

* Good, because dark mode switches instantly with the system or page setting,
  with no re-render.
* Good, because pages can use a strict CSP (`style-src 'self'`) and serve the
  stylesheet as a file.
* Good, because the SVG bytes don't depend on the theme, so `hx.Tag` and
  golden tests are stable across themes.
* Good, because users can retheme with a few CSS custom properties, or with
  their own `Theme`.
* Good, because a chart without CSS is still readable, which also helps in
  forced-colors mode.
* Bad, because a page must include the stylesheet to get colors; a forgotten
  `<link>` produces a monochrome chart rather than an error. The docs make
  this prominent.
* Bad, because CSS-dependent output can't be colored correctly by tools that
  ignore CSS (some SVG-to-PDF converters); `Standalone` covers file export.
* Neutral, because `Standalone` output contains a `<style>` element and so
  needs `style-src 'unsafe-inline'` (or a hash) if embedded in a page; it is
  intended for files, not pages.

### Confirmation

The renderer's tests assert that output contains no `style=""` attributes,
and golden files make any change to presentation attributes visible in
review. The palette tokens and their order are specified in
[DESIGN.md](../DESIGN.md#default-theme-tokens-validated-palette), and
`Theme.CSS()` is tested against them. Changes to output are reviewed by eye
with and without the stylesheet, in light, dark and forced-colors modes.

## Pros and Cons of the Options

### Classes, an external stylesheet and `currentColor` fallbacks

* Good, because live theming, CSP compatibility and graceful degradation.
* Bad, because charts need the stylesheet to show color.

### Inline `fill` and `stroke` colors

* Good, because colors work without any stylesheet.
* Bad, because dark mode needs a re-render per theme, the output depends on
  the theme, and user CSS has to fight attribute values.

### `style=""` attributes or an inline `<style>` per chart

* Good, because self-contained.
* Bad, because it is blocked by strict CSP, duplicates the stylesheet in
  every chart, and bloats live updates.

## More Information

* [DESIGN.md](../DESIGN.md#css-themecss): the CSS contract.
* [docs/htmx.md](../htmx.md#content-security-policy): CSP notes.
