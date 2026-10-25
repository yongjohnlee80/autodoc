package tui

import (
	"strconv"
	"testing"

	tuicore "github.com/yongjohnlee80/golib/tui"
	"github.com/yongjohnlee80/golib/tui/widget"
)

// tilelayout_prefs_test.go: each window's Layout and tile, as preferences (tilelayout.go).

// tileOf is the base's tile key: wanted (its Layout is Tiled) and governed (placed over a tile now).
func tileOf(r *running, key string) (wants, governed bool) {
	st := onLoop(r, func() [2]bool {
		tiles := tilesOf(r)
		if tiles == nil || tiles.Tile(key) == nil {
			return [2]bool{}
		}
		return [2]bool{tiles.Tile(key).Wants(), tiles.Tile(key).Governed()}
	})
	return st[0], st[1]
}

// boundsOf is the window id's bounds, as its core has them.
func boundsOf(r *running, id string) tuicore.Rect {
	return onLoop(r, func() tuicore.Rect {
		c, _ := r.h.p.Find(id)
		w, ok := c.(interface{ WindowBehavior() *widget.WindowCore })
		if !ok || w.WindowBehavior() == nil {
			return tuicore.Rect{}
		}
		return w.WindowBehavior().Bounds()
	})
}

// tiledExplorer runs the TUI with the explorer's Layout Tiled, opens it, and waits for its tile.
func tiledExplorer(t *testing.T) *running {
	t.Helper()
	d := startDaemonWith(t, "", map[string][]string{"kb": {"a.md", "a\n"}}, daemonOpts{prefs: map[string]string{layoutKey("explorer"): layoutTiled}})
	r := runTUI(t, NewSession(d.sock, nil), Options{})
	r.ready(t)
	onLoop(r, func() bool { r.h.keep(r.h.p.Call("explorer", "open")); return true })
	r.s.WaitFor(t, "the explorer tiled", func(string) bool { _, g := tileOf(r, "explorer"); return g })
	return r
}

// A panel whose Layout is Tiled is placed over its tile when it opens: the explorer down the left,
// 30% of the base, the page beside it.
func TestAPanelWhoseLayoutIsTiledIsTiledBesideThePage(t *testing.T) {
	r := tiledExplorer(t)
	if b := boundsOf(r, "explorer"); b.X != 0 || b.W != 30 {
		t.Fatalf("the explorer tiled at %+v, want 30 cells down the left", b)
	}
	if b := boundsOf(r, "editorWindow"); b.X != 30 || b.W != 70 {
		t.Fatalf("the page at %+v, want the rest of the base beside the explorer", b)
	}
}

// The Layout is the user's choice. The explorer, the agent and the todos tiled beside the page fill
// 100 columns at their least (20 each, the page's 40); narrowed to 90, the todos, the last to ask,
// are released to their own place, but their Layout is still Tiled (their preference, and what the
// manager wants), and their Layout › still checks Tiled. Widened again, they tile again by
// themselves. (Below the Window's 80 columns is the too-small screen: no shrink goes there.)
func TestAPanelReleasedForRoomStaysTiledAndTilesAgainWhenTheRoomReturns(t *testing.T) {
	d := startDaemonWith(t, "", map[string][]string{"kb": {"a.md", "a\n"}}, daemonOpts{prefs: map[string]string{
		layoutKey("explorer"): layoutTiled, layoutKey("agent"): layoutTiled, layoutKey("todos"): layoutTiled,
	}})
	r := runTUI(t, NewSession(d.sock, nil), Options{})
	r.ready(t)
	for _, id := range []string{"explorer", "agent", "todos"} {
		onLoop(r, func() bool { r.h.keep(r.h.p.Call(id, "open")); return true })
		r.s.WaitFor(t, "the "+id+" tiled", func(string) bool { _, g := tileOf(r, id); return g })
	}
	r.s.Backend.InjectResize(90, 30)
	r.s.WaitFor(t, "the todos released for room", func(string) bool { _, g := tileOf(r, "todos"); return !g })
	if _, g := tileOf(r, "explorer"); !g {
		t.Fatal("the explorer, which asked first, was released too")
	}
	wants, _ := tileOf(r, "todos")
	stored := onLoop(r, func() string { return r.h.prefs.Value(layoutKey("todos")) })
	if !wants || stored != layoutTiled {
		t.Fatalf("released for room: wanted %v, Layout %q; want still Tiled, the user's choice", wants, stored)
	}
	wantLayoutChecked(t, "the todos released", onLoop(r, func() []widget.MenuItemModel { return layoutChoices(core{r.h}, "todos", tabsRowOf) }), true)
	r.s.Backend.InjectResize(100, 30)
	r.s.WaitFor(t, "the todos tiled again", func(string) bool { _, g := tileOf(r, "todos"); return g })
}

// A shared border's drag keeps the tile's size, and only that: the explorer's border dragged ten
// cells right is a tile of 40%, its Layout and edge as they were, and its own place (its docked
// size, its floating rectangle) untouched.
func TestABorderDragKeepsTheTileSizeAlone(t *testing.T) {
	r := tiledExplorer(t)
	before := onLoop(r, func() [2]int { return [2]int{len(r.h.prefs.panelFloat), len(r.h.prefs.panelGeo)} })
	r.keys(t, tuicore.MouseEvent{Kind: tuicore.MousePress, Button: tuicore.MouseRight, Mods: tuicore.ModAlt, X: 29, Y: 10},
		tuicore.MouseEvent{Kind: tuicore.MouseMotion, Button: tuicore.MouseRight, Mods: tuicore.ModAlt, X: 39, Y: 10},
		tuicore.MouseEvent{Kind: tuicore.MouseRelease, Button: tuicore.MouseRight, Mods: tuicore.ModAlt, X: 39, Y: 10})
	r.s.WaitFor(t, "the tile's size kept", func(string) bool {
		return onLoop(r, func() string { return r.h.prefs.Value(tileSizeKey("explorer")) }) == "40"
	})
	got := onLoop(r, func() [4]string {
		return [4]string{r.h.prefs.Value(layoutKey("explorer")), r.h.prefs.Value(tileEdgeKey("explorer")),
			strconv.Itoa(len(r.h.prefs.panelFloat)), strconv.Itoa(len(r.h.prefs.panelGeo))}
	})
	want := [4]string{layoutTiled, "left", strconv.Itoa(before[0]), strconv.Itoa(before[1])}
	if got != want {
		t.Fatalf("after the border drag: Layout, edge, floats, sizes %v; want %v (the panel's own place untouched)", got, want)
	}
}

// Layout › Float and Tiled are live, on the same node: the explorer floats and tiles again with
// no rebuild.
func TestALayoutFlipIsLiveOnTheSameNode(t *testing.T) {
	r := tiledExplorer(t)
	node := onLoop(r, func() any { c, _ := r.h.p.Find("explorer"); return c })
	onLoop(r, func() bool { r.h.tiles.setLayout("explorer", false); return true })
	r.s.WaitFor(t, "the explorer floated", func(string) bool { w, g := tileOf(r, "explorer"); return !w && !g })
	onLoop(r, func() bool { r.h.tiles.setLayout("explorer", true); return true })
	r.s.WaitFor(t, "the explorer tiled again", func(string) bool { w, g := tileOf(r, "explorer"); return w && g })
	if now := onLoop(r, func() any { c, _ := r.h.p.Find("explorer"); return c }); now != node {
		t.Fatal("a Layout flip rebuilt the explorer")
	}
}
