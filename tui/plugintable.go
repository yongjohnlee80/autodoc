package tui

import (
	"cmp"
	"encoding/csv"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/yongjohnlee80/golib/parse/qml"
	tuicore "github.com/yongjohnlee80/golib/tui"
	tuidecl "github.com/yongjohnlee80/golib/tui/decl"

	"github.com/yongjohnlee80/autodoc/plugin"
)

// THE RESULTS PANEL — a table a plugin sends (protocol 4: host.table), shown by AutoDoc itself in a
// panel docked at the bottom: golib's cell-cursor table, its loaded rows sortable. A plugin whose
// manifest declares [table] sends its fields and first rows; the table asks for more as its cursor
// nears the end of those loaded, and the plugin answers each request (plugin.table.fetch) with the
// next rows (host.rows), or ends them early (host.table_end). One request waits at a time. A newer
// table replaces the one shown, whoever's it was, and its plugin is told the old one is gone
// (plugin.table.close), as it is when the panel closes; the plugin frees what serves its rows.
//
// A table's id is new for each table: one sent again under the id of the table shown is refused,
// since a fetch for the old one may still be on its way and its rows would join the new. Rows are
// taken only as the answer to the fetch that waits.
//
// The panel holds at most resultsResident rows: a query of millions is read a page at a time, never
// whole. At the cap the table stops asking for more and its heading says more remain. The CSV
// export takes them all: the rows past the cap go to its file, never to the panel.
//
// The rows leave through AutoDoc's own commands, each saying which rows it takes: results.copy the
// selected cells (or the cursor's), results.markdown the rows loaded now, into the page, and
// results.csv every row, fetched to the last, into a file.

// resultsPanel is the panel's id, and resultsView its table's, which takes the keyboard.
const (
	resultsPanel = "results"
	resultsView  = "resultsTable"
)

// resultsPage is how many rows one plugin.table.fetch asks for. The plugin may send fewer: it sizes
// a page by its encoded bytes, to stay under the link's limit.
const resultsPage = 500

// toastExport is the CSV export's toast, updated as rows are written.
const toastExport = "results-export"

// resultsResident is the most rows the panel holds (a variable, so a test can lower it). Past it a
// query's rows are not loaded into the table; an export writes them and lets them go.
var resultsResident = 100_000

// resultsTable is the table shown: whose it is, its id there, and the plugin's paging. Loop-owned.
type resultsTable struct {
	model *resultsModel
	owner *pluginRun // nil when no plugin serves it: none shown, or its plugin closed
	id    string     // the plugin's id for it; "" when none is shown
	title string
	// more is the last message's: rows remain to fetch. pending is a fetch sent and not yet
	// answered: another is not sent until host.rows or host.table_end answers it. cut is rows that
	// remained when the table's rows ended early (host.table_end, or its plugin closed): what is
	// loaded is not every row, so no export claims it is.
	more, pending, cut bool
	// over are rows that came past the panel's cap, not shown, waiting for an export to write them;
	// spilled is rows past the cap that an export wrote and let go, so the table is not all of them.
	over    [][]string
	spilled bool
}

// full reports whether the panel holds all the rows it may.
func (res *resultsTable) full() bool { return len(res.model.rows) >= resultsResident }

// resultsModel is the table's rows as a TableView reads them: its fields as the columns, its cells
// as text, and a Fetcher at the root, so the cell-cursor view asks for more as it nears the end.
// It keeps the cells as the plugin sent them, for the CSV export (which keeps a line break in a
// cell); the view is given them on one line. Loop-owned, as every model.
type resultsModel struct {
	t      *resultsTable
	fetch  func()
	fields []string
	rows   [][]string
	subs   map[int]func(tuidecl.Change)
	next   int
}

func newResultsModel(t *resultsTable, fetch func()) *resultsModel {
	return &resultsModel{t: t, fetch: fetch, subs: map[int]func(tuidecl.Change){}}
}

// reset replaces the fields and the rows.
func (m *resultsModel) reset(fields []string, rows [][]string) {
	m.fields, m.rows = fields, nil
	m.rows = append(m.rows, rows...)
	m.notify(tuidecl.Change{Kind: tuidecl.Reset})
}

// add appends rows.
func (m *resultsModel) add(rows [][]string) {
	if len(rows) == 0 {
		m.notify(tuidecl.Change{Kind: tuidecl.Changed, First: 0, Last: max(len(m.rows)-1, 0)})
		return
	}
	first := len(m.rows)
	m.rows = append(m.rows, rows...)
	m.notify(tuidecl.Change{Kind: tuidecl.Inserted, First: first, Last: len(m.rows) - 1})
}

func (m *resultsModel) notify(c tuidecl.Change) {
	for _, fn := range m.subs {
		fn(c)
	}
}

func (m *resultsModel) RowCount(parent *tuidecl.Index) int {
	if parent != nil {
		return 0
	}
	return len(m.rows)
}

func (m *resultsModel) ColumnCount(parent *tuidecl.Index) int {
	if parent != nil {
		return 0
	}
	return len(m.fields)
}

// Data is a cell's text on one line: a line break or a tab shows as a space.
func (m *resultsModel) Data(ix tuidecl.Index, role string) qml.SpecValue {
	col := ix.Column
	if role != "" {
		n, err := strconv.Atoi(strings.TrimPrefix(role, "c"))
		if err != nil || !strings.HasPrefix(role, "c") {
			return qml.SpecValue{}
		}
		col = n
	}
	if ix.Parent != nil || ix.Row < 0 || ix.Row >= len(m.rows) || col < 0 || col >= len(m.fields) {
		return qml.SpecValue{}
	}
	cell := ""
	if row := m.rows[ix.Row]; col < len(row) {
		cell = oneLine(row[col])
	}
	return qml.SpecValue{Kind: qml.SpecValueString, Raw: cell}
}

func (m *resultsModel) HeaderData(column int) string {
	if column < 0 || column >= len(m.fields) {
		return ""
	}
	return m.fields[column]
}

// Roles are the columns, c0 to cN.
func (m *resultsModel) Roles() []string {
	out := make([]string, len(m.fields))
	for i := range out {
		out[i] = "c" + strconv.Itoa(i)
	}
	return out
}

func (m *resultsModel) Key(ix tuidecl.Index) string { return strconv.Itoa(ix.Row) }

func (m *resultsModel) Subscribe(fn func(tuidecl.Change)) (cancel func()) {
	id := m.next
	m.next++
	m.subs[id] = fn
	return func() { delete(m.subs, id) }
}

// CanFetchMore is the last message's more, at the root, while the panel is under its cap: it stays
// true while a fetch is answered, and is false once the panel holds resultsResident rows, so the
// view stops asking (the export takes the rest).
func (m *resultsModel) CanFetchMore(ix tuidecl.Index) bool {
	return ix.Parent == nil && m.t.more && !m.t.full()
}

// FetchMore asks the plugin for the next rows; one waiting already is not asked again.
func (m *resultsModel) FetchMore(ix tuidecl.Index) {
	if ix.Parent == nil {
		m.fetch()
	}
}

var _ tuidecl.Fetcher = (*resultsModel)(nil)

// oneLine is a cell's text as a table row shows it.
var oneLine = strings.NewReplacer("\r\n", " ", "\n", " ", "\r", " ", "\t", " ").Replace

// ---------------------------------------------------------------- the plugin's messages

// onTable is a protocol-4 plugin's table messages, read on the link's goroutine and handled on the
// loop. One from a plugin whose manifest declares no [table], or before it is ready, is logged and
// dropped.
func (r *pluginRun) onTable(method string, params []any) {
	switch {
	case r.e.m.Table.Title == "":
		r.log.note(method + " dropped: the manifest declares no [table]")
		return
	case !r.readyIn.Load():
		r.log.note(method + " dropped: sent before host.ready")
		return
	}
	switch method {
	case plugin.MethodTable:
		t, dropped, err := plugin.ReadTable(params)
		if err != nil {
			r.log.note("a table refused: " + err.Error())
			return
		}
		r.noteDropped(method, dropped)
		r.pl.c.Post(func() { r.pl.tableOpened(r, t) })
	case plugin.MethodRows:
		id, rows, more, dropped, err := plugin.ReadRows(params)
		if err != nil {
			r.log.note("rows refused: " + err.Error())
			return
		}
		r.noteDropped(method, dropped)
		r.pl.c.Post(func() { r.pl.tableRows(r, id, rows, more) })
	case plugin.MethodTableEnd:
		id, why, err := plugin.ReadTableEnd(params)
		if err != nil {
			r.log.note("a table's end refused: " + err.Error())
			return
		}
		r.pl.c.Post(func() { r.pl.tableEnded(r, id, why) })
	}
}

func (r *pluginRun) noteDropped(method string, n int) {
	if n > 0 {
		r.log.note(fmt.Sprintf("%s had %d malformed rows or cells", method, n))
	}
}

// tableOpened is host.table: it replaces the table shown, and the panel opens if it is closed,
// leaving the keyboard where it is.
func (pl *plugins) tableOpened(r *pluginRun, t plugin.TableOpen) {
	if r.closing || pl.running[r.e.key()] != r {
		return // its plugin closed while the table was on its way
	}
	res := pl.results
	if res.owner == r && res.id == t.ID {
		// a fetch for the table shown may be on its way: under the same id its rows would join the
		// new table's, and an export of it would write the two mixed
		r.log.note(fmt.Sprintf("host.table reused the shown table's id %q; a new table needs a new id", t.ID))
		return
	}
	pl.closeTable("a newer table replaced it")
	res.owner, res.id, res.title, res.more, res.pending, res.cut = r, t.ID, cmp.Or(t.Title, r.e.m.Table.Title), t.More, false, false
	res.over, res.spilled = nil, false
	res.model.reset(t.Fields, t.Rows[:min(len(t.Rows), resultsResident)])
	res.over = append(res.over, t.Rows[min(len(t.Rows), resultsResident):]...)
	pl.showResultsTitle()
	if !pl.c.PanelOpen(resultsPanel) {
		pl.c.Keep(pl.c.View(resultsPanel, "open"))
	}
}

// tableRows is host.rows: the next rows of the table shown, answering the fetch that waits. Rows
// for a table no longer shown are dropped, and so are rows no fetch asked for. Rows past the
// panel's cap are kept apart (over) for the export, never shown.
func (pl *plugins) tableRows(r *pluginRun, id string, rows [][]string, more bool) {
	res := pl.results
	if res.owner != r || res.id != id {
		return
	}
	if !res.pending {
		r.log.note(fmt.Sprintf("host.rows for %q dropped: no fetch is waiting for them", id))
		return
	}
	res.pending, res.more = false, more
	keep := min(max(resultsResident-len(res.model.rows), 0), len(rows))
	res.over = append(res.over, rows[keep:]...)
	res.model.add(rows[:keep])
	pl.showResultsTitle()
	pl.exportPump()
}

// tableEnded is host.table_end: no more rows will come, and why when it failed. An export waiting
// for them stops, leaving nothing.
func (pl *plugins) tableEnded(r *pluginRun, id, why string) {
	res := pl.results
	if res.owner != r || res.id != id {
		return
	}
	res.cut = res.cut || res.more
	res.pending, res.more = false, false
	res.model.add(nil)
	pl.showResultsTitle()
	if why != "" {
		pl.c.Notify(r.e.m.Name + ": the results ended early: " + why)
	}
	pl.cancelExport("the results ended early" + cmp.Or(": "+why, ""))
}

// fetchRows asks the table's plugin for its next rows, unless one request waits already, no rows
// remain, or no plugin serves the table. With no export it asks only for what the panel has room
// for, and nothing at its cap; an export asks for whole pages, which go past the cap to its file.
func (pl *plugins) fetchRows() {
	res := pl.results
	if res.owner == nil || res.owner.closing || !res.more || res.pending {
		return
	}
	n := resultsPage
	if pl.export == nil {
		if n = min(n, resultsResident-len(res.model.rows)); n <= 0 || len(res.over) > 0 {
			return
		}
	}
	res.pending = true
	res.owner.send(plugin.MethodFetch, plugin.FetchParams(plugin.TableFetch{ID: res.id, N: n}))
}

// closeTable lets go of the table shown: its plugin is told it is gone, and an export of it stops,
// saying why. The rows stay in the model until something replaces them.
func (pl *plugins) closeTable(why string) {
	res := pl.results
	if res.id == "" {
		return
	}
	if res.owner != nil && !res.owner.closing {
		res.owner.send(plugin.MethodTableGone, plugin.TableGoneParams(res.id))
	}
	pl.cancelExport(why)
	res.owner, res.id, res.more, res.pending = nil, "", false, false
}

// tableOwnerGone is a plugin closing: the table it served has no more rows to give, and an export
// of it stops. Its rows stay shown.
func (pl *plugins) tableOwnerGone(r *pluginRun) {
	res := pl.results
	if res.owner != r {
		return
	}
	pl.cancelExport(r.e.m.Name + " closed")
	res.cut = res.cut || res.more
	res.owner, res.more, res.pending = nil, false, false
	res.model.add(nil)
	pl.showResultsTitle()
}

// resultsClosed is the panel closed (Esc, q, or Go › Results): the table is let go, and the panel
// holds nothing until the next.
func (pl *plugins) resultsClosed() {
	pl.closeTable("the results panel closed")
	pl.results.title, pl.results.cut, pl.results.over, pl.results.spilled = "", false, nil, false
	pl.results.model.reset(nil, nil)
	pl.showResultsTitle()
}

// showResultsTitle is the panel's heading: the table's title, its rows, and whether more remain.
func (pl *plugins) showResultsTitle() {
	res := pl.results
	title := "results"
	if res.title != "" {
		title += ": " + res.title
	}
	if n := len(res.model.rows); n > 0 || res.id != "" {
		switch {
		case (res.more || len(res.over) > 0) && res.full():
			title += fmt.Sprintf(" · the first %d rows shown; more remain (Export CSV takes them all)", n)
		case res.spilled:
			title += fmt.Sprintf(" · the first %d rows shown; the rest were exported, not kept", n)
		case res.more:
			title += fmt.Sprintf(" · %d rows, more to load", n)
		case res.cut:
			title += fmt.Sprintf(" · %d rows, ended early", n)
		default:
			title += fmt.Sprintf(" · %d rows", n)
		}
	}
	if x := pl.export; x != nil {
		title += fmt.Sprintf(" · exporting: %d rows", x.written)
	}
	pl.c.Set("App.resultsTitle", title)
}

// ---------------------------------------------------------------- copy and Markdown

// copyResults is results.copy: the selected cells, or the cursor's, as TSV.
func (pl *plugins) copyResults() {
	core := pl.c.TableCore(resultsView)
	if core == nil || core.Len() == 0 {
		pl.c.Notify("no results to copy")
		return
	}
	if !core.Copy() {
		pl.c.Notify("the results were not copied: no clipboard")
		return
	}
	pl.c.Say("results copied")
}

// insertResults is results.markdown: the rows loaded now, as the table shows them (its sort
// applied), a Markdown table at the editor's cursor. While rows remain unloaded the notice says so,
// and that the CSV export takes them all.
func (pl *plugins) insertResults() {
	core := pl.c.TableCore(resultsView)
	if core == nil || len(core.Fields()) == 0 {
		pl.c.Notify("no results to insert")
		return
	}
	fields := make([]string, len(core.Fields()))
	for i, f := range core.Fields() {
		fields[i] = f.Title
	}
	rows := core.Rows(0, core.Len())
	if err := pl.c.InsertBlock(markdownTable(fields, rows)); err != nil {
		pl.c.Failed("insert the results", err)
		return
	}
	if pl.results.more {
		pl.c.Notify(fmt.Sprintf("%d loaded rows inserted; more remain (Export all rows as CSV takes them all)", len(rows)))
		return
	}
	pl.c.Notify(fmt.Sprintf("%d rows inserted", len(rows)))
}

// markdownTable is fields and rows as a Markdown table, its columns padded to their widest cell: a
// | in a cell is escaped, so it stays one cell, and a cell is one line.
func markdownTable(fields []string, rows [][]string) string {
	cell := func(s string) string { return strings.ReplaceAll(oneLine(s), "|", `\|`) }
	widths := make([]int, len(fields))
	grid := make([][]string, 0, len(rows)+1)
	for _, r := range append([][]string{fields}, rows...) {
		line := make([]string, len(fields))
		for i := range fields {
			if i < len(r) {
				line[i] = cell(r[i])
			}
			widths[i] = max(widths[i], tuicore.StringWidth(line[i]), 3)
		}
		grid = append(grid, line)
	}
	var b strings.Builder
	write := func(line []string) {
		b.WriteString("|")
		for i, c := range line {
			b.WriteString(" " + c + strings.Repeat(" ", widths[i]-tuicore.StringWidth(c)) + " |")
		}
		b.WriteString("\n")
	}
	write(grid[0])
	b.WriteString("|")
	for _, w := range widths {
		b.WriteString(" " + strings.Repeat("-", w) + " |")
	}
	b.WriteString("\n")
	for _, line := range grid[1:] {
		write(line)
	}
	return b.String()
}

// ---------------------------------------------------------------- the CSV export

// resultsExport is results.csv at work: every row of the table, fetched to the last, written to
// <path>.part beside the chosen path as pages arrive and renamed over it once all are written, so
// the chosen path never holds part of a table. A writer goroutine owns the file; the loop feeds it
// one page at a time and fetches the next only once that page is written, so at most one page and
// the closing step ever wait for it (steps holds both, and a send never blocks the loop).
type resultsExport struct {
	path, part string
	steps      chan exportStep
	done       chan struct{} // closed when the writer has finished: the file renamed, or removed
	// loop-owned: the model's rows handed to the writer, whether a page is with it, the rows it has
	// written, and whether the closing step was sent (finish or cancel)
	sent     int
	writing  bool
	written  int
	stopping bool
}

// exportStep is the writer's next step: a page of rows, or the end, finish or cancel (why).
type exportStep struct {
	rows   [][]string
	finish bool
	cancel string
}

// startExportResults is results.csv: the save dialog, at the workspace's folder.
func (pl *plugins) startExportResults() {
	switch {
	case pl.results.id == "" && len(pl.results.model.rows) == 0:
		pl.c.Notify("no results to export")
		return
	case pl.export != nil:
		pl.c.Notify("the results are being exported already")
		return
	}
	pl.c.Set("App.browseFolder", pl.c.BrowseFolder())
	pl.c.Open("exportResults")
}

// exportResults is the save dialog accepted: a file there already asks Replace or Cancel first.
func (pl *plugins) exportResults(path string) {
	pl.c.Close("exportResults")
	if path == "" {
		return
	}
	path = filepath.Clean(path)
	if filepath.Ext(path) == "" {
		path += ".csv"
	}
	if _, err := os.Stat(path); err == nil {
		pl.c.Ask(Question{Title: "replace " + filepath.Base(path) + "?",
			Text: path + " exists. Export the results over it? It is replaced only once every row is written.",
			Yes:  "&Replace", No: "&Cancel", OnYes: func() { pl.beginExport(path) }})
		return
	}
	pl.beginExport(path)
}

// beginExport starts the writer and hands it the rows loaded; the pump fetches the rest.
func (pl *plugins) beginExport(path string) {
	if pl.export != nil {
		pl.c.Notify("the results are being exported already")
		return
	}
	fields := pl.results.model.fields
	switch {
	case len(fields) == 0:
		pl.c.Notify("no results to export")
		return
	case pl.results.cut:
		pl.c.Notify("the results ended early, so the rows loaded are not all of them: run the query again to export them all")
		return
	case pl.results.spilled:
		pl.c.Notify(fmt.Sprintf("only the first %d rows were kept; run the query again to export them all", len(pl.results.model.rows)))
		return
	}
	x := &resultsExport{path: path, part: path + ".part", steps: make(chan exportStep, 2), done: make(chan struct{})}
	pl.export = x
	go x.write(fields, pl.c.Post, pl.exportWritten, pl.exportEnded)
	pl.c.NotifyOngoing(toastExport, "exporting the results to "+path+"…")
	pl.exportPump()
}

// exportPump moves the export on, after it starts, after each page is written and as rows arrive:
// the rows the writer has not had go to it; with none, the next page is fetched; with none left,
// the export finishes. A page with the writer waits for it to be written.
func (pl *plugins) exportPump() {
	x := pl.export
	if x == nil || x.writing || x.stopping {
		return
	}
	res := pl.results
	if rows := res.model.rows; x.sent < len(rows) {
		x.writing = true
		x.steps <- exportStep{rows: rows[x.sent:]}
		x.sent = len(rows)
		return
	}
	if len(res.over) > 0 { // past the panel's cap: written, and let go
		x.writing = true
		x.steps <- exportStep{rows: res.over}
		res.over, res.spilled = nil, true
		return
	}
	if pl.results.more {
		if pl.results.owner == nil {
			pl.cancelExport("no plugin serves the results")
			return
		}
		pl.fetchRows()
		return
	}
	x.stopping = true
	x.steps <- exportStep{finish: true}
}

// exportWritten is a page written; a write that failed has removed the .part.
func (pl *plugins) exportWritten(x *resultsExport, n int) {
	if pl.export != x {
		return
	}
	x.writing = false
	x.written += n
	pl.c.NotifyOngoing(toastExport, fmt.Sprintf("exporting: %d rows", x.written))
	pl.showResultsTitle()
	pl.exportPump()
}

// cancelExport stops an export before it finishes: the writer closes and removes the .part, and
// the chosen path is left as it was. One already finishing is let finish.
func (pl *plugins) cancelExport(why string) {
	x := pl.export
	if x == nil || x.stopping {
		return
	}
	x.stopping = true
	x.steps <- exportStep{cancel: why}
}

// exportEnded is the writer finished: the file renamed into place, or removed, and why.
func (pl *plugins) exportEnded(x *resultsExport, rows int, err error) {
	if pl.export == x {
		pl.export = nil
	}
	pl.showResultsTitle()
	if err != nil {
		pl.c.NotifyDone(toastExport, "the results were not exported: "+err.Error()+"; "+x.path+" not written")
		return
	}
	pl.c.NotifyDone(toastExport, fmt.Sprintf("%d rows exported to %s", rows, x.path))
}

// errExportCancelled is an export stopped before it finished, with why.
type errExportCancelled struct{ why string }

func (e errExportCancelled) Error() string { return "cancelled: " + e.why }

// write is the writer: it creates the .part (never over one there already: another export's, or
// one a crash left), writes the header and each page as it comes, and at the end syncs it and
// renames it over the chosen path; cancelled, or on any error, it closes and removes it. Each
// page written, and the end, are told to the loop through post.
func (x *resultsExport) write(fields []string, post func(func()), written func(*resultsExport, int), ended func(*resultsExport, int, error)) {
	defer close(x.done)
	rows := 0
	fail := func(f *os.File, err error) {
		if f != nil {
			_ = f.Close()
			_ = os.Remove(x.part)
		}
		post(func() { ended(x, rows, err) })
	}
	f, err := os.OpenFile(x.part, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
	if err != nil {
		if errors.Is(err, os.ErrExist) {
			err = fmt.Errorf("%s exists: another export's, or one that did not finish; remove it first", x.part)
		}
		fail(nil, err)
		return
	}
	w := csv.NewWriter(f)
	_ = w.Write(fields)
	for step := range x.steps {
		switch {
		case step.cancel != "":
			fail(f, errExportCancelled{step.cancel})
			return
		case step.finish:
			w.Flush()
			err := w.Error()
			if err == nil {
				err = f.Sync()
			}
			if cerr := f.Close(); err == nil {
				err = cerr
			}
			if err == nil {
				err = os.Rename(x.part, x.path)
			}
			if err != nil {
				_ = os.Remove(x.part)
			}
			post(func() { ended(x, rows, err) })
			return
		}
		for _, r := range step.rows {
			_ = w.Write(fieldsOf(r, len(fields)))
		}
		w.Flush()
		if err := w.Error(); err != nil {
			fail(f, err)
			return
		}
		rows += len(step.rows)
		n := len(step.rows)
		post(func() { written(x, n) })
	}
}

// fieldsOf is a row with a cell per field: a short one's last cells empty, a long one's extra cells
// dropped, as the table shows it.
func fieldsOf(row []string, n int) []string {
	if len(row) == n {
		return row
	}
	out := make([]string, n)
	copy(out, row)
	return out
}

// stopExport is AutoDoc quitting: an export not finished is cancelled, and its writer waited for,
// so no .part is left behind. The writer is only ever between two file writes.
func (pl *plugins) stopExport() {
	x := pl.export
	if x == nil {
		return
	}
	if !x.stopping {
		x.stopping = true
		x.steps <- exportStep{cancel: "AutoDoc closed"}
	}
	<-x.done
	pl.export = nil
}
