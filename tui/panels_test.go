package tui

import "testing"

// TestEveryPanelIsInTheDocument: each spec's drawer and the view in it that takes the keyboard are
// ids the layout declares, so the registry cannot name a panel the document lacks.
func TestEveryPanelIsInTheDocument(t *testing.T) {
	d := startDaemon(t, map[string][]string{"kb": {"a.md", "a\n"}})
	r := attached(t, d)
	for _, s := range panelSpecs {
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
	got := keyboardPanes()
	want := []string{"editor", "explorerTree", "linksList"}
	if len(got) != len(want) {
		t.Fatalf("keyboard panes %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("keyboard panes %v, want %v", got, want)
		}
	}
	if panelView("nope") != "" {
		t.Error("a view for a panel that does not exist")
	}
}
