package tui

import "github.com/yongjohnlee80/golib/tui/widget"

// THE PANELS — the explorer and the links, drawers over the page: toggled (SPC e, SPC l, the Go
// menu), each from the edge its preference names, the page beneath never moving. Ctrl+h/j/k/l
// move between the page and the panels open at those edges, in Normal mode only: in Insert mode
// they are the editor's.

// The panels, by the document's ids: the drawer, and what in it takes the keyboard.
var panels = map[string]string{"explorer": "explorerTree", "links": "linksList", "terminal": "terminalView", "agent": "agentView"}

// togglePanel opens or closes a panel; open, it has the keyboard.
func (h *Host) togglePanel(name string) {
	if h.panelOpen[name] {
		h.keep(h.p.Call(name, "close"))
		return
	}
	if name == "links" {
		h.relationsOfOpenFile()
	}
	h.keep(h.p.Call(name, "open"))
	h.keep(h.p.Call(panels[name], "forceActiveFocus"))
}

// panelOpened and panelClosed follow the drawers, however they were opened or closed (Escape
// inside one closes it).
func (h *Host) panelOpened(name string) {
	h.panelOpen[name] = true
	h.showFind() // a drawer at the right edge hides "finding …"
	if name == "terminal" {
		h.set("App.terminalShown", true)
	}
	if name == "agent" {
		h.set("App.agentShown", true)
	}
}

func (h *Host) panelClosed(name string) {
	h.panelOpen[name] = false
	h.showFind()
	if name == "terminal" {
		h.set("App.terminalShown", false)
		h.terminalClosed()
		return
	}
	if name == "agent" {
		h.set("App.agentShown", false)
	}
	h.keep(h.p.Call("editor", "forceActiveFocus"))
}

// dirEdge is the edge a Ctrl+h/j/k/l move heads for.
var dirEdge = map[string]string{"h": "left", "l": "right", "k": "top", "j": "bottom"}

// opposite is the edge across from an edge: the way back to the page.
var opposite = map[string]string{"left": "right", "right": "left", "top": "bottom", "bottom": "top"}

// movePane is Ctrl+h/j/k/l: from the page, in Normal mode, to the open panel at that edge; from a
// panel, back to the page the other way.
func (h *Host) movePane(dir string) {
	edge, ok := dirEdge[dir]
	if !ok {
		return
	}
	app := h.p.App()
	if c, ok := h.p.Find("editor"); ok && app.FocusWithin(c) {
		if h.core.Mode() != widget.ModeNormal {
			return // Insert mode: the editor's keys, not a move
		}
		for name, at := range h.panelEdges() {
			if at == edge && h.panelOpen[name] {
				h.keep(h.p.Call(panels[name], "forceActiveFocus"))
				return
			}
		}
		return
	}
	for name, at := range h.panelEdges() {
		if c, ok := h.p.Find(panels[name]); ok && app.FocusWithin(c) && edge == opposite[at] {
			h.keep(h.p.Call("editor", "forceActiveFocus"))
			return
		}
	}
}

func (h *Host) panelEdges() map[string]string {
	return map[string]string{"explorer": h.prefs.explorerEdge, "links": h.prefs.linkEdge, "terminal": h.prefs.termEdge}
}
