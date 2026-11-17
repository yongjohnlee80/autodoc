package tui

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	tuicore "github.com/yongjohnlee80/golib/tui"
)

// CARDS IN THE TILE MANAGER, WHILE THE CARD SET CHANGES: a card added or removed while AutoDoc runs
// reloads the document, and golib's TileManager splices the card panels in and out (golib v0.8.62)
// rather than building every panel again.

// tileCard tiles the open card name at edge from its menu's Layout › Tiled, and waits for it there.
func tileCard(t *testing.T, r *running, name, edge string) {
	t.Helper()
	row, ok := edgeRow(r.cardMenuRows(name), edge)
	if !ok {
		t.Fatalf("%s's Layout has no Tiled › %s", name, edge)
	}
	runCardMenuRow(r, row)
	r.s.WaitFor(t, name+" tiled at the "+edge, func(string) bool { return cardTiledAt(r, name, edge) })
}

// borderDrag Alt+Right drags the right border of the card panel id, tiled at the left, dx cells.
func borderDrag(t *testing.T, r *running, id string, dx int) {
	t.Helper()
	b := boundsOf(r, id)
	border := b.X + b.W - 1
	r.keys(t, tuicore.MouseEvent{Kind: tuicore.MousePress, Button: tuicore.MouseRight, Mods: tuicore.ModAlt, X: border, Y: 10},
		tuicore.MouseEvent{Kind: tuicore.MouseMotion, Button: tuicore.MouseRight, Mods: tuicore.ModAlt, X: border + dx, Y: 10},
		tuicore.MouseEvent{Kind: tuicore.MouseRelease, Button: tuicore.MouseRight, Mods: tuicore.ModAlt, X: border + dx, Y: 10})
}

// resizedByDrag drags the card's border until a drag lands and widens it past was: a card-set change
// landing mid-drag cancels the drag by design (its layout changed under it), so a drag is tried
// again, up to three times.
func resizedByDrag(t *testing.T, r *running, id string, was int) {
	t.Helper()
	for range 3 {
		borderDrag(t, r, id, 6)
		deadline := time.Now().Add(2 * time.Second)
		for time.Now().Before(deadline) {
			if boundsOf(r, id).W > was {
				return
			}
			time.Sleep(20 * time.Millisecond)
		}
	}
	t.Fatalf("no drag of %s's border widened it past %d", id, was)
}

// A card added while AutoDoc runs is spliced in: the card already there keeps its panel and its
// view, the same objects, over its tile; and the added card tiles from its own menu, its live tile
// bindings reaching it though it was not in the first build. The mutants, named here: the
// TileManager rebuilt on a reload (the first card's view is a new one), and the added card's tile
// bindings not reaching it (it never tiles).
func TestACardAddedWhileRunningIsSplicedInAndTilesFromItsMenu(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	installPlugin2(t, dir, "card", "dialog", "echo", rightCard)
	d, r, _ := liveCardTUI(t, dir, nil, false)
	r.openPlugin("card")
	r.s.WaitFor(t, "the card's panel", func(string) bool { return r.panelShown("card") })
	tileCard(t, r, "card", "left")
	id := cardPanelID("card")
	// the card set's reload reads the preferences again: the choice must be stored first
	storedPref(t, d, r, layoutKey(id), layoutTiled)
	view := onLoop(r, func() *pluginView { return r.h.plugins.cards["card"].view })
	drawer := onLoop(r, func() tuicore.Component { c, _ := r.h.p.Find(id); return c })

	installPlugin2(t, dir, "other", "dialog", "echo", leftCard)
	r.readFolder()
	r.s.WaitFor(t, "the added card's panel", func(string) bool { return r.hasCardPanel("other") })
	if now := onLoop(r, func() *pluginView { return r.h.plugins.cards["card"].view }); now != view {
		t.Fatal("the first card's view is a new one: the reload rebuilt its panel")
	}
	if now := onLoop(r, func() tuicore.Component { c, _ := r.h.p.Find(id); return c }); now != drawer {
		t.Fatal("the first card's panel is a new node: the reload rebuilt it")
	}
	r.s.WaitFor(t, "the first card over its tile once the added card is laid out", func(string) bool {
		return cardTiledAt(r, "card", "left")
	})

	r.openPlugin("other")
	r.s.WaitFor(t, "the added card's panel open", func(string) bool { return r.panelShown("other") })
	tileCard(t, r, "other", "right")
}

// A tiled card removed while AutoDoc runs leaves the tile manager: the other card keeps its tile,
// edge and size, the page takes the removed card's cells, and a drag of the kept card's border
// still resizes it, so no gesture or capture was left behind. The mutant, named here: a removed
// member kept in the tile manager (the page never widens).
func TestRemovingATiledCardLeavesTheOtherOnItsTile(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	installPlugin2(t, dir, "card", "dialog", "echo", rightCard)
	installPlugin2(t, dir, "other", "dialog", "echo", leftCard)
	d, r, _ := liveCardTUI(t, dir, nil, false)
	for _, c := range []struct{ name, edge string }{{"card", "left"}, {"other", "right"}} {
		r.openPlugin(c.name)
		r.s.WaitFor(t, c.name+"'s panel", func(string) bool { return r.panelShown(c.name) })
		tileCard(t, r, c.name, c.edge)
		storedPref(t, d, r, layoutKey(cardPanelID(c.name)), layoutTiled) // a reload reads them again
	}
	id := cardPanelID("card")
	before, page := boundsOf(r, id), boundsOf(r, "editorWindow")

	if err := os.RemoveAll(filepath.Join(dir, "other")); err != nil {
		t.Fatal(err)
	}
	r.readFolder()
	r.s.WaitFor(t, "the removed card's panel and run gone", func(string) bool {
		return onLoop(r, func() bool { return r.h.panelSpec(cardPanelID("other")) == nil && r.h.plugins.running["other"] == nil })
	})
	r.s.WaitFor(t, "the page widened, the first card on its tile as it was", func(string) bool {
		return cardTiledAt(r, "card", "left") && boundsOf(r, id) == before && boundsOf(r, "editorWindow").W > page.W
	})
	resizedByDrag(t, r, id, before.W)
}

// A card whose folder is removed while AutoDoc runs, then added back, comes back over its tile: the
// card set's reload reads the preferences again (loadPrefs), its stored tiling with its geometry, so
// the tiling the user chose survives the card's absence. The mutant, named here: readCardTiles
// not reading a card's tiling (it comes back floating).
func TestACardRemovedAndAddedBackComesBackOnItsTile(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	installPlugin2(t, dir, "card", "dialog", "echo", rightCard)
	d, r, _ := liveCardTUI(t, dir, nil, false)
	r.openPlugin("card")
	r.s.WaitFor(t, "the card's panel", func(string) bool { return r.panelShown("card") })
	tileCard(t, r, "card", "left")
	storedPref(t, d, r, layoutKey(cardPanelID("card")), layoutTiled) // a reload reads it again
	if err := os.RemoveAll(filepath.Join(dir, "card")); err != nil {
		t.Fatal(err)
	}
	r.readFolder()
	r.s.WaitFor(t, "the removed card's panel gone", func(string) bool {
		return onLoop(r, func() bool { return r.h.panelSpec(cardPanelID("card")) == nil })
	})
	installPlugin2(t, dir, "card", "dialog", "echo", rightCard)
	r.readFolder()
	r.s.WaitFor(t, "the card's panel back", func(string) bool { return r.hasCardPanel("card") })
	r.openPlugin("card")
	r.s.WaitFor(t, "the card back over its tile at the left", func(string) bool { return cardTiledAt(r, "card", "left") })
}
