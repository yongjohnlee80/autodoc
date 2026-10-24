package tui

import (
	"strings"
	"testing"
	"time"
)

// THE STRIP'S + — a panel's tab strip ends in _, + and x, the title bar's controls side by side. +
// maximizes the panel as its tab menu's Maximize and F11 do (the Drawer's own toggleMaximize, which
// panelChanged follows), and a second + restores it: the terminal, the agents and the explorer.

// stripMaximizes presses the strip's + and waits for panel to be maximized, then presses it again and
// waits for it restored.
func stripMaximizes(t *testing.T, r *running, panel string) {
	t.Helper()
	maximized := func() bool { return onLoop(r, func() bool { return r.h.panelMax[panel] }) }
	if maximized() {
		t.Fatalf("the %s panel is maximized before its +", panel)
	}
	x0, y0 := cellOf(t, r.s.String(), "_  +  x")
	windowControl(t, r, "+")
	r.s.WaitFor(t, "+ maximized the "+panel, maximizedNow(maximized, true))
	// panelMax is told as the Drawer changes, before the screen is drawn again: the second press waits
	// for the screen to settle, so it lands on the strip where the maximized panel draws it
	settled(t, r)
	windowControl(t, r, "+")
	r.s.WaitFor(t, "+ again restored the "+panel, func(sc string) bool {
		x, y := stripAt(sc)
		return !maximized() && x == x0 && y == y0
	})
}

// settled waits for the screen to stop changing: the same over three looks 50ms apart.
func settled(t *testing.T, r *running) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	last, same := r.s.String(), 0
	for same < 3 {
		if time.Now().After(deadline) {
			t.Fatalf("the screen never settled:\n%s", r.s)
		}
		time.Sleep(50 * time.Millisecond)
		if sc := r.s.String(); sc == last {
			same++
		} else {
			last, same = sc, 0
		}
	}
}

// stripAt is where a strip's controls begin on screen, their column in cells and their row; -1, -1
// when none is drawn.
func stripAt(screen string) (x, y int) {
	for row, line := range strings.Split(screen, "\n") {
		if i := strings.Index(line, "_  +  x"); i >= 0 {
			return len([]rune(line[:i])), row
		}
	}
	return -1, -1
}

func TestTheTerminalsStripMaximizesAndRestores(t *testing.T) {
	_, r := runShellTUI(t, nil)
	r.leader(t, '`')
	r.s.WaitFor(t, "the terminal's keyboard", func(string) bool { return r.focused("terminalView") })
	row0, _ := frameAt(r.s.String())
	maximized := func() bool { return onLoop(r, func() bool { return r.h.panelMax["terminal"] }) }
	windowControl(t, r, "+")
	r.s.WaitFor(t, "+ maximized it: it fills the dock area", func(sc string) bool { y, _ := frameAt(sc); return maximized() && y >= 0 && y < row0 })
	windowControl(t, r, "+")
	r.s.WaitFor(t, "+ again restored it: the frame back", func(sc string) bool { y, _ := frameAt(sc); return !maximized() && y == row0 })
}

func TestTheAgentsStripMaximizesAndRestores(t *testing.T) {
	agent, _ := fakeAgent(t)
	_, r, _ := runAgentTUI(t, t.TempDir(), []agentProfile{{Name: "fake", Command: shellQuote(agent)}}, "fake")
	r.leader(t, 'g')
	r.s.WaitForText(t, "started")
	stripMaximizes(t, r, "agent")
}

func TestTheExplorersStripMaximizesAndRestores(t *testing.T) {
	d := startDaemon(t, map[string][]string{"kb": {"a.md", "a\n"}})
	r := attached(t, d)
	r.waitListed(t, 1)
	r.leader(t, 'e')
	r.s.WaitFor(t, "the explorer open", func(string) bool { return r.panelIsOpen("explorer") })
	stripMaximizes(t, r, "explorer")
}
