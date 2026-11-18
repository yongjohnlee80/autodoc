package tui

import (
	"slices"
	"strconv"
	"strings"

	"github.com/yongjohnlee80/autodoc/tui/feature"
)

// CARDS IN THE TILE MANAGER: the host's side. A card's panel is a window of the tile manager as the
// static panels are (tilelayout.go). The plugins declare each card's panel inside the TileManager
// (pluginpanels.go) and read its tiling through pluginHost's CardTiling; the tiling itself, its
// windows and its preferences, is the host's, as every tile window's is.

// cardTileEdge is the edge a card's tile docks at before the user picks one: its placement's side.
// The centre, which the page holds as the tile manager's one fill member, takes the right, as the
// agents' tile does. Its docking edge (cardEdge) is untouched.
func cardTileEdge(place string) string {
	if e := cardEdge(place); e != "center" {
		return e
	}
	return "right"
}

// cardTileSize is a card's tile size before the user drags its border, in percent across its edge.
const cardTileSize = 35

// cardTileWindows are the cards' windows of the tile manager, in the card set's order: each floats
// by default, its tile at cardTileEdge of its placement, cardTileSize across.
func (c core) cardTileWindows() []tileWindow {
	pl := c.h.plugins
	var out []tileWindow
	for _, card := range pl.fixCards() {
		id := cardPanelID(card.name)
		out = append(out, tileWindow{id: id, node: id, edge: cardTileEdge(pl.cardEdgeIn(c.Prefs(), card.name)), size: cardTileSize})
	}
	return out
}

// CardTiling is the card's tiling as the preferences have it now, its panel docked at edge
// (pluginHost's): its Layout tiled or not, its tile's edge and size. A value outside its choices or
// bounds, or none, is the default.
func (c core) CardTiling(name, edge string) (tiled bool, tileEdge string, size int) {
	id := cardPanelID(name)
	p := c.Prefs()
	tiled = p.Value(layoutKey(id)) == layoutTiled
	tileEdge = p.Value(tileEdgeKey(id))
	if !slices.Contains(feature.Edges, tileEdge) {
		tileEdge = cardTileEdge(edge)
	}
	size = cardTileSize
	if n, err := strconv.Atoi(p.Value(tileSizeKey(id))); err == nil && n >= minTileSize && n <= maxTileSize {
		size = n
	}
	return tiled, tileEdge, size
}

// readCardTiles reads each card's tiling, kept under its panel's id as its geometry is: its Layout,
// its tile's edge and size, as stored. CardTiling takes a value outside its choices or bounds as
// the default, so a bad one stored is no error.
func readCardTiles(p *prefs, m map[string]any) {
	for _, spec := range p.geometrySpecs() {
		if !strings.HasPrefix(spec.ID, cardPanelPrefix) {
			continue
		}
		for _, key := range []string{layoutKey(spec.ID), tileEdgeKey(spec.ID), tileSizeKey(spec.ID)} {
			if v, ok := m[key].(string); ok && v != "" {
				p.SetValue(key, v)
			}
		}
	}
}
