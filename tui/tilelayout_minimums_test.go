//go:build linux || darwin

package tui

import (
	"strings"
	"testing"

	tuicore "github.com/yongjohnlee80/golib/tui"
)

// tilelayout_minimums_test.go: each tileable panel's least tile is what its content needs, so a
// panel tiled at its least still takes the keyboard in its view (main.qml names what each number
// holds). Each is tiled at a 5% tile, under its least, on the edge given, so the least is what it
// gets: across a left or right edge its columns, across a top or bottom one its rows.

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

// atItsLeast tiles panel at edge at a 5% tile, opens it as open does, and wants it tiled at least
// cells across the edge, the keyboard in its view. The page takes the keyboard first, so the open
// is what brings it into the panel.
func atItsLeast(t *testing.T, r *running, panel, edge string, least int, open func()) tuicore.Rect {
	t.Helper()
	onLoop(r, func() bool {
		c := core{r.h}
		c.SetPref(layoutKey(panel), layoutTiled)
		c.SetPref(tileEdgeKey(panel), edge)
		c.SetPref(tileSizeKey(panel), "5")
		r.h.focusEditor()
		return true
	})
	open()
	view := onLoop(r, func() string { return r.h.windowView(panel) })
	var at tuicore.Rect
	r.s.WaitFor(t, "the "+panel+" tiled at its least at the "+edge+", the keyboard in "+view, func(string) bool {
		var on bool
		at, on = screenRectOf(r, panel)
		across := at.W
		if edge == "top" || edge == "bottom" {
			across = at.H
		}
		return on && across == least && onLoop(r, func() bool { return r.h.tiledNow(panel) && within(r, view) })
	})
	return at
}

// closePanel closes panel and waits for it to go.
func closePanel(t *testing.T, r *running, panel string) {
	t.Helper()
	onLoop(r, func() bool { r.h.keep(r.h.p.Call(panel, "close")); return true })
	r.s.WaitFor(t, "the "+panel+" closed", func(string) bool { return !onLoop(r, func() bool { return r.h.panelOpen[panel] }) })
}

// The explorer's least: 25 columns at the left, its edge, and 4 rows at the bottom, SPC e each time.
func TestTheExplorerTiledAtItsLeastTakesTheKeyboard(t *testing.T) {
	d := startDaemon(t, map[string][]string{"kb": {"a.md", "a\n"}})
	r := attached(t, d)
	spcE := func() { r.leader(t, 'e') }
	atItsLeast(t, r, "explorer", "left", 25, spcE)
	closePanel(t, r, "explorer")
	atItsLeast(t, r, "explorer", "bottom", 4, spcE)
}

// The todos' least: 20 columns at the left, their edge, and 16 rows at the bottom, where their table
// still has a row for the keyboard, SPC x each time.
func TestTheTodosTiledAtTheirLeastTakeTheKeyboard(t *testing.T) {
	d := startDaemon(t, map[string][]string{"kb": {"a.md", "a\n"}})
	r := attached(t, d)
	spcX := func() { r.leader(t, 'x') }
	atItsLeast(t, r, "todos", "left", 20, spcX)
	closePanel(t, r, "todos")
	atItsLeast(t, r, "todos", "bottom", 16, spcX)
}

// The terminal's least: 6 rows at the bottom, its edge, three of them the shell's, and 27 columns
// at the left, SPC ` each time.
func TestTheTerminalTiledAtItsLeastTakesTheKeyboard(t *testing.T) {
	_, r := runShellTUI(t, nil)
	spcTick := func() { r.leader(t, '`') }
	atItsLeast(t, r, "terminal", "bottom", 6, spcTick)
	closePanel(t, r, "terminal") // its shell runs on: SPC ` opens it again
	atItsLeast(t, r, "terminal", "left", 27, spcTick)
}

// The agent's least: 27 columns at the right, its edge, and 6 rows at the bottom, SPC g each time.
func TestTheAgentTiledAtItsLeastTakesTheKeyboard(t *testing.T) {
	agent, _ := fakeAgent(t)
	_, r, _ := runAgentTUI(t, t.TempDir(), []agentProfile{{Name: "heron", Command: shellQuote(agent), Codename: "heron"}}, "heron")
	spcG := func() { r.leader(t, 'g') }
	atItsLeast(t, r, "agent", "right", 27, spcG)
	closePanel(t, r, "agent")
	atItsLeast(t, r, "agent", "bottom", 6, spcG)
}

// The results' least: 7 rows at the bottom, their edge, one of them a row of results, and 20
// columns at the left. They open with their table; the keyboard goes there as Ctrl+h/j/k/l takes it.
func TestTheResultsTiledAtTheirLeastTakeTheKeyboard(t *testing.T) {
	r, _ := kindsTUI(t, "tablestall")
	toResults := func() { onLoop(r, func() bool { r.h.focusWindow("results"); return true }) }
	at := atItsLeast(t, r, "results", "bottom", 7, toResults)
	if rows := strings.Split(r.s.String(), "\n"); at.Y+4 >= len(rows) || !strings.Contains(rows[at.Y+4], "carol") {
		t.Fatalf("the results at their least show no row of results:\n%s", r.s.String())
	}
	atItsLeast(t, r, "results", "left", 20, toResults)
}
