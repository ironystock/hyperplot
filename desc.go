package hyperplot

import (
	"math"
	"strconv"
	"strings"
)

// chartType names the chart's form, such as "Line chart" or "Stacked bar
// chart".
func (p *plan) chartType() string {
	var kinds []string
	if p.hasBar {
		if p.c.Stack {
			kinds = append(kinds, "stacked bar")
		} else {
			kinds = append(kinds, "bar")
		}
	}
	if p.hasArea {
		if p.c.Stack {
			kinds = append(kinds, "stacked area")
		} else {
			kinds = append(kinds, "area")
		}
	}
	if p.hasLine {
		kinds = append(kinds, "line")
	}
	if p.hasDot {
		if len(kinds) == 0 && p.xk != xBand {
			return "Scatter plot"
		}
		kinds = append(kinds, "dot")
	}
	if len(kinds) == 0 {
		return "Empty chart"
	}
	s := strings.Join(kinds, " and ") + " chart"
	return strings.ToUpper(s[:1]) + s[1:]
}

// description returns a brief factual summary of the chart: its type,
// series and axis ranges.
func (p *plan) description() string {
	if p.c.Description != "" {
		return p.c.Description
	}
	var b strings.Builder
	b.WriteString(p.chartType())
	switch n := len(p.series); n {
	case 0:
		b.WriteString(".")
		return b.String()
	case 1:
		b.WriteString(" of " + p.series[0].label + ".")
	default:
		b.WriteString(" with " + strconv.Itoa(n) + " series: ")
		for i, s := range p.series {
			if i > 0 {
				b.WriteString(", ")
			}
			b.WriteString(s.label)
		}
		b.WriteString(".")
	}
	if p.hasData() {
		if x := p.describeX(); x != "" {
			b.WriteString(" " + x)
		}
		b.WriteString(" " + p.describeY())
	} else {
		// The plot says "No data", and so does the description. Its axis
		// ranges would be fallbacks, except for x values that exist.
		b.WriteString(" No data.")
		if p.hasX || p.xk == xBand && len(p.cats) > 0 {
			b.WriteString(" " + p.describeX())
		}
	}
	rules, _ := p.visibleRules()
	for _, r := range rules {
		b.WriteString(" Reference line")
		if r.Label != "" {
			b.WriteString(" " + r.Label)
		}
		b.WriteString(" at " + p.yValue(r.Y) + ".")
	}
	return b.String()
}

func axisName(prefix, label string) string {
	if label == "" {
		return prefix
	}
	return prefix + " " + label + ","
}

func (p *plan) describeX() string {
	c := p.c
	switch p.xk {
	case xTime:
		t0, t1 := p.timeDomain()
		f := c.X.TimeValueFormat
		if f == nil {
			f = p.x.tim.Formatter(p.x.count)
		}
		if t0.Equal(t1) {
			return axisName("X axis", c.X.Label) + " time at " + f(t0) + "."
		}
		return axisName("X axis", c.X.Label) + " time from " + f(t0) + " to " + f(t1) + "."
	case xBand:
		n := len(p.cats)
		if n == 1 {
			return axisName("X axis", c.X.Label) + " 1 category: " + p.cats[0] + "."
		}
		return axisName("X axis", c.X.Label) + " " + strconv.Itoa(n) + " categories from " + p.cats[0] + " to " + p.cats[n-1] + "."
	case xLinear:
		d := p.x.lin.Domain
		lo, hi := math.Min(d[0], d[1]), math.Max(d[0], d[1])
		lv, hv := p.xPlain(lo), p.xPlain(hi)
		if c.X.Format == nil && lv == hv && lo < hi {
			lv, hv = distinctPair(lo, hi)
		}
		return axisName("X axis", c.X.Label) + " " + lv + c.X.Unit + " to " + hv + c.X.Unit + "."
	}
	return ""
}

// describeY names the Y range, with its end points written so that they
// differ when the domain does, and the unit in full.
func (p *plan) describeY() string {
	d := p.y.Domain
	lo, hi := math.Min(d[0], d[1]), math.Max(d[0], d[1])
	lv, hv := p.yNumber(lo), p.yNumber(hi)
	if p.c.Y.Format == nil && lv == hv && lo < hi {
		lv, hv = distinctPair(lo, hi)
	}
	return axisName("Y axis", p.c.Y.Label) + " " + lv + " to " + hv + p.c.Y.Unit + "."
}
