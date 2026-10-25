package tui

import (
	"slices"

	tuicore "github.com/yongjohnlee80/golib/tui"
	"github.com/yongjohnlee80/golib/tui/panelgeometry"
	"github.com/yongjohnlee80/golib/tui/widget"
)

// THE PANELS — the explorer, with its Explorer and Links tabs, and the others, drawers over the
// page: toggled (SPC e, SPC l, the Go menu), each from the edge its preference names, the page
// beneath never moving. Ctrl+h/j/k/l
// move between the page and the panels open at those edges, in Normal mode only: in Insert mode
// they are the editor's.

// PanelSpec is one drawer over the page: everything the host's panel code asks of it, in one place,
// so a list of panels cannot disagree with another.
type PanelSpec struct {
	ID   string // the Drawer's id in the document
	View string // what in it takes the keyboard
	// Focus is what in it takes the keyboard now, where that changes: a tabbed panel's tab shown.
	// nil: View.
	Focus func() string
	// Tab and ShowTab are a tabbed panel's tab shown and the way to show another, its view given the
	// keyboard (togglePanelTab); nil: it has no tabs.
	Tab     func() int
	ShowTab func(tab int)
	// GoTabs are :go names of its tabs, by tab, "" for none: the explorer's Links tab keeps the name
	// its own drawer had, relations (widgets.go).
	GoTabs []string
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
	// Icon is the panel's dock icon: one cell, East Asian Width neutral (dock.go)
	Icon string
	// Alive reports that the panel lives on with its drawer closed (a plugin card's running
	// plugin); nil: only a TerminalTabs view's running tab keeps a closed panel in the dock
	Alive func() bool
}

// focusView is what in s takes the keyboard now.
func (s PanelSpec) focusView() string {
	if s.Focus != nil {
		return s.Focus()
	}
	return s.View
}

// geometry is s as golib's panel geometry check reads it: its id, the places it opens from and its
// bounds (tui/panelgeometry, ADR 1792459145 §4.1).
func (s PanelSpec) geometry() panelgeometry.Spec {
	return panelgeometry.Spec{ID: s.ID, Edges: s.Places, Size: s.Size, Length: s.Length}
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

// panelView is what in the panel id takes the keyboard now; "" for no such panel.
func (h *Host) panelView(id string) string {
	if s := h.panelSpec(id); s != nil {
		return s.focusView()
	}
	return ""
}

// togglePanel opens or closes a panel its own way, or the one way: the drawer, and its view
// takes the keyboard. Open, it has the keyboard. Hidden by the dock, it is shown again as it was,
// as the dock's own click does (ADR 1792397302 r3).
func (h *Host) togglePanel(name string) {
	s := h.panelSpec(name)
	if s == nil {
		return
	}
	if h.panelOpen[name] && h.panelHidden(name) {
		h.goTo(widgetTarget{panel: name})
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

// togglePanelTab is a tabbed panel's toggle for one of its tabs (SPC e, SPC l): closed or hidden,
// the panel shown on that tab; shown on another tab, that tab; shown on it, closed.
func (h *Host) togglePanelTab(name string, tab int) {
	s := h.panelSpec(name)
	if s == nil || s.Tab == nil || s.ShowTab == nil {
		return
	}
	shown := h.panelOpen[name] && !h.panelHidden(name)
	if shown && s.Tab() == tab {
		h.togglePanel(name)
		return
	}
	s.ShowTab(tab)
	if !shown {
		h.togglePanel(name)
	}
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
	h.dockHidden = nil // a panel opened since the editor's icon hid the others: its next click hides
	h.lastPanel = name
	if slices.Contains(maximizable, name) {
		// the newest first: F11 and SPC M off the panels reach the newest still open
		h.maximizableOpened = append([]string{name}, slices.DeleteFunc(h.maximizableOpened, func(p string) bool { return p == name })...)
	}
	h.showFind() // a drawer at the right edge hides "finding …"
	if s.Opened != nil {
		s.Opened(h.panelHost(name))
	}
	h.refreshDock()
}

func (h *Host) panelClosed(name string) {
	s := h.panelSpec(name)
	if s == nil {
		return
	}
	h.panelOpen[name] = false
	h.dockHidden = nil // a panel closed since the editor's icon hid the others: its next click hides
	h.showFind()
	if h.panelAlive(*s) { // a strip's x has stopped its tabs already
		h.flashDock(name) // closed into the dock, its shells or agents running on: it hid there
	}
	defer h.refreshDock() // a closed panel leaves the dock, unless something of it runs on
	if s.Closed != nil {
		s.Closed(h.panelHost(name))
		return
	}
	h.focusEditor()
}

// dirStep is the way a Ctrl+h/j/k/l move heads.
var dirStep = map[string]tuicore.Point{"h": {X: -1}, "l": {X: 1}, "k": {Y: -1}, "j": {Y: 1}}

// placedWindow is a shown window where it is placed now: a panel, or the page ("").
type placedWindow struct {
	panel string
	at    tuicore.Rect
}

// movePane is Ctrl+h/j/k/l: the keyboard to the shown window next to the one it is in, that way,
// by where each is placed now (tiled, docked or floating alike), the page among them. From the page
// only in Normal mode: in Insert mode the keys are the editor's.
func (h *Host) movePane(dir string) {
	step, ok := dirStep[dir]
	if !ok {
		return
	}
	shown := h.shownWindows()
	from := slices.IndexFunc(shown, h.hasKeyboard)
	if from < 0 || shown[from].panel == "" && h.core.Mode() != widget.ModeNormal {
		return
	}
	if to, ok := nextWindow(shown, from, step); ok {
		h.focusWindow(to.panel)
	}
}

// shownWindows are the windows on screen, placed: the page, then the open panels not hidden, in
// their declared order.
func (h *Host) shownWindows() []placedWindow {
	var out []placedWindow
	add := func(panel, node string) {
		c, _ := h.p.Find(node)
		w, ok := c.(interface{ WindowBehavior() *widget.WindowCore })
		if !ok || w.WindowBehavior() == nil || w.WindowBehavior().State().Minimized {
			return
		}
		if at := w.WindowBehavior().Bounds(); at.W > 0 && at.H > 0 {
			out = append(out, placedWindow{panel, at})
		}
	}
	add("", "editorWindow")
	for _, s := range h.panelSpecs() {
		if h.panelOpen[s.ID] {
			add(s.ID, s.ID)
		}
	}
	return out
}

// hasKeyboard reports whether the keyboard is in w.
func (h *Host) hasKeyboard(w placedWindow) bool {
	view := "editor"
	if w.panel != "" {
		view = h.panelSpec(w.panel).focusView()
	}
	c, ok := h.p.Find(view)
	return ok && h.p.App().FocusWithin(c)
}

// focusWindow gives the keyboard to a panel, or to the page ("").
func (h *Host) focusWindow(panel string) {
	if panel == "" {
		h.focusEditor()
		return
	}
	h.keep(h.p.Call(h.panelSpec(panel).focusView(), "forceActiveFocus"))
}

// nextWindow is the window next to shown[from] a step away: of those whose centre lies that way,
// the one whose facing edge is nearest (an overlapping one is nearest of all), then the one nearer
// across, then the first in shown's order, never a map's.
func nextWindow(shown []placedWindow, from int, step tuicore.Point) (placedWindow, bool) {
	f := shown[from].at
	best, found := placedWindow{}, false
	var bestGap, bestAcross int
	for i, w := range shown {
		r := w.at
		// doubled centres: no half cells
		dx, dy := (2*r.X+r.W)-(2*f.X+f.W), (2*r.Y+r.H)-(2*f.Y+f.H)
		along, across := dx*step.X+dy*step.Y, abs(dx*step.Y+dy*step.X)
		if i == from || along <= 0 {
			continue
		}
		var gap int
		switch {
		case step.X > 0:
			gap = r.X - (f.X + f.W)
		case step.X < 0:
			gap = f.X - (r.X + r.W)
		case step.Y > 0:
			gap = r.Y - (f.Y + f.H)
		default:
			gap = f.Y - (r.Y + r.H)
		}
		gap = max(gap, 0)
		if !found || gap < bestGap || gap == bestGap && across < bestAcross {
			best, found, bestGap, bestAcross = w, true, gap, across
		}
	}
	return best, found
}

// panelEdges are the edges each panel opens from now.
func (h *Host) panelEdges() map[string]string {
	out := make(map[string]string, len(h.panelSpecs()))
	for _, s := range h.panelSpecs() {
		out[s.ID] = s.Edge(h.prefs)
	}
	return out
}
