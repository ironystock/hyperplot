// Package docs holds tests for the Go samples in hyperplot's
// documentation. It has no importable code.
//
// The tests extract every Go code block from the Markdown guides and the
// hx package documentation, then
//
//   - compile them against the current API, so a renamed function or a
//     pointer-receiver mistake fails CI instead of a reader's build, and
//   - check them for patterns that misbehave under htmx 4 even though they
//     compile.
//
// Samples are fragments: they call helpers such as latencyChart or
// metrics.Latency that the reader supplies. The compile check declares
// those helpers, with the types the documentation gives them, in a
// prelude file (see [prelude]), so everything a sample does with their
// results is type-checked too. It ignores only unused variables and
// imports; an undefined name fails the test, so a new reader-supplied
// helper must be added to the prelude.
package docs

import (
	"bufio"
	"bytes"
	"fmt"
	"go/ast"
	"go/doc/comment"
	"go/parser"
	"go/token"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"testing"
)

// root is the module root, relative to this package's directory.
const root = ".."

// markdownDocs and godocFiles are the documents whose Go samples are
// checked, relative to the module root. DESIGN.md is left out on purpose:
// its code blocks are API outlines with elided bodies, not samples.
var (
	markdownDocs = []string{"README.md", "docs/htmx.md", "client/README.md"}
	godocFiles   = []string{"hx/doc.go"}
)

// snippet is one Go code block from a document.
type snippet struct {
	doc  string // path relative to the module root
	line int    // 1-based line of the block's first code line in doc
	code string
}

func (s snippet) String() string { return fmt.Sprintf("%s:%d", s.doc, s.line) }

func loadSnippets(t *testing.T) []snippet {
	t.Helper()
	var all []snippet
	for _, doc := range markdownDocs {
		all = append(all, markdownSnippets(t, doc)...)
	}
	for _, doc := range godocFiles {
		all = append(all, godocSnippets(t, doc)...)
	}
	if len(all) == 0 {
		t.Fatal("found no Go snippets; is the extractor broken?")
	}
	return all
}

// markdownSnippets returns the ```go fenced blocks of a Markdown file.
func markdownSnippets(t *testing.T, doc string) []snippet {
	t.Helper()
	src, err := os.ReadFile(filepath.Join(root, doc))
	if err != nil {
		t.Fatal(err)
	}
	var (
		out   []snippet
		cur   *snippet
		lines []string
	)
	sc := bufio.NewScanner(bytes.NewReader(src))
	for n := 1; sc.Scan(); n++ {
		text := sc.Text()
		trimmed := strings.TrimSpace(text)
		switch {
		case cur == nil && trimmed == "```go":
			cur = &snippet{doc: doc, line: n + 1}
			lines = lines[:0]
		case cur != nil && strings.HasPrefix(trimmed, "```"):
			cur.code = strings.Join(lines, "\n") + "\n"
			out = append(out, *cur)
			cur = nil
		case cur != nil:
			lines = append(lines, text)
		}
	}
	if err := sc.Err(); err != nil {
		t.Fatal(err)
	}
	if cur != nil {
		t.Fatalf("%s: unterminated code block", cur)
	}
	return out
}

// godocSnippets returns the code blocks of a Go file's package comment
// that look like Go (HTML samples are skipped).
func godocSnippets(t *testing.T, doc string) []snippet {
	t.Helper()
	path := filepath.Join(root, doc)
	src, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	f, err := parser.ParseFile(token.NewFileSet(), path, src, parser.ParseComments|parser.PackageClauseOnly)
	if err != nil {
		t.Fatal(err)
	}
	if f.Doc == nil {
		t.Fatalf("%s: no package comment", doc)
	}
	var p comment.Parser
	var out []snippet
	for _, block := range p.Parse(f.Doc.Text()).Content {
		code, ok := block.(*comment.Code)
		if !ok || strings.HasPrefix(strings.TrimSpace(code.Text), "<") {
			continue
		}
		first, _, _ := strings.Cut(code.Text, "\n")
		out = append(out, snippet{doc: doc, line: lineOf(src, "//\t"+first), code: code.Text})
	}
	return out
}

// lineOf returns the 1-based line of the first line of src equal to want,
// or 0.
func lineOf(src []byte, want string) int {
	for i, l := range strings.Split(string(src), "\n") {
		if strings.TrimRight(l, "\r") == want {
			return i + 1
		}
	}
	return 0
}

// knownImports maps the package names that samples use without an import
// declaration to their import paths.
var knownImports = map[string]string{
	"bytes":    "bytes",
	"client":   "github.com/ironystock/hyperplot/client",
	"context":  "context",
	"errors":   "errors",
	"fmt":      "fmt",
	"html":     "html",
	"http":     "net/http",
	"httptest": "net/http/httptest",
	"hx":       "github.com/ironystock/hyperplot/hx",
	"io":       "io",
	"log":      "log",
	"math":     "math",
	"plot":     "github.com/ironystock/hyperplot/plot",
	"scale":    "github.com/ironystock/hyperplot/scale",
	"strconv":  "strconv",
	"strings":  "strings",
	"sync":     "sync",
	"template": "html/template",
	"testing":  "testing",
	"time":     "time",
	"url":      "net/url",
}

var leadingImports = regexp.MustCompile(`^\s*(?:import\s+(?:\([^)]*\)|"[^"]*")\s*)+`)

// goFile is a snippet turned into a compilable Go file.
type goFile struct {
	src    string
	offset int // file line minus snippet line
	main   bool
	ast    *ast.File
}

// toFile wraps a snippet so that it parses as a Go file in package pkg:
// declarations stay at file level, statements go into a function body,
// and missing imports are added for the package names the code uses.
func toFile(s snippet, pkg string) (goFile, error) {
	if strings.HasPrefix(strings.TrimSpace(s.code), "package ") {
		f, err := parse(s.code)
		return goFile{src: s.code, offset: 0, main: true, ast: f}, err
	}
	imports, body := "", s.code
	if m := leadingImports.FindString(body); m != "" {
		imports, body = m, body[len(m):]
	}
	if imports == "" {
		imports = autoImports(body)
	}
	header := "package " + pkg + "\n\n" + imports + "\n"
	src := header + body
	if f, err := parse(src); err == nil {
		// The snippet's own lines start after the header; an explicit
		// import block is part of the snippet.
		return goFile{src: src, offset: lineCount(header) - lineCount(s.code[:len(s.code)-len(body)]), ast: f}, nil
	}
	header += "func _() {\n"
	src = header + body + "}\n"
	f, err := parse(src)
	return goFile{src: src, offset: lineCount(header) - lineCount(s.code[:len(s.code)-len(body)]), ast: f}, err
}

func parse(src string) (*ast.File, error) {
	return parser.ParseFile(token.NewFileSet(), "snippet.go", src, parser.SkipObjectResolution)
}

func lineCount(s string) int { return strings.Count(s, "\n") }

func autoImports(code string) string {
	var paths []string
	for name, path := range knownImports {
		if regexp.MustCompile(`\b` + name + `\.`).MatchString(code) {
			paths = append(paths, strconv.Quote(path))
		}
	}
	if len(paths) == 0 {
		return ""
	}
	sort.Strings(paths)
	return "import (\n\t" + strings.Join(paths, "\n\t") + "\n)\n"
}

// TestSnippetsParse checks that every sample is syntactically valid Go.
func TestSnippetsParse(t *testing.T) {
	for _, s := range loadSnippets(t) {
		if _, err := toFile(s, "p"); err != nil {
			t.Errorf("%s: sample does not parse: %v", s, err)
		}
	}
}

// prelude declares the names that samples use without defining: helpers
// the reader writes (latencyChart), values a fragment takes from its
// surroundings (w, r, svg), and data sources (metrics). Each is declared
// with the type the documentation gives it, so the compile check also
// checks what a sample does with the result: a renamed Chart method, a
// changed return type or a wrong argument to hx.Tag or Stream.Send fails.
// Without a declaration, the type checker would give the result an
// invalid type and report nothing further about it.
//
// A document that declares a name itself gets its own declaration, not
// the prelude's. Names starting with "sample" are support types.
var prelude = []struct{ name, src string }{
	// Reader-supplied helpers.
	{"latencyChart", "func latencyChart(width, height float64) *plot.Chart {\n\treturn &plot.Chart{ID: \"latency\", Width: width, Height: height}\n}"},
	{"throughputChart", "func throughputChart() *plot.Chart { return &plot.Chart{ID: \"throughput\"} }"},
	{"load", "func load(region, window string) sampleData { return sampleData{} }"},
	{"renderCharts", "func renderCharts(w http.ResponseWriter, region string) {}"},
	{"renderPage", "func renderPage(w http.ResponseWriter, region string) {}"},
	{"metrics", "var metrics sampleMetrics"},
	{"tmpl", "var tmpl *template.Template"},
	{"mux", "var mux *http.ServeMux"},

	// Values a fragment takes from the handler around it.
	{"w", "var w http.ResponseWriter"},
	{"r", "var r *http.Request"},
	{"stream", "var stream *hx.Stream"},
	{"svg", "var svg []byte"},
	{"p99Tile", "var p99Tile []byte"},
	{"errorsTile", "var errorsTile []byte"},
	{"region", "var region string"},
	{"data", "var data sampleData"},
	{"marks", "var marks []plot.Mark"},
	{"times", "var times []time.Time"},
	{"ts", "var ts []time.Time"},
	{"p50", "var p50 []float64"},
	{"p99", "var p99 []float64"},
	{"eu", "var eu []float64"},
	{"us", "var us []float64"},

	// Support types.
	{"sampleMetrics", "type sampleMetrics interface {\n\tLatency() (ts []time.Time, p50, p99 []float64)\n\tSnapshot() sampleSnapshot\n}"},
	{"sampleSnapshot", "type sampleSnapshot struct {\n\tP99     []float64\n\tP99Last float64\n}"},
	{"sampleData", "type sampleData struct{ Regions []sampleRegion }"},
	{"sampleRegion", "type sampleRegion struct {\n\tName   string\n\tTimes  []time.Time\n\tValues []float64\n}"},
}

// preludeFile returns a Go file in package pkg that declares every
// prelude name not in declared.
func preludeFile(pkg string, declared map[string]bool) string {
	var body strings.Builder
	for _, d := range prelude {
		if !declared[d.name] {
			body.WriteString(d.src + "\n\n")
		}
	}
	return "package " + pkg + "\n\n" + autoImports(body.String()) + "\n" + body.String()
}

// topLevelNames adds the names that f declares at file level to names.
func topLevelNames(f *ast.File, names map[string]bool) {
	for _, decl := range f.Decls {
		switch d := decl.(type) {
		case *ast.FuncDecl:
			if d.Recv == nil {
				names[d.Name.Name] = true
			}
		case *ast.GenDecl:
			for _, spec := range d.Specs {
				switch s := spec.(type) {
				case *ast.TypeSpec:
					names[s.Name.Name] = true
				case *ast.ValueSpec:
					for _, n := range s.Names {
						names[n.Name] = true
					}
				}
			}
		}
	}
}

// ignoredCompileErrors are errors that come from a sample being a
// fragment: variables and imports a fragment leaves unused. Undefined
// names are not ignored: declare a reader-supplied name in [prelude].
var ignoredCompileErrors = regexp.MustCompile(`(?:declared|imported) and not used|(?:is not used)$`)

var compileError = regexp.MustCompile(`^(?:\./)?(s\d+)/(\w+)\.go:(\d+):(?:\d+:)? (.*)$`)

// TestSnippetsCompile type-checks every sample against the current API
// by building it in a throwaway module that replaces hyperplot with this
// checkout.
func TestSnippetsCompile(t *testing.T) {
	for _, p := range compileSnippets(t, loadSnippets(t)) {
		t.Error(p)
	}
}

// TestSnippetsCompileChecksHelperResults guards the compile check itself:
// misuse of a reader-supplied helper's result must be reported, not hidden
// behind an "undefined" error, and an undeclared helper must be reported.
func TestSnippetsCompileChecksHelperResults(t *testing.T) {
	snips := []snippet{
		{doc: "check.md", line: 1, code: "svg, err := latencyChart(640, 320).RenderTheWrongName(42)\n_, _ = svg, err\n"},
		{doc: "check.md", line: 10, code: "chart := throughputChart()\nvar s string = chart.Render()\n_ = s\n"},
		{doc: "check.md", line: 20, code: "stream.Send(\"not bytes\")\n"},
		{doc: "check.md", line: 30, code: "notInThePrelude(w, r)\n"},
	}
	problems := compileSnippets(t, snips)
	all := strings.Join(problems, "\n")
	for _, want := range []string{
		"check.md:1: sample does not compile: latencyChart(640, 320).RenderTheWrongName undefined",
		"check.md:11: sample does not compile: multiple-value chart.Render()",
		"check.md:20: sample does not compile: cannot use \"not bytes\"",
		"check.md:30: sample does not compile: undefined: notInThePrelude",
	} {
		if !strings.Contains(all, want) {
			t.Errorf("compile check did not report %q; got:\n%s", want, all)
		}
	}
}

// compileSnippets builds snips in a throwaway module that replaces
// hyperplot with this checkout, and returns one message per compile
// error that is not in ignoredCompileErrors.
func compileSnippets(t *testing.T, snips []snippet) []string {
	t.Helper()
	if testing.Short() {
		t.Skip("builds the samples with the go command")
	}
	goTool, err := exec.LookPath("go")
	if err != nil {
		t.Skip("go command not found")
	}
	modRoot, err := filepath.Abs(root)
	if err != nil {
		t.Fatal(err)
	}

	dir := t.TempDir()
	gomod := "module hyperplot.test/snippets\n\ngo 1.24\n\n" +
		"require github.com/ironystock/hyperplot v0.0.0\n\n" +
		"replace github.com/ironystock/hyperplot => " + strconv.Quote(filepath.ToSlash(modRoot)) + "\n"
	writeFile(t, filepath.Join(dir, "go.mod"), gomod)

	// One package per document, so samples in different documents may
	// declare the same names. A complete program gets its own package
	// and no prelude: it must compile as written.
	type origin struct {
		s      snippet
		offset int
	}
	where := map[string]origin{}             // "sN/fM" -> snippet
	pkgOf := map[string]string{}             // doc -> package dir
	declared := map[string]map[string]bool{} // fragment package dir -> top-level names
	for i, s := range snips {
		f, err := toFile(s, "p")
		if err != nil {
			continue // reported by TestSnippetsParse
		}
		pkg := pkgOf[s.doc]
		if f.main || pkg == "" {
			pkg = fmt.Sprintf("s%d", i)
			if !f.main {
				pkgOf[s.doc] = pkg
				declared[pkg] = map[string]bool{}
			}
		}
		if names := declared[pkg]; names != nil {
			topLevelNames(f.ast, names)
		}
		name := fmt.Sprintf("f%d", i)
		where[pkg+"/"+name] = origin{s, f.offset}
		writeFile(t, filepath.Join(dir, pkg, name+".go"), f.src)
	}
	for pkg, names := range declared {
		writeFile(t, filepath.Join(dir, pkg, "prelude.go"), preludeFile("p", names))
	}

	cmd := exec.CommandContext(t.Context(), goTool, "build", "-gcflags=-e", "./...")
	cmd.Dir = dir
	cmd.Env = append(os.Environ(),
		"GOFLAGS=-mod=mod", "GOWORK=off", "GOPROXY=off", "GOTOOLCHAIN=local", "GO111MODULE=on")
	out, err := cmd.CombinedOutput()
	if err == nil {
		return nil
	}
	var problems []string
	ignored := 0
	for _, line := range strings.Split(strings.ReplaceAll(string(out), "\r\n", "\n"), "\n") {
		line = filepath.ToSlash(line)
		m := compileError.FindStringSubmatch(line)
		if m == nil {
			continue
		}
		if ignoredCompileErrors.MatchString(m[4]) {
			ignored++
			continue
		}
		o, ok := where[m[1]+"/"+m[2]]
		if !ok { // the prelude itself no longer compiles
			problems = append(problems, fmt.Sprintf("prelude (%s): %s", m[1], m[4]))
			continue
		}
		n, _ := strconv.Atoi(m[3])
		problems = append(problems, fmt.Sprintf("%s:%d: sample does not compile: %s", o.s.doc, o.s.line+n-1-o.offset, m[4]))
	}
	if len(problems) == 0 && ignored == 0 {
		t.Fatalf("go build failed:\n%s", out)
	}
	return problems
}

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}

// funcBodies returns the bodies of every function declaration and
// function literal in f.
func funcBodies(f *ast.File) []*ast.BlockStmt {
	var out []*ast.BlockStmt
	ast.Inspect(f, func(n ast.Node) bool {
		switch fn := n.(type) {
		case *ast.FuncDecl:
			if fn.Body != nil {
				out = append(out, fn.Body)
			}
		case *ast.FuncLit:
			out = append(out, fn.Body)
		}
		return true
	})
	return out
}

// isPkgCall reports whether n is a call of pkg.name, or of any function in
// pkg when name is "".
func isPkgCall(n ast.Node, pkg, name string) bool {
	call, ok := n.(*ast.CallExpr)
	if !ok {
		return false
	}
	sel, ok := call.Fun.(*ast.SelectorExpr)
	if !ok {
		return false
	}
	id, ok := sel.X.(*ast.Ident)
	return ok && id.Name == pkg && (name == "" || sel.Sel.Name == name)
}

func contains(n ast.Node, match func(ast.Node) bool) bool {
	found := false
	ast.Inspect(n, func(n ast.Node) bool {
		if found || n == nil {
			return false
		}
		found = match(n)
		return !found
	})
	return found
}

// TestSnippetsNoErrorBodiesForHtmx checks that no handler that talks htmx
// answers an error with http.Error. htmx 4 swaps every response except
// 204 and 304, so an error body replaces the chart with its text, and on
// an hx-sse:connect request the stream is then never retried. Samples
// must answer 204 (or render a deliberate error fragment) instead.
func TestSnippetsNoErrorBodiesForHtmx(t *testing.T) {
	for _, s := range loadSnippets(t) {
		f, err := toFile(s, "p")
		if err != nil {
			continue
		}
		for _, body := range funcBodies(f.ast) {
			usesHx := contains(body, func(n ast.Node) bool { return isPkgCall(n, "hx", "") })
			if !usesHx {
				continue
			}
			if contains(body, func(n ast.Node) bool { return isPkgCall(n, "http", "Error") }) {
				t.Errorf("%s: an htmx handler calls http.Error; htmx 4 would swap the error text over the chart. Answer 204 No Content instead", s)
			}
		}
	}
}

// TestSnippetsSizeKeepsServerHeight checks that samples discard the height
// from hx.Size. The client reports its autosize wrapper's clientHeight,
// and a wrapper without a CSS height takes that height from the chart
// inside it (plus the inline baseline gap), so feeding it back grows the
// chart on every poll. Pages that give the wrapper an explicit height can
// use it; the samples don't.
func TestSnippetsSizeKeepsServerHeight(t *testing.T) {
	for _, s := range loadSnippets(t) {
		f, err := toFile(s, "p")
		if err != nil {
			continue
		}
		ok := map[ast.Node]bool{}
		ast.Inspect(f.ast, func(n ast.Node) bool {
			as, isAssign := n.(*ast.AssignStmt)
			if isAssign && len(as.Lhs) == 2 && len(as.Rhs) == 1 && isPkgCall(as.Rhs[0], "hx", "Size") {
				if id, isIdent := as.Lhs[1].(*ast.Ident); isIdent && id.Name == "_" {
					ok[as.Rhs[0]] = true
				}
			}
			return true
		})
		ast.Inspect(f.ast, func(n ast.Node) bool {
			if isPkgCall(n, "hx", "Size") && !ok[n] {
				t.Errorf("%s: sample uses the height from hx.Size; with a content-sized wrapper the chart grows on every poll. Use `width, _ := hx.Size(r, 640, 0)` and a fixed Height", s)
			}
			return true
		})
	}
}

// TestSnippetsStreamsSurviveRenderErrors checks that SSE handlers skip a
// frame whose Render fails instead of returning. Returning ends the
// response, and hx-sse 4.0.2 reconnects about 500 ms later; it resets its
// backoff on every successful reconnect, so an error that recurs on every
// tick (ErrTooManySeries, say) turns each open tab into a new request every
// second and a half, forever. The guide's advice is to log and continue.
func TestSnippetsStreamsSurviveRenderErrors(t *testing.T) {
	for _, s := range loadSnippets(t) {
		f, err := toFile(s, "p")
		if err != nil {
			continue
		}
		if streamReturnsOnRenderError(f.ast) {
			t.Errorf("%s: an SSE handler returns when Render fails; the client then reconnects in a loop. Log the error and `continue` to skip the frame", s)
		}
	}
}

// streamReturnsOnRenderError reports whether f opens an hx.Stream and
// returns from an if statement that tests the error of the Render call
// just before it.
func streamReturnsOnRenderError(f *ast.File) bool {
	if !contains(f, func(n ast.Node) bool { return isPkgCall(n, "hx", "NewStream") }) {
		return false
	}
	bad := false
	ast.Inspect(f, func(n ast.Node) bool {
		list := stmtList(n)
		for i := 0; i+1 < len(list) && !bad; i++ {
			errName := renderErrName(list[i])
			if errName == "" {
				continue
			}
			ifs, ok := list[i+1].(*ast.IfStmt)
			bad = ok && mentions(ifs.Cond, errName) &&
				contains(ifs.Body, func(n ast.Node) bool { _, ok := n.(*ast.ReturnStmt); return ok })
		}
		return !bad
	})
	return bad
}

func TestStreamReturnsOnRenderError(t *testing.T) {
	const open = "stream, _ := hx.NewStream(w, r)\n"
	tests := []struct {
		name, code string
		want       bool
	}{
		{"return on render error", open + "for range tick.C {\n\tsvg, err := chart.Render()\n\tif err != nil {\n\t\treturn\n\t}\n\tstream.Send(svg)\n}\n", true},
		{"combined condition", open + "for {\n\tselect {\n\tcase <-tick.C:\n\t\tsvg, err := chart.Render()\n\t\tif err != nil || stream.Send(svg) != nil {\n\t\t\treturn\n\t\t}\n\t}\n}\n", true},
		{"continue on render error", open + "for range tick.C {\n\tsvg, err := chart.Render()\n\tif err != nil {\n\t\tcontinue\n\t}\n\tif err := stream.Send(svg); err != nil {\n\t\treturn\n\t}\n}\n", false},
		{"not a stream handler", "svg, err := chart.Render()\nif err != nil {\n\treturn\n}\n_ = svg\n", false},
	}
	for _, tt := range tests {
		f, err := toFile(snippet{doc: tt.name, line: 1, code: tt.code}, "p")
		if err != nil {
			t.Fatalf("%s: %v", tt.name, err)
		}
		if got := streamReturnsOnRenderError(f.ast); got != tt.want {
			t.Errorf("%s: streamReturnsOnRenderError = %v, want %v", tt.name, got, tt.want)
		}
	}
}

// stmtList returns the statements that n holds directly, if any.
func stmtList(n ast.Node) []ast.Stmt {
	switch n := n.(type) {
	case *ast.BlockStmt:
		return n.List
	case *ast.CaseClause:
		return n.Body
	case *ast.CommClause:
		return n.Body
	}
	return nil
}

// renderErrName returns the name of the error variable that stmt assigns
// from a call of a method named Render, or "".
func renderErrName(stmt ast.Stmt) string {
	as, ok := stmt.(*ast.AssignStmt)
	if !ok || len(as.Lhs) != 2 || len(as.Rhs) != 1 {
		return ""
	}
	call, ok := as.Rhs[0].(*ast.CallExpr)
	if !ok {
		return ""
	}
	if sel, ok := call.Fun.(*ast.SelectorExpr); !ok || sel.Sel.Name != "Render" {
		return ""
	}
	if id, ok := as.Lhs[1].(*ast.Ident); ok && id.Name != "_" {
		return id.Name
	}
	return ""
}

// mentions reports whether the identifier name occurs in n.
func mentions(n ast.Node, name string) bool {
	return contains(n, func(n ast.Node) bool {
		id, ok := n.(*ast.Ident)
		return ok && id.Name == name
	})
}

// TestSnippetsPartialPagesVary checks that a sample which answers one URL
// with either a full page or a fragment, depending on hx.IsPartial, sends
// a Vary header. Without it, a shared or browser cache can store the bare
// fragment and serve it for the page URL (to everyone who opens a link
// made with hx-push-url, or on back and forward navigation).
func TestSnippetsPartialPagesVary(t *testing.T) {
	for _, s := range loadSnippets(t) {
		f, err := toFile(s, "p")
		if err != nil {
			continue
		}
		if !contains(f.ast, func(n ast.Node) bool { return isPkgCall(n, "hx", "IsPartial") }) {
			continue
		}
		setsVary := contains(f.ast, func(n ast.Node) bool {
			call, ok := n.(*ast.CallExpr)
			if !ok || len(call.Args) != 2 {
				return false
			}
			sel, ok := call.Fun.(*ast.SelectorExpr)
			if !ok || sel.Sel.Name != "Add" && sel.Sel.Name != "Set" {
				return false
			}
			lit, ok := call.Args[0].(*ast.BasicLit)
			return ok && lit.Value == `"Vary"` &&
				mentions(call.Args[1], "HeaderRequest") && mentions(call.Args[1], "HeaderRequestType")
		})
		if !setsVary {
			t.Errorf("%s: sample serves a page or a fragment by hx.IsPartial without `Vary: HX-Request, HX-Request-Type`; a cache could serve the fragment for the page URL", s)
		}
	}
}
