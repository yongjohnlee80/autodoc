package tui

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestEveryBackgroundAnswerCanCarryAFailure: a background job's panic reaches its caller as a
// failure in the job's own answer (failed, crash.go), so every do and coreDo in this package answers
// in a shape that can carry one: an error, any, an answerOf, or a struct with an err field of type
// error. A job answering a bool, a string or a slice would leave its caller waiting on a panic, or
// read a zero answer as a success; this test names it.
func TestEveryBackgroundAnswerCanCarryAFailure(t *testing.T) {
	fset := token.NewFileSet()
	paths, _ := filepath.Glob("*.go")
	files := map[string]*ast.File{}
	pkgTypes := map[string]ast.Expr{} // the package's own types, by name
	for _, p := range paths {
		if strings.HasSuffix(p, "_test.go") {
			continue
		}
		src, err := os.ReadFile(p)
		if err != nil {
			t.Fatal(err)
		}
		f, err := parser.ParseFile(fset, p, src, 0)
		if err != nil {
			t.Fatal(err)
		}
		files[p] = f
		for _, d := range f.Decls {
			if g, ok := d.(*ast.GenDecl); ok {
				for _, s := range g.Specs {
					if ts, ok := s.(*ast.TypeSpec); ok {
						pkgTypes[ts.Name.Name] = ts.Type
					}
				}
			}
		}
	}
	checked := 0
	for _, f := range files {
		for _, d := range f.Decls {
			fd, ok := d.(*ast.FuncDecl)
			if !ok || fd.Body == nil {
				continue
			}
			local := map[string]ast.Expr{} // the types this function declares
			ast.Inspect(fd.Body, func(n ast.Node) bool {
				if ts, ok := n.(*ast.TypeSpec); ok {
					local[ts.Name.Name] = ts.Type
				}
				return true
			})
			ast.Inspect(fd.Body, func(n ast.Node) bool {
				call, ok := n.(*ast.CallExpr)
				if !ok || len(call.Args) < 2 || !isBackgroundCall(call.Fun) {
					return true
				}
				lit, ok := call.Args[1].(*ast.FuncLit)
				if !ok || lit.Type.Results == nil || len(lit.Type.Results.List) != 1 {
					return true
				}
				checked++
				res := lit.Type.Results.List[0].Type
				if !carriesFailure(res, local, pkgTypes) {
					t.Errorf("%s: a background job answers %s, which cannot carry its failure: answer an error, an answerOf, or a struct with an err error field",
						fset.Position(call.Pos()), exprString(res))
				}
				return true
			})
		}
	}
	if checked < 30 {
		t.Fatalf("only %d background jobs found: the scan missed them", checked)
	}
}

// isBackgroundCall is a call of do or coreDo, typed or not.
func isBackgroundCall(fun ast.Expr) bool {
	switch f := fun.(type) {
	case *ast.Ident:
		return f.Name == "do" || f.Name == "coreDo"
	case *ast.IndexExpr:
		return isBackgroundCall(f.X)
	case *ast.IndexListExpr:
		return isBackgroundCall(f.X)
	}
	return false
}

// carriesFailure is whether an answer of type e can carry an error (failed, crash.go).
func carriesFailure(e ast.Expr, local, pkg map[string]ast.Expr) bool {
	switch x := e.(type) {
	case *ast.Ident:
		if x.Name == "error" || x.Name == "any" {
			return true
		}
		def, ok := local[x.Name]
		if !ok {
			def, ok = pkg[x.Name]
		}
		st, isStruct := def.(*ast.StructType)
		if !ok || !isStruct {
			return false
		}
		for _, fl := range st.Fields.List {
			for _, n := range fl.Names {
				if id, isIdent := fl.Type.(*ast.Ident); n.Name == "err" && isIdent && id.Name == "error" {
					return true
				}
			}
		}
	case *ast.IndexExpr:
		id, ok := x.X.(*ast.Ident)
		return ok && id.Name == "answerOf"
	}
	return false
}

func exprString(e ast.Expr) string {
	switch x := e.(type) {
	case *ast.Ident:
		return x.Name
	case *ast.ArrayType:
		return "[]" + exprString(x.Elt)
	case *ast.IndexExpr:
		return exprString(x.X) + "[" + exprString(x.Index) + "]"
	}
	return "an unnamed type"
}
