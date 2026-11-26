# Filters

A filter form whose every change re-renders three charts with one request,
and updates a summary line elsewhere on the page in the same response.

## Run

```sh
go run ./examples/recipes/filters-form            # http://localhost:8080
go run ./examples/recipes/filters-form -addr :9000
```

## What to look at

1. Untick a region. All three charts change at once and the remaining
   lines move to their new values instead of being redrawn. `us-east` stays
   orange whichever regions are shown.
2. In devtools' Network panel, each change is one
   `GET /?range=…&region=&region=…` with `HX-Request-Type: partial`. The
   response is only the three charts plus
   `<hx-partial hx-target="#summary" …>`, and its headers include
   `Vary: HX-Request, HX-Request-Type`.
3. The address bar follows the filters (`hx-push-url`). Reload, or press
   Back: the same URL serves the whole page with those filters.
4. In devtools, throttle the network ("Slow 4G") and change three filters
   quickly. Each change cancels the request before it
   (`hx-sync="this:replace"`), so the last one wins: the charts, the
   summary and the URL end up matching the form. htmx's default would
   queue one request and drop the rest.
5. Untick every region: the charts empty and the summary says so, without
   an error. The hidden `region=""` field is what tells "nothing ticked"
   apart from a first visit, which shows every region.
6. Turn JavaScript off and reload: an **Apply** button appears and the
   form works as a plain GET.

## Key code

| What | Where |
|---|---|
| `hx-get="/" hx-trigger="change" hx-target="#charts" hx-swap="innerMorph"` on the form | `page.html` |
| `hx-push-url="true"` | `page.html` |
| `hx-sync="this:replace"`: a change made during a request aborts it, so the last change wins | `page.html` |
| `hx.IsPartial(r)`: only `#charts` for htmx, the page otherwise | `main.go`: `page` |
| `Vary: HX-Request, HX-Request-Type` | `main.go`: `page` |
| `hx.Partial("#summary", "innerHTML", …)`, text escaped first | `main.go`: `page` |
| Fixed `Chart.ID` per chart, so the morph matches them | `main.go`: `chart` |
| `Slot` pins each region's color | `main.go`: `regions`, `chart` |
| Unknown filter values ignored | `main.go`: `parseFilters` |

## How it works

The form sends all its fields on every change, and the server re-renders
every chart for that slice of data. Because each chart keeps its id and
each series id derives from the series name, `innerMorph` updates the
existing elements and `hx-plot.js` animates the change; `Slot` keeps a
region on its color when others are filtered out. See
[Pattern 3: filters](../../../docs/htmx.md#pattern-3-filters) and
[Keep colors stable with `Slot`](../../../docs/htmx.md#keep-colors-stable-with-slot)
in the htmx guide.

One URL answers both the page and the fragment, so the response says
`Vary: HX-Request, HX-Request-Type` and no cache serves one for the other
([spec §4.5](../../../docs/spec.md#45-transports)). The summary line lives
outside `#charts`; an `<hx-partial>` in the same response updates it
([Pattern 4: multi-target updates](../../../docs/htmx.md#pattern-4-multi-target-updates)).

The charts have a fixed width, scaled down by CSS. A filter request
targets `#charts`, so it carries no `hp-w`; to keep autosized charts at
their widths across a filter change, see
[Autosized charts in a filtered area](../../../docs/htmx.md#autosized-charts-in-a-filtered-area)
and the [dashboard](../../dashboard/).

## Tests

```sh
go test ./examples/recipes/filters-form
```

They check the page (status, CSP header, `Vary`, the pinned htmx tag, the
form's attributes, the checked defaults), that a filter request gets the
three charts and the summary partial but not the page, that a region keeps
its slot alone, that the range sets the x domain, a table of filters
(first visit, two regions, none, junk that is ignored and never echoed),
the no-script submit, and deterministic output.
