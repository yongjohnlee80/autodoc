package tui

import (
	"strings"
	"testing"

	tuicore "github.com/yongjohnlee80/golib/tui"
	"github.com/yongjohnlee80/golib/tui/decl/decltest"
)

// THE PAGE'S FIND, marked and cleared (Johno, 2026-10-02): the page marks a find's words,
// "finding …" under the menu clears it with its ✕, and the Search menu reaches the find from the
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
		// a cell holds one grapheme here (the notes are ASCII), so runes count cells
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
// shows at the top right a row under the menu bar; its ✕, clicked, unmarks the page and hides it.
// A drawer opened at the right edge hides it while it is open.
func TestAPageFindIsMarkedAndClearedByItsX(t *testing.T) {
	d := startDaemon(t, map[string][]string{"kb": {"a.md", "one kestrel\ntwo\nthree kestrel\nfour\n", "x.md", "see [[a]]\n"}})
	r := attached(t, d)
	r.openByPicker(t, "a.md")
	r.waitNote(t, "a.md")
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
	r.s.WaitFor(t, "finding … under the menu", func(string) bool { y, _ := r.findChipRow(); return y == 1 })
	_, row := r.findChipRow()
	if !strings.HasSuffix(strings.TrimRight(row, " "), "✕") {
		t.Fatalf("the chip is not at the right end with its ✕: %q", row)
	}

	// the links drawer opens at the right edge: the chip hides under it, and comes back after
	r.leader(t, 'l')
	r.s.WaitForText(t, "backlinks (1)")
	r.s.WaitFor(t, "the chip hidden under the drawer", func(string) bool { y, _ := r.findChipRow(); return y < 0 })
	r.keys(t, esc())
	r.s.WaitFor(t, "the chip back", func(string) bool { y, _ := r.findChipRow(); return y == 1 })

	// its ✕, clicked
	y, row := r.findChipRow()
	x := len([]rune(row[:strings.LastIndex(row, "✕")]))
	r.keys(t, tuicore.MouseEvent{Kind: tuicore.MousePress, Button: tuicore.MouseLeft, X: x, Y: y},
		tuicore.MouseEvent{Kind: tuicore.MouseRelease, Button: tuicore.MouseLeft, X: x, Y: y})
	r.s.WaitForText(t, "find cleared")
	r.s.WaitFor(t, "the chip gone and the page unmarked", func(string) bool {
		y, _ := r.findChipRow()
		return y < 0 && !r.marked("one kestrel", "kestrel", "one") && !r.marked("three kestrel", "kestrel", "three")
	})
	if q := onLoop(r, func() string { return r.h.find.query }); q != "" {
		t.Fatalf("the find after its ✕ is %q, want none", q)
	}
}

// TestSearchMenuFindsInTheTextMode: in the Text mode / types a slash, so Search › Find in page asks
// for the find; Find next goes on from the menu; Clear find ends it. Driven through the menu bar
// itself (Alt+R, then each item's letter), which has the keyboard while it is open.
func TestSearchMenuFindsInTheTextMode(t *testing.T) {
	d := startDaemon(t, map[string][]string{"kb": {"a.md", "one kestrel\ntwo\nthree kestrel\nfour\n"}})
	r := attached(t, d)
	r.openByPicker(t, "a.md")
	r.waitNote(t, "a.md")
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
	r.s.WaitFor(t, "the keyboard in the explorer", func(string) bool { return r.focused(panels["explorer"]) })
	searchMenu('n') // Search › Find next
	r.s.WaitForText(t, `find "kestrel": 2 of 2 in the page`)
	searchMenu('c') // Search › Clear find
	r.s.WaitForText(t, "find cleared")
	r.s.WaitFor(t, "unmarked", func(string) bool { return !r.marked("three kestrel", "kestrel", "three") })
}
