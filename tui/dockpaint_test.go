package tui

import (
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	tuicore "github.com/yongjohnlee80/golib/tui"
)

// THE DOCK'S PAINTED ICON (dock.go: A HIDE FLASHES ITS ICON) — what the screen shows, not the flash's
// hook: a panel hidden into the dock, by its strip's _, the dock's click or Escape, has its icon
// painted in the bar's accent, then in the hidden look (dimmed), each a colour of its own beside the
// shown icon's. Read from the painted cells, the same in the terminal and the window.

// dockIconPaints samples the painted foreground of the dock icon glyph on the bar's row every 10ms,
// from before hide runs until a second after: the shown colour, and every colour after, repeats
// collapsed.
func dockIconPaints(t *testing.T, r *running, glyph string, hide func()) (shown tuicore.CellColor, after []tuicore.CellColor) {
	t.Helper()
	settled(t, r) // the shown colour as drawn, not one a redraw has still to replace
	bar := strings.Split(r.s.String(), "\n")[0]
	i := strings.Index(bar, glyph)
	if i < 0 {
		t.Fatalf("no %s on the bar: %q", glyph, bar)
	}
	x := len([]rune(bar[:i]))
	fg := func() tuicore.CellColor { return r.s.Backend.Snapshot()[0][x].Attrs.FG }
	shown = fg()
	var mu sync.Mutex
	stop, done := make(chan struct{}), make(chan struct{})
	go func() {
		defer close(done)
		for {
			c := fg()
			mu.Lock()
			if len(after) == 0 || after[len(after)-1] != c {
				after = append(after, c)
			}
			mu.Unlock()
			select {
			case <-stop:
				return
			case <-time.After(10 * time.Millisecond):
			}
		}
	}()
	hide()
	time.Sleep(time.Duration(dockFlashSteps)*dockFlashStep + 400*time.Millisecond)
	close(stop)
	<-done
	return shown, after
}

// litThenDimmed fails unless the icon, after the hide, was painted in a colour of its own (lit) and
// ends in another (the hidden look), each unlike the shown icon's.
func litThenDimmed(t *testing.T, what string, shown tuicore.CellColor, after []tuicore.CellColor) {
	t.Helper()
	if len(after) == 0 {
		t.Fatalf("%s: nothing sampled", what)
	}
	dimmed := after[len(after)-1]
	if dimmed == shown {
		t.Fatalf("%s: the hidden icon ends painted as the shown one (%+v); want it dimmed. Painted: %+v", what, shown, after)
	}
	if !slices.ContainsFunc(after[:len(after)-1], func(c tuicore.CellColor) bool { return c != shown && c != dimmed }) {
		t.Fatalf("%s: the icon was never painted lit (a colour unlike both the shown %+v and the dimmed %+v): %+v", what, shown, dimmed, after)
	}
}

func TestTheExplorersStripHidePaintsItsDockIconLitThenDimmed(t *testing.T) {
	d := startDaemon(t, map[string][]string{"kb": {"a.md", "a\n"}})
	r := attached(t, d)
	r.waitListed(t, 1)
	r.leader(t, 'e')
	r.s.WaitFor(t, "the explorer open", func(string) bool { return r.panelIsOpen("explorer") })
	shown, after := dockIconPaints(t, r, "☰", func() { windowControl(t, r, "_") })
	litThenDimmed(t, "the explorer's _", shown, after)

	onLoop(r, func() bool { r.h.goDock("explorer"); return true }) // shown again
	r.s.WaitFor(t, "shown again", func(string) bool { return onLoop(r, func() bool { return !r.h.panelHidden("explorer") }) })
	shown, after = dockIconPaints(t, r, "☰", func() { onLoop(r, func() bool { r.h.goDock("explorer"); return true }) })
	litThenDimmed(t, "the dock's click", shown, after)
}

func TestTheTerminalsStripHideAndEscapePaintItsDockIconLitThenDimmed(t *testing.T) {
	_, r := runShellTUI(t, nil)
	r.leader(t, '`')
	r.s.WaitFor(t, "the terminal's keyboard", func(string) bool { return r.focused("terminalView") })
	shown, after := dockIconPaints(t, r, "❯", func() { windowControl(t, r, "_") })
	litThenDimmed(t, "the terminal's _", shown, after)

	r.leader(t, '`')
	r.s.WaitFor(t, "the terminal's keyboard again", func(string) bool { return r.focused("terminalView") })
	shown, after = dockIconPaints(t, r, "❯", func() { r.keys(t, ctrlKey('t'), esc()) })
	litThenDimmed(t, "Escape in Normal mode", shown, after)
}

func TestTheAgentsStripHidePaintsItsDockIconLitThenDimmed(t *testing.T) {
	agent, _ := fakeAgent(t)
	_, r, _ := runAgentTUI(t, t.TempDir(), []agentProfile{{Name: "fake", Command: shellQuote(agent)}}, "fake")
	r.leader(t, 'g')
	r.s.WaitForText(t, "started")
	shown, after := dockIconPaints(t, r, "✦", func() { windowControl(t, r, "_") })
	litThenDimmed(t, "the agents' _", shown, after)
}
