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

// barePython matches a command line that runs a bare `python`: at the
// start of a line (a shell block, after an optional "$ " prompt), or
// opening a code span or a quoted string (a table's run cell, hpcheck's
// --spawn "..."), followed by a flag or a script. Prose that names the
// Windows interpreter (`python` or `py`, PYTHON=python) does not match.
var barePython = regexp.MustCompile("(?m)(?:^[ \t]*(?:\\$ )?|[`\"'(])python +(?:-[mcIu]\\b|[A-Za-z0-9_./{}$-]*\\.py\\b)")

func TestBarePythonPattern(t *testing.T) {
	for _, tc := range []struct {
		line string
		want bool
	}{
		{"python examples/python/server.py", true},
		{"$ python -m unittest discover examples/python", true},
		{"  python tools/hpcheck/hpcheck.py fixtures", true},
		{"| `python examples/python/server.py` |", true},
		{`    --spawn "python examples/python/server.py --addr 127.0.0.1:{port}"`, true},
		{"run (`python -m unittest discover tools/hpcheck`)", true},
		{"python server.py 8000", true},
		{"    python -m unittest discover examples/python     # from the repository root", true},
		{"    python examples/python/server.py --speed 5             # time runs 5x faster", true},
		{"    python3 tools/hpcheck/hpcheck.py file PATH... [--table PATH] [--strict]", false},
		{`subprocess.run(['python', 'server.py'])`, false},
		{`exec.Command("python", "server.py")`, false},
		{"python3 examples/python/server.py", false},
		{"`python3 -m unittest discover examples/python`", false},
		{"On Windows the interpreter is usually `python` (or `py`).", false},
		{"pass `PYTHON=python` to make", false},
		{"```python", false},
		{"Python 3.10+, standard library", false},
	} {
		if got := barePython.MatchString(tc.line); got != tc.want {
			t.Errorf("barePython.MatchString(%q) = %v, want %v", tc.line, got, tc.want)
		}
	}
}

// sourceExts are the source files under examples/ and tools/ whose
// comments and docstrings tell a reader how to run them (the run lines at
// the top of server.py or hpcheck.py), so they are read too. Code there
// that starts Python passes "python" as its own argument, which
// barePython does not match.
var sourceExts = []string{".py", ".mjs", ".js", ".go", ".html", ".sh"}

// TestPythonCommandsUsePython3 checks that the commands the docs give
// readers run `python3`, the name the Makefile (PYTHON ?= python3) uses:
// every Markdown file, and the comments and docstrings of the source
// files under examples/ and tools/. macOS since 12.3 and Debian or Ubuntu
// without python-is-python3 have no `python` at all, so a bare `python`
// fails on the first command a non-Go reader pastes. Windows, where the
// interpreter is usually `python` or `py`, gets a note in prose instead
// (examples/README.md, "Tests"). CI's workflow is not read: setup-python
// provides `python` on every runner.
func TestPythonCommandsUsePython3(t *testing.T) {
	checked, sources := 0, 0
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		name := d.Name()
		if d.IsDir() {
			if path != root && (strings.HasPrefix(name, ".") || name == "node_modules") {
				return filepath.SkipDir
			}
			return nil
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		rel = filepath.ToSlash(rel)
		switch ext := filepath.Ext(name); {
		case ext == ".md":
			checked++
		case slices.Contains(sourceExts, ext) && (strings.HasPrefix(rel, "examples/") || strings.HasPrefix(rel, "tools/")):
			sources++
		default:
			return nil
		}
		src, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		for i, line := range strings.Split(string(src), "\n") {
			if m := barePython.FindString(line); m != "" {
				t.Errorf("%s:%d: %q runs `python`; write `python3` (Windows readers get a note)", rel, i+1, strings.TrimSpace(m))
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if checked < 10 || sources < 20 {
		t.Fatalf("read %d Markdown files and %d source files; is the walk out of date?", checked, sources)
	}
}
