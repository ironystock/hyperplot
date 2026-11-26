package docs

import (
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"
)

// htmxTag matches a <script> tag that loads a file of the htmx.org package
// from jsDelivr, wherever it appears: an HTML template, a Python string, a
// JavaScript template literal or a Go string.
var htmxTag = regexp.MustCompile(`<script\b[^>]*\bsrc="https://cdn\.jsdelivr\.net/npm/htmx\.org@[^"]*"[^>]*>`)

// dashboardPage is the page whose htmx tags every example copies: the
// same src, integrity and crossorigin, never deferred (examples/README.md,
// "What every example has in common").
const dashboardPage = "examples/dashboard/templates/page.html"

// TestExamplesPinHtmx checks that every example loads htmx and its
// extensions with exactly the dashboard's tags: the same pinned version,
// the same Subresource Integrity hash and crossorigin, and no defer or
// async. A deferred htmx misses the page's own extensions (DESIGN.md, "htmx
// 4 integration"); a different version or a missing hash is a page
// nobody tested; and the browser tests answer only those exact URLs from
// node_modules, so a page with another one fails there with no network.
func TestExamplesPinHtmx(t *testing.T) {
	ref := htmxTag.FindAllString(readRepoFile(t, dashboardPage), -1)
	if len(ref) < 3 {
		t.Fatalf("%s: found %d htmx tags, expected htmx, hx-sse and hx-ptag", dashboardPage, len(ref))
	}
	for _, tag := range ref {
		if !strings.Contains(tag, ` integrity="sha384-`) || !strings.Contains(tag, ` crossorigin="anonymous"`) {
			t.Errorf("%s: %s has no integrity hash or crossorigin", dashboardPage, tag)
		}
		if regexp.MustCompile(`\s(defer|async)\b`).MatchString(tag) {
			t.Errorf("%s: %s must not be deferred", dashboardPage, tag)
		}
	}

	pages := 0
	walkExamples(t, func(rel string, src []byte) {
		if strings.HasSuffix(rel, "_test.go") || strings.HasPrefix(filepath.Base(rel), "test_") || strings.HasSuffix(rel, ".test.mjs") {
			return // tests may hold broken tags on purpose
		}
		tags := htmxTag.FindAllString(string(src), -1)
		if len(tags) > 0 {
			pages++
		}
		for _, tag := range tags {
			if !slices.Contains(ref, tag) {
				t.Errorf("%s: %s\n\tis not one of the dashboard's tags (%s); copy the tag exactly", rel, tag, dashboardPage)
			}
		}
	})
	if pages < 8 {
		t.Fatalf("found htmx tags in %d example files; is the pattern out of date?", pages)
	}
}

// walkExamples calls fn with every source file under examples/.
func walkExamples(t *testing.T, fn func(rel string, src []byte)) {
	t.Helper()
	err := filepath.WalkDir(filepath.Join(root, "examples"), func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if name := d.Name(); name == "node_modules" || name == "__pycache__" || name == "preview" || strings.HasPrefix(name, ".") {
				return filepath.SkipDir
			}
			return nil
		}
		switch filepath.Ext(path) {
		case ".html", ".go", ".py", ".mjs", ".js":
		default:
			return nil
		}
		src, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		fn(filepath.ToSlash(rel), src)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

// exampleDirs returns the examples: each directory under examples/ with a
// README.md or a main.go (examples/basic has no README), as a slash path
// relative to examples/ ("dashboard", "recipes/sizing").
// examples/recipes itself is an index, not an example.
func exampleDirs(t *testing.T) []string {
	t.Helper()
	var dirs []string
	for _, pattern := range []string{"examples/*/README.md", "examples/*/*/README.md", "examples/*/main.go", "examples/*/*/main.go"} {
		matches, err := filepath.Glob(filepath.Join(root, filepath.FromSlash(pattern)))
		if err != nil {
			t.Fatal(err)
		}
		for _, m := range matches {
			rel, err := filepath.Rel(filepath.Join(root, "examples"), filepath.Dir(m))
			if err != nil {
				t.Fatal(err)
			}
			if rel = filepath.ToSlash(rel); rel != "recipes" && !slices.Contains(dirs, rel) {
				dirs = append(dirs, rel)
			}
		}
	}
	slices.Sort(dirs)
	if len(dirs) < 17 {
		t.Fatalf("found %d examples (%v); is the pattern out of date?", len(dirs), dirs)
	}
	return dirs
}

// TestExampleIndexes checks that examples/README.md links every example
// and examples/recipes/README.md every recipe, so a new example cannot
// be missing from the index, and that neither promises examples that do
// not exist yet.
func TestExampleIndexes(t *testing.T) {
	index := readRepoFile(t, "examples/README.md")
	recipes := readRepoFile(t, "examples/recipes/README.md")
	for _, dir := range exampleDirs(t) {
		if !strings.Contains(index, "]("+dir+"/)") {
			t.Errorf("examples/README.md does not link %s/", dir)
		}
		if name, ok := strings.CutPrefix(dir, "recipes/"); ok && !strings.Contains(recipes, "]("+name+"/)") {
			t.Errorf("examples/recipes/README.md does not link %s/", name)
		}
	}
	stale := regexp.MustCompile(`(?i)\b(planned|coming soon|to come|not yet written|TODO)\b`)
	for path, src := range map[string]string{"examples/README.md": index, "examples/recipes/README.md": recipes} {
		if m := stale.FindString(src); m != "" {
			t.Errorf("%s says %q; every example it lists exists now", path, m)
		}
	}
}

// browserTest is the real-browser test that opens every example.
const browserTest = "client/browser/examples.browser.mjs"

var (
	// jsComment matches a JavaScript block comment, or a line comment that
	// starts a line or follows a space (so the // of 'http://' stays).
	jsComment = regexp.MustCompile(`(?ms)/\*.*?\*/|(?:^|[ \t])//[^\n]*`)
	// examplesArray is the browser test's list of examples.
	examplesArray = regexp.MustCompile(`(?s)\nconst examples = \[\n(.*?)\n\]\n`)
	// exampleEntry is one entry of that list: { name: '...', ... }.
	exampleEntry = regexp.MustCompile(`(?m)^\s*\{\s*name: '([^']+)',(.*)\},?\s*$`)
	// entryGo and entryScript are how an entry starts its example: a Go
	// package under examples/, or a script such as examples/python/server.py.
	entryGo     = regexp.MustCompile(`\bgo: '([^']+)'`)
	entryScript = regexp.MustCompile(`'examples/([a-z0-9/-]+)/[A-Za-z0-9_-]+\.(?:py|mjs)'`)
	// examplesLoop is the loop that turns every entry into a test, with no
	// options (an option may skip it).
	examplesLoop = regexp.MustCompile(`\bfor \(const ex of examples\) \{\s*test\(ex\.name, async \(\) => \{`)
	// standaloneTest is standalone-svg's own test, with no options.
	standaloneTest = regexp.MustCompile(`(?m)^test\('standalone-svg', async \(\) => \{`)
	// skipped is a test or entry that is skipped or left to do.
	skipped = regexp.MustCompile(`\.(?:skip|todo)\(|\b(?:skip|todo):`)
)

// browserStarted returns the examples (directories under examples/) that
// the browser test src opens, and what in src stops it from opening any.
// It reads the code only, not comments: an entry that is commented out
// opens nothing. Each entry of the examples list starts a Go package
// (go: '<dir>') or a script ('examples/<dir>/server.py'), and the loop
// over the list must run every entry; standalone-svg, which writes files
// rather than serving, needs a test of its own, since building it (the
// buildGo argument) is not opening it.
func browserStarted(src string) (started map[string]bool, problems []string) {
	code := jsComment.ReplaceAllString(src, "")
	started = map[string]bool{}
	if m := skipped.FindString(code); m != "" {
		problems = append(problems, "skips a test ("+strings.TrimSuffix(m, "(")+"); every example must run")
	}
	list := examplesArray.FindStringSubmatch(code)
	if list == nil {
		return started, append(problems, "has no `const examples = [...]` list")
	}
	if !examplesLoop.MatchString(code) {
		problems = append(problems, "has no `for (const ex of examples) { test(ex.name, ...` loop over the list")
	} else {
		for _, e := range exampleEntry.FindAllStringSubmatch(list[1], -1) {
			if m := entryGo.FindStringSubmatch(e[2]); m != nil {
				started[m[1]] = true
			} else if m := entryScript.FindStringSubmatch(e[2]); m != nil {
				started[m[1]] = true
			}
		}
	}
	if standaloneTest.MatchString(code) {
		started["recipes/standalone-svg"] = true
	}
	return started, problems
}

// TestEveryExampleInBrowser checks that the browser test opens every
// example (browserStarted says how). examples/basic is the README quick
// start and listens on :8080 only, so it is left out; quickstart_test.go
// keeps it equal to the README, whose chart the other examples cover.
func TestEveryExampleInBrowser(t *testing.T) {
	started, problems := browserStarted(readRepoFile(t, browserTest))
	for _, p := range problems {
		t.Errorf("%s %s", browserTest, p)
	}
	for _, dir := range exampleDirs(t) {
		if dir != "basic" && !started[dir] {
			t.Errorf("%s does not open examples/%s; add it to the examples list", browserTest, dir)
		}
	}
}

// TestBrowserStartedMutations checks that browserStarted sees an example
// as not opened when the browser test is changed so that it no longer
// opens it, the ways a reader might not notice in review.
func TestBrowserStartedMutations(t *testing.T) {
	src := readRepoFile(t, browserTest)
	started, problems := browserStarted(src)
	if len(problems) > 0 || !started["recipes/theming"] || !started["recipes/standalone-svg"] || !started["python"] || !started["node"] {
		t.Fatalf("%s as it stands: started %v, problems %v", browserTest, started, problems)
	}
	// The theming entry, the line after it, and the standalone-svg test.
	const (
		theming    = "  { name: 'theming', go: 'recipes/theming', charts: 2, check: darkTheme },\n"
		afterTheme = "  { name: 'sparkline-table',"
		standalone = "test('standalone-svg', async"
		nextFunc   = "// record forgets"
	)
	for _, s := range []string{theming, afterTheme, standalone, nextFunc, "  test(ex.name, async", "  { name: 'python',"} {
		if n := strings.Count(src, s); n != 1 {
			t.Fatalf("%s: %q occurs %d times, want 1; update this test", browserTest, s, n)
		}
	}
	replace := func(from, to string) func(string) string {
		return func(s string) string { return strings.Replace(s, from, to, 1) }
	}
	for _, tc := range []struct {
		name    string
		edit    func(string) string
		missing string // an example no longer opened, or ""
		problem bool   // whether a problem must be reported
	}{
		{"entry line-commented", replace(theming, "  // "+theming[2:]), "recipes/theming", false},
		{"entry block-commented", replace(theming, "  /* "+theming[2:]+"  */\n"), "recipes/theming", false},
		{"script entry commented", replace("  { name: 'python',", "  // { name: 'python',"), "python", false},
		{"entry deleted", replace(theming, ""), "recipes/theming", false},
		{"standalone-svg skipped", replace(standalone, "test.skip('standalone-svg', async"), "recipes/standalone-svg", true},
		{"standalone-svg with skip option", replace(standalone, "test('standalone-svg', { skip: 'flaky' }, async"), "recipes/standalone-svg", true},
		{"standalone-svg deleted", func(s string) string {
			before, _, _ := strings.Cut(s, standalone)
			_, after, _ := strings.Cut(s, nextFunc)
			return before + nextFunc + after
		}, "recipes/standalone-svg", false},
		{"loop skipped", replace("  test(ex.name, async", "  test.skip(ex.name, async"), "recipes/theming", true},
		{"loop with skip option", replace("  test(ex.name, async", "  test(ex.name, { skip: true }, async"), "recipes/theming", true},
		{"entry with todo", replace("check: darkTheme },\n"+afterTheme, "check: darkTheme, todo: true },\n"+afterTheme), "", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			changed := tc.edit(src)
			if changed == src {
				t.Fatal("the edit changed nothing; update this test")
			}
			started, problems := browserStarted(changed)
			if tc.missing != "" && started[tc.missing] {
				t.Errorf("examples/%s still counts as opened", tc.missing)
			}
			if tc.problem && len(problems) == 0 {
				t.Error("no problem reported")
			}
		})
	}
}

// TestPreviewsRegeneratedByMakeGolden checks that `make golden` rewrites
// every example's preview/ SVGs: the Makefile runs `go test ... -run
// Preview -update` on $(wildcard ...) patterns that match each package
// with a preview/ directory, and each of those packages defines the
// -update flag and a TestPreview. go test fails a package that does not
// know -update, so ./examples/... cannot be passed instead.
func TestPreviewsRegeneratedByMakeGolden(t *testing.T) {
	src := readRepoFile(t, makefilePath)
	mk := parseMakefile(src)
	var run string
	for _, c := range mk.recipes["golden"] {
		if strings.Contains(c, "-run Preview -update") {
			run = c
		}
	}
	if run == "" {
		t.Fatalf("%s: the golden target runs no `go test ... -run Preview -update`", makefilePath)
	}
	// The patterns of every $(wildcard ...) in the variables the command uses.
	var patterns []string
	for _, ref := range makeVar.FindAllStringSubmatch(run, -1) {
		for _, w := range regexp.MustCompile(`\$\(wildcard ([^)]*)\)`).FindAllStringSubmatch(mk.vars[ref[1]], -1) {
			patterns = append(patterns, strings.Fields(w[1])...)
		}
	}

	dirs, err := filepath.Glob(filepath.Join(root, "examples", "*", "preview"))
	if err != nil {
		t.Fatal(err)
	}
	deeper, err := filepath.Glob(filepath.Join(root, "examples", "*", "*", "preview"))
	if err != nil {
		t.Fatal(err)
	}
	dirs = append(dirs, deeper...)
	if len(dirs) == 0 {
		t.Fatal("no examples/**/preview directories; is the pattern out of date?")
	}
	for _, dir := range dirs {
		rel, err := filepath.Rel(root, dir)
		if err != nil {
			t.Fatal(err)
		}
		rel = filepath.ToSlash(rel)
		if !slices.ContainsFunc(patterns, func(p string) bool {
			ok, _ := filepath.Match(strings.TrimSuffix(p, "/"), rel)
			return ok
		}) {
			t.Errorf("%s: `make golden` does not rewrite %s (patterns %v)", makefilePath, rel, patterns)
		}
		pkg := filepath.ToSlash(filepath.Dir(rel))
		tests := readRepoFile(t, pkg+"/main_test.go")
		if !strings.Contains(tests, `flag.Bool("update"`) || !strings.Contains(tests, "func TestPreview(") {
			t.Errorf("%s/main_test.go: has preview/ but no -update flag or TestPreview", pkg)
		}
	}
}

// standaloneOut is standalone-svg's -out flag and its default, a fixed
// directory under os.TempDir().
var standaloneOut = regexp.MustCompile(`flag\.String\("out", filepath\.Join\(os\.TempDir\(\), "([^"]+)"\)`)

// TestStandaloneOutDocumented checks that the READMEs that say where
// standalone-svg writes without -out name its default directory, which
// is the same on every run (the files are overwritten), and none of them
// promises a new directory per run.
func TestStandaloneOutDocumented(t *testing.T) {
	m := standaloneOut.FindStringSubmatch(readRepoFile(t, "examples/recipes/standalone-svg/main.go"))
	if m == nil {
		t.Fatal("examples/recipes/standalone-svg/main.go: no -out flag under os.TempDir(); update this test")
	}
	fresh := regexp.MustCompile(`(?i)\bnew\s+(?:temporary|temp)\b|\bfresh\s+(?:temporary\s+)?director`)
	for _, path := range []string{"examples/README.md", "examples/recipes/README.md", "examples/recipes/standalone-svg/README.md"} {
		src := readRepoFile(t, path)
		if !strings.Contains(src, "`"+m[1]+"`") {
			t.Errorf("%s does not name standalone-svg's default directory `%s`", path, m[1])
		}
		if f := fresh.FindString(src); f != "" {
			t.Errorf("%s says %q; standalone-svg writes to the same `%s` on every run", path, f, m[1])
		}
	}
}
