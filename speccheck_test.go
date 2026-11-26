package hyperplot

import (
	"bytes"
	"encoding/json"
	"fmt"
	"math"
	"math/big"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"
)

// This file checks the MUST requirements of docs/spec.md that hold for
// every chart, whatever its data: the root contract (§3.2), the required
// descendants and the no-transform rule (§3.3), the hover metadata (§3.5),
// text safety and no external references (§3.6), the table (§3.8),
// identity (§4.1), the geometry attributes (§4.2) and row keys (§4.3).
// TestConformance runs it on the fixtures, TestGoldensMeetSpec on every
// golden chart, and FuzzRender on every chart it renders, so the reference
// server meets the spec for arbitrary input, not only for the fixtures.

const svgNS = "http://www.w3.org/2000/svg"

var (
	chartIDRe = regexp.MustCompile(`^[A-Za-z0-9_:.-]+$`)
	viewBoxRe = regexp.MustCompile(`^0 0 (\S+) (\S+)$`)
	slotRe    = regexp.MustCompile(`^hp-s([0-9]+)$`)
)

// wireTipsPayload is the hover metadata, decoded loosely enough to report
// what is wrong with it.
type wireTipsPayload struct {
	Mode   string        `json:"mode"`
	Xs     []float64     `json:"xs"`
	Bw     *float64      `json:"bw"`
	Top    *float64      `json:"top"`
	Bottom *float64      `json:"bottom"`
	Pts    [][]float64   `json:"pts"`
	Rows   []wireTipsRow `json:"rows"`
}

type wireTipsRow struct {
	T *string             `json:"t"`
	K *string             `json:"k"`
	V [][]json.RawMessage `json:"v"`
}

// specReport collects violations.
type specReport []string

func (r *specReport) addf(format string, args ...any) { *r = append(*r, fmt.Sprintf(format, args...)) }

// checkSpec checks a rendered chart, and its table when table is not nil,
// against the spec's universal MUSTs. It returns the violations.
func checkSpec(chart, table []byte) specReport {
	var r specReport
	if bytes.IndexByte(chart, '\r') >= 0 {
		r.addf("§3.1: the chart holds a raw CR byte")
	}
	root, err := parseDOM(chart)
	if err != nil {
		r.addf("§3.1: %v", err)
		return r
	}
	id := checkRoot(&r, root)
	checkSafety(&r, root, "chart")
	checkNoTransform(&r, root)

	// Identity (§4.1): unique ids, all in the chart's namespace.
	seen := map[string]bool{}
	root.walk(func(n *xnode) {
		v, ok := n.attr("id")
		if !ok {
			return
		}
		if seen[v] {
			r.addf("§4.1: duplicate id %q", v)
		}
		seen[v] = true
		if n != root && !strings.HasPrefix(v, id+"-") {
			r.addf("§4.1: id %q is not in the chart's {id}-* namespace", v)
		}
	})

	// Series groups (§3.3, §4.1).
	series := query(root, "g.hp-series")
	if len(series) > 8 {
		r.addf("§3.3: %d series groups, at most 8", len(series))
	}
	slots := map[int]bool{}
	seriesKeys := map[string]bool{}
	for _, g := range series {
		if g.parent == nil || g.parent.name != "g" || !g.parent.hasClass("hp-marks") {
			r.addf("§3.3: %s is not a child of g.hp-marks", g.path())
		}
		n := 0
		for _, c := range strings.Fields(g.get("class")) {
			if m := slotRe.FindStringSubmatch(c); m != nil {
				s, _ := strconv.Atoi(m[1])
				if s < 1 || s > 8 || slots[s] {
					r.addf("§3.3: %s has slot %d (1 to 8, unique)", g.path(), s)
				}
				slots[s] = true
				n++
			}
		}
		if n != 1 {
			r.addf("§3.3: %s has %d hp-sN classes, want 1", g.path(), n)
		}
		key, ok := strings.CutPrefix(g.get("id"), id+"-s-")
		if !ok || key == "" {
			r.addf("§4.1: series group id %q is not %s-s-{key}", g.get("id"), id)
		}
		seriesKeys[key] = true
		for _, bar := range query(g, "path.hp-bar") {
			if !strings.HasPrefix(bar.get("id"), id+"-b-"+key+"-") {
				r.addf("§4.1: bar id %q is not %s-b-%s-{catKey}", bar.get("id"), id, key)
			}
		}
	}
	for _, s := range []string{"circle.hp-focus", "rect.hp-brush"} {
		if len(query(root, s)) > 0 {
			r.addf("§3.3: the server emitted %s, which only the client writes", s)
		}
	}

	geo := checkGeometry(&r, root)
	tips := checkTips(&r, root, chart, geo, len(series), slots)

	if table != nil {
		checkTable(&r, table, id, tips)
	}
	return r
}

// checkRoot checks the root element (§3.2, §3.3) and returns the chart id.
func checkRoot(r *specReport, root *xnode) string {
	if root.name != "svg" {
		r.addf("§3.1: root is <%s>, want <svg>", root.name)
	}
	id := root.get("id")
	if !chartIDRe.MatchString(id) {
		r.addf("§3.2: chart id %q has characters outside [A-Za-z0-9-_:.]", id)
	}
	want := map[string]string{
		"xmlns": svgNS, "role": "img", "tabindex": "0", "data-hp": "1",
		"aria-labelledby": id + "-title", "aria-describedby": id + "-desc",
	}
	for _, k := range []string{"xmlns", "role", "tabindex", "data-hp", "aria-labelledby", "aria-describedby"} {
		if v, ok := root.attr(k); !ok || v != want[k] {
			r.addf("§3.2: root %s = %q, want %q", k, v, want[k])
		}
	}
	if !root.hasClass("hp") {
		r.addf("§3.2: root class %q lacks hp", root.get("class"))
	}
	if m := viewBoxRe.FindStringSubmatch(root.get("viewBox")); m == nil {
		r.addf("§3.2: viewBox %q is not \"0 0 W H\"", root.get("viewBox"))
	} else {
		for _, s := range m[1:] {
			if v, err := strconv.ParseFloat(s, 64); err != nil || !(v > 0) || math.IsInf(v, 0) {
				r.addf("§3.2: viewBox size %q is not a positive number", s)
			}
		}
	}
	for _, a := range []string{"style", "transform"} {
		if _, ok := root.attr(a); ok {
			r.addf("§3.2: root carries %s", a)
		}
	}
	kids := root.elements()
	if len(kids) < 2 || kids[0].name != "title" || kids[0].get("id") != id+"-title" ||
		kids[1].name != "desc" || kids[1].get("id") != id+"-desc" {
		r.addf("§3.3: the first two children are not <title id=%q> and <desc id=%q>", id+"-title", id+"-desc")
	}
	return id
}

// refAttrs are the attributes that load something: href and xlink:href
// (parseDOM keeps only the local name of a prefixed attribute) and src.
// urlAttrs are the presentation attributes that take a url(...) paint
// server, clip, mask, filter or marker. tools/hpcheck/hpcheck.py checks the
// same lists, so the two checkers agree.
var (
	refAttrs = []string{"href", "src"}
	urlAttrs = []string{"clip-path", "fill", "stroke", "mask", "filter", "marker-start", "marker-mid", "marker-end"}
	// urlRe matches url(...) with its target double-quoted, single-quoted
	// or bare (Go's regexp has no backreferences, hence three groups).
	urlRe = regexp.MustCompile(`(?i)url\(\s*(?:"([^"]*)"|'([^']*)'|([^)]*?))\s*\)`)
)

// checkSafety checks §3.6: no script or foreignObject, no style or on*
// attributes and, in a chart, no references to external resources:
// href and src point at an id (#...), and url(...) at an id in the chart.
// A javascript: link counts as external.
func checkSafety(r *specReport, root *xnode, doc string) {
	ids := map[string]bool{}
	root.walk(func(n *xnode) {
		if v, ok := n.attr("id"); ok {
			ids[v] = true
		}
	})
	root.walk(func(n *xnode) {
		if n.name == "script" || n.name == "foreignObject" {
			r.addf("§3.6: %s holds <%s>", doc, n.name)
		}
		for _, a := range n.attrs {
			k, v := a.Name.Local, a.Value
			if k == "style" || strings.HasPrefix(strings.ToLower(k), "on") {
				r.addf("§3.6: %s carries the attribute %s", n.path(), k)
			}
			if doc != "chart" {
				continue
			}
			switch {
			case slices.Contains(refAttrs, k):
				if !strings.HasPrefix(strings.TrimSpace(v), "#") {
					r.addf("§3.6: %s %s=%q refers outside the chart", n.path(), k, v)
				}
			case slices.Contains(urlAttrs, k) && strings.Contains(strings.ToLower(v), "url("):
				refs := urlRe.FindAllStringSubmatch(v, -1)
				if len(refs) == 0 {
					r.addf("§3.6: %s %s=%q is not url(#id)", n.path(), k, v)
				}
				for _, m := range refs {
					ref := m[1] + m[2] + m[3] // only one group matched
					if id, ok := strings.CutPrefix(ref, "#"); !ok || !ids[id] {
						r.addf("§3.6: %s %s=%q does not point at an id in the chart", n.path(), k, v)
					}
				}
			}
		}
	})
}

// checkNoTransform checks the positions rule of §3.3.
func checkNoTransform(r *specReport, root *xnode) {
	check := func(n *xnode) {
		if _, ok := n.attr("transform"); ok {
			r.addf("§3.3: %s carries a transform attribute", n.path())
		}
	}
	check(root)
	for _, s := range []string{"g.hp-marks", "g.hp-labels", "g.hp-axis-x", "g.hp-hover"} {
		for _, g := range query(root, s) {
			g.walk(func(n *xnode) {
				check(n)
				if n != g && (n.name == "svg" || n.name == "symbol" || n.name == "use") {
					r.addf("§3.3: %s holds <%s>", g.path(), n.name)
				}
			})
		}
	}
	for _, s := range []string{"rect.hp-band-hl", ".hp-xa"} {
		for _, n := range query(root, s) {
			check(n)
		}
	}
}

// wireGeometry is the parsed geometry attributes.
type wireGeometry struct {
	ok         bool
	l, t, r, b float64
	xKind      string // "", "time", "linear", "band" or unknown
	x0, x1     float64
}

// canonicalNumber parses s, which must be in the spec's number form.
func canonicalNumber(r *specReport, attr, s string) float64 {
	v, err := strconv.ParseFloat(s, 64)
	if err != nil || math.IsInf(v, 0) || math.IsNaN(v) {
		r.addf("§4.2: %s holds %q, not a finite number", attr, s)
		return 0
	}
	if w := wireNumber(v); w != s {
		r.addf("§1.1: %s holds %q, whose number form is %q", attr, s, w)
	}
	return v
}

func checkGeometry(r *specReport, root *xnode) wireGeometry {
	var g wireGeometry
	plot := strings.Split(root.get("data-hp-plot"), " ")
	if len(plot) != 4 {
		r.addf("§4.2: data-hp-plot %q is not four numbers", root.get("data-hp-plot"))
		return g
	}
	g.l, g.t = canonicalNumber(r, "data-hp-plot", plot[0]), canonicalNumber(r, "data-hp-plot", plot[1])
	g.r, g.b = canonicalNumber(r, "data-hp-plot", plot[2]), canonicalNumber(r, "data-hp-plot", plot[3])
	if !(g.l < g.r && g.t < g.b) {
		r.addf("§4.2: data-hp-plot %q is empty", root.get("data-hp-plot"))
	}
	domain := func(attr string, f []string) (float64, float64) {
		if len(f) != 3 {
			r.addf("§4.2: %s %q is not \"kind d0 d1\"", attr, root.get(attr))
			return 0, 0
		}
		d0, d1 := canonicalNumber(r, attr, f[1]), canonicalNumber(r, attr, f[2])
		if !(d0 < d1) {
			r.addf("§4.2: %s %q has d0 >= d1", attr, root.get(attr))
		}
		return d0, d1
	}
	if x, ok := root.attr("data-hp-x"); ok {
		f := strings.Split(x, " ")
		g.xKind = f[0]
		switch g.xKind {
		case "time", "linear":
			g.x0, g.x1 = domain("data-hp-x", f)
		case "band":
			if len(f) != 1 {
				r.addf("§4.2: data-hp-x %q: band has no domain", x)
			}
		default:
			r.addf("§4.2: data-hp-x %q has an unknown kind", x)
		}
	}
	y := strings.Split(root.get("data-hp-y"), " ")
	if y[0] != "linear" {
		r.addf("§4.2: data-hp-y %q is not linear", root.get("data-hp-y"))
	}
	domain("data-hp-y", y)
	g.ok = true
	return g
}

// checkTips checks §3.5 and §4.3 and returns the payload.
func checkTips(r *specReport, root *xnode, raw []byte, geo wireGeometry, nSeries int, slots map[int]bool) wireTipsPayload {
	var tips wireTipsPayload
	hovers := query(root, "g.hp-hover")
	if len(hovers) != 1 {
		r.addf("§3.3: %d g.hp-hover, want exactly 1", len(hovers))
		return tips
	}
	metas := query(hovers[0], "g.hp-hover > metadata.hp-tips")
	if len(metas) != 1 || len(query(root, "metadata.hp-tips")) != 1 {
		r.addf("§3.3: g.hp-hover holds %d metadata.hp-tips, want exactly 1", len(metas))
		return tips
	}
	// The raw text needs no XML escaping (§3.5).
	if start := bytes.Index(raw, []byte(`<metadata class="hp-tips">`)); start >= 0 {
		body := raw[start+len(`<metadata class="hp-tips">`):]
		if end := bytes.Index(body, []byte("</metadata>")); end >= 0 {
			if i := bytes.IndexAny(body[:end], "<>&\r"); i >= 0 {
				r.addf("§3.5: the raw metadata text holds %q", body[i])
			}
		}
	}
	dec := json.NewDecoder(strings.NewReader(metas[0].text()))
	if err := dec.Decode(&tips); err != nil {
		r.addf("§3.5: metadata is not the payload JSON: %v", err)
		return tips
	}
	n := len(tips.Rows)
	switch tips.Mode {
	case "x", "band":
		if len(tips.Xs) != n {
			r.addf("§3.5: %d xs for %d rows", len(tips.Xs), n)
		}
		for i := 1; i < len(tips.Xs); i++ {
			if tips.Xs[i] < tips.Xs[i-1] {
				r.addf("§3.5: xs decrease at %d: %v", i, tips.Xs)
				break
			}
		}
		for _, x := range tips.Xs {
			if geo.ok && (x < geo.l-0.01 || x > geo.r+0.01) {
				r.addf("§3.5: x %v is outside the plot [%v, %v]", x, geo.l, geo.r)
			}
		}
		if tips.Bw == nil || *tips.Bw < 0 || tips.Mode == "x" && *tips.Bw != 0 {
			r.addf("§3.5: bw %v is wrong for mode %s", tips.Bw, tips.Mode)
		}
		if tips.Top == nil || tips.Bottom == nil || !(*tips.Top < *tips.Bottom) {
			r.addf("§3.5: top/bottom missing or empty")
		} else if geo.ok && (math.Abs(*tips.Top-geo.t) > 0.01 || math.Abs(*tips.Bottom-geo.b) > 0.01) {
			r.addf("§3.5: top/bottom %v/%v differ from the plot %v/%v", *tips.Top, *tips.Bottom, geo.t, geo.b)
		}
		if tips.Mode == "x" {
			if c := query(hovers[0], "g.hp-hover > line.hp-crosshair"); len(c) != 1 || c[0].get("visibility") != "hidden" {
				r.addf("§3.3: mode x needs exactly one hidden line.hp-crosshair in g.hp-hover")
			}
		} else {
			hl := query(root, "rect.hp-band-hl")
			if len(hl) != 1 || hl[0].get("visibility") != "hidden" {
				r.addf("§3.3: mode band needs exactly one hidden rect.hp-band-hl")
			} else if !before(root, hl[0], "g.hp-marks") {
				r.addf("§3.3: rect.hp-band-hl is not before g.hp-marks")
			}
			if geo.xKind != "band" && geo.xKind != "" {
				r.addf("§4.2: mode band with data-hp-x kind %q", geo.xKind)
			}
		}
	case "xy":
		if len(tips.Pts) != n {
			r.addf("§3.5: %d pts for %d rows", len(tips.Pts), n)
		}
		for _, p := range tips.Pts {
			if len(p) != 2 {
				r.addf("§3.5: point %v is not [x, y]", p)
			} else if geo.ok && (p[0] < geo.l-0.01 || p[0] > geo.r+0.01) {
				r.addf("§3.5: point x %v is outside the plot [%v, %v]", p[0], geo.l, geo.r)
			}
		}
	default:
		r.addf("§3.5: unknown mode %q", tips.Mode)
		return tips
	}
	if n > 0 && geo.xKind == "" {
		r.addf("§4.2: %d rows but no data-hp-x", n)
	}
	keys := map[string]bool{}
	for i, row := range tips.Rows {
		if row.T == nil {
			r.addf("§3.5: row %d has no t", i)
		}
		if row.K == nil {
			r.addf("§4.3: row %d has no k", i)
			continue
		}
		k := *row.K
		if tips.Mode != "xy" {
			if keys[k] {
				r.addf("§4.3: row key %q repeats", k)
			}
			keys[k] = true
		}
		checkKey(r, k, geo)
		want := nSeries
		if tips.Mode == "xy" {
			want = 1
		}
		if len(row.V) != want {
			r.addf("§3.5: row %d lists %d series, want %d", i, len(row.V), want)
		}
		for _, e := range row.V {
			var slot int
			var name, val string
			if len(e) != 3 || json.Unmarshal(e[0], &slot) != nil || json.Unmarshal(e[1], &name) != nil || json.Unmarshal(e[2], &val) != nil {
				r.addf("§3.5: row %d entry is not [slot, name, value]", i)
				continue
			}
			if slot != 0 && !slots[slot] {
				r.addf("§3.5: row %d names slot %d, which no series group has", i, slot)
			}
		}
		// §4.2: the geometry attributes describe the scales actually
		// used, so a key mapped through them lands where its row is drawn.
		drawn, has := 0.0, false
		switch {
		case tips.Mode == "x" && i < len(tips.Xs):
			drawn, has = tips.Xs[i], true
		case tips.Mode == "xy" && i < len(tips.Pts) && len(tips.Pts[i]) == 2:
			drawn, has = tips.Pts[i][0], true
		}
		if px, ok := keyX(k, geo); ok && has && math.Abs(px-drawn) > geometryTolerance {
			r.addf("§4.2: row %d key %q maps through data-hp-x to x %v but is drawn at %v", i, k, px, drawn)
		}
	}
	return tips
}

// geometryTolerance is how far, in viewBox units, a key mapped through the
// geometry attributes may land from where its row is drawn: the
// coordinates and the plot are rounded to 2 decimals.
const geometryTolerance = 0.05

// keyX maps a row key through data-hp-x and data-hp-plot (§4.2) as a
// client would, but in extended precision, so the check measures the
// renderer and not its own rounding. ok is false for band axes and keys
// that do not parse.
func keyX(k string, geo wireGeometry) (x float64, ok bool) {
	if !geo.ok || !(geo.x0 < geo.x1) {
		return 0, false
	}
	const prec = 256
	v := new(big.Float).SetPrec(prec)
	switch geo.xKind {
	case "time":
		t, err := time.Parse(time.RFC3339Nano, k)
		if err != nil {
			return 0, false
		}
		v.SetInt64(t.Unix())
		v.Mul(v, big.NewFloat(1000))
		ns := new(big.Float).SetPrec(prec).SetInt64(int64(t.Nanosecond()))
		v.Add(v, ns.Quo(ns, big.NewFloat(1e6)))
	case "linear":
		f, err := strconv.ParseFloat(k, 64)
		if err != nil || math.IsInf(f, 0) {
			return 0, false
		}
		v.SetFloat64(f)
	default:
		return 0, false
	}
	d0 := new(big.Float).SetPrec(prec).SetFloat64(geo.x0)
	span := new(big.Float).SetPrec(prec).SetFloat64(geo.x1)
	span.Sub(span, d0)
	v.Sub(v, d0)
	frac, _ := v.Quo(v, span).Float64()
	return geo.l + frac*(geo.r-geo.l), true
}

// checkKey checks a row key's form and that it lies in the x domain.
func checkKey(r *specReport, k string, geo wireGeometry) {
	switch geo.xKind {
	case "time":
		t, err := time.Parse(time.RFC3339Nano, k)
		if err != nil || timeKey(t) != k || t.Year() < 1 || t.Year() > 9999 {
			// Years 0 and 10000+ parse in Go but not in RFC 3339 parsers
			// such as hx.Selection.Time and Python's datetime.
			r.addf("§1.1: row key %q is not a time value", k)
			return
		}
		if ms := epochMS(t); ms < geo.x0 || ms > geo.x1 {
			r.addf("§3.5: row key %q is outside the x domain", k)
		}
	case "linear":
		v, err := strconv.ParseFloat(k, 64)
		if err != nil || wireNumber(v) != k {
			r.addf("§1.1: row key %q is not in the number form", k)
			return
		}
		if v < geo.x0 || v > geo.x1 {
			r.addf("§3.5: row key %q is outside the x domain [%v, %v]", k, geo.x0, geo.x1)
		}
	}
}

// before reports whether n comes before the first match of sel in
// document order.
func before(root, n *xnode, sel string) bool {
	all := root.descendants()
	ni, si := -1, -1
	for i, e := range all {
		if e == n {
			ni = i
		}
		if si < 0 && len(query(e, sel)) > 0 && query(e, sel)[0] == e {
			si = i
		}
	}
	return ni >= 0 && (si < 0 || ni < si)
}

// checkTable checks §3.8 and the table ids of §4.1.
func checkTable(r *specReport, b []byte, id string, tips wireTipsPayload) {
	if bytes.IndexByte(b, '\r') >= 0 {
		r.addf("§3.6: the table holds a raw CR byte")
	}
	root, err := parseDOM(b)
	if err != nil {
		r.addf("§3.8: table: %v", err)
		return
	}
	checkSafety(r, root, "table")
	if root.name != "table" || !root.hasClass("hp-table") || root.get("id") != id+"-table" {
		r.addf("§3.8: table root is %s, want table.hp-table#%s-table", root.path(), id)
	}
	if k := root.elements(); len(k) == 0 || k[0].name != "caption" {
		r.addf("§3.8: the table has no leading <caption>")
	}
	if len(query(root, `table > thead > tr > th[scope="col"]`)) == 0 {
		r.addf("§3.8: no thead row of th[scope=col]")
	}
	rows := query(root, "table > tbody > tr")
	if len(rows) != len(tips.Rows) {
		r.addf("§3.8: %d table rows for %d metadata rows", len(rows), len(tips.Rows))
	}
	ids := map[string]bool{}
	for i, tr := range rows {
		rid := tr.get("id")
		if !strings.HasPrefix(rid, id+"-r-") || ids[rid] {
			r.addf("§4.1: table row id %q is not a unique %s-r-{rowKey}", rid, id)
		}
		ids[rid] = true
		if tips.Mode != "xy" {
			// A label column may come first (dot labels); the x label is
			// the row's one th[scope=row].
			if len(query(tr, `tr > th[scope="row"]`)) != 1 {
				r.addf("§3.8: row %s is not headed by one th[scope=row]", rid)
			}
		}
		if i < len(tips.Rows) {
			checkTableRow(r, tr, rid, tips.Mode, tips.Rows[i])
		}
		for _, td := range query(tr, "td") {
			if strings.TrimSpace(td.text()) == "–" {
				r.addf("§3.8: row %s writes a missing value as a bare dash", rid)
			}
		}
	}
	for _, sr := range query(root, "td > span.hp-sr") {
		if sr.text() != " no data" {
			r.addf("§3.8: missing-value text %q", sr.text())
		}
	}
}

// checkTableRow checks a table row against the metadata row at the same
// position (§3.8): in modes x and band, its th[scope=row] is the row's
// heading t and the cells after it hold the row's values, one per series
// in series order (the column layout §3.8 recommends, which the reference
// follows); in a chart of only dots, whose columns are the server's
// choice, a cell holds the point's value and the row header is part of
// its heading (the reference heads a labeled point "label · x"). Since
// rows are compared in order, this also checks the order §3.8 requires.
// Text is compared as XML carries it: the controls §3.6 drops are dropped
// from the metadata's strings too.
func checkTableRow(r *specReport, tr *xnode, rid, mode string, row wireTipsRow) {
	heading := ""
	if row.T != nil {
		heading = xmlChars(*row.T)
	}
	var values []string
	for _, e := range row.V {
		var v string
		if len(e) == 3 && json.Unmarshal(e[2], &v) == nil {
			values = append(values, xmlChars(v))
		}
	}
	heads := query(tr, `tr > th[scope="row"]`)
	if mode == "xy" {
		if len(heads) == 1 && !strings.Contains(heading, heads[0].text()) {
			r.addf("§3.8: row %s is headed %q, which its metadata heading %q lacks", rid, heads[0].text(), heading)
		}
		if len(values) == 1 && !slices.Contains(cellValues(tr.elements()), values[0]) {
			r.addf("§3.8: row %s does not hold its metadata value %q", rid, values[0])
		}
		return
	}
	if len(heads) != 1 {
		return // reported by checkTable
	}
	if got := heads[0].text(); got != heading {
		r.addf("§3.8: row %s is headed %q, but its metadata row reads %q", rid, got, heading)
	}
	cells := tr.elements()
	at := slices.Index(cells, heads[0])
	if got := cellValues(cells[at+1:]); !slices.Equal(got, values) {
		r.addf("§3.8: row %s holds %q, but its metadata row %q", rid, got, values)
	}
}

// cellValues returns the text of each td in cells, with the missing-value
// form (§3.8) read as the "–" that the metadata writes for it.
func cellValues(cells []*xnode) []string {
	var out []string
	for _, c := range cells {
		if c.name != "td" {
			continue
		}
		if len(query(c, "td > span.hp-sr")) > 0 {
			out = append(out, missing)
			continue
		}
		out = append(out, c.text())
	}
	return out
}

// xmlChars drops what XML 1.0 cannot carry, as the renderer does in
// markup (spec.md §3.6; Appendix B's _xml_chars).
func xmlChars(s string) string {
	return strings.Map(func(c rune) rune {
		if c == '\t' || c == '\n' || c == '\r' || c >= 0x20 && c <= 0xD7FF || c >= 0xE000 && c <= 0xFFFD || c >= 0x10000 {
			return c
		}
		return -1
	}, s)
}

// assertSpec fails tb with every violation of chart (and table).
func assertSpec(tb testing.TB, name string, chart, table []byte) {
	tb.Helper()
	for _, v := range checkSpec(chart, table) {
		tb.Errorf("%s: %s", name, v)
	}
}

// TestGoldensMeetSpec runs the universal spec checks on every golden
// chart and its table.
func TestGoldensMeetSpec(t *testing.T) {
	for _, gc := range goldenCases {
		t.Run(gc.name, func(t *testing.T) {
			c := gc.chart()
			table, err := c.Table()
			if err != nil {
				t.Fatal(err)
			}
			assertSpec(t, gc.name, mustRender(t, c), []byte(table))
			s := *c
			s.Standalone = true
			assertSpec(t, gc.name+" standalone", mustRender(t, &s), nil)
		})
	}
}

// TestSpecCheckCatchesViolations makes sure the checker is not vacuous:
// each mutation of a valid chart must be reported.
func TestSpecCheckCatchesViolations(t *testing.T) {
	c := &Chart{
		ID: "lt", Title: "Latency", Width: 240, Height: 120, Animate: true,
		Marks: []Mark{Line{Name: "p50", X: minutes(3, 10), Y: []float64{42, 45, 41}}},
	}
	good := mustRender(t, c)
	table, err := c.Table()
	if err != nil {
		t.Fatal(err)
	}
	if v := checkSpec(good, []byte(table)); len(v) > 0 {
		t.Fatalf("valid chart reported: %v", v)
	}
	mutations := []struct{ name, from, to string }{
		{"raw CR", "<title id=\"lt-title\">Latency", "<title id=\"lt-title\">Lat\rency"},
		{"version", `data-hp="1"`, `data-hp=""`},
		{"transform on a tick", `<text class="hp-tick"`, `<text transform="rotate(-45)" class="hp-tick"`},
		{"rounded domain", `data-hp-y="linear 40 46"`, `data-hp-y="linear 40.0 46"`},
		{"reversed domain", `data-hp-y="linear 40 46"`, `data-hp-y="linear 46 40"`},
		{"missing key", `"k":"2026-03-03T14:10:00Z",`, ``},
		{"key outside domain", `"k":"2026-03-03T14:10:00Z"`, `"k":"2026-03-03T15:10:00Z"`},
		{"non-canonical key", `"k":"2026-03-03T14:10:00Z"`, `"k":"2026-03-03T14:10:00.000Z"`},
		{"foreign id", `id="lt-s-2e119f3a"`, `id="other"`},
		{"style", `<g class="hp-hover"`, `<g style="x" class="hp-hover"`},
		{"on* attribute", `<g class="hp-hover"`, `<g onclick="x()" class="hp-hover"`},
		{"focus ring", `<metadata`, `<circle class="hp-focus"/><metadata`},
	}
	for _, m := range mutations {
		if !bytes.Contains(good, []byte(m.from)) {
			t.Fatalf("%s: %q not in the chart:\n%s", m.name, m.from, good)
		}
		bad := bytes.Replace(good, []byte(m.from), []byte(m.to), 1)
		if v := checkSpec(bad, []byte(table)); len(v) == 0 {
			t.Errorf("%s: not reported", m.name)
		}
	}
	for _, m := range []struct{ name, from, to string }{
		{"table id", `id="lt-table"`, `id="x-table"`},
		{"row id", `id="lt-r-`, `id="lt-x-`},
		{"row count", `</tbody>`, `<tr id="lt-r-0"><th scope="row">x</th></tr></tbody>`},
		{"value differs", `<td>42</td>`, `<td>999</td>`},
		{"missing value", `<td>42</td>`, `<td>` + missingCell + `</td>`},
		{"heading differs", `<th scope="row">Mar 3, 2026 2:00 PM</th>`, `<th scope="row">bogus</th>`},
		{"extra cell", `<td>42</td>`, `<td>42</td><td>42</td>`},
	} {
		if !strings.Contains(string(table), m.from) {
			t.Fatalf("%s: %q not in the table:\n%s", m.name, m.from, table)
		}
		bad := strings.Replace(string(table), m.from, m.to, 1)
		if v := checkSpec(good, []byte(bad)); len(v) == 0 {
			t.Errorf("%s: not reported", m.name)
		}
	}
	// Rows out of the metadata's (axis) order.
	trs := regexp.MustCompile(`<tr id="lt-r-[^"]*">.*?</tr>`).FindAllString(string(table), -1)
	if len(trs) != 3 {
		t.Fatalf("want 3 body rows, got %d:\n%s", len(trs), table)
	}
	swapped := strings.Replace(string(table), trs[0]+trs[1], trs[1]+trs[0], 1)
	if swapped == string(table) {
		t.Fatal("rows not swapped")
	}
	if v := checkSpec(good, []byte(swapped)); len(v) == 0 {
		t.Error("swapped rows: not reported")
	}
	// The same for a chart of only dots, whose columns are the server's.
	d := &Chart{ID: "sc", Width: 240, Height: 120, Marks: []Mark{Dot{Name: "a", X: Floats{1, 2}, Y: []float64{3, 4}}}}
	dots := mustRender(t, d)
	dt, err := d.Table()
	if err != nil {
		t.Fatal(err)
	}
	if v := checkSpec(dots, []byte(dt)); len(v) > 0 {
		t.Fatalf("valid dot chart reported: %v", v)
	}
	for _, m := range []struct{ name, from, to string }{
		{"dot value differs", `<td>3</td>`, `<td>5</td>`},
		{"dot heading differs", `<th scope="row">1</th>`, `<th scope="row">7</th>`},
	} {
		if !strings.Contains(string(dt), m.from) {
			t.Fatalf("%s: %q not in the table:\n%s", m.name, m.from, dt)
		}
		if v := checkSpec(dots, []byte(strings.Replace(string(dt), m.from, m.to, 1))); len(v) == 0 {
			t.Errorf("%s: not reported", m.name)
		}
	}
}

// TestSpecCheckExternalReferences checks the §3.6 rule that a chart
// refers to nothing outside itself, with the same cases as
// tools/hpcheck/test_hpcheck.py, so the Go and Python checkers agree. The
// references that stay inside the chart must pass.
func TestSpecCheckExternalReferences(t *testing.T) {
	c := &Chart{
		ID: "lt", Title: "Latency", Width: 240, Height: 120,
		Marks: []Mark{Line{Name: "p50", X: minutes(3, 10), Y: []float64{42, 45, 41}}},
	}
	good := mustRender(t, c)
	const hover = `<g class="hp-hover"`
	const clip = `clip-path="url(#lt-clip)"`
	tests := []struct {
		name, from, to string
		bad            bool
	}{
		{"javascript: link", hover, `<a href="javascript:x()"><text>x</text></a>` + hover, true},
		{"external image", hover, `<image href="https://x.example/i.png"/>` + hover, true},
		{"external use", hover, `<use xlink:href="https://x.example/s.svg#a"/>` + hover, true},
		{"relative src", hover, `<g src="i.png"/>` + hover, true},
		{"external paint", hover, `<g fill="url(https://x.example/f)" class="hp-hover"`, true},
		{"quoted external paint", hover, `<g fill="url('https://x.example/f')" class="hp-hover"`, true},
		{"url() to no id", clip, `clip-path="url(#lt-nope)"`, true},
		{"unclosed url()", clip, `clip-path="url(#lt-clip"`, true},
		{"second url() external", clip, `clip-path="url(#lt-clip) url(https://x.example/c)"`, true},
		{"use of an id", hover, `<use href="#lt-clip"/>` + hover, false},
		{"quoted url() to an id", clip, `clip-path="url(&quot;#lt-clip&quot;)"`, false},
		{"spaced URL() to an id", clip, `clip-path="URL( #lt-clip )"`, false},
		{"plain paint", hover, `<g fill="currentColor" class="hp-hover"`, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if !bytes.Contains(good, []byte(tt.from)) {
				t.Fatalf("%q not in the chart:\n%s", tt.from, good)
			}
			v := checkSpec(bytes.Replace(good, []byte(tt.from), []byte(tt.to), 1), nil)
			got := slices.ContainsFunc(v, func(m string) bool { return strings.HasPrefix(m, "§3.6:") })
			if got != tt.bad {
				t.Errorf("§3.6 reported = %v, want %v: %v", got, tt.bad, v)
			}
		})
	}
}

// TestTableRowsMatchMetadata pins two cases where the table listed rows
// that the metadata leaves out (spec.md §3.8: one row per metadata row):
// a line on a band axis with categories that have no value, and a
// scatter point with a missing y.
func TestTableRowsMatchMetadata(t *testing.T) {
	for _, c := range []*Chart{
		{ID: "lb", Marks: []Mark{Line{Name: "a", X: Categories{"p", "q", "r"}, Y: []float64{nan, 2, nan}}}},
		{ID: "sc", Marks: []Mark{Dot{Name: "a", X: Floats{1, 2, 3}, Y: []float64{1, nan, 3}}}},
	} {
		table, err := c.Table()
		if err != nil {
			t.Fatal(err)
		}
		assertSpec(t, c.ID, mustRender(t, c), []byte(table))
	}
}
