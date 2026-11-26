// Reading the gallery's own source. charts.go is embedded into the binary
// and parsed once at startup with go/parser; each declaration is cut out
// of it by position, doc comment included. The page therefore shows the
// exact text that was compiled, and an entry cannot show one func while
// rendering another: the gallery finds the source by the name of the func
// value it calls.
package main

import (
	_ "embed"
	"go/ast"
	"go/parser"
	"go/scanner"
	"go/token"
	"html"
	"html/template"
	"reflect"
	"runtime"
	"strings"
)

//go:embed charts.go
var chartsGo []byte

// decl is one top-level declaration of charts.go.
type decl struct {
	Name string // the func's name; "" for a var or const block
	Code string // its source text, doc comment included
}

// parseDecls returns the declarations of a Go file in file order, imports
// left out.
func parseDecls(src []byte) ([]decl, error) {
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, "charts.go", src, parser.ParseComments)
	if err != nil {
		return nil, err
	}
	file := fset.File(f.Pos())
	var decls []decl
	for _, d := range f.Decls {
		var name string
		var doc *ast.CommentGroup
		switch d := d.(type) {
		case *ast.FuncDecl:
			name, doc = d.Name.Name, d.Doc
		case *ast.GenDecl:
			if d.Tok == token.IMPORT {
				continue
			}
			doc = d.Doc
		}
		from := d.Pos()
		if doc != nil {
			from = doc.Pos()
		}
		decls = append(decls, decl{Name: name, Code: string(src[file.Offset(from):file.Offset(d.End())])})
	}
	return decls, nil
}

// funcName returns the name of a top-level func value: "lineGaps" for
// lineGaps. The runtime knows it as "main.lineGaps".
func funcName(fn any) string {
	full := runtime.FuncForPC(reflect.ValueOf(fn).Pointer()).Name()
	return full[strings.LastIndexByte(full, '.')+1:]
}

// kebab turns a func name into a chart id: "lineLinearX" becomes
// "line-linear-x".
func kebab(name string) string {
	var b strings.Builder
	for _, r := range name {
		if 'A' <= r && r <= 'Z' {
			b.WriteByte('-')
			r += 'a' - 'A'
		}
		b.WriteRune(r)
	}
	return b.String()
}

// highlight escapes Go source for HTML and wraps comments, strings,
// numbers and keywords in spans that app.css colors. Everything between
// those tokens is copied through, escaped, so the text reads exactly as
// the file does.
func highlight(code string) template.HTML {
	fset := token.NewFileSet()
	file := fset.AddFile("", fset.Base(), len(code))
	var s scanner.Scanner
	s.Init(file, []byte(code), nil, scanner.ScanComments)

	var b strings.Builder
	last := 0
	for {
		pos, tok, lit := s.Scan()
		if tok == token.EOF {
			break
		}
		var class string
		switch {
		case tok == token.COMMENT:
			class = "tok-c"
		case tok == token.STRING || tok == token.CHAR:
			class = "tok-s"
		case tok == token.INT || tok == token.FLOAT:
			class = "tok-n"
		case tok.IsKeyword():
			class = "tok-k"
		default:
			continue // operators, names, inserted semicolons: copied as plain text
		}
		at := file.Offset(pos)
		b.WriteString(html.EscapeString(code[last:at]))
		b.WriteString(`<span class="` + class + `">` + html.EscapeString(lit) + `</span>`)
		last = at + len(lit)
	}
	b.WriteString(html.EscapeString(code[last:]))
	// Every byte of code went through html.EscapeString, and the only
	// markup added is the spans above, so the result is safe as HTML.
	return template.HTML(b.String())
}
