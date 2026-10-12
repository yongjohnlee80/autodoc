package tui

import "testing"

// TestPickingTheWorkspaceInUseOnlyClosesThePicker: choosing the workspace already in use enters
// nothing again; the picker closes and the page stays as it is.
func TestPickingTheWorkspaceInUseOnlyClosesThePicker(t *testing.T) {
	d := startDaemon(t, map[string][]string{"kb": {"a.md", "a\n"}, "notes": {"n.md", "n\n"}})
	r := attached(t, d)
	r.s.WaitFor(t, "a workspace entered", func(string) bool {
		return onLoop(r, func() bool { return r.h.ws != "" && len(r.h.workspaces.All()) == 2 })
	})
	before := onLoop(r, func() [2]any { return [2]any{r.h.ws, r.h.epoch} })
	onLoop(r, func() bool {
		for i, w := range r.h.workspaces.All() {
			if w.name == r.h.ws {
				r.h.useWorkspace(i)
			}
		}
		r.h.useWorkspace(-1) // no such row: nothing
		return true
	})
	after := onLoop(r, func() [2]any { return [2]any{r.h.ws, r.h.epoch} })
	if before != after {
		t.Errorf("picking the workspace in use entered it again: %v, then %v", before, after)
	}
}
