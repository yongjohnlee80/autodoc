package tui

import (
	"fmt"
	"path/filepath"
	"strings"
	"testing"

	tuicore "github.com/yongjohnlee80/golib/tui"
	"github.com/yongjohnlee80/golib/tui/decl/decltest"
	"github.com/yongjohnlee80/golib/tui/widget/editor"
)

// AN HTML FILE'S PAGE, opened raw: on a terminal its Rendered view is a page drawn in cells over the
// editor, as a note's was before the editor drew notes itself (docs/decisions/editor.md). These
// cells hold the page's editing keys, its command line and a find only the file's source has.

// openHTMLPage runs the TUI with an HTML file on disk, page in p.html and more files beside it,
// opens p.html raw and waits for its page to show title: Rendered, the page made from the file's
// text. It answers the run and the folder.
func openHTMLPage(t *testing.T, page, title string, more ...string) (*running, string) {
	t.Helper()
	r, _ := runHTML(t)
	dir := fileDir(t, append([]string{"p.html", page}, more...)...)
	path := filepath.Join(dir, "p.html")
	r.openHTMLAnswer(t, path, "raw")
	r.waitOpen(t, "", path)
	r.s.WaitFor(t, "the file's page", func(string) bool { return shownView(r) == editor.ViewRendered })
	r.s.WaitForText(t, title)
	p := htmlPage(r)
	r.s.WaitFor(t, "the file's text on its page", func(string) bool {
		return onLoop(r, func() bool { return p.view != nil && strings.Contains(string(p.view.Source()), title) })
	})
	return r, dir
}

// htmlPage is the page an HTML file is drawn on.
func htmlPage(r *running) *cellNotePage { return onLoop(r, r.h.notePage) }

// TestAnEditingKeyFromARawHTMLPageActsOnce: x from the page deletes the character the cursor is on,
// once, in the text; o from the page opens a line below, once, in Insert mode.
func TestAnEditingKeyFromARawHTMLPageActsOnce(t *testing.T) {
	const page = "<h1>Pantry</h1>\n<p>shelf</p>\n"
	r, _ := openHTMLPage(t, page, "Pantry")
	r.keys(t, key('x'))
	r.s.WaitFor(t, "x acted once", func(string) bool { return r.editorText() == page[1:] })
	if v := shownView(r); v != editor.ViewRaw {
		t.Fatalf("x left the view %v, want the text", v)
	}
	r.keys(t, decltest.Ctrl('t'))
	r.s.WaitFor(t, "the page again", func(string) bool { return shownView(r) == editor.ViewRendered })
	r.keys(t, key('o'))
	r.keys(t, decltest.Type("z")...)
	r.keys(t, tuicore.KeyEvent{Kind: tuicore.KeyPress, Code: tuicore.KeyEscape})
	r.s.WaitFor(t, "o acted once", func(string) bool { return r.editorText() == "h1>Pantry</h1>\nz\n<p>shelf</p>\n" })
}

// TestTheCommandLineOpensFromARawHTMLPage: ':' from the page opens the command line, as from the text.
func TestTheCommandLineOpensFromARawHTMLPage(t *testing.T) {
	r, _ := openHTMLPage(t, "<h1>Pantry</h1>\n", "Pantry")
	r.keys(t, key(':'))
	r.s.WaitForText(t, "VIM Commands")
}

// birdsPage holds "kestrel" twice in its text and once more in a link's target, which only the
// source has.
const birdsPage = "<h1>Birds</h1>\n<p>a kestrel, see <a href=\"kestrel.html\">the hawk</a>, another kestrel</p>\n"

// TestAFindOnlyARawHTMLFilesSourceHasGoesToTheText: a term the page doesn't show but the source has,
// a link's target, is the text's: the page opens Raw, says so, and the find goes on there, the
// cursor on its match. A term in neither is not found, the page staying.
func TestAFindOnlyARawHTMLFilesSourceHasGoesToTheText(t *testing.T) {
	r, _ := openHTMLPage(t, birdsPage, "Birds")
	find := func(term string) {
		t.Helper()
		r.h.p.Post(func() { r.h.find.pending = findPage; r.h.startFind(term) })
	}
	find("zebra")
	r.s.WaitForText(t, `find: no "zebra" in the page`)
	if v := shownView(r); v != editor.ViewRendered {
		t.Fatalf("a term in neither left the page: view %v", v)
	}
	find("kestrel.html")
	r.waitKeptNotice(t, `"kestrel.html" is not on the page: found in the text (Raw)`)
	r.s.WaitFor(t, "the find in the text, on its match", func(string) bool {
		return shownView(r) == editor.ViewRaw && r.cursor()[0] == 1 && onLoop(r, func() int { return r.h.find.total }) == 1
	})
}

// paragraphs is n lines of <p>, each "word NN of the page.".
func paragraphs(word string, n int) string {
	var b strings.Builder
	for i := range n {
		fmt.Fprintf(&b, "<p>%s %02d of the page.</p>\n", word, i)
	}
	return b.String()
}

// pageScroll is how far the page is scrolled, in rows.
func pageScroll(r *running) float32 {
	p := htmlPage(r)
	return onLoop(r, func() float32 { return p.view.ScrollY() })
}

// TestARawHTMLPagesReadingKeysKeepItRendered: the keys that read the page (the arrows, j/k,
// PgUp/PgDn, g/G, ] [ and } {, h/l, y and Ctrl+C) leave it Rendered and the file as it was, and the
// page's scroll is theirs: PgDn moves it on, PgUp back, G to the end, g to the start, ] to the next
// block and [ back.
func TestARawHTMLPagesReadingKeysKeepItRendered(t *testing.T) {
	r, _ := openHTMLPage(t, "<h1>Long</h1>\n"+paragraphs("Line", 40), "Long")
	before := r.editorText()
	var down, bottom float32
	for _, k := range []struct {
		ev     tuicore.Event
		scroll func() bool // the scroll the key makes, nil: none to wait for
	}{
		{key('j'), nil}, {key('k'), nil}, {tuicore.KeyEvent{Kind: tuicore.KeyPress, Code: tuicore.KeyDown}, nil},
		{tuicore.KeyEvent{Kind: tuicore.KeyPress, Code: tuicore.KeyUp}, nil},
		{tuicore.KeyEvent{Kind: tuicore.KeyPress, Code: tuicore.KeyPageDown}, func() bool { down = pageScroll(r); return down > 0 }},
		{tuicore.KeyEvent{Kind: tuicore.KeyPress, Code: tuicore.KeyPageUp}, func() bool { return pageScroll(r) < down }},
		{key('G'), func() bool { bottom = pageScroll(r); return bottom > down }},
		{key('g'), func() bool { return pageScroll(r) == 0 }},
		{key(']'), func() bool { return pageScroll(r) > 0 }},
		{key('['), func() bool { return pageScroll(r) == 0 }},
		{key('}'), nil}, {key('{'), nil},
		{key('h'), nil}, {key('l'), nil}, {tuicore.KeyEvent{Kind: tuicore.KeyPress, Code: tuicore.KeyLeft}, nil},
		{tuicore.KeyEvent{Kind: tuicore.KeyPress, Code: tuicore.KeyRight}, nil},
		{key('y'), nil}, {tuicore.KeyEvent{Kind: tuicore.KeyPress, Code: 'c', Mods: tuicore.ModCtrl}, nil},
	} {
		r.keys(t, k.ev)
		if k.scroll != nil {
			r.s.WaitFor(t, fmt.Sprintf("%+v scrolls the page", k.ev), func(string) bool { return k.scroll() })
		}
		if v := shownView(r); v != editor.ViewRendered {
			t.Fatalf("%+v left the page (view %v)", k.ev, v)
		}
	}
	if got := r.editorText(); got != before {
		t.Fatalf("reading the page changed the file:\n%q\nwant\n%q", got, before)
	}
}

// TestTheTextKeymapTypesEveryPrintingKeyOnARawHTMLPage: under Text (modeless) every printing key
// types, so the keys Vim reads the page by open the text and type there; the page travels by Alt and
// an arrow instead.
func TestTheTextKeymapTypesEveryPrintingKeyOnARawHTMLPage(t *testing.T) {
	r, _ := openHTMLPage(t, "<h1>Long</h1>\n"+paragraphs("Line", 20), "Long")
	r.leader(t, 'k') // Vim to Text
	r.s.WaitForText(t, "editor mode: Text (modeless)")
	if v := shownView(r); v != editor.ViewRendered {
		t.Fatalf("after the keymap switch the file shows view %v, want its page", v)
	}
	r.keys(t, tuicore.KeyEvent{Kind: tuicore.KeyPress, Code: tuicore.KeyDown, Mods: tuicore.ModAlt})
	r.s.WaitFor(t, "Alt+Down travels to the next block", func(string) bool { return pageScroll(r) > 0 })
	r.keys(t, tuicore.KeyEvent{Kind: tuicore.KeyPress, Code: tuicore.KeyUp, Mods: tuicore.ModAlt})
	r.s.WaitFor(t, "Alt+Up travels back", func(string) bool { return pageScroll(r) == 0 })
	const typed = " hjkl[]{}gGy"
	r.keys(t, decltest.Type(typed)...)
	r.s.WaitFor(t, "the keys typed into the file", func(string) bool { return strings.HasPrefix(r.editorText(), typed+"<h1>Long") })
	if v := shownView(r); v != editor.ViewRaw {
		t.Fatalf("after typing the file shows view %v, want its text", v)
	}
}

// TestAFindOnARawHTMLPageCountsWhatItShows: the page's find counts and marks what the page shows:
// two of "kestrel", the third being a link's target only the source has; n and N step through the
// page's marks; Ctrl+T to the text counts its source, three, and Ctrl+T back to the page counts
// the page's two again, at once, with no n or N.
func TestAFindOnARawHTMLPageCountsWhatItShows(t *testing.T) {
	r, _ := openHTMLPage(t, birdsPage, "Birds")
	r.h.p.Post(func() { r.h.find.pending = findPage; r.h.startFind("kestrel") })
	at := func() [2]int { return onLoop(r, func() [2]int { return [2]int{r.h.find.at, r.h.find.total} }) }
	r.s.WaitFor(t, "found on the page", func(string) bool { return at()[1] == 2 })
	if v := shownView(r); v != editor.ViewRendered {
		t.Fatalf("the find left the page: view %v", v)
	}
	first := at()[0]
	r.h.p.Post(r.h.findNext)
	r.s.WaitFor(t, "n to the other mark", func(string) bool { return at()[0] != first && at()[1] == 2 })
	r.h.p.Post(r.h.findPrevious)
	r.s.WaitFor(t, "N back", func(string) bool { return at()[0] == first && at()[1] == 2 })
	r.keys(t, decltest.Ctrl('t'))
	r.s.WaitFor(t, "counted in the text", func(string) bool { return shownView(r) == editor.ViewRaw && at()[1] == 3 })
	r.keys(t, decltest.Ctrl('t'))
	r.s.WaitFor(t, "counted on the page again", func(string) bool {
		return shownView(r) == editor.ViewRendered && at() == [2]int{1, 2}
	})
}

// TestARightPressOnARawHTMLPageOpensTheEditorsMenu: a right press on the page opens the editor's
// menu there, as over the text, and Esc closes it with the page still shown.
func TestARightPressOnARawHTMLPageOpensTheEditorsMenu(t *testing.T) {
	r, _ := openHTMLPage(t, "<h1>Pantry</h1>\n<p>shelf</p>\n", "Pantry")
	r.keys(t, tuicore.MouseEvent{Kind: tuicore.MousePress, Button: tuicore.MouseRight, X: 20, Y: 6})
	r.s.WaitForText(t, "Undo")
	r.keys(t, tuicore.KeyEvent{Kind: tuicore.KeyPress, Code: tuicore.KeyEscape})
	r.s.WaitFor(t, "the menu closed", func(sc string) bool { return !strings.Contains(sc, "Undo") })
	if v := shownView(r); v != editor.ViewRendered {
		t.Fatalf("the menu left the page: view %v", v)
	}
}

// TestSPCjOnARawHTMLPageFollowsItsLink: the page has no cursor, so SPC j follows the first link in
// the file's source, read as a note's links are: here the file beside it. The
// page is closed for the file it opens.
func TestSPCjOnARawHTMLPageFollowsItsLink(t *testing.T) {
	r, dir := openHTMLPage(t, "<h1>Links</h1>\n\nfirst [b](b.md)\n", "Links", "b.md", "# B\n")
	r.leader(t, 'j')
	r.waitOpen(t, "", filepath.Join(dir, "b.md")) // beside the page, as a relative link from it reads
}

// TestSPCjOnARawHTMLPageWithNoLinkSaysSo: a page with no link says so, in the page's words.
func TestSPCjOnARawHTMLPageWithNoLinkSaysSo(t *testing.T) {
	r, dir := openHTMLPage(t, "<h1>Plain</h1>\n<p>no link here</p>\n", "Plain")
	r.leader(t, 'j')
	r.waitKeptNotice(t, "no link on the page here: SPC l shows what "+filepath.Join(dir, "p.html")+" links with")
}
