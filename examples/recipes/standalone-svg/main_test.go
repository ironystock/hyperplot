package main

import (
	"bytes"
	"encoding/xml"
	"flag"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"
)

var update = flag.Bool("update", false, "rewrite the files in preview/")

// forbidden matches a style attribute or an inline event handler in a tag.
// (A <style> element is expected: that is what makes the file standalone.)
var forbidden = regexp.MustCompile(`(?i)<[^>]*\s(style|on[a-z]+)\s*=`)

// root is the part of the root element that the tests check.
type root struct {
	XMLName xml.Name
	ID      string `xml:"id,attr"`
	Class   string `xml:"class,attr"`
	Width   string `xml:"width,attr"`
	Height  string `xml:"height,attr"`
	DataHP  string `xml:"data-hp,attr"`
	Title   string `xml:"title"`
	Style   string `xml:"style"`
}

func TestWrite(t *testing.T) {
	dir := t.TempDir()
	files, err := write(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(files) != 3 {
		t.Fatalf("%d files, want 3", len(files))
	}
	page, err := os.ReadFile(filepath.Join(dir, "index.html"))
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range files {
		b, err := os.ReadFile(filepath.Join(dir, f.File))
		if err != nil {
			t.Fatal(err)
		}
		// The whole file must parse as XML: viewers, <img> and most
		// tools that open .svg files are XML parsers.
		var r root
		if err := xml.Unmarshal(b, &r); err != nil {
			t.Fatalf("%s: %v", f.File, err)
		}
		if r.XMLName.Space != "http://www.w3.org/2000/svg" || r.XMLName.Local != "svg" {
			t.Errorf("%s: root %v", f.File, r.XMLName)
		}
		if !strings.Contains(" "+r.Class+" ", " hp ") || r.DataHP != "1" || r.ID == "" {
			t.Errorf("%s: class %q, data-hp %q, id %q", f.File, r.Class, r.DataHP, r.ID)
		}
		if r.Title != f.Alt {
			t.Errorf("%s: <title> %q, alt %q", f.File, r.Title, f.Alt)
		}
		if !strings.Contains(r.Style, "--hp-series-1:") {
			t.Errorf("%s: no embedded stylesheet", f.File)
		}
		if r.Width != strconv.FormatFloat(f.Width, 'f', -1, 64) || r.Height != strconv.FormatFloat(f.Height, 'f', -1, 64) {
			t.Errorf("%s: %s × %s, index says %v × %v", f.File, r.Width, r.Height, f.Width, f.Height)
		}
		if m := forbidden.Find(b); m != nil {
			t.Errorf("%s: forbidden attribute in %q", f.File, m)
		}
		if bytes.ContainsRune(b, '\r') {
			t.Errorf("%s: raw CR", f.File)
		}
		if !bytes.Contains(page, []byte(`<img src="`+f.File+`"`)) {
			t.Errorf("index.html does not show %s", f.File)
		}
	}
}

// TestIndexNarrow checks that index.html lets its images shrink on a narrow
// window: it links index.css, which caps their width, and its CSP allows
// that stylesheet and nothing inline.
func TestIndexNarrow(t *testing.T) {
	dir := t.TempDir()
	if _, err := write(dir); err != nil {
		t.Fatal(err)
	}
	page, _ := os.ReadFile(filepath.Join(dir, "index.html"))
	css, _ := os.ReadFile(filepath.Join(dir, "index.css"))
	for _, want := range []string{`<link rel="stylesheet" href="index.css">`, `style-src 'self'"`} {
		if !bytes.Contains(page, []byte(want)) {
			t.Errorf("index.html lacks %s", want)
		}
	}
	if bytes.Contains(page, []byte("<style")) || bytes.Contains(page, []byte("unsafe-inline")) {
		t.Error("index.html has inline styles")
	}
	if !bytes.Contains(css, []byte("max-width: 100%")) {
		t.Errorf("index.css = %q", css)
	}
}

// TestDarkTheme checks that Chart.Theme reaches the file: the dark file's
// light palette (the one used without a dark preference) is the dark one.
func TestDarkTheme(t *testing.T) {
	dir := t.TempDir()
	if _, err := write(dir); err != nil {
		t.Fatal(err)
	}
	light, _ := os.ReadFile(filepath.Join(dir, "latency.svg"))
	dark, _ := os.ReadFile(filepath.Join(dir, "latency-dark.svg"))
	first := func(b []byte) string { // the first, unconditional token block
		s := string(b)
		i := strings.Index(s, "--hp-surface:")
		return s[i : i+len("--hp-surface:#000000")]
	}
	if got := first(light); got != "--hp-surface:#ffffff" {
		t.Errorf("latency.svg: %s", got)
	}
	if got := first(dark); got != "--hp-surface:#191919" {
		t.Errorf("latency-dark.svg: %s", got)
	}
}

// TestDeterministic checks that writing twice gives the same bytes.
func TestDeterministic(t *testing.T) {
	a, b := t.TempDir(), t.TempDir()
	if _, err := write(a); err != nil {
		t.Fatal(err)
	}
	if _, err := write(b); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"latency.svg", "latency-dark.svg", "deploys.svg", "index.html", "index.css"} {
		x, _ := os.ReadFile(filepath.Join(a, name))
		y, _ := os.ReadFile(filepath.Join(b, name))
		if len(x) == 0 || !bytes.Equal(x, y) {
			t.Errorf("%s differs between runs", name)
		}
	}
}

// TestPreview keeps preview/, the pictures in the README, identical to
// what the command writes. Regenerate it with:
//
//	go test ./examples/recipes/standalone-svg -update
func TestPreview(t *testing.T) {
	dir := t.TempDir()
	if _, err := write(dir); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"latency.svg", "latency-dark.svg"} {
		got, err := os.ReadFile(filepath.Join(dir, name))
		if err != nil {
			t.Fatal(err)
		}
		path := filepath.Join("preview", name)
		if *update {
			if err := os.MkdirAll("preview", 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(path, got, 0o644); err != nil {
				t.Fatal(err)
			}
			continue
		}
		want, err := os.ReadFile(path)
		if err != nil || !bytes.Equal(got, want) {
			t.Errorf("%s is out of date; run go test ./examples/recipes/standalone-svg -update "+
				"(or, after a renderer change, make golden) and review it", path)
		}
	}
}

// outArg matches the -out flag in a documented run line.
var outArg = regexp.MustCompile(`-out\s+(\S+)`)

// TestRunLinesWriteOutsideRepo keeps the documented -out examples out of
// the repository in every shell. They are run from the repository root,
// so a relative directory such as "charts" would leave untracked generated
// files there, and so would "~/charts" in Windows PowerShell or cmd.exe,
// which pass the ~ through for Go to create as a directory named "~"
// (hidden from git status by .gitignore's *~). "../charts" is outside the
// repository whatever the shell.
func TestRunLinesWriteOutsideRepo(t *testing.T) {
	for _, name := range []string{"README.md", "main.go"} {
		b, err := os.ReadFile(name)
		if err != nil {
			t.Fatal(err)
		}
		found := 0
		for _, m := range outArg.FindAllStringSubmatch(string(b), -1) {
			found++
			dir := strings.Trim(m[1], `"'`)
			if !strings.HasPrefix(dir, "../") && !filepath.IsAbs(dir) && !strings.HasPrefix(dir, "/") {
				t.Errorf("%s: -out %s may write inside the repository; use ../DIR", name, m[1])
			}
		}
		if found == 0 {
			t.Errorf("%s: no -out example found", name)
		}
	}
}
