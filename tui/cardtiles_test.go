package tui

import (
	"slices"
	"testing"

	tuicore "github.com/yongjohnlee80/golib/tui"
	widgetcore "github.com/yongjohnlee80/golib/tui/widget/core"
	"github.com/yongjohnlee80/golib/tui/widget/editor"
)

// CARDS IN THE TILE MANAGER (pluginpanels.go, tilelayout.go) — a card's panel is a window of the
// tile manager as the static panels are: it floats until the user tiles it from its right-click
// menu's Layout › or :layout, sits over its tile at cardTileEdge until floated again, and keeps its
// layout, tile edge and tile size under its panel's id, across a restart and a reload of the card
// set.

// cardMenuRows are the card's right-click menu's rows, as it would open now.
func (r *running) cardMenuRows(name string) []widgetcore.MenuItemModel {
	return onLoop(r, func() []widgetcore.MenuItemModel {
		c := r.h.plugins.cards[name]
		if c == nil || c.view == nil {
			return nil
		}
		return c.view.menuRows()
	})
}

// runCardMenuRow runs row, a row of a card's right-click menu (or of its Layout ›), on the loop.
func runCardMenuRow(r *running, row widgetcore.MenuItemModel) {
	onLoop(r, func() bool { row.Action.(editor.CoreMenuAction).Run(nil); return true })
}

// cardTiledAt reports whether the card's panel is over its tile on edge's side of the page: tiled and
// governed, its centre beyond the page's on that side.
func cardTiledAt(r *running, name, edge string) bool {
	id := cardPanelID(name)
	if wants, governed := tileOf(r, id); !wants || !governed {
		return false
	}
	card, page := boundsOf(r, id), boundsOf(r, "editorWindow")
	if card.W == 0 || page.W == 0 {
		return false
	}
	cx, cy, px, py := 2*card.X+card.W, 2*card.Y+card.H, 2*page.X+page.W, 2*page.Y+page.H
	return map[string]bool{"left": cx < px, "right": cx > px, "top": cy < py, "bottom": cy > py}[edge]
}

// A card floats until its menu's Layout › Tiled › Right tiles it at the right, the page beside it,
// 35% across; Float then floats it again. Its menu holds Layout › right above Close (Esc), checked
// as its Layout is. The mutants, named here: the card marker left outside the tile manager (the
// card is no member); syncCard not setting the card's Tiled (the choice never reaches the panel).
func runACardTilesAtItsEdgeAndFloatsAgain(t *testing.T, gui bool) {
	dir := t.TempDir()
	installPlugin2(t, dir, "card", "dialog", "echo", rightCard)
	_, r, _ := liveCardTUI(t, dir, nil, gui)
	r.openPlugin("card")
	r.s.WaitFor(t, "the card's panel", func(string) bool { return r.panelShown("card") })
	id := cardPanelID("card")
	if wants, governed := tileOf(r, id); wants || governed {
		t.Fatalf("a card opened tiled (wanted %v, governed %v); want it floating until the user tiles it", wants, governed)
	}
	rows := r.cardMenuRows("card")
	if l, c := rowAt(rows, "layout"), rowAt(rows, "plugin.card.close"); l < 0 || c != l+1 {
		t.Fatalf("the card's menu: Layout at %d, Close at %d; want Layout right above Close: %v", l, c, rowIDs(rows))
	}
	wantLayoutChecked(t, "the floating card", rows[rowAt(rows, "layout")].Children, false)
	right, ok := edgeRow(rows, "right")
	if !ok {
		t.Fatalf("the card's Layout has no Tiled › Right: %v", rowIDs(rows))
	}
	runCardMenuRow(r, right)
	r.s.WaitFor(t, "the card tiled at the right", func(string) bool { return cardTiledAt(r, "card", "right") })
	if b := boundsOf(r, id); b.W != cardTileSize {
		t.Fatalf("the card's tile at %+v, want %d columns across, its default", b, cardTileSize)
	}
	rows = r.cardMenuRows("card")
	wantLayoutChecked(t, "the tiled card", rows[rowAt(rows, "layout")].Children, true)
	float, ok := menuRow(rows[rowAt(rows, "layout")].Children, "layout.float")
	if !ok {
		t.Fatalf("the card's Layout has no Float: %v", rowIDs(rows[rowAt(rows, "layout")].Children))
	}
	runCardMenuRow(r, float)
	r.s.WaitFor(t, "the card floating again", func(string) bool { w, g := tileOf(r, id); return !w && !g })
}

func TestACardTilesAtItsEdgeAndFloatsAgain(t *testing.T) {
	t.Parallel()
	runACardTilesAtItsEdgeAndFloatsAgain(t, false)
}

func TestACardTilesAtItsEdgeAndFloatsAgainGUI(t *testing.T) {
	t.Parallel()
	runACardTilesAtItsEdgeAndFloatsAgain(t, true)
}

// A card's tiling is kept under its panel's id, as its geometry is: tiled at the left and its
// shared border dragged ten cells right, it is 45% across; another card added while AutoDoc runs
// (the card set reloaded) leaves it so; and a TUI started on the store tiles it there again. The
// mutant, named here: tileChanged keeping the static windows' drags alone (the card's tile size is
// never kept).
func TestACardsTilingSurvivesARestartAndAReloadOfTheCardSet(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	installPlugin2(t, dir, "card", "dialog", "echo", rightCard)
	d, r, _ := liveCardTUI(t, dir, nil, false)
	r.openPlugin("card")
	r.s.WaitFor(t, "the card's panel", func(string) bool { return r.panelShown("card") })
	id := cardPanelID("card")
	left, ok := edgeRow(r.cardMenuRows("card"), "left")
	if !ok {
		t.Fatal("the card's Layout has no Tiled › Left")
	}
	runCardMenuRow(r, left)
	r.s.WaitFor(t, "the card tiled at the left", func(string) bool { return cardTiledAt(r, "card", "left") })
	b := boundsOf(r, id)
	border := b.X + b.W - 1
	r.keys(t, tuicore.MouseEvent{Kind: tuicore.MousePress, Button: tuicore.MouseRight, Mods: tuicore.ModAlt, X: border, Y: 10},
		tuicore.MouseEvent{Kind: tuicore.MouseMotion, Button: tuicore.MouseRight, Mods: tuicore.ModAlt, X: border + 10, Y: 10},
		tuicore.MouseEvent{Kind: tuicore.MouseRelease, Button: tuicore.MouseRight, Mods: tuicore.ModAlt, X: border + 10, Y: 10})
	r.s.WaitFor(t, "the card's tile size kept", func(string) bool {
		return onLoop(r, func() string { return r.h.prefs.Value(tileSizeKey(id)) }) == "45"
	})
	storedPref(t, d, r, layoutKey(id), layoutTiled)
	storedPref(t, d, r, tileEdgeKey(id), "left")
	storedPref(t, d, r, tileSizeKey(id), "45")

	installPlugin2(t, dir, "other", "dialog", "echo", leftCard)
	r.readFolder()
	r.s.WaitFor(t, "the added card's panel", func(string) bool { return r.hasCardPanel("other") })
	r.s.WaitFor(t, "the first card still tiled at the left, 45 across", func(string) bool {
		return cardTiledAt(r, "card", "left") && boundsOf(r, id).W == 45
	})

	again, _ := pluginTUIOn(t, d, dir, "")
	again.openPlugin("card")
	again.s.WaitFor(t, "the card tiled at the left after a restart, 45 across", func(string) bool {
		return cardTiledAt(again, "card", "left") && boundsOf(again, id).W == 45
	})
}

// A card at the centre, at an edge's corner, or with no placement builds with the page present and
// the card floating; tiled with no edge chosen, each tiles at the right (the page holds the tile
// manager's one centre), and floats back. :layout names the card's window. The mutant, named here:
// cardTileEdge returning the centre (a second centre member, which the tile manager refuses).
func TestACardAtTheCentreOrACornerTilesAtTheRight(t *testing.T) {
	t.Parallel()
	for _, c := range []struct{ name, placements string }{
		{"omitted", ""},
		{"center", "placements = [\"center\"]\n"},
		{"top-right", "placements = [\"top-right\"]\n"},
	} {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			dir := t.TempDir()
			installPlugin2(t, dir, "card", "dialog", "echo", "[dialog]\nmodal = false\nwidth = 36\nheight = 8\n"+c.placements)
			_, r, _ := liveCardTUI(t, dir, nil, false)
			if !r.hasCardPanel("card") {
				t.Fatal("the card has no panel")
			}
			if page := boundsOf(r, "editorWindow"); page.W == 0 {
				t.Fatal("the page is not there")
			}
			id := cardPanelID("card")
			if !slices.Contains(onLoop(r, func() []string { return r.h.tileWindowIDs() }), id) {
				t.Fatalf(":layout's windows %v don't name the card's, %s", onLoop(r, func() []string { return r.h.tileWindowIDs() }), id)
			}
			r.openPlugin("card")
			r.s.WaitFor(t, "the card's panel", func(string) bool { return r.panelShown("card") })
			if w, g := tileOf(r, id); w || g {
				t.Fatalf("the card opened tiled (wanted %v, governed %v)", w, g)
			}
			onLoop(r, func() bool { r.h.tiles.setLayout(id, true); return true })
			r.s.WaitFor(t, "the card tiled at the right", func(string) bool { return cardTiledAt(r, "card", "right") })
			onLoop(r, func() bool { r.h.tiles.setLayout(id, false); return true })
			r.s.WaitFor(t, "the card floating again", func(string) bool { w, g := tileOf(r, id); return !w && !g })
		})
	}
}
