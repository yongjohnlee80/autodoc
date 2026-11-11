package tui

import (
	"testing"

	"github.com/yongjohnlee80/autodoc/tui/feature"
	"github.com/yongjohnlee80/autodoc/tui/todos"
)

// TestTodosPanelOpensLargeAndKeepsTheUsersSize: before the user sizes it the panel is about 65% of
// the window wide and 80% tall, at any edge, the left one unless chosen; a size the user leaves it
// at is kept, per edge, and wins, and so do an edge they chose and a place they float it to.
func TestTodosPanelOpensLargeAndKeepsTheUsersSize(t *testing.T) {
	p := prefs{panelDefs: []feature.PanelSpec{todos.PanelSpec()}}
	for edge, want := range map[string][2]int{"right": {65, 80}, "left": {65, 80}, "top": {80, 65}, "bottom": {80, 65}} {
		if s, l := p.panelGeometry("todos", edge); s != want[0] || l != want[1] {
			t.Errorf("at the %s edge, before it is sized: %d × %d, want %d × %d", edge, s, l, want[0], want[1])
		}
	}
	p.todosEdge = "right"
	if st := panelState(p); st["App.todosSize"] != 65 || st["App.todosLength"] != 80 {
		t.Errorf("the document reads %v × %v", st["App.todosSize"], st["App.todosLength"])
	}
	readPanelPrefs(&p, map[string]any{"tui.todos.right.size": "40", "tui.todos.right.length": "50", "tui.todos.top.size": "99"})
	if s, l := p.panelGeometry("todos", "right"); s != 40 || l != 50 {
		t.Errorf("the user's size at the right edge: %d × %d, want 40 × 50", s, l)
	}
	if s, l := p.panelGeometry("todos", "top"); s != 80 || l != 65 {
		t.Errorf("an out-of-bounds size kept: %d × %d, want the default", s, l)
	}
	// the edge: the left by default, a kept one (the right) winning
	if p := defaultPrefs(); p.todosEdge != "left" {
		t.Errorf("a fresh state's todos edge: %q, want left", p.todosEdge)
	}
	var kept prefs
	feature.ReadRows(&kept, map[string]any{todos.PrefEdge: "right"}, legacyPrefRows(nil, false))
	if kept.todosEdge != "right" || todos.PanelSpec().Edge(kept) != "right" {
		t.Errorf("a kept right edge read as %q, the panel's %q", kept.todosEdge, todos.PanelSpec().Edge(kept))
	}
	r := [4]float64{5, 6, 30, 40}
	st := floatState(map[string][4]float64{"todos": r}, p.panelDefs)
	if st["App.todosFloating"] != true || st["App.todosX"] != 5.0 || st["App.todosW"] != 30.0 || st["App.todosH"] != 40.0 {
		t.Errorf("a place the user floated it to: %v", st)
	}
}
