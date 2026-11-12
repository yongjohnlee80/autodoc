package tui

import (
	"fmt"
	"strings"
	"testing"

	tuicore "github.com/yongjohnlee80/golib/tui"
	"github.com/yongjohnlee80/golib/tui/widget/editor"
)

// TestSwitchingBetweenMarkdownNotesKeepsThemHighlighted: switching the terminal editor between two
// Markdown notes, each new to it, draws each highlighted (its heading's "#" in another style than a
// body line) in its Raw view, its crumb naming the heading the cursor is under, the second as much
// as the first: the page's syntax is Markdown both times, so no change of it reaches the editor,
// and the buffer shown second must take the highlighting the editor already has. Opening the same
// note again, and the other again with :e, draw it highlighted too. A note on a terminal opens
// Rendered (notepage.go): the first is taken to its text with Ctrl+T, as a reader does, and every
// switch after it keeps that choice, so each check finds the Raw view without asking for it again.
func TestSwitchingBetweenMarkdownNotesKeepsThemHighlighted(t *testing.T) {
	note := func(h1 string) string { return "# " + h1 + "\n\nSome **bold** words.\n\n## Shelf\n\nbeans\n" }
	d := startDaemon(t, map[string][]string{"kb": {"a.md", note("Larder"), "b.md", note("Cellar")}})
	r := runTUISized(t, NewSession(d.sock, nil), Options{}, 120, 40)
	r.waitAttached(t, "kb")
	// drawn: the "#" of the "# <h1>" line and the "b" of "beans", their styles
	drawn := func(h1 string) (head, body tuicore.CellAttrs, ok bool) {
		var gotHead, gotBody bool
		for _, row := range r.s.Backend.Snapshot() {
			var line strings.Builder
			for _, c := range row {
				line.WriteString(c.Content)
			}
			s := line.String()
			if i := strings.Index(s, "# "+h1); i >= 0 && !gotHead {
				head, gotHead = cellOnScreen(row, i), true
			}
			if i := strings.Index(s, "beans"); i >= 0 && !gotBody {
				body, gotBody = cellOnScreen(row, i), true
			}
		}
		return head, body, gotHead && gotBody
	}
	check := func(how, p, h1 string) {
		t.Helper()
		r.waitFile(t, p)
		if got := r.syntaxShown(); !strings.Contains(got, "Markdown") {
			t.Fatalf("%s: App.syntaxDefinition %s", how, got)
		}
		if v := onLoop(r, func() editor.EditorView { return r.h.editor.(*editor.Editor).ShownView() }); v != editor.ViewRaw {
			t.Fatalf("%s: the editor shows view %v, not Raw", how, v)
		}
		var last string
		r.s.WaitFor(t, how+": "+p+" drawn highlighted", func(string) bool {
			head, body, ok := drawn(h1)
			last = fmt.Sprintf("heading %+v, body %+v", head, body)
			return ok && head != body
		})
		r.keys(t, key('G')) // the cursor under ## Shelf
		r.s.WaitFor(t, fmt.Sprintf("%s: the crumb names its heading (%s)", how, last), func(sc string) bool {
			return strings.Contains(sc, p+" ›") && strings.Contains(sc, "Shelf")
		})
	}
	open := func(p string) { onLoop(r, func() bool { r.h.openPath(p); return true }) }
	open("a.md")
	r.asText(t) // the note opens as its page; the highlighting under test is its text's
	check("the first note", "a.md", "Larder")
	open("b.md")
	check("a second Markdown note after the first", "b.md", "Cellar")
	open("a.md")
	check("the first again", "a.md", "Larder")
	open("a.md")
	check("the same note opened again", "a.md", "Larder")
	r.command(t, "e b.md")
	check("the second again with :e", "b.md", "Cellar")
}

// cellOnScreen is the style of row's cell under byte offset i of its text.
func cellOnScreen(row []tuicore.Cell, i int) tuicore.CellAttrs {
	off := 0
	for _, c := range row {
		if off >= i {
			return c.Attrs
		}
		off += len(c.Content)
	}
	return tuicore.CellAttrs{}
}
