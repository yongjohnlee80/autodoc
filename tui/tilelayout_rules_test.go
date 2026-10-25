package tui

import (
	"strings"
	"testing"

	"github.com/yongjohnlee80/golib/tui/panelgeometry"
)

// tilelayout_rules_test.go: the base's rules for its windows. Ctrl+h/j/k/l goes by where a window
// is placed; Arrange leaves a tiled panel; the dock's editor icon brings a minimized page back;
// focus raises a floating page.

// within reports whether the keyboard is in view id; read on the loop.
func within(r *running, id string) bool {
	c, ok := r.h.p.Find(id)
	return ok && r.h.p.App().FocusWithin(c)
}

// Ctrl+h/j/k/l goes by where the windows are placed, not by a panel's edge preference: the explorer
// floating at the left, its edge preference the right, is the page's window to the left, and the
// page is the explorer's to the right. Nothing lies above the page.
func TestCtrlHjklGoesToTheWindowPlacedThatWay(t *testing.T) {
	d := startDaemonWith(t, "", map[string][]string{"kb": {"a.md", "a\n"}}, daemonOpts{prefs: map[string]string{
		"tui.explorer.edge":                "right",
		panelgeometry.FloatKey("explorer"): panelgeometry.FormatFloat([4]float64{0, 30, 30, 40}), // centred down the page: nothing of it lies above
	}})
	r := runTUI(t, NewSession(d.sock, nil), Options{})
	r.ready(t)
	onLoop(r, func() bool { r.h.keep(r.h.p.Call("explorer", "open")); return true })
	r.s.WaitFor(t, "the explorer floating at the left", func(string) bool { b := boundsOf(r, "explorer"); return b.W > 0 && b.X < 10 })
	if got := onLoop(r, func() [3]bool {
		r.h.focusEditor()
		r.h.movePane("k")
		up := within(r, "editor")
		r.h.movePane("h")
		left := within(r, "explorerTree")
		r.h.movePane("l")
		return [3]bool{up, left, within(r, "editor")}
	}); got != [3]bool{true, true, true} {
		t.Fatalf("Ctrl+K kept the page %v, Ctrl+H reached the explorer %v, Ctrl+L came back %v; want all true", got[0], got[1], got[2])
	}
}

// A panel tiled now is its tile's to place: Arrange says so and leaves it, and opens no card.
func TestArrangeLeavesATiledPanel(t *testing.T) {
	r := tiledExplorer(t)
	onLoop(r, func() bool { r.h.keep(r.h.p.Call("explorerTree", "forceActiveFocus")); r.h.arrangePanel(); return true })
	r.s.WaitFor(t, "the notice that it is tiled", func(sc string) bool { // a toast wraps it
		return strings.Contains(sc, "the explorer is tiled: Layout › Float lets it") && strings.Contains(sc, "be arranged")
	})
	if a := onLoop(r, func() bool { return r.h.arrange != nil }); a {
		t.Fatal("an arrangement began over a tiled panel")
	}
}

// The dock's editor icon shows the page's window when it is minimized, the keyboard in it.
func TestTheDocksEditorIconRestoresAMinimizedPage(t *testing.T) {
	d := startDaemonWith(t, "", map[string][]string{"kb": {"a.md", "a\n"}}, daemonOpts{prefs: map[string]string{layoutKey("editor"): layoutFloat}})
	r := runTUI(t, NewSession(d.sock, nil), Options{})
	r.ready(t)
	onLoop(r, func() bool { r.h.keep(r.h.p.Call("editorWindow", "minimize")); return true })
	r.s.WaitFor(t, "the page minimized", func(string) bool { return onLoop(r, r.h.editorMinimized) })
	onLoop(r, func() bool { r.h.goDock("editor"); return true })
	r.s.WaitFor(t, "the page shown, the keyboard in it", func(string) bool {
		return onLoop(r, func() bool { return !r.h.editorMinimized() && within(r, "editor") })
	})
}

// Focus raises a floating page, as it raises any window: the explorer opened as a user opens it
// (SPC e, the keyboard in it) is over the page; the page given the keyboard is over the explorer.
func TestFocusRaisesAFloatingPage(t *testing.T) {
	d := startDaemonWith(t, "", map[string][]string{"kb": {"a.md", "a\n"}}, daemonOpts{prefs: map[string]string{
		layoutKey("editor"):                layoutFloat,
		panelgeometry.FloatKey("explorer"): panelgeometry.FormatFloat([4]float64{10, 20, 40, 50}),
	}})
	r := runTUI(t, NewSession(d.sock, nil), Options{})
	r.ready(t)
	r.leader(t, 'e')
	r.s.WaitFor(t, "the explorer over the page", func(sc string) bool { return strings.Contains(sc, "explorer") })
	onLoop(r, func() bool { r.h.focusEditor(); return true })
	r.s.WaitFor(t, "the page over the explorer", func(sc string) bool { return !strings.Contains(sc, "explorer") })
}
