package tui

import (
	"strings"
	"testing"

	"github.com/yongjohnlee80/golib/tui/decl/decltest"
	"github.com/yongjohnlee80/golib/tui/widget"
)

// Johno, 2026-10-13: in the Vim editor mode's Insert, Vim wins. Ctrl+W there deletes the word
// before the cursor and Ctrl+O runs one Normal-mode command; in Normal mode, and in the Text
// editor mode, Ctrl+W switches workspace.

// waitValue waits for the page's text to be want, back in Normal mode: keys and a read on the loop
// are not ordered, so the keys must be seen done.
func (r *running) waitValue(t *testing.T, want string) {
	t.Helper()
	r.s.WaitFor(t, "the keys done", func(string) bool {
		return onLoop(r, func() bool { return r.h.core.Value() == want && r.h.core.Mode() == widget.ModeNormal })
	})
}

// TestCtrlWInVimInsertDeletesAWord: Ctrl+W in Insert reaches the editor, a word back, and the
// workspace picker doesn't open.
func TestCtrlWInVimInsertDeletesAWord(t *testing.T) {
	r := attached(t, startDaemon(t, landingKB))
	r.openByPicker(t, "a.md")
	r.waitFile(t, "a.md")
	r.keys(t, decltest.Alt('2'), key('A'))
	r.keys(t, decltest.Type(" foo bar")...)
	r.keys(t, decltest.Ctrl('w'), esc())
	r.waitValue(t, "# A foo \n")
	if strings.Contains(r.s.String(), "Manage…") {
		t.Fatal("Ctrl+W in Insert opened the workspace picker")
	}
}

// TestCtrlWSwitchesWorkspaceOutsideInsert: in Normal mode, and in the Text editor mode, Ctrl+W
// is the workspace picker's.
func TestCtrlWSwitchesWorkspaceOutsideInsert(t *testing.T) {
	r := attached(t, startDaemon(t, landingKB))
	r.openByPicker(t, "a.md")
	r.waitFile(t, "a.md")
	r.keys(t, decltest.Ctrl('w'))
	r.s.WaitForText(t, "Manage…")
	r.keys(t, esc())
	r.s.WaitFor(t, "the picker closed", func(sc string) bool { return !strings.Contains(sc, "Manage…") })

	onLoop(r, func() bool { r.h.setKeymap("text"); return true })
	r.s.WaitFor(t, "the Text mode", func(string) bool {
		return onLoop(r, func() bool { return r.h.core.Mode() == widget.ModeInsert })
	})
	r.keys(t, decltest.Ctrl('w'))
	r.s.WaitForText(t, "Manage…")
	if v := onLoop(r, func() string { return r.h.core.Value() }); v != "# A\n" {
		t.Fatalf("Ctrl+W in the Text mode changed the page: %q", v)
	}
}

// TestCtrlOInVimInsertRunsOneCommand: Ctrl+O in Insert runs one Normal-mode command (0, to the
// line's start), the status line saying (INSERT) meanwhile, and Insert resumes there; it is no
// jump to another document.
func TestCtrlOInVimInsertRunsOneCommand(t *testing.T) {
	r := attached(t, startDaemon(t, landingKB))
	r.openByPicker(t, "a.md")
	r.waitFile(t, "a.md")
	r.openByPicker(t, "b.md")
	r.waitFile(t, "b.md")
	r.keys(t, decltest.Alt('2'), key('A'))
	r.keys(t, decltest.Ctrl('o'))
	r.s.WaitForText(t, "(INSERT)") // the status line, while the command runs
	r.keys(t, key('0'))
	r.keys(t, decltest.Type("X")...)
	r.keys(t, esc())
	r.waitValue(t, "X# B\n\none\n\ntwo\n")
	if f := r.file(); f.path != "b.md" {
		t.Fatalf("Ctrl+O in Insert left %s, want b.md: it jumped", f.path)
	}
}
