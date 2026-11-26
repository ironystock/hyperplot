package docs

import (
	"os/exec"
	"strings"
	"testing"
)

// TestGitignoreCoversExampleOutput checks that what the example READMEs tell
// readers to run leaves nothing for `git add -A` to pick up: the Python
// tests write __pycache__/ next to the modules, and the fixture renderers
// and the gallery's -out export write to out/ at the repository root.
func TestGitignoreCoversExampleOutput(t *testing.T) {
	git, err := exec.LookPath("git")
	if err != nil {
		t.Skip("git not installed")
	}
	run := func(args ...string) *exec.Cmd {
		return exec.CommandContext(t.Context(), git, append([]string{"-C", root}, args...)...)
	}
	if err := run("rev-parse", "--git-dir").Run(); err != nil {
		t.Skip("not a git checkout")
	}
	ignored := []string{
		"examples/python/__pycache__/hp_wire.cpython-313.pyc",
		"tools/hpcheck/__pycache__/hpcheck.cpython-310.pyc",
		"out/python/line-time/chart.svg",
		"out/node/bars-band/table.html",
		"out/gallery/index.html",
		"out/gallery/static/hx-plot.js",
	}
	for _, p := range ignored {
		// --no-index: answer from the ignore rules, even for tracked paths.
		out, err := run("check-ignore", "--no-index", "-v", p).CombinedOutput()
		if err != nil {
			t.Errorf("%s is not ignored by .gitignore (git check-ignore: %v %s)", p, err, strings.TrimSpace(string(out)))
		}
	}
	// The rules must not swallow source: /out/ is anchored to the root.
	for _, p := range []string{"examples/python/render.py", "examples/recipes/standalone-svg/out/x.go"} {
		if run("check-ignore", "--no-index", "-q", p).Run() == nil {
			t.Errorf("%s is ignored, but it is source", p)
		}
	}
}
