package tui

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	tuicore "github.com/yongjohnlee80/golib/tui"
	"github.com/yongjohnlee80/golib/tui/decl/decltest"
	"github.com/yongjohnlee80/golib/tui/widget"
)

// THE PAGE'S FIND, marked and cleared (Johno, 2026-10-02): the page marks a find's words,
// "finding …" at the page window's top right clears it with its X, and the Search menu reaches the find from the
// Text mode.

// cellOf is the screen cell at the start of sub, on the first row holding within (sub inside it),
// and whether it was found.
func (r *running) cellOf(within, sub string) (tuicore.Cell, bool) {
	snap := r.s.Backend.Snapshot()
	for _, row := range snap {
		var b strings.Builder
		for _, c := range row {
			b.WriteString(c.Content)
		}
		line := b.String()
		i := strings.Index(line, within)
		if i < 0 {
			continue
		}
		at := len([]rune(line[:i])) + len([]rune(within[:strings.Index(within, sub)]))
		// a cell holds one grapheme here (the files are ASCII), so runes count cells
		for x, n := 0, 0; x < len(row); x++ {
			if n == at {
				return row[x], true
			}
			n += len([]rune(row[x].Content))
		}
	}
	return tuicore.Cell{}, false
}

// marked is whether the page paints sub in "one kestrel" differently from "one", its plain text.
func (r *running) marked(within, sub, plain string) bool {
	return func() bool {
		w, ok1 := r.cellOf(within, sub)
		p, ok2 := r.cellOf(within, plain)
		return ok1 && ok2 && w.Attrs != p.Attrs
	}()
}

// findCardWhole reports whether row, the find card's, ends with the card's own X and frame: nothing
// drawn over its right end.
func findCardWhole(row string) bool {
	end := strings.TrimRight(row, " ")
	return strings.HasSuffix(end, "│") && strings.HasSuffix(strings.TrimRight(strings.TrimSuffix(end, "│"), " "), "X")
}

// findChipRow is the row "finding …" is on, and its text; -1 when it is not shown.
func (r *running) findChipRow() (int, string) {
	for y, row := range strings.Split(r.s.String(), "\n") {
		if strings.Contains(row, "finding \"") {
			return y, row
		}
	}
	return -1, ""
}

// TestAPageFindIsMarkedAndClearedByItsX: / marks every occurrence in the page, and "finding …"
// shows at the top right of the page's window; its X, clicked, unmarks the page and hides it. A
// drawer opened over the window covers it while it has the keyboard, the card being the window's.
func TestAPageFindIsMarkedAndClearedByItsX(t *testing.T) {
	d := startDaemon(t, map[string][]string{"kb": {"a.md", "one kestrel\ntwo\nthree kestrel\nfour\n", "x.md", "see [[a]]\n"}})
	r := attached(t, d)
	r.openByPicker(t, "a.md")
	r.waitFile(t, "a.md")
	r.s.WaitForText(t, "three kestrel")
	if r.marked("one kestrel", "kestrel", "one") {
		t.Fatal("kestrel is marked before any find")
	}

	r.keys(t, key('/'))
	r.s.WaitForText(t, "find in the page")
	r.keys(t, decltest.Ctrl('u'))
	r.keys(t, decltest.Type("kestrel")...)
	r.keys(t, enter())
	r.s.WaitForText(t, `find "kestrel": 1 of 2 in the page`)
	r.s.WaitFor(t, "both occurrences marked", func(string) bool {
		return r.marked("one kestrel", "kestrel", "one") && r.marked("three kestrel", "kestrel", "three")
	})
	r.s.WaitFor(t, "the find card at the window's top right", func(string) bool { y, _ := r.findChipRow(); return y == 1 })
	_, row := r.findChipRow()
	if end := strings.TrimRight(row, " "); !strings.HasSuffix(end, "│") || !strings.HasSuffix(strings.TrimRight(strings.TrimSuffix(end, "│"), " "), "X") || !strings.Contains(r.s.String(), "┌ Find ") {
		t.Fatalf("the card is not framed and titled, with its X at the right end: %q", row)
	}
	// the match the find is on is highlighted, the other only marked
	if m, ok := onLoop(r, func() widget.TextRange { m, _ := r.h.core.CurrentMatch(); return m }), true; !ok || m != (widget.TextRange{From: widget.TextPos{Line: 0, Col: 4}, To: widget.TextPos{Line: 0, Col: 11}}) {
		t.Fatalf("the current match is %v", m)
	}

	// the explorer's drawer, at the right edge, opens on its Links tab with the keyboard: the card,
	// the window's, is under it, and back once the page has the keyboard again
	onLoop(r, func() bool { r.h.choosePrefNamed(prefExplorer, slices.Index(edges, "right")); return true })
	r.leader(t, 'l')
	r.s.WaitForText(t, "backlinks (1)")
	r.s.WaitFor(t, "the drawer drawn over the card's right end", func(string) bool {
		_, row := r.findChipRow()
		return strings.Contains(row, "links (1)") && !findCardWhole(row)
	})
	r.keys(t, esc())
	r.s.WaitFor(t, "the card whole again", func(string) bool { _, row := r.findChipRow(); return findCardWhole(row) })

	// its X, clicked
	y, row := r.findChipRow()
	x := len([]rune(row[:strings.LastIndex(row, "X")]))
	r.keys(t, tuicore.MouseEvent{Kind: tuicore.MousePress, Button: tuicore.MouseLeft, X: x, Y: y},
		tuicore.MouseEvent{Kind: tuicore.MouseRelease, Button: tuicore.MouseLeft, X: x, Y: y})
	r.s.WaitForText(t, "find cleared")
	r.s.WaitFor(t, "the chip gone and the page unmarked", func(string) bool {
		y, _ := r.findChipRow()
		return y < 0 && !r.marked("one kestrel", "kestrel", "one") && !r.marked("three kestrel", "kestrel", "three")
	})
	if q := onLoop(r, func() string { return r.h.find.query }); q != "" {
		t.Fatalf("the find after its X is %q, want none", q)
	}
	if _, on := onLoop(r, func() widget.TextRange { m, _ := r.h.core.CurrentMatch(); return m }), onLoop(r, func() bool { _, ok := r.h.core.CurrentMatch(); return ok }); on {
		t.Fatal("the current match outlived the find")
	}
}

// TestSearchMenuFindsInTheTextMode: in the Text mode / types a slash, so Search › Find in page asks
// for the find; Find next goes on from the menu; Clear find ends it. Driven through the menu bar
// itself (Alt+R, then each item's letter), which has the keyboard while it is open.
func TestSearchMenuFindsInTheTextMode(t *testing.T) {
	d := startDaemon(t, map[string][]string{"kb": {"a.md", "one kestrel\ntwo\nthree kestrel\nfour\n"}})
	r := attached(t, d)
	r.openByPicker(t, "a.md")
	r.waitFile(t, "a.md")
	r.h.p.Post(func() { r.h.setKeymap("text") })
	r.s.WaitFor(t, "the Text mode", func(string) bool { return onLoop(r, func() bool { return r.h.prefs.keymap == "text" }) })
	searchMenu := func(item rune) {
		t.Helper()
		r.keys(t, decltest.Alt('r'))
		r.s.WaitForText(t, "Find in page")
		r.keys(t, key(item))
	}
	searchMenu('f') // Search › Find in page
	r.s.WaitForText(t, "find in the page")
	r.keys(t, decltest.Ctrl('u'))
	r.keys(t, decltest.Type("kestrel")...)
	r.keys(t, enter())
	r.s.WaitForText(t, `find "kestrel": 1 of 2 in the page`)
	r.s.WaitFor(t, "kestrel marked", func(string) bool { return r.marked("three kestrel", "kestrel", "three") })
	// with the explorer open and the keyboard in it, Search › Find next still goes on in the page:
	// the menu bar hands the keyboard back to the page before it runs an item
	r.h.p.Post(func() { r.h.togglePanel("explorer") })
	r.s.WaitFor(t, "the keyboard in the explorer", func(string) bool { return r.focused(r.h.panelView("explorer")) })
	searchMenu('n') // Search › Find next
	r.s.WaitForText(t, `find "kestrel": 2 of 2 in the page`)
	searchMenu('c') // Search › Clear find
	r.s.WaitForText(t, "find cleared")
	r.s.WaitFor(t, "unmarked", func(string) bool { return !r.marked("three kestrel", "kestrel", "three") })
}

// A reload that rebuilds the page's window puts the find's card in the new one. Run from a copy of
// the QML (-dev), main.qml edited so the window is built anew (a label, a property it is built
// with), the find still on: "finding …" is back in the new window.
func TestTheFindCardOutlivesARebuiltPageWindow(t *testing.T) {
	dev := t.TempDir()
	if err := os.CopyFS(dev, qmlFiles); err != nil {
		t.Fatal(err)
	}
	d := startDaemon(t, map[string][]string{"kb": {"a.md", "one kestrel\ntwo\n"}})
	r := runTUI(t, NewSession(d.sock, nil), Options{Dev: dev})
	r.ready(t)
	r.openByPicker(t, "a.md")
	r.waitFile(t, "a.md")
	r.keys(t, key('/'))
	r.s.WaitForText(t, "find in the page")
	r.keys(t, decltest.Ctrl('u'))
	r.keys(t, decltest.Type("kestrel")...)
	r.keys(t, enter())
	r.s.WaitFor(t, "the find card", func(string) bool { y, _ := r.findChipRow(); return y >= 0 })
	was := onLoop(r, func() any { c, _ := r.h.p.Find("editorWindow"); return c })
	main := filepath.Join(dev, "main.qml")
	src, err := os.ReadFile(main)
	if err != nil {
		t.Fatal(err)
	}
	edited := strings.Replace(string(src), `key: "editor"`, `key: "editor"; label: "Page"`, 1)
	if edited == string(src) {
		t.Fatal(`main.qml's editorWindow has no key: "editor" to edit beside`)
	}
	if err := os.WriteFile(main+".tmp", []byte(edited), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(main+".tmp", main); err != nil {
		t.Fatal(err)
	}
	r.s.WaitFor(t, "the page's window rebuilt", func(string) bool {
		return onLoop(r, func() any { c, _ := r.h.p.Find("editorWindow"); return c }) != was
	})
	r.s.WaitFor(t, "the find card in the rebuilt window", func(string) bool { y, _ := r.findChipRow(); return y >= 0 })
}
