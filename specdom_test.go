package hyperplot

import (
	"bytes"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"strings"
)

// This file is a tiny DOM over encoding/xml and the selector subset of
// testdata/conformance/README.md, used by the conformance and spec tests.

// xnode is an element, or a text node when name is empty.
type xnode struct {
	name     string // local name; "" for text
	attrs    []xml.Attr
	children []*xnode
	parent   *xnode
	data     string // text nodes only
}

// parseDOM parses one XML document (a chart, or a table fragment, which
// is well-formed too) and returns its root element.
func parseDOM(b []byte) (*xnode, error) {
	dec := xml.NewDecoder(bytes.NewReader(b))
	doc := &xnode{name: "#document"}
	cur := doc
	for {
		tok, err := dec.Token()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("not well-formed XML: %w", err)
		}
		switch tok := tok.(type) {
		case xml.StartElement:
			n := &xnode{name: tok.Name.Local, parent: cur}
			for _, a := range tok.Attr {
				name := a.Name.Local
				if a.Name.Space == "xmlns" {
					name = "xmlns:" + name
				}
				n.attrs = append(n.attrs, xml.Attr{Name: xml.Name{Local: name}, Value: a.Value})
			}
			cur.children = append(cur.children, n)
			cur = n
		case xml.EndElement:
			cur = cur.parent
		case xml.CharData:
			cur.children = append(cur.children, &xnode{parent: cur, data: string(tok)})
		}
	}
	var root *xnode
	for _, c := range doc.children {
		if c.name != "" {
			if root != nil {
				return nil, errors.New("more than one root element")
			}
			root = c
		}
	}
	if root == nil {
		return nil, errors.New("no root element")
	}
	root.parent = nil
	return root, nil
}

func (n *xnode) attr(name string) (string, bool) {
	for _, a := range n.attrs {
		if a.Name.Local == name {
			return a.Value, true
		}
	}
	return "", false
}

func (n *xnode) get(name string) string { v, _ := n.attr(name); return v }

func (n *xnode) hasClass(c string) bool {
	v, _ := n.attr("class")
	for _, f := range strings.Fields(v) {
		if f == c {
			return true
		}
	}
	return false
}

// elements returns the element children of n.
func (n *xnode) elements() []*xnode {
	var out []*xnode
	for _, c := range n.children {
		if c.name != "" {
			out = append(out, c)
		}
	}
	return out
}

// text returns the text content of n.
func (n *xnode) text() string {
	if n.name == "" {
		return n.data
	}
	var b strings.Builder
	for _, c := range n.children {
		b.WriteString(c.text())
	}
	return b.String()
}

// walk calls f for n and every element below it, in document order.
func (n *xnode) walk(f func(*xnode)) {
	if n.name == "" {
		return
	}
	f(n)
	for _, c := range n.children {
		c.walk(f)
	}
}

// descendants returns the elements below n, in document order.
func (n *xnode) descendants() []*xnode {
	var out []*xnode
	n.walk(func(e *xnode) {
		if e != n {
			out = append(out, e)
		}
	})
	return out
}

// path names an element for failure messages.
func (n *xnode) path() string {
	var parts []string
	for e := n; e != nil; e = e.parent {
		s := e.name
		if id := e.get("id"); id != "" {
			s += "#" + id
		} else if c := e.get("class"); c != "" {
			s += "." + strings.ReplaceAll(c, " ", ".")
		}
		parts = append(parts, s)
	}
	for i, j := 0, len(parts)-1; i < j; i, j = i+1, j-1 {
		parts[i], parts[j] = parts[j], parts[i]
	}
	return strings.Join(parts, " > ")
}

// compound is one compound selector: a type (or "" for any), classes, an
// id and attribute conditions.
type compound struct {
	tag     string
	id      string
	classes []string
	attrs   []attrCond
}

type attrCond struct {
	name  string
	value string
	eq    bool
}

// selector is compounds joined by combinators: comb[i] (' ' or '>') sits
// between parts[i] and parts[i+1].
type selector struct {
	parts []compound
	comb  []byte
}

// parseSelector parses the README's subset: type selectors and "*",
// ".class", "#id", "[attr]" and `[attr="value"]`, compounds of these, and
// the descendant and child combinators.
func parseSelector(s string) (selector, error) {
	var sel selector
	i := 0
	ident := func() string {
		j := i
		for j < len(s) && (s[j] == '-' || s[j] == '_' || s[j] >= '0' && s[j] <= '9' || s[j] >= 'a' && s[j] <= 'z' || s[j] >= 'A' && s[j] <= 'Z') {
			j++
		}
		out := s[i:j]
		i = j
		return out
	}
	pending := byte(0)
	for {
		ws := false
		for i < len(s) && s[i] == ' ' {
			i++
			ws = true
		}
		if i == len(s) {
			break
		}
		if s[i] == '>' {
			pending = '>'
			i++
			continue
		}
		if len(sel.parts) > 0 {
			switch {
			case pending != 0:
				sel.comb = append(sel.comb, pending)
			case ws:
				sel.comb = append(sel.comb, ' ')
			default:
				return sel, fmt.Errorf("selector %q: unexpected %q", s, s[i])
			}
		} else if pending != 0 {
			return sel, fmt.Errorf("selector %q starts with a combinator", s)
		}
		pending = 0
		var c compound
		if s[i] == '*' {
			c.tag = "*"
			i++
		} else {
			c.tag = ident()
		}
		for i < len(s) && (s[i] == '.' || s[i] == '#' || s[i] == '[') {
			switch s[i] {
			case '.':
				i++
				name := ident()
				if name == "" {
					return sel, fmt.Errorf("selector %q: empty class", s)
				}
				c.classes = append(c.classes, name)
			case '#':
				i++
				if c.id = ident(); c.id == "" {
					return sel, fmt.Errorf("selector %q: empty id", s)
				}
			case '[':
				i++
				a := attrCond{name: ident()}
				if a.name == "" {
					return sel, fmt.Errorf("selector %q: empty attribute name", s)
				}
				if i < len(s) && s[i] == '=' {
					if i+1 >= len(s) || s[i+1] != '"' {
						return sel, fmt.Errorf("selector %q: attribute values must be double-quoted", s)
					}
					end := strings.IndexByte(s[i+2:], '"')
					if end < 0 {
						return sel, fmt.Errorf("selector %q: unterminated value", s)
					}
					a.value, a.eq = s[i+2:i+2+end], true
					i += 2 + end + 1
				}
				if i >= len(s) || s[i] != ']' {
					return sel, fmt.Errorf("selector %q: expected ]", s)
				}
				i++
				c.attrs = append(c.attrs, a)
			}
		}
		if c.tag == "" && c.id == "" && len(c.classes) == 0 && len(c.attrs) == 0 {
			return sel, fmt.Errorf("selector %q: unexpected %q", s, s[i])
		}
		sel.parts = append(sel.parts, c)
	}
	if len(sel.parts) == 0 || pending != 0 {
		return sel, fmt.Errorf("selector %q is incomplete", s)
	}
	return sel, nil
}

func (c compound) matches(n *xnode) bool {
	if c.tag != "" && c.tag != "*" && c.tag != n.name {
		return false
	}
	if c.id != "" && n.get("id") != c.id {
		return false
	}
	for _, cl := range c.classes {
		if !n.hasClass(cl) {
			return false
		}
	}
	for _, a := range c.attrs {
		v, ok := n.attr(a.name)
		if !ok || a.eq && v != a.value {
			return false
		}
	}
	return true
}

// matchAt reports whether n matches parts[:i+1] with n as parts[i].
func (sel selector) matchAt(n *xnode, i int) bool {
	if !sel.parts[i].matches(n) {
		return false
	}
	if i == 0 {
		return true
	}
	if sel.comb[i-1] == '>' {
		return n.parent != nil && sel.matchAt(n.parent, i-1)
	}
	for p := n.parent; p != nil; p = p.parent {
		if sel.matchAt(p, i-1) {
			return true
		}
	}
	return false
}

// selectAll returns the elements of the tree at root (root included) that
// match sel, in document order.
func selectAll(root *xnode, sel selector) []*xnode {
	var out []*xnode
	root.walk(func(n *xnode) {
		if sel.matchAt(n, len(sel.parts)-1) {
			out = append(out, n)
		}
	})
	return out
}

// query parses s and returns its matches, panicking on a bad selector
// (tests pass literal selectors).
func query(root *xnode, s string) []*xnode {
	sel, err := parseSelector(s)
	if err != nil {
		panic(err)
	}
	return selectAll(root, sel)
}
