package hx

import (
	"go/ast"
	"go/parser"
	"go/token"
	"strings"
	"testing"
)

// TestExamplesSizeKeepsServerHeight checks that the package examples,
// which pkg.go.dev shows next to Size, discard the height from hx.Size.
// A data-hp-autosize container without a CSS height is as tall as the
// chart inside it, so a sample that renders at the reported height grows
// the chart on every poll when copied. Only examples whose name ends in
// "_both", which document data-hp-autosize="both", may use the height.
// docs/snippets_test.go applies the same rule to the guides and doc.go.
func TestExamplesSizeKeepsServerHeight(t *testing.T) {
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, "example_test.go", nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	calls := 0
	for _, decl := range f.Decls {
		fn, ok := decl.(*ast.FuncDecl)
		if !ok || fn.Body == nil {
			continue
		}
		both := strings.HasSuffix(fn.Name.Name, "_both")
		discarded := map[ast.Expr]bool{}
		ast.Inspect(fn.Body, func(n ast.Node) bool {
			if as, ok := n.(*ast.AssignStmt); ok && len(as.Lhs) == 2 && len(as.Rhs) == 1 {
				if id, ok := as.Lhs[1].(*ast.Ident); ok && id.Name == "_" {
					discarded[as.Rhs[0]] = true
				}
			}
			return true
		})
		ast.Inspect(fn.Body, func(n ast.Node) bool {
			call, ok := n.(*ast.CallExpr)
			if !ok || !isHxSize(call) {
				return true
			}
			calls++
			if !both && !discarded[call] {
				t.Errorf("%s: %s uses the height from hx.Size; write `width, _ := hx.Size(r, 640, 0)`, or name the example *_both and say it needs a CSS height",
					fset.Position(call.Pos()), fn.Name.Name)
			}
			return true
		})
	}
	if calls == 0 {
		t.Fatal("found no hx.Size calls in example_test.go; is the check broken?")
	}
}

// isHxSize reports whether call is hx.Size(...).
func isHxSize(call *ast.CallExpr) bool {
	sel, ok := call.Fun.(*ast.SelectorExpr)
	if !ok || sel.Sel.Name != "Size" {
		return false
	}
	pkg, ok := sel.X.(*ast.Ident)
	return ok && pkg.Name == "hx"
}
