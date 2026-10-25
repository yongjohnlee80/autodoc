package tui

import (
	"slices"
	"strings"
	"testing"

	tuicore "github.com/yongjohnlee80/golib/tui"
	"github.com/yongjohnlee80/golib/tui/panelgeometry"
	"github.com/yongjohnlee80/golib/tui/widget"
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
// page is the explorer's to the right. The explorer's centre is below the page's, so nothing lies
// above the page.
func TestCtrlHjklGoesToTheWindowPlacedThatWay(t *testing.T) {
	d := startDaemonWith(t, "", map[string][]string{"kb": {"a.md", "a\n"}}, daemonOpts{prefs: map[string]string{
		"tui.explorer.edge":                "right",
		panelgeometry.FloatKey("explorer"): panelgeometry.FormatFloat([4]float64{0, 40, 30, 40}), // its centre below the page's on the screen
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

// Ctrl+h/j/k/l reads every window where it is drawn, in one frame: tiled or floating. The todos are
// tiled at the bottom, under the menu bar, so their tile is a row lower on the screen than in the
// manager's own frame. The explorer floats one row above them, as tall: its centre is above theirs
// on the screen, so the window Ctrl+J takes the keyboard to from it is the todos. (Where the keyboard
// lands once chosen is focusWindow's, TestCtrlHjklGoesToTheWindowPlacedThatWay.)
func TestCtrlJFromAFloatingPanelChoosesTheTiledOneJustBelow(t *testing.T) {
	d := startDaemonWith(t, "", map[string][]string{"kb": {"a.md", "a\n"}}, daemonOpts{prefs: map[string]string{
		layoutKey("todos"): layoutTiled, tileEdgeKey("todos"): "bottom",
	}})
	r := runTUI(t, NewSession(d.sock, nil), Options{})
	r.ready(t)
	onLoop(r, func() bool {
		r.h.keep(r.h.p.Call("todos", "open"))
		r.h.keep(r.h.p.Call("explorer", "open"))
		return true
	})
	r.s.WaitFor(t, "the todos tiled at the bottom, the explorer floating", func(string) bool {
		return boundsOf(r, "todos").H > 0 && boundsOf(r, "explorer").W > 0 && onLoop(r, func() bool { return r.h.tiledNow("todos") })
	})
	if onLoop(r, func() bool { return r.h.menuAutoHide(r.h.prefs) }) {
		t.Fatal("the menu bar hides: the manager is at the screen's top, and the frames agree")
	}
	todos := boundsOf(r, "todos")
	top := todos.Y + 1 // drawn below the menu bar's row (golib: a member is at its tile offset by the manager's origin)
	want := tuicore.Rect{X: todos.X + 2, Y: top - 1, W: 30, H: todos.H}
	onLoop(r, func() bool { // resized first: moved down at its full height, it would stop at the screen's bottom
		c, _ := r.h.p.Find("explorer")
		w := c.(interface{ WindowBehavior() *widget.WindowCore }).WindowBehavior()
		b := w.Bounds()
		w.ResizeBy(want.W-b.W, want.H-b.H)
		b = w.Bounds()
		w.MoveBy(want.X-b.X, want.Y-b.Y)
		return true
	})
	var got tuicore.Rect
	defer func() {
		if t.Failed() {
			t.Logf("the explorer at %v, wanted %v; the todos' tile %v", got, want, todos)
		}
	}()
	r.s.WaitFor(t, "the explorer a row above the todos", func(string) bool { got = boundsOf(r, "explorer"); return got == want })
	onLoop(r, func() bool { r.h.keep(r.h.p.Call("explorerTree", "forceActiveFocus")); return true })
	r.s.WaitFor(t, "the explorer's keyboard", func(string) bool { return onLoop(r, func() bool { return within(r, "explorerTree") }) })
	chosen := onLoop(r, func() string {
		shown := r.h.shownWindows()
		from := slices.IndexFunc(shown, r.h.hasKeyboard)
		if from < 0 || shown[from].panel != "explorer" {
			return "<the keyboard is not in the explorer>"
		}
		to, ok := nextWindow(shown, from, dirStep["j"])
		if !ok {
			return "<none>"
		}
		return to.panel
	})
	if chosen != "todos" {
		t.Fatalf("Ctrl+J from the explorer chose %q, want the todos just below it on the screen", chosen)
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

// The keyboard follows the stack, as a window manager's does: the explorer opened, then the todos
// over it; the todos closed, the keyboard is in the explorer, still on the screen over the page;
// the explorer closed, it is in the page.
func TestClosingAPanelGivesTheKeyboardToTheTopmostWindowStillOpen(t *testing.T) {
	d := startDaemonWith(t, "", map[string][]string{"kb": {"a.md", "a\n"}}, daemonOpts{prefs: map[string]string{}})
	r := runTUI(t, NewSession(d.sock, nil), Options{})
	r.ready(t)
	r.leader(t, 'e')
	r.s.WaitFor(t, "the explorer's keyboard", func(string) bool { return onLoop(r, func() bool { return within(r, "explorerTree") }) })
	r.leader(t, 'x')
	r.s.WaitFor(t, "the todos' keyboard", func(string) bool { return onLoop(r, func() bool { return within(r, "todoOpenTable") }) })
	onLoop(r, func() bool { r.h.keep(r.h.p.Call("todos", "close")); return true })
	r.s.WaitFor(t, "the explorer's keyboard, the explorer on the screen", func(sc string) bool {
		return onLoop(r, func() bool { return within(r, "explorerTree") }) && strings.Contains(sc, "explorer")
	})
	onLoop(r, func() bool { r.h.keep(r.h.p.Call("explorer", "close")); return true })
	r.s.WaitFor(t, "the page's keyboard", func(string) bool { return onLoop(r, func() bool { return within(r, "editor") }) })
}

// A minimized window is no home for the keyboard, though its layer stays in the overlay's stack
// (minimized is only concealed): the todos open, the page raised over them and minimized, the
// explorer opened over both and closed, the keyboard is in the todos on the screen, not the hidden
// page.
func TestTheKeyboardSkipsAMinimizedPageForTheTopmostWindowShown(t *testing.T) {
	d := startDaemonWith(t, "", map[string][]string{"kb": {"a.md", "a\n"}}, daemonOpts{prefs: map[string]string{layoutKey("editor"): layoutFloat}})
	r := runTUI(t, NewSession(d.sock, nil), Options{})
	r.ready(t)
	r.leader(t, 'x')
	r.s.WaitFor(t, "the todos' keyboard", func(string) bool { return onLoop(r, func() bool { return within(r, "todoOpenTable") }) })
	onLoop(r, func() bool { r.h.focusEditor(); return true })
	r.s.WaitFor(t, "the page's keyboard, raised", func(string) bool { return onLoop(r, func() bool { return within(r, "editor") }) })
	onLoop(r, func() bool { r.h.keep(r.h.p.Call("editorWindow", "minimize")); return true })
	r.s.WaitFor(t, "the page minimized", func(string) bool { return onLoop(r, r.h.editorMinimized) })
	onLoop(r, func() bool { // opened over both, the keyboard in it
		r.h.keep(r.h.p.Call("explorer", "open"))
		r.h.keep(r.h.p.Call("explorerTree", "forceActiveFocus"))
		return true
	})
	r.s.WaitFor(t, "the explorer's keyboard", func(string) bool { return onLoop(r, func() bool { return within(r, "explorerTree") }) })
	onLoop(r, func() bool { r.h.keep(r.h.p.Call("explorer", "close")); return true })
	r.s.WaitFor(t, "the todos' keyboard, the page still minimized", func(string) bool {
		return onLoop(r, func() bool { return within(r, "todoOpenTable") && r.h.editorMinimized() })
	})
}

// Ctrl+K's choice, by placed rect alone: of the windows whose centre is above, the one whose bottom
// edge is nearest the window's top; one overlapping it is nearest of all; at one gap, the one nearer
// across; and with none above, none.
func TestCtrlKChoosesTheNearestWindowAbove(t *testing.T) {
	from := placedWindow{"from", tuicore.Rect{X: 10, Y: 10, W: 10, H: 5}}
	far := placedWindow{"far", tuicore.Rect{X: 10, Y: 0, W: 10, H: 4}}      // its bottom 6 above
	near := placedWindow{"near", tuicore.Rect{X: 10, Y: 3, W: 10, H: 5}}    // 2 above
	aside := placedWindow{"aside", tuicore.Rect{X: 40, Y: 3, W: 10, H: 5}}  // 2 above, far across
	over := placedWindow{"over", tuicore.Rect{X: 12, Y: 8, W: 6, H: 3}}     // overlapping its top
	below := placedWindow{"below", tuicore.Rect{X: 10, Y: 20, W: 10, H: 5}} // not that way
	for _, c := range []struct {
		shown []placedWindow
		want  string
	}{
		{[]placedWindow{from, far, near, below}, "near"},
		{[]placedWindow{from, far, near, over}, "over"},
		{[]placedWindow{from, aside, near}, "near"},
		{[]placedWindow{from, below}, ""},
	} {
		got, ok := nextWindow(c.shown, 0, dirStep["k"])
		if got.panel != c.want || ok != (c.want != "") {
			t.Errorf("Ctrl+K among %v chose %q (%v), want %q", c.shown, got.panel, ok, c.want)
		}
	}
}

// With every window put away, the page minimized and no panel open, the keyboard's home is the
// page's editor still, the last of all; and a direction Ctrl+h/j/k/l has no step for moves nothing.
func TestTheKeyboardsLastHomeIsThePage(t *testing.T) {
	d := startDaemonWith(t, "", map[string][]string{"kb": {"a.md", "a\n"}}, daemonOpts{prefs: map[string]string{layoutKey("editor"): layoutFloat}})
	r := runTUI(t, NewSession(d.sock, nil), Options{})
	r.ready(t)
	onLoop(r, func() bool { r.h.movePane("x"); return true })
	if !onLoop(r, func() bool { return within(r, "editor") }) {
		t.Fatal("a direction with no step moved the keyboard out of the page")
	}
	onLoop(r, func() bool { r.h.keep(r.h.p.Call("editorWindow", "minimize")); return true })
	r.s.WaitFor(t, "the page minimized", func(string) bool { return onLoop(r, r.h.editorMinimized) })
	if home := onLoop(r, r.h.keyboardHome); home != "editor" {
		t.Fatalf("nothing shown: the keyboard's home is %q, want the page's editor", home)
	}
}
