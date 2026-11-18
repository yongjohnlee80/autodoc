package tui

import (
	"slices"

	tuidecl "github.com/yongjohnlee80/golib/tui/decl"
)

// A CARD'S WINDOW (protocol 12): the host's side. A card's manifest lists the icons its frame shows
// ([window] controls); the plugins write them into the card's panel (pluginpanels.go) and act on
// them (cardWindow). What the host keeps is here: the keys a protocol-12 card leaves to the Window,
// and the panels Maximize reaches, the cards that list maximize among them.

// CardHostKeys are the keys a protocol-12 card declines, so the Window's Shortcuts run (pluginHost's):
// F6 and Shift+F6 always, maximize's when it lists maximize, as the map binds them now.
func (c core) CardHostKeys(maximize bool) tuidecl.Keys { return c.h.keys.CardHostKeys(maximize) }

// maximizablePanels are the panels Maximize reaches: the terminal's, the agents', and each card that
// lists maximize.
func (h *Host) maximizablePanels() []string {
	return append(slices.Clip(maximizable), h.plugins.maximizableCards()...)
}
