package tui

import (
	"strings"
	"testing"
	"time"

	tuicore "github.com/yongjohnlee80/golib/tui"
)

// shrink makes the screen smaller than the minimum the way the App sees it, by raising the minimum
// above the screen: resizing the test screen itself races a frame laid out for the old size.
func (r *running) shrink(t *testing.T, small bool) {
	t.Helper()
	min := tuicore.Size{W: 80, H: 20}
	if small {
		min = tuicore.Size{W: 120, H: 40}
	}
	onLoop(r, func() bool { r.h.p.App().SetMinimumSize(min); return true })
}

// Below the Window's minimum, the screen asks to be enlarged. Its Quit is AutoDoc's own: over
// unsaved changes it asks first, and the question shows once the window is large again.
func TestATooSmallWindowsQuitAsksOverUnsavedChanges(t *testing.T) {
	d := startDaemon(t, map[string][]string{"kb": {"a.md", "aaa\n"}})
	r := attached(t, d)
	r.openByPicker(t, "a.md")
	r.waitFile(t, "a.md")
	r.typeInEditor(t, "X")
	r.s.WaitFor(t, "the edit unsaved", func(string) bool { return r.file().dirty })
	r.shrink(t, true)
	r.s.WaitForText(t, "Screen too small")
	r.keys(t, key('q'))
	select {
	case <-r.s.Quit():
		t.Fatal("quit over unsaved changes without asking")
	case <-time.After(300 * time.Millisecond):
	}
	r.shrink(t, false)
	r.s.WaitForText(t, "quit autodoc?")
}

// The Window declares its minimum, 80 by 20; with nothing unsaved, the too-small screen's Quit
// quits.
func TestATooSmallWindowsQuitQuits(t *testing.T) {
	r := attached(t, startDaemon(t, map[string][]string{"kb": {"a.md", "aaa\n"}}))
	if m := onLoop(r, func() tuicore.Size { return r.h.p.App().MinimumSize() }); m != (tuicore.Size{W: 80, H: 20}) {
		t.Fatalf("the Window's minimum is %v, want 80x20", m)
	}
	r.shrink(t, true)
	r.s.WaitForText(t, "Screen too small")
	r.keys(t, key('q'))
	select {
	case <-r.s.Quit():
	case <-time.After(3 * time.Second):
		t.Fatal("did not quit")
	}
}

// A right press on the page opens the editor's menu: Undo, Redo, Copy, Cut, Paste.
func TestThePagesRightClickMenu(t *testing.T) {
	r := attached(t, startDaemon(t, map[string][]string{"kb": {"a.md", "aaa\n"}}))
	r.openByPicker(t, "a.md")
	r.waitFile(t, "a.md")
	r.keys(t, tuicore.MouseEvent{Kind: tuicore.MousePress, Button: tuicore.MouseRight, X: 50, Y: 12})
	r.s.WaitFor(t, "the menu", func(sc string) bool {
		return strings.Contains(sc, "Undo") && strings.Contains(sc, "Redo") && strings.Contains(sc, "Paste")
	})
}
