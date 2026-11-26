// Package hyperplot renders charts as server-side SVG, designed to be kept
// live by htmx.
//
// A [Chart] is plain data: a size, two axes and a list of marks ([Line],
// [Area], [Bar], [Dot] and [RuleY]). [Chart.Render] turns it into a
// self-describing, accessible SVG element whose output is deterministic,
// so the same data always yields the same bytes. That makes charts cheap
// to cache, hash (for htmx's hx-ptag) and morph in place (with htmx's
// innerMorph swap), where stable element ids let CSS transitions animate
// each update.
//
// The SVG carries presentation attributes in terms of currentColor, so it
// is legible with no stylesheet at all. Serve [Stylesheet] (or a custom
// [Theme]'s CSS) to get the full palette, dark mode and tooltip styles.
// [Chart.Table] gives the same data as an accessible HTML table, and
// [Sparkline] draws compact, axis-free charts for stat tiles.
//
// Related packages: hx holds the htmx 4 server helpers (content tags,
// server-sent event streams, multi-target partials and client-reported
// sizes), client embeds the optional browser extension, which adds
// tooltips, a crosshair, keyboard reading and responsive re-rendering,
// and scale holds the scales, ticks and number formatting the renderer
// uses. Labels follow US conventions by default (a 12-hour clock,
// month-first dates); see [Axis] for overriding them.
//
// The package uses only the Go standard library.
package hyperplot

import (
	"context"
	"errors"
	"html/template"
	"io"
	"time"
)

// Chart describes one chart. The zero value plus some marks is a valid
// chart: every field has a sensible default. Render does not modify the
// Chart or its slices, so a Chart can be rendered concurrently and
// repeatedly.
type Chart struct {
	// ID is the DOM id of the <svg> element and the prefix of every id
	// inside it (its <title>, <desc> and clip path, which aria-labelledby
	// and clip-path refer to). It must be unique on the page, and live
	// updates need it to be stable. It may contain only ASCII letters,
	// digits, '-', '_', ':' and '.'.
	//
	// The default is "hp-" plus an 8-digit hash of the Title,
	// Description, axis labels, and the kind and name of every series:
	// stable while the data changes, and different for charts that differ
	// in any of these. Set ID explicitly for every chart you update live,
	// and whenever two charts on a page could agree on all of these
	// (untitled charts of unnamed series, or the same chart rendered for
	// two filters), because charts with equal ids would share each other's
	// accessible name and clip path.
	ID string
	// Title is the chart's accessible name. It is drawn visibly only
	// when ShowTitle or Standalone is set.
	Title string
	// Description is the chart's accessible description. When empty, a
	// brief factual summary of the chart type, series and axis ranges is
	// generated.
	Description string
	// Width and Height are the size in pixels, 640 × 320 by default. Each
	// must be at least 50 and at most 32768.
	Width, Height float64
	// FontSize is the base font size in pixels, 12 by default, between 4
	// and 96.
	FontSize float64
	// X and Y configure the axes.
	X, Y Axis
	// Marks are the chart's layers, drawn in order.
	Marks []Mark
	// Stack stacks Bars (and Areas) instead of grouping (or overlapping)
	// them.
	Stack bool
	// Legend controls the legend; see [LegendMode].
	Legend LegendMode
	// Animate adds the "hp-anim" class, so that the stylesheet's CSS
	// transitions animate the changes when htmx morphs in a new render.
	Animate bool
	// ShowTitle draws Title as visible text at the top left.
	ShowTitle bool
	// Standalone makes a self-contained SVG file: it embeds the theme's
	// stylesheet, draws a background, adds a margin, and implies
	// ShowTitle. Use it for files and <img> tags, not for inline SVG in
	// a page that already loads the stylesheet.
	Standalone bool
	// Theme sets the colors embedded by Standalone. nil means
	// [DefaultTheme]. It has no effect on inline charts, which are
	// styled by the page's stylesheet.
	Theme *Theme
	// Location is the time zone for a time x axis. nil means UTC.
	Location *time.Location
	// BarWidth is the maximum bar thickness in pixels, 24 by default.
	BarWidth float64
}

// Axis configures one axis of a [Chart].
type Axis struct {
	// Label is the axis title. The Y label is drawn horizontally above
	// the plot at the top left; the X label is drawn under the right end
	// of the plot.
	Label string
	// Unit is appended to values in tooltips, the data table and the
	// description, such as " ms" or "%". It is not added to tick labels.
	// Where it repeats once per value it is shortened with an ellipsis
	// past 32 runes, so a long unit cannot multiply the output size; the
	// description carries it whole.
	Unit string
	// Ticks is the approximate number of ticks. 0 picks a count from the
	// axis length: one per 50px on Y and one per 90px on X.
	Ticks int
	// Format formats numeric ticks and values. nil uses a format chosen
	// from the ticks for tick labels and a precise, compact format for
	// values.
	Format func(float64) string
	// TimeFormat formats time tick labels. nil uses multi-scale labels
	// from [github.com/ironystock/hyperplot/scale.Time.TickFormatFor]
	// ("Mar 3", "6 AM", "12 PM", …), which follow US conventions: a
	// 12-hour clock and month-first dates. It affects ticks only; set
	// TimeValueFormat too for 24-hour or other locale-specific labels
	// throughout the chart.
	TimeFormat func(time.Time) string
	// TimeValueFormat formats a time in full, where it is read out of
	// context: tooltip headings, data table rows and the description.
	// It should label distinct data times differently. nil uses
	// [github.com/ironystock/hyperplot/scale.Time.FormatterFor], US
	// style at the precision the ticks and data need
	// ("Mar 3, 2026 2:05 PM").
	TimeValueFormat func(time.Time) string
	// Domain fixes a numeric domain instead of fitting the data. It is
	// used when Domain[0] != Domain[1]; both must then be finite.
	Domain [2]float64
	// TimeDomain fixes a time domain. It is used when both ends are
	// non-zero.
	TimeDomain [2]time.Time
	// Zero always includes 0 in a numeric domain. Charts with bars or
	// areas always include 0 on Y.
	Zero bool
	// Hide hides the axis's tick labels. Gridlines stay.
	Hide bool
	// NoGrid hides the horizontal gridlines. It applies to Y only.
	NoGrid bool
}

// LegendMode controls when a chart draws its legend.
type LegendMode int

const (
	// LegendAuto shows the legend when the chart has two or more
	// series. A single series is named by the chart's title.
	LegendAuto LegendMode = iota
	// LegendShow always shows the legend.
	LegendShow
	// LegendHide never shows the legend.
	LegendHide
)

// ErrTooManySeries is returned (wrapped) when a chart has more than eight
// series. Beyond eight, colors can no longer be told apart reliably: fold
// the smallest series into an "Other" series or use small multiples.
var ErrTooManySeries = errors.New("hyperplot: too many series; at most 8 can be told apart by color, so fold the rest into an \"Other\" series or use small multiples")

var errNilChart = errors.New("hyperplot: nil *Chart")

// Render renders the chart as an <svg> element. It returns an error, and
// never panics, for invalid input: mismatched X and Y lengths, mixed x
// kinds across marks, a Bar with no categories or a repeated category, an
// invalid or doubly pinned Slot, more than eight series
// ([ErrTooManySeries]), an invalid ID, or a size out of range.
func (c *Chart) Render() ([]byte, error) {
	if c == nil {
		return nil, errNilChart
	}
	p, err := newPlan(c)
	if err != nil {
		return nil, err
	}
	return p.render(), nil
}

// WriteTo renders the chart and writes it to w. It implements
// [io.WriterTo]. Nothing is written if rendering fails.
func (c *Chart) WriteTo(w io.Writer) (int64, error) {
	b, err := c.Render()
	if err != nil {
		return 0, err
	}
	n, err := w.Write(b)
	return int64(n), err
}

// HTML renders the chart for use in an html/template. The output is
// escaped by construction, so it is safe to mark as trusted HTML.
func (c *Chart) HTML() (template.HTML, error) {
	b, err := c.Render()
	if err != nil {
		return "", err
	}
	return template.HTML(b), nil //nolint:gosec // all text in b is escaped by the SVG writer
}

// Component is a value that renders itself to a writer. Its method set
// matches templ.Component, so a chart can be used directly in templ
// templates without hyperplot importing templ.
type Component interface {
	Render(ctx context.Context, w io.Writer) error
}

// Component returns the chart as a [Component], for templ templates:
//
//	@chart.Component()
//
// The chart is rendered when the component is, so later changes to the
// Chart are reflected.
func (c *Chart) Component() Component { return chartComponent{c} }

type chartComponent struct{ c *Chart }

// Render renders the chart to w, first checking that ctx is not done.
func (cc chartComponent) Render(ctx context.Context, w io.Writer) error {
	if ctx != nil {
		if err := ctx.Err(); err != nil {
			return err
		}
	}
	_, err := cc.c.WriteTo(w)
	return err
}
