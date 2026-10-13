package tui

import (
	"slices"

	"github.com/yongjohnlee80/golib/tui/widget"
)

// THE PANELS — the explorer and the relations, drawers over the page: toggled (SPC e, SPC l, the Go
// menu), each from the edge its preference names, the page beneath never moving. Ctrl+h/j/k/l
// move between the page and the panels open at those edges, in Normal mode only: in Insert mode
// they are the editor's.

// PanelSpec is one drawer over the page: everything the host's panel code asks of it, in one place,
// so a list of panels cannot disagree with another.
type PanelSpec struct {
	ID   string // the Drawer's id in the document
	View string // what in it takes the keyboard
	// Priority orders focus targets across features; ties keep contribution order.
	Priority int
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
	Open, Close func(PanelHost)
	// Opened and Closed follow the drawer, however it was opened or closed (Escape inside one
	// closes it): what it shows and where the keyboard goes. nil Closed: the editor.
	Opened, Closed func(PanelHost)
}

// panelSpecs are this program's snapshotted Feature contributions.
func (h *Host) panelSpecs() []PanelSpec {
	if h.reg == nil || h.reg.panels == nil {
		return nil
	}
	return h.reg.panels.specs
}

// panelSpec is the panel id, or nil when there is no such panel.
func (h *Host) panelSpec(id string) *PanelSpec {
	return findPanel(h.panelSpecs(), id)
}

// panelIDs are the panels' ids.
func (h *Host) panelIDs() []string {
	out := make([]string, len(h.panelSpecs()))
	for i, s := range h.panelSpecs() {
		out[i] = s.ID
	}
	return out
}

// panelView is what in the panel id takes the keyboard; "" for no such panel.
func (h *Host) panelView(id string) string {
	if s := h.panelSpec(id); s != nil {
		return s.View
	}
	return ""
}

// togglePanel opens or closes a panel its own way, or the one way: the drawer, and its view
// takes the keyboard. Open, it has the keyboard.
func (h *Host) togglePanel(name string) {
	s := h.panelSpec(name)
	if s == nil {
		return
	}
	if h.panelOpen[name] {
		if s.Close != nil {
			s.Close(h.panelHost(name))
			return
		}
		h.closeDrawer(name)
		return
	}
	if s.Open != nil {
		s.Open(h.panelHost(name))
		return
	}
	h.openDrawer(name)
}

// openDrawer opens the drawer and gives its view the keyboard; closeDrawer closes it.
func (h *Host) openDrawer(name string) {
	if h.panelSpec(name) == nil {
		return
	}
	h.keep(h.p.Call(name, "open"))
	h.keep(h.p.Call(h.panelView(name), "forceActiveFocus"))
}

func (h *Host) closeDrawer(name string) {
	if h.panelSpec(name) == nil {
		return
	}
	h.keep(h.p.Call(name, "close"))
}

// focusEditor gives the editor the keyboard: where a closed panel's keyboard goes by default.
func (h *Host) focusEditor() {
	h.keep(h.p.Call("editor", "forceActiveFocus"))
}

// panelOpened and panelClosed follow the drawers, however they were opened or closed (Escape
// inside one closes it).
func (h *Host) panelOpened(name string) {
	s := h.panelSpec(name)
	if s == nil {
		return
	}
	h.panelOpen[name] = true
	h.lastPanel = name
	if slices.Contains(maximizable, name) {
		// the newest first: F11 and SPC M off the panels reach the newest still open
		h.maximizableOpened = append([]string{name}, slices.DeleteFunc(h.maximizableOpened, func(p string) bool { return p == name })...)
	}
	h.showFind() // a drawer at the right edge hides "finding …"
	if s.Opened != nil {
		s.Opened(h.panelHost(name))
	}
}

func (h *Host) panelClosed(name string) {
	s := h.panelSpec(name)
	if s == nil {
		return
	}
	h.panelOpen[name] = false
	h.showFind()
	if s.Closed != nil {
		s.Closed(h.panelHost(name))
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
		for _, s := range h.panelSpecs() {
			if s.Edge(h.prefs) == edge && h.panelOpen[s.ID] {
				h.keep(h.p.Call(s.View, "forceActiveFocus"))
				return
			}
		}
		return
	}
	for _, s := range h.panelSpecs() {
		if c, ok := h.p.Find(s.View); ok && app.FocusWithin(c) && edge == opposite[s.Edge(h.prefs)] {
			h.keep(h.p.Call("editor", "forceActiveFocus"))
			return
		}
	}
}

// panelEdges are the edges each panel opens from now.
func (h *Host) panelEdges() map[string]string {
	out := make(map[string]string, len(h.panelSpecs()))
	for _, s := range h.panelSpecs() {
		out[s.ID] = s.Edge(h.prefs)
	}
	return out
}
