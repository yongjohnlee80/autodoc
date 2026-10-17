package tui

import (
	"bufio"
	"bytes"
	"encoding/csv"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	tuidecl "github.com/yongjohnlee80/golib/tui/decl"
	"github.com/yongjohnlee80/golib/tui/widget"
)

// THE EXPORT — results.export (results.csv opens it on CSV): the rows loaded in the view, in the
// order it shows them, into a CSV or a JSON file. It never fetches a row: what the result has past
// the view is changed by changing the query, and the dialog and the notice both say when it has
// more. The rows are taken when the export begins, so rows that load, a sort and a newer table
// after it change nothing it writes.
//
// The file is written to <path>.part beside the chosen path and renamed over it once every row is
// written, so the chosen path never holds part of an export. A writer goroutine owns the file; the
// loop hands it one chunk of rows at a time, the next once that one is written, so at most a chunk
// and the closing step ever wait for it (steps holds both, and a send never blocks the loop).

// toastExport is the export's toast, updated as rows are written.
const toastExport = "results-export"

// exportChunk is how many rows the loop hands the writer at a time.
const exportChunk = 1000

// exportFormats are the export's formats, as its dialog's Format choice lists them.
var exportFormats = []copyFormat{formatCSV, formatJSON}

// exportWritten is told each chunk the writer has written, as it is written (a test holds the
// writer here, so an export is still writing when AutoDoc quits); nil in AutoDoc.
var exportWritten func(rows int)

// resultsExport is an export at work.
type resultsExport struct {
	path, part string
	format     copyFormat
	fields     []string
	// rows are the view's rows as the export began, in the order shown, and kinds theirs (nil when
	// the table has none); partial says the result has rows the view did not.
	rows    [][]string
	kinds   []string
	partial bool
	from    int // the loaded rows a WHERE kept these of; 0 without one
	steps   chan exportStep
	done    chan struct{} // closed when the writer has finished: the file renamed, or removed
	// loop-owned: the rows handed to the writer, whether a chunk is with it, the rows it has
	// written, and whether the closing step was sent (finish or cancel)
	sent     int
	writing  bool
	written  int
	stopping bool
}

// exportStep is the writer's next step: a chunk of rows and their kinds, or the end, finish or
// cancel (why).
type exportStep struct {
	rows   [][]string
	kinds  []string
	finish bool
	cancel string
}

// startExportResults is results.export, and results.csv on CSV (format): the dialog that says what
// the export takes, with its Format choice, before a file is chosen.
func (pl *plugins) startExportResults(format copyFormat) {
	switch {
	case pl.results.id == "" && len(pl.results.model.rows) == 0:
		pl.c.Notify("no results to export")
		return
	case pl.export != nil:
		pl.c.Notify("the results are being exported already")
		return
	}
	if format != "" {
		pl.exportFormat = format
	}
	heading := pl.exportHeading()
	more := ""
	if pl.results.partial() {
		more = "the result has more rows than are loaded: change the query to change what is returned"
	}
	pl.c.Set("App.exportHeading", heading)
	pl.c.Set("App.exportMore", more)
	pl.c.Set("App.exportMoreShown", more != "")
	pl.c.Set("App.exportFormatIndex", pl.exportFormatIndex())
	pl.c.Set("App.exportCanSave", pl.viewLen() > 0)
	pl.c.Open("exportResults")
}

// exportHeading is the export dialog's heading: the rows it writes, the view's, and what of the
// loaded rows a WHERE kept.
func (pl *plugins) exportHeading() string {
	rows := func(n int, what string) string {
		if n == 1 {
			return "1 " + what + "row"
		}
		return fmt.Sprintf("%d %srows", n, what)
	}
	if shown, loaded, on := pl.filtered(); on {
		return fmt.Sprintf("Export %s, as shown (filtered from %d loaded)", rows(shown, ""), loaded)
	}
	return fmt.Sprintf("Export %s, as shown", rows(len(pl.results.model.rows), "loaded "))
}

// viewLen is the rows the view shows: the loaded rows a WHERE keeps, all of them without one.
func (pl *plugins) viewLen() int {
	if core := pl.c.TableCore(resultsView); core != nil {
		return core.Len()
	}
	return len(pl.results.model.rows)
}

func (pl *plugins) exportFormatIndex() int {
	for i, f := range exportFormats {
		if f == pl.exportFormat {
			return i
		}
	}
	return 0
}

// exportFormatChosen is the dialog's Format choice.
func (pl *plugins) exportFormatChosen(i int) {
	if i >= 0 && i < len(exportFormats) {
		pl.exportFormat = exportFormats[i]
	}
}

// chooseExportFile is the dialog accepted: the save dialog, at the page's folder, its name ending
// in the format's extension. A view with no row has nothing to save.
func (pl *plugins) chooseExportFile() {
	pl.c.Close("exportResults")
	if pl.viewLen() == 0 {
		pl.c.Notify(pl.nothingIn("export"))
		return
	}
	folder := pl.c.BrowseFolder()
	pl.c.Set("App.browseFolder", folder)
	pl.c.Set("App.exportFile", filepath.Join(folder, "results."+string(pl.exportFormat)))
	pl.c.Open("exportResultsFile")
}

// exportResults is the save dialog accepted: a name with no extension takes the format's, and a
// file there already asks Replace or Cancel first.
func (pl *plugins) exportResults(path string) {
	pl.c.Close("exportResultsFile")
	if path == "" {
		return
	}
	path = filepath.Clean(path)
	if filepath.Ext(path) == "" {
		path += "." + string(pl.exportFormat)
	}
	if _, err := os.Stat(path); err == nil {
		pl.c.Ask(Question{Title: "replace " + filepath.Base(path) + "?",
			Text: path + " exists. Export the results over it? It is replaced only once every row is written.",
			Yes:  "&Replace", No: "&Cancel", OnYes: func() { pl.beginExport(path) }})
		return
	}
	pl.beginExport(path)
}

// viewRows are the rows the view shows, in its order, and their kinds (nil when the table has
// none): what an export takes. A WHERE's view is the loaded rows it keeps, then sorted.
func (pl *plugins) viewRows() (rows [][]string, kinds []string) {
	m := pl.results.model
	core := pl.c.TableCore(resultsView)
	n := len(m.rows)
	if core != nil {
		n = core.Len()
	}
	rows = make([][]string, n)
	if m.kinded {
		kinds = make([]string, n)
	}
	for d := range n {
		src := d
		if core != nil {
			src = core.SourceRow(d)
		}
		rows[d] = m.rows[src]
		if kinds != nil {
			kinds[d] = m.rowKinds(src)
		}
	}
	return rows, kinds
}

// beginExport takes the view's rows and starts the writer on them.
func (pl *plugins) beginExport(path string) {
	if pl.export != nil {
		pl.c.Notify("the results are being exported already")
		return
	}
	fields := pl.results.model.fields
	if len(fields) == 0 {
		pl.c.Notify("no results to export")
		return
	}
	rows, kinds := pl.viewRows()
	if len(rows) == 0 && len(pl.results.model.rows) > 0 {
		pl.c.Notify(pl.nothingIn("export"))
		return
	}
	x := &resultsExport{path: path, part: path + ".part", format: pl.exportFormat, fields: fields, rows: rows, kinds: kinds,
		partial: pl.results.partial(), steps: make(chan exportStep, 2), done: make(chan struct{})}
	if _, loaded, on := pl.filtered(); on {
		x.from = loaded
	}
	pl.export = x
	go x.write(pl.c.Post, pl.exportWrote, pl.exportEnded)
	what := fmt.Sprintf("exporting %d rows to %s…", len(rows), path)
	if x.partial {
		what = fmt.Sprintf("exporting %d loaded rows, as shown, to %s…", len(rows), path)
	}
	pl.c.NotifyOngoing(toastExport, what)
	pl.exportPump()
}

// exportPump moves the export on, after it starts and after each chunk is written: the next chunk
// goes to the writer; with none left, the export finishes. A chunk with the writer waits for it.
func (pl *plugins) exportPump() {
	x := pl.export
	if x == nil || x.writing || x.stopping {
		return
	}
	if x.sent < len(x.rows) {
		end := min(x.sent+exportChunk, len(x.rows))
		step := exportStep{rows: x.rows[x.sent:end]}
		if x.kinds != nil {
			step.kinds = x.kinds[x.sent:end]
		}
		x.writing, x.sent = true, end
		x.steps <- step
		return
	}
	x.stopping = true
	x.steps <- exportStep{finish: true}
}

// exportWrote is a chunk written; a write that failed has removed the .part.
func (pl *plugins) exportWrote(x *resultsExport, n int) {
	if pl.export != x {
		return
	}
	x.writing = false
	x.written += n
	pl.c.NotifyOngoing(toastExport, fmt.Sprintf("exporting: %d rows", x.written))
	pl.showResultsTitle()
	pl.exportPump()
}

// exportEnded is the writer finished: the file renamed into place, or removed, and why. An export
// of a result with more says so, so its file is never taken for the whole result.
func (pl *plugins) exportEnded(x *resultsExport, rows int, err error) {
	if pl.export == x {
		pl.export = nil
	}
	pl.showResultsTitle()
	more := "; the result has more: change the query (a LIMIT, a WHERE) to change what is returned"
	switch {
	case err != nil:
		pl.c.NotifyDone(toastExport, "the results were not exported: "+err.Error()+"; "+x.path+" not written")
	case x.from > 0 && x.partial:
		pl.c.NotifyDone(toastExport, fmt.Sprintf("%d rows exported to %s, as shown (filtered from %d loaded)%s", rows, x.path, x.from, more))
	case x.from > 0:
		pl.c.NotifyDone(toastExport, fmt.Sprintf("%d rows exported to %s, as shown (filtered from %d loaded)", rows, x.path, x.from))
	case x.partial:
		pl.c.NotifyDone(toastExport, fmt.Sprintf("%d loaded rows exported to %s, as shown%s", rows, x.path, more))
	default:
		pl.c.NotifyDone(toastExport, fmt.Sprintf("%d rows exported to %s", rows, x.path))
	}
}

// errExportCancelled is an export stopped before it finished, with why.
type errExportCancelled struct{ why string }

func (e errExportCancelled) Error() string { return "cancelled: " + e.why }

// rowWriter writes an export's rows in its format: write takes a row and its kinds ("" for none),
// end closes what the format opened. Neither flushes past the writer it was given.
type rowWriter interface {
	write(row []string, kinds string) error
	end() error
}

func newRowWriter(format copyFormat, w io.Writer, fields []string) rowWriter {
	if format == formatJSON {
		return newJSONRows(w, fields)
	}
	return newCSVRows(w, fields)
}

// write is the writer: it creates the .part (never over one there already: another export's, or
// one a crash left), writes each chunk as it comes, and at the end flushes, syncs and renames it
// over the chosen path; cancelled, or on any error, it closes and removes it. Each chunk written,
// and the end, are told to the loop through post.
func (x *resultsExport) write(post func(func()), wrote func(*resultsExport, int), ended func(*resultsExport, int, error)) {
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
	buf := bufio.NewWriter(f)
	w := newRowWriter(x.format, buf, x.fields)
	for step := range x.steps {
		switch {
		case step.cancel != "":
			fail(f, errExportCancelled{step.cancel})
			return
		case step.finish:
			err := w.end()
			if err == nil {
				err = buf.Flush()
			}
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
		for i, r := range step.rows {
			k := ""
			if step.kinds != nil {
				k = step.kinds[i]
			}
			if err := w.write(r, k); err != nil {
				fail(f, err)
				return
			}
		}
		if err := buf.Flush(); err != nil {
			fail(f, err)
			return
		}
		rows += len(step.rows)
		n := len(step.rows)
		if exportWritten != nil {
			exportWritten(rows)
		}
		post(func() { wrote(x, n) })
	}
}

// stopExport is AutoDoc quitting: an export not finished is cancelled, and its writer waited for,
// so no .part is left behind. The writer is only ever between two writes.
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

// ---------------------------------------------------------------- CSV

// csvRows writes the fields as a header, then each row, quoted where a cell needs it (a line break
// in a cell is kept).
type csvRows struct {
	w      *csv.Writer
	fields int
	err    error
}

func newCSVRows(w io.Writer, fields []string) *csvRows {
	c := &csvRows{w: csv.NewWriter(w), fields: len(fields)}
	c.err = c.w.Write(fields)
	return c
}

func (c *csvRows) write(row []string, _ string) error {
	if c.err != nil {
		return c.err
	}
	if c.err = c.w.Write(fieldsOf(row, c.fields)); c.err == nil {
		c.w.Flush()
		c.err = c.w.Error()
	}
	return c.err
}

func (c *csvRows) end() error {
	if c.err != nil {
		return c.err
	}
	c.w.Flush()
	return c.w.Error()
}

// ---------------------------------------------------------------- JSON

// jsonRows writes an array of objects, one per row, keyed by field: a name that appears twice takes
// a suffix (id, id_2). Each value follows its cell's kind (jsonValue); with no kinds every value is
// a string.
type jsonRows struct {
	w    io.Writer
	keys []string // each field's key, encoded
	n    int      // the rows written
	err  error
}

func newJSONRows(w io.Writer, fields []string) *jsonRows {
	j := &jsonRows{w: w, keys: make([]string, len(fields))}
	for i, k := range jsonKeys(fields) {
		j.keys[i] = jsonString(k)
	}
	_, j.err = io.WriteString(w, "[")
	return j
}

// jsonKeys are the fields as an object's keys: each its name, a name taken already followed by _2,
// _3, … until it is not.
func jsonKeys(fields []string) []string {
	taken := make(map[string]bool, len(fields))
	out := make([]string, len(fields))
	for i, f := range fields {
		k := f
		for n := 2; taken[k]; n++ {
			k = f + "_" + strconv.Itoa(n)
		}
		taken[k], out[i] = true, k
	}
	return out
}

func (j *jsonRows) write(row []string, kinds string) error {
	return j.writeSome(row, kinds, nil)
}

// writeSome writes row as an object of the fields at keep (every field when keep is nil).
func (j *jsonRows) writeSome(row []string, kinds string, keep []int) error {
	if j.err != nil {
		return j.err
	}
	var b strings.Builder
	if j.n > 0 {
		b.WriteString(",\n")
	} else {
		b.WriteString("\n")
	}
	b.WriteByte('{')
	first := true
	put := func(i int) {
		if !first {
			b.WriteByte(',')
		}
		first = false
		cell, kind := "", byte(widget.TableCellText)
		if i < len(row) {
			cell = row[i]
		}
		if i < len(kinds) {
			kind = kinds[i]
		}
		b.WriteString(j.keys[i])
		b.WriteByte(':')
		b.WriteString(jsonValue(cell, kind))
	}
	if keep == nil {
		for i := range j.keys {
			put(i)
		}
	} else {
		for _, i := range keep {
			put(i)
		}
	}
	b.WriteByte('}')
	j.n++
	_, j.err = io.WriteString(j.w, b.String())
	return j.err
}

func (j *jsonRows) end() error {
	if j.err != nil {
		return j.err
	}
	tail := "]\n"
	if j.n > 0 {
		tail = "\n]\n"
	}
	_, j.err = io.WriteString(j.w, tail)
	return j.err
}

// jsonValue is a cell as JSON, by its kind:
//   - NULL is null;
//   - a number is its literal text when that is a JSON number (never read through a float, so
//     every digit is kept), and a string otherwise (NaN, Infinity);
//   - a boolean is true or false when its text reads as one, else a string;
//   - JSON is the value itself when it parses, else a string;
//   - text, and any other kind, is a string.
func jsonValue(cell string, kind byte) string {
	switch widget.TableCellKind(kind) {
	case widget.TableCellNull:
		return "null"
	case widget.TableCellNumber:
		if jsonNumber(cell) {
			return cell
		}
	case widget.TableCellBool:
		if b, err := strconv.ParseBool(cell); err == nil {
			return strconv.FormatBool(b)
		}
	case widget.TableCellJSON:
		var out bytes.Buffer
		if json.Valid([]byte(cell)) && json.Compact(&out, []byte(cell)) == nil {
			return out.String()
		}
	}
	return jsonString(cell)
}

// jsonNumber reports whether s is a number as JSON writes one:
// -?(0|[1-9][0-9]*)(\.[0-9]+)?([eE][+-]?[0-9]+)?
func jsonNumber(s string) bool {
	i := 0
	digits := func() int {
		n := 0
		for i < len(s) && s[i] >= '0' && s[i] <= '9' {
			i++
			n++
		}
		return n
	}
	if i < len(s) && s[i] == '-' {
		i++
	}
	switch {
	case i < len(s) && s[i] == '0':
		i++
	case i < len(s) && s[i] >= '1' && s[i] <= '9':
		digits()
	default:
		return false
	}
	if i < len(s) && s[i] == '.' {
		i++
		if digits() == 0 {
			return false
		}
	}
	if i < len(s) && (s[i] == 'e' || s[i] == 'E') {
		i++
		if i < len(s) && (s[i] == '+' || s[i] == '-') {
			i++
		}
		if digits() == 0 {
			return false
		}
	}
	return i == len(s)
}

// jsonString is s as a JSON string, its <, > and & as they are.
func jsonString(s string) string {
	var b strings.Builder
	e := json.NewEncoder(&b)
	e.SetEscapeHTML(false)
	_ = e.Encode(s)
	return strings.TrimSuffix(b.String(), "\n")
}

// cappedWriter is a writer that refuses what would take it past cap bytes: a copy stops building
// its text there, rather than build it all to throw it away.
type cappedWriter struct {
	w   io.Writer
	n   int
	cap int
}

func (c *cappedWriter) Write(p []byte) (int, error) {
	if c.n+len(p) > c.cap {
		return 0, errPastCopyCap
	}
	c.n += len(p)
	return c.w.Write(p)
}

// exportFormatsModel is the dialog's Format choice: CSV, JSON.
func exportFormatsModel() *tuidecl.ListModel {
	m := tuidecl.NewListModel("key", "label")
	rows := make([]tuidecl.Row, len(exportFormats))
	for i, f := range exportFormats {
		rows[i] = tuidecl.Row{"key": string(f), "label": strings.ToUpper(string(f))}
	}
	m.Reset(rows)
	return m
}
