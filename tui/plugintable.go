package tui

import (
	"cmp"
	"errors"
	"fmt"
	"strconv"
	"strings"

	"github.com/yongjohnlee80/golib/parse/qml"
	tuicore "github.com/yongjohnlee80/golib/tui"
	tuidecl "github.com/yongjohnlee80/golib/tui/decl"
	"github.com/yongjohnlee80/golib/tui/widget"

	"github.com/yongjohnlee80/autodoc/plugin"
)

// THE RESULTS PANEL — a table a plugin sends (protocol 4: host.table), shown by AutoDoc itself in a
// panel docked at the bottom: golib's cell-cursor table, its rows numbered, its loaded rows sortable
// from an ORDER BY bar, with vim's selections and a right-click menu. A plugin whose manifest
// declares [table] sends its fields and first rows; the table asks for more as its cursor nears the
// end of those loaded, and the plugin answers each request (plugin.table.fetch) with the next rows
// (host.rows), or ends them early (host.table_end). One request waits at a time. A newer table
// replaces the one shown, whoever's it was, and its plugin is told the old one is gone
// (plugin.table.close), as it is when the panel closes; the plugin frees what serves its rows. From
// protocol 5 the plugin may send each cell's kind, so a NULL is told from the text "NULL" and a
// number from its digits: the sort and the JSON export read them.
//
// A table's id is new for each table: one sent again under the id of the table shown is refused,
// since a fetch for the old one may still be on its way and its rows would join the new. Rows are
// taken only as the answer to the fetch that waits.
//
// The panel holds at most resultsResident rows: a query of millions is read a page at a time, never
// whole. At the cap the table stops asking for more and its heading says more remain.
//
// THE PANEL ACTS ON THE TABLE IN THE VIEW. Its sort, its copies, results.markdown and its exports
// take the rows loaded, in the order shown, and none of them fetches a row: what the query returns
// is changed by changing the query. More rows load as the reader scrolls, and the view then holds
// them.

// resultsPanel is the panel's id, and resultsView its table's, which takes the keyboard.
const (
	resultsPanel = "results"
	resultsView  = "resultsTable"
)

// resultsPage is how many rows one plugin.table.fetch asks for. The plugin may send fewer: it sizes
// a page by its encoded bytes, to stay under the link's limit.
const resultsPage = 500

// resultsResident is the most rows the panel holds (a variable, so a test can lower it). Rows past
// it are not loaded, and the table says the result has more.
var resultsResident = 100_000

// resultsCopyCap is the most text a copy puts on the clipboard (a variable, so a test can lower it):
// past it nothing is copied, and the notice says to export instead.
var resultsCopyCap = 16 << 20

// resultsTable is the table shown: whose it is, its id there, and the plugin's paging. Loop-owned.
type resultsTable struct {
	model *resultsModel
	shown tuidecl.ItemModel // what App.resultsRows holds: the model, or kindedResults over it
	owner *pluginRun        // nil when no plugin serves it: none shown, or its plugin closed
	id    string            // the plugin's id for it; "" when none is shown
	title string
	// more is the last message's: rows remain to fetch. pending is a fetch sent and not yet
	// answered: another is not sent until host.rows or host.table_end answers it. cut is rows that
	// remained when the table's rows ended early (host.table_end, its plugin closed, or a page
	// refused). capped is rows that came past the panel's cap and were let go.
	more, pending, cut, capped bool
	// sorting is the bar's sort being applied. The view keeps the cursor on its row, and the row's
	// new place may be near the end of those loaded, where the view asks for more; a sort is not
	// the reader scrolling, so nothing is fetched meanwhile.
	sorting bool
}

// full reports whether the panel holds all the rows it may.
func (res *resultsTable) full() bool { return len(res.model.rows) >= resultsResident }

// partial reports whether the result has rows the view does not: rows still to fetch, rows past the
// cap, or rows that never came.
func (res *resultsTable) partial() bool { return res.more || res.cut || res.capped }

// resultsModel is the table's rows as a TableView reads them: its fields as the columns, its cells
// as text, and a Fetcher at the root, so the cell-cursor view asks for more as it nears the end.
// It keeps the cells as the plugin sent them, for the CSV and JSON exports (which keep a line break
// in a cell); the view is given them on one line. Loop-owned, as every model.
type resultsModel struct {
	t      *resultsTable
	fetch  func()
	fields []string
	rows   [][]string
	// kinds are each row's cells' kinds, parallel to rows, while the table has them (kinded: its
	// host.table carried kinds); a row's "" is every cell text. nil for a table sent without them.
	kinds  []string
	kinded bool
	subs   map[int]func(tuidecl.Change)
	next   int
}

func newResultsModel(t *resultsTable, fetch func()) *resultsModel {
	return &resultsModel{t: t, fetch: fetch, subs: map[int]func(tuidecl.Change){}}
}

// reset replaces the fields, the rows and their kinds; kinded says the table has kinds.
func (m *resultsModel) reset(fields []string, rows [][]string, kinds []string, kinded bool) {
	m.fields, m.rows, m.kinds, m.kinded = fields, nil, nil, kinded
	m.rows = append(m.rows, rows...)
	m.addKinds(len(rows), kinds)
	m.notify(tuidecl.Change{Kind: tuidecl.Reset})
}

// add appends rows and their kinds.
func (m *resultsModel) add(rows [][]string, kinds []string) {
	if len(rows) == 0 {
		m.notify(tuidecl.Change{Kind: tuidecl.Changed, First: 0, Last: max(len(m.rows)-1, 0)})
		return
	}
	first := len(m.rows)
	m.rows = append(m.rows, rows...)
	m.addKinds(len(rows), kinds)
	m.notify(tuidecl.Change{Kind: tuidecl.Inserted, First: first, Last: len(m.rows) - 1})
}

// addKinds keeps n new rows' kinds, while the table has them: a page sent without them is text.
func (m *resultsModel) addKinds(n int, kinds []string) {
	if !m.kinded {
		return
	}
	if kinds == nil {
		kinds = make([]string, n)
	}
	m.kinds = append(m.kinds, kinds[:n]...)
}

// kind is a cell's kind: text when the table, the row or the field has none.
func (m *resultsModel) kind(row, col int) widget.TableCellKind {
	if row < 0 || row >= len(m.kinds) || col < 0 || col >= len(m.kinds[row]) {
		return widget.TableCellText
	}
	return widget.TableCellKind(m.kinds[row][col])
}

// rowKinds is a row's kinds as the table holds them: "" without.
func (m *resultsModel) rowKinds(row int) string {
	if row < 0 || row >= len(m.kinds) {
		return ""
	}
	return m.kinds[row]
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
// view stops asking.
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

// kindedResults is the model as the view reads it while the table has kinds: its Kind makes the
// view sort by them (golib's TableKinds), NULLs last and numbers as exact decimals. A table without
// kinds is shown through the model itself, which has no Kind, so its sort reads the text as a
// table of strings always has been read: as numbers where both cells are numbers.
type kindedResults struct{ *resultsModel }

func (k kindedResults) Kind(row, col int) widget.TableCellKind { return k.kind(row, col) }

var _ widget.TableKinds = kindedResults{}

// oneLine is a cell's text as a table row shows it.
var oneLine = strings.NewReplacer("\r\n", " ", "\n", " ", "\r", " ", "\t", " ").Replace

// ---------------------------------------------------------------- the plugin's messages

// onTable is a protocol-4 plugin's table messages, read on the link's goroutine and handled on the
// loop. One from a plugin whose manifest declares no [table], or before it is ready, is logged and
// dropped. Kinds are read from a plugin whose manifest says protocol 5 or later.
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
		t, dropped, err := plugin.ReadTableAt(params, r.e.m.Protocol)
		if err != nil {
			r.log.note("a table refused: " + err.Error())
			return
		}
		r.noteDropped(method, dropped)
		r.pl.c.Post(func() { r.pl.tableOpened(r, t) })
	case plugin.MethodRows:
		page, dropped, err := plugin.ReadTableRows(params, r.e.m.Protocol)
		if err != nil {
			r.log.note("rows refused: " + err.Error())
			return
		}
		r.noteDropped(method, dropped)
		r.pl.c.Post(func() { r.pl.tableRows(r, page) })
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
		// new table's
		r.log.note(fmt.Sprintf("host.table reused the shown table's id %q; a new table needs a new id", t.ID))
		return
	}
	pl.closeTable()
	res.owner, res.id, res.title, res.more, res.pending, res.cut = r, t.ID, cmp.Or(t.Title, r.e.m.Table.Title), t.More, false, false
	keep := min(len(t.Rows), resultsResident)
	res.capped = keep < len(t.Rows)
	var kinds []string
	if t.Kinds != nil {
		kinds = t.Kinds[:keep]
	}
	res.model.reset(t.Fields, t.Rows[:keep], kinds, t.Kinds != nil)
	pl.showResultsModel()
	pl.showResultsTitle()
	pl.orderResults(pl.resultsOrder) // the bar's text, read against the new fields
	if !pl.c.PanelOpen(resultsPanel) {
		pl.c.Keep(pl.c.View(resultsPanel, "open"))
	}
	pl.bindResultsMenu()
}

// showResultsModel gives the view the model as it should read it: with kinds, or without.
func (pl *plugins) showResultsModel() {
	var v tuidecl.ItemModel = pl.results.model
	if pl.results.model.kinded {
		v = kindedResults{pl.results.model}
	}
	if v != pl.results.shown {
		pl.results.shown = v
		pl.c.Set("App.resultsRows", v)
	}
}

// tableRows is host.rows: the next rows of the table shown, answering the fetch that waits. Rows
// for a table no longer shown are dropped, and so are rows no fetch asked for. Rows past the
// panel's cap are let go. A page whose kinds do not fit the table's fields is dropped, and the
// table's rows end there: the rows after it would not follow the rows before.
func (pl *plugins) tableRows(r *pluginRun, page plugin.TableRows) {
	res := pl.results
	if res.owner != r || res.id != page.ID {
		return
	}
	if !res.pending {
		r.log.note(fmt.Sprintf("host.rows for %q dropped: no fetch is waiting for them", page.ID))
		return
	}
	if !res.model.kinded {
		page.Kinds = nil
	} else if why := plugin.CheckKinds(page.Kinds, len(page.Rows), len(res.model.fields)); page.Kinds != nil && why != "" {
		r.log.note(fmt.Sprintf("host.rows for %q dropped: its kinds: %s", page.ID, why))
		pl.tableEnded(r, page.ID, "a page of rows was malformed: its kinds: "+why)
		return
	}
	res.pending, res.more = false, page.More
	keep := min(max(resultsResident-len(res.model.rows), 0), len(page.Rows))
	res.capped = res.capped || keep < len(page.Rows)
	var kinds []string
	if page.Kinds != nil {
		kinds = page.Kinds[:keep]
	}
	res.model.add(page.Rows[:keep], kinds)
	pl.showResultsTitle()
}

// tableEnded is host.table_end: no more rows will come, and why when it failed.
func (pl *plugins) tableEnded(r *pluginRun, id, why string) {
	res := pl.results
	if res.owner != r || res.id != id {
		return
	}
	res.cut = res.cut || res.more
	res.pending, res.more = false, false
	res.model.add(nil, nil)
	pl.showResultsTitle()
	if why != "" {
		pl.c.Notify(r.e.m.Name + ": the results ended early: " + why)
	}
}

// fetchRows asks the table's plugin for its next rows, unless one request waits already, no rows
// remain, no plugin serves the table, the panel is at its cap, or the bar's sort is being applied.
// It asks only for what the panel has room for.
func (pl *plugins) fetchRows() {
	res := pl.results
	if res.owner == nil || res.owner.closing || !res.more || res.pending || res.sorting {
		return
	}
	n := min(resultsPage, resultsResident-len(res.model.rows))
	if n <= 0 {
		return
	}
	res.pending = true
	res.owner.send(plugin.MethodFetch, plugin.FetchParams(plugin.TableFetch{ID: res.id, N: n}))
}

// closeTable lets go of the table shown: its plugin is told it is gone. The rows stay in the model
// until something replaces them. An export already begun writes the rows it took, whatever comes
// after.
func (pl *plugins) closeTable() {
	res := pl.results
	if res.id == "" {
		return
	}
	if res.owner != nil && !res.owner.closing {
		res.owner.send(plugin.MethodTableGone, plugin.TableGoneParams(res.id))
	}
	res.owner, res.id, res.more, res.pending = nil, "", false, false
}

// tableOwnerGone is a plugin closing: the table it served has no more rows to give. Its rows stay
// shown.
func (pl *plugins) tableOwnerGone(r *pluginRun) {
	res := pl.results
	if res.owner != r {
		return
	}
	res.cut = res.cut || res.more
	res.owner, res.more, res.pending = nil, false, false
	res.model.add(nil, nil)
	pl.showResultsTitle()
}

// resultsClosed is the panel closed (Esc, q, or Go › Results): the table is let go, and the panel
// holds nothing until the next.
func (pl *plugins) resultsClosed() {
	pl.closeTable()
	pl.results.title, pl.results.cut, pl.results.capped = "", false, false
	pl.results.model.reset(nil, nil, nil, false)
	pl.showResultsModel()
	pl.showResultsTitle()
}

// showResultsTitle is the panel's heading: the table's title, its rows, and whether more remain;
// and the sort bar's note.
func (pl *plugins) showResultsTitle() {
	res := pl.results
	title := "results"
	if res.title != "" {
		title += ": " + res.title
	}
	n := len(res.model.rows)
	if n > 0 || res.id != "" {
		switch {
		case res.capped || res.more && res.full():
			title += fmt.Sprintf(" · the first %d rows shown; the result has more: change the query to change what is returned", n)
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
	note := ""
	if res.partial() && n > 0 {
		note = fmt.Sprintf("sorts the loaded rows: %d of a result with more", n)
	}
	pl.resultsNote = note
	pl.c.Set("App.resultsSortNote", note)
	pl.showSortLine()
}

// ---------------------------------------------------------------- the bar: ORDER BY, WHERE

// The bar is a clause, chosen in its dropdown, and that clause's text, which Enter applies; each
// clause keeps its own text. ORDER BY's applied text is App.resultsOrder, which the TableView's
// orderBy is bound to: the view's own sorts (s, a header) are written back to it, and the menu's
// sorts write it. WHERE filters the loaded rows once AutoDoc takes the golib that reads it; until
// then its Enter says so. The line beside the bar is the chosen clause's error, else the field
// names the word being typed may be, else the view's note.

// The bar's clauses, in its dropdown's order.
const (
	clauseOrderBy = iota
	clauseWhere
)

// errWhereUnread is WHERE's Enter until AutoDoc's golib reads a filter.
const errWhereUnread = "WHERE needs a newer golib: it filters the loaded rows once AutoDoc takes one that reads it"

// resultsClausesModel is the bar's clauses, its dropdown's rows.
func resultsClausesModel() *tuidecl.ListModel {
	m := tuidecl.NewListModel("key", "label")
	m.Reset([]tuidecl.Row{{"key": "orderBy", "label": "ORDER BY"}, {"key": "where", "label": "WHERE"}})
	return m
}

// resultsClauseChosen is the dropdown's choice: the clause left keeps the text typed into it, and
// the bar shows the chosen one's.
func (pl *plugins) resultsClauseChosen(i int, typed string) {
	if i < clauseOrderBy || i > clauseWhere || i == pl.resultsClause {
		return
	}
	pl.clauseText[pl.resultsClause] = typed
	pl.resultsClause, pl.resultsHint = i, ""
	pl.c.Set("App.resultsClauseIndex", i)
	pl.c.Set("App.resultsClauseText", pl.clauseText[i])
	pl.showSortLine()
}

// applyResultsClause is the bar's Enter: its text applied as the chosen clause.
func (pl *plugins) applyResultsClause(text string) {
	if pl.resultsClause == clauseOrderBy {
		pl.orderResults(text)
		return
	}
	pl.clauseText[clauseWhere], pl.resultsHint = text, ""
	pl.resultsWhereErr = ""
	if strings.TrimSpace(text) != "" {
		pl.resultsWhereErr = errWhereUnread
	}
	pl.showSortLine()
}

// resultsClauseTyped is the bar's text as it is typed: while an ORDER BY's field name is typed,
// the line offers the fields it may be, each written as the bar reads it back (a name two fields
// share as its position).
func (pl *plugins) resultsClauseTyped(text string) {
	pl.resultsHint = ""
	if pl.resultsClause == clauseOrderBy {
		pl.resultsHint = orderHint(text, pl.resultsFields())
	}
	pl.showSortLine()
}

// orderResults is ORDER BY applied: the text is the table's orderBy. Text that does not parse
// leaves the rows as they were and shows why: the view's own words (ParseTableOrder's, over the
// same fields).
func (pl *plugins) orderResults(text string) {
	pl.resultsOrder, pl.resultsHint = text, ""
	pl.resultsOrderErr = ""
	if _, err := widget.ParseTableOrder(text, pl.resultsFields()); err != nil {
		pl.resultsOrderErr = err.Error()
	}
	pl.showOrderText(text)
	// the view applies orderBy within the Set, its sort and any request for rows the sort makes
	pl.results.sorting = true
	pl.c.Set("App.resultsOrder", text)
	pl.results.sorting = false
	pl.showSortLine()
}

// resultsOrderChanged is the view's own sort (the s key, a header), written back to orderBy: the
// bar's ORDER BY shows it.
func (pl *plugins) resultsOrderChanged(text string) {
	pl.resultsOrder, pl.resultsOrderErr, pl.resultsHint = text, "", ""
	pl.showOrderText(text)
	pl.results.sorting = true
	pl.c.Set("App.resultsOrder", text)
	pl.results.sorting = false
	pl.showSortLine()
}

// showOrderText is ORDER BY's text, kept, and in the bar while ORDER BY is chosen.
func (pl *plugins) showOrderText(text string) {
	pl.clauseText[clauseOrderBy] = text
	if pl.resultsClause == clauseOrderBy {
		pl.c.Set("App.resultsClauseText", text)
	}
}

// orderHint is what the bar offers for text: "fields: name, 3, …" for the field the last key is
// naming, "" once its name is followed by a space (a direction comes next) or nothing matches.
func orderHint(text string, fields []widget.TableField) string {
	word := text[strings.LastIndexByte(text, ',')+1:]
	word = strings.TrimLeft(word, " \t")
	if strings.ContainsAny(word, " \t") || len(fields) == 0 {
		return ""
	}
	word = strings.TrimPrefix(word, `"`)
	var offer []string
	for col, f := range fields {
		if strings.HasPrefix(strings.ToLower(f.Title), strings.ToLower(word)) {
			offer = append(offer, strings.TrimSuffix(widget.FormatTableOrder([]widget.TableSortKey{{Col: col}}, fields), " ASC"))
		}
	}
	if len(offer) == 0 {
		return ""
	}
	return "fields: " + strings.Join(offer, ", ")
}

func (pl *plugins) showSortLine() {
	line, err := pl.resultsNote, pl.resultsOrderErr
	if pl.resultsClause == clauseWhere {
		err = pl.resultsWhereErr
	}
	switch {
	case err != "":
		line = err
	case pl.resultsHint != "":
		line = pl.resultsHint
	}
	pl.c.Set("App.resultsSortLine", line)
}

// resultsFields are the table's fields as the view has them.
func (pl *plugins) resultsFields() []widget.TableField {
	out := make([]widget.TableField, len(pl.results.model.fields))
	for i, f := range pl.results.model.fields {
		out[i].Title = f
	}
	return out
}

// sortResults is results.sort_asc, results.sort_desc and results.unsort: the field (a 1-based
// position; the cursor's when none is given) written to the bar as its one key, or no sort.
func (pl *plugins) sortResults(how string, args []string) error {
	core := pl.c.TableCore(resultsView)
	if core == nil || len(core.Fields()) == 0 {
		pl.c.Notify("no results to sort")
		return nil
	}
	if how == "off" {
		pl.orderResults("")
		return nil
	}
	col := core.Cursor().Col
	if len(args) > 0 {
		n, err := strconv.Atoi(args[0])
		if err != nil || n < 1 || n > len(core.Fields()) {
			return fmt.Errorf("no field %q: the results have %d", args[0], len(core.Fields()))
		}
		col = n - 1
	}
	pl.orderResults(widget.FormatTableOrder([]widget.TableSortKey{{Col: col, Desc: how == "desc"}}, core.Fields()))
	return nil
}

// selectAllResults is results.select_all: every loaded row.
func (pl *plugins) selectAllResults() {
	if core := pl.c.TableCore(resultsView); core != nil && core.Len() > 0 {
		core.SelectAll()
	}
}

// ---------------------------------------------------------------- the menu

// bindResultsMenu gives the table's right-click menu its rows: built each time it opens, so the
// sorts name the field under the pointer.
func (pl *plugins) bindResultsMenu() {
	if t := pl.c.ResultsTable(resultsView); t != nil && t.Menu() != nil {
		t.Menu().SetRows(pl.resultsMenuRows)
	}
}

// resultsMenuRows are the menu's rows, each a results command of the catalog. The sorts take the
// field the right press was on (MenuAt), not the cursor's.
func (pl *plugins) resultsMenuRows() []widget.MenuItemModel {
	t := pl.c.ResultsTable(resultsView)
	if t == nil {
		return nil
	}
	core := t.Core()
	have := core.Len() > 0
	at, hit := t.MenuAt()
	field := []string{}
	onField := hit == widget.TableHitCell || hit == widget.TableHitHeader
	if onField {
		field = []string{strconv.Itoa(at.Col + 1)}
	}
	name := ""
	if f := core.Fields(); onField && at.Col < len(f) {
		name = " by " + f[at.Col].Title
	}
	row := func(id, label, entry string, args []string, enabled bool) widget.MenuItemModel {
		m := widget.NewCommand(widget.ItemID("results.menu."+id), label, widget.CoreMenuAction{ID: tuicore.ActionID("results.menu." + id),
			Run: func(*widget.EditorCore) { pl.runResultsEntry(entry, args) }})
		m.Enabled = enabled
		return m
	}
	return []widget.MenuItemModel{
		row("copy", "Copy", "results.copy", nil, have),
		row("copy_csv", "Copy as CSV", "results.copy_csv", nil, have),
		row("copy_json", "Copy as JSON", "results.copy_json", nil, have),
		row("copy_markdown", "Copy as Markdown", "results.copy_markdown", nil, have),
		widget.NewSeparator("results.menu.selecting"),
		row("select_all", "Select all", "results.select_all", nil, have),
		widget.NewSeparator("results.menu.sorting"),
		row("sort_asc", "Sort ascending"+name, "results.sort_asc", field, onField),
		row("sort_desc", "Sort descending"+name, "results.sort_desc", field, onField),
		row("unsort", "Unsort", "results.unsort", nil, pl.resultsOrder != ""),
		widget.NewSeparator("results.menu.leaving"),
		row("export", "Export loaded rows…", "results.export", nil, have),
		row("markdown", "Insert as Markdown", "results.markdown", nil, have),
	}
}

// runResultsEntry runs a results command of the catalog, as the menu's rows do; its error is a
// notice.
func (pl *plugins) runResultsEntry(id string, args []string) {
	if err := pl.c.RunEntry(id, args); err != nil {
		pl.c.Failed(id, err)
	}
}

// ---------------------------------------------------------------- copies and Markdown

// copyFormat is a copy's or an export's text: TSV (a copy's), CSV, JSON or Markdown (a copy's).
type copyFormat string

const (
	formatTSV      copyFormat = "tsv"
	formatCSV      copyFormat = "csv"
	formatJSON     copyFormat = "json"
	formatMarkdown copyFormat = "markdown"
)

// errPastCopyCap is a copy's text grown past resultsCopyCap.
var errPastCopyCap = errors.New("past the copy's cap")

// copyResults is results.copy and its siblings: the selected cells (or the cursor's), from the
// loaded rows as the table shows them, in format. Past resultsCopyCap of text nothing is copied.
func (pl *plugins) copyResults(format copyFormat) {
	core := pl.c.TableCore(resultsView)
	if core == nil || core.Len() == 0 {
		pl.c.Notify("no results to copy")
		return
	}
	text, err := pl.selectionText(core, format)
	if errors.Is(err, errPastCopyCap) || err == nil && len(text) > resultsCopyCap {
		pl.c.Notify(fmt.Sprintf("nothing copied: the selection is more than the %s a copy takes; use Export loaded rows… to write it to a file", sizeOf(resultsCopyCap)))
		return
	}
	if err != nil {
		pl.c.Failed("copy the results", err)
		return
	}
	if !pl.c.CopyText(text) {
		pl.c.Notify("the results were not copied: no clipboard")
		return
	}
	if format == formatTSV {
		pl.c.Say("results copied")
		return
	}
	pl.c.Say("results copied as " + strings.ToUpper(string(format)))
}

// sizeOf is n bytes as a reader says it: "16 MiB", "512 KiB", "300 bytes".
func sizeOf(n int) string {
	switch {
	case n >= 1<<20 && n%(1<<20) == 0:
		return fmt.Sprintf("%d MiB", n>>20)
	case n >= 1<<10 && n%(1<<10) == 0:
		return fmt.Sprintf("%d KiB", n>>10)
	}
	return fmt.Sprintf("%d bytes", n)
}

// selectionText is the cells a copy takes, in format. TSV is the table's own (CopyText): the cells
// as shown, no header. CSV and Markdown are the selected fields, a header first, a row for each row
// the selection is in; a cell of those fields outside the selection (vim's charwise v, ragged at
// its ends) is empty. JSON is an object per row holding its selected cells.
func (pl *plugins) selectionText(core *widget.TableCore, format copyFormat) (string, error) {
	if format == formatTSV {
		return core.CopyText(), nil
	}
	cols, rows, in := selectedCells(core)
	m := pl.results.model
	names := make([]string, len(cols))
	for i, c := range cols {
		names[i] = m.fields[c]
	}
	pick := func(src int) (cells []string, kinds string) {
		row, k := m.rows[src], m.rowKinds(src)
		kb := make([]byte, len(cols))
		cells = make([]string, len(cols))
		for i, c := range cols {
			kb[i] = byte(widget.TableCellText)
			if c < len(k) {
				kb[i] = k[c]
			}
			if c < len(row) {
				cells[i] = row[c]
			}
		}
		return cells, string(kb)
	}
	switch format {
	case formatMarkdown:
		table := make([][]string, 0, len(rows))
		for _, d := range rows {
			cells, _ := pick(core.SourceRow(d))
			for i, c := range cols {
				if !in(d, c) {
					cells[i] = ""
				}
			}
			table = append(table, cells)
		}
		return markdownTable(names, table), nil
	case formatCSV:
		var b strings.Builder
		w := newCSVRows(&cappedWriter{w: &b, cap: resultsCopyCap}, names)
		for _, d := range rows {
			cells, _ := pick(core.SourceRow(d))
			for i, c := range cols {
				if !in(d, c) {
					cells[i] = ""
				}
			}
			if err := w.write(cells, ""); err != nil {
				return "", err
			}
		}
		if err := w.end(); err != nil {
			return "", err
		}
		return b.String(), nil
	}
	var b strings.Builder
	w := newJSONRows(&cappedWriter{w: &b, cap: resultsCopyCap}, names)
	for _, d := range rows {
		cells, kinds := pick(core.SourceRow(d))
		var keep []int
		for i, c := range cols {
			if in(d, c) {
				keep = append(keep, i)
			}
		}
		if err := w.writeSome(cells, kinds, keep); err != nil {
			return "", err
		}
	}
	if err := w.end(); err != nil {
		return "", err
	}
	return b.String(), nil
}

// selectedCells is the selection over the loaded rows as shown: the fields any selected cell is in
// (in field order), the display rows holding one (in the order shown), and whether a cell is
// selected. With no selection it is the cursor's cell.
func selectedCells(core *widget.TableCore) (cols, rows []int, in func(d, col int) bool) {
	from, to, ok := core.Selection()
	if !ok {
		cur := core.Cursor()
		return []int{cur.Col}, []int{cur.Row}, func(d, col int) bool { return d == cur.Row && col == cur.Col }
	}
	n := len(core.Fields())
	used := make([]bool, n)
	for d := from.Row; d <= to.Row && d < core.Len(); d++ {
		any := false
		for col := range n {
			if core.InSelection(d, col) {
				used[col], any = true, true
			}
		}
		if any {
			rows = append(rows, d)
		}
	}
	for col, u := range used {
		if u {
			cols = append(cols, col)
		}
	}
	return cols, rows, core.InSelection
}

// insertResults is results.markdown: the rows loaded now, as the table shows them (its sort
// applied), a Markdown table at the editor's cursor. While the result has more the notice says so.
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
	if pl.results.partial() {
		pl.c.Notify(fmt.Sprintf("%d loaded rows inserted; the result has more: change the query to change what is returned", len(rows)))
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
