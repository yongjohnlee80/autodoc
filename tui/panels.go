package tui

import "github.com/yongjohnlee80/golib/tui/widget"

// THE PANELS — the explorer and the links, drawers over the page: toggled (SPC e, SPC l, the Go
// menu), each from the edge its preference names, the page beneath never moving. Ctrl+h/j/k/l
// move between the page and the panels open at those edges, in Normal mode only: in Insert mode
// they are the editor's.

// PanelSpec is one drawer over the page: everything the host's panel code asks of it, in one place,
// so a list of panels cannot disagree with another.
type PanelSpec struct {
	ID   string // the Drawer's id in the document
	View string // what in it takes the keyboard
	// Edge is the edge it opens from now; "center" for none.
	Edge func(p prefs) string
	// Places are the edges whose dragged size and length preferences keep; nil: it sizes itself.
	Places []string
	// Size and Length are the percent bounds a kept size or length stays within, and Default is
	// the geometry at a place before the user sets one. A panel with Places has both.
	Size, Length [2]int
	Default      func(edge string) (size, length int)
	// Keyboard: the terminal hands the keyboard back to it.
	Keyboard bool
	// Open and Close are the panel's own way of opening and closing, where it has one; nil, and
	// the drawer opens and its view takes the keyboard (openDrawer), or the drawer closes. A
	// bespoke Open opens the drawer itself when its way decides to (the agent's opens only when
	// an agent starts).
	Open, Close func(h *Host)
	// Opened and Closed follow the drawer, however it was opened or closed (Escape inside one
	// closes it): what it shows and where the keyboard goes. nil Closed: the editor.
	Opened, Closed func(h *Host)
}

// panelSpecs are the panels, in the order the host walks them. Every one floats (panelfloat.go).
var panelSpecs []PanelSpec

// The hooks use the registry at invocation, after package initialization.
func init() {
	panelSpecs = []PanelSpec{
		{ID: "explorer", View: "explorerTree", Edge: func(p prefs) string { return p.explorerEdge }, Places: edges,
			Keyboard: true,
			Size:     [2]int{sidePanelMin, 90}, Length: [2]int{20, 100}, Default: sideDefault},
		{ID: "links", View: "linksList", Edge: func(p prefs) string { return p.linkEdge }, Places: edges,
			Keyboard: true,
			Size:     [2]int{sidePanelMin, 90}, Length: [2]int{20, 100}, Default: sideDefault,
			Open: func(h *Host) { h.rel.ofPage(); h.openDrawer("links") }},
		{ID: "terminal", View: "terminalView", Edge: func(p prefs) string { return p.termEdge }, Places: termEdges,
			Size: [2]int{10, 90}, Length: [2]int{10, 100}, Default: termDefault,
			Open: (*Host).openTerminalPanel, Close: (*Host).closeTerminalPanel,
			Opened: func(h *Host) { h.set("App.terminalShown", true) },
			Closed: (*Host).terminalPanelClosed},
		{ID: "agent", View: "agentView", Edge: func(p prefs) string { return p.agentEdge }, Places: agentEdges,
			Size: [2]int{sidePanelMin, 90}, Length: [2]int{20, 100}, Default: agentDefault,
			Open:   (*Host).openAgentPanel,
			Opened: func(h *Host) { h.set("App.agentShown", true) },
			Closed: func(h *Host) { h.set("App.agentShown", false); h.focusEditor() }},
		// the HTML preview's drawer is at the right edge (main.qml), and it sizes itself
		{ID: "htmlPane", View: "htmlView", Edge: func(prefs) string { return "right" }},
	}
}

// panelSpec is the panel id, or nil when there is no such panel.
func panelSpec(id string) *PanelSpec {
	for i := range panelSpecs {
		if panelSpecs[i].ID == id {
			return &panelSpecs[i]
		}
	}
	return nil
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
	if s := panelSpec(id); s != nil {
		return s.View
	}
	return ""
}

// togglePanel opens or closes a panel its own way, or the one way: the drawer, and its view
// takes the keyboard. Open, it has the keyboard.
func (h *Host) togglePanel(name string) {
	s := panelSpec(name)
	if s == nil {
		return
	}
	if h.panelOpen[name] {
		if s.Close != nil {
			s.Close(h)
			return
		}
		h.closeDrawer(name)
		return
	}
	if s.Open != nil {
		s.Open(h)
		return
	}
	h.openDrawer(name)
}

// openDrawer opens the drawer and gives its view the keyboard; closeDrawer closes it.
func (h *Host) openDrawer(name string) {
	h.keep(h.p.Call(name, "open"))
	h.keep(h.p.Call(panelView(name), "forceActiveFocus"))
}

func (h *Host) closeDrawer(name string) {
	h.keep(h.p.Call(name, "close"))
}

// focusEditor gives the editor the keyboard: where a closed panel's keyboard goes by default.
func (h *Host) focusEditor() {
	h.keep(h.p.Call("editor", "forceActiveFocus"))
}

// panelOpened and panelClosed follow the drawers, however they were opened or closed (Escape
// inside one closes it).
func (h *Host) panelOpened(name string) {
	h.panelOpen[name] = true
	h.lastPanel = name
	h.showFind() // a drawer at the right edge hides "finding …"
	if s := panelSpec(name); s != nil && s.Opened != nil {
		s.Opened(h)
	}
}

func (h *Host) panelClosed(name string) {
	h.panelOpen[name] = false
	h.showFind()
	if s := panelSpec(name); s != nil && s.Closed != nil {
		s.Closed(h)
		return
	}
	h.focusEditor()
}

// dirEdge is the edge a Ctrl+h/j/k/l move heads for.
var dirEdge = map[string]string{"h": "left", "l": "right", "k": "top", "j": "bottom"}

// opposite is the edge across from an edge: the way back to the page.
var opposite = map[string]string{"left": "right", "right": "left", "top": "bottom", "bottom": "top"}

// movePane is Ctrl+h/j/k/l: from the page, in Normal mode, to the open panel at that edge; from a
// panel, back to the page the other way. When panels share an edge, declared
// registry order determines the target; map iteration must never pick focus.
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
		for _, s := range panelSpecs {
			if s.Edge(h.prefs) == edge && h.panelOpen[s.ID] {
				h.keep(h.p.Call(s.View, "forceActiveFocus"))
				return
			}
		}
		return
	}
	for _, s := range panelSpecs {
		if c, ok := h.p.Find(s.View); ok && app.FocusWithin(c) && edge == opposite[s.Edge(h.prefs)] {
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
