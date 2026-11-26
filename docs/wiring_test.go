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

// The wiring files: the Makefile, whose test and check targets are what
// contributors run, and the CI workflow.
const (
	makefilePath = "Makefile"
	ciPath       = ".github/workflows/ci.yml"
)

// makeEntryPoints are the Makefile targets that must run every suite.
var makeEntryPoints = []string{"test", "check"}

// fixtureRequire is the fixture list every example renderer must pass
// (docs/implementing-a-server.md, tools/hpcheck/README.md).
const fixtureRequire = "line-time,empty,bars-band,escaping,collisions"

// TestNonGoSuitesAreWired checks that `make test`, `make check` and CI run
// everything go test ./... cannot see:
//
//   - every directory under examples/ or tools/ with Python (test_*.py)
//     or Node (*.test.mjs) tests, by `unittest discover <dir>` or by
//     `node --test` inside the directory;
//   - every fixture renderer (render_fixtures.py, render-fixtures.mjs),
//     with `hpcheck.py fixtures --out` on the directory it wrote and
//     --require naming the required fixtures;
//   - every server config of the checker (examples/**/hpcheck.json,
//     tools/hpcheck/targets/*.json), with `hpcheck.py server --config`.
//
// Those suites and checks test against files the Go side owns
// (docs/spec.md Appendix B, testdata/conformance/, client/), so one that
// only runs by hand stops catching breakage without anyone noticing.
//
// It matches commands, not names: comments, help text, step names and
// Makefile targets that test and check never reach do not count. The
// commands are the recipes reachable from each entry point (through
// prerequisites and $(MAKE) calls, with the Makefile's variables
// expanded) and the run: lines of the workflow (a `make <target>` there
// runs that target's commands).
func TestNonGoSuitesAreWired(t *testing.T) {
	mk := parseMakefile(readRepoFile(t, makefilePath))
	ci := ciCommands(readRepoFile(t, ciPath), mk)

	reqs := wiringRequirements(t)
	var dirs []string
	for _, r := range reqs {
		dirs = append(dirs, r.what)
	}
	for _, want := range []string{"examples/python", "examples/node", "tools/hpcheck"} {
		if !slices.Contains(dirs, want) {
			t.Fatalf("found suites %v; expected %s among them", dirs, want)
		}
	}

	for _, r := range reqs {
		for _, target := range makeEntryPoints {
			if !r.met(mk.commands(target)) {
				t.Errorf("%s: %s is not run by `make %s`", makefilePath, r.what, target)
			}
		}
		if !r.met(ci) {
			t.Errorf("%s: %s is not run by any step", ciPath, r.what)
		}
	}
}

// TestWiringMatchesCommandsOnly checks the matching that
// TestNonGoSuitesAreWired relies on, against small Makefiles and
// workflows: a suite counts only when a command reachable from the entry
// point runs it, never because a comment, help text, step name or
// unreachable target names it.
func TestWiringMatchesCommandsOnly(t *testing.T) {
	py := suiteRequirement("examples/python", false)
	node := suiteRequirement("examples/node", true)
	renderer := rendererRequirement("examples/python/render_fixtures.py")
	config := configRequirement("examples/python/hpcheck.json")

	const pyRule = "py: ## Run unittest discover examples/python.\n\t$(PYTHON) -m unittest discover examples/python\n"
	tests := []struct {
		name     string
		makefile string
		ci       string
		req      requirement
		want     bool
	}{
		{
			name:     "make: recipe of the entry point",
			makefile: "PYTHON ?= python3\ntest:\n\t$(PYTHON) -m unittest discover examples/python\n",
			req:      py, want: true,
		},
		{
			name:     "make: through $(MAKE)",
			makefile: "PYTHON ?= python3\ntest:\n\t$(MAKE) --no-print-directory py\n" + pyRule,
			req:      py, want: true,
		},
		{
			name:     "make: through a prerequisite",
			makefile: "PYTHON ?= python3\ntest: lint py ## all\n" + pyRule + "lint:\n\tgo vet ./...\n",
			req:      py, want: true,
		},
		{
			name:     "make: target the entry point never reaches",
			makefile: "PYTHON ?= python3\ntest:\n\tgo test ./...\n" + pyRule,
			req:      py, want: false,
		},
		{
			name:     "make: help text and comments only",
			makefile: "# unittest discover examples/python\ntest: ## unittest discover examples/python\n\t# unittest discover examples/python\n\tgo test ./...\n",
			req:      py, want: false,
		},
		{
			name:     "make: another directory with the same prefix",
			makefile: "test:\n\tpython -m unittest discover examples/python2\n",
			req:      py, want: false,
		},
		{
			name:     "make: node --test inside the directory",
			makefile: "test:\n\tcd examples/node && node --test\n",
			req:      node, want: true,
		},
		{
			name:     "make: node --test elsewhere",
			makefile: "test:\n\tcd client && node --test\n\tls examples/node\n",
			req:      node, want: false,
		},
		{
			name: "make: renderer checked in a continued recipe",
			makefile: "PYTHON ?= python3\nHPCHECK = $(PYTHON) tools/hpcheck/hpcheck.py\nREQ = " + fixtureRequire + "\n" +
				"test:\n\t@set -e; out=\"$$(mktemp -d)\"; \\\n\t$(PYTHON) examples/python/render_fixtures.py \"$$out/py\"; \\\n" +
				"\t$(HPCHECK) fixtures --out \"$$out/py\" --require $(REQ)\n",
			req: renderer, want: true,
		},
		{
			name: "make: renderer run but its output not checked",
			makefile: "test:\n\tpython examples/python/render_fixtures.py out/py\n" +
				"\tpython tools/hpcheck/hpcheck.py fixtures --out out/node --require " + fixtureRequire + "\n",
			req: renderer, want: false,
		},
		{
			name: "make: renderer checked without --require",
			makefile: "test:\n\tpython examples/python/render_fixtures.py out\n" +
				"\tpython tools/hpcheck/hpcheck.py fixtures --out out\n",
			req: renderer, want: false,
		},
		{
			name:     "make: server config",
			makefile: "test:\n\tpython tools/hpcheck/hpcheck.py server http://127.0.0.1:8080 --config examples/python/hpcheck.json --spawn \"x {port}\"\n",
			req:      config, want: true,
		},
		{
			name:     "make: server config named in an echo",
			makefile: "test:\n\techo see examples/python/hpcheck.json\n",
			req:      config, want: false,
		},
		{
			name: "ci: run line",
			ci:   "jobs:\n  python:\n    steps:\n      - name: unittest\n        run: python -m unittest discover examples/python\n",
			req:  py, want: true,
		},
		{
			name: "ci: comments and step names only",
			ci: "jobs:\n  python:\n    # 3.10 is the oldest version examples/python/README.md supports.\n" +
				"    steps:\n      - name: unittest discover examples/python\n        run: echo skipped\n",
			req: py, want: false,
		},
		{
			name: "ci: run block",
			ci: "jobs:\n  python:\n    steps:\n      - name: tests\n        run: |\n" +
				"          # unittest discover examples/node\n          python -m unittest discover examples/python\n",
			req: py, want: true,
		},
		{
			name: "ci: node --test with working-directory",
			ci: "jobs:\n  client:\n    steps:\n      - name: node --test (examples/node)\n" +
				"        working-directory: examples/node\n        run: node --test\n",
			req: node, want: true,
		},
		{
			name: "ci: node --test in another working-directory",
			ci: "jobs:\n  client:\n    steps:\n      # not only changes under examples/node\n      - name: node --test (examples/node)\n" +
				"        working-directory: client\n        run: node --test\n",
			req: node, want: false,
		},
		{
			name:     "ci: make target",
			makefile: "PYTHON ?= python3\nconformance:\n\t$(PYTHON) tools/hpcheck/hpcheck.py server http://x --config examples/python/hpcheck.json\n",
			ci:       "jobs:\n  c:\n    steps:\n      - name: make conformance\n        run: make conformance PYTHON=python\n",
			req:      config, want: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mk := parseMakefile(tt.makefile)
			var cmds []string
			if tt.ci != "" {
				cmds = ciCommands(tt.ci, mk)
			} else {
				cmds = mk.commands("test")
			}
			if got := tt.req.met(cmds); got != tt.want {
				t.Errorf("%s met = %v, want %v; commands %q", tt.req.what, got, tt.want, cmds)
			}
		})
	}
}

// A requirement is something the wiring must run, and how to tell from a
// list of commands that it does.
type requirement struct {
	what string
	met  func(cmds []string) bool
}

// wiringRequirements finds the suites, fixture renderers and server
// configs in the repository.
func wiringRequirements(t *testing.T) []requirement {
	t.Helper()
	var reqs []requirement
	seen := map[string]bool{}
	for _, top := range []string{"examples", "tools"} {
		dir := filepath.Join(root, top)
		if _, err := os.Stat(dir); err != nil {
			continue // tools/ is optional
		}
		err := filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			name := d.Name()
			if d.IsDir() {
				if name == "node_modules" || name == "testdata" || name == "__pycache__" || strings.HasPrefix(name, ".") {
					return filepath.SkipDir
				}
				return nil
			}
			rel, err := filepath.Rel(root, path)
			if err != nil {
				return err
			}
			rel = filepath.ToSlash(rel)
			relDir := filepath.ToSlash(filepath.Dir(rel))
			py := strings.HasPrefix(name, "test_") && strings.HasSuffix(name, ".py")
			switch {
			case py || strings.HasSuffix(name, ".test.mjs"):
				key := relDir + " node"
				if py {
					key = relDir + " python"
				}
				if !seen[key] {
					seen[key] = true
					reqs = append(reqs, suiteRequirement(relDir, !py))
				}
			case name == "render_fixtures.py" || name == "render-fixtures.mjs":
				reqs = append(reqs, rendererRequirement(rel))
			case name == "hpcheck.json" || (relDir == "tools/hpcheck/targets" && strings.HasSuffix(name, ".json")):
				reqs = append(reqs, configRequirement(rel))
			}
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
	}
	return reqs
}

// endOfArg matches the end of a command-line argument.
const endOfArg = `(?:/)?["']?(?:\s|;|&|\||\)|$)`

// suiteRequirement: `unittest discover dir` for Python, `node --test`
// run inside dir for Node.
func suiteRequirement(dir string, node bool) requirement {
	q := regexp.QuoteMeta(dir)
	re := regexp.MustCompile(`unittest\s+discover\s+(?:-s\s+)?["']?(?:\./)?` + q + endOfArg)
	if node {
		re = regexp.MustCompile(`(?:^|[;&|(]\s*)cd\s+["']?(?:\./)?` + q + `/?["']?\s*&&\s*node\s+--test\b`)
	}
	return requirement{what: dir, met: func(cmds []string) bool {
		return slices.ContainsFunc(cmds, re.MatchString)
	}}
}

// rendererRequirement: the renderer runs with an output directory, and
// hpcheck fixtures checks that directory with --require naming at least
// the required fixtures.
func rendererRequirement(path string) requirement {
	run := regexp.MustCompile(`(?:python\S*|node)\s+(?:\./)?` + regexp.QuoteMeta(path) + `\s+("[^"]+"|'[^']+'|[^\s;&|]+)`)
	return requirement{what: path, met: func(cmds []string) bool {
		var outs []string
		for _, c := range cmds {
			for _, m := range run.FindAllStringSubmatch(c, -1) {
				outs = append(outs, m[1])
			}
		}
		for _, out := range outs {
			check := regexp.MustCompile(`hpcheck\.py\s+fixtures\s+--out\s+` + regexp.QuoteMeta(out) +
				`\s+--require\s+["']?([a-z,-]+)`)
			for _, c := range cmds {
				for _, m := range check.FindAllStringSubmatch(c, -1) {
					if requiresAll(m[1]) {
						return true
					}
				}
			}
		}
		return false
	}}
}

// requiresAll reports whether a --require list names every required fixture.
func requiresAll(list string) bool {
	have := strings.Split(list, ",")
	for _, f := range strings.Split(fixtureRequire, ",") {
		if !slices.Contains(have, f) {
			return false
		}
	}
	return true
}

// configRequirement: hpcheck server with --config path.
func configRequirement(path string) requirement {
	re := regexp.MustCompile(`hpcheck\.py\s+server\s.*--config\s+["']?(?:\./)?` + regexp.QuoteMeta(path) + endOfArg)
	return requirement{what: path, met: func(cmds []string) bool {
		return slices.ContainsFunc(cmds, re.MatchString)
	}}
}

// makefile is the little of a Makefile the wiring test needs: variables,
// and each target's prerequisites, sub-make targets and recipe lines.
type makefile struct {
	vars    map[string]string
	deps    map[string][]string
	recipes map[string][]string
}

var (
	makeAssign = regexp.MustCompile(`^([A-Za-z_][A-Za-z0-9_]*)\s*(\?=|::=|:=|\+=|=)\s*(.*)$`)
	makeRule   = regexp.MustCompile(`^([A-Za-z0-9_.%/ -]+?)\s*:([^=].*)?$`)
	makeVar    = regexp.MustCompile(`\$[({]([A-Za-z_][A-Za-z0-9_]*)[)}]`)
	subMake    = regexp.MustCompile(`\$[({]MAKE[)}]([^;&|]*)`)
)

// parseMakefile reads src. Comment lines, the comments after a rule's
// prerequisites (the ## help text) and shell comments in recipes are
// dropped, and backslash-continued lines are joined.
func parseMakefile(src string) *makefile {
	m := &makefile{vars: map[string]string{}, deps: map[string][]string{}, recipes: map[string][]string{}}
	var current []string
	for _, line := range joinContinued(src) {
		if strings.HasPrefix(line, "\t") {
			cmd := strings.TrimLeft(line, "\t @-+")
			if cmd == "" || strings.HasPrefix(cmd, "#") {
				continue
			}
			for _, target := range current {
				m.recipes[target] = append(m.recipes[target], cmd)
			}
			continue
		}
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		if a := makeAssign.FindStringSubmatch(line); a != nil {
			name, op, val := a[1], a[2], a[3]
			switch _, set := m.vars[name]; {
			case op == "?=" && set:
			case op == "+=" && set:
				m.vars[name] += " " + val
			default:
				m.vars[name] = val
			}
			current = nil
			continue
		}
		if r := makeRule.FindStringSubmatch(line); r != nil {
			current = strings.Fields(r[1])
			if slices.Contains(current, ".PHONY") {
				current = nil
				continue
			}
			prereqs, _, _ := strings.Cut(r[2], "#")
			for _, target := range current {
				m.deps[target] = append(m.deps[target], strings.Fields(prereqs)...)
			}
			continue
		}
		current = nil
	}
	// $(MAKE) calls in a recipe are edges like prerequisites.
	for target, cmds := range m.recipes {
		for _, cmd := range cmds {
			for _, call := range subMake.FindAllStringSubmatch(cmd, -1) {
				m.deps[target] = append(m.deps[target], makeTargets(call[1])...)
			}
		}
	}
	return m
}

// joinContinued splits src into lines, joining a line that ends in a
// backslash with the next. A continued recipe line keeps its leading tab.
func joinContinued(src string) []string {
	var out []string
	var b strings.Builder
	for _, line := range strings.Split(src, "\n") {
		if b.Len() > 0 {
			line = strings.TrimLeft(line, "\t ")
		}
		if strings.HasSuffix(line, "\\") {
			b.WriteString(strings.TrimSuffix(line, "\\"))
			b.WriteString(" ")
			continue
		}
		b.WriteString(line)
		out = append(out, b.String())
		b.Reset()
	}
	return out
}

// makeTargets returns the targets of a make command line's arguments:
// the words that are neither options nor variable assignments. -C (run
// another directory's Makefile) is not followed.
func makeTargets(args string) []string {
	var targets []string
	for _, w := range strings.Fields(args) {
		switch {
		case w == "-C" || strings.HasPrefix(w, "--directory"):
			return nil
		case strings.HasPrefix(w, "-") || strings.Contains(w, "="):
		default:
			targets = append(targets, w)
		}
	}
	return targets
}

// expand replaces the Makefile's own variables in s.
func (m *makefile) expand(s string) string {
	for range 10 {
		next := makeVar.ReplaceAllStringFunc(s, func(ref string) string {
			name := makeVar.FindStringSubmatch(ref)[1]
			if v, ok := m.vars[name]; ok {
				return v
			}
			return ref
		})
		if next == s {
			break
		}
		s = next
	}
	return s
}

// commands returns the recipe lines that `make target` runs, with the
// Makefile's variables expanded.
func (m *makefile) commands(target string) []string {
	var cmds []string
	seen := map[string]bool{}
	var visit func(string)
	visit = func(t string) {
		if seen[t] {
			return
		}
		seen[t] = true
		for _, d := range m.deps[t] {
			visit(d)
		}
		for _, c := range m.recipes[t] {
			cmds = append(cmds, m.expand(c))
		}
	}
	visit(target)
	return cmds
}

var (
	ciStep    = regexp.MustCompile(`^(\s*)- (.*)$`)
	ciKey     = regexp.MustCompile(`^(\s*)([A-Za-z_-]+):\s*(.*)$`)
	makeCall  = regexp.MustCompile(`^make(\s.*)?$`)
	yamlBlock = regexp.MustCompile(`^[|>][-+]?$`)
)

// ciCommands returns the shell commands of a workflow's run: steps, one
// per line of a run block, as `cd <dir> && <cmd>` when the step sets a
// working-directory. A `make <target>` step adds that target's commands.
// Comment lines are dropped, so are step names and every other key.
func ciCommands(src string, mk *makefile) []string {
	var lines []string
	for _, l := range strings.Split(src, "\n") {
		if t := strings.TrimSpace(l); t == "" || strings.HasPrefix(t, "#") {
			continue
		}
		lines = append(lines, l)
	}

	var cmds []string
	for i := 0; i < len(lines); i++ {
		s := ciStep.FindStringSubmatch(lines[i])
		if s == nil {
			continue
		}
		// The step's lines, with its first key moved to its own line.
		indent := len(s[1])
		step := []string{strings.Repeat(" ", indent+2) + s[2]}
		j := i + 1
		for ; j < len(lines) && len(lines[j])-len(strings.TrimLeft(lines[j], " ")) > indent; j++ {
			step = append(step, lines[j])
		}
		i = j - 1

		var workdir string
		var run []string
		for k := 0; k < len(step); k++ {
			kv := ciKey.FindStringSubmatch(step[k])
			if kv == nil {
				continue
			}
			switch kv[2] {
			case "working-directory":
				workdir = strings.Trim(kv[3], `"'`)
			case "run":
				if !yamlBlock.MatchString(kv[3]) {
					run = append(run, kv[3])
					continue
				}
				keyIndent := len(kv[1])
				for k+1 < len(step) && len(step[k+1])-len(strings.TrimLeft(step[k+1], " ")) > keyIndent {
					k++
					run = append(run, strings.TrimSpace(step[k]))
				}
			}
		}
		for _, c := range joinContinued(strings.Join(run, "\n")) {
			c = strings.TrimSpace(c)
			if c == "" {
				continue
			}
			if m := makeCall.FindStringSubmatch(c); m != nil && workdir == "" {
				for _, target := range makeTargets(m[1]) {
					cmds = append(cmds, mk.commands(target)...)
				}
			}
			if workdir != "" {
				c = "cd " + workdir + " && " + c
			}
			cmds = append(cmds, c)
		}
	}
	return cmds
}

// readRepoFile reads a file of the repository by its slash path.
func readRepoFile(t *testing.T, path string) string {
	t.Helper()
	src, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(path)))
	if err != nil {
		t.Fatal(err)
	}
	return string(src)
}
