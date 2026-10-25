package tui

import (
	"errors"
	"fmt"

	"github.com/yongjohnlee80/golib/tui/panelgeometry"
)

// FLOATING PANELS — the five drawers move and resize anywhere over the page: Alt/Option-left drag
// moves one, Alt/Option-right drag resizes it, and Go › Arrange panel (SPC L) does both by keys.
// Where one ends is kept as percentages of the Window, tui.<panel>.float = "x,y,w,h", and it opens
// there from then on; its edge preference only says where it first appears. Go › Reset panel
// layout, or a new edge in Preferences, docks it at its edge again (ADR 1791500773).

// readPanelFloats reads each panel's floating rectangle: four percentages (to a hundredth, as the
// drawer reports them), the size at least 5.
func readPanelFloats(p *prefs, m map[string]any) {
	for _, spec := range p.geometrySpecs() {
		panel := spec.ID
		s, _ := m[panelgeometry.FloatKey(panel)].(string)
		if r, ok := panelgeometry.ParseFloat(s); ok {
			p.panelFloat = withFloat(p.panelFloat, panel, &r)
		}
	}
}

// withFloat is m with panel's rectangle r, or without it when r is nil: a new map, as the prefs
// are values.
func withFloat(m map[string][4]float64, panel string, r *[4]float64) map[string][4]float64 {
	out := make(map[string][4]float64, len(m)+1)
	for k, v := range m {
		out[k] = v
	}
	if r != nil {
		out[panel] = *r
	} else {
		delete(out, panel)
	}
	return out
}

// floatState is what the document reads of a panel's placement: App.<panel>Floating, and its
// rectangle App.<panel>X, Y, W and H (a docked panel's are where it would float: the middle).
func floatState(m map[string][4]float64, specs []PanelSpec) map[string]any {
	st := map[string]any{}
	for _, spec := range specs {
		panel := spec.ID
		r, ok := m[panel]
		if !ok {
			r = [4]float64{25, 25, 50, 50}
		}
		st["App."+panel+"Floating"] = ok
		for i, k := range []string{"X", "Y", "W", "H"} {
			st["App."+panel+k] = r[i]
		}
	}
	return st
}

// panelPlaced is a panel's move or resize ended: it floats there. While that panel is being
// arranged the rectangle is only shown, and kept when the arrangement is (arrange.go).
// maximizable are the panels Maximize reaches: the terminal's and the agents' (their Drawers are
// maximizable, main.qml).
var maximizable = []string{"terminal", "agent"}

// toggleMaximize maximizes panel, or restores it: its Drawer's own toggleMaximize. The tab menus'
// Maximize (Restore), F11 and SPC M all come here.
func (h *Host) toggleMaximize(panel string) error { return h.p.Call(panel, "toggleMaximize") }

// panelInHand is the terminal or the agents, whichever has the keyboard, else the most recently
// opened of them that is still open; "" for neither. Maximize and Resume act on it.
func (h *Host) panelInHand() string {
	target := ""
	for _, p := range maximizable {
		if s := h.panelSpec(p); s != nil && h.paneHasKeyboard(s.View) {
			target = p
		}
	}
	for _, p := range h.maximizableOpened {
		if target == "" && h.panelOpen[p] {
			target = p
		}
	}
	return target
}

// maximizePanel is F11 and SPC M (panel.maximize): the panel in hand (panelInHand).
func (h *Host) maximizePanel() {
	target := h.panelInHand()
	if target == "" {
		h.notify(h.tr("autodoc.notice.panelfloat.maximize_is_the_terminal_s_and_the_agents_open_o"))
		return
	}
	if err := h.toggleMaximize(target); err != nil {
		op := "Maximize"
		if h.panelMax[target] {
			op = "Restore"
		}
		h.keep(fmt.Errorf("%s the %s panel: %w", op, target, err))
	}
}

// resumePanel is panel.resume: a SIGCONT to the active tab of the panel in hand (panelInHand), so
// a program stopped by its own Ctrl+Z, or by a SIGTSTP from elsewhere, runs again. A running one is
// not disturbed.
func (h *Host) resumePanel() {
	target := h.panelInHand()
	if target == "" {
		h.keep(errors.New("Resume: neither the terminal nor the agents is open"))
		return
	}
	s := h.panelSpec(target)
	tabs, ok := h.tabsOf(s.View)
	if !ok || tabs.Active() == "" {
		h.keep(fmt.Errorf("Resume: the %s panel has no tab", target))
		return
	}
	if err := (terminalPanel{tabs: tabs}).Resume(tabs.Active()); err != nil {
		h.keep(fmt.Errorf("Resume the %s panel's tab: %w", target, err))
	}
}

// panelChanged is a panel's Drawer changed signal, as far as the host follows it: a maximize
// toggle (operation "maximize"), maximized or restored, which its tab menu's Maximize reads as
// Restore while it is.
func (h *Host) panelChanged(panel, operation string, maximized bool) {
	if operation != "maximize" {
		return
	}
	if h.panelMax == nil {
		h.panelMax = map[string]bool{}
	}
	h.panelMax[panel] = maximized
}

func (h *Host) panelPlaced(panel string, x, y, w, ht float64) {
	r := [4]float64{x, y, w, ht}
	if a := h.arrange; a != nil && a.panel == panel {
		a.rect = &r
		h.showFloat(panel, &r)
		return
	}
	h.setPref(panelgeometry.FloatKey(panel), panelgeometry.FormatFloat(r), func(p *prefs) { p.panelFloat = withFloat(p.panelFloat, panel, &r) })
}

// showFloat sets what the document reads of panel's placement, with nothing stored.
func (h *Host) showFloat(panel string, r *[4]float64) {
	h.set("App."+panel+"Floating", r != nil)
	if r != nil {
		for i, k := range []string{"X", "Y", "W", "H"} {
			h.set("App."+panel+k, r[i])
		}
	}
}

// dockPanel forgets panel's floating rectangle: it opens at its edge again.
func (h *Host) dockPanel(panel string) {
	if _, ok := h.prefs.panelFloat[panel]; !ok {
		return
	}
	h.setPref(panelgeometry.FloatKey(panel), "", func(p *prefs) { p.panelFloat = withFloat(p.panelFloat, panel, nil) })
}

// resetPanelLayout is Go › Reset panel layout: every panel docks at its edge again.
func (h *Host) resetPanelLayout() {
	docked := 0
	for _, panel := range h.panelIDs() {
		if _, ok := h.prefs.panelFloat[panel]; ok {
			h.dockPanel(panel)
			docked++
		}
	}
	if docked == 0 {
		h.notify(h.tr("autodoc.notice.panelfloat.every_panel_is_at_its_edge_already"))
	}
}

// panelWithKeyboard is the open panel that has the keyboard, else the one opened last; "" when
// none is open.
func (h *Host) panelWithKeyboard() string {
	app := h.p.App()
	for _, panel := range h.panelIDs() {
		if c, ok := h.p.Find(h.panelView(panel)); ok && h.panelOpen[panel] && app.FocusWithin(c) {
			return panel
		}
	}
	if h.panelOpen[h.lastPanel] {
		return h.lastPanel
	}
	return ""
}
