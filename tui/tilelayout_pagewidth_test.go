//go:build linux || darwin

package tui

import (
	"testing"

	tuicore "github.com/yongjohnlee80/golib/tui"
)

// tilelayout_pagewidth_test.go: the page tiled is as wide as its ruler and no wider. The strips
// beside it take the rest, and alone it is centred, the columns either side of it no window's.

// The agent tiled at the right on a 220-column screen asks 35%, 77 columns, which would leave the
// page 143: the page keeps its width, and the agent takes the rest, beside it with no gap.
func TestTheAgentTiledBesideThePageTakesWhatThePageLeaves(t *testing.T) {
	agent, _ := fakeAgent(t)
	_, r, _ := runAgentTUIWith(t, t.TempDir(), []agentProfile{{Name: "heron", Command: shellQuote(agent), Codename: "heron"}}, "heron",
		map[string]string{layoutKey("agent"): layoutTiled, tileEdgeKey("agent"): "right"}, Options{})
	r.s.Backend.InjectResize(220, 30)
	onLoop(r, func() bool { r.h.agents.toggleAgent(); return true })
	width := onLoop(r, func() int { return r.h.pageWidth })
	var page, ag tuicore.Rect
	r.s.WaitFor(t, "the page at its width, the agent beside it to the screen's edge", func(string) bool {
		page, _ = screenRectOf(r, "editorWindow")
		ag, _ = screenRectOf(r, "agent")
		return page.W == width && ag.X == page.X+page.W && ag.X+ag.W == 220
	})
}

// The click back: the explorer floats at the left with the keyboard, and a click on the page,
// tiled and centred at its width, takes the keyboard there and raises it over the explorer. A click
// back on the explorer, left of the page, takes the keyboard back: the page's tile is the page's
// width, so nothing of the page lies under the click, though the page is above.
func TestAClickOnAFloatingPanelBesideTheTiledPageTakesTheKeyboard(t *testing.T) {
	d := startDaemon(t, map[string][]string{"kb": {"a.md", "a\n"}})
	r := attached(t, d)
	r.s.Backend.InjectResize(220, 30)
	onLoop(r, func() bool {
		r.h.keep(r.h.p.Call("explorer", "open"))
		r.h.keep(r.h.p.Call("explorerTree", "forceActiveFocus"))
		return true
	})
	r.s.WaitFor(t, "the explorer floating at the left, the keyboard in it", func(string) bool {
		ex, on := screenRectOf(r, "explorer")
		return on && ex.X == 0 && onLoop(r, func() bool { return within(r, "explorerTree") })
	})
	click := func(at tuicore.Point) {
		r.keys(t, tuicore.MouseEvent{Kind: tuicore.MousePress, Button: tuicore.MouseLeft, X: at.X, Y: at.Y},
			tuicore.MouseEvent{Kind: tuicore.MouseRelease, Button: tuicore.MouseLeft, X: at.X, Y: at.Y})
	}
	ex, _ := screenRectOf(r, "explorer")
	click(tuicore.Point{X: 150, Y: ex.Y + ex.H/2}) // on the page's text, right of the explorer
	r.s.WaitFor(t, "the page's keyboard", func(string) bool { return onLoop(r, func() bool { return within(r, "editor") }) })
	at := tuicore.Point{X: ex.X + 2, Y: ex.Y + ex.H/2} // on the explorer, left of the page's ruler-wide text
	if width := onLoop(r, func() int { return r.h.pageWidth }); at.X >= (220-width)/2 {
		t.Fatalf("the click at %v is not left of the page's %d columns centred in 220", at, width)
	}
	click(at)
	r.s.WaitFor(t, "the explorer's keyboard again", func(string) bool { return onLoop(r, func() bool { return within(r, "explorerTree") }) })
}
