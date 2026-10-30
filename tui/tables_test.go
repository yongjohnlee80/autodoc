package tui

import (
	"testing"

	tuicore "github.com/yongjohnlee80/golib/tui"
	"github.com/yongjohnlee80/golib/tui/widget"
)

// tableInclude admits CSV and TSV files beside the notes: a workspace's default include does not.
var tableInclude = []string{"**/*.md", "**/*.csv", "**/*.tsv"}

// A CSV or TSV file opens as an editable table in the terminal's editor
// (docs/decisions/editor.md#csv-and-tsv-files-as-editable-tables): the editor starts Rendered, a
// cell kept is the file's text and modifies the buffer, the editor's u undoes it; Ctrl+T shows the
// text and that choice holds; each table file has its own table, kept with its buffer, and a note
// has none.
func TestACSVFileIsATableInTheTerminal(t *testing.T) {
	d := startManagedIncluding(t, map[string]string{"kb": fileDir(t, "a.csv", "h,v\na,1\nb,2\n", "b.tsv", "h\tv\nx\t1\n", "c.md", "# Note\n")}, tableInclude)
	r := runTUI(t, NewSession(d.sock, nil), Options{})
	r.s.WaitForText(t, "· kb")
	ed := func() *widget.Editor {
		return onLoop(r, func() *widget.Editor { e, _ := r.h.editor.(*widget.Editor); return e })
	}
	if ed() == nil {
		t.Fatalf("the terminal's editor is a %T", onLoop(r, func() editorWidget { return r.h.editor }))
	}
	type shown struct {
		view  widget.EditorView
		table *widget.TableDocument
	}
	now := func() shown {
		return onLoop(r, func() shown {
			e := r.h.editor.(*widget.Editor)
			tb, _ := e.DocumentView().(*widget.TableDocument)
			return shown{e.ShownView(), tb}
		})
	}
	ctrlT := tuicore.KeyEvent{Kind: tuicore.KeyPress, Code: 't', Mods: tuicore.ModCtrl}

	onLoop(r, func() bool { r.h.openPath("a.csv"); return true })
	r.waitFile(t, "a.csv")
	// the editor starts Rendered (startRendered): a table file opens as its table, no key pressed
	r.s.WaitFor(t, "the table shown as it opens", func(string) bool { return now().view == widget.ViewRendered })
	first := now().table
	if first == nil {
		t.Fatal("a.csv's Rendered view is not a table")
	}
	r.keys(t, tuicore.KeyEvent{Kind: tuicore.KeyPress, Code: tuicore.KeyEnter},
		tuicore.KeyEvent{Kind: tuicore.KeyPress, Code: '9', Text: "9"},
		tuicore.KeyEvent{Kind: tuicore.KeyPress, Code: tuicore.KeyEnter})
	r.s.WaitFor(t, "the cell kept", func(string) bool { return r.editorText() == "h,v\na9,1\nb,2\n" })
	if !r.file().dirty {
		t.Fatal("a cell kept did not modify the buffer")
	}
	r.keys(t, tuicore.KeyEvent{Kind: tuicore.KeyPress, Code: 'u', Text: "u"})
	r.s.WaitFor(t, "u in the table", func(string) bool { return r.editorText() == "h,v\na,1\nb,2\n" })

	// Ctrl+T shows the text, and the choice holds: the next table file opens as its text too
	r.keys(t, ctrlT)
	r.s.WaitFor(t, "the text after Ctrl+T", func(string) bool { return now().view == widget.ViewRaw })
	onLoop(r, func() bool { r.h.openPath("b.tsv"); return true })
	r.waitFile(t, "b.tsv")
	r.s.WaitFor(t, "b.tsv's table, its text shown", func(string) bool {
		s := now()
		return s.table != nil && s.table != first && s.view == widget.ViewRaw
	})
	r.keys(t, ctrlT)
	r.s.WaitFor(t, "b.tsv's table after Ctrl+T", func(string) bool { return now().view == widget.ViewRendered })
	tsv := now().table // on the loop, then its cell on the loop: never one onLoop inside another
	if got := onLoop(r, func() string { return tsv.Core().Cell(0, 1) }); got != "1" {
		t.Fatalf("b.tsv's first record's second field: %q, want 1 (read with tabs)", got)
	}

	onLoop(r, func() bool { r.h.openPath("c.md"); return true })
	r.waitFile(t, "c.md")
	r.s.WaitFor(t, "a note drawn as text", func(string) bool { return now().view == widget.ViewRaw })

	onLoop(r, func() bool { r.h.openPath("a.csv"); return true }) // its buffer, shown again: no load
	r.s.WaitFor(t, "a.csv's buffer shown", func(string) bool { return r.file().path == "a.csv" })
	r.s.WaitFor(t, "a.csv's own table again", func(string) bool { s := now(); return s.table == first && s.view == widget.ViewRendered })
}

// pageEditor is a terminal editor that also offers an HTML page as its document component, as the
// GUI's does: the default build's stand-in for the window, for documentFor's page path.
type pageEditor struct {
	*widget.Editor
	page *widget.HTMLView
}

func (e pageEditor) DocumentComponent() tuicore.Component { return e.page }

// The editor's page view is read once and kept: a table taking the document view's place does not
// lose it, and a raw HTML file after the table gets that page, turned on, while any other file turns
// the document view off.
func TestThePageViewIsKeptWhileATableIsShown(t *testing.T) {
	h := newHost(NewSession("unused", nil), Options{})
	page := widget.NewHTMLView()
	h.core = widget.NewEditorCore(widget.CoreInitialText("h,v\na,1\n"))
	h.editor = pageEditor{Editor: widget.NewEditor(), page: page}
	h.nativeViews = func() bool { return true }
	var on []bool
	h.docSwitch = func(o bool) { on = append(on, o) }
	h.active = &openBuffer{}

	h.documentFor("kb", "a.csv", false)
	if h.active.table == nil || h.pageDoc != page {
		t.Fatalf("a.csv: its table %v, the page kept %v", h.active.table != nil, h.pageDoc == page)
	}
	h.editor = pageEditor{Editor: widget.NewEditor()} // its component is not the page now: the kept one answers
	if v, ok := h.pageView(); !ok || v != page {
		t.Fatal("the page view was not kept")
	}
	h.documentFor("", "/tmp/page.html", true)
	h.documentFor("kb", "c.md", false)
	if len(on) != 3 || !on[0] || !on[1] || on[2] {
		t.Fatalf("the document view was switched %v, want on for the table, on for the raw page, off for a note", on)
	}
}

// The table's clipboard in the terminal
// (docs/decisions/editor.md#the-tables-clipboard-menu-and-row-numbers): the row numbers show; yy
// then p duplicates a row; a system paste (a PasteEvent) fills cells from the cursor's; the yank is
// in the editor's own register, so Raw sees it too.
func TestTheTableYanksAndPastesInTheTerminal(t *testing.T) {
	d := startManagedIncluding(t, map[string]string{"kb": fileDir(t, "a.csv", "h,v\na,1\nb,2\n", "c.md", "# Note\n")}, tableInclude)
	r := runTUI(t, NewSession(d.sock, nil), Options{})
	r.s.WaitForText(t, "· kb")
	onLoop(r, func() bool { r.h.openPath("a.csv"); return true })
	r.waitFile(t, "a.csv")
	table := func() *widget.TableDocument {
		return onLoop(r, func() *widget.TableDocument {
			tb, _ := r.h.editor.(*widget.Editor).DocumentView().(*widget.TableDocument)
			return tb
		})
	}
	r.s.WaitFor(t, "the table shown", func(string) bool { return table() != nil })
	tb := table()
	if !onLoop(r, func() bool { return tb.Core().RowNumbers() }) {
		t.Fatal("the row numbers are off")
	}
	key := func(c rune) tuicore.KeyEvent {
		return tuicore.KeyEvent{Kind: tuicore.KeyPress, Code: c, Text: string(c)}
	}
	r.keys(t, key('y'), key('y'), key('p'))
	r.s.WaitFor(t, "the row pasted", func(string) bool { return r.editorText() == "h,v\na,1\na,1\nb,2\n" })
	if text, rows := onLoop(r, func() string { s, _ := r.h.core.Register(); return s }), onLoop(r, func() bool { _, lw := r.h.core.Register(); return lw }); text != "a\t1" || !rows {
		t.Fatalf("the editor's register holds %q (rows %v), want the row yanked", text, rows)
	}
	r.keys(t, tuicore.PasteEvent{Text: "x\ty\n"})
	r.s.WaitFor(t, "the system paste", func(string) bool { return r.editorText() == "h,v\na,1\nx,y\nb,2\n" })
	// a field past the table's last is cut off, and the notifications say so
	r.keys(t, tuicore.PasteEvent{Text: "p\tq\tr\n"})
	r.s.WaitFor(t, "the paste kept to the table", func(string) bool { return r.editorText() == "h,v\na,1\np,q\nb,2\n" })
	r.s.WaitFor(t, "the cut-off said", func(string) bool { return r.noticed("the paste was cut off at the table's edge") })
	// an open edit dropped by another file shown is said too
	r.keys(t, tuicore.KeyEvent{Kind: tuicore.KeyPress, Code: tuicore.KeyEnter})
	r.s.WaitFor(t, "the edit open", func(string) bool { return onLoop(r, func() bool { return tb.Core().Editing() != nil }) })
	onLoop(r, func() bool { r.h.openPath("c.md"); return true })
	r.s.WaitFor(t, "the dropped edit said", func(string) bool { return r.noticed("the table changed; the edit was dropped") })
}
