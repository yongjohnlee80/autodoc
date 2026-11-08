package tui

import (
	"fmt"
	"strings"
	"testing"

	tuicore "github.com/yongjohnlee80/golib/tui"
	"github.com/yongjohnlee80/golib/tui/decl/decltest"
	"github.com/yongjohnlee80/golib/tui/widget"
)

// AN HTML FILE'S PAGE, opened raw: on a terminal its Rendered view is a page drawn in cells over the
// editor, as a note's was before the editor drew notes itself (docs/decisions/editor.md). These
// cells hold the page's reading, editing, find and link paths.

// htmlInclude is a workspace's patterns with HTML files in it.
var htmlInclude = []string{"**/*.md", "**/*.html"}

// openHTMLPage runs the TUI on a workspace whose p.html holds page, more files beside it, opens p.html
// raw and waits for its page to show title: Rendered, the page made from the file's text.
func openHTMLPage(t *testing.T, page, title string, more ...string) *running {
	t.Helper()
	d := startManagedIncluding(t, map[string]string{"kb": fileDir(t, append([]string{"p.html", page}, more...)...)}, htmlInclude)
	r := runTUI(t, NewSession(d.sock, nil), Options{})
	r.waitAttached(t, "kb")
	r.h.p.Post(func() { r.h.openPath("p.html") })
	r.s.WaitForText(t, "open an HTML file")
	r.h.p.Post(func() { r.h.openHTMLAs("raw") })
	r.waitFile(t, "p.html")
	r.s.WaitFor(t, "the file's page", func(string) bool { return shownView(r) == widget.ViewRendered })
	r.s.WaitForText(t, title)
	p := htmlPage(r)
	r.s.WaitFor(t, "the file's text on its page", func(string) bool {
		return onLoop(r, func() bool { return p.view != nil && strings.Contains(string(p.view.Source()), title) })
	})
	return r
}

// htmlPage is the page an HTML file is drawn on.
func htmlPage(r *running) *cellNotePage { return onLoop(r, r.h.notePage) }

// paragraphs is n lines of <p>, each "word NN of the page.".
func paragraphs(word string, n int) string {
	var b strings.Builder
	for i := range n {
		fmt.Fprintf(&b, "<p>%s %02d of the page.</p>\n", word, i)
	}
	return b.String()
}

// TestARawHTMLPageTakesRawToWhereTheReaderScrolled: unscrolled, Ctrl+T leaves the cursor at the
// file's start; scrolled on, Raw opens at the block at the page's top.
func TestARawHTMLPageTakesRawToWhereTheReaderScrolled(t *testing.T) {
	r := openHTMLPage(t, "<h1>Long</h1>\n"+paragraphs("Paragraph", 60), "Long")
	r.keys(t, decltest.Ctrl('t'))
	r.s.WaitFor(t, "Raw", func(string) bool { return shownView(r) == widget.ViewRaw })
	if at := onLoop(r, r.h.cursorBytes); at != 0 {
		t.Fatalf("an unscrolled page moved the cursor to byte %d", at)
	}
	r.keys(t, decltest.Ctrl('t'))
	r.s.WaitFor(t, "the page again", func(string) bool { return shownView(r) == widget.ViewRendered })
	for range 3 {
		r.keys(t, tuicore.KeyEvent{Kind: tuicore.KeyPress, Code: tuicore.KeyPageDown})
	}
	page := htmlPage(r)
	top := func() int { return onLoop(r, func() int { return page.top }) }
	r.s.WaitFor(t, "the page scrolled on", func(string) bool { return top() > 0 })
	at := top()
	r.keys(t, decltest.Ctrl('t'))
	r.s.WaitFor(t, "Raw at the reader's place", func(string) bool {
		return shownView(r) == widget.ViewRaw && onLoop(r, r.h.cursorBytes) == at
	})
	if rest := onLoop(r, r.h.core.Value)[at:]; !strings.Contains(rest[:min(len(rest), 20)], "Paragraph ") {
		t.Errorf("the cursor is at byte %d, not a paragraph's block: %q", at, rest[:min(len(rest), 20)])
	}
}

// TestARawHTMLPagesReadingKeysKeepItRendered: the keys that read the page (the arrows, j/k,
// PgUp/PgDn, g/G, ] [ and } {, h/l, y and Ctrl+C) leave it Rendered and the file as it was, and the
// page's own scroll is theirs: PgDn moves its top on, PgUp back, G to the end, g to the start, ] to
// the next block and [ back.
func TestARawHTMLPagesReadingKeysKeepItRendered(t *testing.T) {
	r := openHTMLPage(t, "<h1>Long</h1>\n"+paragraphs("Line", 40), "Long")
	before := r.editorText()
	page := htmlPage(r)
	top := func() int { return onLoop(r, func() int { return page.top }) }
	var down, bottom int
	for _, k := range []struct {
		ev     tuicore.Event
		scroll func() bool // the scroll the key makes, nil: none to wait for
	}{
		{key('j'), nil}, {key('k'), nil}, {tuicore.KeyEvent{Kind: tuicore.KeyPress, Code: tuicore.KeyDown}, nil},
		{tuicore.KeyEvent{Kind: tuicore.KeyPress, Code: tuicore.KeyUp}, nil},
		{tuicore.KeyEvent{Kind: tuicore.KeyPress, Code: tuicore.KeyPageDown}, func() bool { down = top(); return down > 0 }},
		{tuicore.KeyEvent{Kind: tuicore.KeyPress, Code: tuicore.KeyPageUp}, func() bool { return top() < down }},
		{key('G'), func() bool { bottom = top(); return bottom > down }},
		{key('g'), func() bool { return top() == 0 }},
		{key(']'), func() bool { return top() > 0 }},
		{key('['), func() bool { return top() == 0 }},
		{key('}'), nil}, {key('{'), nil},
		{key('h'), nil}, {key('l'), nil}, {tuicore.KeyEvent{Kind: tuicore.KeyPress, Code: tuicore.KeyLeft}, nil},
		{tuicore.KeyEvent{Kind: tuicore.KeyPress, Code: tuicore.KeyRight}, nil},
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
		t.Fatalf("reading the page changed the file:\n%q\nwant\n%q", got, before)
	}
}

// TestTheTextKeymapTypesEveryPrintingKeyOnARawHTMLPage: under Text (modeless) every printing key
// types, so the keys Vim reads the page by open the text at the reader's place and type there; the
// page travels by Alt and an arrow instead.
func TestTheTextKeymapTypesEveryPrintingKeyOnARawHTMLPage(t *testing.T) {
	r := openHTMLPage(t, "<h1>Long</h1>\n"+paragraphs("Line", 20), "Long")
	r.leader(t, 'k') // Vim to Text
	r.s.WaitForText(t, "editor mode: Text (modeless)")
	if v := shownView(r); v != widget.ViewRendered {
		t.Fatalf("after the keymap switch the file shows view %v, want its page", v)
	}
	page := htmlPage(r)
	top := func() int { return onLoop(r, func() int { return page.top }) }
	r.keys(t, tuicore.KeyEvent{Kind: tuicore.KeyPress, Code: tuicore.KeyDown, Mods: tuicore.ModAlt})
	r.s.WaitFor(t, "Alt+Down travels to the next block", func(string) bool { return top() > 0 })
	r.keys(t, tuicore.KeyEvent{Kind: tuicore.KeyPress, Code: tuicore.KeyUp, Mods: tuicore.ModAlt})
	r.s.WaitFor(t, "Alt+Up travels back", func(string) bool { return top() == 0 })
	const typed = " hjkl[]{}gGy"
	r.keys(t, decltest.Type(typed)...)
	r.s.WaitFor(t, "the keys typed into the file", func(string) bool { return strings.HasPrefix(r.editorText(), typed+"<h1>Long") })
	if v := shownView(r); v != widget.ViewRaw {
		t.Fatalf("after typing the file shows view %v, want its text", v)
	}
}

// TestAnEditingKeyFromARawHTMLPageActsOnce: x from the page deletes the character the cursor is on,
// once, in the text; o from the page opens a line below, once, in Insert mode.
func TestAnEditingKeyFromARawHTMLPageActsOnce(t *testing.T) {
	const page = "<h1>Pantry</h1>\n<p>shelf</p>\n"
	r := openHTMLPage(t, page, "Pantry")
	r.keys(t, key('x'))
	r.s.WaitFor(t, "x acted once", func(string) bool { return r.editorText() == page[1:] })
	if v := shownView(r); v != widget.ViewRaw {
		t.Fatalf("x left the view %v, want the text", v)
	}
	r.keys(t, decltest.Ctrl('t'))
	r.s.WaitFor(t, "the page again", func(string) bool { return shownView(r) == widget.ViewRendered })
	r.keys(t, key('o'))
	r.keys(t, decltest.Type("z")...)
	r.keys(t, tuicore.KeyEvent{Kind: tuicore.KeyPress, Code: tuicore.KeyEscape})
	r.s.WaitFor(t, "o acted once", func(string) bool { return r.editorText() == "h1>Pantry</h1>\nz\n<p>shelf</p>\n" })
}

// TestTheCommandLineOpensFromARawHTMLPage: ':' from the page opens the command line, as from the text.
func TestTheCommandLineOpensFromARawHTMLPage(t *testing.T) {
	r := openHTMLPage(t, "<h1>Pantry</h1>\n", "Pantry")
	r.keys(t, key(':'))
	r.s.WaitForText(t, "VIM Commands")
}

// TestAnEditingKeyEditsWhereARawHTMLPageWasScrolledTo: scrolled on, the page's first editing key
// edits at the block the reader scrolled to: o opens a line under that block's line.
func TestAnEditingKeyEditsWhereARawHTMLPageWasScrolledTo(t *testing.T) {
	r := openHTMLPage(t, "<h1>Long</h1>\n"+paragraphs("Paragraph", 60), "Long")
	for range 3 {
		r.keys(t, tuicore.KeyEvent{Kind: tuicore.KeyPress, Code: tuicore.KeyPageDown})
	}
	page := htmlPage(r)
	r.s.WaitFor(t, "the page scrolled on", func(string) bool { return onLoop(r, func() int { return page.top }) > 0 })
	top := onLoop(r, func() int { return page.top })
	text := r.editorText()
	start := strings.LastIndexByte(text[:top], '\n') + 1 // the line the page's top block is on
	line := text[start : top+strings.IndexByte(text[top:], '\n')]
	r.keys(t, key('o'))
	r.keys(t, decltest.Type("here")...)
	r.keys(t, tuicore.KeyEvent{Kind: tuicore.KeyPress, Code: tuicore.KeyEscape})
	r.s.WaitFor(t, "a line opened under the scrolled-to block", func(string) bool {
		return strings.Contains(r.editorText(), line+"\nhere\n")
	})
}

// birdsPage holds "kestrel" twice in its text and once more in a link's target, which only the
// source has.
const birdsPage = "<h1>Birds</h1>\n<p>a kestrel, see <a href=\"kestrel.html\">the hawk</a>, another kestrel</p>\n"

// TestAFindOnARawHTMLPageCountsWhatItShows: the page's find counts and marks what the page shows,
// and the text its source: Ctrl+T takes "of 2" on the page to "of 3" in the text, and back; n and N
// step through the page's marks.
func TestAFindOnARawHTMLPageCountsWhatItShows(t *testing.T) {
	r := openHTMLPage(t, birdsPage, "Birds")
	r.h.p.Post(func() { r.h.find.pending = findPage; r.h.startFind("kestrel") })
	at := func() [2]int { return onLoop(r, func() [2]int { return [2]int{r.h.find.at, r.h.find.total} }) }
	r.s.WaitFor(t, "found on the page", func(string) bool { return at()[1] == 2 })
	if v := shownView(r); v != widget.ViewRendered {
		t.Fatalf("the find left the page: view %v", v)
	}
	first := at()[0]
	r.h.p.Post(r.h.findNext)
	r.s.WaitFor(t, "n to the other mark", func(string) bool { return at()[0] != first && at()[1] == 2 })
	r.h.p.Post(r.h.findPrevious)
	r.s.WaitFor(t, "N back", func(string) bool { return at()[0] == first })
	r.keys(t, decltest.Ctrl('t'))
	r.s.WaitFor(t, "counted in the text", func(string) bool { return shownView(r) == widget.ViewRaw && at()[1] == 3 })
	r.keys(t, decltest.Ctrl('t'))
	r.s.WaitFor(t, "counted on the page again", func(string) bool { return shownView(r) == widget.ViewRendered && at()[1] == 2 })
	r.h.p.Post(r.h.clearFind)
	r.s.WaitFor(t, "cleared", func(string) bool { return onLoop(r, func() string { return r.h.find.query }) == "" })
}

// TestAFindOnlyARawHTMLFilesSourceHasGoesToTheText: a term the page doesn't show but the source has,
// a link's target, is the text's: the page opens Raw, says so, and the find goes on there, the
// cursor on its match. A term in neither is not found, the page staying.
func TestAFindOnlyARawHTMLFilesSourceHasGoesToTheText(t *testing.T) {
	r := openHTMLPage(t, birdsPage, "Birds")
	find := func(term string) {
		t.Helper()
		r.h.p.Post(func() { r.h.find.pending = findPage; r.h.startFind(term) })
	}
	find("zebra")
	r.s.WaitForText(t, `find: no "zebra" in the page`)
	if v := shownView(r); v != widget.ViewRendered {
		t.Fatalf("a term in neither left the page: view %v", v)
	}
	find("kestrel.html")
	r.waitKeptNotice(t, `"kestrel.html" is not on the page: found in the text (Raw)`)
	r.s.WaitFor(t, "the find in the text, on its match", func(string) bool {
		return shownView(r) == widget.ViewRaw && r.cursor()[0] == 1 && onLoop(r, func() int { return r.h.find.total }) == 1
	})
}

// TestSPCjOnARawHTMLPageFollowsTheFirstLinkOnScreen: the page has no cursor, so SPC j follows the
// first link at or after its top, among what is on screen; with none on screen it says so. The
// links are the source's, read as a note's links are.
func TestSPCjOnARawHTMLPageFollowsTheFirstLinkOnScreen(t *testing.T) {
	page := "<h1>Links</h1>\n\nfirst [b](b.md)\n\n" + paragraphs("Tail", 60)
	r := openHTMLPage(t, page, "Links", "b.md", "# B\n")
	r.keys(t, key('G')) // the page's end: no link on screen
	r.s.WaitFor(t, "the page's end", func(sc string) bool { return strings.Contains(sc, "Tail 59") })
	r.leader(t, 'j')
	r.waitKeptNotice(t, "no link on the page here: SPC l shows what p.html links with")
	r.keys(t, key('g')) // the top: the link first on screen
	page2 := htmlPage(r)
	r.s.WaitFor(t, "the page's top", func(string) bool { return onLoop(r, func() int { return page2.top }) == 0 })
	r.s.WaitForText(t, "first")
	r.leader(t, 'j')
	r.waitFile(t, "b.md")
}
