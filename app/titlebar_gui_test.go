//go:build gui

package app

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"testing"

	"github.com/yongjohnlee80/golib/gui"

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
// own bar the window asks for no frame of the system's (WithDecorations(false), read back as a value
// through gui.ResolveOptions, the options the backend is made with) and the launch asks for the bar
// in the layout; with the system's, or no mirror, the window leaves decorations to the platform and
// the launch asks for no bar. A window without the system's frame and without AutoDoc's bar would
// have no title bar at all, and the system's frame over AutoDoc's bar would draw two. It proves what
// windowFor asks for, not what a platform then draws (that is golib's, and its window chrome's cells).
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
	// open answers whether the launch asks for AutoDoc's bar, and what the window asks for: its
	// decorations (on, and whether an option chose them) and its title.
	open := func() (bar, on, set bool, title string) {
		t.Helper()
		o, opts := windowFor(tui.LaunchOptions{ConfigPath: conf})
		w := gui.ResolveOptions(opts...)
		on, set = w.Decorations()
		return o.TitleBar, on, set, w.Title()
	}
	systems := func(what string) {
		t.Helper()
		if bar, _, set, title := open(); bar || set || title != "AutoDoc" {
			t.Fatalf("%s: the launch asks for AutoDoc's bar %v, the window chooses its decorations %v, title %q; want the system's title bar: no bar, decorations left to the platform, titled AutoDoc", what, bar, set, title)
		}
	}
	systems("no mirror")
	for _, v := range []string{tui.TitleBarSystem, "something else"} {
		if err := os.WriteFile(mirror, []byte(v+"\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		systems("mirror " + v)
	}
	if err := os.WriteFile(mirror, []byte(tui.TitleBarOwn+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if bar, on, set, title := open(); !bar || on || !set || title != "AutoDoc" {
		t.Fatalf("mirror %q: the launch asks for AutoDoc's bar %v, decorations on %v (chosen %v), title %q; want the bar, and a window titled AutoDoc that asks for no frame of the system's", tui.TitleBarOwn, bar, on, set, title)
	}
}
