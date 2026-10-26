package tui

import (
	"go/ast"
	"go/parser"
	"go/token"
	"slices"
	"sort"
	"strings"
	"testing"
)

// PARALLEL TESTS (the tui test-runtime task, step 3b) — a test marked t.Parallel() runs beside the
// other parallel tests of the package, so it must not touch what the process shares: the
// environment (t.Setenv, os.Setenv), the working directory (Chdir), or a package-level variable.
// parallelHazards finds those reached from each parallel test, through the functions it calls in
// this package, transitively. It reads calls by name, a method by its name alone, so it can see a
// hazard a test doesn't reach (never miss one it does, within the package); what it can't see,
// calls through function values and state golib keeps, is -race's to catch.

// parallelHazards are the package's tests that call Parallel, each with the path to the first
// global effect it reaches: "TestX → helper → t.Setenv".
func parallelHazards(files []*ast.File) map[string]string {
	globals := map[string]bool{}
	funcs := map[string]*ast.FuncDecl{}
	for _, f := range files {
		for _, d := range f.Decls {
			switch d := d.(type) {
			case *ast.GenDecl:
				if d.Tok == token.VAR {
					for _, s := range d.Specs {
						for _, n := range s.(*ast.ValueSpec).Names {
							globals[n.Name] = true
						}
					}
				}
			case *ast.FuncDecl:
				key := d.Name.Name
				if d.Recv != nil {
					key = "." + d.Name.Name
				}
				if _, ok := funcs[key]; !ok {
					funcs[key] = d
				}
			}
		}
	}
	effects, callees, parallel := map[string][]string{}, map[string][]string{}, map[string]bool{}
	for key, fn := range funcs {
		if fn.Body == nil {
			continue
		}
		// a function's own names (its parameters and results, its var declarations, := and range
		// variables) are never the package's, whatever they shadow
		locals := map[string]bool{}
		for _, fl := range []*ast.FieldList{fn.Type.Params, fn.Type.Results, fn.Recv} {
			if fl != nil {
				for _, f := range fl.List {
					for _, n := range f.Names {
						locals[n.Name] = true
					}
				}
			}
		}
		ast.Inspect(fn.Body, func(n ast.Node) bool {
			switch n := n.(type) {
			case *ast.ValueSpec:
				for _, id := range n.Names {
					locals[id.Name] = true
				}
			case *ast.RangeStmt:
				for _, e := range []ast.Expr{n.Key, n.Value} {
					if id, ok := e.(*ast.Ident); ok && n.Tok == token.DEFINE {
						locals[id.Name] = true
					}
				}
			case *ast.FuncLit:
				for _, f := range n.Type.Params.List {
					for _, id := range f.Names {
						locals[id.Name] = true
					}
				}
			}
			return true
		})
		var eff, cal []string
		ast.Inspect(fn.Body, func(n ast.Node) bool {
			switch n := n.(type) {
			case *ast.AssignStmt:
				if n.Tok == token.DEFINE {
					for _, l := range n.Lhs {
						if id, ok := l.(*ast.Ident); ok {
							locals[id.Name] = true
						}
					}
					return true
				}
				for _, l := range n.Lhs {
					root := l
					for {
						if s, ok := root.(*ast.SelectorExpr); ok {
							root = s.X
						} else if s, ok := root.(*ast.IndexExpr); ok {
							root = s.X
						} else {
							break
						}
					}
					if id, ok := root.(*ast.Ident); ok && id.Name != "_" && globals[id.Name] && !locals[id.Name] {
						eff = append(eff, "var "+id.Name)
					}
				}
			case *ast.CallExpr:
				switch f := n.Fun.(type) {
				case *ast.Ident:
					cal = append(cal, f.Name)
				case *ast.SelectorExpr:
					if x, ok := f.X.(*ast.Ident); ok {
						switch f.Sel.Name {
						case "Setenv", "Unsetenv", "Chdir":
							eff = append(eff, x.Name+"."+f.Sel.Name)
						case "Parallel":
							parallel[key] = true
						}
					}
					cal = append(cal, "."+f.Sel.Name)
				}
			}
			return true
		})
		sort.Strings(eff)
		effects[key], callees[key] = eff, cal
	}
	// path is the shortest call path from key to a global effect, breadth first
	path := func(key string) string {
		type step struct{ at string }
		prev := map[string]step{key: {}}
		queue := []string{key}
		for len(queue) > 0 {
			k := queue[0]
			queue = queue[1:]
			if len(effects[k]) > 0 {
				chain := []string{effects[k][0]}
				for at := k; at != ""; at = prev[at].at {
					chain = append(chain, strings.TrimPrefix(at, "."))
				}
				slices.Reverse(chain)
				return strings.Join(chain, " → ")
			}
			for _, c := range callees[k] {
				if _, ok := funcs[c]; !ok {
					continue
				}
				if _, seen := prev[c]; !seen {
					prev[c] = step{at: k}
					queue = append(queue, c)
				}
			}
		}
		return ""
	}
	out := map[string]string{}
	for key := range parallel {
		if !strings.HasPrefix(key, "Test") {
			continue
		}
		if p := path(key); p != "" {
			out[key] = p
		}
	}
	return out
}

func parsePackage(t *testing.T, dir string, src map[string]string) []*ast.File {
	t.Helper()
	fset := token.NewFileSet()
	var files []*ast.File
	if src != nil {
		for name, s := range src {
			f, err := parser.ParseFile(fset, name, s, 0)
			if err != nil {
				t.Fatal(err)
			}
			files = append(files, f)
		}
		return files
	}
	pkgs, err := parser.ParseDir(fset, dir, nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	for _, p := range pkgs {
		for _, f := range p.Files {
			files = append(files, f)
		}
	}
	return files
}

// TestParallelTestsTouchNoGlobalState: no tui test marked t.Parallel() reaches t.Setenv, os.Setenv,
// a Chdir, or a write of a package-level variable. Each one that does is named with its path; it
// stays serial, or its state moves into the test (a per-test config, a field).
func TestParallelTestsTouchNoGlobalState(t *testing.T) {
	hazards := parallelHazards(parsePackage(t, ".", nil))
	names := make([]string, 0, len(hazards))
	for name := range hazards {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		t.Errorf("%s is parallel and reaches global state: %s", name, hazards[name])
	}
}

// TestTheParallelAuditSeesThroughHelpers: the audit itself, on a package of its own. A parallel test
// reaching t.Setenv through a helper, one writing a package var through a method, and one calling
// Chdir are each named with the path; a serial test touching all three, and parallel tests that
// touch only their own names (a local, a parameter, a var or range variable shadowing a package
// var), are not.
func TestTheParallelAuditSeesThroughHelpers(t *testing.T) {
	src := `package p
import ("os"; "testing")
var registry = map[string]int{}
var limit int
var _ = registry // a package-level blank: "_ = x" writes nothing
type cfg struct{}
func (cfg) store() { registry["x"] = 1 }
func withHome(t *testing.T) { t.Setenv("HOME", t.TempDir()) }
func TestEnv(t *testing.T) { t.Parallel(); withHome(t) }
func TestVar(t *testing.T) { t.Parallel(); var c cfg; c.store() }
func TestDir(t *testing.T) { t.Parallel(); os.Chdir("/") }
func TestSerial(t *testing.T) { withHome(t); limit = 3; os.Chdir("/") }
func TestLocal(t *testing.T) { t.Parallel(); limit := 2; limit = 3; _ = limit }
func shadow(limit int) { limit = 4; var registry []int; registry = append(registry, 1); for limit := range 2 { limit = 5 } }
func TestShadow(t *testing.T) { t.Parallel(); shadow(1) }
`
	got := parallelHazards(parsePackage(t, "", map[string]string{"p_test.go": src}))
	want := map[string]string{
		"TestEnv": "TestEnv → withHome → t.Setenv",
		"TestVar": "TestVar → store → var registry",
		"TestDir": "TestDir → os.Chdir",
	}
	if len(got) != len(want) {
		t.Errorf("hazards %v, want %v", got, want)
	}
	for name, p := range want {
		if got[name] != p {
			t.Errorf("%s: %q, want %q", name, got[name], p)
		}
	}
}
