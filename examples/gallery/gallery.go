// The gallery's contents: which funcs of charts.go appear, in what order,
// under which heading, and what to look at in each. build renders every
// entry once and pairs it with its source.
package main

import (
	"fmt"
	"html/template"
	"math"
	"strconv"
	"strings"

	"github.com/ironystock/hyperplot"
)

// entry is one card. Exactly one of Chart and Sparks is set.
type entry struct {
	Title     string
	Note      string // what to look at, one or two sentences
	Chart     func() *hyperplot.Chart
	Sparks    func() []hyperplot.Sparkline
	OpenTable bool // show the data table unfolded
}

// section is a group of cards with a heading.
type section struct {
	ID, Title string
	Entries   []entry
}

// sections is the gallery, in page order. It is a slice, not a map, so the
// page is the same bytes on every run.
var sections = []section{
	{
		ID:    "lines",
		Title: "Lines",
		Entries: []entry{
			{
				Title: "Direct labels",
				Chart: lineDirectLabels,
				Note:  "Two to four lines get their names at their last points. Hover or focus the chart and use the arrow keys to read the values.",
			},
			{
				Title: "Legend only",
				Chart: lineLegendOnly,
				Note:  "Five series: past four, the legend names the lines. Colors come from eight fixed slots, in mark order.",
			},
			{
				Title: "Gaps",
				Chart: lineGaps,
				Note:  "Missing readings break the line. The reading between the two gaps stands alone; every reading has a dot.",
			},
			{
				Title: "Curves",
				Chart: lineCurves,
				Note:  "Linear, monotone and step interpolation of the same readings.",
			},
			{
				Title: "Categorical x",
				Chart: lineCategorical,
				Note:  "Months as categories. The 2026 line stops where its data does.",
			},
			{
				Title: "Numeric x",
				Chart: lineLinearX,
				Note:  "A linear x axis, labeled under its right end.",
			},
		},
	},
	{
		ID:    "areas",
		Title: "Areas",
		Entries: []entry{
			{
				Title: "Area",
				Chart: areaSingle,
				Note:  "A light wash under a 2 px line. The y axis starts at zero.",
			},
			{
				Title: "Stacked areas",
				Chart: areaStacked,
				Note:  "Each tier stacks on the ones before it; the top edge is the total.",
			},
			{
				Title: "Band across zero",
				Chart: areaBand,
				Note:  "A min–max band (Area.Base) that dips below zero, with the mean drawn over it.",
			},
		},
	},
	{
		ID:    "bars",
		Title: "Bars",
		Entries: []entry{
			{
				Title: "Bars",
				Chart: barsSingle,
				Note:  "One series of categories. Each bar has a native tooltip, even without JavaScript.",
			},
			{
				Title: "Grouped bars",
				Chart: barsGrouped,
				Note:  "Three series side by side in each category.",
			},
			{
				Title: "Grouped bars with negatives",
				Chart: barsGroupedNegative,
				Note:  "Negative bars hang from the zero baseline with their rounded end down.",
			},
			{
				Title: "Stacked bars with negatives",
				Chart: barsStackedNegative,
				Note:  "Positive values stack up from zero and negative values down.",
			},
		},
	},
	{
		ID:    "scatter-plots", // not "scatter": that is a chart's id, and ids share one page
		Title: "Scatter",
		Entries: []entry{
			{
				Title: "Scatter",
				Chart: scatter,
				Note:  "Two series of dots on numeric axes.",
			},
			{
				Title: "Labeled points",
				Chart: scatterLabels,
				Note:  "Labels go where they fit. In the crowd at the lower right there is no room for \"catalog\", so it is only in the tooltip and the table.",
			},
		},
	},
	{
		ID:    "annotations",
		Title: "Rules, domains and formats",
		Entries: []entry{
			{
				Title: "Thresholds",
				Chart: rules,
				Note:  "Reference lines with labels. They take no color and stay out of the legend.",
			},
			{
				Title: "Fixed domains",
				Chart: fixedDomains,
				Note:  "Y is fixed at 0–100% and x at the whole day, while the data stops at 2 PM.",
			},
			{
				Title: "Your own formats",
				Chart: customFormats,
				Note:  "A 24-hour clock on the axis and in the tooltip, and values in thousands.",
			},
		},
	},
	{
		ID:    "edge-cases",
		Title: "Long text, no data, untrusted text",
		Entries: []entry{
			{
				Title: "Long names",
				Chart: longNames,
				Note:  "The title, the legend and the category labels are cut with an ellipsis. Hover a bar for the full text.",
			},
			{
				Title: "No data",
				Chart: empty,
				Note:  "An empty series still renders a chart, with \"No data\" where the plot would be.",
			},
			{
				Title: "Untrusted text",
				Chart: untrustedText,
				Note:  "Markup in names and categories is escaped and shown as text.",
			},
		},
	},
	{
		ID:    "compact",
		Title: "Sparklines and tables",
		Entries: []entry{
			{
				Title:  "Sparklines",
				Sparks: sparklines,
				Note:   "Axis-free lines for tiles and table rows. A screen reader hears the label, the first, lowest, highest and last values.",
			},
			{
				Title:     "Data table",
				Chart:     dataTable,
				OpenTable: true,
				Note:      "Chart.Table is the chart as an accessible HTML table. Every card has one under \"View as table\"; here it is open.",
			},
		},
	},
}

// gallery is everything the page shows, rendered once.
type gallery struct {
	Sections []sectionView
	Helpers  template.HTML // the declarations of charts.go that are not cards, highlighted
}

type sectionView struct {
	ID, Title string
	Cards     []card
}

// card is one rendered entry.
type card struct {
	Anchor      string // the card's id: "ex-" plus the chart id
	Title, Note string
	Func        string        // the func in charts.go that made the chart
	Source      string        // that func's source text
	Code        template.HTML // Source, highlighted
	SVG         template.HTML
	Table       template.HTML
	TableOpen   bool
	Sparks      []sparkRow
}

// sparkRow is one sparkline with its label and latest value.
type sparkRow struct {
	Label, Last string
	SVG         template.HTML
}

// build renders every entry and looks up its source in src (charts.go).
func build(sections []section, src []byte) (*gallery, error) {
	decls, err := parseDecls(src)
	if err != nil {
		return nil, fmt.Errorf("parse charts.go: %w", err)
	}
	source := make(map[string]string, len(decls))
	for _, d := range decls {
		if d.Name != "" {
			source[d.Name] = d.Code
		}
	}

	g := &gallery{}
	shown := map[string]bool{}
	for _, sec := range sections {
		view := sectionView{ID: sec.ID, Title: sec.Title}
		for _, e := range sec.Entries {
			c, err := render(e)
			if err != nil {
				return nil, err
			}
			code, ok := source[c.Func]
			if !ok {
				return nil, fmt.Errorf("%s: not declared in charts.go", c.Func)
			}
			c.Source, c.Code = code, highlight(code)
			shown[c.Func] = true
			view.Cards = append(view.Cards, c)
		}
		g.Sections = append(g.Sections, view)
	}

	// The rest of charts.go (the data helpers) goes at the end of the page,
	// so every name a card calls can be looked up.
	var rest []string
	for _, d := range decls {
		if !shown[d.Name] {
			rest = append(rest, d.Code)
		}
	}
	g.Helpers = highlight(strings.Join(rest, "\n\n"))
	return g, nil
}

// render calls an entry's func and renders what it returns.
func render(e entry) (card, error) {
	c := card{Title: e.Title, Note: e.Note, TableOpen: e.OpenTable}
	if e.Sparks != nil {
		c.Func = funcName(e.Sparks)
		c.Anchor = "ex-" + kebab(c.Func)
		for _, s := range e.Sparks() {
			svg, err := s.HTML()
			if err != nil {
				return card{}, fmt.Errorf("%s: %w", c.Func, err)
			}
			c.Sparks = append(c.Sparks, sparkRow{Label: s.Label, Last: last(s.Values) + s.Unit, SVG: svg})
		}
		return c, nil
	}
	c.Func = funcName(e.Chart)
	chart := e.Chart()
	c.Anchor = "ex-" + chart.ID
	var err error
	if c.SVG, err = chart.HTML(); err != nil {
		return card{}, fmt.Errorf("%s: %w", c.Func, err)
	}
	if c.Table, err = chart.Table(); err != nil {
		return card{}, fmt.Errorf("%s: %w", c.Func, err)
	}
	return c, nil
}

// last formats the last finite value, or "–" when there is none.
func last(vs []float64) string {
	for i := len(vs) - 1; i >= 0; i-- {
		if !math.IsNaN(vs[i]) && !math.IsInf(vs[i], 0) {
			return strconv.FormatFloat(vs[i], 'f', -1, 64)
		}
	}
	return "–"
}
