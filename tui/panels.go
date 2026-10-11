package tui

import "github.com/yongjohnlee80/golib/tui/widget"

// THE PANELS — the explorer and the links, drawers over the page: toggled (SPC e, SPC l, the Go
// menu), each from the edge its preference names, the page beneath never moving. Ctrl+h/j/k/l
// move between the page and the panels open at those edges, in Normal mode only: in Insert mode
// they are the editor's.

// PanelSpec is one drawer over the page: everything the host's panel code asks of it, in one place,
// so a list of panels cannot disagree with another.
type PanelSpec struct {
	ID       string               // the Drawer's id in the document
	View     string               // what in it takes the keyboard
	Edge     func(p prefs) string // the edge it opens from now; "center" for none
	Places   []string             // the edges whose dragged size and length prefs keep; nil: it sizes itself
	Keyboard bool                 // the terminal hands the keyboard back to it
}

// panelSpecs are the panels, in the order the host walks them. Every one floats (panelfloat.go).
var panelSpecs = []PanelSpec{
	{ID: "explorer", View: "explorerTree", Edge: func(p prefs) string { return p.explorerEdge }, Places: edges, Keyboard: true},
	{ID: "links", View: "linksList", Edge: func(p prefs) string { return p.linkEdge }, Places: edges, Keyboard: true},
	{ID: "terminal", View: "terminalView", Edge: func(p prefs) string { return p.termEdge }},
	{ID: "agent", View: "agentView", Edge: func(p prefs) string { return p.agentEdge }, Places: agentEdges},
	// the HTML preview's drawer is at the right edge (main.qml)
	{ID: "htmlPane", View: "htmlView", Edge: func(prefs) string { return "right" }},
}

// panelIDs are the panels' ids.
func panelIDs() []string {
	out := make([]string, len(panelSpecs))
	for i, s := range panelSpecs {
		out[i] = s.ID
	}
	return out
}

// panelView is what in the panel id takes the keyboard; "" for no such panel.
func panelView(id string) string {
	for _, s := range panelSpecs {
		if s.ID == id {
			return s.View
		}
	}
	return ""
}

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
	h.keep(h.p.Call(panelView(name), "forceActiveFocus"))
}

// panelOpened and panelClosed follow the drawers, however they were opened or closed (Escape
// inside one closes it).
func (h *Host) panelOpened(name string) {
	h.panelOpen[name] = true
	h.lastPanel = name
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
				h.keep(h.p.Call(panelView(name), "forceActiveFocus"))
				return
			}
		}
		return
	}
	for name, at := range h.panelEdges() {
		if c, ok := h.p.Find(panelView(name)); ok && app.FocusWithin(c) && edge == opposite[at] {
			h.keep(h.p.Call("editor", "forceActiveFocus"))
			return
		}
	}
}

// panelEdges are the edges each panel opens from now.
func (h *Host) panelEdges() map[string]string {
	out := make(map[string]string, len(panelSpecs))
	for _, s := range panelSpecs {
		out[s.ID] = s.Edge(h.prefs)
	}
	return out
}
