package tui

import (
	"context"
	"strings"
	"testing"

	"github.com/yongjohnlee80/golib/parse/qml"
	tuicore "github.com/yongjohnlee80/golib/tui"
)

// gripAt is where a panel's corner grip is drawn, ok false when nowhere.
func gripAt(r *running, glyph string) (x, y int, ok bool) {
	for y, row := range r.s.Backend.Snapshot() {
		for x, c := range row {
			if c.Content == glyph {
				return x, y, true
			}
		}
	}
	return 0, 0, false
}

func dragGrip(t *testing.T, r *running, glyph string, dx, dy int) {
	t.Helper()
	var x, y int
	r.s.WaitFor(t, "the grip "+glyph, func(string) bool {
		var ok bool
		x, y, ok = gripAt(r, glyph)
		return ok
	})
	r.keys(t, tuicore.MouseEvent{Kind: tuicore.MousePress, Button: tuicore.MouseLeft, X: x, Y: y},
		tuicore.MouseEvent{Kind: tuicore.MouseMotion, X: x + dx, Y: y + dy},
		tuicore.MouseEvent{Kind: tuicore.MouseRelease, Button: tuicore.MouseLeft, X: x + dx, Y: y + dy})
}

// TestADraggedPanelKeepsItsSize: the terminal at the bottom, dragged by its top-right grip from 30%
// to 50% of the rows, and the explorer at the left, dragged by its bottom-right grip from 30% to
// 40% of the columns, each keep the size for their edge in the store, and a TUI attached after
// opens them at it (ADR 1791213315).
func TestADraggedPanelKeepsItsSize(t *testing.T) {
	d, r := runShellTUI(t, nil)
	ctx := context.Background()
	stored := func(name, want string) {
		t.Helper()
		r.s.WaitFor(t, name+" = "+want, func(string) bool {
			m, err := d.db.Preferences(ctx)
			return err == nil && m[name] == want
		})
	}

	onLoop(r, func() bool { r.h.toggleTerminal(); return true })
	r.s.WaitFor(t, "the terminal 30% high", func(sc string) bool { row, _ := frameAt(sc); return row == 21 })
	dragGrip(t, r, "□", 0, -6)
	r.s.WaitFor(t, "the terminal 50% high", func(sc string) bool { row, _ := frameAt(sc); return row == 15 })
	stored("tui.terminal.bottom.size", "50")
	if got := onLoop(r, func() int { return r.h.prefs.termSize["bottom"] }); got != 50 {
		t.Errorf("the terminal's size held is %d, want 50", got)
	}
	onLoop(r, func() bool { r.h.toggleTerminal(); return true })

	onLoop(r, func() bool { r.h.togglePanel("explorer"); return true })
	dragGrip(t, r, "□", 10, 0)
	r.s.WaitFor(t, "the explorer 40% wide", func(string) bool { x, _, ok := gripAt(r, "□"); return ok && x == 39 })
	stored("tui.explorer.left.size", "40")

	again := attached(t, d)
	onLoop(again, func() bool { again.h.togglePanel("explorer"); return true })
	again.s.WaitFor(t, "the explorer 40% wide in a new TUI", func(string) bool {
		x, _, ok := gripAt(again, "□")
		return ok && x == 39
	})
	onLoop(again, func() bool { again.h.togglePanel("explorer"); again.h.toggleTerminal(); return true })
	again.s.WaitFor(t, "the terminal 50% high in a new TUI", func(sc string) bool { row, _ := frameAt(sc); return row == 15 })
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
