package tui

import (
	"encoding/csv"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	tuicore "github.com/yongjohnlee80/golib/tui"
	"github.com/yongjohnlee80/golib/tui/decl/decltest"
	"github.com/yongjohnlee80/golib/tui/widget"

	"github.com/yongjohnlee80/autodoc/plugin"
)

// THE RESULTS PANEL'S VIEW — protocol 5's kinds, the ORDER BY bar, the menu, the copies and the
// exports of the rows the view holds, through the real host and the SDK.

// installPlugin5 writes a protocol-5 manifest for the test plugin in mode: kind, then extra.
func installPlugin5(t *testing.T, dir, name, kind, mode, extra string) {
	t.Helper()
	installPlugin4(t, dir, name, kind, mode, extra)
	path := filepath.Join(dir, name, "plugin.toml")
	b, _ := os.ReadFile(path)
	if err := os.WriteFile(path, []byte(strings.Replace(string(b), "protocol = 4\n", "protocol = 5\n", 1)), 0o644); err != nil {
		t.Fatal(err)
	}
}

// The kinds plugin's table: five rows of six fields, the last named as the first. A NULL name, a
// number past a float's digits (as n, and as t), NaN as a number, a boolean that is none, JSON that
// is not.
var (
	kindsFields = []string{"id", "name", "amount", "flag", "doc", "id"}
	kindsRows   = [][]string{
		{"3", "carol", "12345678901234567890.124", "true", `{"a": [1, 2]}`, "x"},
		{"1", "NULL", "12345678901234567890.123", "false", "not json", "12345678901234567890.123"},
		{"2", "alice", "NaN", "maybe", "[]", "z"},
		{"10", "bob", "-0.5e3", "true", "null", "w"},
		{"1", "dave", "12345678901234567890.123", "t", `"s"`, "v"},
	}
	kindsOf = []string{"ntnbjt", "nznbjt", "ntnbjt", "ntnbjt", "ntnbjt"}
)

// kinded is the plugin's commands in modes "kinds" and "kindsbad" (protocol 5): in "kinds" run
// sends the whole kinds table, its kinds with it; in "kindsbad" run sends its first two rows with
// more to come, and run2 a table whose kinds are a byte short.
func (p *tablePlugin) kinded(id string) bool {
	switch {
	case p.mode == "kinds" && id == "run":
		err := p.peer.Table(plugin.TableOpen{ID: "k1", Fields: kindsFields, Rows: kindsRows, Kinds: kindsOf})
		p.say(fmt.Sprintf("table k1 %v", err))
	case p.mode == "kindsbad" && id == "run":
		err := p.peer.Table(plugin.TableOpen{ID: "k1", Fields: kindsFields, Rows: kindsRows[:2], Kinds: kindsOf[:2], More: true})
		p.say(fmt.Sprintf("table k1 %v", err))
	case p.mode == "kindsbad" && id == "run2":
		err := p.peer.Table(plugin.TableOpen{ID: "k2", Fields: kindsFields, Rows: kindsRows[:1], Kinds: []string{"ntnbj"}})
		p.say(fmt.Sprintf("table k2 %v", err))
	default:
		return false
	}
	return true
}

// fetchKinded answers a fetch in "kindsbad": the next rows, the first's kinds a byte short.
func (p *tablePlugin) fetchKinded(f plugin.TableFetch) bool {
	if p.mode != "kindsbad" {
		return p.mode == "kinds"
	}
	kinds := slices.Clone(kindsOf[2:])
	kinds[0] = kinds[0][:5]
	_ = p.peer.RowsKinds(f.ID, kindsRows[2:], kinds, false)
	return true
}

// source is a source of the program's App state, as the document reads it.
func (r *running) source(name string) string {
	return onLoop(r, func() string { v, _ := r.h.p.Tree().Source(name); return v.Raw })
}

// resultsCore is the results' table core, on the loop only.
func (r *running) resultsCore() *widget.TableCore { return r.h.plugins.c.TableCore(resultsView) }

// shownColumn is field col of every loaded row, as the view shows them, in its order.
func (r *running) shownColumn(col int) []string {
	return onLoop(r, func() []string {
		tc := r.resultsCore()
		out := make([]string, tc.Len())
		for d := range out {
			out[d] = tc.Cell(d, col)
		}
		return out
	})
}

// waitColumn waits until field col of the rows shown is want.
func (r *running) waitColumn(t *testing.T, col int, want []string) {
	t.Helper()
	r.s.WaitFor(t, fmt.Sprintf("field %d shown as %q", col, want), func(string) bool { return slices.Equal(r.shownColumn(col), want) })
}

// kindsTUI is a TUI whose plugin "kinds" has shown the kinds table, and whose plugin "rows" (a
// protocol-4 one, in mode) is ready to show its own.
func kindsTUI(t *testing.T, mode string) (*running, string) {
	t.Helper()
	dir := t.TempDir()
	installPlugin5(t, dir, "kinds", "service", "kinds", tableManifest)
	installPlugin4(t, dir, "rows", "service", mode, tableManifest)
	r, logs := pluginTUI(t, dir)
	onLoop(r, func() bool { r.h.plugins.runPluginCommand("kinds", "run"); return true })
	r.waitResults(t, "k1", len(kindsRows))
	return r, logs
}

// TestKindsReachTheTableFromProtocolFive: a protocol-5 table's kinds reach the model, and the view
// reads it through its kinds (golib's TableKinds), so it sorts by them; a protocol-4 table is
// shown through the model alone. The SDK's kind bytes are golib's.
func TestKindsReachTheTableFromProtocolFive(t *testing.T) {
	r, _ := kindsTUI(t, "tablestall")
	got := onLoop(r, func() string {
		m := r.h.plugins.results.model
		_, shown := r.h.plugins.results.shown.(kindedResults)
		_, viewKinds := r.resultsCore().Source().(widget.TableKinds)
		return fmt.Sprintf("kinded %v %q, shown with kinds %v, the view reads kinds %v, kind(1,1) %c", m.kinded, m.kinds, shown, viewKinds, m.kind(1, 1))
	})
	if want := fmt.Sprintf("kinded true %q, shown with kinds true, the view reads kinds true, kind(1,1) z", kindsOf); got != want {
		t.Fatalf("the kinds table: %s\nwant %s", got, want)
	}
	onLoop(r, func() bool { r.h.plugins.runPluginCommand("rows", "run"); return true })
	r.waitResults(t, "q1", tablePage)
	got = onLoop(r, func() string {
		m := r.h.plugins.results.model
		_, shown := r.h.plugins.results.shown.(*resultsModel)
		_, viewKinds := r.resultsCore().Source().(widget.TableKinds)
		return fmt.Sprintf("kinded %v %d, shown alone %v, the view reads kinds %v", m.kinded, len(m.kinds), shown, viewKinds)
	})
	if want := "kinded false 0, shown alone true, the view reads kinds false"; got != want {
		t.Fatalf("a protocol-4 table: %s, want %s", got, want)
	}
	for b, k := range map[byte]widget.TableCellKind{plugin.KindText: widget.TableCellText, plugin.KindNull: widget.TableCellNull,
		plugin.KindNumber: widget.TableCellNumber, plugin.KindBool: widget.TableCellBool, plugin.KindJSON: widget.TableCellJSON} {
		if b != byte(k) {
			t.Errorf("the SDK's kind %q is golib's %q", b, byte(k))
		}
	}
}

// TestAKindsPageThatDoesNotFitIsDropped: a host.rows page whose first kinds string is a byte short
// of the table's fields is dropped, noted, and ends the table's rows there, the rows before it kept
// as they were; a host.table whose kinds do not fit is refused, the table shown kept.
func TestAKindsPageThatDoesNotFitIsDropped(t *testing.T) {
	dir := t.TempDir()
	installPlugin5(t, dir, "kinds", "service", "kindsbad", tableManifest)
	r, logs := pluginTUI(t, dir)
	onLoop(r, func() bool { r.h.plugins.runPluginCommand("kinds", "run"); return true })
	r.waitResults(t, "k1", 2)
	why := "row 0's kinds are 5 bytes for 6 fields"
	r.s.WaitFor(t, "the page dropped", func(string) bool {
		return pluginLogHas(logs, "kinds", `host.rows for "k1" dropped: its kinds: `+why)
	})
	r.waitNotice(t, "kinds: the results ended early: a page of rows was malformed: its kinds: "+why)
	if got := onLoop(r, func() string {
		res := r.h.plugins.results
		return fmt.Sprintf("%d rows, kinds %q, more %v, pending %v, cut %v", len(res.model.rows), res.model.kinds, res.more, res.pending, res.cut)
	}); got != fmt.Sprintf("2 rows, kinds %q, more false, pending false, cut true", kindsOf[:2]) {
		t.Fatalf("after the page dropped: %s", got)
	}
	onLoop(r, func() bool { r.h.plugins.runPluginCommand("kinds", "run2"); return true })
	r.s.WaitFor(t, "the table refused", func(string) bool {
		return pluginLogHas(logs, "kinds", "a table refused: plugin: malformed parameters: host.table's kinds: "+why)
	})
	if id := onLoop(r, func() string { return r.h.plugins.results.id }); id != "k1" {
		t.Errorf("the table shown is %q after one refused", id)
	}
}

// TestTheSortIsInBuffer: with 600 rows of a 10 000-row result loaded, an ORDER BY sorts the loaded
// rows and sends the plugin nothing (whatever reaches it before the witness is said), and the note
// says it covers the loaded rows; on a whole result the note is empty. The rows are numbered, the
// gutter as wide as the last number.
func TestTheSortIsInBuffer(t *testing.T) {
	r, logs := kindsTUI(t, "tablestall")
	if note := r.source("App.resultsViewNote"); note != "" {
		t.Errorf("the note on a whole result: %q", note)
	}
	onLoop(r, func() bool { r.h.plugins.runPluginCommand("rows", "run"); return true })
	loaded := tablePage + resultsPage
	r.waitResults(t, "q1", loaded)
	note := "the result has more: change the query to change what is returned"
	r.s.WaitFor(t, "the note", func(string) bool { return r.source("App.resultsViewNote") == note })
	if line := r.source("App.resultsSortLine"); line != note {
		t.Errorf("the bar's line %q, want the note", line)
	}
	if got := onLoop(r, func() string {
		tc := r.resultsCore()
		return fmt.Sprintf("numbers %v, %d digits", tc.RowNumbers(), tc.RowNumberDigits())
	}); got != "numbers true, 3 digits" {
		t.Errorf("the gutter: %s", got)
	}

	waitSaidN(t, logs, "rows", "table q1", 1)
	waitSaidN(t, logs, "rows", "fetch ", 1)
	before := said(t, logs, "rows")
	onLoop(r, func() bool { r.h.plugins.orderResults("n DESC"); return true })
	r.s.WaitFor(t, "the loaded rows sorted", func(string) bool {
		return onLoop(r, func() bool { return r.resultsCore().Cell(0, 0) == fmt.Sprint(loaded-1) })
	})
	onLoop(r, func() bool { r.h.plugins.runPluginCommand("rows", "witness"); return true })
	waitSaidN(t, logs, "rows", "witness", 1)
	if got := said(t, logs, "rows")[len(before):]; !slices.Equal(got, []string{"witness"}) {
		t.Fatalf("the sort sent the plugin %q", got)
	}
	if onLoop(r, func() bool { return r.h.plugins.results.pending }) {
		t.Error("a fetch waits after the sort")
	}
}

// TestTheSortBarReadsTheFields: the bar's text sorts the kinds table as SQL would: a name two fields
// share is an error naming their positions, the rows left as they were; a position and a second
// key apply in order, NULLs last in both directions; numbers as exact decimals, NaN above them,
// equal rows in the query's order. While a name is typed the line offers the fields it may be, a
// shared one as its positions. The s key writes its sort back to the bar.
func TestTheSortBarReadsTheFields(t *testing.T) {
	r, _ := kindsTUI(t, "tablestall")
	names := func(order ...int) []string {
		out := make([]string, len(order))
		for i, o := range order {
			out[i] = kindsRows[o][1]
		}
		return out
	}
	onLoop(r, func() bool { r.h.plugins.orderResults("id"); return true })
	if line := r.source("App.resultsSortLine"); line != `"id" names fields 1 and 6: write its position` {
		t.Errorf("a shared name: %q", line)
	}
	r.waitColumn(t, 1, names(0, 1, 2, 3, 4))
	onLoop(r, func() bool { r.h.plugins.orderResults("1, name DESC"); return true })
	r.waitColumn(t, 1, names(4, 1, 2, 0, 3))
	if line := r.source("App.resultsSortLine"); line != "" {
		t.Errorf("the line after a sort that parsed: %q", line)
	}
	onLoop(r, func() bool { r.h.plugins.orderResults("amount"); return true })
	r.waitColumn(t, 1, names(3, 1, 4, 0, 2))
	onLoop(r, func() bool { r.h.plugins.orderResults("amount DESC"); return true })
	r.waitColumn(t, 1, names(2, 0, 1, 4, 3))

	for typed, want := range map[string]string{"na": "fields: name", "i": "fields: 1, 6", "1, AM": "fields: amount",
		`1, "do`: "fields: doc", "name ": "", "zz": ""} {
		onLoop(r, func() bool { r.h.plugins.resultsClauseTyped(typed); return true })
		if line := r.source("App.resultsSortLine"); line != want {
			t.Errorf("typing %q: the line %q, want %q", typed, line, want)
		}
	}

	onLoop(r, func() bool {
		r.h.keep(r.h.p.Call(resultsView, "forceActiveFocus"))
		r.resultsCore().SetCursor(widget.TablePos{Col: 1})
		return true
	})
	r.keys(t, key('s'))
	r.s.WaitFor(t, "s written to the bar", func(string) bool {
		return r.source("App.resultsOrder") == "name ASC" && r.source("App.resultsClauseText") == "name ASC"
	})
	onLoop(r, func() bool { r.resultsCore().SetCursor(widget.TablePos{Col: 0}); return true })
	r.keys(t, key('s'))
	r.s.WaitFor(t, "a shared name's s written as its position", func(string) bool { return r.source("App.resultsOrder") == "1 ASC" })
}

// wantJSON is the kinds table as the JSON export writes it, in its rows' order.
const wantJSON = `[
{"id":3,"name":"carol","amount":12345678901234567890.124,"flag":true,"doc":{"a":[1,2]},"id_2":"x"},
{"id":1,"name":null,"amount":12345678901234567890.123,"flag":false,"doc":"not json","id_2":"12345678901234567890.123"},
{"id":2,"name":"alice","amount":"NaN","flag":"maybe","doc":[],"id_2":"z"},
{"id":10,"name":"bob","amount":-0.5e3,"flag":true,"doc":null,"id_2":"w"},
{"id":1,"name":"dave","amount":12345678901234567890.123,"flag":true,"doc":"s","id_2":"v"}
]
`

// TestJSONIsExact: the JSON export writes each value by its kind: a number digit for digit (the
// same digits as text a string), NaN as a string, NULL as null, a boolean as one (one that is none
// as a string), JSON embedded (JSON that is not as a string), a second id as id_2. A name with no
// extension takes .json. A protocol-4 table's values are all strings. A failed write leaves no
// .part.
func TestJSONIsExact(t *testing.T) {
	r, _ := kindsTUI(t, "tablestall")
	out := t.TempDir()
	onLoop(r, func() bool {
		r.h.plugins.exportFormatChosen(1)
		r.h.plugins.exportResults(filepath.Join(out, "rows"))
		return true
	})
	path := filepath.Join(out, "rows.json")
	r.waitNotice(t, "5 rows exported to "+path)
	b, err := os.ReadFile(path)
	if err != nil || string(b) != wantJSON || !json.Valid(b) {
		t.Fatalf("the JSON (%v):\n%s\nwant:\n%s", err, b, wantJSON)
	}

	onLoop(r, func() bool { r.h.plugins.runPluginCommand("rows", "run"); return true })
	r.waitResults(t, "q1", tablePage+resultsPage)
	text := filepath.Join(out, "text.json")
	onLoop(r, func() bool { r.h.plugins.exportResults(text); return true })
	r.waitNotice(t, "loaded rows exported to "+text)
	b, _ = os.ReadFile(text)
	var rows []map[string]any
	if err := json.Unmarshal(b, &rows); err != nil || len(rows) != tablePage+resultsPage {
		t.Fatalf("the protocol-4 JSON: %d rows, %v", len(rows), err)
	}
	if first := strings.SplitN(string(b), "\n", 3)[1]; first != `{"n":"0","name":"a \"quoted\", value\nrow 0"},` {
		t.Errorf("a protocol-4 row: %s", first)
	}

	dirPath := filepath.Join(out, "taken.json")
	if err := os.Mkdir(dirPath, 0o755); err != nil {
		t.Fatal(err)
	}
	onLoop(r, func() bool { r.h.plugins.beginExport(dirPath); return true })
	r.waitNotice(t, dirPath+" not written")
	if _, err := os.Stat(dirPath + ".part"); err == nil {
		t.Error("a failed JSON export left its .part")
	}

	for s, want := range map[string]bool{"0": true, "-0": true, "12": true, "1.5": true, "1e5": true, "1E+5": true, "-0.5e-3": true,
		"01": false, "1.": false, ".5": false, "-": false, "1e": false, "+1": false, " 1": false, "NaN": false, "Infinity": false, "0x1": false} {
		if jsonNumber(s) != want {
			t.Errorf("jsonNumber(%q) = %v", s, !want)
		}
	}
	if got := jsonKeys([]string{"id", "id", "id_2", "id"}); !slices.Equal(got, []string{"id", "id_2", "id_2_2", "id_3"}) {
		t.Errorf("the keys of shared names: %q", got)
	}
	if got := jsonValue("a<b>&", 't'); got != `"a<b>&"` {
		t.Errorf("text with <>&: %s", got)
	}
}

// TestThePanelActsOnTheView: sorted by two keys, the loaded rows leave as shown: the CSV and JSON
// exports, a V selection's copies as TSV, CSV, JSON and Markdown, and results.markdown.
func TestThePanelActsOnTheView(t *testing.T) {
	r, _ := kindsTUI(t, "tablestall")
	order := []int{4, 1, 2, 0, 3}
	var names []string
	for _, o := range order {
		names = append(names, kindsRows[o][1])
	}
	onLoop(r, func() bool { r.h.plugins.orderResults("1, name DESC"); return true })
	r.waitColumn(t, 1, names)
	out := t.TempDir()
	csvPath, jsonPath := filepath.Join(out, "rows.csv"), filepath.Join(out, "rows.json")
	onLoop(r, func() bool { r.h.plugins.exportFormatChosen(0); r.h.plugins.exportResults(csvPath); return true })
	r.waitNotice(t, "5 rows exported to "+csvPath)
	records := readCSVFile(t, csvPath)
	csvNames := func(records [][]string) []string {
		var out []string
		for _, rec := range records[1:] {
			out = append(out, rec[1])
		}
		return out
	}
	if got := csvNames(records); !slices.Equal(got, names) || !slices.Equal(records[0], kindsFields) {
		t.Errorf("the CSV export: header %q, names %q; want %q", records[0], got, names)
	}
	onLoop(r, func() bool { r.h.plugins.exportFormatChosen(1); r.h.plugins.exportResults(jsonPath); return true })
	r.waitNotice(t, "5 rows exported to "+jsonPath)
	jsonNames := func(b []byte) []string {
		var rows []map[string]any
		if err := json.Unmarshal(b, &rows); err != nil {
			t.Fatalf("not JSON: %v\n%s", err, b)
		}
		var out []string
		for _, row := range rows {
			out = append(out, fmt.Sprint(row["name"]))
		}
		return out
	}
	shownNull := slices.Clone(names)
	shownNull[1] = "<nil>" // the NULL, as JSON's null reads back
	if b, _ := os.ReadFile(jsonPath); !slices.Equal(jsonNames(b), shownNull) {
		t.Errorf("the JSON export's names: %q, want %q", jsonNames(b), shownNull)
	}

	onLoop(r, func() bool { r.resultsCore().SelectAll(); return true })
	copied := func(format copyFormat) string {
		onLoop(r, func() bool { r.h.plugins.copyResults(format); return true })
		return string(r.s.Backend.Clipboard())
	}
	var tsv []string
	for line := range strings.SplitSeq(strings.TrimSuffix(copied(formatTSV), "\n"), "\n") {
		tsv = append(tsv, strings.Split(line, "\t")[1])
	}
	if !slices.Equal(tsv, names) {
		t.Errorf("the TSV copy's names: %q", tsv)
	}
	csvCopy, err := csv.NewReader(strings.NewReader(copied(formatCSV))).ReadAll()
	if err != nil || !slices.Equal(csvNames(csvCopy), names) {
		t.Errorf("the CSV copy: %q, %v", csvCopy, err)
	}
	if got := jsonNames([]byte(copied(formatJSON))); !slices.Equal(got, shownNull) {
		t.Errorf("the JSON copy's names: %q", got)
	}
	md := copied(formatMarkdown)
	if got := markdownNames(md); !slices.Equal(got, names) {
		t.Errorf("the Markdown copy's names: %q\n%s", got, md)
	}
	onLoop(r, func() bool {
		r.h.editor.SetValue("")
		r.h.core.SetCursor(widget.TextPos{})
		r.h.plugins.insertResults()
		return true
	})
	r.waitNotice(t, "5 rows inserted")
	if got := markdownNames(r.editorText()); !slices.Equal(got, names) {
		t.Errorf("results.markdown's names: %q", got)
	}
}

// markdownNames is the second cell of each row of a Markdown table in text.
func markdownNames(text string) []string {
	var out []string
	for line := range strings.SplitSeq(text, "\n") {
		cells := strings.Split(line, "|")
		if len(cells) < 3 || strings.HasPrefix(strings.TrimSpace(cells[1]), "---") || strings.TrimSpace(cells[1]) == "id" {
			continue
		}
		out = append(out, strings.TrimSpace(cells[2]))
	}
	return out
}

// TestTheExportDialogSaysWhatItTakes: on a whole result the dialog says how many rows it takes, and
// no line of more; JSON chosen, the save dialog's name ends .json and a name with no extension takes
// it; results.csv opens the dialog on CSV again, and results.export keeps the last choice.
func TestTheExportDialogSaysWhatItTakes(t *testing.T) {
	r, _ := kindsTUI(t, "tablestall")
	if err := onLoop(r, func() error { return r.h.cat.run("results.export", nil) }); err != nil {
		t.Fatal(err)
	}
	r.s.WaitForText(t, "Export 5 loaded rows, as shown")
	if more, shown := r.source("App.exportMore"), r.source("App.exportMoreShown"); more != "" || shown != "false" {
		t.Errorf("the line of more on a whole result: %q, shown %s", more, shown)
	}
	onLoop(r, func() bool { r.h.plugins.exportFormatChosen(1); r.h.plugins.chooseExportFile(); return true })
	r.s.WaitForText(t, "export the loaded rows to")
	if file := r.source("App.exportFile"); filepath.Base(file) != "results.json" {
		t.Errorf("the save dialog's name: %q", file)
	}
	path := filepath.Join(t.TempDir(), "plain")
	onLoop(r, func() bool { r.h.plugins.exportResults(path); return true })
	r.waitNotice(t, "5 rows exported to "+path+".json")

	if err := onLoop(r, func() error { return r.h.cat.run("results.csv", nil) }); err != nil {
		t.Fatal(err)
	}
	r.s.WaitFor(t, "results.csv on CSV", func(string) bool { return r.source("App.exportFormatIndex") == "0" })
	onLoop(r, func() bool { r.h.closeDialog("exportResults"); r.h.plugins.exportFormatChosen(1); return true })
	if err := onLoop(r, func() error { return r.h.cat.run("results.export", nil) }); err != nil {
		t.Fatal(err)
	}
	r.s.WaitFor(t, "results.export on the last choice", func(string) bool { return r.source("App.exportFormatIndex") == "1" })
}

// resultsMenuRow runs the results menu's row id, as choosing it does.
func (r *running) resultsMenuRow(t *testing.T, id string) {
	t.Helper()
	found := onLoop(r, func() bool {
		for _, row := range r.h.plugins.resultsMenuRows() {
			if string(row.ID) == "results.menu."+id {
				row.Action.(widget.CoreMenuAction).Run(nil)
				return true
			}
		}
		return false
	})
	if !found {
		t.Fatalf("no menu row %q", id)
	}
}

// TestTheMenuRowsRunTheirCommands: a right press on the name field, the cursor on another, opens
// the menu over the selection, which stays; its sort takes the field pressed on. Each row runs its
// command: the copies, Select all, the sorts and Unsort, Export loaded rows… and Insert as
// Markdown. A copy past the cap copies nothing and says to export.
func TestTheMenuRowsRunTheirCommands(t *testing.T) {
	r, _ := kindsTUI(t, "tablestall")
	onLoop(r, func() bool {
		tc := r.resultsCore()
		tc.SetCursor(widget.TablePos{Row: 1})
		tc.Select(widget.TableSelectRows, widget.TablePos{})
		x := resultsGutter(tc) + tc.Width(0) + 2 + 1 // inside the name field
		r.h.plugins.c.ResultsTable(resultsView).HandleEvent(tuicore.MouseEvent{Kind: tuicore.MousePress, Button: tuicore.MouseRight, X: x, Y: resultsHeaderRows + 2})
		return true
	})
	r.s.WaitForText(t, "Sort ascending by name")
	if got := onLoop(r, func() string {
		tc := r.resultsCore()
		from, to, _ := tc.Selection()
		at, hit := r.h.plugins.c.ResultsTable(resultsView).MenuAt()
		return fmt.Sprintf("open %v, selection %v %v to %v, at %v %v", r.h.plugins.c.ResultsTable(resultsView).Menu().IsOpen(),
			tc.SelectionKind(), from, to, at, hit)
	}); got != fmt.Sprintf("open true, selection %v {0 0} to {1 0}, at {2 1} %v", widget.TableSelectRows, widget.TableHitCell) {
		t.Fatalf("after the right press: %s", got)
	}
	r.resultsMenuRow(t, "sort_asc")
	r.s.WaitFor(t, "the menu's sort on the field pressed", func(string) bool { return r.source("App.resultsOrder") == "name ASC" })
	r.resultsMenuRow(t, "sort_desc")
	r.s.WaitFor(t, "descending", func(string) bool { return r.source("App.resultsOrder") == "name DESC" })
	r.resultsMenuRow(t, "unsort")
	r.s.WaitFor(t, "unsorted", func(string) bool { return r.source("App.resultsOrder") == "" })
	onLoop(r, func() bool { r.h.plugins.c.ResultsTable(resultsView).Menu().Close(); return true })

	for id, want := range map[string]string{"copy": "1\tNULL", "copy_csv": "id,name,amount", "copy_json": "[\n{\"id\":3,", "copy_markdown": "| id "} {
		r.resultsMenuRow(t, id)
		r.s.WaitFor(t, id+" on the clipboard", func(string) bool { return strings.Contains(string(r.s.Backend.Clipboard()), want) })
	}
	r.resultsMenuRow(t, "select_all")
	if got := onLoop(r, func() string {
		from, to, _ := r.resultsCore().Selection()
		return fmt.Sprintf("%v %v %v", r.resultsCore().SelectionKind(), from.Row, to.Row)
	}); got != fmt.Sprintf("%v 0 4", widget.TableSelectRows) {
		t.Errorf("Select all: %s", got)
	}
	r.resultsMenuRow(t, "export")
	r.s.WaitForText(t, "Export 5 loaded rows, as shown")
	onLoop(r, func() bool { r.h.closeDialog("exportResults"); r.h.editor.SetValue(""); return true })
	r.resultsMenuRow(t, "markdown")
	r.waitNotice(t, "5 rows inserted")
	if !strings.Contains(r.editorText(), "| carol ") {
		t.Errorf("Insert as Markdown: %q", r.editorText())
	}

	limit := resultsCopyCap
	resultsCopyCap = 64
	t.Cleanup(func() { resultsCopyCap = limit })
	before := string(r.s.Backend.Clipboard())
	for _, format := range []copyFormat{formatTSV, formatCSV, formatJSON, formatMarkdown} {
		onLoop(r, func() bool { r.h.plugins.copyResults(format); return true })
		if got := string(r.s.Backend.Clipboard()); got != before {
			t.Fatalf("%s past the cap copied %d bytes", format, len(got))
		}
	}
	r.waitNotice(t, "nothing copied: the selection is more than the 64 bytes a copy takes; use Export loaded rows… to write it to a file")
}

// TestTheBlurredCursor: with the keys, the results' cursor is in the highlight; once the editor has
// them, it is in the theme's inactive highlight (not the faint highlight a table has without it);
// when the keys come back, the highlight again.
func TestTheBlurredCursor(t *testing.T) {
	r, _ := kindsTUI(t, "tablestall")
	look := func(id string) string {
		return onLoop(r, func() string {
			r.h.keep(r.h.p.Call(id, "forceActiveFocus"))
			dt := r.h.plugins.c.ResultsTable(resultsView)
			st := dt.Styles()
			switch dt.CursorStyle() {
			case st.Cursor:
				return "focused"
			case st.CursorBlurred:
				if st.CursorBlurred == st.Cursor.Faint(true) {
					return "blurred, faint"
				}
				if bg, ok := st.CursorBlurred.GetBackground(); ok {
					if want, _ := st.Cursor.GetBackground(); bg != want {
						return "blurred"
					}
				}
				return "blurred, as the cursor"
			}
			return "neither"
		})
	}
	for _, c := range []struct{ id, want string }{{resultsView, "focused"}, {"editor", "blurred"}, {resultsView, "focused"}} {
		if got := look(c.id); got != c.want {
			t.Errorf("with the keys in %s: the cursor %s, want %s", c.id, got, c.want)
		}
	}
}

// TestTheBarIsAClauseAndItsText: the bar's dropdown shows ORDER BY chosen, of ORDER BY and WHERE;
// text typed into its input and entered sorts the rows. Each clause keeps its own text as the
// dropdown moves between them, and WHERE's Enter filters the rows the sort orders.
func TestTheBarIsAClauseAndItsText(t *testing.T) {
	r, _ := kindsTUI(t, "tablestall")
	r.s.WaitForText(t, "ORDER BY")
	read := func(id, prop string) string {
		return onLoop(r, func() string {
			node, ok := r.h.p.Tree().NodeByID(id)
			if !ok {
				return "no " + id
			}
			v, err := r.h.p.Adapter().ReadProperty(node, prop)
			if err != nil {
				return err.Error()
			}
			return v.Raw
		})
	}
	if got := read("resultsClause", "currentIndex") + " " + read("resultsClause", "currentValue"); got != "0 orderBy" {
		t.Fatalf("the dropdown: %s, want ORDER BY chosen", got)
	}
	onLoop(r, func() bool { r.h.keep(r.h.p.Call("resultsText", "forceActiveFocus")); return true })
	r.s.WaitFor(t, "the bar's input with the keys", func(string) bool { return r.focused("resultsText") })
	r.keys(t, decltest.Type("name DESC")...)
	r.keys(t, enter())
	names := func(order ...int) []string {
		out := make([]string, len(order))
		for i, o := range order {
			out[i] = kindsRows[o][1]
		}
		return out
	}
	r.waitColumn(t, 1, names(4, 0, 3, 2, 1))
	if got := r.source("App.resultsOrder"); got != "name DESC" {
		t.Errorf("orderBy after Enter: %q", got)
	}

	onLoop(r, func() bool { r.h.plugins.resultsClauseChosen(clauseWhere, "name DESC"); return true })
	r.s.WaitFor(t, "WHERE chosen, its own text empty", func(string) bool {
		return read("resultsClause", "currentIndex") == "1" && read("resultsText", "text") == ""
	})
	onLoop(r, func() bool { r.h.plugins.applyResultsClause("#1 > 1"); return true })
	r.waitColumn(t, 1, names(0, 3, 2)) // #1 > 1 kept (the kinds table names two fields id), then name DESC
	if line := r.source("App.resultsSortLine"); line != "3 of 5 loaded rows" {
		t.Errorf("WHERE's Enter: the line %q", line)
	}
	onLoop(r, func() bool { r.h.plugins.resultsClauseChosen(clauseOrderBy, "#1 > 1"); return true })
	r.s.WaitFor(t, "ORDER BY's own text back", func(string) bool { return read("resultsText", "text") == "name DESC" })
	if line := r.source("App.resultsSortLine"); line != "3 of 5 loaded rows" {
		t.Errorf("ORDER BY's line: %q", line)
	}
	onLoop(r, func() bool { r.h.plugins.resultsClauseChosen(clauseWhere, "name DESC"); return true })
	r.s.WaitFor(t, "WHERE's own text kept", func(string) bool { return read("resultsText", "text") == "#1 > 1" })
}
