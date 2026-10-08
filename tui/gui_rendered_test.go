//go:build gui

package tui

import (
	"testing"

	guidecl "github.com/yongjohnlee80/golib/gui/decl"
	guiwidget "github.com/yongjohnlee80/golib/gui/widget"
	tuicore "github.com/yongjohnlee80/golib/tui"
	tuidecl "github.com/yongjohnlee80/golib/tui/decl"
)

// The Rendered view follows the page's kind: a note renders; a Go file opened after it is Raw and
// Ctrl+T does nothing there; the draft left by closing it renders; and a note opened from the
// rendered draft keeps the view, Ctrl+T switching it back. No window opens.
func TestTheRenderedViewFollowsTheFilesKind(t *testing.T) {
	d := startDaemonWith(t, "", map[string][]string{
		"kb": {"a.md", "# Alpha\n\n## a heading\n", "a.go", goWithAHashLine, "b.md", "# Bravo\n"},
	}, daemonOpts{goChunker: wholeGo{}})
	r := runTUI(t, NewSession(d.sock, nil), Options{ProgramOptions: []tuidecl.ProgramOption{tuidecl.WithStyle(guidecl.Native())}})
	r.s.WaitForText(t, "connected — autodoc v-test")
	r.s.WaitFor(t, "the notes listed", func(string) bool { return len(r.listed()) > 0 })
	ed := func() *guiwidget.Editor {
		return onLoop(r, func() *guiwidget.Editor { e, _ := r.h.editor.(*guiwidget.Editor); return e })
	}
	if ed() == nil {
		t.Fatal("the editor under the native style is not gui's Editor")
	}
	// on the loop, as one call: ed() is its own onLoop, and nesting one in another waits forever
	mode := func() guiwidget.EditorMode {
		return onLoop(r, func() guiwidget.EditorMode { return r.h.editor.(*guiwidget.Editor).Mode() })
	}
	// Keys reach the gui Editor after r.keys returns: it draws natively, so the cells do not
	// change, and the screen gives no sign. ctrlT is a barrier: Ctrl+T, then l, which moves the
	// cursor one column; the cursor moving means Ctrl+T, sent first, was handled.
	ctrlT := func() {
		col := onLoop(r, func() int { _, c := r.h.core.Line(); return c })
		r.keys(t, tuicore.KeyEvent{Kind: tuicore.KeyPress, Code: 't', Mods: tuicore.ModCtrl},
			tuicore.KeyEvent{Kind: tuicore.KeyPress, Code: 'l', Text: "l"})
		r.s.WaitFor(t, "Ctrl+T handled", func(string) bool {
			return onLoop(r, func() int { _, c := r.h.core.Line(); return c }) != col
		})
	}

	onLoop(r, func() bool { r.h.openPath("a.md"); return true })
	r.waitFile(t, "a.md")
	ctrlT()
	if m := mode(); m != guiwidget.Rendered {
		t.Fatalf("a note: Ctrl+T gives %v, want Rendered", m)
	}

	onLoop(r, func() bool { r.h.openPath("a.go"); return true })
	r.waitFile(t, "a.go")
	if m := mode(); m != guiwidget.Raw {
		t.Fatalf("a Go file opened after a rendered note is %v, want Raw", m)
	}
	ctrlT()
	if m := mode(); m != guiwidget.Raw {
		t.Fatalf("Ctrl+T on a Go file gives %v, want Raw: its # line must not render as a heading", m)
	}

	// closing the Go file leaves the untitled draft, which is Markdown: Ctrl+T renders it
	onLoop(r, func() bool { r.h.closeFile(); return true })
	r.s.WaitFor(t, "the draft", func(string) bool { return !r.file().open })
	onLoop(r, func() bool { r.h.core.SetValue("# Draft heading\nmore\n"); return true })
	ctrlT()
	if m := mode(); m != guiwidget.Rendered {
		t.Fatalf("the draft after a Go file: Ctrl+T gives %v, want Rendered", m)
	}

	// a note opened from the rendered draft keeps the view chosen: Markdown to Markdown, the view
	// is the user's; Ctrl+T switches it back
	onLoop(r, func() bool { r.h.openPath("b.md"); return true })
	r.waitFile(t, "b.md")
	if m := mode(); m != guiwidget.Rendered {
		t.Fatalf("a note opened from the rendered draft is %v, want Rendered (the view kept)", m)
	}
	ctrlT()
	if m := mode(); m != guiwidget.Raw {
		t.Fatalf("Ctrl+T on a rendered note gives %v, want Raw", m)
	}
}
