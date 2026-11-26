# Chart gallery

Every mark and variant hyperplot draws, each on a card next to the Go
code that drew it, in light and dark.

## Run

```sh
go run ./examples/gallery                    # http://localhost:8080
go run ./examples/gallery -addr :9000
go run ./examples/gallery -out out/gallery   # write static files and exit
```

`-out DIR` writes the same page as static files, for a project site or
any static host:

```text
out/gallery/
  index.html     follows the reader's system theme
  light.html     data-theme="light"
  dark.html      data-theme="dark"
  static/hx-plot.js  static/hx-plot.css  static/app.css
```

Every URL in the pages is relative, so the directory works from any path,
or opened straight from disk. A static host may send no headers, so the
exported pages carry their Content Security Policy in a `<meta>` tag
(`out/` is in `.gitignore`).

## What to look at

1. The cards: lines with and without direct labels, gaps, curves,
   categorical and numeric x; single, stacked and banded areas (the band
   crosses zero); single, grouped and stacked bars, with negatives;
   scatter plots with and without point labels; reference lines, fixed
   domains and custom formats; long names, an empty chart and untrusted
   text; sparklines and a data table.
2. The code beside each chart is the func in `charts.go` that returned
   it, doc comment and all. Change a func, restart, and the card shows
   the new code and the new chart.
3. Hover a chart, or press Tab to focus it and read it with the arrow
   keys. There is no htmx on the page: `hx-plot.js` runs on its own.
4. The Theme links switch light and dark with no script: the server
   writes `data-theme` on `<html>` (`?theme=dark`), and hyperplot's
   stylesheet and `app.css` both follow it. Without one, the page follows
   the system setting.
5. "Untrusted text": the title, series name and categories hold markup,
   shown as text. Try `/?theme="><script>`: the server drops anything but
   `light` and `dark`.
6. Turn JavaScript off: every chart still shows, bars and dots keep their
   native tooltips, and each "View as table" holds the numbers.

## Key code

| What | Where |
|---|---|
| One func per card, returning a `*plot.Chart` | `charts.go` |
| The cards, in page order (a slice, so the page is deterministic) | `gallery.go`: `sections` |
| `//go:embed charts.go`, then `go/parser` cuts out each declaration | `source.go`: `parseDecls` |
| The card's source looked up by the name of the func it calls | `source.go`: `funcName`; `gallery.go`: `build` |
| Go source escaped and colored with `go/scanner` | `source.go`: `highlight` |
| `chart.HTML()` and `chart.Table()` into `html/template` | `gallery.go`: `render` |
| `?theme=` checked and written to `<html data-theme>` | `main.go`: `page`; `page.html` |
| `<script defer src="static/hx-plot.js">`, no htmx | `page.html` |
| `-out`: a page per theme plus `client.JS()` and the stylesheets | `main.go`: `export` |
| The strict CSP header, and a `<meta>` CSP in the export | `main.go`: `securityHeaders`, `exportPolicy` |

## How it works

`main` renders every chart once at startup. Each entry in `gallery.go`
names a func of `charts.go`; `build` calls it, renders the chart and its
table, and looks up the func's source by name in the parsed copy of
`charts.go` that is embedded in the binary. Since the source is found
from the func that was called, and the embedded file is the one that was
compiled, the code on a card cannot drift from its chart. The tests check
that again, and that every chart func in `charts.go` is on the page.

The page has no htmx. `hx-plot.js` finds every inline chart and adds the
tooltip, the crosshair and keyboard reading on its own
([spec §6.10](../../spec/spec.md#610-without-htmx)). Without script, bars
and dots keep native tooltips
([§3.7](../../spec/spec.md#37-native-fallback)), and the data table is
the chart's equivalent ([§3.8](../../spec/spec.md#38-the-table-equivalent)).
All text is escaped ([§3.6](../../spec/spec.md#36-text-and-safety)), so
the page runs under a CSP with no inline script or style
([Content Security Policy](../../docs/htmx.md#content-security-policy)).
Dark mode is the stylesheet's `data-theme` switch
([Dark mode](../../docs/htmx.md#dark-mode)).

Each chart is drawn 640 px wide. On a narrow window the stylesheet
scales it down, its text with it: a static page cannot ask the server
for another width. A served page can, with `hx-plot:autosize` (see the
[sizing recipe](../recipes/sizing/)). For live charts (polling, streams,
gestures), see the [dashboard](../dashboard/README.md).

## Tests

```sh
go test ./examples/gallery
python3 spec/hpcheck/hpcheck.py file out/gallery/index.html  # after -out out/gallery
```

They render every entry (no error, every SVG and table well-formed XML,
each chart id the func's name in kebab case); check that each card's
code is the source of the func it rendered, that its highlighted HTML
reads back as that source, that every chart func in `charts.go` is on
the page, and that `charts.go` stays within 88 columns, the width of
the code beside a chart on a wide window; load the page for each
theme, including a hostile `?theme=` (status, CSP header, `data-theme`,
ids unique page-wide, no `style` or `on*` attributes, no inline script,
no htmx); check the assets; and export to a temporary directory and
check every file.
