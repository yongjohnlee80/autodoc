package tui

import (
	"context"
	"encoding/csv"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/yongjohnlee80/golib/parse/qml"
	tuidecl "github.com/yongjohnlee80/golib/tui/decl"
	"github.com/yongjohnlee80/golib/tui/widget"

	"github.com/yongjohnlee80/autodoc/core/kind"
	"github.com/yongjohnlee80/autodoc/plugin"
)

// PROTOCOL 4 — the results panel a plugin's table shows in, the document a command carries, and its
// byte offsets, through the real host and the SDK (the test plugin is this binary, run again).

// installPlugin4 writes a protocol-4 manifest for the test plugin in mode: kind, then extra.
func installPlugin4(t *testing.T, dir, name, kind, mode, extra string) {
	t.Helper()
	installPlugin2(t, dir, name, kind, mode, extra)
	path := filepath.Join(dir, name, "plugin.toml")
	b, _ := os.ReadFile(path)
	if err := os.WriteFile(path, []byte(strings.Replace(string(b), "protocol = 2\n", "protocol = 4\n", 1)), 0o644); err != nil {
		t.Fatal(err)
	}
}

// tableManifest is a service serving tables: run opens one, run2 another.
const tableManifest = "[table]\ntitle = \"Rows\"\n[[commands]]\nid = \"run\"\ntitle = \"Run\"\n[[commands]]\nid = \"run2\"\ntitle = \"Run again\"\n" +
	"[[commands]]\nid = \"unsolicited\"\ntitle = \"Rows unasked\"\n"

// snapManifest is a card on the feed whose command says the document it runs on.
const snapManifest = "[dialog]\nmodal = false\nwidth = 60\nheight = 6\n[feed]\ndocument = true\n[[commands]]\nid = \"run\"\ntitle = \"Run\"\n"

// tableRows is how many rows the table plugin's tables hold, and tablePage its first page.
const (
	tableRows = 10000
	tablePage = 100
)

// tableRow is row i of the test plugin's tables. Every 2500th has a cell a CSV must quote and a
// table row shows on one line.
func tableRow(i int) []string {
	name := fmt.Sprintf("row %d", i)
	if i%2500 == 0 {
		name = fmt.Sprintf("a \"quoted\", value\nrow %d", i)
	}
	return []string{strconv.Itoa(i), name}
}

// tablePlugin is a protocol-4 service serving tables of tableRows rows, a page at a time. In mode
// "tablestall" it answers only its first fetch, so a fetch stays waiting. In "tablereuse", before
// answering each fetch, it sends a new table under the id of the one being fetched (one row,
// "new"), as a plugin that runs a query again under a stable id would. In "table1000" its tables
// hold 1000 rows, sent in pages of at most 100. Its command "unsolicited" sends rows no fetch asked
// for.
type tablePlugin struct {
	plugin.Base
	mode    string
	peer    *plugin.Peer
	sent    map[string]int
	fetches int
}

func runTablePlugin(mode string) int {
	if err := plugin.Serve(context.Background(), &tablePlugin{mode: mode, sent: map[string]int{}}); err != nil {
		fmt.Fprintln(os.Stderr, "serve:", err)
		return 1
	}
	return 0
}

func (p *tablePlugin) say(line string) { fmt.Fprintln(os.Stderr, "said: "+line) }

func (p *tablePlugin) Open(peer *plugin.Peer, o plugin.Open) { p.peer = peer }

// total is how many rows the plugin's tables hold, and page the most it sends at once.
func (p *tablePlugin) total() (rows, page int) {
	if p.mode == "table1000" {
		return 1000, 100
	}
	return tableRows, tableRows
}

func (p *tablePlugin) Command(id string) {
	if id == "unsolicited" {
		_ = p.peer.Rows("q1", [][]string{{"unsolicited", "unsolicited"}}, true)
		p.say("sent unsolicited")
		return
	}
	t := map[string]string{"run": "q1", "run2": "q2"}[id]
	rows := make([][]string, tablePage)
	for i := range rows {
		rows[i] = tableRow(i)
	}
	p.sent[t] = tablePage
	err := p.peer.Table(plugin.TableOpen{ID: t, Fields: []string{"n", "name"}, Rows: rows, More: true})
	p.say(fmt.Sprintf("table %s %v", t, err))
}

func (p *tablePlugin) FetchRows(f plugin.TableFetch) {
	p.fetches++
	p.say(fmt.Sprintf("fetch %s %d", f.ID, f.N))
	if p.mode == "tablestall" && p.fetches > 1 {
		return // never answered: the host's fetch waits
	}
	if p.mode == "tablereuse" {
		_ = p.peer.Table(plugin.TableOpen{ID: f.ID, Fields: []string{"n", "name"}, Rows: [][]string{{"new", "new"}}, More: true})
	}
	total, page := p.total()
	from := p.sent[f.ID]
	to := min(from+min(f.N, page), total)
	rows := make([][]string, 0, to-from)
	for i := from; i < to; i++ {
		rows = append(rows, tableRow(i))
	}
	p.sent[f.ID] = to
	_ = p.peer.Rows(f.ID, rows, to < total)
}

func (p *tablePlugin) TableClosed(id string) { p.say("closed " + id) }

// snapLine is what the snap plugin says of a command: the document it runs on, its cursor and
// selections as offsets, and the text at each offset.
func snapLine(id string, d plugin.Document) string {
	text := fmt.Sprintf("%d bytes", len(d.Text))
	if len(d.Text) < 200 {
		text = strconv.Quote(d.Text)
	}
	rest := "" // two characters from the offset: one inside a character shows as U+FFFD
	if d.CursorByte >= 0 && d.CursorByte <= len(d.Text) {
		r := []rune(d.Text[d.CursorByte:])
		rest = string(r[:min(2, len(r))])
	}
	var sel []string
	for _, r := range d.SelectionBytes {
		if r[0] >= 0 && r[0] <= r[1] && r[1] <= len(d.Text) {
			sel = append(sel, strconv.Quote(d.Text[r[0]:r[1]]))
		}
	}
	return fmt.Sprintf("command %s v%d %s at %d:%d byte %d rest %q sel [%s] large=%v", id, d.Version, text,
		d.Cursor.Line, d.Cursor.Col, d.CursorByte, rest, strings.Join(sel, " "), d.TooLarge)
}

// hold is the snap plugin's w: it holds Serve's queue until the test writes the file release in
// the plugin's directory, so what the host sends meanwhile waits.
func (e *echo) hold() {
	e.say("held")
	release := filepath.Join(os.Getenv("AUTODOC_PLUGIN_DIR"), "release")
	for deadline := time.Now().Add(30 * time.Second); time.Now().Before(deadline); time.Sleep(10 * time.Millisecond) {
		if _, err := os.Stat(release); err == nil {
			break
		}
	}
	e.say("released")
}

// saidLines are what the plugin said that start with prefix.
func saidLines(t *testing.T, logs, name, prefix string) []string {
	t.Helper()
	return slices.DeleteFunc(said(t, logs, name), func(s string) bool { return !strings.HasPrefix(s, prefix) })
}

// waitSaidN waits until the plugin has said n lines starting with prefix, and returns them.
func waitSaidN(t *testing.T, logs, name, prefix string, n int) []string {
	t.Helper()
	deadline := time.Now().Add(20 * time.Second)
	for {
		if got := saidLines(t, logs, name, prefix); len(got) >= n {
			return got
		}
		if time.Now().After(deadline) {
			t.Fatalf("%s never said %d lines %q; it said %q", name, n, prefix, said(t, logs, name))
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// waitResults waits until the results panel shows table id with at least rows rows.
func (r *running) waitResults(t *testing.T, id string, rows int) {
	t.Helper()
	r.s.WaitFor(t, fmt.Sprintf("the table %s with %d rows", id, rows), func(string) bool {
		return onLoop(r, func() bool { return r.h.plugins.results.id == id && len(r.h.plugins.results.model.rows) >= rows })
	})
}

// slowFeed keeps the feed from sending a document during a test: what a plugin is sent of the note
// is then only what a command carries.
func slowFeed(t *testing.T) {
	delay := pluginFeedDelay
	pluginFeedDelay = time.Minute
	t.Cleanup(func() { pluginFeedDelay = delay })
}

// TestATableNeedsProtocolFour: [table] needs protocol = 4, so an AutoDoc that speaks 3 refuses the
// manifest rather than run a plugin whose results it cannot show; it names the panel's title, and
// the install question says it shows a table.
func TestATableNeedsProtocolFour(t *testing.T) {
	const head = "name = \"p\"\nkind = \"service\"\ncommand = [\"x\"]\n[[commands]]\nid = \"run\"\ntitle = \"Run\"\n"
	for _, c := range []struct{ toml, reason string }{
		{"protocol = 4\n" + head + "[table]\ntitle = \"Results\"\n", ""},
		{"protocol = 3\n" + head + "[table]\ntitle = \"Results\"\n", "declares [table]: needs protocol = 4"},
		{"protocol = 2\n" + head + "[table]\ntitle = \"Results\"\n", "declares [table]: needs protocol = 4"},
		{"protocol = 4\n" + head + "[table]\n", "[table]: no title"},
		{"protocol = 4\n" + head + "[table]\ntitle = \"R\"\nrows = 5\n", `unknown key "table.rows"`},
	} {
		dir := filepath.Join(t.TempDir(), "p")
		_ = os.MkdirAll(dir, 0o755)
		_ = os.WriteFile(filepath.Join(dir, "plugin.toml"), []byte(c.toml), 0o644)
		e := readPlugin(dir)
		if c.reason == "" && e.reason != "" || c.reason != "" && !strings.HasPrefix(e.reason, c.reason) {
			t.Errorf("%q: reason %q, want %q", c.toml, e.reason, c.reason)
		}
		if c.reason == "" && !strings.Contains(pluginDeclares(&e.m), "shows a table of results (Results)") {
			t.Errorf("the install question declares %q", pluginDeclares(&e.m))
		}
	}
}

// TestAFetchWaitsForItsAnswer: the table asks its plugin for more rows as its cursor nears the end
// of those loaded, and asks again only once the plugin has answered: however often the table wants
// more, a fetch waiting is never sent twice.
func TestAFetchWaitsForItsAnswer(t *testing.T) {
	dir := t.TempDir()
	installPlugin4(t, dir, "rows", "service", "tablestall", tableManifest)
	r, logs := pluginTUI(t, dir)
	onLoop(r, func() bool { r.h.plugins.runPluginCommand("rows", "run"); return true })
	// the cursor at the top of the first page is within a page of its end: the table asks once, and
	// the answer lands
	r.waitResults(t, "q1", tablePage+resultsPage)
	if got := saidLines(t, logs, "rows", "fetch "); len(got) != 1 {
		t.Fatalf("the first page asked %d times: %q", len(got), got)
	}
	onLoop(r, func() bool {
		tc := r.h.plugins.c.TableCore(resultsView)
		tc.SetCursor(widget.TablePos{Row: tc.Len() - 1})
		for range 5 {
			r.h.plugins.results.model.FetchMore(tuidecl.Index{})
		}
		return true
	})
	waitSaidN(t, logs, "rows", "fetch ", 2)
	time.Sleep(500 * time.Millisecond) // any fetch sent again would have reached the plugin
	if got := saidLines(t, logs, "rows", "fetch "); len(got) != 2 {
		t.Fatalf("a fetch was sent while one waited: %q", got)
	}
	if !onLoop(r, func() bool {
		res := r.h.plugins.results
		return res.pending && res.model.CanFetchMore(tuidecl.Index{})
	}) {
		t.Fatal("the waiting fetch is not pending, or the table says no more rows remain")
	}
}

// TestANewerTableClosesTheOld: a newer host.table replaces the one shown, and its plugin is told
// the old one is gone (plugin.table.close); closing the panel tells it of the one shown.
func TestANewerTableClosesTheOld(t *testing.T) {
	dir := t.TempDir()
	installPlugin4(t, dir, "rows", "service", "table", tableManifest)
	r, logs := pluginTUI(t, dir)
	onLoop(r, func() bool { r.h.plugins.runPluginCommand("rows", "run"); return true })
	r.waitResults(t, "q1", tablePage)
	r.s.WaitFor(t, "the results panel open for the table", func(string) bool {
		return onLoop(r, func() bool { return r.h.panelOpen[resultsPanel] })
	})
	if title := onLoop(r, func() string { v, _ := r.h.p.Tree().Source("App.resultsTitle"); return v.Raw }); !strings.HasPrefix(title, "results: Rows · ") {
		t.Errorf("the panel's heading: %q, want the manifest's [table] title", title)
	}
	if got := saidLines(t, logs, "rows", "closed "); len(got) != 0 {
		t.Fatalf("a table was closed before any was replaced: %q", got)
	}
	onLoop(r, func() bool { r.h.plugins.runPluginCommand("rows", "run2"); return true })
	r.waitResults(t, "q2", tablePage)
	if got := waitSaidN(t, logs, "rows", "closed ", 1); !slices.Equal(got, []string{"closed q1"}) {
		t.Fatalf("the replaced table: %q, want closed q1", got)
	}
	onLoop(r, func() bool { r.h.togglePanel(resultsPanel); return true })
	if got := waitSaidN(t, logs, "rows", "closed ", 2); got[1] != "closed q2" {
		t.Fatalf("the panel closed: %q, want closed q2", got)
	}
	if onLoop(r, func() bool { return r.h.plugins.results.id != "" || len(r.h.plugins.results.model.rows) != 0 }) {
		t.Fatal("the closed panel still holds a table")
	}
}

// readCSVFile is the CSV file at p, read whole.
func readCSVFile(t *testing.T, p string) [][]string {
	t.Helper()
	f, err := os.Open(p)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	records, err := csv.NewReader(f).ReadAll()
	if err != nil {
		t.Fatal(err)
	}
	return records
}

// wantAllRows checks an exported CSV: the header, then the plugin's n rows, each once, in order.
func wantAllRows(t *testing.T, records [][]string, n int) {
	t.Helper()
	if len(records) != n+1 || !slices.Equal(records[0], []string{"n", "name"}) {
		t.Fatalf("%d records, header %q; want %d rows under n, name", len(records), records[0], n)
	}
	for i, rec := range records[1:] {
		if !slices.Equal(rec, tableRow(i)) {
			t.Fatalf("row %d: %q, want %q", i, rec, tableRow(i))
		}
	}
}

// TestExportingAllRowsAsCSV: Export all rows as CSV drains the plugin's pages to the last and
// writes every row, in order, quoted where a cell needs it; the rows land in the table too, and no
// .part is left. A file there already is asked about, and replaced once the rows are written.
func TestExportingAllRowsAsCSV(t *testing.T) {
	dir := t.TempDir()
	installPlugin4(t, dir, "rows", "service", "table", tableManifest)
	r, _ := pluginTUI(t, dir)
	onLoop(r, func() bool { r.h.plugins.runPluginCommand("rows", "run"); return true })
	r.waitResults(t, "q1", tablePage)

	out := t.TempDir()
	path := filepath.Join(out, "rows.csv")
	onLoop(r, func() bool { r.h.plugins.exportResults(path); return true })
	r.waitNotice(t, fmt.Sprintf("%d rows exported to %s", tableRows, path))
	readCSV := func(p string) [][]string { t.Helper(); return readCSVFile(t, p) }
	wantAllRows(t, readCSV(path), tableRows)
	if _, err := os.Stat(path + ".part"); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("the .part is left: %v", err)
	}
	if n := onLoop(r, func() int { return len(r.h.plugins.results.model.rows) }); n != tableRows {
		t.Errorf("the table holds %d rows after the export, want all %d", n, tableRows)
	}

	// a file there already: asked first, and replaced
	if err := os.WriteFile(path, []byte("old\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	onLoop(r, func() bool { r.h.plugins.exportResults(path); return true })
	r.s.WaitFor(t, "the replace question", func(string) bool {
		return onLoop(r, func() bool { v, _ := r.h.p.Tree().Source("App.askTitle"); return v.Raw == "replace rows.csv?" })
	})
	if b, _ := os.ReadFile(path); string(b) != "old\n" {
		t.Fatal("the file was written before the question was answered")
	}
	onLoop(r, func() bool { r.h.asker.accepted(); return true })
	r.s.WaitFor(t, "the file replaced", func(string) bool { b, _ := os.ReadFile(path); return len(b) > 4 })
	if records := readCSV(path); len(records) != tableRows+1 {
		t.Fatalf("the replaced file holds %d records", len(records))
	}
}

// TestACancelledExportLeavesNothing: an export cancelled after rows were written to its .part (the
// panel closed under it) removes the .part and leaves the chosen path as it was: nothing.
func TestACancelledExportLeavesNothing(t *testing.T) {
	dir := t.TempDir()
	installPlugin4(t, dir, "rows", "service", "tablestall", tableManifest)
	r, logs := pluginTUI(t, dir)
	onLoop(r, func() bool { r.h.plugins.runPluginCommand("rows", "run"); return true })
	r.waitResults(t, "q1", tablePage+resultsPage)

	path := filepath.Join(t.TempDir(), "rows.csv")
	onLoop(r, func() bool { r.h.plugins.exportResults(path); return true })
	// the loaded rows written, then the next fetch waits, never answered
	r.s.WaitFor(t, "a page written to the .part", func(string) bool {
		return onLoop(r, func() bool { x := r.h.plugins.export; return x != nil && x.written > 0 })
	})
	if b, err := os.ReadFile(path + ".part"); err != nil || strings.Count(string(b), "\n") < 2 {
		t.Fatalf("the .part before the cancel: %d bytes, %v; want the header and rows", len(b), err)
	}
	onLoop(r, func() bool { r.h.togglePanel(resultsPanel); return true })
	r.waitNotice(t, "the results were not exported: cancelled: the results panel closed; "+path+" not written")
	for _, p := range []string{path, path + ".part"} {
		if _, err := os.Stat(p); !errors.Is(err, os.ErrNotExist) {
			t.Errorf("%s is left after the cancel: %v", p, err)
		}
	}
	waitSaidN(t, logs, "rows", "closed q1", 1)
}

// TestInsertingTheLoadedRowsAsMarkdown: Insert loaded rows as a Markdown table puts the rows loaded
// now into the page, on lines of their own, a cell on one line with its | escaped; while rows remain
// the notice says so, and that the CSV export takes them all.
func TestInsertingTheLoadedRowsAsMarkdown(t *testing.T) {
	dir := t.TempDir()
	installPlugin4(t, dir, "rows", "service", "tablestall", tableManifest)
	r, _ := pluginTUI(t, dir)
	onLoop(r, func() bool { r.h.plugins.runPluginCommand("rows", "run"); return true })
	r.waitResults(t, "q1", tablePage+resultsPage)
	n := onLoop(r, func() int {
		r.h.editor.SetValue("intro\nafter")
		r.h.core.SetCursor(widget.TextPos{})
		r.h.plugins.insertResults()
		return len(r.h.plugins.results.model.rows)
	})
	r.waitNotice(t, fmt.Sprintf("%d loaded rows inserted; more remain (Export all rows as CSV takes them all)", n))
	text := r.editorText()
	if !strings.HasPrefix(text, "intro\n\n| n ") || !strings.HasSuffix(text, "|\n\nafter") {
		t.Fatalf("the table is not a block of its own:\n%.200s\n…\n%.200s", text, text[max(len(text)-200, 0):])
	}
	var rows []string
	for line := range strings.SplitSeq(text, "\n") {
		if strings.HasPrefix(line, "|") {
			rows = append(rows, line)
		}
	}
	if len(rows) != n+2 {
		t.Fatalf("%d table lines, want the header, the delimiter and %d rows", len(rows), n)
	}
	if !strings.Contains(rows[2], "a \"quoted\", value row 0") || !strings.HasPrefix(rows[1], "| --") {
		t.Errorf("the delimiter %q, the first row %q", rows[1], rows[2])
	}
	if got, want := markdownTable([]string{"a|b"}, [][]string{{"x|y"}}), "| a\\|b |\n| ---- |\n| x\\|y |\n"; got != want {
		t.Errorf("a | in a cell: %q, want %q", got, want)
	}
}

// TestACommandCarriesTheNoteItWasChosenOn: an edit, a move and a selection, each followed at once
// by the command with no pause for the feed, run on the new text, cursor and selection: the command
// carries them. The byte offsets are the grapheme cursor's, on multibyte text.
func TestACommandCarriesTheNoteItWasChosenOn(t *testing.T) {
	slowFeed(t)
	dir := t.TempDir()
	installPlugin4(t, dir, "snap", "dialog", "snap", snapManifest)
	r, logs := pluginTUI(t, dir)
	onLoop(r, func() bool { r.h.plugins.openPlugin("snap"); return true })
	waitSaid(t, logs, "snap", "doc ")
	run := func(what string, do func()) string {
		t.Helper()
		n := len(saidLines(t, logs, "snap", "command "))
		v := onLoop(r, func() int { do(); r.h.plugins.runPluginCommand("snap", "run"); return r.h.pageVersion })
		got := waitSaidN(t, logs, "snap", "command ", n+1)[n]
		if !strings.HasPrefix(got, fmt.Sprintf("command run v%d ", v)) {
			t.Fatalf("%s: %q, want the page's version %d", what, got, v)
		}
		return got
	}
	edited := func(text string, line, col int) func() {
		return func() {
			r.h.editor.SetValue(text)
			r.h.edited()
			r.h.core.SetCursor(widget.TextPos{Line: line, Col: col})
		}
	}
	if got := run("an edit", edited("SELECT 1;\nSELECT 2;", 1, 3)); !strings.Contains(got, `"SELECT 1;\nSELECT 2;" at 2:4 byte 13 rest "EC" sel [] large=false`) {
		t.Errorf("an edit, then the command: %q", got)
	}
	if got := run("a move", func() { r.h.core.SetCursor(widget.TextPos{}) }); !strings.Contains(got, `at 1:1 byte 0 rest "SE"`) {
		t.Errorf("a move, then the command: %q", got)
	}
	selectLine := func() {
		r.h.core.Select(widget.TextRange{From: widget.TextPos{Line: 1}, To: widget.TextPos{Line: 1, Col: 9}})
	}
	if got := run("a selection", selectLine); !strings.Contains(got, `sel ["SELECT 2;"]`) {
		t.Errorf("a selection, then the command: %q", got)
	}
	onLoop(r, func() bool { r.h.core.Unselect(); return true })

	// multibyte: an e with a combining accent (3 bytes), a family emoji (18 bytes, one cluster), x;
	// then Hangul (3 bytes a syllable)
	const accent, family = "é", "\U0001F468‍\U0001F469‍\U0001F467"
	text := accent + family + "x\n한국어;"
	got := run("multibyte", edited(text, 0, 2))
	if want := fmt.Sprintf(`at 1:3 byte %d rest "x\n"`, len(accent+family)); !strings.Contains(got, want) {
		t.Errorf("the cursor on x after two clusters: %q, want %s", got, want)
	}
	got = run("multibyte, line two", func() { r.h.core.SetCursor(widget.TextPos{Line: 1, Col: 2}) })
	if want := fmt.Sprintf(`at 2:3 byte %d rest "어;"`, len(accent+family+"x\n한국")); !strings.Contains(got, want) {
		t.Errorf("the cursor on the third syllable: %q, want %s", got, want)
	}
	got = run("a multibyte selection", func() {
		r.h.core.Select(widget.TextRange{From: widget.TextPos{Line: 0, Col: 1}, To: widget.TextPos{Line: 0, Col: 2}})
	})
	if want := fmt.Sprintf(`sel [%q]`, family); !strings.Contains(got, want) {
		t.Errorf("the family selected: %q, want %s", got, want)
	}
}

// TestACommandRunsOnItsOwnNoteWhenANewerOneFollows: with the plugin's queue held, the command (its
// note inside it) and then a newer feed document are sent; the newer document replaces any
// document waiting, but not the command's: the command runs on the note it was chosen on, and the
// newer one arrives after it.
func TestACommandRunsOnItsOwnNoteWhenANewerOneFollows(t *testing.T) {
	slowFeed(t)
	dir := t.TempDir()
	installPlugin4(t, dir, "snap", "dialog", "snap", snapManifest)
	r, logs := pluginTUI(t, dir)
	onLoop(r, func() bool { r.h.plugins.openPlugin("snap"); return true })
	waitSaid(t, logs, "snap", "doc ")
	onLoop(r, func() bool {
		r.h.plugins.running["snap"].send(plugin.MethodKey, plugin.KeyParams(plugin.Key{Key: "w"}))
		return true
	})
	waitSaid(t, logs, "snap", "held")
	onLoop(r, func() bool {
		r.h.editor.SetValue("chosen")
		r.h.edited()
		r.h.plugins.runPluginCommand("snap", "run")
		r.h.editor.SetValue("typed after it")
		r.h.edited()
		r.h.plugins.feedDocuments() // a newer document, at once
		return true
	})
	time.Sleep(300 * time.Millisecond) // both on their way through the link, to the held queue
	if err := os.WriteFile(filepath.Join(dir, "snap", "release"), nil, 0o644); err != nil {
		t.Fatal(err)
	}
	cmd := waitSaidN(t, logs, "snap", "command ", 1)[0]
	if !strings.Contains(cmd, `"chosen"`) {
		t.Fatalf("the command ran on %q, want the note it was chosen on", cmd)
	}
	all := waitSaid(t, logs, "snap", "doc v")
	after := slices.IndexFunc(all, func(s string) bool { return strings.HasPrefix(s, "command ") })
	if !slices.ContainsFunc(all[after:], func(s string) bool { return strings.Contains(s, " 14 bytes") }) {
		t.Fatalf("the newer document never followed the command: %q", all)
	}
}

// TestACommandTooLargeForTheLinkGoesTooLarge: a note that fits a plugin.document of its own but not
// inside a plugin.command goes with the command as its too-large form, through the real link; the
// plugin stays connected, and the next command carries its note whole.
func TestACommandTooLargeForTheLinkGoesTooLarge(t *testing.T) {
	slowFeed(t)
	dir := t.TempDir()
	installPlugin4(t, dir, "snap", "dialog", "snap", snapManifest)
	r, logs := pluginTUI(t, dir)
	run := onLoop(r, func() *pluginRun { r.h.plugins.openPlugin("snap"); return r.h.plugins.running["snap"] })
	waitSaid(t, logs, "snap", "doc ")
	target := plugin.MaxMessageBytes - 4
	ok := onLoop(r, func() bool {
		lines := strings.Repeat(strings.Repeat("x", 79)+"\n", target/80-2)
		tail := 10
		var d plugin.Document
		for range 4 { // the version's encoding can grow a byte as the text is set again
			r.h.editor.SetValue(lines + strings.Repeat("y", tail))
			r.h.edited()
			r.h.core.SetCursor(widget.TextPos{})
			d = r.h.plugins.document()
			n, _ := plugin.EncodedSize(plugin.MethodDocument, plugin.DocumentParamsAt(4, d))
			if n == target {
				break
			}
			tail += target - n
		}
		doc, _ := plugin.EncodedSize(plugin.MethodDocument, plugin.DocumentParamsAt(4, d))
		cmd, _ := plugin.EncodedSize(plugin.MethodCommand, plugin.CommandDocParams("run", d))
		if doc > plugin.MaxMessageBytes || cmd <= plugin.MaxMessageBytes {
			t.Errorf("the note encodes to %d alone and %d in the command: the cell tests nothing", doc, cmd)
			return false
		}
		r.h.plugins.runPluginCommand("snap", "run")
		return true
	})
	if !ok {
		return
	}
	if got := waitSaidN(t, logs, "snap", "command ", 1)[0]; !strings.Contains(got, ` "" at 0:0 `) || !strings.HasSuffix(got, "large=true") {
		t.Fatalf("the command too large for the link: %q, want its note's too-large form", got)
	}
	onLoop(r, func() bool {
		r.h.editor.SetValue("SELECT 1;")
		r.h.edited()
		r.h.plugins.runPluginCommand("snap", "run")
		return true
	})
	if got := waitSaidN(t, logs, "snap", "command ", 2)[1]; !strings.Contains(got, `"SELECT 1;"`) || !strings.HasSuffix(got, "large=false") {
		t.Fatalf("the next command: %q", got)
	}
	if onLoop(r, func() bool { return r.h.plugins.running["snap"] != run || run.closing }) {
		t.Fatal("the plugin was closed")
	}
}

// TestASQLFileOpensAsSource: a file a source language claims (*.sql) opens as code: SQL's syntax,
// read as plain text, with no Markdown table keys, though its text looks like a Markdown table; a
// note beside it keeps them.
func TestASQLFileOpensAsSource(t *testing.T) {
	const table = "| a | b |\n|---|---|\n| 1 | 2 |\n"
	d := startDaemonWith(t, "", map[string][]string{"kb": {"a.md", table, "q.sql", table}}, daemonOpts{goFiles: true, chunked: ".sql"})
	r := runTUI(t, NewSession(d.sock, nil), Options{})
	r.s.WaitFor(t, "the files listed", func(string) bool { return len(r.listed()) >= 2 })
	r.h.p.Post(func() { r.h.openPath("q.sql") })
	r.waitFile(t, "q.sql")
	syntax := func() string {
		return onLoop(r, func() string { v, _ := r.h.p.Tree().Source("App.syntaxDefinition"); return v.Raw })
	}
	if k, tables, s := onLoop(r, r.h.pageKind), onLoop(r, r.h.core.TableEditing), syntax(); k != kind.Text || tables || s != "SQL (PostgreSQL)" {
		t.Fatalf("q.sql: read as %s, table keys %v, syntax %q; want text, none, SQL", k, tables, s)
	}
	r.h.p.Post(func() { r.h.openPath("a.md") })
	r.waitFile(t, "a.md")
	if k, tables := onLoop(r, r.h.pageKind), onLoop(r, r.h.core.TableEditing); k != kind.Markdown || !tables {
		t.Fatalf("a.md: read as %s, table keys %v; want Markdown with them", k, tables)
	}
}

// wantShownRows checks the panel's rows are the plugin's first n, in order: nothing of another
// table, and nothing no fetch asked for.
func (r *running) wantShownRows(t *testing.T, n int) {
	t.Helper()
	rows := onLoop(r, func() [][]string { return slices.Clone(r.h.plugins.results.model.rows) })
	if len(rows) != n {
		t.Fatalf("the panel holds %d rows, want %d", len(rows), n)
	}
	for i, row := range rows {
		if !slices.Equal(row, tableRow(i)) {
			t.Fatalf("row %d is %q, want %q: rows of another table joined", i, row, tableRow(i))
		}
	}
}

// pluginLogHas reports whether the plugin's log holds text: what the host noted of it.
func pluginLogHas(logs, name, text string) bool {
	b, _ := os.ReadFile(filepath.Join(logs, name+".log"))
	return strings.Contains(string(b), text)
}

// TestATableReusingTheShownIDIsRefused: with a fetch on its way, the plugin sends a new table under
// the shown table's id. It is refused and noted; the table shown, and its rows, are as they were, and
// the fetch's answer is taken as the shown table's: nothing of the new one mixes in.
func TestATableReusingTheShownIDIsRefused(t *testing.T) {
	dir := t.TempDir()
	installPlugin4(t, dir, "rows", "service", "tablereuse", tableManifest)
	r, logs := pluginTUI(t, dir)
	onLoop(r, func() bool { r.h.plugins.runPluginCommand("rows", "run"); return true })
	// the first page's cursor is within a page of its end: one fetch, answered after the reuse
	r.waitResults(t, "q1", tablePage+resultsPage)
	r.wantShownRows(t, tablePage+resultsPage)
	if !pluginLogHas(logs, "rows", `host.table reused the shown table's id "q1"; a new table needs a new id`) {
		t.Fatalf("the refusal is not in the plugin's log: %q", said(t, logs, "rows"))
	}
	if got := saidLines(t, logs, "rows", "closed "); len(got) != 0 {
		t.Fatalf("the shown table was closed by its reuse: %q", got)
	}
}

// TestAReusedIDLeavesAnExportWhole: the same reuse at every fetch while the CSV export drains the
// table: the file holds every row once, in order, none of the refused tables'.
func TestAReusedIDLeavesAnExportWhole(t *testing.T) {
	dir := t.TempDir()
	installPlugin4(t, dir, "rows", "service", "tablereuse", tableManifest)
	r, _ := pluginTUI(t, dir)
	onLoop(r, func() bool { r.h.plugins.runPluginCommand("rows", "run"); return true })
	r.waitResults(t, "q1", tablePage)
	path := filepath.Join(t.TempDir(), "rows.csv")
	onLoop(r, func() bool { r.h.plugins.exportResults(path); return true })
	r.waitNotice(t, fmt.Sprintf("%d rows exported to %s", tableRows, path))
	wantAllRows(t, readCSVFile(t, path), tableRows)
	r.wantShownRows(t, tableRows)
}

// TestRowsNoFetchAskedForAreDropped: host.rows with no fetch waiting are dropped and noted, the
// table as it was.
func TestRowsNoFetchAskedForAreDropped(t *testing.T) {
	dir := t.TempDir()
	installPlugin4(t, dir, "rows", "service", "tablestall", tableManifest)
	r, logs := pluginTUI(t, dir)
	onLoop(r, func() bool { r.h.plugins.runPluginCommand("rows", "run"); return true })
	r.waitResults(t, "q1", tablePage+resultsPage)
	// the cursor is at the top, far from the end: no fetch waits
	if onLoop(r, func() bool { return r.h.plugins.results.pending }) {
		t.Fatal("a fetch waits: the cell needs none")
	}
	onLoop(r, func() bool { r.h.plugins.runPluginCommand("rows", "unsolicited"); return true })
	waitSaid(t, logs, "rows", "sent unsolicited")
	r.s.WaitFor(t, "the rows noted as dropped", func(string) bool {
		return pluginLogHas(logs, "rows", `host.rows for "q1" dropped: no fetch is waiting for them`)
	})
	r.wantShownRows(t, tablePage+resultsPage)
}

// TestThePanelHoldsAtMostItsCap: with the cap lowered to 250 and a 1000-row table in pages of 100,
// the panel stops asking at 250, and its heading says the first rows are shown and more remain;
// the export writes all 1000 rows in order, and the panel never holds more than 250 (every change
// the model announces is measured), showing the first 250 after.
func TestThePanelHoldsAtMostItsCap(t *testing.T) {
	cap := resultsResident
	resultsResident = 250
	t.Cleanup(func() { resultsResident = cap })
	dir := t.TempDir()
	installPlugin4(t, dir, "rows", "service", "table1000", tableManifest)
	r, logs := pluginTUI(t, dir)
	high := 0
	onLoop(r, func() bool {
		m := r.h.plugins.results.model
		m.Subscribe(func(tuidecl.Change) { high = max(high, len(m.rows)) })
		r.h.plugins.runPluginCommand("rows", "run")
		return true
	})
	// the reader walks to the last row loaded, again as each page lands: the table asks for more
	// each time, until the cap
	r.s.WaitFor(t, "the panel filled to its cap", func(string) bool {
		return onLoop(r, func() bool {
			if tc := r.h.plugins.c.TableCore(resultsView); tc != nil && tc.Len() > 0 {
				tc.SetCursor(widget.TablePos{Row: tc.Len() - 1})
			}
			return r.h.plugins.results.id == "q1" && len(r.h.plugins.results.model.rows) >= 250
		})
	})
	title := func() string {
		return onLoop(r, func() string { v, _ := r.h.p.Tree().Source("App.resultsTitle"); return v.Raw })
	}
	r.s.WaitFor(t, "the heading at the cap", func(string) bool {
		return strings.Contains(title(), "the first 250 rows shown; more remain (Export CSV takes them all)")
	})
	onLoop(r, func() bool { // at the last row again: at the cap, nothing is asked
		tc := r.h.plugins.c.TableCore(resultsView)
		tc.SetCursor(widget.TablePos{Row: tc.Len() - 1})
		r.h.plugins.results.model.FetchMore(tuidecl.Index{})
		return true
	})
	time.Sleep(300 * time.Millisecond) // a fetch past the cap would have been answered by now
	if n := onLoop(r, func() int { return len(r.h.plugins.results.model.rows) }); n != 250 || onLoop(r, func() bool {
		return r.h.plugins.results.model.CanFetchMore(tuidecl.Index{})
	}) {
		t.Fatalf("at the cap: %d rows, and the table still asks for more", n)
	}
	fetches := len(saidLines(t, logs, "rows", "fetch "))

	path := filepath.Join(t.TempDir(), "rows.csv")
	onLoop(r, func() bool { r.h.plugins.exportResults(path); return true })
	r.waitNotice(t, "1000 rows exported to "+path)
	wantAllRows(t, readCSVFile(t, path), 1000)
	if got := onLoop(r, func() int { return high }); got > 250 {
		t.Fatalf("the panel held %d rows at its most, past its cap of 250", got)
	}
	r.wantShownRows(t, 250)
	if !strings.Contains(title(), "the first 250 rows shown; the rest were exported, not kept") {
		t.Errorf("the heading after the export: %q", title())
	}
	if n := len(saidLines(t, logs, "rows", "fetch ")); n <= fetches {
		t.Errorf("the export fetched nothing past the cap: %d fetches before it, %d after", fetches, n)
	}
	onLoop(r, func() bool { r.h.plugins.beginExport(filepath.Join(t.TempDir(), "again.csv")); return true })
	r.waitNotice(t, "only the first 250 rows were kept; run the query again to export them all")
}

// resultsTUI is the TUI with the plugins in dir, over a daemon of workspaces and o: pluginTUI's,
// for a cell that needs another daemon (a build that derives PDFs) or quits the TUI itself.
func resultsTUI(t *testing.T, dir string, workspaces map[string][]string, o daemonOpts) (*running, string) {
	t.Helper()
	logs := t.TempDir()
	o.prefs = testPrefs
	d := startDaemonWith(t, "", workspaces, o)
	r := runTUI(t, NewSession(d.sock, nil), Options{Plugins: Plugins{Dir: dir, LogDir: logs, Socket: d.sock}})
	r.s.WaitForText(t, "connected — autodoc v-test")
	return r, logs
}

// app runs one of the App.* commands, as the panel's buttons and the Go menu do.
func (r *running) app(t *testing.T, name string, args ...qml.SpecValue) {
	t.Helper()
	if err := onLoop(r, func() error { return r.h.reg.handlers[name](args) }); err != nil {
		t.Fatalf("%s: %v", name, err)
	}
}

// TestTheResultsCommandsRunFromTheCatalogAndThePanel: the results commands, run as the panel's
// buttons, the Go menu and the catalog run them. Copy puts the cursor's cell on the clipboard;
// Export CSV… opens the save dialog at the page's folder; Insert as Markdown puts the table under
// the paragraph above the cursor, a blank line between, and on a read-only page it says why it
// cannot; results.toggle closes the panel, telling the plugin its table is gone, and Go › Results
// opens it again, empty.
func TestTheResultsCommandsRunFromTheCatalogAndThePanel(t *testing.T) {
	dir := t.TempDir()
	installPlugin4(t, dir, "rows", "service", "tablestall", tableManifest)
	r, logs := resultsTUI(t, dir, map[string][]string{"kb": {"a.md", "# A\n", "manual.pdf", manualPDF}}, daemonOpts{deriver: pdfAsText{}})
	onLoop(r, func() bool { r.h.plugins.runPluginCommand("rows", "run"); return true })
	r.waitResults(t, "q1", tablePage+resultsPage)

	r.app(t, "App.copyResults")
	r.s.WaitFor(t, "the cursor's cell on the clipboard", func(string) bool { return string(r.s.Backend.Clipboard()) == "0" })

	r.app(t, "App.startExportResults")
	r.s.WaitForText(t, "export all rows as CSV")
	if folder := onLoop(r, func() string { v, _ := r.h.p.Tree().Source("App.browseFolder"); return v.Raw }); folder != onLoop(r, r.h.browseFolder) || folder == "" {
		t.Errorf("the save dialog starts at %q, want the page's folder", folder)
	}
	onLoop(r, func() bool { r.h.closeDialog("exportResults"); return true })

	r.s.WaitFor(t, "the daemon's derived formats", func(string) bool {
		return onLoop(r, func() bool { return r.h.kinds.Readable("manual.pdf") })
	})
	r.h.p.Post(func() { r.h.openPath("manual.pdf") })
	r.waitFile(t, "manual.pdf")
	if err := onLoop(r, func() error { return r.h.cat.run("results.markdown", nil) }); err != nil {
		t.Fatal(err)
	}
	r.waitNotice(t, "insert the results: widget: the editor is read-only")
	if r.editorText() != manualPDF {
		t.Fatalf("the read-only page changed: %q", r.editorText())
	}

	onLoop(r, func() bool { r.h.closeFile(); return true })
	r.s.WaitFor(t, "the page closed", func(string) bool { return !r.file().open })

	// on a blank line under a paragraph: a blank line kept between them, and before the next
	r.page("intro\n\nafter", 2, 1)
	r.app(t, "App.insertResults")
	r.waitNotice(t, "loaded rows inserted; more remain")
	text := r.editorText()
	if lines := strings.Split(text, "\n"); len(lines) < 5 || lines[0] != "intro" || lines[1] != "" || !strings.HasPrefix(lines[2], "| n ") ||
		lines[len(lines)-2] != "" || lines[len(lines)-1] != "after" {
		t.Fatalf("the table under the paragraph:\n%.120s\n…\n%s", text, text[max(len(text)-120, 0):])
	}

	if err := onLoop(r, func() error { return r.h.cat.run("results.toggle", nil) }); err != nil {
		t.Fatal(err)
	}
	waitSaidN(t, logs, "rows", "closed q1", 1)
	r.s.WaitFor(t, "the panel closed", func(string) bool { return !onLoop(r, func() bool { return r.h.panelOpen[resultsPanel] }) })
	r.app(t, "App.toggleResults")
	r.s.WaitFor(t, "the panel open, empty", func(string) bool {
		return onLoop(r, func() bool { return r.h.panelOpen[resultsPanel] && len(r.h.plugins.results.model.rows) == 0 })
	})
	r.app(t, "App.copyResults")
	r.waitNotice(t, "no results to copy")
}

// TestQuittingStopsAnExport: AutoDoc quitting with an export not finished cancels it: the .part is
// removed and the chosen path never written.
func TestQuittingStopsAnExport(t *testing.T) {
	dir := t.TempDir()
	installPlugin4(t, dir, "rows", "service", "tablestall", tableManifest)
	r, _ := resultsTUI(t, dir, map[string][]string{"kb": {"a.md", "a\n"}}, daemonOpts{})
	onLoop(r, func() bool { r.h.plugins.runPluginCommand("rows", "run"); return true })
	r.waitResults(t, "q1", tablePage+resultsPage)
	path := filepath.Join(t.TempDir(), "rows.csv")
	onLoop(r, func() bool { r.h.plugins.exportResults(path); return true })
	r.s.WaitFor(t, "a page written to the .part", func(string) bool {
		return onLoop(r, func() bool { x := r.h.plugins.export; return x != nil && x.written > 0 })
	})
	if _, err := os.Stat(path + ".part"); err != nil {
		t.Fatalf("no .part before quitting: %v", err)
	}
	onLoop(r, func() bool { r.h.p.Quit(); return true }) // nothing runs on the loop after this
	select {
	case <-r.s.Quit():
	case <-time.After(20 * time.Second):
		t.Fatal("the program did not stop")
	}
	r.h.reg.stopped() // what Run does once the loop has ended: each feature stops what it started
	_, partErr := os.Stat(path + ".part")
	_, pathErr := os.Stat(path)
	if !errors.Is(partErr, os.ErrNotExist) || !errors.Is(pathErr, os.ErrNotExist) {
		t.Fatalf("after quitting: .part %v, the file %v; want neither", partErr, pathErr)
	}
}

// TestTheHostsTableAndBlockServices: TableCore answers only a cell-cursor TableView (the results'),
// never another view or an id that is none; before the program runs the host has neither a table
// nor a page to insert into.
func TestTheHostsTableAndBlockServices(t *testing.T) {
	early := core{newHost(NewSession("unused", nil), Options{})}
	if early.TableCore(resultsView) != nil {
		t.Error("a table before the program runs")
	}
	if err := early.InsertBlock("| a |\n"); err == nil || err.Error() != "no page is open" {
		t.Errorf("an insert before the program runs: %v", err)
	}
	r := runTUI(t, NewSession("unused", nil), Options{})
	onLoop(r, func() bool {
		c := core{r.h}
		if c.TableCore(resultsView) == nil {
			t.Error("no core behind the results' table")
		}
		for _, id := range []string{"editor", "no-such-view"} {
			if c.TableCore(id) != nil {
				t.Errorf("TableCore(%q) answered a core", id)
			}
		}
		return true
	})
}
