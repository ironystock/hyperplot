package main

import (
	"bytes"
	"context"
	"encoding/xml"
	"errors"
	"go/ast"
	"go/parser"
	"go/printer"
	"go/token"
	"html"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/ironystock/hyperplot"
	"github.com/ironystock/hyperplot/client"
)

func get(t *testing.T, h http.Handler, target string) *httptest.ResponseRecorder {
	t.Helper()
	w := httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequestWithContext(t.Context(), http.MethodGet, target, nil))
	return w
}

func mustBuild(t *testing.T) *gallery {
	t.Helper()
	g, err := build(sections, chartsGo)
	if err != nil {
		t.Fatal(err)
	}
	return g
}

// TestEntriesRender renders every entry on its own: no error, every SVG
// well-formed XML, and each chart's ID is its func's name in kebab case,
// which keeps ids unique and makes a card easy to find in charts.go.
func TestEntriesRender(t *testing.T) {
	n := 0
	for _, sec := range sections {
		for _, e := range sec.Entries {
			n++
			if (e.Chart == nil) == (e.Sparks == nil) {
				t.Errorf("%s: set exactly one of Chart and Sparks", e.Title)
				continue
			}
			if e.Sparks != nil {
				name := funcName(e.Sparks)
				for _, s := range e.Sparks() {
					b, err := s.Render()
					if err != nil {
						t.Errorf("%s: %v", name, err)
						continue
					}
					wellFormed(t, name, string(b))
				}
				continue
			}
			name := funcName(e.Chart)
			c := e.Chart()
			if want := kebab(name); c.ID != want {
				t.Errorf("%s: ID %q, want %q", name, c.ID, want)
			}
			b, err := c.Render()
			if err != nil {
				t.Errorf("%s: %v", name, err)
				continue
			}
			wellFormed(t, name, string(b))
			table, err := c.Table()
			if err != nil {
				t.Errorf("%s: table: %v", name, err)
				continue
			}
			wellFormed(t, name+" table", string(table))
		}
	}
	if n < 20 {
		t.Errorf("only %d entries", n)
	}
}

// TestShownCodeIsTheRenderedFunc checks the gallery's promise: each card's
// code is the source of the very func whose chart it shows, and the
// highlighted HTML reads back as that source.
func TestShownCodeIsTheRenderedFunc(t *testing.T) {
	g := mustBuild(t)
	if len(g.Sections) != len(sections) {
		t.Fatalf("%d sections, want %d", len(g.Sections), len(sections))
	}
	for i, sec := range sections {
		for j, e := range sec.Entries {
			c := g.Sections[i].Cards[j]
			var fn any = e.Chart
			if e.Sparks != nil {
				fn = e.Sparks
			}
			if want := funcName(fn); c.Func != want {
				t.Errorf("card %q shows %s, renders %s", c.Title, c.Func, want)
			}
			if strings.TrimSpace(c.Source) == "" {
				t.Errorf("%s: no source", c.Func)
				continue
			}
			// The source is one whole func declaration of that name, with
			// its doc comment.
			f, err := parser.ParseFile(token.NewFileSet(), "", "package main\n\n"+c.Source, parser.ParseComments)
			if err != nil {
				t.Errorf("%s: source does not parse: %v", c.Func, err)
				continue
			}
			if len(f.Decls) != 1 {
				t.Errorf("%s: source holds %d declarations", c.Func, len(f.Decls))
				continue
			}
			fd, ok := f.Decls[0].(*ast.FuncDecl)
			if !ok || fd.Name.Name != c.Func || fd.Doc == nil {
				t.Errorf("%s: source is not func %s with a doc comment", c.Func, c.Func)
			}
			if got := html.UnescapeString(stripTags(string(c.Code))); got != c.Source {
				t.Errorf("%s: highlighted code reads back as\n%s", c.Func, got)
			}
		}
	}
}

// TestEveryChartFuncIsOnThePage keeps charts.go and the entry list in
// step: every func that returns a chart or sparklines is a card exactly
// once, and the data helpers are everything else.
func TestEveryChartFuncIsOnThePage(t *testing.T) {
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, "charts.go", chartsGo, 0)
	if err != nil {
		t.Fatal(err)
	}
	cards := map[string]int{}
	for _, sec := range sections {
		for _, e := range sec.Entries {
			if e.Chart != nil {
				cards[funcName(e.Chart)]++
			} else {
				cards[funcName(e.Sparks)]++
			}
		}
	}
	for _, d := range f.Decls {
		fd, ok := d.(*ast.FuncDecl)
		if !ok || fd.Type.Results == nil || len(fd.Type.Results.List) != 1 {
			continue
		}
		var b bytes.Buffer
		if err := printer.Fprint(&b, fset, fd.Type.Results.List[0].Type); err != nil {
			t.Fatal(err)
		}
		if typ := b.String(); typ != "*hyperplot.Chart" && typ != "[]hyperplot.Sparkline" {
			continue
		}
		if cards[fd.Name.Name] != 1 {
			t.Errorf("%s returns %s and is on the page %d times, want once", fd.Name.Name, b.String(), cards[fd.Name.Name])
		}
	}
	helpers := html.UnescapeString(stripTags(string(mustBuild(t).Helpers)))
	for name := range cards {
		if strings.Contains(helpers, "func "+name+"(") {
			t.Errorf("%s is a card and a helper", name)
		}
	}
	for _, want := range []string{"var start", "func hours(", "func wave(", "func noise("} {
		if !strings.Contains(helpers, want) {
			t.Errorf("helpers lack %s", want)
		}
	}
}

// TestChartsGoFitsTheCodePanel keeps every line of charts.go within 88
// columns (a tab counts 4, as app.css shows it), the width app.css gives
// the code beside a chart, so no card's code needs scrolling sideways.
func TestChartsGoFitsTheCodePanel(t *testing.T) {
	for i, line := range strings.Split(string(chartsGo), "\n") {
		if n := len([]rune(strings.ReplaceAll(line, "\t", "    "))); n > 88 {
			t.Errorf("charts.go:%d is %d columns wide, more than 88", i+1, n)
		}
	}
}

func TestPage(t *testing.T) {
	h := newServer(mustBuild(t)).routes()
	tests := []struct {
		target, theme, current string
	}{
		{"/", "", "System"},
		{"/?theme=light", "light", "Light"},
		{"/?theme=dark", "dark", "Dark"},
		{"/?theme=" + "%22%3E%3Cscript%3Ealert(1)%3C/script%3E", "", "System"}, // untrusted: dropped
		{"/?theme=DARK", "", "System"},
	}
	for _, tt := range tests {
		w := get(t, h, tt.target)
		if w.Code != http.StatusOK {
			t.Fatalf("%s: status %d", tt.target, w.Code)
		}
		csp := w.Header().Get("Content-Security-Policy")
		if csp != contentSecurityPolicy {
			t.Errorf("%s: CSP = %q", tt.target, csp)
		}
		// The page loads hx-plot.js from this origin and nothing else, as
		// in the static export's policy.
		if !strings.Contains(csp, "script-src 'self';") || strings.Contains(csp, "jsdelivr") {
			t.Errorf("%s: CSP allows more scripts than the page loads: %q", tt.target, csp)
		}
		if got := w.Header().Get("Content-Type"); got != "text/html; charset=utf-8" {
			t.Errorf("%s: Content-Type %q", tt.target, got)
		}
		body := w.Body.String()
		checkMarkup(t, tt.target, body)
		checkCards(t, tt.target, body)
		root := regexp.MustCompile(`<html[^>]*>`).FindString(body)
		if want := `data-theme="` + tt.theme + `"`; tt.theme != "" && !strings.Contains(root, want) {
			t.Errorf("%s: root %s lacks %s", tt.target, root, want)
		}
		if tt.theme == "" && strings.Contains(root, "data-theme") {
			t.Errorf("%s: root %s has a data-theme", tt.target, root)
		}
		if !regexp.MustCompile(`aria-current="true">` + tt.current + `<`).MatchString(body) {
			t.Errorf("%s: %s is not the current theme link", tt.target, tt.current)
		}
		if strings.Count(body, "aria-current") != 1 {
			t.Errorf("%s: %d current theme links", tt.target, strings.Count(body, "aria-current"))
		}
		// No htmx on this page: hx-plot.js runs alone (docs/spec.md §6.10).
		for _, bad := range []string{"htmx.org", "hx-get", "hx-trigger", "htmx-config", "http-equiv"} {
			if strings.Contains(body, bad) {
				t.Errorf("%s: page contains %q", tt.target, bad)
			}
		}
	}
}

func TestAssets(t *testing.T) {
	h := newServer(mustBuild(t)).routes()
	tests := []struct {
		path, contentType string
		status            int
	}{
		{"/static/hx-plot.js", "text/javascript; charset=utf-8", http.StatusOK},
		{"/static/hx-plot.css", "text/css; charset=utf-8", http.StatusOK},
		{"/static/app.css", "text/css; charset=utf-8", http.StatusOK},
		{"/nope", "", http.StatusNotFound},
	}
	for _, tt := range tests {
		w := get(t, h, tt.path)
		if w.Code != tt.status {
			t.Errorf("%s: status %d, want %d", tt.path, w.Code, tt.status)
		}
		if tt.contentType != "" && w.Header().Get("Content-Type") != tt.contentType {
			t.Errorf("%s: Content-Type %q", tt.path, w.Header().Get("Content-Type"))
		}
	}
}

// TestExport checks the -out files: a page per theme with every card,
// relative URLs only, the CSP as a <meta>, and the assets byte for byte.
func TestExport(t *testing.T) {
	dir := t.TempDir()
	if err := export(dir, mustBuild(t)); err != nil {
		t.Fatal(err)
	}
	read := func(name string) string {
		t.Helper()
		b, err := os.ReadFile(filepath.Join(dir, filepath.FromSlash(name)))
		if err != nil {
			t.Fatal(err)
		}
		return string(b)
	}
	assets := map[string]string{
		"static/hx-plot.js":  string(client.JS()),
		"static/hx-plot.css": hyperplot.Stylesheet(),
		"static/app.css":     appCSS,
	}
	for name, want := range assets {
		if read(name) != want {
			t.Errorf("%s differs from what the server serves", name)
		}
	}
	absolute := regexp.MustCompile(`\s(?:href|src)="/`)
	for _, tt := range []struct{ file, theme string }{{"index.html", ""}, {"light.html", "light"}, {"dark.html", "dark"}} {
		body := read(tt.file)
		checkMarkup(t, tt.file, body)
		checkCards(t, tt.file, body)
		if absolute.MatchString(body) {
			t.Errorf("%s: has a root-relative URL: %s", tt.file, absolute.FindString(body))
		}
		if !strings.Contains(body, `<meta http-equiv="Content-Security-Policy" content="`+html.EscapeString(exportPolicy)+`">`) {
			t.Errorf("%s: lacks the CSP meta", tt.file)
		}
		for _, link := range []string{`href="index.html"`, `href="light.html"`, `href="dark.html"`, `src="static/hx-plot.js"`} {
			if !strings.Contains(body, link) {
				t.Errorf("%s: lacks %s", tt.file, link)
			}
		}
		if hasTheme := strings.Contains(body, `<html lang="en" data-theme="`+tt.theme+`">`); hasTheme != (tt.theme != "") {
			t.Errorf("%s: data-theme wrong for %q", tt.file, tt.theme)
		}
	}
}

// TestDeterministic renders the page twice from separate builds.
func TestDeterministic(t *testing.T) {
	page := func() string {
		return get(t, newServer(mustBuild(t)).routes(), "/").Body.String()
	}
	if first, second := page(), page(); first != second {
		t.Error("two builds render different pages")
	}
}

func TestHighlight(t *testing.T) {
	tests := []struct{ in, want string }{
		{"x := 1", `x := <span class="tok-n">1</span>`},
		{`return "<b>&"`, `<span class="tok-k">return</span> <span class="tok-s">&#34;&lt;b&gt;&amp;&#34;</span>`},
		{"// a < b\nfunc f() {}", `<span class="tok-c">// a &lt; b</span>` + "\n" + `<span class="tok-k">func</span> f() {}`},
		{"a <- b", "a &lt;- b"},
		{"s := `raw`", "s := <span class=\"tok-s\">`raw`</span>"},
		{"", ""},
	}
	for _, tt := range tests {
		if got := string(highlight(tt.in)); got != tt.want {
			t.Errorf("highlight(%q) = %q, want %q", tt.in, got, tt.want)
		}
	}
}

// TestAttrScanner checks the attribute reader checkMarkup relies on: it
// finds real attributes and skips text inside quoted values.
func TestAttrScanner(t *testing.T) {
	tag := `<g id="a" data-x="b onerror=c style=d" onclick='e' hidden STYLE=f>`
	var names []string
	for _, a := range attrRE.FindAllStringSubmatch(tag, -1) {
		names = append(names, a[1])
	}
	if got, want := strings.Join(names, " "), "id data-x onclick hidden STYLE"; got != want {
		t.Errorf("attributes %q, want %q", got, want)
	}
}

func TestKebab(t *testing.T) {
	for in, want := range map[string]string{
		"rules":            "rules",
		"lineGaps":         "line-gaps",
		"lineLinearX":      "line-linear-x",
		"lineDirectLabels": "line-direct-labels",
	} {
		if got := kebab(in); got != want {
			t.Errorf("kebab(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestRunStopsOnCancel(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if err := run(ctx, "127.0.0.1:0", http.NotFoundHandler()); err != nil {
		t.Fatal(err)
	}
}

// TestDisplayAddr checks that the logged URL opens in a browser: a
// wildcard listen address such as -addr :9000 logs as localhost.
func TestDisplayAddr(t *testing.T) {
	for _, tc := range []struct {
		addr net.Addr
		want string
	}{
		{&net.TCPAddr{IP: net.IPv6unspecified, Port: 9000}, "localhost:9000"},
		{&net.TCPAddr{IP: net.IPv4zero, Port: 9000}, "localhost:9000"},
		{&net.TCPAddr{IP: net.IPv4(127, 0, 0, 1), Port: 8080}, "127.0.0.1:8080"},
	} {
		if got := displayAddr(tc.addr); got != tc.want {
			t.Errorf("displayAddr(%v) = %q, want %q", tc.addr, got, tc.want)
		}
	}
}

// TestExamplePaths checks that every examples/... path the README and the
// source mention exists.
func TestExamplePaths(t *testing.T) {
	mention := regexp.MustCompile(`examples/[A-Za-z0-9_./-]*[A-Za-z0-9]`)
	for _, name := range []string{"README.md", "main.go"} {
		b, err := os.ReadFile(name)
		if err != nil {
			t.Fatal(err)
		}
		for _, p := range mention.FindAllString(string(b), -1) {
			if _, err := os.Stat(filepath.Join("..", "..", filepath.FromSlash(p))); err != nil {
				t.Errorf("%s mentions %s, which does not exist", name, p)
			}
		}
	}
}

var (
	svgRE    = regexp.MustCompile(`(?s)<svg\b.*?</svg>`)
	tagRE    = regexp.MustCompile(`<[a-zA-Z][^>]*>`)
	attrRE   = regexp.MustCompile(`\s([^\s=/>"']+)(?:\s*=\s*("[^"]*"|'[^']*'|[^\s>"']+))?`)
	scriptRE = regexp.MustCompile(`<script\b[^>]*>`)
	anyTagRE = regexp.MustCompile(`<[^>]*>`)
)

// checkMarkup checks a whole page: every <svg> parses as XML, ids are
// unique page-wide, and there is no style or on* attribute, <style>
// element or inline script, which the CSP would block anyway.
func checkMarkup(t *testing.T, where, body string) {
	t.Helper()
	svgs := svgRE.FindAllString(body, -1)
	if len(svgs) < 20 {
		t.Errorf("%s: only %d <svg> elements", where, len(svgs))
	}
	for _, s := range svgs {
		wellFormed(t, where, s)
	}
	seen := map[string]bool{}
	for _, tag := range tagRE.FindAllString(body, -1) {
		// Attributes one by one, each with its whole quoted value, so text
		// such as an escaped " onerror=" inside a value is not a name.
		for _, a := range attrRE.FindAllStringSubmatch(tag, -1) {
			name, value := strings.ToLower(a[1]), strings.Trim(a[2], `"'`)
			if name == "style" || strings.HasPrefix(name, "on") {
				t.Errorf("%s: forbidden attribute %s in %.120s", where, name, tag)
			}
			if name == "id" {
				if seen[value] {
					t.Errorf("%s: duplicate id %q", where, value)
				}
				seen[value] = true
			}
		}
	}
	for _, s := range scriptRE.FindAllString(body, -1) {
		if !strings.Contains(s, " src=") {
			t.Errorf("%s: inline script %s", where, s)
		}
	}
	if strings.Contains(body, "<style") {
		t.Errorf("%s: has a <style> element", where)
	}
}

// checkCards checks that a page holds every card, chart and table.
func checkCards(t *testing.T, where, body string) {
	t.Helper()
	for _, sec := range sections {
		for _, e := range sec.Entries {
			if e.Sparks != nil {
				if !strings.Contains(body, `id="ex-`+kebab(funcName(e.Sparks))+`"`) {
					t.Errorf("%s: lacks the %s card", where, funcName(e.Sparks))
				}
				continue
			}
			id := e.Chart().ID
			for _, want := range []string{`id="ex-` + id + `"`, `id="` + id + `"`, `id="` + id + `-table"`} {
				if !strings.Contains(body, want) {
					t.Errorf("%s: lacks %s", where, want)
				}
			}
		}
	}
}

func wellFormed(t *testing.T, where, markup string) {
	t.Helper()
	d := xml.NewDecoder(strings.NewReader(markup))
	for {
		_, err := d.Token()
		if errors.Is(err, io.EOF) {
			return
		}
		if err != nil {
			t.Errorf("%s: not well-formed: %v", where, err)
			return
		}
	}
}

func stripTags(s string) string { return anyTagRE.ReplaceAllString(s, "") }
