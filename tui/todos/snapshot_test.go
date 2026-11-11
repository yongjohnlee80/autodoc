package todos

import (
	"testing"

	"github.com/yongjohnlee80/autodoc/tui/feature"
)

// TestTheSnapshotIsACopy: what Snapshot answers shares nothing with the panel. A cell that changes
// its copy (the task under the cursor, the tabs outdated) leaves the panel as it was: the next
// Snapshot reads the panel's own (the mutant: the cursor's task answered as the panel's own map).
func TestTheSnapshotIsACopy(t *testing.T) {
	v := New(scriptedStore(true))
	v.list()
	onTask(t, v, "open")
	s := v.Snapshot()
	if !s.HasStore || s.Cursor == nil {
		t.Fatalf("fixture: %+v, want a store shown and a task under the cursor", s)
	}
	title, stale := feature.Str(s.Cursor, "title"), s.Stale[s.Tab]
	s.Cursor["title"] = "changed by the cell"
	s.Stale[s.Tab] = !stale
	again := v.Snapshot()
	if got := feature.Str(again.Cursor, "title"); got != title {
		t.Errorf("the task under the cursor, its copy changed: %q, want %q", got, title)
	}
	if again.Stale[again.Tab] != stale {
		t.Errorf("the tab's outdated mark, its copy changed: %v, want %v", again.Stale[again.Tab], stale)
	}
}

// TestTabsAreACopy: the tabs Tabs answers are a copy: changing one changes nothing Tabs answers next.
func TestTabsAreACopy(t *testing.T) {
	tabs := Tabs()
	if len(tabs) != 5 || tabs[0].Table != "todoOpenTable" || tabs[0].Bucket != "open" || tabs[1].Title != "In progress" {
		t.Fatalf("the tabs: %+v", tabs)
	}
	tabs[0].Table = "changed"
	if Tabs()[0].Table != "todoOpenTable" {
		t.Error("a change to Tabs' answer reached the panel's tabs")
	}
	if VarsTab() != len(tabs) {
		t.Errorf("VarsTab is %d, want %d: after the task tabs", VarsTab(), len(tabs))
	}
}
