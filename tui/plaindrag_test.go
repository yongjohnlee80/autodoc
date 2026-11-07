package tui

import (
	"context"
	"strings"
	"testing"

	tuicore "github.com/yongjohnlee80/golib/tui"
	"github.com/yongjohnlee80/golib/tui/decl/decltest"
	"github.com/yongjohnlee80/golib/tui/panelgeometry"
)

// plainDrag drags with no modifier from (x, y) by (dx, dy) with the left button.
func plainDrag(t *testing.T, r *running, x, y, dx, dy int) {
	t.Helper()
	r.keys(t, tuicore.MouseEvent{Kind: tuicore.MousePress, Button: tuicore.MouseLeft, X: x, Y: y},
		tuicore.MouseEvent{Kind: tuicore.MouseMotion, Button: tuicore.MouseLeft, X: x + dx, Y: y + dy},
		tuicore.MouseEvent{Kind: tuicore.MouseRelease, Button: tuicore.MouseLeft, X: x + dx, Y: y + dy})
}

// plainRule is the column in the middle of row y's longest run of ─: a frame's own cells, no
// control on them.
func plainRule(screen string, y int) int {
	rows := strings.Split(screen, "\n")
	if y < 0 || y >= len(rows) {
		return -1
	}
	best, bestLen, at, n := -1, 0, -1, 0
	for x, c := range []rune(rows[y]) {
		if c != '─' {
			n = 0
			continue
		}
		if n == 0 {
			at = x
		}
		if n++; n > bestLen {
			best, bestLen = at, n
		}
	}
	if best < 0 {
		return -1
	}
	return best + bestLen/2
}

// rightBorder is the column of the frame's right border on row y, right of x: its ┐.
func rightBorder(screen string, y, x int) int {
	rows := strings.Split(screen, "\n")
	if y < 0 || y >= len(rows) {
		return -1
	}
	for i, c := range []rune(rows[y]) {
		if i > x && c == '┐' {
			return i
		}
	}
	return -1
}

// shellTUIOn is runShellTUI on a 100×30 terminal reporting caps: AutoDoc's terminal reports
// FrameDrag (app's terminalOptions, term.WithFrameDrag).
func shellTUIOn(t *testing.T, caps tuicore.Capabilities) (*daemon, *running) {
	t.Helper()
	t.Setenv("SHELL", "/bin/sh")
	t.Setenv("HOME", t.TempDir())
	t.Setenv("PS1", "$ ")
	d := startDaemonWith(t, "", map[string][]string{"kb": {"a.md", "a\n"}}, daemonOpts{})
	h := newHost(NewSession(d.sock, nil), Options{})
	s := decltest.RunOn(t, tuicore.NewTestBackend(100, 30, tuicore.WithTestCapabilities(caps)), h.attach, h.options(Options{})...)
	t.Cleanup(h.cancel)
	r := &running{h: h, s: s}
	r.connected(t)
	r.waitWithin(t, "the notes listed", daemonWait, func() bool { return len(r.listed()) > 0 })
	return d, r
}

// TestAPlainDragMovesAndResizesAPanel: on a terminal whose panels take plain frame drags, as
// AutoDoc's does, the terminal panel dragged by its title row six rows up floats there, and the
// explorer dragged by its right border ten columns wider floats at 45%, each kept as the Alt drags
// keep them (TestADraggedPanelFloatsWhereItIsLeft). On one that doesn't, the same drag leaves the
// panel where it was: the Alt chord alone moves it there.
func TestAPlainDragMovesAndResizesAPanel(t *testing.T) {
	t.Run("plain frame drags", func(t *testing.T) {
		d, r := shellTUIOn(t, tuicore.Capabilities{FrameDrag: true})
		onLoop(r, func() bool { r.h.terminals.toggleTerminal(); return true })
		var row, x int
		r.s.WaitFor(t, "the terminal 30% high, a rule on its title row", func(sc string) bool {
			row, _ = frameAt(sc)
			x = plainRule(sc, row)
			return row == 21 && x >= 0
		})
		plainDrag(t, r, x, row, 0, -6)
		r.s.WaitFor(t, "the terminal six rows up", func(sc string) bool { row, _ := frameAt(sc); return row == 15 })
		storedPref(t, d, r, "tui.terminal.float", "0,50,100,30")
		onLoop(r, func() bool { r.h.terminals.toggleTerminal(); return true })

		onLoop(r, func() bool { r.h.togglePanel("explorer"); return true })
		var erow, right int
		r.s.WaitFor(t, "the explorer", func(sc string) bool {
			var ecol int
			erow, ecol = panelAt(sc, "explorer")
			right = rightBorder(sc, erow, ecol)
			return erow >= 0 && right > 0
		})
		plainDrag(t, r, right, erow+5, 10, 0)
		r.waitWithin(t, "the explorer floating 45% wide", daemonWait, func() bool {
			m, _ := d.db.Preferences(context.Background())
			f, ok := panelgeometry.ParseFloat(m["tui.explorer.float"])
			return ok && f[0] == 0 && f[2] == 45
		})
	})
	t.Run("the Alt chord alone", func(t *testing.T) {
		d, r := shellTUIOn(t, tuicore.Capabilities{})
		onLoop(r, func() bool { r.h.terminals.toggleTerminal(); return true })
		var row, x int
		r.s.WaitFor(t, "the terminal 30% high, a rule on its title row", func(sc string) bool {
			row, _ = frameAt(sc)
			x = plainRule(sc, row)
			return row == 21 && x >= 0
		})
		plainDrag(t, r, x, row, 0, -6)
		altDrag(t, r, tuicore.MouseLeft, 50, 25, 0, -3) // after it, in order: the Alt chord moves it
		r.s.WaitFor(t, "the terminal three rows up, by the Alt drag alone", func(sc string) bool { row, _ := frameAt(sc); return row == 18 })
		storedPref(t, d, r, "tui.terminal.float", "0,60,100,30")
	})
}
