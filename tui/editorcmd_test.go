package tui

import (
	"strings"
	"testing"
	"time"

	tuicore "github.com/yongjohnlee80/golib/tui"
	"github.com/yongjohnlee80/golib/tui/decl/decltest"
)

// command runs one line through the editor's ':' prompt, from Normal mode.
func (r *running) command(t *testing.T, line string) {
	t.Helper()
	r.keys(t, key(':'))
	r.s.WaitForText(t, "VIM Commands")
	r.keys(t, decltest.Type(line)...)
	r.keys(t, tuicore.KeyEvent{Kind: tuicore.KeyPress, Code: tuicore.KeyEnter})
}

// :w saves as File › Save does; :wq saves, then quits.
func TestTheCommandLineSavesAndQuits(t *testing.T) {
	d := startDaemon(t, map[string][]string{"kb": {"a.md", "aaa\n"}})
	r := attached(t, d)
	r.openByPicker(t, "a.md")
	r.waitFile(t, "a.md")
	r.typeInEditor(t, "X")
	r.command(t, "w")
	r.s.WaitForText(t, "saved a.md")
	if got := d.read(t, "kb", "a.md"); got != "Xaaa\n" {
		t.Fatalf("on disk after :w: %q", got)
	}
	r.typeInEditor(t, "Y")
	r.command(t, "wq")
	select {
	case <-r.s.Quit():
	case <-time.After(3 * time.Second):
		t.Fatal(":wq did not quit")
	}
	if got := d.read(t, "kb", "a.md"); got != "YXaaa\n" {
		t.Fatalf("on disk after :wq: %q", got)
	}
}

// :q over unsaved changes asks, as every quit does; :q! quits without asking.
func TestTheCommandLineQuitAsksAndQBangDoesNot(t *testing.T) {
	d := startDaemon(t, map[string][]string{"kb": {"a.md", "aaa\n"}})
	r := attached(t, d)
	r.openByPicker(t, "a.md")
	r.waitFile(t, "a.md")
	r.typeInEditor(t, "X")
	r.command(t, "q")
	r.s.WaitForText(t, "Save them before you quit?")
	r.keys(t, key('t'))
	r.s.WaitFor(t, "stayed", func(sc string) bool { return !strings.Contains(sc, "before you quit?") })
	r.command(t, "q!")
	select {
	case <-r.s.Quit():
	case <-time.After(3 * time.Second):
		t.Fatal(":q! did not quit")
	}
	if got := d.read(t, "kb", "a.md"); got != "aaa\n" {
		t.Fatalf(":q! wrote the file: %q", got)
	}
}

// A window's close button (the GUI's) asks over unsaved changes too.
func TestTheWindowsCloseRequestAsksOverUnsavedChanges(t *testing.T) {
	d := startDaemon(t, map[string][]string{"kb": {"a.md", "aaa\n"}})
	r := attached(t, d)
	r.openByPicker(t, "a.md")
	r.waitFile(t, "a.md")
	r.typeInEditor(t, "X")
	r.keys(t, tuicore.CloseRequestEvent{})
	r.s.WaitForText(t, "Save them before you quit?")
	select {
	case <-r.s.Quit():
		t.Fatal("the close request quit over unsaved changes")
	default:
	}
}

// The prompt's find is the page's: "finding …" shows and n goes on, as after /.
func TestTheCommandLinesFindIsThePagesFind(t *testing.T) {
	d := startDaemon(t, map[string][]string{"kb": {"a.md", "one\ntwo\none more\n"}})
	r := attached(t, d)
	r.openByPicker(t, "a.md")
	r.waitFile(t, "a.md")
	r.command(t, "find one")
	r.s.WaitForText(t, `finding "one" 1/2`) // x/y: from the cursor's own line, as / does
	r.keys(t, key('n'))
	r.s.WaitForText(t, `finding "one" 2/2`)
	r.s.WaitFor(t, "the cursor on line 3", func(string) bool {
		return onLoop(r, func() int { ln, _ := r.h.core.Line(); return ln }) == 2
	})
	r.command(t, "nohl")
	r.s.WaitFor(t, "the find cleared", func(sc string) bool { return !strings.Contains(sc, "finding") })
	// Esc in the page ends it too, as its X does
	r.command(t, "find one")
	r.s.WaitForText(t, `finding "one"`)
	r.keys(t, esc())
	r.s.WaitFor(t, "Esc cleared the find", func(sc string) bool { return !strings.Contains(sc, "finding") })
}

// TestCtrlSemicolonOpensTheCommandLineInTheTextMode: the Text mode types ':', so Ctrl+; (':' with
// Ctrl) is its way to the command line and to every command, its Lua ones among them.
func TestCtrlSemicolonOpensTheCommandLineInTheTextMode(t *testing.T) {
	d := startDaemon(t, map[string][]string{"kb": {"a.md", "aaa\n"}})
	r := attached(t, d)
	r.openByPicker(t, "a.md")
	r.waitFile(t, "a.md")
	onLoop(r, func() bool { r.h.setKeymap("text"); return true })
	r.keys(t, decltest.Type("X")...)
	r.s.WaitFor(t, "X typed", func(string) bool { return strings.Contains(r.editorText(), "X") })
	r.keys(t, decltest.Ctrl(';'))
	r.s.WaitForText(t, "VIM Commands")
	r.keys(t, decltest.Type("w")...)
	r.keys(t, tuicore.KeyEvent{Kind: tuicore.KeyPress, Code: tuicore.KeyEnter})
	r.s.WaitForText(t, "saved a.md")
	if got := d.read(t, "kb", "a.md"); got != "Xaaa\n" {
		t.Fatalf("on disk after Ctrl+; w: %q", got)
	}
}

// TestSearchMenuOpensTheCommandLine: the menu reaches it in any terminal, Ctrl+; or not.
func TestSearchMenuOpensTheCommandLine(t *testing.T) {
	d := startDaemon(t, map[string][]string{"kb": {"a.md", "aaa\n"}})
	r := attached(t, d)
	r.openByPicker(t, "a.md")
	r.waitFile(t, "a.md")
	onLoop(r, func() bool { r.h.openCommandLine(); return true })
	r.s.WaitForText(t, "VIM Commands")
}
