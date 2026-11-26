package hyperplot

import (
	"html/template"
	"strings"

	"github.com/ironystock/hyperplot/internal/svg"
)

// Table returns an accessible HTML <table class="hp-table"> holding the
// chart's data, the equivalent of the chart for screen readers, keyboard
// users and anyone who wants exact numbers. Its caption is the chart's
// title, and the x column is headed by X.Label ("Time" or "X" when it is
// empty). There is one row per x value and one column per series, with
// values formatted like the tooltips (Y.Format plus Y.Unit) and "–" for
// missing values; Dot labels, when present, fill the first column. A
// chart of only dots lists one row per point instead.
//
// Place it next to the chart, for example inside a <details> element, so
// that it stays reachable without hovering. It returns the same
// validation errors as [Chart.Render].
func (c *Chart) Table() (template.HTML, error) {
	if c == nil {
		return "", errNilChart
	}
	p, err := newPlan(c)
	if err != nil {
		return "", err
	}
	return template.HTML(p.table()), nil //nolint:gosec // every cell is escaped
}

type tableWriter struct{ b strings.Builder }

func (t *tableWriter) raw(s string) { t.b.WriteString(s) }

// missingCell is the content of a cell with no value: the visible dash,
// hidden from screen readers, which skip a bare en dash at their default
// punctuation level and would read the cell as blank, and "no data" for
// them in visually hidden text (the stylesheet's .hp-sr).
const missingCell = `<span aria-hidden="true">` + missing + `</span><span class="hp-sr"> no data</span>`

func (t *tableWriter) cell(tag, scope, text string) {
	t.b.WriteString("<" + tag)
	if scope != "" {
		t.b.WriteString(` scope="` + scope + `"`)
	}
	t.b.WriteString(">" + svg.Escape(text) + "</" + tag + ">")
}

// value writes a value cell, spelling out a missing value for screen
// readers.
func (t *tableWriter) value(text string) {
	if text == missing {
		t.b.WriteString("<td>" + missingCell + "</td>")
		return
	}
	t.cell("td", "", text)
}

func (p *plan) table() string {
	var t tableWriter
	t.raw(`<table class="hp-table">`)
	caption := p.c.Title
	if caption == "" {
		caption = "Chart data"
	}
	t.raw("<caption>" + svg.Escape(caption) + "</caption>")
	xHead := p.c.X.Label
	switch {
	case xHead != "":
	case p.xk == xTime:
		xHead = "Time"
	default:
		xHead = "X"
	}
	if p.tipMode() == "xy" {
		p.pointTable(&t, xHead)
	} else {
		p.seriesTable(&t, xHead)
	}
	t.raw("</table>")
	return t.b.String()
}

// seriesTable writes one row per x value and one column per series.
func (p *plan) seriesTable(t *tableWriter, xHead string) {
	idx := p.pointIndex()
	type row struct {
		k    xkey
		head string
	}
	var rows []row
	if p.xk == xBand {
		for _, cat := range p.cats {
			rows = append(rows, row{xkey{s: cat}, cat})
		}
	} else {
		for _, c := range p.xColumns(idx) {
			rows = append(rows, row{c.k, p.xValue(c.s, c.i)})
		}
	}
	hasLabels := false
	for _, s := range p.series {
		hasLabels = hasLabels || s.labels != nil
	}
	t.raw("<thead><tr>")
	if hasLabels {
		t.cell("th", "col", "Label")
	}
	t.cell("th", "col", xHead)
	for _, s := range p.series {
		t.cell("th", "col", s.label)
	}
	t.raw("</tr></thead><tbody>")
	for _, r := range rows {
		t.raw("<tr>")
		if hasLabels {
			var labels []string
			for _, s := range p.series {
				if s.labels == nil {
					continue
				}
				for i := idx[s.index].at(r.k); i >= 0; i = idx[s.index].after(i) {
					if s.labels[i] != "" {
						labels = append(labels, s.labels[i])
					}
				}
			}
			t.cell("td", "", strings.Join(labels, ", "))
		}
		t.cell("th", "row", r.head)
		for _, e := range p.entries(idx, r.k) {
			t.value(e.value)
		}
		t.raw("</tr>")
	}
	t.raw("</tbody>")
}

// pointTable writes one row per point, for charts of only dots.
func (p *plan) pointTable(t *tableWriter, xHead string) {
	hasLabels, multi := false, len(p.series) > 1
	for _, s := range p.series {
		hasLabels = hasLabels || s.labels != nil
	}
	yHead := p.c.Y.Label
	if yHead == "" {
		yHead = "Y"
	}
	t.raw("<thead><tr>")
	if hasLabels {
		t.cell("th", "col", "Label")
	}
	if multi {
		t.cell("th", "col", "Series")
	}
	t.cell("th", "col", xHead)
	t.cell("th", "col", yHead)
	t.raw("</tr></thead><tbody>")
	for _, s := range p.series {
		for i := range s.y {
			if _, ok := keyOf(s.x, i); !ok {
				continue
			}
			t.raw("<tr>")
			if hasLabels {
				label := ""
				if s.labels != nil {
					label = s.labels[i]
				}
				t.cell("td", "", label)
			}
			if multi {
				t.cell("td", "", s.label)
			}
			t.cell("th", "row", p.xValue(s, i))
			t.value(p.yValue(s.y[i]))
			t.raw("</tr>")
		}
	}
	t.raw("</tbody>")
}
