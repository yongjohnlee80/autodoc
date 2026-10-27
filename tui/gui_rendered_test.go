//go:build gui

package tui

import (
	"strings"
	"testing"

	guiwidget "github.com/yongjohnlee80/golib/gui/widget"
	tuicore "github.com/yongjohnlee80/golib/tui"
	tuidecl "github.com/yongjohnlee80/golib/tui/decl"
	"github.com/yongjohnlee80/golib/tui/decl/decltest"
	tuiwidget "github.com/yongjohnlee80/golib/tui/widget"
)

// The window opens in the Rendered view, and the view drawn follows the page's kind: a note renders;
// a Go file opened after it is drawn Raw, Rendered kept as the choice, and Ctrl+T does nothing
// there; the draft left by closing it is drawn Rendered again; and a note opened from the rendered
// draft keeps the view, Ctrl+T switching it back. No window opens.
func TestTheRenderedViewFollowsTheFilesKind(t *testing.T) {
	d := startDaemonWith(t, "", map[string][]string{
		"kb": {"a.md", "# Alpha\n\n## a heading\n", "a.go", goWithAHashLine, "b.md", "# Bravo\n"},
	}, daemonOpts{goChunker: wholeGo{}})
	r := runTUI(t, NewSession(d.sock, nil), Options{ProgramOptions: []tuidecl.ProgramOption{tuidecl.WithStyle(GUIStyle(&NoteEmbeds{}))}})
	r.connected(t)
	r.s.WaitFor(t, "the notes listed", func(string) bool { return len(r.listed()) > 0 })
	ed := func() *guiwidget.Editor {
		return onLoop(r, func() *guiwidget.Editor { e, _ := r.h.editor.(*guiwidget.Editor); return e })
	}
	if ed() == nil {
		t.Fatal("the editor under the native style is not gui's Editor")
	}
	// on the loop, as one call: ed() is its own onLoop, and nesting one in another waits forever
	view := func() tuiwidget.EditorView {
		return onLoop(r, func() tuiwidget.EditorView { return r.h.editor.(*guiwidget.Editor).View() })
	}
	ctrlT := func() { pageCtrlT(t, r) }

	onLoop(r, func() bool { r.h.openPath("a.md"); return true })
	r.waitFile(t, "a.md")
	// the window opens in the Rendered view: a note reads as it will look
	if m := view(); m != tuiwidget.ViewRendered {
		t.Fatalf("a note as the window opens: %v, want Rendered", m)
	}
	ctrlT()
	if m := view(); m != tuiwidget.ViewRaw {
		t.Fatalf("a note: Ctrl+T from Rendered gives %v, want Raw", m)
	}
	ctrlT()
	if m := view(); m != tuiwidget.ViewRendered {
		t.Fatalf("a note: Ctrl+T gives %v, want Rendered", m)
	}

	// what the editor draws, apart from the view the user chose (View): a document that cannot
	// render is drawn Raw while Rendered stays the choice
	shown := func() tuiwidget.EditorView {
		return onLoop(r, func() tuiwidget.EditorView { return r.h.editor.(*guiwidget.Editor).ShownView() })
	}
	onLoop(r, func() bool { r.h.openPath("a.go"); return true })
	r.waitFile(t, "a.go")
	if s, m := shown(), view(); s != tuiwidget.ViewRaw || m != tuiwidget.ViewRendered {
		t.Fatalf("a Go file opened after a rendered note is drawn %v, the choice %v; want drawn Raw, Rendered kept", s, m)
	}
	ctrlT()
	if s := shown(); s != tuiwidget.ViewRaw {
		t.Fatalf("Ctrl+T on a Go file draws %v, want Raw: its # line must not render as a heading", s)
	}

	// closing the Go file leaves the untitled draft, which is Markdown: Rendered, the choice kept,
	// is drawn again
	onLoop(r, func() bool { r.h.closeFile(); return true })
	r.s.WaitFor(t, "the draft", func(string) bool { return !r.file().open })
	onLoop(r, func() bool { r.h.core.SetValue("# Draft heading\nmore\n"); return true })
	if s := shown(); s != tuiwidget.ViewRendered {
		t.Fatalf("the draft after a Go file is drawn %v, want Rendered: the choice was kept", s)
	}

	// a note opened from the rendered draft keeps the view chosen: Markdown to Markdown, the view
	// is the user's; Ctrl+T switches it back
	onLoop(r, func() bool { r.h.openPath("b.md"); return true })
	r.waitFile(t, "b.md")
	if s := shown(); s != tuiwidget.ViewRendered {
		t.Fatalf("a note opened from the rendered draft is drawn %v, want Rendered (the view kept)", s)
	}
	ctrlT()
	if s, m := shown(), view(); s != tuiwidget.ViewRaw || m != tuiwidget.ViewRaw {
		t.Fatalf("Ctrl+T on a rendered note: drawn %v, the choice %v; want Raw both", s, m)
	}
}

// pageCtrlT is Ctrl+T on the page. Keys reach the gui Editor after r.keys returns: it draws
// natively, so the cells do not change, and the screen gives no sign. It is a barrier: Ctrl+T,
// then l, which moves the cursor one column; the cursor moving means Ctrl+T, sent first, was
// handled.
func pageCtrlT(t *testing.T, r *running) {
	t.Helper()
	col := onLoop(r, func() int { _, c := r.h.core.Line(); return c })
	r.keys(t, tuicore.KeyEvent{Kind: tuicore.KeyPress, Code: 't', Mods: tuicore.ModCtrl},
		tuicore.KeyEvent{Kind: tuicore.KeyPress, Code: 'l', Text: "l"})
	r.s.WaitFor(t, "Ctrl+T handled", func(string) bool {
		return onLoop(r, func() int { _, c := r.h.core.Line(); return c }) != col
	})
}

// Ctrl+T is the terminal's with the keyboard in it, toggling its mode, and never reaches the
// page's Rendered view; with the keyboard on the page it is the page's, and the terminal, hidden,
// keeps its mode: Esc, shown again, is still its program's (cat -v shows the 0x1b as ^[).
func TestCtrlTIsTheTerminalsInItAndThePagesOnThePage(t *testing.T) {
	t.Setenv("SHELL", "/bin/sh")
	t.Setenv("HOME", t.TempDir())
	t.Setenv("PS1", "$ ")
	d := startDaemonWith(t, "", map[string][]string{"kb": {"a.md", "# Alpha\n\nsome text\n"}}, daemonOpts{})
	r := runTUI(t, NewSession(d.sock, nil), Options{ProgramOptions: []tuidecl.ProgramOption{tuidecl.WithStyle(GUIStyle(&NoteEmbeds{}))}})
	r.connected(t)
	r.s.WaitFor(t, "the notes listed", func(string) bool { return len(r.listed()) > 0 })
	view := func() tuiwidget.EditorView {
		return onLoop(r, func() tuiwidget.EditorView { return r.h.editor.(*guiwidget.Editor).View() })
	}
	onLoop(r, func() bool { r.h.openPath("a.md"); return true })
	r.waitFile(t, "a.md")
	if m := view(); m != tuiwidget.ViewRendered {
		t.Fatalf("a note as the window opens: %v, want Rendered", m)
	}

	onLoop(r, func() bool { r.h.terminals.toggleTerminal(); return true })
	r.s.WaitFor(t, "the terminal's keyboard", func(string) bool { return r.focused("terminalView") })
	r.keys(t, decltest.Type("cat -v\r")...)
	// Normal mode and back: b, between, reaches no program; Esc after is cat's
	r.keys(t, tuicore.KeyEvent{Kind: tuicore.KeyPress, Code: 't', Mods: tuicore.ModCtrl}, key('b'),
		tuicore.KeyEvent{Kind: tuicore.KeyPress, Code: 't', Mods: tuicore.ModCtrl}, esc())
	r.keys(t, decltest.Type("panel\r")...)
	r.s.WaitForText(t, "^[panel")
	if sc := r.s.String(); strings.Contains(sc, "b^[panel") || strings.Contains(sc, "^T") {
		t.Fatalf("Normal mode's b, or Ctrl+T itself, reached the program:\n%s", sc)
	}
	if m := view(); m != tuiwidget.ViewRendered {
		t.Fatalf("Ctrl+T in the terminal reached the page: %v, want Rendered", m)
	}

	onLoop(r, func() bool { r.h.terminals.toggleTerminal(); return true })
	r.s.WaitFor(t, "the page's keyboard", func(string) bool { return r.focused("editor") })
	pageCtrlT(t, r)
	if m := view(); m != tuiwidget.ViewRaw {
		t.Fatalf("Ctrl+T on the page gives %v, want Raw", m)
	}
	onLoop(r, func() bool { r.h.terminals.toggleTerminal(); return true })
	r.s.WaitFor(t, "the terminal's keyboard", func(string) bool { return r.focused("terminalView") })
	r.keys(t, esc())
	r.keys(t, decltest.Type("page\r")...)
	r.s.WaitForText(t, "^[page")
}

// The search's preview has a Rendered view for a Markdown hit, as the editor does: a note's
// preview offers it; a Go file's does not. The preview binds App.searchPreviewRendered.
func TestTheSearchPreviewRendersAMarkdownHit(t *testing.T) {
	d := startDaemonWith(t, "", map[string][]string{
		"kb": {"a.md", "# Alpha\n\n- one\n", "a.go", goWithAHashLine},
	}, daemonOpts{goChunker: wholeGo{}})
	r := runTUI(t, NewSession(d.sock, nil), Options{ProgramOptions: []tuidecl.ProgramOption{tuidecl.WithStyle(GUIStyle(&NoteEmbeds{}))}})
	r.connected(t)
	r.s.WaitFor(t, "the notes listed", func(string) bool { return len(r.listed()) > 0 })
	onLoop(r, func() bool { r.h.openSearch(); return true })
	show := func(p, want string) {
		t.Helper()
		onLoop(r, func() bool { r.h.searchPick.previewItem(hit{path: p}); return true })
		r.s.WaitFor(t, p+"'s preview: rendered "+want, func(string) bool {
			return onLoop(r, func() bool {
				v, _ := r.h.p.Tree().Source("App.searchPreviewRendered")
				title, _ := r.h.p.Tree().Source("App.searchPreviewTitle")
				return v.Raw == want && title.Raw == p
			})
		})
	}
	show("a.md", "true")
	show("a.go", "false")
	show("a.md", "true")
}

// Ctrl+O returns to a document in the view it was left in, at its place: a link followed from a's
// Raw view, b shown Rendered meanwhile, comes back to a's Raw view at the link.
func TestCtrlOReturnsToTheViewItLeft(t *testing.T) {
	d := startDaemonWith(t, "", map[string][]string{"kb": {"a.md", "# A\n\nsee [[b]]\n", "b.md", "# B\n"}}, daemonOpts{})
	r := runTUI(t, NewSession(d.sock, nil), Options{ProgramOptions: []tuidecl.ProgramOption{tuidecl.WithStyle(GUIStyle(&NoteEmbeds{}))}})
	r.connected(t)
	r.s.WaitFor(t, "the notes listed", func(string) bool { return len(r.listed()) > 0 })
	view := func() tuiwidget.EditorView {
		return onLoop(r, func() tuiwidget.EditorView { return r.h.editor.(*guiwidget.Editor).View() })
	}
	setView := func(m tuiwidget.EditorView) {
		onLoop(r, func() bool { r.h.editor.(*guiwidget.Editor).SetView(m); return true })
	}
	onLoop(r, func() bool { r.h.openPath("a.md"); return true })
	r.waitFile(t, "a.md")
	setView(tuiwidget.ViewRaw)
	col := strings.Index("see [[b]]", "b]]")
	r.putCursor(2, col)
	onLoop(r, func() bool { r.h.followLink(); return true })
	r.waitFile(t, "b.md")
	setView(tuiwidget.ViewRendered)
	r.keys(t, ctrlKey('o'))
	r.waitAt(t, "a.md", 2, col)
	if m := view(); m != tuiwidget.ViewRaw {
		t.Fatalf("back to a: %v, want the Raw view it was left in", m)
	}
}

// TestCtrlWInTheGUIsVimInsert: in the native window too, Ctrl+W in Vim's Insert is the editor's
// (a word back) and opens no picker; in Normal mode it switches workspace (Johno, 2026-10-13).
func TestCtrlWInTheGUIsVimInsert(t *testing.T) {
	d := startDaemonWith(t, "", map[string][]string{"kb": {"a.md", "# A\n"}}, daemonOpts{})
	r := runTUI(t, NewSession(d.sock, nil), Options{ProgramOptions: []tuidecl.ProgramOption{tuidecl.WithStyle(GUIStyle(&NoteEmbeds{}))}})
	r.connected(t)
	r.s.WaitFor(t, "the notes listed", func(string) bool { return len(r.listed()) > 0 })
	onLoop(r, func() bool { r.h.openPath("a.md"); return true })
	r.waitFile(t, "a.md")
	r.keys(t, decltest.Alt('2'), key('A'))
	r.keys(t, decltest.Type(" foo bar")...)
	r.keys(t, decltest.Ctrl('w'), esc())
	r.waitValue(t, "# A foo \n")
	if strings.Contains(r.s.String(), "Manage…") {
		t.Fatal("Ctrl+W in the GUI's Insert opened the workspace picker")
	}
	r.keys(t, decltest.Ctrl('w'))
	r.s.WaitForText(t, "Manage…")
}
