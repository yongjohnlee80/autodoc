//go:build gui

package app

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"testing"

	"github.com/yongjohnlee80/autodoc/tui"
)

// runGUI opens its window as windowFor says: the options windowFor returns are the ones the
// backend is made with, and the launch options it returns are the ones launched. runGUI only runs
// inside gui.Main with a real window, so this reads its source: a runGUI that built its options
// elsewhere, or launched the options it was given, would open a window that ignores the title
// bar's mirror while every other cell passed.
func TestTheGUIOpensItsWindowThroughWindowFor(t *testing.T) {
	f, err := parser.ParseFile(token.NewFileSet(), "gui_on.go", nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	var run *ast.FuncDecl
	for _, d := range f.Decls {
		if fn, ok := d.(*ast.FuncDecl); ok && fn.Name.Name == "runGUI" {
			run = fn
		}
	}
	if run == nil {
		t.Fatal("gui_on.go has no runGUI")
	}
	var fromWindowFor, toBackend, launched bool
	ast.Inspect(run.Body, func(n ast.Node) bool {
		switch n := n.(type) {
		case *ast.AssignStmt: // o, opts = windowFor(o)
			if len(n.Lhs) == 2 && len(n.Rhs) == 1 && isIdent(n.Lhs[0], "o") && isIdent(n.Lhs[1], "opts") {
				if c, ok := n.Rhs[0].(*ast.CallExpr); ok && isIdent(c.Fun, "windowFor") && len(c.Args) == 1 && isIdent(c.Args[0], "o") {
					fromWindowFor = true
				}
			}
		case *ast.CallExpr:
			if s, ok := n.Fun.(*ast.SelectorExpr); ok && isIdent(s.X, "gui") && s.Sel.Name == "NewBackend" &&
				len(n.Args) == 1 && n.Ellipsis.IsValid() && isIdent(n.Args[0], "opts") {
				toBackend = true // gui.NewBackend(opts...)
			}
			if s, ok := n.Fun.(*ast.SelectorExpr); ok && isIdent(s.X, "tui") && s.Sel.Name == "Launch" &&
				len(n.Args) == 2 && isIdent(n.Args[1], "o") {
				launched = true // tui.Launch(ctx, o)
			}
		}
		return true
	})
	if !fromWindowFor || !toBackend || !launched {
		t.Fatalf("runGUI: options from windowFor %v, the backend made with them %v, the launch options launched %v; want all three",
			fromWindowFor, toBackend, launched)
	}
}

func isIdent(e ast.Expr, name string) bool {
	id, ok := e.(*ast.Ident)
	return ok && id.Name == name
}

// The window opens as tui.gui.titlebar's mirror says, and both halves go together: with AutoDoc's
// own bar the window gets one option more (no frame of the system's) and the launch asks for the
// bar in the layout; with the system's, or no mirror, neither. A window without the system's frame
// and without AutoDoc's bar would have no title bar at all, and the system's frame over AutoDoc's
// bar would draw two.
func TestTheWindowOpensAsTheTitleBarMirrorSays(t *testing.T) {
	dir := t.TempDir()
	state := filepath.Join(dir, "state")
	if err := os.MkdirAll(state, 0o700); err != nil {
		t.Fatal(err)
	}
	conf := filepath.Join(dir, "config.toml")
	// TOML literal strings: a path's separators are written as they are, on every OS
	body := "[server]\nsocket = '" + filepath.Join(dir, "s.sock") + "'\nstate_dir = '" + state + "'\ndata_dir = '" + filepath.Join(dir, "data") + "'\n"
	if err := os.WriteFile(conf, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	mirror := tui.GUITitleBarFile(conf)
	if mirror == "" {
		t.Fatal("the config's state directory was not read")
	}
	open := func(name string) (bool, int) {
		t.Helper()
		o, opts := windowFor(tui.LaunchOptions{ConfigPath: conf})
		return o.TitleBar, len(opts)
	}
	bar, base := open("no mirror")
	if bar {
		t.Fatal("no mirror: the launch asks for AutoDoc's bar; want the system's title bar")
	}
	for _, v := range []string{tui.TitleBarSystem, "something else"} {
		if err := os.WriteFile(mirror, []byte(v+"\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		if bar, n := open(v); bar || n != base {
			t.Fatalf("mirror %q: bar %v, %d options; want the system's title bar, %d options", v, bar, n, base)
		}
	}
	if err := os.WriteFile(mirror, []byte(tui.TitleBarOwn+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if bar, n := open(tui.TitleBarOwn); !bar || n != base+1 {
		t.Fatalf("mirror %q: bar %v, %d options; want AutoDoc's bar asked for and %d options (no frame of the system's)", tui.TitleBarOwn, bar, n, base+1)
	}
}
