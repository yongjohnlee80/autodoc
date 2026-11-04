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
	"```mermaid\nmindmap\n  root((Larder))\n    Shelf\n    Bin\n```\n\n```mermaid\nflowchart LR\n  A-->B\n```\n"

// TestATerminalNoteIsItsPage (docs/decisions/editor.md#a-terminal-notes-rendered-view): on a
// terminal backend, in either build (go test -tags gui runs it as a GUI build's --ui), a note
// opens Rendered as its page in cells (its table boxed, its mind map drawn, another diagram said
// not to be drawn here), Ctrl+T shows its text and back its page, and an edit while the page is
// shown draws it again once typing rests.
func TestATerminalNoteIsItsPage(t *testing.T) {
	d := startManagedIncluding(t, map[string]string{"kb": fileDir(t, "pantry.md", notePageNote)}, tableInclude)
	r := runTUI(t, NewSession(d.sock, nil), Options{})
	r.waitAttached(t, "kb")
	view := func() (widget.EditorView, bool) {
		type shown struct {
			v    widget.EditorView
			page bool
		}
		s := onLoop(r, func() shown {
			e := r.h.editor.(*widget.Editor)
			_, page := e.DocumentView().(*notePageView)
			return shown{e.ShownView(), page}
		})
		return s.v, s.page
	}
	ctrlT := tuicore.KeyEvent{Kind: tuicore.KeyPress, Code: 't', Mods: tuicore.ModCtrl}

	onLoop(r, func() bool { r.h.openPath("pantry.md"); return true })
	r.waitFile(t, "pantry.md")
	r.s.WaitFor(t, "the note shown as its page", func(string) bool { v, page := view(); return page && v == widget.ViewRendered })
	r.s.WaitForText(t, "Pantry")
	r.s.WaitForText(t, "┌") // the table, boxed
	r.s.WaitForText(t, "Larder")
	r.s.WaitForText(t, "flowchart diagram: not drawn in the terminal (Raw shows its source)")
	if strings.Contains(r.s.String(), "**bold**") || strings.Contains(r.s.String(), "A-->B") {
		t.Fatalf("the page shows the note's source:\n%s", r.s.String())
	}

	// the leader is the app's over the page as over the text: SPC o opens the picker
	r.leader(t, 'o')
	r.s.WaitForText(t, "open a document")
	r.keys(t, tuicore.KeyEvent{Kind: tuicore.KeyPress, Code: tuicore.KeyEscape})
	r.s.WaitFor(t, "the picker closed, the page still shown", func(sc string) bool {
		v, page := view()
		return !strings.Contains(sc, "open a document") && page && v == widget.ViewRendered
	})

	r.keys(t, ctrlT)
	r.s.WaitFor(t, "the note's text after Ctrl+T", func(string) bool { v, _ := view(); return v == widget.ViewRaw })
	r.s.WaitForText(t, "**bold**")
	r.keys(t, ctrlT)
	r.s.WaitFor(t, "its page again", func(string) bool { v, _ := view(); return v == widget.ViewRendered })
	r.s.WaitForText(t, "Larder")

	// an edit while the page is shown (an agent's, say): drawn once typing rests
	onLoop(r, func() bool {
		if _, err := r.h.core.Insert(widget.TextPos{}, "# Larder notes\n\n"); err != nil {
			t.Error(err)
		}
		return true
	})
	r.s.WaitForText(t, "Larder notes")
}

// TestTheTerminalNotePagesNoticeIsTheCatalogs: the line a diagram the terminal doesn't draw shows is
// the catalog's, its kind filled in (the page asks it at each layout, so a language switch's
// relayout says it in the new language).
func TestTheTerminalNotePagesNoticeIsTheCatalogs(t *testing.T) {
	p := newCellNotePage(newHost(NewSession("unused", nil), Options{}))
	if got := p.notice("flowchart"); got != "flowchart diagram: not drawn in the terminal (Raw shows its source)" {
		t.Errorf("the English notice: %q", got)
	}
	if !strings.Contains(p.h.trf(diagramNoticeID, "%s"), "%s") {
		t.Errorf("the notice takes no kind: %q", p.h.trf(diagramNoticeID, "%s"))
	}
}

// TestATerminalNotesPageTakesRawToWhereTheReaderScrolled: the page shows the cursor's block; scrolled
// on in it, Ctrl+T opens Raw with the cursor at the first byte of the block then at the page's top.
// Unscrolled, the cursor stays where it was.
func TestATerminalNotesPageTakesRawToWhereTheReaderScrolled(t *testing.T) {
	var note strings.Builder
	for i := range 60 {
		fmt.Fprintf(&note, "Paragraph %02d of the long note.\n\n", i)
	}
	d := startManagedIncluding(t, map[string]string{"kb": fileDir(t, "long.md", note.String())}, tableInclude)
	r := runTUI(t, NewSession(d.sock, nil), Options{})
	r.waitAttached(t, "kb")
	ctrlT := tuicore.KeyEvent{Kind: tuicore.KeyPress, Code: 't', Mods: tuicore.ModCtrl}
	shown := func() widget.EditorView {
		return onLoop(r, func() widget.EditorView { return r.h.editor.(*widget.Editor).ShownView() })
	}
	onLoop(r, func() bool { r.h.openPath("long.md"); return true })
	r.waitFile(t, "long.md")
	r.s.WaitFor(t, "the page", func(string) bool { return shown() == widget.ViewRendered })
	r.s.WaitForText(t, "Paragraph 00")

	// unscrolled: Raw keeps the cursor at the note's start
	r.keys(t, ctrlT)
	r.s.WaitFor(t, "Raw", func(string) bool { return shown() == widget.ViewRaw })
	if at := onLoop(r, r.h.cursorBytes); at != 0 {
		t.Fatalf("an unscrolled page moved the cursor to byte %d", at)
	}
	r.keys(t, ctrlT)
	r.s.WaitFor(t, "the page again", func(string) bool { return shown() == widget.ViewRendered })
	for range 3 {
		r.keys(t, tuicore.KeyEvent{Kind: tuicore.KeyPress, Code: tuicore.KeyPageDown})
	}
	page := onLoop(r, func() *cellNotePage {
		for _, f := range r.h.reg.features {
			if p, ok := f.(*cellNotePage); ok {
				return p
			}
		}
		return nil
	})
	r.s.WaitFor(t, "the page scrolled on", func(string) bool { return onLoop(r, func() int { return page.top }) > 0 })
	top := onLoop(r, func() int { return page.top })
	r.keys(t, ctrlT)
	r.s.WaitFor(t, "Raw at the reader's place", func(string) bool {
		return shown() == widget.ViewRaw && onLoop(r, r.h.cursorBytes) == top
	})
	if !strings.HasPrefix(onLoop(r, r.h.core.Value)[top:], "Paragraph ") {
		t.Errorf("the cursor is at byte %d, not a paragraph's start", top)
	}
}

// TestATerminalNotePageTakesALanguageBeforeItsFirstNote: a language applied before any note is shown
// (applyPrefs at start) tells the page nothing and makes none; the page made later is told, and
// told again at the next change.
func TestATerminalNotePageTakesALanguageBeforeItsFirstNote(t *testing.T) {
	p := newCellNotePage(newHost(NewSession("unused", nil), Options{}))
	p.LanguageChanged() // no page yet: it failed here, stopping the preferences' read
	if p.view != nil {
		t.Fatal("a language change made the page")
	}
	p.viewOf()
	p.LanguageChanged()
}

const koNotice = "flowchart 다이어그램: 터미널에서는 그리지 않습니다 (Raw에서 소스를 볼 수 있습니다)"

// TestAStoredLanguageReachesTheTerminalNotePage: a start with a language stored (Korean) has the store's
// preferences in, with no handler failed on the way (a page told of the language before it was made
// stopped the preferences' read); the first note shown says a diagram it doesn't draw in Korean, and
// a switch to English with the page shown says it in English.
func TestAStoredLanguageReachesTheTerminalNotePage(t *testing.T) {
	d := startManagedIncluding(t, map[string]string{"kb": fileDir(t, "flow.md", "# Flow\n\n```mermaid\nflowchart LR\n  A-->B\n```\n")}, tableInclude)
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
	onLoop(r, func() bool { r.h.openPath("flow.md"); return true })
	// its toast is Korean now: the file is waited for, not its English "opened"
	r.s.WaitFor(t, "flow.md shown", func(string) bool { return onLoop(r, func() string { return r.h.file.path }) == "flow.md" })
	r.s.WaitForText(t, koNotice) // the page made after the language: its words already Korean
	onLoop(r, func() bool { r.h.useLanguage("en"); return true })
	r.s.WaitForText(t, "flowchart diagram: not drawn in the terminal (Raw shows its source)")
	if logs := crashLogs(t, dir); len(logs) != 0 {
		t.Fatalf("a handler crashed on the way:\n%s", logs[0])
	}
}

// TestTheLeaderWorksOverTheStartsNotePage: at start the untitled note is shown as its page; SPC is
// the app's over it, as over the text: SPC o opens the picker.
func TestTheLeaderWorksOverTheStartsNotePage(t *testing.T) {
	d := startManagedIncluding(t, map[string]string{"kb": fileDir(t, "a.md", "# A\n")}, tableInclude)
	r := runTUI(t, NewSession(d.sock, nil), Options{})
	r.connected(t)
	r.s.WaitFor(t, "the start's page", func(string) bool {
		return onLoop(r, func() bool { _, ok := r.h.editor.(*widget.Editor).DocumentView().(*notePageView); return ok })
	})
	r.leader(t, 'o')
	r.s.WaitForText(t, "open a document")
}

// openNotePage is r with note open on a terminal, its page shown and drawn (title on screen).
func openNotePage(t *testing.T, note, title string) *running {
	t.Helper()
	d := startManagedIncluding(t, map[string]string{"kb": fileDir(t, "n.md", note)}, tableInclude)
	r := runTUI(t, NewSession(d.sock, nil), Options{})
	r.waitAttached(t, "kb")
	onLoop(r, func() bool { r.h.openPath("n.md"); return true })
	r.waitFile(t, "n.md")
	r.s.WaitFor(t, "the note's page", func(string) bool { return shownView(r) == widget.ViewRendered })
	r.s.WaitForText(t, title)
	return r
}

func shownView(r *running) widget.EditorView {
	return onLoop(r, func() widget.EditorView { return r.h.editor.(*widget.Editor).ShownView() })
}

// notePage is the run's terminal note page.
func notePage(r *running) *cellNotePage {
	return onLoop(r, func() *cellNotePage {
		for _, f := range r.h.reg.features {
			if p, ok := f.(*cellNotePage); ok {
				return p
			}
		}
		return nil
	})
}

// TestTypingInAJustOpenedNoteEditsIt: a note opens as its page, and the
// first editing key opens the text and acts: i, then the typing and Esc, edit the note where it was
// read, and the text is shown.
func TestTypingInAJustOpenedNoteEditsIt(t *testing.T) {
	r := openNotePage(t, "# Pantry\n\nshelf\n", "Pantry")
	r.keys(t, key('i'))
	r.keys(t, decltest.Type("xy")...)
	r.keys(t, tuicore.KeyEvent{Kind: tuicore.KeyPress, Code: tuicore.KeyEscape})
	r.s.WaitFor(t, "the typing in the note", func(string) bool { return strings.HasPrefix(r.editorText(), "xy# Pantry") })
	if v := shownView(r); v != widget.ViewRaw {
		t.Fatalf("after typing the note shows view %v, want its text", v)
	}
}

// TestThePagesReadingKeysKeepItRendered: the keys that read the page (the arrows, j/k, PgUp/PgDn, g/G,
// y and Ctrl+C) leave it Rendered and the note as it was, and the page's own scroll is theirs: PgDn
// moves its top on, PgUp back, G to the end, g to the start.
func TestThePagesReadingKeysKeepItRendered(t *testing.T) {
	var note strings.Builder
	for i := range 40 {
		fmt.Fprintf(&note, "Line %02d of the note.\n\n", i)
	}
	r := openNotePage(t, "# Long\n\n"+note.String(), "Long")
	before := r.editorText()
	page := notePage(r)
	top := func() int { return onLoop(r, func() int { return page.top }) }
	// the keys are taken on the loop in their own time (decltest's Keys): a scroll is waited for
	var down, bottom int
	for _, k := range []struct {
		ev     tuicore.Event
		scroll func() bool // the page's scroll the key makes, nil: none the page's top must show
	}{
		{key('j'), nil}, {key('k'), nil}, {tuicore.KeyEvent{Kind: tuicore.KeyPress, Code: tuicore.KeyDown}, nil},
		{tuicore.KeyEvent{Kind: tuicore.KeyPress, Code: tuicore.KeyUp}, nil},
		{tuicore.KeyEvent{Kind: tuicore.KeyPress, Code: tuicore.KeyPageDown}, func() bool { down = top(); return down > 0 }},
		{tuicore.KeyEvent{Kind: tuicore.KeyPress, Code: tuicore.KeyPageUp}, func() bool { return top() < down }},
		{key('G'), func() bool { bottom = top(); return bottom > down }},
		{key('g'), func() bool { return top() == 0 }},
		{key('y'), nil}, {tuicore.KeyEvent{Kind: tuicore.KeyPress, Code: 'c', Mods: tuicore.ModCtrl}, nil},
	} {
		r.keys(t, k.ev)
		if k.scroll != nil {
			r.s.WaitFor(t, fmt.Sprintf("%+v scrolls the page", k.ev), func(string) bool { return k.scroll() })
		}
		if v := shownView(r); v != widget.ViewRendered {
			t.Fatalf("%+v left the page (view %v)", k.ev, v)
		}
	}
	if got := r.editorText(); got != before {
		t.Fatalf("reading the page changed the note:\n%q\nwant\n%q", got, before)
	}
}

// TestAnEditingKeyFromThePageActsOnce: x from the page deletes the character the cursor is on, once,
// in the text; o from the page opens a line below, once, in Insert mode.
func TestAnEditingKeyFromThePageActsOnce(t *testing.T) {
	r := openNotePage(t, "# Pantry\n\nshelf\n", "Pantry")
	ctrlT := tuicore.KeyEvent{Kind: tuicore.KeyPress, Code: 't', Mods: tuicore.ModCtrl}
	r.keys(t, key('x'))
	r.s.WaitFor(t, "x acted once", func(string) bool { return r.editorText() == " Pantry\n\nshelf\n" })
	if v := shownView(r); v != widget.ViewRaw {
		t.Fatalf("x left the view %v, want the text", v)
	}
	r.keys(t, ctrlT)
	r.s.WaitFor(t, "the page again", func(string) bool { return shownView(r) == widget.ViewRendered })
	r.keys(t, key('o'))
	r.keys(t, decltest.Type("z")...)
	r.keys(t, tuicore.KeyEvent{Kind: tuicore.KeyPress, Code: tuicore.KeyEscape})
	// o keeps the line's indent, as vim's does: the space x left
	r.s.WaitFor(t, "o acted once", func(string) bool { return r.editorText() == " Pantry\n z\n\nshelf\n" })
}

// TestTheCommandLineOpensFromThePage: ':' from a note's page opens the command line, as from its text.
func TestTheCommandLineOpensFromThePage(t *testing.T) {
	r := openNotePage(t, "# Pantry\n", "Pantry")
	r.keys(t, key(':'))
	r.s.WaitForText(t, "VIM Commands")
}

// TestAnEditingKeyEditsWhereThePageWasScrolledTo: scrolled on, the page's first editing key edits at
// the block the reader scrolled to, not where the cursor was: o opens a line under that block's line.
func TestAnEditingKeyEditsWhereThePageWasScrolledTo(t *testing.T) {
	var note strings.Builder
	for i := range 60 {
		fmt.Fprintf(&note, "Paragraph %02d of the long note.\n\n", i)
	}
	r := openNotePage(t, note.String(), "Paragraph 00")
	for range 3 {
		r.keys(t, tuicore.KeyEvent{Kind: tuicore.KeyPress, Code: tuicore.KeyPageDown})
	}
	page := notePage(r)
	// the keys are taken on the loop in their own time (decltest's Keys): their effect is waited for
	r.s.WaitFor(t, "the page scrolled on", func(string) bool { return onLoop(r, func() int { return page.top }) > 0 })
	top := onLoop(r, func() int { return page.top })
	text := r.editorText()
	if !strings.HasPrefix(text[top:], "Paragraph ") {
		t.Fatalf("the page scrolled by three pages reports its top at byte %d", top)
	}
	line := text[top : top+strings.IndexByte(text[top:], '\n')] // the block at the page's top
	r.keys(t, key('o'))
	r.keys(t, decltest.Type("here")...)
	r.keys(t, tuicore.KeyEvent{Kind: tuicore.KeyPress, Code: tuicore.KeyEscape})
	r.s.WaitFor(t, "a line opened under the scrolled-to block", func(string) bool {
		return strings.Contains(r.editorText(), line+"\nhere\n")
	})
}
