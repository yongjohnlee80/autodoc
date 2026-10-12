package tui

import (
	"slices"
	"strconv"
)

func (p prefs) geometrySpecs() []PanelSpec {
	if p.panelDefs != nil {
		return p.panelDefs
	}
	return defaultPanelSpecs()
}

// PANEL SIZES — every panel's size across its edge and length along it, in percent of the Window,
// kept per edge and place as one preference shape, tui.<panel>.<place>.size and .length, set by
// Preferences. Floating rectangles from move/resize gestures are kept separately
// in panelFloat (panelfloat.go). Each panel's defaults and
// bounds are its spec's (panels.go); the HTML pane sizes itself and keeps none.

// sidePanelMin is the explorer's and the links' smallest size across their edge, in percent: their
// trees stay readable.
const sidePanelMin = 15

// panelPref is a panel's size or length preference at a place.
func panelPref(panel, edge, what string) string { return "tui." + panel + "." + edge + "." + what }

// sideDefault is the explorer's and the links' size and length before the user drags them: a
// third across, and 85% along a side or 80% along the top or bottom.
func sideDefault(edge string) (size, length int) { return 30, panelLength(edge) }

// panelGeometry is a panel's size and length at a place: the kept ones, or its spec's defaults.
func (p prefs) panelGeometry(id, edge string) (size, length int) {
	s := findPanel(p.geometrySpecs(), id)
	if s == nil || s.Default == nil {
		return 0, 0
	}
	size, length = s.Default(edge)
	if v, ok := p.panelGeo[panelPref(id, edge, "size")]; ok {
		size = v
	}
	if v, ok := p.panelGeo[panelPref(id, edge, "length")]; ok {
		length = v
	}
	return size, length
}

// readPanelPrefs reads every sized panel's geometry, keeping the defaults for anything out of
// bounds. The spec names the places and the bounds, so a panel cannot be missed here.
func readPanelPrefs(p *prefs, m map[string]any) {
	for _, s := range p.geometrySpecs() {
		if s.Places == nil {
			continue
		}
		for _, edge := range s.Places {
			if n, ok := pct(m[panelPref(s.ID, edge, "size")], s.Size[0], s.Size[1]); ok {
				p.panelGeo = withEntry(p.panelGeo, panelPref(s.ID, edge, "size"), n)
			}
			if n, ok := pct(m[panelPref(s.ID, edge, "length")], s.Length[0], s.Length[1]); ok {
				p.panelGeo = withEntry(p.panelGeo, panelPref(s.ID, edge, "length"), n)
			}
		}
	}
}

// keepPanelGeo writes a panel's size or length at the place it opens from now, one path for
// Preferences and a finished drag.
func (h *Host) keepPanelGeo(panel, what string, n int) {
	s := h.panelSpec(panel)
	if s == nil || len(s.Places) == 0 {
		return
	}
	var bounds [2]int
	switch what {
	case "size":
		bounds = s.Size
	case "length":
		bounds = s.Length
	default:
		return
	}
	if !inPercentRange(n, bounds) {
		return
	}
	edge := s.Edge(h.prefs)
	if !slices.Contains(s.Places, edge) {
		return
	}
	k := panelPref(panel, edge, what)
	h.setPref(k, strconv.Itoa(n), func(p *prefs) { p.panelGeo = withEntry(p.panelGeo, k, n) })
}

// panelState is what the document reads of the side panels' sizes, at the edges they open from.
func panelState(p prefs) map[string]any {
	es, el := p.panelGeometry("explorer", p.explorerEdge)
	ls, ll := p.panelGeometry("links", p.linkEdge)
	return map[string]any{
		"App.explorerSize": es, "App.explorerLength": el,
		"App.linksSize": ls, "App.linksLength": ll,
	}
}

// panelResized is a panel's grip released: its size and length kept for the place it opens from
// now, the one way for every panel.
func (h *Host) panelResized(panel string, size, length int) {
	if s := h.panelSpec(panel); s == nil || len(s.Places) == 0 ||
		!inPercentRange(size, s.Size) || !inPercentRange(length, s.Length) {
		return // a panel that sizes itself keeps no geometry
	}
	h.keepPanelGeo(panel, "size", size)
	h.keepPanelGeo(panel, "length", length)
}
