# hyperplot brand: "Trace"

hyperplot's identity is an oscilloscope. A phosphor-yellow trace runs across
a dark glass screen, and the trace forms a lowercase **h**. hyperplot is
No. 001 of the open-source htmx projects at [vibehyper.media](https://vibehyper.media),
and the red spot at the end of the trace is the family tie to vibehyper.

## Rules

- **Red lives only in the mark.** The spot is Signal red `#db0018`. Red is
  never data: no series, rule, link or focus ring uses it, because a red
  series reads as an alarm.
- **Slot 1 is the brand color.** It is the phosphor `#fae431` on dark
  grounds and `#a88d00` on light ones. The phosphor is only 1.2:1 on paper,
  so it never goes on a light ground.
- **On light grounds the glass carries the brand.** Text, links and focus
  use ink `#101818`, not gold.
- **Don't recolor the mark.** Keep clear space of one spot diameter around
  it.

## Charts in the brand colors

The library ships the palette as an opt-in theme. The default theme is
unchanged.

```go
css := hyperplot.TraceTheme().CSS()      // serve in place of hyperplot.Stylesheet()

chart.Standalone = true
theme := hyperplot.TraceTheme()
chart.Theme = &theme                     // standalone SVG in the brand colors
```

The tokens and the validation are in [DESIGN.md, "Trace theme"](../DESIGN.md#trace-theme-tracetheme).

## Files

| File | Use |
|---|---|
| `lockup.svg`, `lockup-dark.svg` | Mark and wordmark together, for light and dark grounds: the README header |
| `hero-dark.svg`, `hero-light.svg` | README hero, 1600×790: a live latency chart between the tags of the htmx element that fetches it. Self-contained, with outlined glyphs and no fonts or `<style>`. Use the dark one as the fallback, since its opaque glass holds on any ground |
| `mark.svg` | The mark on any ground. Its glass tile holds on light and dark. Muddy at 24 px and below |
| `mark-dark.svg` | The mark with a lit bezel edge, for dark grounds at 32–64 px |
| `mark-mono.svg` | One-color mark using `currentColor`, for **inline SVG only**. Inside `<img>` it turns black and vanishes on dark pages |
| `favicon.svg`, `favicon-32.png`, `apple-touch-180.png` | Site icons. Use these, not `mark.svg`, at 24 px and below |
| `mark-512.png` | Large raster mark, for an npm or org avatar |
| `wordmark.svg`, `wordmark-dark.svg` | The wordmark alone, with no red full stop (red lives only in the mark) |
| `og-mark.png` | GitHub social preview, 1280×640. Upload it by hand: Settings → General → Social preview |
| `og-markup.png` | A second card, for the future docs site's `og:image` or launch posts |

The hero copies the README quick start verbatim:
`hx-get="/charts/latency" hx-trigger="every 2s, htmx:plot:resize" hx-swap="innerMorph" hx-plot:autosize`.
**If the quick start changes, the hero is stale.** Regenerate it with the
generators the vibehyper.media project keeps for this identity.
