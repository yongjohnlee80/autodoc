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
