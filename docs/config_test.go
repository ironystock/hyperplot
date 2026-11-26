package docs

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"
)

// htmxConfigPages are the documents and templates whose
// <meta name="htmx-config"> tags are checked, relative to the module root.
var htmxConfigPages = []string{
	"README.md",
	"docs/htmx.md",
	"docs/DESIGN.md",
	"spec/spec.md",
	"docs/implementing-a-server.md",
	"client/README.md",
	"examples/dashboard/templates/page.html",
}

var htmxConfigMeta = regexp.MustCompile(`<meta\s+name="htmx-config"\s+content='([^']*)'`)

// TestHtmxConfigGuardsErrorPages checks every htmx-config meta tag that the
// documentation shows or the example serves. htmx 4 swaps every status that
// config.noSwap doesn't list, so without "4xx" and "5xx" a proxy's 502 or a
// middleware's 401 page replaces the chart, and hx-ptag keeps it there
// after recovery (the error page carries no HX-PTag, so the next poll sends
// the last good tag and gets a 304). Each array in the config replaces
// htmx's default, so the defaults must be listed too.
func TestHtmxConfigGuardsErrorPages(t *testing.T) {
	found := map[string]int{}
	for _, page := range htmxConfigPages {
		src, err := os.ReadFile(filepath.Join(root, page))
		if err != nil {
			t.Fatal(err)
		}
		for _, m := range htmxConfigMeta.FindAllStringSubmatch(string(src), -1) {
			found[page]++
			for _, problem := range checkHtmxConfig(m[1]) {
				t.Errorf("%s: htmx-config %s: %s", page, m[1], problem)
			}
		}
	}
	// The guide's setup and the example must show the guard, so this test
	// can't pass by finding nothing.
	for _, page := range []string{"docs/htmx.md", "examples/dashboard/templates/page.html"} {
		if found[page] == 0 {
			t.Errorf("%s: no <meta name=\"htmx-config\"> found", page)
		}
	}
}

// checkHtmxConfig returns what is wrong with an htmx-config JSON value.
func checkHtmxConfig(content string) []string {
	var cfg struct {
		NoSwap      []any    `json:"noSwap"`
		MorphIgnore []string `json:"morphIgnore"`
	}
	if err := json.Unmarshal([]byte(content), &cfg); err != nil {
		return []string{"not valid JSON: " + err.Error()}
	}
	var problems []string
	noSwap := make([]string, len(cfg.NoSwap))
	for i, v := range cfg.NoSwap {
		noSwap[i] = fmt.Sprint(v)
	}
	for _, want := range []string{"204", "304", "4xx", "5xx"} {
		if !slices.Contains(noSwap, want) {
			problems = append(problems, "noSwap lacks "+want)
		}
	}
	if cfg.MorphIgnore != nil && !slices.Contains(cfg.MorphIgnore, "data-htmx-powered") {
		problems = append(problems, `morphIgnore drops the default "data-htmx-powered"`)
	}
	return problems
}

func TestCheckHtmxConfig(t *testing.T) {
	tests := []struct {
		content string
		want    string // substring of the joined problems; "" for none
	}{
		{`{"noSwap":[204,304,"4xx","5xx"],"morphIgnore":["data-htmx-powered","open"]}`, ""},
		{`{"noSwap":[204,304,"4xx","5xx"]}`, ""},
		{`{"morphIgnore":["data-htmx-powered","open"]}`, "noSwap lacks 4xx"},
		{`{"noSwap":["4xx","5xx"]}`, "noSwap lacks 204"},
		{`{"noSwap":[204,304,"4xx","5xx"],"morphIgnore":["open"]}`, "data-htmx-powered"},
		{`{noSwap:[204]}`, "not valid JSON"},
	}
	for _, tt := range tests {
		got := strings.Join(checkHtmxConfig(tt.content), "; ")
		if tt.want == "" && got != "" || !strings.Contains(got, tt.want) {
			t.Errorf("checkHtmxConfig(%s) = %q, want %q", tt.content, got, tt.want)
		}
	}
}

// TestNoRootClientTestCommand checks that no guide tells people to run
// `node --test client/` from the repository root. That command finds no
// test files there and passes without running the client suite; the
// client tests run with `node --test` inside client/ (make client).
func TestNoRootClientTestCommand(t *testing.T) {
	files := []string{"README.md", ".github/CONTRIBUTING.md", "CLAUDE.md", "client/README.md", "Makefile"}
	docs, err := filepath.Glob(filepath.Join(root, "docs", "*.md"))
	if err != nil {
		t.Fatal(err)
	}
	for _, d := range docs {
		rel, err := filepath.Rel(root, d)
		if err != nil {
			t.Fatal(err)
		}
		files = append(files, filepath.ToSlash(rel))
	}
	for _, file := range files {
		src, err := os.ReadFile(filepath.Join(root, file))
		if err != nil {
			t.Fatal(err)
		}
		for i, line := range strings.Split(string(src), "\n") {
			// DESIGN.md explains why the command is wrong; that line
			// says so.
			if strings.Contains(line, "node --test client") && !strings.Contains(line, "finds no test files") {
				t.Errorf("%s:%d: `node --test client/` runs no client tests from the repository root; use `cd client && node --test` (make client)", file, i+1)
			}
		}
	}
}
