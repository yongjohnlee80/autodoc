package tui

import (
	"context"
	"strings"
	"testing"

	"github.com/yongjohnlee80/golib/parse/qml"
	tuicore "github.com/yongjohnlee80/golib/tui"
)

// panelAt is where a panel's frame starts, its title's "┌ title": row and column, -1 when not shown.
func panelAt(screen, title string) (row, column int) {
	for y, l := range strings.Split(screen, "\n") {
		if c := col(l, "┌ "+title); c >= 0 {
			return y, c
		}
	}
	return -1, -1
}

// altDrag drags with Alt held from (x, y) by (dx, dy): the left button moves a panel, the right
// one resizes it.
func altDrag(t *testing.T, r *running, button tuicore.MouseButton, x, y, dx, dy int) {
	t.Helper()
	r.keys(t, tuicore.MouseEvent{Kind: tuicore.MousePress, Button: button, Mods: tuicore.ModAlt, X: x, Y: y},
		tuicore.MouseEvent{Kind: tuicore.MouseMotion, Button: button, X: x + dx, Y: y + dy},
		tuicore.MouseEvent{Kind: tuicore.MouseRelease, Button: button, X: x + dx, Y: y + dy})
}

// storedPref waits for the store's preference name to be want ("" for unset).
func storedPref(t *testing.T, d *daemon, r *running, name, want string) {
	t.Helper()
	r.s.WaitFor(t, name+" = "+want, func(string) bool {
		m, err := d.db.Preferences(context.Background())
		return err == nil && m[name] == want
	})
}

// TestADraggedPanelFloatsWhereItIsLeft: the terminal at the bottom, Alt-dragged up six rows, floats
// there, and the explorer at the left, Alt-right-dragged ten columns wider, floats at its new size;
// the store keeps each as percentages of the Window, and a TUI attached after opens them there
// (ADR 1791500773). No corner grip is drawn.
func TestADraggedPanelFloatsWhereItIsLeft(t *testing.T) {
	d, r := runShellTUI(t, nil)
	onLoop(r, func() bool { r.h.toggleTerminal(); return true })
	r.s.WaitFor(t, "the terminal 30% high", func(sc string) bool { row, _ := frameAt(sc); return row == 21 })
	if strings.Contains(r.s.String(), "□") {
		t.Error("a corner grip is drawn")
	}
	altDrag(t, r, tuicore.MouseLeft, 50, 25, 0, -6)
	r.s.WaitFor(t, "the terminal six rows up", func(sc string) bool { row, _ := frameAt(sc); return row == 15 })
	storedPref(t, d, r, "tui.terminal.float", "0,50,100,30")
	onLoop(r, func() bool { r.h.toggleTerminal(); return true })

	onLoop(r, func() bool { r.h.togglePanel("explorer"); return true })
	r.s.WaitFor(t, "the explorer", func(sc string) bool { row, _ := panelAt(sc, "explorer"); return row >= 0 })
	altDrag(t, r, tuicore.MouseRight, 20, 10, 10, 0)
	r.s.WaitFor(t, "the explorer floating 40% wide", func(string) bool {
		m, _ := d.db.Preferences(context.Background())
		f, ok := parseFloat(m["tui.explorer.float"])
		return ok && f[0] == 0 && f[2] == 40
	})

	again := attached(t, d)
	onLoop(again, func() bool { again.h.toggleTerminal(); return true })
	again.s.WaitFor(t, "the terminal floating six rows up in a new TUI", func(sc string) bool { row, _ := frameAt(sc); return row == 15 })
}

// TestArrangingAPanelIsOneChange: SPC L's steps move the panel at once and store nothing; Escape
// puts it back where it was, still open; Enter keeps the steps, written once.
func TestArrangingAPanelIsOneChange(t *testing.T) {
	d, r := runShellTUI(t, nil)
	onLoop(r, func() bool { r.h.togglePanel("explorer"); return true })
	var row0, col0 int
	r.s.WaitFor(t, "the explorer", func(sc string) bool { row0, col0 = panelAt(sc, "explorer"); return row0 >= 0 })

	onLoop(r, func() bool { r.h.arrangePanel(); return true }) // SPC L, with the explorer's tree focused
	r.s.WaitForText(t, "arrange the explorer")
	r.keys(t, key('l'), key('l'))
	r.s.WaitFor(t, "four columns right", func(sc string) bool { _, c := panelAt(sc, "explorer"); return c == col0+4 })
	if m, _ := d.db.Preferences(context.Background()); m["tui.explorer.float"] != "" {
		t.Fatalf("a step was stored: %v", m["tui.explorer.float"])
	}
	r.keys(t, esc())
	r.s.WaitFor(t, "back at its edge, open", func(sc string) bool { row, c := panelAt(sc, "explorer"); return row == row0 && c == col0 })
	if m, _ := d.db.Preferences(context.Background()); m["tui.explorer.float"] != "" {
		t.Errorf("Escape stored %v", m["tui.explorer.float"])
	}

	onLoop(r, func() bool { r.h.arrangePanel(); return true })
	r.s.WaitForText(t, "arrange the explorer")
	r.keys(t, key('j'), key('L'), enter())
	r.s.WaitFor(t, "a row down", func(sc string) bool { row, _ := panelAt(sc, "explorer"); return row == row0+1 })
	r.s.WaitFor(t, "kept once", func(string) bool {
		m, _ := d.db.Preferences(context.Background())
		return strings.HasPrefix(m["tui.explorer.float"], "0,")
	})
	if got := onLoop(r, func() bool { return r.h.arrange == nil }); !got {
		t.Error("the arrangement did not end")
	}
}

// TestResetAndANewEdgeDockThePanels: Go › Reset panel layout forgets every floating panel, and a
// new edge in Preferences forgets that panel's.
func TestResetAndANewEdgeDockThePanels(t *testing.T) {
	d, r := runShellTUI(t, map[string]string{"tui.explorer.float": "50,25,30,50", "tui.links.float": "10,10,30,50"})
	onLoop(r, func() bool { r.h.togglePanel("explorer"); return true })
	r.s.WaitFor(t, "the explorer floating mid-screen", func(sc string) bool { _, c := panelAt(sc, "explorer"); return c >= 45 })
	onLoop(r, func() bool { r.h.setExplorerEdge(1); return true }) // right
	storedPref(t, d, r, "tui.explorer.float", "")
	if got := onLoop(r, func() bool { _, ok := r.h.prefs.panelFloat["links"]; return ok }); !got {
		t.Fatal("a new edge for the explorer docked the links too")
	}
	onLoop(r, func() bool { r.h.resetPanelLayout(); return true })
	storedPref(t, d, r, "tui.links.float", "")
}

// App.panelResized takes a panel's name and two whole numbers, and refuses anything else, naming it.
func TestPanelResizedTakesANameAndTwoWholeNumbers(t *testing.T) {
	var got []any
	h := stringAndTwoNumbers("App.panelResized", "a panel and its size and length", func(p string, s, l int) { got = []any{p, s, l} })
	str := func(v string) qml.SpecValue { return qml.SpecValue{Kind: qml.SpecValueString, Raw: v} }
	num := func(v string) qml.SpecValue { return qml.SpecValue{Kind: qml.SpecValueNumber, Raw: v} }
	if err := h([]qml.SpecValue{str("agent"), num("60"), num("70")}); err != nil || len(got) != 3 || got[0] != "agent" || got[1] != 60 || got[2] != 70 {
		t.Fatalf("(agent, 60, 70) gave %v, %v", got, err)
	}
	for _, c := range []struct {
		args []qml.SpecValue
		says string
	}{
		{[]qml.SpecValue{str("agent"), num("60")}, "takes a panel and its size and length"},
		{[]qml.SpecValue{num("1"), num("60"), num("70")}, "takes a panel and its size and length"},
		{[]qml.SpecValue{str("agent"), num("33.5"), num("70")}, "not 33.5 and 70"},
		{[]qml.SpecValue{str("agent"), num("60"), num("7e1")}, "not 60 and 7e1"},
	} {
		got = nil
		if err := h(c.args); err == nil || !strings.Contains(err.Error(), c.says) || got != nil {
			t.Errorf("%v: err %v (want %q), called with %v", c.args, err, c.says, got)
		}
	}
}
