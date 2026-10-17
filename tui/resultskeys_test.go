package tui

import (
	"strings"
	"testing"

	tuicore "github.com/yongjohnlee80/golib/tui"
	"github.com/yongjohnlee80/golib/tui/decl/decltest"
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

// A table yank and an editor put share the unnamed register. Sending both keys through the
// program catches a missing table-to-editor bridge or a focus path that swallows y.
func TestAResultsYankPastesIntoTheEditor(t *testing.T) {
	r, _ := kindsTUI(t, "tablestall")
	onLoop(r, func() bool { r.h.keep(r.h.p.Call(resultsView, "forceActiveFocus")); return true })
	r.s.WaitFor(t, "the results with the keys", func(string) bool { return r.focused(resultsView) })
	want := onLoop(r, func() string { return r.resultsCore().Cell(0, 0) })
	r.keys(t, key('y'))
	r.s.WaitFor(t, "the editor register holds the table cell", func(string) bool {
		return onLoop(r, func() bool { got, _ := r.h.core.Register(); return got == want })
	})
	before := r.editorText()
	onLoop(r, func() bool { r.h.keep(r.h.p.Call("editor", "forceActiveFocus")); return true })
	r.s.WaitFor(t, "the editor with the keys", func(string) bool { return r.focused("editor") })
	r.keys(t, key('p'))
	r.s.WaitFor(t, "the yanked cell pasted", func(string) bool { return r.editorText() != before })
	if got := r.editorText(); !strings.Contains(got, want) {
		t.Errorf("editor text %q does not contain yanked cell %q", got, want)
	}
}

// The results bar uses each clause's own parser forms: two id fields complete to a position,
// ORDER BY's bare number and WHERE's #number. Tab inserts it into the actual TextField.
func TestTheResultsBarTabInsertsAFieldTheClauseParses(t *testing.T) {
	r, _ := kindsTUI(t, "kinds")
	readText := func() string {
		return onLoop(r, func() string {
			node, ok := r.h.p.Tree().NodeByID("resultsText")
			if !ok {
				return "no resultsText"
			}
			value, err := r.h.p.Adapter().ReadProperty(node, "text")
			if err != nil {
				return err.Error()
			}
			return value.Raw
		})
	}
	focus := func() {
		onLoop(r, func() bool { r.h.keep(r.h.p.Call("resultsText", "forceActiveFocus")); return true })
		r.s.WaitFor(t, "the results bar has focus", func(string) bool { return r.focused("resultsText") })
	}
	focus()
	r.keys(t, decltest.Type("id")...)
	r.keys(t, tuicore.KeyEvent{Kind: tuicore.KeyPress, Code: tuicore.KeyTab})
	r.s.WaitFor(t, "ORDER BY Tab inserts the first id's position 1", func(string) bool { return readText() == "1" })
	r.keys(t, enter())
	r.s.WaitFor(t, "the completed ORDER BY is applied", func(string) bool { return r.source("App.resultsClauseText") == "1" })
	onLoop(r, func() bool { r.h.plugins.resultsClauseChosen(clauseWhere, "1"); return true })
	r.s.WaitFor(t, "WHERE starts empty", func(string) bool { return readText() == "" })
	focus()
	r.keys(t, decltest.Type("id")...)
	r.keys(t, tuicore.KeyEvent{Kind: tuicore.KeyPress, Code: tuicore.KeyTab})
	r.s.WaitFor(t, "WHERE Tab inserts the first id's position #1", func(string) bool { return readText() == "#1" })
}

// The s key cycles a sort over the loaded rows. A sort landing the cursor at the far end must not
// send plugin.table.fetch; only reading the table should do that.
func TestTheResultsSKeySortsWithoutFetching(t *testing.T) {
	dir := t.TempDir()
	installPlugin4(t, dir, "rows", "service", "tablestall", tableManifest)
	r, logs := pluginTUI(t, dir)
	onLoop(r, func() bool { r.h.plugins.runPluginCommand("rows", "run"); return true })
	r.waitResults(t, "q1", tablePage+resultsPage)
	fetches := len(saidLines(t, logs, "rows", "fetch "))
	onLoop(r, func() bool { r.h.keep(r.h.p.Call(resultsView, "forceActiveFocus")); return true })
	r.s.WaitFor(t, "results focused", func(string) bool { return r.focused(resultsView) })
	r.keys(t, key('s'), key('s')) // ascending then descending: row zero moves near the end
	r.wantNoFetchSince(t, logs, fetches)
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
