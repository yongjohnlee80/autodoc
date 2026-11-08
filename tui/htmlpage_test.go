package tui

import (
	"path/filepath"
	"strings"
	"testing"

	tuicore "github.com/yongjohnlee80/golib/tui"
	"github.com/yongjohnlee80/golib/tui/decl/decltest"
	"github.com/yongjohnlee80/golib/tui/widget"
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
	r.s.WaitFor(t, "the file's page", func(string) bool { return shownView(r) == widget.ViewRendered })
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
	if v := shownView(r); v != widget.ViewRendered {
		t.Fatalf("a term in neither left the page: view %v", v)
	}
	find("kestrel.html")
	r.waitKeptNotice(t, `"kestrel.html" is not on the page: found in the text (Raw)`)
	r.s.WaitFor(t, "the find in the text, on its match", func(string) bool {
		return shownView(r) == widget.ViewRaw && r.cursor()[0] == 1 && onLoop(r, func() int { return r.h.find.total }) == 1
	})
}
