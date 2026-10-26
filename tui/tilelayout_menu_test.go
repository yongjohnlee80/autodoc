package tui

import (
	"context"
	"slices"
	"strings"
	"testing"

	tuicore "github.com/yongjohnlee80/golib/tui"
	"github.com/yongjohnlee80/golib/tui/widget"
)

// tilelayout_menu_test.go: each window's Layout ›, Tiled and Float, on its menu right above Go to ›,
// and :layout (tilelayout.go).

// rowAt is where the row id is in rows, top level; -1 when it is not.
func rowAt(rows []widget.MenuItemModel, id string) int {
	return slices.IndexFunc(rows, func(m widget.MenuItemModel) bool { return string(m.ID) == id })
}

// wantLayoutAbove fails unless rows hold Layout › right above the row goTo, Tiled and Float radios
// in it, the one checked as tiled says.
func wantLayoutAbove(t *testing.T, menu string, rows []widget.MenuItemModel, goTo string, tiled bool) {
	t.Helper()
	l, g := rowAt(rows, "layout"), rowAt(rows, goTo)
	if l < 0 || g != l+1 {
		t.Fatalf("%s: Layout at %d, Go to at %d; want Layout right above Go to: %v", menu, l, g, rowIDs(rows))
	}
	wantLayoutChecked(t, menu, rows[l].Children, tiled)
}

// wantLayoutChecked fails unless choices are Tiled and the radio Float, the one checked as tiled
// says. The page's Tiled is a radio; a panel's is a submenu of its edges' radios, one checked while
// it is tiled and none while it floats.
func wantLayoutChecked(t *testing.T, menu string, choices []widget.MenuItemModel, tiled bool) {
	t.Helper()
	tr, okT := menuRow(choices, "layout.tiled")
	fl, okF := menuRow(choices, "layout.float")
	if !okT || !okF || fl.Kind != widget.ItemKindRadio {
		t.Fatalf("%s: Layout holds %v; want Tiled and the radio Float", menu, rowIDs(choices))
	}
	isTiled := tr.Checked
	if tr.Kind == widget.ItemKindSubmenu {
		if ids := rowIDs(tr.Children); !slices.Equal(ids, []string{"layout.tiled.left", "layout.tiled.right", "layout.tiled.top", "layout.tiled.bottom"}) {
			t.Fatalf("%s: Tiled holds %v; want the edges Left, Right, Top and Bottom", menu, ids)
		}
		checked := 0
		for _, e := range tr.Children {
			if e.Kind != widget.ItemKindRadio {
				t.Fatalf("%s: Tiled's %s is no radio", menu, e.ID)
			}
			if e.Checked {
				checked++
			}
		}
		isTiled = checked == 1
		if checked > 1 {
			t.Fatalf("%s: %d edges checked", menu, checked)
		}
	} else if tr.Kind != widget.ItemKindRadio {
		t.Fatalf("%s: Tiled is neither a radio nor a submenu", menu)
	}
	if isTiled != tiled || fl.Checked == tiled {
		t.Fatalf("%s: Tiled checked %v, Float %v; want the Layout stored, tiled %v", menu, isTiled, fl.Checked, tiled)
	}
}

// edgeRow is a panel's Layout › Tiled › edge row in rows (a panel's menu), or false.
func edgeRow(rows []widget.MenuItemModel, edge string) (widget.MenuItemModel, bool) {
	l := rowAt(rows, "layout")
	if l < 0 {
		return widget.MenuItemModel{}, false
	}
	tiled, ok := menuRow(rows[l].Children, "layout.tiled")
	if !ok {
		return widget.MenuItemModel{}, false
	}
	return menuRow(tiled.Children, "layout.tiled."+edge)
}

// The page's buffer strip and the terminal's tabs have Layout › right above Go to ›, checked as
// stored: the page Tiled, the terminal Float. Its Tiled chosen, the terminal's Layout is Tiled,
// and its menu says so.
func TestLayoutIsRightAboveGoToOnTheBufferAndShellMenus(t *testing.T) {
	_, r := runShellTUI(t, nil)
	wantLayoutAbove(t, "the buffer strip", onLoop(r, func() []widget.MenuItemModel { return r.h.bufferTabRows("") }), "buffer.tabs.goto", true)
	r.leader(t, '`')
	r.s.WaitFor(t, "the terminal's keyboard", func(string) bool { return r.focused("terminalView") })
	rows := onLoop(r, func() []widget.MenuItemModel { return r.h.terminals.tabRows("") })
	wantLayoutAbove(t, "the terminal", rows, "tabs.goto", false)
	bottom, ok := edgeRow(rows, "bottom")
	if !ok {
		t.Fatalf("the terminal's Layout has no Tiled › Bottom: %v", rowIDs(rows))
	}
	onLoop(r, func() bool { bottom.Action.(widget.TerminalTabsMenuAction).Run(""); return true })
	r.s.WaitFor(t, "the terminal's Layout Tiled", func(string) bool {
		return onLoop(r, func() string { return r.h.prefs.Value(layoutKey("terminal")) }) == layoutTiled
	})
	wantLayoutAbove(t, "the terminal, Tiled", onLoop(r, func() []widget.MenuItemModel { return r.h.terminals.tabRows("") }), "tabs.goto", true)
}

// The agent's tabs have Layout › right above Go to ›, Float as stored.
func TestLayoutIsRightAboveGoToOnTheAgentsMenu(t *testing.T) {
	agent, _ := fakeAgent(t)
	_, r, _ := runAgentTUI(t, t.TempDir(), []agentProfile{{Name: "heron", Command: shellQuote(agent), Codename: "heron"}}, "heron")
	onLoop(r, func() bool { r.h.agents.toggleAgent(); return true })
	r.s.WaitFor(t, "the agent running", func(string) bool { return r.agentRunningNow() })
	tab := onLoop(r, func() widget.TerminalTabID { return r.h.agents.instances["heron"].tab })
	wantLayoutAbove(t, "the agent", onLoop(r, func() []widget.MenuItemModel { return r.h.agents.tabRows(tab) }), "tabs.goto", false)
}

// The results panel has no strip: its Layout › ends its table's menu, after a separator.
func TestTheResultsLayoutEndsItsTablesMenu(t *testing.T) {
	r, _ := kindsTUI(t, "tablestall")
	rows := onLoop(r, func() []widget.MenuItemModel { return r.h.plugins.resultsMenuRows() })
	l := rowAt(rows, "layout")
	if l != len(rows)-1 || l < 1 || rows[l-1].Kind != widget.ItemKindSeparator {
		t.Fatalf("the results' menu %v; want Layout last, after a separator", rowIDs(rows))
	}
	wantLayoutChecked(t, "the results", rows[l].Children, false)
}

// glyphAt is where r's screen draws glyph inside rect, the first from the top-left; false when it
// does not.
func glyphAt(r *running, glyph string, in tuicore.Rect) (tuicore.Point, bool) {
	for y, line := range strings.Split(r.s.String(), "\n") {
		if y < in.Y || y >= in.Y+in.H {
			continue
		}
		for x, g := range []rune(line) {
			if string(g) == glyph && x >= in.X && x < in.X+in.W {
				return tuicore.Point{X: x, Y: y}, true
			}
		}
	}
	return tuicore.Point{}, false
}

// The explorer's and the todos' tabs have a menu of their own, Layout › alone: opened as a user
// opens them (SPC e, SPC x: the panel takes the keyboard, and comes forward), its ≡ opens the menu,
// and Tiled chosen by its keys (a, then t) makes the window's Layout Tiled.
func TestTheExplorersAndTheTodosTabsOfferTheirLayout(t *testing.T) {
	for _, c := range []struct {
		window string
		leader rune
	}{{"explorer", 'e'}, {"todos", 'x'}} {
		t.Run(c.window, func(t *testing.T) {
			d := startDaemonWith(t, "", map[string][]string{"kb": {"a.md", "a\n"}}, daemonOpts{prefs: map[string]string{}})
			r := runTUI(t, NewSession(d.sock, nil), Options{})
			r.ready(t)
			r.leader(t, c.leader)
			r.s.WaitFor(t, "the "+c.window+" open", func(string) bool { return boundsOf(r, c.window).W > 0 })
			at, ok := glyphAt(r, "≡", boundsOf(r, c.window))
			if !ok {
				t.Fatalf("no ≡ on the %s's tabs:\n%s", c.window, r.s)
			}
			r.keys(t, tuicore.MouseEvent{Kind: tuicore.MousePress, Button: tuicore.MouseLeft, X: at.X, Y: at.Y},
				tuicore.MouseEvent{Kind: tuicore.MouseRelease, Button: tuicore.MouseLeft, X: at.X, Y: at.Y})
			r.s.WaitForText(t, "Layout")
			r.keys(t, key('a'))
			r.s.WaitForText(t, "Float")
			r.keys(t, key('t'))
			r.s.WaitForText(t, "Bottom") // Tiled ›, the edges
			r.keys(t, key('f'))          // Left
			r.s.WaitFor(t, "the "+c.window+"'s Layout Tiled", func(string) bool {
				return onLoop(r, func() string { return r.h.prefs.Value(layoutKey(c.window)) }) == layoutTiled
			})
		})
	}
}

// :layout opens the editor's Layout over the page, whose Float its key chooses; :layout terminal the
// terminal's, whose Tiled its key chooses. A name that is no window says which are, and changes
// nothing.
func TestTheLayoutCommandOpensAWindowsLayout(t *testing.T) {
	d := startDaemonWith(t, "", map[string][]string{"kb": {"a.md", "a\n"}}, daemonOpts{prefs: map[string]string{}})
	r := runTUI(t, NewSession(d.sock, nil), Options{})
	r.ready(t)
	layouts := func() [2]string {
		return onLoop(r, func() [2]string {
			return [2]string{r.h.prefs.Value(layoutKey("editor")), r.h.prefs.Value(layoutKey("terminal"))}
		})
	}
	r.command(t, "layout")
	r.s.WaitFor(t, "the editor's Layout", func(sc string) bool { return strings.Contains(sc, "Tiled") && strings.Contains(sc, "Float") })
	r.keys(t, key('f'))
	r.s.WaitFor(t, "the editor's Layout Float", func(string) bool { return layouts() == [2]string{layoutFloat, layoutFloat} })
	r.command(t, "layout terminal")
	r.s.WaitForText(t, "Float")
	r.keys(t, key('t'))
	r.s.WaitForText(t, "Bottom") // Tiled ›, the edges
	r.keys(t, key('b'))
	r.s.WaitFor(t, "the terminal's Layout Tiled", func(string) bool { return layouts() == [2]string{layoutFloat, layoutTiled} })
	r.command(t, "layout nope")
	r.s.WaitFor(t, "the notice naming the windows", func(sc string) bool { // a toast wraps it
		return strings.Contains(sc, "no window nope: the windows are editor,") && strings.Contains(sc, "explorer, terminal, agent, results, todos")
	})
	if got := layouts(); got != [2]string{layoutFloat, layoutTiled} {
		t.Fatalf("the editor's and the terminal's Layout %v after :layout nope; want them unchanged", got)
	}
}

// TestAPanelsEdgeIsChosenOnItsLayout: Layout › Tiled › on the terminal's menu. Chosen on the
// floating terminal, Right tiles it there, the page beside it; Top, Left and Bottom then move its
// tile, each checked as chosen. Its edge set elsewhere (the preference), the menu checks it.
func TestAPanelsEdgeIsChosenOnItsLayout(t *testing.T) {
	_, r := runShellTUI(t, nil)
	r.leader(t, '`')
	r.s.WaitFor(t, "the terminal's keyboard", func(string) bool { return r.focused("terminalView") })
	rows := func() []widget.MenuItemModel {
		return onLoop(r, func() []widget.MenuItemModel { return r.h.terminals.tabRows("") })
	}
	wantLayoutChecked(t, "the floating terminal", rows()[rowAt(rows(), "layout")].Children, false)
	choose := func(edge string) {
		t.Helper()
		row, ok := edgeRow(rows(), edge)
		if !ok {
			t.Fatalf("the terminal's Layout has no Tiled › %s: %v", edge, rowIDs(rows()))
		}
		onLoop(r, func() bool { row.Action.(widget.TerminalTabsMenuAction).Run(""); return true })
	}
	// at is whether the terminal's tile is on edge's side of the page: centres compared
	at := func(edge string) bool {
		term, page := boundsOf(r, "terminal"), boundsOf(r, "editorWindow")
		if term.W == 0 || page.W == 0 {
			return false
		}
		tx, ty, px, py := 2*term.X+term.W, 2*term.Y+term.H, 2*page.X+page.W, 2*page.Y+page.H
		return map[string]bool{"left": tx < px, "right": tx > px, "top": ty < py, "bottom": ty > py}[edge]
	}
	tiledAt := func(edge string) bool {
		return onLoop(r, func() bool {
			return r.h.prefs.Value(layoutKey("terminal")) == layoutTiled && r.h.prefs.Value(tileEdgeKey("terminal")) == edge
		}) && at(edge)
	}
	wantEdgeChecked := func(edge string) {
		t.Helper()
		for _, e := range edges {
			row, _ := edgeRow(rows(), e)
			if row.Checked != (e == edge) {
				t.Fatalf("Tiled › %s checked %v with the edge %s", e, row.Checked, edge)
			}
		}
	}
	for _, edge := range []string{"right", "top", "left", "bottom"} {
		choose(edge)
		r.s.WaitFor(t, "the terminal tiled at the "+edge, func(string) bool { return tiledAt(edge) })
		wantEdgeChecked(edge)
		wantLayoutChecked(t, "the terminal at the "+edge, rows()[rowAt(rows(), "layout")].Children, true)
	}
	onLoop(r, func() bool { core{r.h}.SetPref(tileEdgeKey("terminal"), "top"); return true })
	r.s.WaitFor(t, "the terminal tiled at the top, set elsewhere", func(string) bool { return tiledAt("top") })
	wantEdgeChecked("top")
}

// TestAnEdgeChosenTilesThePanelAsItIsNow (Zen, #16 r1): :layout terminal open on the tiled
// terminal, its Tiled › showing, the store floats it (as another TUI's choice, read back by a
// reload). Right then docks it at the right and tiles it: the Layout as it is when chosen, not as
// it was when the menu was built.
func TestAnEdgeChosenTilesThePanelAsItIsNow(t *testing.T) {
	d, r := runShellTUI(t, nil)
	r.leader(t, '`')
	r.s.WaitFor(t, "the terminal's keyboard", func(string) bool { return r.focused("terminalView") })
	onLoop(r, func() bool {
		core{r.h}.SetPref(layoutKey("terminal"), layoutTiled)
		r.h.focusEditor()
		return true
	})
	r.command(t, "layout terminal")
	r.s.WaitForText(t, "Float")
	r.keys(t, key('t'))
	r.s.WaitForText(t, "Bottom")
	r.s.WaitFor(t, "the terminal's Layout stored Tiled", func(string) bool {
		p, err := d.db.Preferences(context.Background())
		return err == nil && p[layoutKey("terminal")] == layoutTiled
	})
	if err := d.db.SetPreference(context.Background(), layoutKey("terminal"), layoutFloat); err != nil {
		t.Fatal(err)
	}
	onLoop(r, func() bool { r.h.loadPrefs(); return true })
	r.s.WaitFor(t, "the reload floating it", func(string) bool {
		return onLoop(r, func() bool { return r.h.prefs.Value(layoutKey("terminal")) == layoutFloat })
	})
	r.s.WaitForText(t, "Bottom") // Tiled › still open
	r.keys(t, key('r'))
	r.s.WaitFor(t, "Right chosen", func(string) bool {
		return onLoop(r, func() bool { return r.h.prefs.Value(tileEdgeKey("terminal")) == "right" })
	})
	if got := onLoop(r, func() string { return r.h.prefs.Value(layoutKey("terminal")) }); got != layoutTiled {
		t.Fatalf("Tiled › Right on the menu left the terminal %s after a reload floated it", got)
	}
}
