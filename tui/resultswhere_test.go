package tui

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/yongjohnlee80/golib/tui/widget"
)

// WHERE OVER THE LOADED ROWS — the bar's WHERE filters the rows the view holds, before its sort, and
// never asks the plugin for any: the note says what of the loaded rows it keeps, and the copies and
// exports take the rows kept, in the order shown.

// kindsRowsTSV is the kinds table's rows, in the order given, as Copy writes them.
func kindsRowsTSV(order ...int) string {
	var lines []string
	for _, r := range order {
		lines = append(lines, strings.Join(kindsRows[r], "\t"))
	}
	return strings.Join(lines, "\n")
}

// kindsNames is the name field of the kinds table's rows, in the order given.
func kindsNames(order ...int) []string {
	out := make([]string, len(order))
	for i, o := range order {
		out[i] = kindsRows[o][1]
	}
	return out
}

// TestWhereFiltersTheViewThenTheSort: WHERE keeps the loaded rows it is true of, by their kinds (a
// number past a float's digits exactly, a NULL only for IS NULL), and ORDER BY sorts what it kept;
// the note reads "N of M loaded rows". Text that does not parse says why and leaves the view as it
// was. Copy, the CSV and JSON exports and results.markdown take the rows kept, in the order shown,
// and the export dialog and notice count them against the rows loaded.
func TestWhereFiltersTheViewThenTheSort(t *testing.T) {
	r, _ := kindsTUI(t, "tablestall")
	onLoop(r, func() bool { r.h.plugins.resultsClauseChosen(clauseWhere, ""); return true })
	where := func(text string) {
		t.Helper()
		onLoop(r, func() bool { r.h.plugins.whereResults(text); return true })
	}
	where("id > 1")
	r.waitColumn(t, 1, kindsNames(0, 2, 3))
	if note, line := r.source("App.resultsViewNote"), r.source("App.resultsSortLine"); note != "3 of 5 loaded rows" || line != note {
		t.Errorf("the note %q, the line %q; want 3 of 5 loaded rows", note, line)
	}
	onLoop(r, func() bool { r.h.plugins.orderResults("id DESC"); return true })
	r.waitColumn(t, 1, kindsNames(3, 0, 2))

	onLoop(r, func() bool { r.resultsCore().SelectAll(); r.h.plugins.copyResults(formatTSV); return true })
	if got := string(r.s.Backend.Clipboard()); got != kindsRowsTSV(3, 0, 2) {
		t.Errorf("Copy of the filtered view:\n%q\nwant\n%q", got, kindsRowsTSV(3, 0, 2))
	}
	if err := onLoop(r, func() error { return r.h.cat.run("results.export", nil) }); err != nil {
		t.Fatal(err)
	}
	r.s.WaitForText(t, "Export 3 rows, as shown (filtered from 5 loaded)")
	if can := r.source("App.exportCanSave"); can != "true" {
		t.Errorf("the export dialog's accept on 3 rows: %s", can)
	}
	onLoop(r, func() bool { r.h.closeDialog("exportResults"); return true })
	out := t.TempDir()
	csvPath, jsonPath := filepath.Join(out, "kept.csv"), filepath.Join(out, "kept.json")
	onLoop(r, func() bool { r.h.plugins.exportFormatChosen(0); r.h.plugins.exportResults(csvPath); return true })
	r.waitNotice(t, "3 rows exported to "+csvPath+", as shown (filtered from 5 loaded)")
	records := readCSVFile(t, csvPath)
	var ids []string
	for _, rec := range records[1:] {
		ids = append(ids, rec[0])
	}
	if !slices.Equal(ids, []string{"10", "3", "2"}) {
		t.Errorf("the CSV export's ids: %q, want 10, 3, 2", ids)
	}
	onLoop(r, func() bool { r.h.plugins.exportFormatChosen(1); r.h.plugins.exportResults(jsonPath); return true })
	r.waitNotice(t, "3 rows exported to "+jsonPath)
	b, _ := os.ReadFile(jsonPath)
	var rows []map[string]any
	if err := json.Unmarshal(b, &rows); err != nil || len(rows) != 3 || fmt.Sprint(rows[0]["id"], rows[1]["id"], rows[2]["id"]) != "10 3 2" {
		t.Errorf("the JSON export: %v\n%s", err, b)
	}
	onLoop(r, func() bool {
		r.h.editor.SetValue("")
		r.h.core.SetCursor(widget.TextPos{})
		r.h.plugins.insertResults()
		return true
	})
	r.waitNotice(t, "3 rows inserted")
	if got := markdownNames(r.editorText()); !slices.Equal(got, kindsNames(3, 0, 2)) {
		t.Errorf("results.markdown's names: %q", got)
	}

	// by kind: the NULL name, and numbers past a float's digits compared exactly
	where("name IS NULL")
	r.waitColumn(t, 1, kindsNames(1))
	where("amount > 12345678901234567890.122")
	r.waitColumn(t, 1, kindsNames(0, 1, 4))
	// a WHERE that does not parse: its error, and the view as it was
	where("nam = 'x'")
	_, want := widget.ParseTableWhere("nam = 'x'", r.resultsCore().Fields())
	if want == nil || r.source("App.resultsSortLine") != want.Error() {
		t.Errorf("the line after WHERE nam = 'x': %q, want the parser's %v", r.source("App.resultsSortLine"), want)
	}
	r.waitColumn(t, 1, kindsNames(0, 1, 4))
	if note := r.source("App.resultsViewNote"); note != "3 of 5 loaded rows" {
		t.Errorf("the note under a WHERE that did not parse: %q", note)
	}
}

// TestAWhereThatKeepsNothing: a WHERE that keeps no row shows the empty table, no cursor on a row;
// Copy and Insert say there is nothing to take, and the export dialog counts 0 rows with its accept
// disabled. A WHERE that keeps rows again puts the cursor on the first.
func TestAWhereThatKeepsNothing(t *testing.T) {
	r, _ := kindsTUI(t, "tablestall")
	before := string(r.s.Backend.Clipboard())
	onLoop(r, func() bool { r.h.plugins.whereResults("id > 100"); return true })
	r.s.WaitFor(t, "the empty view", func(string) bool { return onLoop(r, func() int { return r.resultsCore().Len() }) == 0 })
	if note := r.source("App.resultsViewNote"); note != "0 of 5 loaded rows" {
		t.Errorf("the note: %q", note)
	}
	onLoop(r, func() bool { r.h.plugins.copyResults(formatTSV); r.h.plugins.insertResults(); return true })
	r.waitNotice(t, "nothing to copy: the WHERE keeps no row of the 5 loaded")
	r.waitNotice(t, "nothing to insert: the WHERE keeps no row of the 5 loaded")
	if got := string(r.s.Backend.Clipboard()); got != before {
		t.Errorf("Copy of the empty view put %q on the clipboard", got)
	}
	if err := onLoop(r, func() error { return r.h.cat.run("results.export", nil) }); err != nil {
		t.Fatal(err)
	}
	r.s.WaitForText(t, "Export 0 rows, as shown (filtered from 5 loaded)")
	if can := r.source("App.exportCanSave"); can != "false" {
		t.Errorf("the export dialog's accept on the empty view: %s, want disabled", can)
	}
	onLoop(r, func() bool { r.h.plugins.chooseExportFile(); return true })
	r.waitNotice(t, "nothing to export: the WHERE keeps no row of the 5 loaded")

	onLoop(r, func() bool { r.h.plugins.whereResults("id > 2"); return true })
	r.waitColumn(t, 1, kindsNames(0, 3))
	if got := onLoop(r, func() string {
		tc := r.resultsCore()
		_, _, sel := tc.Selection()
		return fmt.Sprintf("cursor %v, a selection %v", tc.Cursor(), sel)
	}); got != "cursor {0 0}, a selection false" {
		t.Errorf("after the empty view: %s", got)
	}
}

// TestAWhereFetchesNothing: on a 10 000-row result with 600 rows loaded, a WHERE that keeps nine of
// them sends the plugin nothing (the witness command, sent after, is all it hears), and the note
// counts them and says the result has more. Rows that load later, as the reader scrolls, are
// filtered as they arrive.
func TestAWhereFetchesNothing(t *testing.T) {
	r, logs := kindsTUI(t, "table")
	onLoop(r, func() bool { r.h.plugins.runPluginCommand("rows", "run"); return true })
	r.waitResults(t, "q1", tablePage+resultsPage)
	waitSaidN(t, logs, "rows", "fetch ", 1)
	before := said(t, logs, "rows")
	onLoop(r, func() bool { r.h.plugins.whereResults("n > 590"); return true })
	r.s.WaitFor(t, "nine rows kept", func(string) bool { return onLoop(r, func() int { return r.resultsCore().Len() }) == 9 })
	onLoop(r, func() bool { r.h.plugins.runPluginCommand("rows", "witness"); return true })
	waitSaidN(t, logs, "rows", "witness", 1)
	if got := said(t, logs, "rows")[len(before):]; !slices.Equal(got, []string{"witness"}) {
		t.Fatalf("the WHERE sent the plugin %q", got)
	}
	if note := r.source("App.resultsViewNote"); note != "9 of 600 loaded rows; the result has more: change the query to change what is returned" {
		t.Errorf("the note: %q", note)
	}
	// the reader moves to the last row shown: the next page loads, and its rows are filtered
	onLoop(r, func() bool { tc := r.resultsCore(); tc.SetCursor(widget.TablePos{Row: tc.Len() - 1}); return true })
	r.waitResults(t, "q1", tablePage+2*resultsPage)
	r.s.WaitFor(t, "the next page filtered", func(string) bool {
		return onLoop(r, func() int { return r.resultsCore().Len() }) == tablePage+2*resultsPage-591
	})
}

// TestTheWhereLineOffersFields: while WHERE's text is typed where a field goes (the start, after
// "(", AND, OR or NOT), the line offers the fields the word may name, a shared name as #position
// and a quoted name typed as itself; after an operator or a field, nothing.
func TestTheWhereLineOffersFields(t *testing.T) {
	r, _ := kindsTUI(t, "tablestall")
	onLoop(r, func() bool { r.h.plugins.resultsClauseChosen(clauseWhere, ""); return true })
	for typed, want := range map[string]string{"na": "fields: name", "id > 1 AND i": "fields: #1, #6", "(am": "fields: amount",
		`"do`: "fields: doc", "NOT fl": "fields: flag", "id >": "", "name ": "", "id = 1 OR zz": ""} {
		onLoop(r, func() bool { r.h.plugins.resultsClauseTyped(typed); return true })
		if line := r.source("App.resultsSortLine"); line != want {
			t.Errorf("typing %q: the line %q, want %q", typed, line, want)
		}
	}
}
