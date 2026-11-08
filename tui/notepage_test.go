package tui

import (
	"context"
	"fmt"
	"strings"
	"testing"

	tuicore "github.com/yongjohnlee80/golib/tui"
	"github.com/yongjohnlee80/golib/tui/decl/decltest"
	"github.com/yongjohnlee80/golib/tui/widget"
)

const notePageNote = "# Pantry\n\nSome **bold** words.\n\n| Item | Qty |\n| --- | ---: |\n| apple | 3 |\n\n" +
	"```mermaid\nmindmap\n  root((Larder))\n    Shelf\n    Bin\n```\n\n```mermaid\nkanban\n  Todo\n    [Write the docs]\n  Done\n    [Ship it]\n```\n"

// renderedByTheEditor reports whether the editor draws the note shown itself, Rendered: the note
// page's Markdown cell renderer is the editor's, and no document view (an HTML file's page, a
// table file's table, kept from an earlier file) is drawn over it.
func renderedByTheEditor(r *running) bool {
	return onLoop(r, func() bool {
		e := r.h.editor.(*widget.Editor)
		_, md := e.Renderer().(*widget.MarkdownCellRenderer)
		dv := e.DocumentView()
		return md && (dv == nil || !dv.Visible()) && e.ShownView() == widget.ViewRendered
	})
}

// TestATerminalNoteOpensRenderedByTheEditor (docs/decisions/editor.md#a-terminal-notes-rendered-view):
// on a terminal backend, in either build (go test -tags gui runs it as a GUI build's --ui), a note
// opens Rendered, drawn by the editor through its Markdown cell renderer: markup hidden, its table
// boxed, its mind map drawn, a diagram of a kind golib doesn't draw shown as its fence's source.
// SPC is the app's leader there, Ctrl+T shows its text and back, and an edit is drawn at once.
func TestATerminalNoteOpensRenderedByTheEditor(t *testing.T) {
	d := startManagedIncluding(t, map[string]string{"kb": fileDir(t, "pantry.md", notePageNote)}, tableInclude)
	r := runTUI(t, NewSession(d.sock, nil), Options{})
	r.waitAttached(t, "kb")
	ctrlT := tuicore.KeyEvent{Kind: tuicore.KeyPress, Code: 't', Mods: tuicore.ModCtrl}

	onLoop(r, func() bool { r.h.openPath("pantry.md"); return true })
	r.waitFile(t, "pantry.md")
	r.s.WaitFor(t, "the note Rendered by the editor", func(string) bool { return renderedByTheEditor(r) })
	if same := onLoop(r, func() bool {
		p := r.h.notePage()
		return p != nil && p.md != nil && r.h.editor.(*widget.Editor).Renderer() == p.md
	}); !same {
		t.Fatal("the editor's renderer is not the note page's")
	}
	r.s.WaitForText(t, "Pantry")
	r.s.WaitForText(t, "┌") // the table, boxed
	r.s.WaitFor(t, "the mind map drawn, not its source", func(sc string) bool {
		return strings.Contains(sc, "Larder") && !strings.Contains(sc, "root((")
	})
	r.s.WaitForText(t, "Write the docs") // the kanban, a kind golib doesn't draw: its source
	if sc := r.s.String(); strings.Contains(sc, "**bold**") || strings.Contains(sc, "not drawn in the terminal") || !strings.Contains(sc, "kanban") {
		t.Fatalf("the Rendered view shows the note's markup, or a notice for the kanban, or not its source:\n%s", sc)
	}

	// the leader is the app's over the Rendered view as over the text: SPC o opens the picker
	r.leader(t, 'o')
	r.s.WaitForText(t, "open a document")
	r.keys(t, tuicore.KeyEvent{Kind: tuicore.KeyPress, Code: tuicore.KeyEscape})
	r.s.WaitFor(t, "the picker closed, the note still Rendered", func(sc string) bool {
		return !strings.Contains(sc, "open a document") && renderedByTheEditor(r)
	})

	r.keys(t, ctrlT)
	r.s.WaitFor(t, "the note's text after Ctrl+T", func(string) bool { return shownView(r) == widget.ViewRaw })
	r.s.WaitForText(t, "**bold**")
	r.keys(t, ctrlT)
	r.s.WaitFor(t, "Rendered again", func(sc string) bool { return renderedByTheEditor(r) && !strings.Contains(sc, "**bold**") })
	r.s.WaitForText(t, "Larder")

	// an edit while it is Rendered (an agent's, say): drawn at once, its markup hidden
	onLoop(r, func() bool {
		if _, err := r.h.core.Insert(widget.TextPos{}, "# Larder notes\n\n"); err != nil {
			t.Error(err)
		}
		return true
	})
	r.s.WaitFor(t, "the edit drawn", func(sc string) bool {
		return strings.Contains(sc, "Larder notes") && !strings.Contains(sc, "# Larder notes")
	})
}

// TestTheTerminalPagesDiagramNoticeIsTheCatalogs: the line a diagram the terminal's page doesn't
// draw shows is the catalog's, its kind filled in (the page asks it at each layout, so a language
// switch's relayout says it in the new language).
func TestTheTerminalPagesDiagramNoticeIsTheCatalogs(t *testing.T) {
	p := newCellNotePage(newHost(NewSession("unused", nil), Options{}))
	if got := p.notice("flowchart"); got != "flowchart diagram: not drawn in the terminal (Raw shows its source)" {
		t.Errorf("the English notice: %q", got)
	}
	if !strings.Contains(p.h.trf(diagramNoticeID, "%s"), "%s") {
		t.Errorf("the notice takes no kind: %q", p.h.trf(diagramNoticeID, "%s"))
	}
}

// TestCtrlTKeepsTheCursorBetweenRenderedAndRaw: the cursor moved in a note's Rendered view, the view
// following it, Ctrl+T opens Raw with the cursor where it was, and back.
func TestCtrlTKeepsTheCursorBetweenRenderedAndRaw(t *testing.T) {
	var note strings.Builder
	for i := range 60 {
		fmt.Fprintf(&note, "Paragraph %02d of the long note.\n\n", i)
	}
	r := openRenderedNote(t, note.String(), "Paragraph 00")
	r.putCursor(40, 3) // in "Paragraph 20"
	r.s.WaitForText(t, "Paragraph 20")
	r.keys(t, decltest.Ctrl('t'))
	r.s.WaitFor(t, "Raw at the cursor", func(sc string) bool {
		return shownView(r) == widget.ViewRaw && r.cursor() == [2]int{40, 3} && strings.Contains(sc, "Paragraph 20")
	})
	r.keys(t, decltest.Ctrl('t'))
	r.s.WaitFor(t, "Rendered at the cursor", func(sc string) bool {
		return shownView(r) == widget.ViewRendered && r.cursor() == [2]int{40, 3} && strings.Contains(sc, "Paragraph 20")
	})
}

// TestTheHTMLPageTakesALanguageBeforeItIsMade: a language applied before the HTML page is made
// (applyPrefs at start) tells the page nothing and makes none; the page made later is told, and
// told again at the next change.
func TestTheHTMLPageTakesALanguageBeforeItIsMade(t *testing.T) {
	p := newCellNotePage(newHost(NewSession("unused", nil), Options{}))
	p.LanguageChanged() // no page yet: it failed here, stopping the preferences' read
	if p.view != nil {
		t.Fatal("a language change made the page")
	}
	p.viewOf()
	p.LanguageChanged()
}

// TestAStoredLanguageReachesTheStart: a start with a language stored (Korean) has the store's
// preferences in, with no handler failed on the way (a page told of the language before it was made
// stopped the preferences' read); a note shown then is Rendered by the editor, a diagram it doesn't
// draw its source, and a switch to English with it shown fails no handler either.
func TestAStoredLanguageReachesTheStart(t *testing.T) {
	d := startManagedIncluding(t, map[string]string{"kb": fileDir(t, "history.md", "# History\n\n```mermaid\nkanban\n  Todo\n    [Write the docs]\n  Done\n    [Ship it]\n```\n")}, tableInclude)
	first := runTUI(t, NewSession(d.sock, nil), Options{})
	first.connected(t)
	onLoop(first, func() bool { first.h.setLanguage("ko_KR"); return true }) // Options › Language
	first.s.WaitFor(t, "the language stored", func(string) bool {
		res, err := first.h.call(context.Background(), "preference.list")
		return err == nil && asMap(res)[prefLanguage] == "ko_KR"
	})

	dir := t.TempDir()
	r := runTUI(t, NewSession(d.sock, nil), Options{CrashDir: dir})
	r.connected(t) // the store's preferences in, within the normal bound: Korean applied at start
	if lang := onLoop(r, func() string { return r.h.prefs.language }); lang != "ko_KR" {
		t.Fatalf("the start's language is %q, want the stored ko_KR", lang)
	}
	if logs := crashLogs(t, dir); len(logs) != 0 {
		t.Fatalf("a start with a stored language crashed %d times:\n%s", len(logs), logs[0])
	}
	onLoop(r, func() bool { r.h.openPath("history.md"); return true })
	// its toast is Korean now: the file is waited for, not its English "opened"
	r.s.WaitFor(t, "history.md shown", func(string) bool { return onLoop(r, func() string { return r.h.file.path }) == "history.md" })
	r.s.WaitFor(t, "the note Rendered by the editor", func(string) bool { return renderedByTheEditor(r) })
	r.s.WaitForText(t, "Write the docs") // the kanban's source, in any language
	onLoop(r, func() bool { r.h.useLanguage("en"); return true })
	r.s.WaitFor(t, "English applied, the note still Rendered", func(sc string) bool {
		return onLoop(r, func() bool { return r.h.language == "en" }) && renderedByTheEditor(r) && strings.Contains(sc, "Write the docs")
	})
	if logs := crashLogs(t, dir); len(logs) != 0 {
		t.Fatalf("a handler crashed on the way:\n%s", logs[0])
	}
}

// TestATerminalNoteDrawsAFlowchartAndAPie: kinds golib draws in cells are drawn in a note's
// Rendered view, not said to be undrawn nor shown as their source: a flowchart's boxes and the line
// between, and a pie's title, circle and legend (a note each: a pie is as tall as the view).
func TestATerminalNoteDrawsAFlowchartAndAPie(t *testing.T) {
	d := startManagedIncluding(t, map[string]string{"kb": fileDir(t,
		"flow.md", "# Flow\n\n```mermaid\nflowchart LR\n  Jar-->Shelf\n```\n",
		"pets.md", "```mermaid\npie title Pets\n  \"Dogs\" : 386\n  \"Cats\" : 85\n```\n")}, tableInclude)
	r := runTUI(t, NewSession(d.sock, nil), Options{})
	r.waitAttached(t, "kb")
	undrawn := func() {
		t.Helper()
		if sc := r.s.String(); strings.Contains(sc, "not drawn in the terminal") || strings.Contains(sc, "Jar-->Shelf") || strings.Contains(sc, "\"Dogs\"") {
			t.Fatalf("a kind golib draws was said undrawn, or its source shown:\n%s", sc)
		}
	}
	onLoop(r, func() bool { r.h.openPath("flow.md"); return true })
	r.waitFile(t, "flow.md")
	r.s.WaitFor(t, "the note Rendered by the editor", func(string) bool { return renderedByTheEditor(r) })
	r.s.WaitFor(t, "the flowchart drawn: its boxes, the line between", func(sc string) bool {
		return strings.Contains(sc, "│ Jar ├") && strings.Contains(sc, "►│ Shelf │")
	})
	undrawn()
	onLoop(r, func() bool { r.h.openPath("pets.md"); return true })
	r.waitFile(t, "pets.md")
	r.s.WaitFor(t, "the pie drawn: its title, its legend, its circle", func(sc string) bool {
		return strings.Contains(sc, "Pets") && strings.Contains(sc, "Dogs") && strings.Contains(sc, "Cats") && strings.ContainsAny(sc, "█▓")
	})
	undrawn()
}

// TestTheLeaderWorksOverTheStartsRenderedNote: at start the untitled note is Rendered by the
// editor; SPC is the app's over it, as over the text: SPC o opens the picker.
func TestTheLeaderWorksOverTheStartsRenderedNote(t *testing.T) {
	d := startManagedIncluding(t, map[string]string{"kb": fileDir(t, "a.md", "# A\n")}, tableInclude)
	r := runTUI(t, NewSession(d.sock, nil), Options{})
	r.connected(t)
	r.s.WaitFor(t, "the start's note Rendered", func(string) bool { return renderedByTheEditor(r) })
	r.leader(t, 'o')
	r.s.WaitForText(t, "open a document")
}

// openRenderedNote is r with note open on a terminal, Rendered by the editor and drawn (title on
// screen).
func openRenderedNote(t *testing.T, note, title string) *running {
	t.Helper()
	d := startManagedIncluding(t, map[string]string{"kb": fileDir(t, "n.md", note)}, tableInclude)
	r := runTUI(t, NewSession(d.sock, nil), Options{})
	r.waitAttached(t, "kb")
	onLoop(r, func() bool { r.h.openPath("n.md"); return true })
	r.waitFile(t, "n.md")
	r.s.WaitFor(t, "the note Rendered by the editor", func(string) bool { return renderedByTheEditor(r) })
	r.s.WaitForText(t, title)
	return r
}

func shownView(r *running) widget.EditorView {
	return onLoop(r, func() widget.EditorView { return r.h.editor.(*widget.Editor).ShownView() })
}

// asText takes the note shown from Rendered to its text (Ctrl+T), for a cell whose subject is the
// text's own (its cursor, gutter, marks, wrap, motions): a note on a terminal opens Rendered.
func (r *running) asText(t *testing.T) {
	t.Helper()
	r.s.WaitFor(t, "the note Rendered", func(string) bool { return shownView(r) == widget.ViewRendered })
	r.keys(t, decltest.Ctrl('t'))
	r.s.WaitFor(t, "the note's text", func(string) bool { return shownView(r) == widget.ViewRaw })
}

// TestTypingInAJustOpenedNoteEditsItInPlace: a note opens Rendered and is edited there: i, then the
// typing and Esc, edit the note at the cursor, and the view stays Rendered.
func TestTypingInAJustOpenedNoteEditsItInPlace(t *testing.T) {
	r := openRenderedNote(t, "# Pantry\n\nshelf\n", "Pantry")
	r.keys(t, key('i'))
	r.keys(t, decltest.Type("xy")...)
	r.keys(t, tuicore.KeyEvent{Kind: tuicore.KeyPress, Code: tuicore.KeyEscape})
	r.s.WaitFor(t, "the typing in the note, back in Normal", func(string) bool {
		return strings.HasPrefix(r.editorText(), "xy# Pantry") && onLoop(r, r.h.core.Mode) == widget.ModeNormal
	})
	if v := shownView(r); v != widget.ViewRendered {
		t.Fatalf("after typing the note shows view %v, want Rendered", v)
	}
}

// TestTheCommandLineOpensFromTheRenderedView: ':' in a note's Rendered view opens the command line,
// as in its text.
func TestTheCommandLineOpensFromTheRenderedView(t *testing.T) {
	r := openRenderedNote(t, "# Pantry\n", "Pantry")
	r.keys(t, key(':'))
	r.s.WaitForText(t, "VIM Commands")
}

// TestAFindInARenderedNoteCountsItsSource: a find in a note's Rendered view runs over the note's
// source, as in its text (a link's target counts too): "1 of 3", the cursor on the first match, and
// the view stays Rendered; Ctrl+T to Raw and back keeps the count (docs/decisions/editor.md).
func TestAFindInARenderedNoteCountsItsSource(t *testing.T) {
	r := openRenderedNote(t, "# Birds\n\na kestrel, see [the hawk](kestrel.md), another kestrel\n", "Birds")
	r.h.p.Post(func() { r.h.find.pending = findPage; r.h.startFind("kestrel") })
	total := func() int { return onLoop(r, func() int { return r.h.find.total }) }
	r.s.WaitFor(t, "found in the source, the cursor on the first", func(string) bool {
		return total() == 3 && r.cursor() == [2]int{2, 2}
	})
	if v := shownView(r); v != widget.ViewRendered {
		t.Fatalf("the find left the Rendered view: view %v", v)
	}
	r.keys(t, decltest.Ctrl('t'))
	r.s.WaitFor(t, "counted in the text", func(string) bool { return shownView(r) == widget.ViewRaw && total() == 3 })
	r.keys(t, decltest.Ctrl('t'))
	r.s.WaitFor(t, "counted Rendered again", func(string) bool { return shownView(r) == widget.ViewRendered && total() == 3 })
	r.h.p.Post(r.h.clearFind)
	r.s.WaitFor(t, "cleared", func(string) bool { return onLoop(r, func() string { return r.h.find.query }) == "" })
}

// TestSPCjInTheRenderedViewFollowsTheLinkUnderTheCursor: in a note's Rendered view SPC j follows the
// link under the cursor, as in its text, not the first one shown: off a link it says so, and on the
// second link it opens that one (docs/decisions/editor.md).
func TestSPCjInTheRenderedViewFollowsTheLinkUnderTheCursor(t *testing.T) {
	line := "second [[c]]"
	d := startDaemon(t, map[string][]string{"kb": {"a.md", "# Links\n\nfirst [[b]]\n\n" + line + "\n", "b.md", "# B\n", "c.md", "# C\n"}})
	r := attached(t, d)
	r.openByPicker(t, "a.md")
	r.waitFile(t, "a.md")
	r.s.WaitFor(t, "the note Rendered by the editor", func(string) bool { return renderedByTheEditor(r) })
	r.s.WaitForText(t, "second")
	r.leader(t, 'j') // the cursor on the heading: no link
	r.waitKeptNotice(t, "no link under the cursor: SPC l shows what a.md links with")
	if f := r.file(); f.path != "a.md" {
		t.Fatalf("SPC j off a link opened %s", f.path)
	}
	r.putCursor(4, strings.Index(line, "c]]"))
	r.leader(t, 'j')
	r.waitFile(t, "c.md")
}

// TestAFindOnlyTheSourceHasIsFoundInTheRenderedView: in a note's Rendered view a term only the
// source has, a link's target, is found in place: the cursor on its match, the view still Rendered,
// with no notice of a move to the text. A term in neither is not found (docs/decisions/editor.md).
func TestAFindOnlyTheSourceHasIsFoundInTheRenderedView(t *testing.T) {
	line := "a kestrel, see [the hawk](https://x.example/target), another kestrel"
	r := openRenderedNote(t, "# Birds\n\n"+line+"\n", "Birds")
	find := func(term string) {
		t.Helper()
		r.h.p.Post(func() { r.h.find.pending = findPage; r.h.startFind(term) })
	}
	find("zebra")
	r.s.WaitForText(t, `find: no "zebra" in the page`)
	if v := shownView(r); v != widget.ViewRendered {
		t.Fatalf("a term in neither left the Rendered view: view %v", v)
	}
	find("x.example/target")
	r.s.WaitFor(t, "the find on its match, Rendered", func(string) bool {
		return shownView(r) == widget.ViewRendered && r.cursor() == [2]int{2, strings.Index(line, "x.example/target")} &&
			onLoop(r, func() int { return r.h.find.total }) == 1
	})
	moved := onLoop(r, func() bool {
		for _, n := range r.h.noticeLog.items {
			if strings.Contains(n.text, "is not on the page") {
				return true
			}
		}
		return false
	})
	if moved {
		t.Fatal("a find in the Rendered view said it went to the text")
	}
}

// chartNote is a note of three vega-lite fences: bars, a spec outside golib's subset, and a line.
const chartNote = "# Charts\n\n```vega-lite\n" +
	`{"mark":"bar","data":{"values":[{"c":"north","v":12.5},{"c":"south","v":7.9}]},` +
	`"encoding":{"y":{"field":"c","type":"nominal"},"x":{"field":"v","type":"quantitative"}}}` +
	"\n```\n\n```vega-lite\n" +
	`{"mark":"geoshape","data":{"values":[{"region":"west"}]}}` +
	"\n```\n\n```vega-lite\n" +
	`{"mark":"line","data":{"values":[{"t":1,"v":2},{"t":2,"v":5},{"t":3,"v":3},{"t":4,"v":6}]},` +
	`"encoding":{"x":{"field":"t","type":"quantitative"},"y":{"field":"v","type":"quantitative"}}}` +
	"\n```\n"

// braille reports whether s holds a Braille cell with a dot: a plot drawn in cells.
func braille(s string) bool {
	return strings.ContainsFunc(s, func(r rune) bool { return r > 0x2800 && r <= 0x28ff })
}

// TestATerminalNoteDrawsItsCharts (docs/decisions/editor.md#a-terminal-notes-rendered-view): a
// note's vega-lite fences in its Rendered view are charts in cells, bars in eighths with their
// categories and values and a line in Braille, never their spec's JSON; a spec outside golib's
// subset is its fence's source.
func TestATerminalNoteDrawsItsCharts(t *testing.T) {
	d := startManagedIncluding(t, map[string]string{"kb": fileDir(t, "charts.md", chartNote)}, tableInclude)
	r := runTUISized(t, NewSession(d.sock, nil), Options{}, 120, 60)
	r.waitAttached(t, "kb")
	onLoop(r, func() bool { r.h.openPath("charts.md"); return true })
	r.waitFile(t, "charts.md")
	r.s.WaitFor(t, "the note Rendered by the editor", func(string) bool { return renderedByTheEditor(r) })
	r.s.WaitFor(t, "the bars and the line drawn", func(sc string) bool {
		return strings.Contains(sc, "north") && strings.Contains(sc, "12.5") && strings.Contains(sc, "█") && braille(sc)
	})
	sc := r.s.String()
	if strings.Contains(sc, `"mark":"bar"`) || strings.Contains(sc, `"mark":"line"`) {
		t.Fatalf("the Rendered view shows a drawn chart's JSON:\n%s", sc)
	}
	if !strings.Contains(sc, `"geoshape"`) || strings.Contains(sc, "not drawn in the terminal") {
		t.Fatalf("the spec outside golib's subset is not its source:\n%s", sc)
	}
}

// TestThePagesChartNoticeIsTheCatalogs: a vega-lite chart the terminal doesn't draw says so in the
// catalog's words, its own sentence (a chart, not "a vega-lite diagram"); a Mermaid kind keeps its own.
func TestThePagesChartNoticeIsTheCatalogs(t *testing.T) {
	h := newHost(NewSession("unused", nil), Options{})
	if got := h.pageNotice("vega-lite"); got != "vega-lite chart: not drawn in the terminal (Raw shows its source)" {
		t.Errorf("the chart's notice: %q", got)
	}
	if got := h.pageNotice("gitGraph"); got != "gitGraph diagram: not drawn in the terminal (Raw shows its source)" {
		t.Errorf("a diagram's notice: %q", got)
	}
}

// TestAnotherFileTakesTheNotesRendererOff: the note's Markdown renderer is the note's alone. Open a
// text file after a note and the editor no longer draws through it, so the text file is never laid
// out as Markdown.
func TestAnotherFileTakesTheNotesRendererOff(t *testing.T) {
	d := startManagedIncluding(t, map[string]string{"kb": fileDir(t, "a.md", "# A\n\n**bold** words\n", "b.txt", "**plain** text\n")},
		[]string{"**/*.md", "**/*.txt"})
	r := runTUI(t, NewSession(d.sock, nil), Options{})
	r.waitAttached(t, "kb")
	onLoop(r, func() bool { r.h.openPath("a.md"); return true })
	r.waitFile(t, "a.md")
	r.s.WaitFor(t, "a.md Rendered by the editor", func(string) bool { return renderedByTheEditor(r) })
	onLoop(r, func() bool { r.h.openPath("b.txt"); return true })
	r.waitFile(t, "b.txt")
	r.s.WaitFor(t, "b.txt drawn with no Markdown renderer", func(sc string) bool {
		md := onLoop(r, func() bool {
			_, md := r.h.editor.(*widget.Editor).Renderer().(*widget.MarkdownCellRenderer)
			return md
		})
		return !md && strings.Contains(sc, "**plain** text")
	})
}
