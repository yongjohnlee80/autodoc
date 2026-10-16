package tui

import (
	"testing"

	"github.com/yongjohnlee80/golib/tui/widget"
)

// CTRL+V IN THE RESULTS — vim's block key is the table's while it has the keys: no key of
// AutoDoc's takes it first, in the TUI (here) and the GUI (gui_resultspanel_test.go).

// TestCtrlVReachesTheResultsTable: with the keys in the results, Ctrl+V starts a block, which j and
// l grow: AutoDoc's own keys never take it.
func TestCtrlVReachesTheResultsTable(t *testing.T) {
	r, _ := kindsTUI(t, "tablestall")
	wantCtrlVBlock(t, r)
}

// wantCtrlVBlock focuses the results table, sends Ctrl+V, j and l through the screen, and checks a
// block from the first cell to the second row's second.
func wantCtrlVBlock(t *testing.T, r *running) {
	t.Helper()
	text := r.editorText()
	onLoop(r, func() bool { r.h.keep(r.h.p.Call(resultsView, "forceActiveFocus")); return true })
	r.s.WaitFor(t, "the results with the keys", func(string) bool { return r.focused(resultsView) })
	r.keys(t, ctrlKey('v'), key('j'), key('l'))
	r.s.WaitFor(t, "a block from Ctrl+V", func(string) bool {
		return onLoop(r, func() bool {
			tc := r.resultsCore()
			from, to, ok := tc.Selection()
			return ok && tc.SelectionKind() == widget.TableSelectBlock && from == (widget.TablePos{}) && to == (widget.TablePos{Row: 1, Col: 1})
		})
	})
	if r.editorText() != text {
		t.Errorf("the page changed: %q", r.editorText())
	}
}

// TestEscClosesTheResultsOnceNothingIsPending: in the results, Esc first ends what the table holds
// (a selection, a count typed), the panel left open; with nothing pending it closes the panel.
func TestEscClosesTheResultsOnceNothingIsPending(t *testing.T) {
	r, _ := kindsTUI(t, "tablestall")
	wantEscCloses(t, r, func() {
		onLoop(r, func() bool { r.h.plugins.runPluginCommand("kinds", "run"); return true })
		r.waitResults(t, "k1", len(kindsRows))
	})
}

// wantEscCloses checks Esc in the results three times over, showing the table again with show
// after each close: a selection ended by the first Esc (the panel open) and the panel closed by the
// second; a count dropped by the first (a j after it moves one row, the panel open) and the panel
// closed by the second; with nothing pending, the panel closed by the first.
func wantEscCloses(t *testing.T, r *running, show func()) {
	t.Helper()
	open := func() bool { return onLoop(r, func() bool { return r.h.panelOpen[resultsPanel] }) }
	focus := func() {
		t.Helper()
		onLoop(r, func() bool { r.h.keep(r.h.p.Call(resultsView, "forceActiveFocus")); return true })
		r.s.WaitFor(t, "the results with the keys", func(string) bool { return r.focused(resultsView) })
	}
	row := func() int { return onLoop(r, func() int { return r.resultsCore().Cursor().Row }) }
	closedBy := func(what string) {
		t.Helper()
		r.keys(t, esc())
		r.s.WaitFor(t, "the panel closed by "+what, func(string) bool { return !open() })
	}

	focus()
	r.keys(t, key('V'))
	selected := func() bool { return onLoop(r, func() bool { _, _, ok := r.resultsCore().Selection(); return ok }) }
	r.s.WaitFor(t, "a selection made", func(string) bool { return selected() })
	r.keys(t, esc())
	r.s.WaitFor(t, "the selection ended by Esc", func(string) bool { return !selected() })
	if !open() {
		t.Fatal("the Esc that ended the selection closed the panel")
	}
	closedBy("a second Esc after the selection")
	show()

	focus()
	from := row()
	r.keys(t, key('3'), esc(), key('j'))
	r.s.WaitFor(t, "j moved one row after Esc dropped the count", func(string) bool { return row() == from+1 })
	if !open() {
		t.Fatal("the Esc that dropped the count closed the panel")
	}
	closedBy("a second Esc after the count")
	show()

	focus()
	closedBy("an Esc with nothing pending")
}
