package tui

import (
	"testing"

	tuicore "github.com/yongjohnlee80/golib/tui"
)

// A card added while the user drags another card's border cancels the drag (its layout changed
// under it): the release that follows changes nothing, and the next drag resizes. The mutant,
// named here: a member added mid-drag leaving the gesture to commit (the size moves).
func TestACardAddedMidDragCancelsTheDrag(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	installPlugin2(t, dir, "card", "dialog", "echo", rightCard)
	d, r, _ := liveCardTUI(t, dir, nil, false)
	r.openPlugin("card")
	r.s.WaitFor(t, "the card's panel", func(string) bool { return r.panelShown("card") })
	tileCard(t, r, "card", "left")
	id := cardPanelID("card")
	storedPref(t, d, r, layoutKey(id), layoutTiled) // a reload reads it again
	before := boundsOf(r, id)
	border := before.X + before.W - 1
	r.keys(t, tuicore.MouseEvent{Kind: tuicore.MousePress, Button: tuicore.MouseRight, Mods: tuicore.ModAlt, X: border, Y: 10},
		tuicore.MouseEvent{Kind: tuicore.MouseMotion, Button: tuicore.MouseRight, Mods: tuicore.ModAlt, X: border + 3, Y: 10})
	installPlugin2(t, dir, "other", "dialog", "echo", leftCard)
	r.readFolder()
	r.s.WaitFor(t, "the added card's panel", func(string) bool { return r.hasCardPanel("other") })
	r.keys(t, tuicore.MouseEvent{Kind: tuicore.MouseMotion, Button: tuicore.MouseRight, Mods: tuicore.ModAlt, X: border + 10, Y: 10},
		tuicore.MouseEvent{Kind: tuicore.MouseRelease, Button: tuicore.MouseRight, Mods: tuicore.ModAlt, X: border + 10, Y: 10})
	r.s.WaitFor(t, "the card at its size before the drag", func(string) bool { return boundsOf(r, id) == before })
	resizedByDrag(t, r, id, before.W)
}

// A protocol-12 card maximized over its tile, then removed while AutoDoc runs, ends the maximize:
// the other card is back over its tile and the page shows again. Its plugin is stopped first here,
// as on Windows it must be. The mutant, named here: a removed member kept as the maximized tile
// (nothing else is shown).
func TestRemovingAMaximizedTiledCardEndsTheMaximize(t *testing.T) {
	removingAMaximizedTiledCard(t, true)
}

// The same, its folder removed while its plugin runs, as a user can on Linux and macOS. Skipped
// where the OS refuses to remove a running program's folder (Windows).
func TestRemovingARunningMaximizedTiledCardEndsTheMaximize(t *testing.T) {
	removingAMaximizedTiledCard(t, false)
}

func removingAMaximizedTiledCard(t *testing.T, stopped bool) {
	t.Parallel()
	dir := t.TempDir()
	installCard12(t, dir, "card", "echo", `"maximize"`)
	installCard12(t, dir, "other", "echo", `"maximize"`)
	d, r, _ := liveCardTUI(t, dir, nil, false)
	for _, c := range []struct{ name, edge string }{{"card", "left"}, {"other", "right"}} {
		r.openPlugin(c.name)
		r.s.WaitFor(t, c.name+"'s panel", func(string) bool { return r.panelShown(c.name) })
		tileCard(t, r, c.name, c.edge)
		storedPref(t, d, r, layoutKey(cardPanelID(c.name)), layoutTiled) // a reload reads them again
	}
	other := cardPanelID("other")
	before := boundsOf(r, other)
	r.clickControl(t, "Other", '+')
	r.s.WaitFor(t, "the other card maximized", func(string) bool { return boundsOf(r, other).W > before.W })
	removeCard(t, r, dir, "other", stopped)
	r.readFolder()
	r.s.WaitFor(t, "the removed card's panel gone", func(string) bool {
		return onLoop(r, func() bool { return r.h.panelSpec(other) == nil })
	})
	r.s.WaitFor(t, "the first card over its tile again, the page shown", func(string) bool {
		return cardTiledAt(r, "card", "left") && boundsOf(r, "editorWindow").W > 0
	})
}
