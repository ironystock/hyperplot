# CI builds

An analytics page for simulated CI builds: every chart is SVG drawn by the
Go reference server and kept current by htmx 4 polling with content tags,
with brush-to-zoom, select-to-drill-down and one filter form.

## Run

```sh
go run ./examples/builds               # http://localhost:8080
go run ./examples/builds -addr :9000
go run ./examples/builds -speed 60     # simulated time runs 60x faster
```

At normal speed a build finishes about every half hour during working
hours (UTC), less often at night and at weekends, so most polls are 304s
for a long time. `-speed 60` makes that about every 30 seconds, and
`-speed 600` every few seconds. The server uses only the Go standard
library. The page loads htmx 4.0.2 and its `hx-ptag` extension from
jsDelivr (pinned, with Subresource Integrity), so the browser needs
network access; `hx-plot.js` and `hx-plot-brush.js` come from the Go
package `client` (elsewhere you would install `htmx-ext-plot` from npm or
use a CDN).

## What to look at

1. **The 304s.** Open the network panel and filter on `charts/`. Each
   chart polls (every 5 or 10 s) with an `HX-PTag` request header; until a
   build finishes, the answer is `304 Not Modified` with no body and
   nothing on the page moves. When one finishes, the durations scatter and
   the daily bars answer 200 with a new `HX-PTag`, and the dots and bars
   morph in place.
2. **The seed.** The page's `hx-ptag` attributes hold the tag of what it
   just rendered, so the first poll after load (sent with `hp-w`, the
   container's width) is a 304 whenever the width matches.
3. **Outlier labels.** Builds slower than twice their pipeline's typical
   time carry a label with their number (`#888671`). Labels that would
   collide are left out of the drawing but stay in the tooltip and the
   "View as table" rows.
4. **Brush to zoom.** Drag across the durations chart. The request carries
   `hp-chart=durations`, `hp-x0` and `hp-x1`; the answer is the whole
   polling element (its `hx-get` now has `from=…&to=…`), the toolbar as an
   `<hx-partial>`, and `HX-Push-Url`. Press Back to undo it. The From/To
   form does the same without a pointer; "Show all" returns.
5. **Select to drill down.** Click the red (failed) part of a day's bar:
   the request carries `hp-x=Mar 3` (the category) and `hp-series=failed`,
   and the list below shows only that day's failed builds. Enter on a
   focused day names no series, so it lists all of them; the day links
   under the chart and the All/Passed/Failed links send the same request.
6. **Filters.** Change the branch or untick a pipeline: one request to `/`
   with `HX-Request-Type: partial` re-renders everything in `#charts`, the
   address bar is rewritten (`HX-Replace-Url`), and each pipeline keeps its
   color. Each chart's hidden `w-…` input sends its current width along.
   Untick every pipeline: the charts show all of them rather than going
   empty, the boxes stay cleared, and the address keeps `pipeline=` so a
   reload shows them cleared too. A URL without `pipeline` (a first visit)
   ticks every box.
7. **Small multiples.** Make the window narrower: the trends container
   reports its new width (`htmx:plot:resize`, `hp-w`) and the server
   answers with one, two or three columns.
8. **No script of its own.** The page has no `app.js`, no inline script or
   style, and runs under a strict `Content-Security-Policy`. The theme
   links (`?theme=dark`) are plain links that the server writes into
   `<html data-theme>`.

## Key code

| What | Where |
|---|---|
| `hx-ptag="{{.Tag}}"`, `hx-trigger="load delay:1ms, every 5s, htmx:plot:resize"` | `templates/page.html` "charts", "durations" |
| `hx.Tag`, `hx.CheckPTag` (304 on a matching tag) | `server.go` `writeTagged` |
| `hx-plot:autosize`, `hx.Size` (`hp-w`) | `server.go` `chartWidth`, `trendsBody`; `charts.go` `trendLayout` |
| `hx-plot:brush`, `hx-trigger="htmx:plot:brush"`, `hx-swap="outerMorph"`, `hx-sync` | `templates/page.html` "charts" |
| `hx.Brushed`, `Brush.TimesIn`, `HX-Push-Url` | `server.go` `durations`, `zoomTo`; `gestures.go` `brushZoom`, `newZoom` |
| `hx-trigger="htmx:plot:select"`, `hx.Selected` (`hp-x`, `hp-series`) | `templates/page.html` "charts"; `gestures.go` `selectedDay`, `newDetail` |
| Select and brush in plain HTML (links, a datetime-local form) | `templates/page.html` "day-links", "zoom-bar", "detail" |
| `hx.Partial("#summary", …)` riding along with a poll | `server.go` `daysResponse` |
| Filter form, `hx.IsPartial`, `Vary`, `HX-Replace-Url` | `templates/page.html` "page"; `server.go` `page` |
| `Dot.Labels` for outliers, stacked `Bar`, `Line` small multiples | `charts.go` |
| Deterministic builds (FNV-1a noise per pipeline and hour) | `data.go` `buildsStarted`, `finishedBuilds` |

## How it works

Every number comes from `data.go`: a build is a pure function of its
pipeline, the hour it started and its index within the hour, and it counts
once it has finished. So two requests between two finishes render the same
bytes, and the content tag ([spec §4.5](../../docs/spec.md#45-transports),
[pattern 1](../../docs/htmx.md#pattern-1-polling-with-content-tags)) turns
every such poll into a bodiless 304. The durations window ends at the next
whole hour rather than at now, for the same reason.

The two gestures are ordinary htmx requests with `hp-*` parameters
([spec §5](../../docs/spec.md#5-gestures-level-3)). The brush follows
[spec §5.4](../../docs/spec.md#54-example-brush-to-zoom-a-live-chart) and
[pattern 6](../../docs/htmx.md#pattern-6-brush-to-zoom): the zoom changes
the poller's own URL, so the answer replaces the poller (`outerMorph`),
and `hx-sync` puts brushes and polls on one queue so a late poll cannot
undo a zoom. A zoomed poll draws exactly the `from` and `to` in its URL;
once midnight moves the range past a zoom on its oldest day, the poll
answers 204 and the chart keeps its last frame, rather than drawing a
window the toolbar and the address bar do not name. The select follows
[pattern 5](../../docs/htmx.md#pattern-5-drill-down-on-select): `hp-x` is
the bar's category, the day label, which the server matches exactly; a
select it cannot match is no selection. Both have plain-HTML equivalents
([spec §5.5](../../docs/spec.md#55-equivalents-in-plain-html)).

The filter form is [pattern 3](../../docs/htmx.md#pattern-3-filters), with
colors pinned per pipeline
([`Slot`](../../docs/htmx.md#keep-colors-stable-with-slot)). The summary
line rides along with the daily chart's poll
([pattern 4](../../docs/htmx.md#pattern-4-multi-target-updates)), and the
charts keep the server's height while taking the container's width
([height](../../docs/htmx.md#height)).

## Tests

```sh
go test ./examples/builds
```

`main_test.go` drives `routes()` with `httptest` at a fixed clock. It
checks that every route answers 200 with the CSP header and well-formed
SVG, that no response has a style attribute, inline handler or inline
script, the ptag round trip (304 for every echo form, 200 for another tag
or width), that the page seeds the tag its first poll gets, that tags hold
until a build finishes and change when one does, `hp-w` sizing and
clamping, the filters and pinned slots, outlier labels, the brush (RFC 3339
and datetime-local values, the pushed URL, the zoomed poll, a zoom that
ages out as the clock moves) and the brushes it ignores, that clearing
every pipeline survives a reload, the select with and without a series, the day links, escaping,
unique ids, and that junk parameters never cause a 5xx.
