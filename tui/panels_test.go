package tui

import (
	"fmt"
	"testing"
)

// TestEveryPanelIsInTheDocument: each spec's drawer and the view in it that takes the keyboard are
// ids the layout declares, so the registry cannot name a panel the document lacks.
func TestEveryPanelIsInTheDocument(t *testing.T) {
	d := startDaemon(t, map[string][]string{"kb": {"a.md", "a\n"}})
	r := attached(t, d)
	for _, s := range r.h.panelSpecs() {
		for _, id := range []string{s.ID, s.View} {
			if !onLoop(r, func() bool { _, ok := r.h.p.Find(id); return ok }) {
				t.Errorf("panel %s: no %q in the document", s.ID, id)
			}
		}
	}
}

// TestTheAgentPanelHasAnEdge: Ctrl+h/j/k/l reach the agent at the edge it opens from — it was the
// one panel missing from the edges the moves look at.
func TestTheAgentPanelHasAnEdge(t *testing.T) {
	d := startDaemon(t, map[string][]string{"kb": {"a.md", "a\n"}})
	r := attached(t, d)
	got := onLoop(r, func() string {
		r.h.prefs.agentEdge = "left"
		return r.h.panelEdges()["agent"]
	})
	if got != "left" {
		t.Errorf("the agent's edge is %q, want left", got)
	}
}

// Two open panels on the same edge use registry order on every move. Returning
// to the editor between moves exercises target selection afresh; a map walk
// used to send the keyboard to either panel depending on iteration order.
func TestPaneMoveToSharedEdgeUsesRegistryOrder(t *testing.T) {
	d := startDaemon(t, map[string][]string{"kb": {"a.md", "a\n"}})
	r := attached(t, d)
	onLoop(r, func() bool {
		r.h.prefs.explorerEdge, r.h.prefs.relationsEdge = "left", "left"
		r.h.keep(r.h.p.Call("explorer", "open"))
		r.h.keep(r.h.p.Call("links", "open"))
		return true
	})
	for i := 0; i < 128; i++ {
		got := onLoop(r, func() string {
			r.h.focusEditor()
			r.h.movePane("h")
			for _, id := range []string{"explorerTree", "relationsList", "editor"} {
				if c, ok := r.h.p.Find(id); ok && r.h.p.App().FocusWithin(c) {
					return id
				}
			}
			return ""
		})
		if got != "explorerTree" {
			t.Fatalf("shared-edge move %d focused %q, want the first panel explorerTree", i, got)
		}
	}
}

// TestAnyPanelAtTheRightHidesTheFindCard: "finding …" sits at the top right, so any open panel
// docked there hides it, not the explorer and the links only.
func TestAnyPanelAtTheRightHidesTheFindCard(t *testing.T) {
	d := startDaemon(t, map[string][]string{"kb": {"a.md", "a\n"}})
	r := attached(t, d)
	for _, tc := range []struct {
		panel string
		set   func(p *prefs)
	}{
		{"terminal", func(p *prefs) { p.termEdge = "right" }},
		{"agent", func(p *prefs) { p.agentEdge = "right" }},
		{"htmlPane", func(*prefs) {}},
		{"explorer", func(p *prefs) { p.explorerEdge = "right" }},
	} {
		got := onLoop(r, func() [2]bool {
			tc.set(&r.h.prefs)
			r.h.panelOpen[tc.panel] = true
			open := r.h.rightDrawerOpen()
			r.h.panelOpen[tc.panel] = false
			return [2]bool{open, r.h.rightDrawerOpen()}
		})
		if got != [2]bool{true, false} {
			t.Errorf("%s at the right: rightDrawerOpen open %v, closed %v; want true, false", tc.panel, got[0], got[1])
		}
	}
}

// TestKeyboardPanesAreThePageAndTheTreePanels: the terminal hands the keyboard back to the page or
// to a panel that takes it.
func TestKeyboardPanesAreThePageAndTheTreePanels(t *testing.T) {
	got := keyboardPanes(defaultPanelSpecs())
	want := []string{"editor", "explorerTree", "relationsList"}
	if len(got) != len(want) {
		t.Fatalf("keyboard panes %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("keyboard panes %v, want %v", got, want)
		}
	}
	if (&Host{}).panelView("nope") != "" {
		t.Error("a view for a panel that does not exist")
	}
}

// Unknown panel commands must not create open-state entries or move focus.
// This keeps stale/custom callers from addressing a drawer the registry does
// not contain.
func TestUnknownPanelToggleKeepsOpenStateAndFocus(t *testing.T) {
	d := startDaemon(t, map[string][]string{"kb": {"a.md", "a\n"}})
	r := attached(t, d)
	got := onLoop(r, func() bool {
		r.h.focusEditor()
		before := make(map[string]bool, len(r.h.panelOpen))
		for name, open := range r.h.panelOpen {
			before[name] = open
		}
		r.h.togglePanel("missing-panel")
		if len(before) != len(r.h.panelOpen) {
			return false
		}
		for name, open := range before {
			if r.h.panelOpen[name] != open {
				return false
			}
		}
		c, ok := r.h.p.Find("editor")
		return ok && r.h.p.App().FocusWithin(c)
	})
	if !got {
		t.Fatal("an unknown panel toggle changed open state or moved focus")
	}
}

// TestEveryPanelKeepsItsSizeAtItsEdge: a panel's grip released keeps its size and length for the
// edge it opens from now, and a new start reads them back, each panel at its own edge.
func TestEveryPanelKeepsItsSizeAtItsEdge(t *testing.T) {
	d := startDaemon(t, map[string][]string{"kb": {"a.md", "a\n"}})
	r := attached(t, d)
	onLoop(r, func() bool {
		r.h.prefs.explorerEdge, r.h.prefs.relationsEdge, r.h.prefs.agentEdge, r.h.prefs.termEdge = "left", "right", "bottom", "center"
		r.h.panelResized("explorer", 20, 70)
		r.h.panelResized("links", 25, 75)
		r.h.panelResized("agent", 40, 90)
		r.h.panelResized("terminal", 60, 80)
		r.h.panelResized("nope", 1, 1) // no such panel: nothing
		return true
	})
	want := map[string]int{
		"tui.explorer.left.size": 20, "tui.explorer.left.length": 70,
		"tui.links.right.size": 25, "tui.links.right.length": 75,
		"tui.agent.bottom.size": 40, "tui.agent.bottom.length": 90,
		"tui.terminal.center.size": 60, "tui.terminal.center.length": 80,
	}
	got := onLoop(r, func() map[string]int { return r.h.prefs.panelGeo })
	for k, v := range want {
		if got[k] != v {
			t.Errorf("%s = %d, want %d (%v)", k, got[k], v, got)
		}
	}
	// read back as a new start reads them; an out-of-bounds one keeps the default
	m := map[string]any{}
	for k, v := range want {
		m[k] = fmt.Sprint(v)
	}
	m["tui.links.right.size"] = "5"
	var p prefs
	readPanelPrefs(&p, m)
	if p.panelGeo["tui.agent.bottom.size"] != 40 || p.panelGeo["tui.explorer.left.length"] != 70 {
		t.Errorf("read back %v", p.panelGeo)
	}
	if _, kept := p.panelGeo["tui.links.right.size"]; kept {
		t.Error("a size under the minimum was kept")
	}
	if s, l := p.panelGeometry("links", "right"); s != 30 || l != 75 {
		t.Errorf("links at the right: %d × %d, want the default size and the kept length", s, l)
	}
}
