package tui

import "strconv"

// PANEL SIZES — the explorer's and the links' size across their edge and length along it, in
// percent of the Window, kept per edge as the terminal's are (terminal.go), and set by dragging a
// panel's corner grip (ADR 1791213315): tui.explorer.<edge>.size and .length, tui.links.<edge>.size
// and .length. A drag of the terminal writes its own, tui.terminal.<edge>.size and .length.

// sidePanels are the panels this file sizes; the terminal sizes itself.
var sidePanels = []string{"explorer", "links"}

// sidePanelMin is the explorer's and the links' smallest size across their edge, in percent: their
// trees stay readable.
const sidePanelMin = 15

// panelPref is a side panel's size or length preference at an edge.
func panelPref(panel, edge, what string) string { return "tui." + panel + "." + edge + "." + what }

// sideDefault is a side panel's size and length before the user drags it: a third across, and
// 85% along a side or 80% along the top or bottom.
func sideDefault(edge string) (size, length int) { return 30, panelLength(edge) }

// sideGeometry is a side panel's size and length at an edge: the dragged ones, or the defaults.
func (p prefs) sideGeometry(panel, edge string) (size, length int) {
	size, length = sideDefault(edge)
	if v, ok := p.panelGeo[panelPref(panel, edge, "size")]; ok {
		size = v
	}
	if v, ok := p.panelGeo[panelPref(panel, edge, "length")]; ok {
		length = v
	}
	return size, length
}

// readPanelPrefs reads the side panels' sizes, keeping the defaults for anything out of bounds.
func readPanelPrefs(p *prefs, m map[string]any) {
	for _, panel := range sidePanels {
		for _, edge := range edges {
			if n, ok := pct(m[panelPref(panel, edge, "size")], sidePanelMin, 90); ok {
				p.panelGeo = withEntry(p.panelGeo, panelPref(panel, edge, "size"), n)
			}
			if n, ok := pct(m[panelPref(panel, edge, "length")], 20, 100); ok {
				p.panelGeo = withEntry(p.panelGeo, panelPref(panel, edge, "length"), n)
			}
		}
	}
}

// panelState is what the document reads of the side panels' sizes, at the edges they open from.
func panelState(p prefs) map[string]any {
	es, el := p.sideGeometry("explorer", p.explorerEdge)
	ls, ll := p.sideGeometry("links", p.linkEdge)
	return map[string]any{
		"App.explorerSize": es, "App.explorerLength": el,
		"App.linksSize": ls, "App.linksLength": ll,
	}
}

// panelResized is a panel's grip released: its size and length kept for the edge it opens from
// now, the terminal's as the Preferences dialog keeps them.
func (h *Host) panelResized(panel string, size, length int) {
	switch panel {
	case "terminal":
		edge := h.prefs.termEdge
		h.setPref(prefTermPrefix+edge+prefTermSize, strconv.Itoa(size), func(p *prefs) {
			p.termSize = withEntry(p.termSize, edge, size)
		})
		h.setPref(prefTermPrefix+edge+prefTermLength, strconv.Itoa(length), func(p *prefs) {
			p.termLength = withEntry(p.termLength, edge, length)
		})
	case "explorer", "links":
		edge := h.prefs.explorerEdge
		if panel == "links" {
			edge = h.prefs.linkEdge
		}
		sk, lk := panelPref(panel, edge, "size"), panelPref(panel, edge, "length")
		h.setPref(sk, strconv.Itoa(size), func(p *prefs) { p.panelGeo = withEntry(p.panelGeo, sk, size) })
		h.setPref(lk, strconv.Itoa(length), func(p *prefs) { p.panelGeo = withEntry(p.panelGeo, lk, length) })
	}
}
