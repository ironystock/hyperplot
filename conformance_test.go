package hyperplot

import (
	"bytes"
	"encoding/json"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"sort"
	"strings"
	"testing"
	"time"
)

// TestConformance renders every fixture of testdata/conformance (format in
// its README) and checks that the Go reference server reproduces each
// reference file byte for byte and passes every server assertion,
// including those marked should. With -update it rewrites the reference
// files instead; review the diff like a golden file, and remember that a
// change to a reference file is a change to what the spec's fixtures
// promise other implementations.

const conformanceDir = "testdata/conformance"

// confInput is a fixture's input.json.
type confInput struct {
	Spec   int                   `json:"spec"`
	About  string                `json:"about"`
	Chart  *confChart            `json:"chart"`
	Frames map[string]*confChart `json:"frames"`
	Table  bool                  `json:"table"`
}

// confChart describes one chart independently of any language.
type confChart struct {
	ID      string  `json:"id"`
	Title   string  `json:"title"`
	Width   float64 `json:"width"`
	Height  float64 `json:"height"`
	Animate bool    `json:"animate"`
	X       struct {
		TimeDomain []string  `json:"timeDomain"`
		Domain     []float64 `json:"domain"`
	} `json:"x"`
	Y struct {
		Domain []float64 `json:"domain"`
		Unit   string    `json:"unit"`
	} `json:"y"`
	Marks []confMark `json:"marks"`
}

type confMark struct {
	Type string  `json:"type"`
	Name *string `json:"name"`
	X    struct {
		Times      []string  `json:"times"`
		Floats     []float64 `json:"floats"`
		Categories []string  `json:"categories"`
	} `json:"x"`
	Y      []*float64 `json:"y"`
	Labels []string   `json:"labels"`
}

// chart maps the fixture's chart to a Chart.
func (in *confChart) chart() (*Chart, error) {
	c := &Chart{ID: in.ID, Title: in.Title, Width: in.Width, Height: in.Height, Animate: in.Animate}
	c.Y.Unit = in.Y.Unit
	parseTime := func(s string) (time.Time, error) { return time.Parse(time.RFC3339Nano, s) }
	if d := in.X.TimeDomain; d != nil {
		if len(d) != 2 {
			return nil, fmt.Errorf("x.timeDomain needs two times")
		}
		for i, s := range d {
			t, err := parseTime(s)
			if err != nil {
				return nil, err
			}
			c.X.TimeDomain[i] = t
		}
	}
	for name, src := range map[string][]float64{"x.domain": in.X.Domain, "y.domain": in.Y.Domain} {
		if src != nil && len(src) != 2 {
			return nil, fmt.Errorf("%s needs two numbers", name)
		}
	}
	if in.X.Domain != nil {
		c.X.Domain = [2]float64{in.X.Domain[0], in.X.Domain[1]}
	}
	if in.Y.Domain != nil {
		c.Y.Domain = [2]float64{in.Y.Domain[0], in.Y.Domain[1]}
	}
	for mi := range in.Marks {
		m := &in.Marks[mi]
		y := make([]float64, len(m.Y))
		for i, v := range m.Y {
			y[i] = math.NaN()
			if v != nil {
				y[i] = *v
			}
		}
		name := ""
		if m.Name != nil {
			name = *m.Name
		}
		var x Values
		switch {
		case m.X.Times != nil:
			ts := make(Times, len(m.X.Times))
			for i, s := range m.X.Times {
				t, err := parseTime(s)
				if err != nil {
					return nil, err
				}
				ts[i] = t
			}
			x = ts
		case m.X.Floats != nil:
			x = Floats(m.X.Floats)
		case m.X.Categories != nil:
			x = Categories(m.X.Categories)
		default:
			return nil, fmt.Errorf("mark %d has no x values", mi)
		}
		switch m.Type {
		case "line":
			c.Marks = append(c.Marks, Line{Name: name, X: x, Y: y})
		case "area":
			c.Marks = append(c.Marks, Area{Name: name, X: x, Y: y})
		case "dot":
			c.Marks = append(c.Marks, Dot{Name: name, X: x, Y: y, Labels: m.Labels})
		case "bar":
			cats, ok := x.(Categories)
			if !ok {
				return nil, fmt.Errorf("mark %d: bars need categories", mi)
			}
			c.Marks = append(c.Marks, Bar{Name: name, X: cats, Y: y})
		default:
			return nil, fmt.Errorf("mark %d: unknown type %q", mi, m.Type)
		}
	}
	return c, nil
}

// confChecks is a fixture's checks.json (the client part is not read
// here).
type confChecks struct {
	Fixture   string            `json:"fixture"`
	Level     int               `json:"level"`
	Input     string            `json:"input"`
	Reference map[string]string `json:"reference"`
	Forbid    struct {
		Elements     []string `json:"elements"`
		Attrs        []string `json:"attrs"`
		AttrPrefixes []string `json:"attrPrefixes"`
		Where        []struct {
			Sel      string   `json:"sel"`
			Attrs    []string `json:"attrs"`
			Elements []string `json:"elements"`
			Deep     bool     `json:"deep"`
		} `json:"where"`
	} `json:"forbid"`
	Raw struct {
		Absent []string `json:"absent"`
	} `json:"raw"`
	TipsRaw struct {
		Absent []string `json:"absent"`
	} `json:"tipsRaw"`
	Server []confAssert `json:"server"`
	Unique []struct {
		Sel  string `json:"sel"`
		Attr string `json:"attr"`
		In   string `json:"in"`
	} `json:"unique"`
	Stable []struct {
		Sel  string `json:"sel"`
		Attr string `json:"attr"`
	} `json:"stable"`
}

type confAssert struct {
	Sel     string            `json:"sel"`
	In      string            `json:"in"`
	Frame   string            `json:"frame"`
	Count   *int              `json:"count"`
	Classes []string          `json:"classes"`
	Attrs   map[string]string `json:"attrs"`
	Match   map[string]string `json:"match"`
	Text    *string           `json:"text"`
	Tips    *struct {
		Mode  *string   `json:"mode"`
		Count *int      `json:"count"`
		K     *[]string `json:"k"`
		Names *[]string `json:"names"`
		Slots *[][2]any `json:"slots"`
	} `json:"tips"`
	Should bool   `json:"should"`
	Note   string `json:"note"`
}

// confDoc is one reference document: a chart (or a slide frame) or a
// table.
type confDoc struct {
	name  string // "chart", "table" or a frame name
	chart bool
	raw   []byte
	root  *xnode
}

func TestConformance(t *testing.T) {
	dirs, err := filepath.Glob(filepath.Join(conformanceDir, "*", "checks.json"))
	if err != nil || len(dirs) == 0 {
		t.Fatalf("no fixtures in %s: %v", conformanceDir, err)
	}
	for _, path := range dirs {
		dir := filepath.Dir(path)
		t.Run(filepath.Base(dir), func(t *testing.T) { runFixture(t, dir) })
	}
}

func decodeStrict(path string, v any) error {
	b, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	dec := json.NewDecoder(bytes.NewReader(b))
	dec.DisallowUnknownFields()
	return dec.Decode(v)
}

func runFixture(t *testing.T, dir string) {
	t.Helper()
	var checks confChecks
	b, err := os.ReadFile(filepath.Join(dir, "checks.json"))
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(b, &checks); err != nil {
		t.Fatal(err)
	}
	var in confInput
	if err := decodeStrict(filepath.Join(dir, checks.Input), &in); err != nil {
		t.Fatalf("%s: %v", checks.Input, err)
	}
	if in.Spec != 1 {
		t.Fatalf("input is for spec %d", in.Spec)
	}

	// Render every reference document.
	rendered := map[string][]byte{}
	charts := map[string]*Chart{}
	if in.Frames != nil {
		for name, fc := range in.Frames {
			c, err := fc.chart()
			if err != nil {
				t.Fatalf("frame %s: %v", name, err)
			}
			charts[name] = c
		}
	} else {
		c, err := in.Chart.chart()
		if err != nil {
			t.Fatal(err)
		}
		charts["chart"] = c
	}
	for name, c := range charts {
		out, err := c.Render()
		if err != nil {
			t.Fatalf("%s: Render: %v", name, err)
		}
		rendered[name] = out
		if name == "chart" && in.Table {
			table, err := c.Table()
			if err != nil {
				t.Fatalf("Table: %v", err)
			}
			rendered["table"] = []byte(table)
		}
	}

	// Compare with (or rewrite) the reference files.
	names := sortedKeys(checks.Reference)
	if len(names) != len(rendered) {
		t.Errorf("references %v, but the input renders %d documents", names, len(rendered))
	}
	var docs []confDoc
	for _, name := range names {
		file := filepath.Join(dir, checks.Reference[name])
		got, ok := rendered[name]
		if !ok {
			t.Errorf("reference %s: the input renders no such document", name)
			continue
		}
		if *update {
			if err := os.WriteFile(file, got, 0o644); err != nil {
				t.Fatal(err)
			}
		}
		want, err := os.ReadFile(file)
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(got, want) {
			t.Errorf("%s differs from the reference file %s at byte %d; run go test . -run Conformance -update and review the diff\n%s",
				name, file, firstDiff(got, want), diffContext(got, want))
		}
		root, err := parseDOM(want)
		if err != nil {
			t.Fatalf("%s: %v", file, err)
		}
		docs = append(docs, confDoc{name: name, chart: name != "table", raw: want, root: root})
	}
	byName := map[string]confDoc{}
	for _, d := range docs {
		byName[d.name] = d
	}

	// The universal MUSTs of the spec, on each chart with its table.
	for _, d := range docs {
		if !d.chart {
			continue
		}
		var table []byte
		if d.name == "chart" {
			table = byName["table"].raw
		}
		assertSpec(t, d.name, d.raw, table)
	}

	checkForbid(t, checks, docs)
	for i := range checks.Server {
		a := &checks.Server[i]
		name := fmt.Sprintf("server[%d] %s", i, a.Sel)
		if a.Should {
			name += " (should)"
		}
		doc := "chart"
		switch {
		case a.Frame != "":
			doc = a.Frame
		case a.In != "" && a.In != "chart":
			doc = a.In
		}
		d, ok := byName[doc]
		if !ok {
			t.Errorf("%s: no document %q", name, doc)
			continue
		}
		for _, e := range checkAssert(a, d.root) {
			t.Errorf("%s in %s: %s", name, doc, e)
		}
	}
	for _, u := range checks.Unique {
		in := u.In
		if in == "" {
			in = "chart"
		}
		for _, d := range docs {
			if d.name != in && (in != "chart" || !d.chart) {
				continue
			}
			seen := map[string]bool{}
			for _, n := range query(d.root, u.Sel) {
				v := n.get(u.Attr)
				if seen[v] {
					t.Errorf("unique %s %s in %s: %q repeats", u.Sel, u.Attr, d.name, v)
				}
				seen[v] = true
			}
		}
	}
	for _, s := range checks.Stable {
		var first []string
		for i, d := range docs {
			var vals []string
			for _, n := range query(d.root, s.Sel) {
				vals = append(vals, n.get(s.Attr))
			}
			if i == 0 {
				first = vals
			} else if !slices.Equal(vals, first) {
				t.Errorf("stable %s %s: %s has %q, %s has %q", s.Sel, s.Attr, docs[0].name, first, d.name, vals)
			}
		}
	}
}

// checkForbid runs forbid, raw.absent and tipsRaw.absent on every
// document.
func checkForbid(t *testing.T, checks confChecks, docs []confDoc) {
	t.Helper()
	f := checks.Forbid
	for _, d := range docs {
		d.root.walk(func(n *xnode) {
			if slices.Contains(f.Elements, n.name) {
				t.Errorf("forbid: %s holds <%s> at %s", d.name, n.name, n.path())
			}
			for _, a := range n.attrs {
				name := a.Name.Local
				if slices.Contains(f.Attrs, name) {
					t.Errorf("forbid: %s carries %s", n.path(), name)
				}
				for _, p := range f.AttrPrefixes {
					if strings.HasPrefix(name, p) {
						t.Errorf("forbid: %s carries %s (prefix %s)", n.path(), name, p)
					}
				}
			}
		})
		for _, w := range f.Where {
			for _, g := range query(d.root, w.Sel) {
				targets := []*xnode{g}
				if w.Deep {
					targets = append(targets, g.descendants()...)
				}
				for _, n := range targets {
					for _, a := range w.Attrs {
						if _, ok := n.attr(a); ok {
							t.Errorf("forbid.where %s: %s carries %s", w.Sel, n.path(), a)
						}
					}
				}
				for _, n := range g.descendants() {
					if slices.Contains(w.Elements, n.name) {
						t.Errorf("forbid.where %s: holds <%s> at %s", w.Sel, n.name, n.path())
					}
				}
			}
		}
		for _, s := range checks.Raw.Absent {
			if bytes.Contains(d.raw, []byte(s)) {
				t.Errorf("raw: %s holds %q", d.name, s)
			}
		}
		if d.chart {
			for _, m := range query(d.root, "metadata.hp-tips") {
				// The raw text: between the start tag and </metadata>.
				start := bytes.Index(d.raw, []byte(`<metadata class="hp-tips">`))
				end := bytes.Index(d.raw, []byte("</metadata>"))
				if start < 0 || end < start {
					t.Errorf("tipsRaw: cannot find the metadata text of %s", m.path())
					continue
				}
				text := d.raw[start+len(`<metadata class="hp-tips">`) : end]
				for _, s := range checks.TipsRaw.Absent {
					if bytes.Contains(text, []byte(s)) {
						t.Errorf("tipsRaw: %s's metadata holds %q", d.name, s)
					}
				}
			}
		}
	}
}

// checkAssert runs one server assertion and returns its failures.
func checkAssert(a *confAssert, root *xnode) []string {
	sel, err := parseSelector(a.Sel)
	if err != nil {
		return []string{err.Error()}
	}
	matches := selectAll(root, sel)
	var errs []string
	fail := func(format string, args ...any) { errs = append(errs, fmt.Sprintf(format, args...)) }
	if a.Count != nil {
		if len(matches) != *a.Count {
			fail("%d matches, want %d", len(matches), *a.Count)
		}
	} else if len(matches) == 0 {
		fail("no match")
	}
	for _, n := range matches {
		for _, c := range a.Classes {
			if !n.hasClass(c) {
				fail("%s lacks class %s", n.path(), c)
			}
		}
		for _, k := range sortedKeys(a.Attrs) {
			if v, ok := n.attr(k); !ok || v != a.Attrs[k] {
				fail("%s: %s = %q, want %q", n.path(), k, v, a.Attrs[k])
			}
		}
		for _, k := range sortedKeys(a.Match) {
			re, err := regexp.Compile(a.Match[k])
			if err != nil {
				fail("bad pattern %q: %v", a.Match[k], err)
				continue
			}
			if v, ok := n.attr(k); !ok || !re.MatchString(v) {
				fail("%s: %s = %q does not match %s", n.path(), k, v, a.Match[k])
			}
		}
		if a.Text != nil && n.text() != *a.Text {
			fail("%s: text %q, want %q", n.path(), n.text(), *a.Text)
		}
		if a.Tips != nil {
			errs = append(errs, checkTipsAssert(a, n)...)
		}
	}
	return errs
}

func checkTipsAssert(a *confAssert, n *xnode) []string {
	var tips struct {
		Mode string `json:"mode"`
		Rows []struct {
			K string  `json:"k"`
			V [][]any `json:"v"`
		} `json:"rows"`
	}
	if err := json.Unmarshal([]byte(n.text()), &tips); err != nil {
		return []string{"tips: " + err.Error()}
	}
	var errs []string
	w := a.Tips
	if w.Mode != nil && tips.Mode != *w.Mode {
		errs = append(errs, fmt.Sprintf("tips mode %q, want %q", tips.Mode, *w.Mode))
	}
	if w.Count != nil && len(tips.Rows) != *w.Count {
		errs = append(errs, fmt.Sprintf("tips: %d rows, want %d", len(tips.Rows), *w.Count))
	}
	if w.K != nil {
		keys := []string{}
		for _, r := range tips.Rows {
			keys = append(keys, r.K)
		}
		if !slices.Equal(keys, *w.K) {
			errs = append(errs, fmt.Sprintf("tips k %q, want %q", keys, *w.K))
		}
	}
	names, slots := []string{}, []string{}
	seenN, seenS := map[string]bool{}, map[string]bool{}
	for _, r := range tips.Rows {
		for _, e := range r.V {
			if len(e) != 3 {
				errs = append(errs, fmt.Sprintf("tips entry %v is not [slot, name, value]", e))
				continue
			}
			name, _ := e[1].(string)
			if !seenN[name] {
				seenN[name] = true
				names = append(names, name)
			}
			pair := fmt.Sprint(e[0], "|", name)
			if !seenS[pair] {
				seenS[pair] = true
				slots = append(slots, pair)
			}
		}
	}
	if w.Names != nil && !slices.Equal(names, *w.Names) {
		errs = append(errs, fmt.Sprintf("tips names %q, want %q", names, *w.Names))
	}
	if w.Slots != nil {
		want := []string{}
		for _, p := range *w.Slots {
			want = append(want, fmt.Sprint(p[0], "|", p[1]))
		}
		if !slices.Equal(slots, want) {
			errs = append(errs, fmt.Sprintf("tips slots %q, want %q", slots, want))
		}
	}
	return errs
}

// stylesheetFile is the reference stylesheet shipped in the npm package
// for servers in other languages (docs/spec.md §3.4).
const stylesheetFile = "client/hx-plot.css"

// TestStylesheetFile checks that client/hx-plot.css is Stylesheet() byte
// for byte. Run go test . -run StylesheetFile -update to write it after a
// change to Theme.CSS.
func TestStylesheetFile(t *testing.T) {
	css := []byte(Stylesheet())
	if *update {
		if err := os.WriteFile(stylesheetFile, css, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	got, err := os.ReadFile(stylesheetFile)
	if os.IsNotExist(err) {
		// The npm package ships it (package.json "files"), and npm would
		// pack without it silently.
		t.Fatalf("%s is missing; write it with go test . -run StylesheetFile -update", stylesheetFile)
	}
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, css) {
		t.Errorf("%s differs from Stylesheet() at byte %d; run go test . -run StylesheetFile -update\n%s",
			stylesheetFile, firstDiff(css, got), diffContext(css, got))
	}
}

func sortedKeys(m map[string]string) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// firstDiff returns the offset of the first byte where a and b differ.
func firstDiff(a, b []byte) int {
	n := min(len(a), len(b))
	for i := range n {
		if a[i] != b[i] {
			return i
		}
	}
	return n
}

// diffContext shows both documents around their first difference.
func diffContext(got, want []byte) string {
	i := firstDiff(got, want)
	cut := func(b []byte) string {
		lo, hi := max(0, i-80), min(len(b), i+80)
		return fmt.Sprintf("%q", b[lo:hi])
	}
	return "got:  " + cut(got) + "\nwant: " + cut(want)
}
