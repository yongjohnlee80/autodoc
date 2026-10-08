package tui

import (
	"fmt"
	"strconv"
	"strings"
)

// FLOATING PANELS — the five drawers move and resize anywhere over the page: Alt/Option-left drag
// moves one, Alt/Option-right drag resizes it, and Go › Arrange panel (SPC L) does both by keys.
// Where one ends is kept as percentages of the Window, tui.<panel>.float = "x,y,w,h", and it opens
// there from then on; its edge preference only says where it first appears. Go › Reset panel
// layout, or a new edge in Preferences, docks it at its edge again (ADR 1791500773).

// floatPanels are the drawers that float, by their document ids.
var floatPanels = []string{"explorer", "links", "terminal", "agent", "htmlPane"}

func floatPref(panel string) string { return "tui." + panel + ".float" }

// readPanelFloats reads each panel's floating rectangle: four percentages, the size at least 5.
func readPanelFloats(p *prefs, m map[string]any) {
	for _, panel := range floatPanels {
		s, _ := m[floatPref(panel)].(string)
		if r, ok := parseFloat(s); ok {
			p.panelFloat = withFloat(p.panelFloat, panel, &r)
		}
	}
}

// parseFloat reads "x,y,w,h".
func parseFloat(s string) ([4]int, bool) {
	var r [4]int
	parts := strings.Split(s, ",")
	if len(parts) != 4 {
		return r, false
	}
	for i, part := range parts {
		n, err := strconv.Atoi(strings.TrimSpace(part))
		if err != nil || n < 0 || n > 100 {
			return r, false
		}
		r[i] = n
	}
	return r, r[2] >= 5 && r[3] >= 5
}

func formatFloat(r [4]int) string { return fmt.Sprintf("%d,%d,%d,%d", r[0], r[1], r[2], r[3]) }

// withFloat is m with panel's rectangle r, or without it when r is nil: a new map, as the prefs
// are values.
func withFloat(m map[string][4]int, panel string, r *[4]int) map[string][4]int {
	out := make(map[string][4]int, len(m)+1)
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
func floatState(m map[string][4]int) map[string]any {
	st := map[string]any{}
	for _, panel := range floatPanels {
		r, ok := m[panel]
		if !ok {
			r = [4]int{25, 25, 50, 50}
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
func (h *Host) panelPlaced(panel string, x, y, w, ht int) {
	r := [4]int{x, y, w, ht}
	if a := h.arrange; a != nil && a.panel == panel {
		a.rect = &r
		h.showFloat(panel, &r)
		return
	}
	h.setPref(floatPref(panel), formatFloat(r), func(p *prefs) { p.panelFloat = withFloat(p.panelFloat, panel, &r) })
}

// showFloat sets what the document reads of panel's placement, with nothing stored.
func (h *Host) showFloat(panel string, r *[4]int) {
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
	h.setPref(floatPref(panel), "", func(p *prefs) { p.panelFloat = withFloat(p.panelFloat, panel, nil) })
}

// resetPanelLayout is Go › Reset panel layout: every panel docks at its edge again.
func (h *Host) resetPanelLayout() {
	docked := 0
	for _, panel := range floatPanels {
		if _, ok := h.prefs.panelFloat[panel]; ok {
			h.dockPanel(panel)
			docked++
		}
	}
	if docked == 0 {
		h.notify("every panel is at its edge already")
	}
}

// panelWithKeyboard is the open panel that has the keyboard, else the one opened last; "" when
// none is open.
func (h *Host) panelWithKeyboard() string {
	app := h.p.App()
	for _, panel := range floatPanels {
		if c, ok := h.p.Find(panels[panel]); ok && h.panelOpen[panel] && app.FocusWithin(c) {
			return panel
		}
	}
	if h.panelOpen[h.lastPanel] {
		return h.lastPanel
	}
	return ""
}
