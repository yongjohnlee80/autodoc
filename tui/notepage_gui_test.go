//go:build gui

package tui

import "testing"

// notePageOf is the host's registered note page.
func notePageOf(t *testing.T, r *running) *cellNotePage {
	t.Helper()
	return onLoop(r, func() *cellNotePage {
		for _, f := range r.h.reg.features {
			if p, ok := f.(*cellNotePage); ok {
				return p
			}
		}
		return nil
	})
}

// TestAWindowNeverMakesTheTerminalsNotePage (docs/decisions/editor.md#a-terminal-notes-rendered-view):
// in a window (the GUI build on its native backend) a note keeps the window's own Rendered view: the
// terminal's note page is registered, as in every host, and never made or installed, and the
// window's editor is never given the terminal's Markdown cell renderer.
func TestAWindowNeverMakesTheTerminalsNotePage(t *testing.T) {
	r := runGUINote(t, notePageNote, func(*Host) {})
	// the note was shown to the features: the page's Shown ran, and must have done nothing
	r.s.WaitFor(t, "the note shown to the features", func(string) bool { return onLoop(r, func() bool { return r.h.reg.pageShown }) })
	p := notePageOf(t, r)
	if p == nil {
		t.Fatal("the host has no note page registered")
	}
	if made := onLoop(r, func() bool { return p.view != nil || p.shown.path != "" }); made {
		t.Fatal("a window made the terminal's note page")
	}
	if given := onLoop(r, func() bool { return p.md != nil }); given {
		t.Fatal("a window made the terminal's Markdown cell renderer")
	}
}
