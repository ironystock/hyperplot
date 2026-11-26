package docs

import (
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"unicode"
)

// mdLink matches an inline Markdown link or image target, and mdRef a
// reference definition. Titles ("…") after the target are allowed.
var (
	mdLink = regexp.MustCompile(`\]\(<?([^)\s>]+)>?(?:\s+"[^"]*")?\)`)
	mdRef  = regexp.MustCompile(`^\s*\[[^\]]+\]:\s+<?([^\s>]+)>?`)
	mdHead = regexp.MustCompile(`^(#{1,6})\s+(.*?)\s*#*\s*$`)
	htmlID = regexp.MustCompile(`<a\s+(?:id|name)="([^"]+)"`)
	// inline links and images in heading text keep only their text.
	headLink = regexp.MustCompile(`!?\[([^\]]*)\]\([^)]*\)`)
	htmlTag  = regexp.MustCompile(`<[^>]+>`)
	codeSpan = regexp.MustCompile("`+[^`]*`+")
)

// slug returns GitHub's anchor for a heading's text: lower case, with
// everything but letters, digits, spaces, hyphens and underscores dropped,
// and spaces turned into hyphens.
func slug(text string) string {
	text = headLink.ReplaceAllString(text, "$1")
	text = htmlTag.ReplaceAllString(text, "")
	var b strings.Builder
	for _, r := range strings.ToLower(text) {
		switch {
		case r == ' ':
			b.WriteByte('-')
		case r == '-' || r == '_' || unicode.IsLetter(r) || unicode.IsDigit(r):
			b.WriteRune(r)
		}
	}
	return b.String()
}

// markdown is one Markdown file split into the lines outside fenced code
// blocks, with the anchors its headings and HTML ids define.
type markdown struct {
	lines   []string // outside code fences; fenced lines are blank
	anchors map[string]bool
}

func readMarkdown(t *testing.T, path string) *markdown {
	t.Helper()
	src, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	md := &markdown{anchors: map[string]bool{}}
	seen := map[string]int{}
	fence := ""
	for _, line := range strings.Split(string(src), "\n") {
		trim := strings.TrimSpace(line)
		if fence != "" {
			if strings.HasPrefix(trim, fence) {
				fence = ""
			}
			md.lines = append(md.lines, "")
			continue
		}
		if strings.HasPrefix(trim, "```") || strings.HasPrefix(trim, "~~~") {
			fence = trim[:3]
			md.lines = append(md.lines, "")
			continue
		}
		md.lines = append(md.lines, line)
		for _, m := range htmlID.FindAllStringSubmatch(line, -1) {
			md.anchors[m[1]] = true
		}
		if m := mdHead.FindStringSubmatch(line); m != nil {
			s := slug(m[2])
			if n := seen[s]; n > 0 {
				md.anchors[s+"-"+strconv.Itoa(n)] = true
			} else {
				md.anchors[s] = true
			}
			seen[s]++
		}
	}
	return md
}

// TestMarkdownLinks checks every relative link in the repository's Markdown
// files: the file or directory must exist, and an anchor into a Markdown
// file must name one of its headings (as GitHub slugs them) or an HTML id.
// A renamed heading otherwise leaves links that open at the top of the page.
func TestMarkdownLinks(t *testing.T) {
	var files []string
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			name := d.Name()
			if path != root && (strings.HasPrefix(name, ".") && name != ".github" || name == "node_modules") {
				return filepath.SkipDir
			}
			return nil
		}
		if strings.HasSuffix(path, ".md") {
			files = append(files, path)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(files) < 10 {
		t.Fatalf("found only %d Markdown files under %s", len(files), root)
	}
	cache := map[string]*markdown{}
	load := func(path string) *markdown {
		if md, ok := cache[path]; ok {
			return md
		}
		md := readMarkdown(t, path)
		cache[path] = md
		return md
	}
	checked := 0
	for _, file := range files {
		md := load(file)
		for i, line := range md.lines {
			line = codeSpan.ReplaceAllString(line, "")
			var targets []string
			for _, m := range mdLink.FindAllStringSubmatch(line, -1) {
				targets = append(targets, m[1])
			}
			if m := mdRef.FindStringSubmatch(line); m != nil {
				targets = append(targets, m[1])
			}
			for _, target := range targets {
				if strings.Contains(target, "://") || strings.HasPrefix(target, "mailto:") {
					continue
				}
				checked++
				where := filepath.ToSlash(file) + ":" + strconv.Itoa(i+1) + ": " + target
				path, anchor, _ := strings.Cut(target, "#")
				dest := file
				if path != "" {
					dest = filepath.Join(filepath.Dir(file), filepath.FromSlash(path))
					if _, err := os.Stat(dest); err != nil {
						t.Errorf("%s: no such file", where)
						continue
					}
				}
				if anchor == "" || !strings.HasSuffix(dest, ".md") {
					continue
				}
				if !load(dest).anchors[anchor] {
					t.Errorf("%s: no heading or id with that anchor", where)
				}
			}
		}
	}
	if checked == 0 {
		t.Fatal("no relative links found; is the link pattern out of date?")
	}
}
