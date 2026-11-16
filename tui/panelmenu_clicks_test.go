package tui

import (
	"strings"
	"testing"

	tuicore "github.com/yongjohnlee80/golib/tui"
)

// A panel's ≡ menu clicked, on every OS: the terminal's cells and the agents' use them.

// clickMenuRow opens panel's ≡ menu with the pointer and clicks its row label. The ≡ is the one
// drawn inside the panel's window: the page's row of tabs has its own.
func clickMenuRow(t *testing.T, r *running, panel, label string) {
	t.Helper()
	click := func(x, y int) {
		r.keys(t, tuicore.MouseEvent{Kind: tuicore.MousePress, Button: tuicore.MouseLeft, X: x, Y: y},
			tuicore.MouseEvent{Kind: tuicore.MouseRelease, Button: tuicore.MouseLeft, X: x, Y: y})
	}
	var mx, my int
	r.s.WaitFor(t, "the "+panel+" panel's ≡", func(screen string) bool {
		at, on := screenRectOf(r, panel)
		if !on {
			return false
		}
		for y, line := range strings.Split(screen, "\n") {
			for x, c := range []rune(line) {
				if c == '≡' && x >= at.X && x < at.X+at.W && y >= at.Y && y < at.Y+at.H {
					mx, my = x, y
					return true
				}
			}
		}
		return false
	})
	click(mx, my)
	r.s.WaitForText(t, label)
	x, y := r.textAt(label)
	click(x+1, y)
}

// clickMaximizeAndRestore clicks Maximize, then Restore, in the open panel's ≡ menu: the panel
// maximizes, then restores, and nothing is kept as refused.
func clickMaximizeAndRestore(t *testing.T, r *running, panel string) {
	t.Helper()
	maxed := func(want bool) func(string) bool {
		return func(string) bool { return onLoop(r, func() bool { return r.h.panelMax[panel] }) == want }
	}
	// the panel as drawn: the next click must find its strip where the maximized frame shows it,
	// not where the frame before did (a bottom panel's strip moves up; a top panel's grows down)
	r.s.WaitForText(t, "┌ "+panel)
	docked := panelHeight(r.s.String(), panel)
	clickMenuRow(t, r, panel, "Maximize")
	r.s.WaitFor(t, "the "+panel+" maximized by a click", maxed(true))
	r.s.WaitFor(t, "the maximized "+panel+" drawn", func(sc string) bool { return panelHeight(sc, panel) > docked })
	clickMenuRow(t, r, panel, "Restore")
	r.s.WaitFor(t, "the "+panel+" restored by a click", maxed(false))
	r.s.WaitFor(t, "the restored "+panel+" drawn", func(sc string) bool { return panelHeight(sc, panel) == docked })
	if errs := onLoop(r, func() []error { r.h.mu.Lock(); defer r.h.mu.Unlock(); return append([]error(nil), r.h.errs...) }); len(errs) != 0 {
		t.Errorf("refused: %v", errs)
	}
}

// panelHeight is how many rows the panel titled title spans on screen, both corners counted; 0 when
// it is not drawn.
func panelHeight(screen, title string) int {
	lines := strings.Split(screen, "\n")
	for y, l := range lines {
		c := col(l, "┌ "+title)
		if c < 0 {
			continue
		}
		for y2 := y + 1; y2 < len(lines); y2++ {
			if rs := []rune(lines[y2]); c < len(rs) && rs[c] == '└' {
				return y2 - y + 1
			}
		}
	}
	return 0
}

// screenRectOf is where window id is drawn; false while it is not on screen.
func screenRectOf(r *running, id string) (tuicore.Rect, bool) {
	type placed struct {
		at tuicore.Rect
		on bool
	}
	p := onLoop(r, func() placed {
		c, _ := r.h.p.Find(id)
		w, ok := c.(interface{ ScreenRect() (tuicore.Rect, bool) })
		if !ok {
			return placed{}
		}
		at, on := w.ScreenRect()
		return placed{at, on}
	})
	return p.at, p.on
}
