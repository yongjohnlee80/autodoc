package tui

import (
	"strings"
	"testing"

	tuicore "github.com/yongjohnlee80/golib/tui"
	"github.com/yongjohnlee80/golib/tui/widget/editor"
)

func press(code rune, text string) tuicore.KeyEvent {
	return tuicore.KeyEvent{Kind: tuicore.KeyPress, Code: code, Text: text}
}

// TestATerminalRunOpensTheCellMindmap: on a terminal, in either build (go test -tags gui runs it as a
// GUI build's --ui), main.qml has the Relations › Mindmap item, and App.toggleMindmap opens the
// cell Mindmap's panel, its view the cell one.
func TestATerminalRunOpensTheCellMindmap(t *testing.T) {
	d := startManagedIncluding(t, map[string]string{"kb": fileDir(t, "a.md", "# A\n")}, tableInclude)
	r := runTUI(t, NewSession(d.sock, nil), Options{})
	r.waitAttached(t, "kb")
	if !strings.Contains(string(withRelationViews(layout, false)), `onTriggered: App.toggleMindmap()`) {
		t.Fatal("a terminal's main.qml has no Relations › Mindmap item")
	}
	m := onLoop(r, func() *cellMindmap {
		for _, f := range r.h.reg.Features {
			if m, ok := f.(*cellMindmap); ok {
				return m
			}
		}
		return nil
	})
	if m == nil {
		t.Fatal("a terminal host has no cell Mindmap registered")
	}
	onLoop(r, func() bool { r.h.openPath("a.md"); return true })
	r.waitFile(t, "a.md")
	onLoop(r, func() bool { m.toggle(); return true })
	r.s.WaitFor(t, "the cell Mindmap open", func(string) bool {
		return onLoop(r, func() bool { s := m.State(); return s.Open && s.Viewed })
	})
}

// TestSPCMOpensTheMindmap: SPC M (the leader's M, on SPC's card under THIS FILE) opens the Mindmap
// of the document shown, and again closes it. Maximize is SPC z, never SPC M.
func TestSPCMOpensTheMindmap(t *testing.T) {
	d := startManagedIncluding(t, map[string]string{"kb": fileDir(t, "a.md", "# A\n")}, tableInclude)
	r := runTUI(t, NewSession(d.sock, nil), Options{})
	r.waitAttached(t, "kb")
	m := onLoop(r, func() *cellMindmap {
		for _, f := range r.h.reg.Features {
			if m, ok := f.(*cellMindmap); ok {
				return m
			}
		}
		return nil
	})
	if m == nil {
		t.Fatal("a terminal host has no cell Mindmap registered")
	}
	onLoop(r, func() bool { r.h.openPath("a.md"); return true })
	r.waitFile(t, "a.md")
	r.leader(t, 'M')
	r.s.WaitFor(t, "SPC M opened the Mindmap", func(string) bool {
		return onLoop(r, func() bool { s := m.State(); return s.Open && s.Viewed })
	})
	if onLoop(r, func() bool { return r.h.panelMax["terminal"] || r.h.panelMax["agent"] }) {
		t.Error("SPC M maximized a panel")
	}
	r.leader(t, 'M')
	r.s.WaitFor(t, "SPC M closed it", func(string) bool { return !onLoop(r, func() bool { return m.State().Open }) })
}

// TestTheCellMindmapTakesTheKeyboard: Relations › Mindmap opened over a note's page (Rendered) or
// over its text (Raw), from the menu (Alt+L, m, as a reader opens it) or by its command, takes the
// keyboard, as the window's Mindmap does: its next key (d: two links out) is the map's, and the note
// is as it was. Closed, it gives the keyboard back to the editor.
func TestTheCellMindmapTakesTheKeyboard(t *testing.T) {
	for _, c := range []struct {
		name      string
		raw, menu bool
	}{{"the menu, from the page", false, true}, {"the menu, from the text", true, true},
		{"its command, from the page", false, false}, {"its command, from the text", true, false}} {
		raw := c.raw
		t.Run(c.name, func(t *testing.T) {
			r := openRenderedNote(t, "# Pantry\n\nshelf\n", "Pantry")
			if raw {
				r.keys(t, tuicore.KeyEvent{Kind: tuicore.KeyPress, Code: 't', Mods: tuicore.ModCtrl})
				r.s.WaitFor(t, "the note's text", func(string) bool { return shownView(r) == editor.ViewRaw })
			}
			m := onLoop(r, func() *cellMindmap {
				for _, f := range r.h.reg.Features {
					if m, ok := f.(*cellMindmap); ok {
						return m
					}
				}
				return nil
			})
			before := r.editorText()
			if c.menu {
				r.keys(t, tuicore.KeyEvent{Kind: tuicore.KeyPress, Code: 'l', Mods: tuicore.ModAlt})
				r.s.WaitFor(t, "the Relations menu", func(string) bool { return r.focused("menuBar") || strings.Contains(r.s.String(), "Mindmap") })
				r.keys(t, key('m'))
			} else {
				onLoop(r, func() bool { m.toggle(); return true })
			}
			r.s.WaitFor(t, "the Mindmap built", func(string) bool {
				return onLoop(r, func() bool { s := m.State(); return s.Open && s.Loaded })
			})
			r.s.WaitFor(t, "the Mindmap has the keyboard", func(string) bool {
				return onLoop(r, func() bool { return r.h.paneHasKeyboard(mindmapViewID) })
			})
			r.keys(t, key('d'))
			r.s.WaitFor(t, "d reached the Mindmap (two links out)", func(string) bool {
				return onLoop(r, func() int { return m.State().Depth }) == 2
			})
			if got := r.editorText(); got != before {
				t.Fatalf("a key for the Mindmap changed the note:\n%q\nwant\n%q", got, before)
			}
			onLoop(r, func() bool { m.toggle(); return true })
			r.s.WaitFor(t, "the editor has the keyboard again", func(string) bool {
				return onLoop(r, func() bool { return !m.State().Open && r.h.paneHasKeyboard("editor") })
			})
		})
	}
}
