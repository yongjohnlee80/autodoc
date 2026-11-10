package tui

import (
	"slices"

	tuicore "github.com/yongjohnlee80/golib/tui"
	tuidecl "github.com/yongjohnlee80/golib/tui/decl"
	widgetcore "github.com/yongjohnlee80/golib/tui/widget/core"
	"github.com/yongjohnlee80/golib/tui/widget/editor"
	"github.com/yongjohnlee80/golib/tui/widget/terminal"
)

// THE PANELS — the explorer, with its Explorer and Links tabs, and the others, drawers over the
// page: toggled (SPC e, SPC l, the Go menu), each from the edge its preference names, the page
// beneath never moving. Ctrl+h/j/k/l
// move between the page and the panels open at those edges, in Normal mode only: in Insert mode
// they are the editor's.

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
// as the dock's own click does (docs/decisions/editor.md#dock-clicks-hide-and-show). Open but
// covered, even in part, by a window or panel over it, it comes to the front with the keyboard
// instead of closing: what decides is whether it is seen whole, never where the keyboard is
// (docs/decisions/editor.md#a-toggle-raises-a-covered-panel).
func (h *Host) togglePanel(name string) {
	s := h.panelSpec(name)
	if s == nil {
		return
	}
	if h.panelOpen[name] && (h.panelHidden(name) || h.panelCovered(name)) {
		h.goTo(widgetTarget{panel: name})
		h.raisePanel(name) // the keyboard may be in it already, where focusing it raises nothing
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
// the panel shown on that tab; shown on another tab, that tab, to the front when covered; shown on
// it, closed, or to the front when covered (togglePanel).
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
	if !shown || h.panelCovered(name) {
		h.togglePanel(name) // shown on it, and to the front when covered
	}
}

// raisePanel brings panel name above the panels and windows over it, the keyboard where it is
// (golib's Drawer and FloatWindow Raise). A panel comes forward by itself when the keyboard enters
// it; one that holds the keyboard already would not.
func (h *Host) raisePanel(name string) {
	if c, ok := h.p.Find(name); ok {
		if r, ok := c.(interface{ Raise() }); ok {
			r.Raise()
		}
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
		// the newest first: F11 and SPC z off the panels reach the newest still open
		h.maximizableOpened = append([]string{name}, slices.DeleteFunc(h.maximizableOpened, func(p string) bool { return p == name })...)
	}
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
	h.dockHidden = nil    // a panel closed since the editor's icon hid the others: its next click hides
	if h.panelAlive(*s) { // a strip's x has stopped its tabs already
		h.flashDock(name) // closed into the dock, its shells or agents running on: it hid there
	}
	defer h.refreshDock() // a closed panel leaves the dock, unless something of it runs on
	if s.Closed != nil {
		s.Closed(h.panelHost(name))
		return
	}
	h.keep(h.p.Call(h.keyboardHome(), "forceActiveFocus"))
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
	if from < 0 || shown[from].panel == "" && h.core.Mode() != editor.ModeNormal {
		return
	}
	if to, ok := nextWindow(shown, from, step); ok {
		h.focusWindow(to.panel)
		h.typeIn(to.panel) // a move arrives ready to type; a click, SPC or reopening keeps the tab's mode
	}
}

// typeIn puts panel's current tab in input mode, when panel is the terminal's or the agents': a
// Ctrl+h/j/k/l move arrives there to type (v0.3.1). A tab with no program has no mode to leave.
func (h *Host) typeIn(panel string) {
	s := h.panelSpec(panel)
	if s == nil || !slices.Contains(terminalPanels, s.View) {
		return
	}
	c, ok := h.p.Find(s.View)
	if !ok {
		return
	}
	if tabs, ok := tuidecl.TerminalTabsOf(c); ok {
		if id := tabs.Active(); id != "" {
			_ = tabs.Select(id)                          // the keyboard in the tab's terminal, not the panel around it
			_ = tabs.SetMode(id, terminal.TerminalInput) // Input needs no program: never refused
		}
	}
}

// shownWindows are the windows on screen, placed: the page, then the open panels, in their declared
// order, each while it is shown (shownWindow).
func (h *Host) shownWindows() []placedWindow {
	var out []placedWindow
	if w, ok := h.shownWindow("", "editorWindow"); ok {
		out = append(out, w)
	}
	for _, s := range h.panelSpecs() {
		if !h.panelOpen[s.ID] {
			continue
		}
		if w, ok := h.shownWindow(s.ID, s.ID); ok {
			out = append(out, w)
		}
	}
	return out
}

// shownWindow is the window panel (the page, "") whose node is node, where it is drawn on the
// screen, while it is: not minimized (a minimized window is only concealed, its layer still in the
// overlay's stack), and laid out. The screen's is the one frame tiled and floating windows share; a
// window core's Bounds is its target's (a tile's is its manager's, under the menu bar). Whether a
// panel is open is the caller's.
func (h *Host) shownWindow(panel, node string) (placedWindow, bool) {
	c, _ := h.p.Find(node)
	w, ok := c.(interface{ ScreenRect() (tuicore.Rect, bool) })
	if !ok {
		return placedWindow{}, false
	}
	at, on := w.ScreenRect()
	return placedWindow{panel, at}, on
}

// windowView is the view the keyboard is in when it is in window panel: the page's editor, or the
// panel's focus view.
func (h *Host) windowView(panel string) string {
	if panel == "" {
		return "editor"
	}
	return h.panelSpec(panel).focusView()
}

// hasKeyboard reports whether the keyboard is in w.
func (h *Host) hasKeyboard(w placedWindow) bool {
	c, ok := h.p.Find(h.windowView(w.panel))
	return ok && h.p.App().FocusWithin(c)
}

// keyboardHome is where the keyboard goes when the window that had it closes: the topmost window
// still shown (shownWindows), by the overlay's stack, as a window manager focuses the next window
// down. It is that window's view, else, with none shown, the page's editor.
func (h *Host) keyboardHome() string {
	host, ok := h.p.Overlay()
	if !ok {
		return "editor"
	}
	var layers []tuicore.Component
	for _, l := range host.Stack.All() {
		layers = append(layers, l)
	}
	var views []string
	for _, w := range h.shownWindows() {
		views = append(views, h.windowView(w.panel))
	}
	for i := len(layers) - 1; i >= 0; i-- {
		layer, ok := layers[i].(widgetcore.ContextOwner)
		if !ok || layer.Context() == nil {
			continue
		}
		for _, view := range views {
			c, _ := h.p.Find(view)
			if n, ok := c.(widgetcore.ContextOwner); ok && n.Context() != nil && layer.Context().SubtreeContains(n.Context().ID()) {
				return view
			}
		}
	}
	return "editor"
}

// focusWindow gives the keyboard to a panel, or to the page (""). An id that names no panel now (a
// card whose plugin went while a caller held it: Arrange's end, posted) is a window gone, so the
// keyboard goes home, as when a window closes (keyboardHome).
func (h *Host) focusWindow(panel string) {
	if panel == "" {
		h.focusEditor()
		return
	}
	s := h.panelSpec(panel)
	if s == nil {
		h.keep(h.p.Call(h.keyboardHome(), "forceActiveFocus"))
		return
	}
	h.keep(h.p.Call(s.focusView(), "forceActiveFocus"))
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

func legacyPanelSpecs() []PanelSpec {
	return []PanelSpec{
		{ID: "htmlPane", View: "htmlView", Priority: 50, Icon: "⎙"},
	}
}

func defaultPanelSpecs() []PanelSpec {
	return []PanelSpec{
		explorerPanelSpec(),
		terminalPanelSpec(), agentPanelSpec(), legacyPanelSpecs()[0],
	}
}

func (l legacy) Panels() []PanelSpec {
	return legacyPanelSpecs()
}
