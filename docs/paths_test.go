package docs

import (
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// repoPath matches a mention of a path under examples/ or tools/ in prose,
// a code span, a code block or a source comment: "go run
// ./examples/dashboard", "python spec/hpcheck/hpcheck.py", "see
// examples/recipes/polling-ptag". Placeholders such as
// examples/recipes/<name>/ stop at the first character a path cannot
// hold, so they match only the part that names a real directory. A path
// must start the word or follow ./ or ../, so the tail of an import path
// (honnef.co/go/tools/cmd/staticcheck) is not a mention.
var repoPath = regexp.MustCompile(`(?:^|[\s` + "`" + `'"(\[]|\./)((?:examples|tools)/[A-Za-z0-9_./-]*)`)

// mentionExts are the files TestRepoPathMentions reads: documentation and
// the source of the examples, whose comments tell readers what to run.
var mentionExts = map[string]bool{
	".md": true, ".go": true, ".py": true, ".mjs": true, ".js": true,
	".html": true, ".json": true, ".yml": true, ".yaml": true,
}

// TestRepoPathMentions checks that every examples/... or tools/... path the
// repository mentions exists, not only the ones in Markdown links
// (TestMarkdownLinks). Run lines, code spans and comments point readers at
// commands and directories too, and a README that says "python
// spec/hpcheck/hpcheck.py" or "see examples/recipes/polling-ptag" before
// those exist sends them to "No such file".
func TestRepoPathMentions(t *testing.T) {
	var files []string
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		name := d.Name()
		if d.IsDir() {
			if path != root && (strings.HasPrefix(name, ".") && name != ".github" || name == "node_modules" || name == "testdata") {
				return filepath.SkipDir
			}
			return nil
		}
		if filepath.Base(filepath.Dir(path)) == "docs" && strings.HasSuffix(name, "_test.go") {
			return nil // these tests name missing paths on purpose
		}
		if mentionExts[filepath.Ext(name)] || name == "Makefile" {
			files = append(files, path)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	checked := 0
	for _, file := range files {
		src, err := os.ReadFile(file)
		if err != nil {
			t.Fatal(err)
		}
		rel, err := filepath.Rel(root, file)
		if err != nil {
			t.Fatal(err)
		}
		for i, line := range strings.Split(string(src), "\n") {
			seen := map[string]bool{}
			for _, m := range repoPath.FindAllStringSubmatch(line, -1) {
				p := strings.TrimRight(m[1], ".-")
				if strings.Contains(p, "//") || seen[p] {
					continue // part of a URL, or already reported for this line
				}
				seen[p] = true
				checked++
				if _, err := os.Stat(filepath.Join(root, filepath.FromSlash(p))); err != nil {
					t.Errorf("%s:%d: mentions %s, which does not exist", filepath.ToSlash(rel), i+1, p)
				}
			}
		}
	}
	if checked == 0 {
		t.Fatal("no examples/ or tools/ paths found; is the pattern out of date?")
	}
}
