package docs

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestQuickStartMatchesExample checks that examples/basic is the README's
// quick start: the complete program in README.md (its only Go block that
// starts with a package clause) must equal examples/basic/main.go after
// the example's package comment. The README says the two are the same
// program, and the example is what CI builds, vets and lints.
func TestQuickStartMatchesExample(t *testing.T) {
	var programs []snippet
	for _, s := range markdownSnippets(t, "README.md") {
		if strings.HasPrefix(s.code, "package ") {
			programs = append(programs, s)
		}
	}
	if len(programs) != 1 {
		t.Fatalf("README.md has %d complete programs (Go blocks starting with a package clause), want 1: the quick start", len(programs))
	}
	readme := programs[0]

	src, err := os.ReadFile(filepath.Join(root, "examples", "basic", "main.go"))
	if err != nil {
		t.Fatal(err)
	}
	example := string(src)
	i := strings.Index(example, "\npackage ")
	if !strings.HasPrefix(example, "package ") {
		if i < 0 {
			t.Fatal("examples/basic/main.go: no package clause")
		}
		example = example[i+1:] // drop the package comment
	}

	if readme.code == example {
		return
	}
	got, want := strings.Split(example, "\n"), strings.Split(readme.code, "\n")
	for n := 0; n < len(got) || n < len(want); n++ {
		var g, w string
		if n < len(got) {
			g = got[n]
		}
		if n < len(want) {
			w = want[n]
		}
		if g != w {
			t.Fatalf("examples/basic/main.go differs from the quick start at %s (line %d of the program):\nexample: %q\nREADME:  %q\nKeep the two identical; only the example's package comment may differ.", readme, n+1, g, w)
		}
	}
	t.Fatal("examples/basic/main.go differs from the README quick start")
}
