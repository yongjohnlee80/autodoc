package todos

import "maps"

// State is the todos panel as the host's cells read it (Snapshot): a copy, sharing nothing with
// the panel, so a cell that changes it changes nothing of the panel's.
type State struct {
	Tab      int            // the tab shown: one of Tabs, or VarsTab
	Stale    [5]bool        // the task tabs whose rows a change has outdated, read again when shown
	Vars     int            // the Variables tab's rows
	HasStore bool           // a store is shown
	Store    string         // the store shown's name ("" when none is)
	Released bool           // the store shown was released: the daemon only reads it
	Cursor   map[string]any // the task under the cursor, a copy; nil on Variables or with none
}

// Snapshot is the panel's state as the host's cells read it, on the loop: a copy (State).
func (t *Todos) Snapshot() State {
	s := State{Tab: t.tab, Stale: t.stale}
	if t.vars != nil {
		s.Vars = t.vars.Len()
	}
	if t.cur != nil {
		s.HasStore, s.Store, s.Released = true, t.cur.name, t.cur.released
	}
	if _, x := t.cursor(); x != nil {
		s.Cursor = maps.Clone(x)
	}
	return s
}

// Tab is one of the panel's task tabs: its table's id, its title, and the status (bucket) its tasks
// have.
type Tab struct{ Table, Title, Bucket string }

// Tabs are the panel's task tabs in order, a copy: the host's cells find each tab's table and title
// by them.
func Tabs() []Tab {
	out := make([]Tab, len(todoTabs))
	for i := range todoTabs {
		out[i] = Tab{Table: todoTabs[i].table, Title: todoBuckets[i].title, Bucket: todoBuckets[i].bucket}
	}
	return out
}

// VarsTab is the Variables tab's index, after the task tabs.
func VarsTab() int { return varsTab }

// HideStoreForTest is for the host's cells only; no production code calls it. It puts the panel
// as it is before its store list answers, no store shown, and answers what shows the store
// again: the host's TestTabReadWaitsForTheStore, whose helper (tabRead) must wait for the
// store, holds the panel there. It replaces that cell's direct set of the panel's unexported
// cur field, which a host cell can no longer reach. Both run on the loop.
func (t *Todos) HideStoreForTest() (restore func()) {
	cur := t.cur
	t.cur = nil
	return func() { t.cur = cur }
}

// SetVarsForTest is for the host's cells only; no production code calls it. It shows rows as
// the Variables tab's, as the daemon's answer would: the host's
// TestASwitchLeavesNoEnglishOnScreen reads a row a model composes in Go (a variable set per
// machine) in another language. It replaces that cell's direct Reset of the panel's unexported
// vars model, which a host cell can no longer reach. On the loop.
func (t *Todos) SetVarsForTest(rows []map[string]any) { t.vars.Reset(rows) }
