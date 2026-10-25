package tui

import (
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/yongjohnlee80/golib/tui/decl/decltest"
	"github.com/yongjohnlee80/golib/tui/widget"
)

// buffertabs_test.go: the GUI's buffer tabs, through what they are made of, which the TUI has too:
// each tab's label and its menu's rows (buffertabs.go). The strip itself is gui_buffertabs_test.go's.

// tabRow is the row of rows with id, searching submenus, and whether it is there.
func tabRow(rows []widget.MenuItemModel, id string) (widget.MenuItemModel, bool) {
	for _, r := range rows {
		if string(r.ID) == id {
			return r, true
		}
		if c, ok := tabRow(r.Children, id); ok {
			return c, true
		}
	}
	return widget.MenuItemModel{}, false
}

// runTabRow runs the row with id of the menu of b's tab, on the loop.
func runTabRow(t *testing.T, r *running, b *openBuffer, id string) {
	t.Helper()
	ok := onLoop(r, func() bool {
		row, ok := tabRow(r.h.bufferTabRows(tabID(b)), id)
		if ok {
			row.Action.(widget.TabStripMenuAction).Run(tabID(b))
		}
		return ok
	})
	if !ok {
		t.Fatalf("no row %s in the menu of buffer %d's tab", id, b.n)
	}
}

// tabID is b's tab id: its number.
func tabID(b *openBuffer) string { return strconv.Itoa(b.n) }

// TestABufferTabIsNamedByItsFile: a tab is its file's name; two files of one name show their paths;
// [+] while modified.
func TestABufferTabIsNamedByItsFile(t *testing.T) {
	d := startDaemon(t, map[string][]string{"kb": {"notes/a.md", "a\n", "old/a.md", "old a\n", "b.md", "b\n"}})
	r := attached(t, d)
	r.waitListed(t, 3)
	for _, p := range []string{"notes/a.md", "b.md"} {
		r.openByPicker(t, p)
		r.waitFile(t, p)
	}
	labels := func() string {
		return onLoop(r, func() string {
			var out []string
			for _, b := range r.h.buffers {
				if b.file.open {
					out = append(out, r.h.bufferTabLabel(b, bufferTabWidth-2))
				}
			}
			return strings.Join(out, "|")
		})
	}
	if got := labels(); got != "a.md|b.md" {
		t.Fatalf("labels %q, want the files' names", got)
	}
	r.openByPicker(t, "old/a.md")
	r.waitFile(t, "old/a.md")
	r.typeInEditor(t, "X")
	r.waitTyped(t)
	if got := labels(); got != "notes/a.md|b.md|old/a.md [+]" {
		t.Fatalf("labels %q, want two a.md's by path and [+] on the modified one", got)
	}
}

// TestABufferTabsMenu: Go to › lists every buffer and shows the one chosen; Save is offered for a
// modified buffer only and saves it, shown; Close others closes every other buffer, asking about a
// modified one first.
func TestABufferTabsMenu(t *testing.T) {
	d := startDaemon(t, map[string][]string{"kb": {"a.md", "aaa\n", "b.md", "bbb\n", "c.md", "ccc\n"}})
	r := attached(t, d)
	r.waitListed(t, 3)
	for _, p := range []string{"a.md", "b.md"} {
		r.openByPicker(t, p)
		r.waitFile(t, p)
	}
	r.typeInEditor(t, "X") // b.md modified
	r.waitTyped(t)
	r.openByPicker(t, "c.md")
	r.waitFile(t, "c.md")
	a, b, c := bufferOn(r, "a.md"), bufferOn(r, "b.md"), bufferOn(r, "c.md")

	rows := onLoop(r, func() []widget.MenuItemModel { return r.h.bufferTabRows(tabID(a)) })
	if g, ok := tabRow(rows, "buffer.tabs.goto"); !ok || len(g.Children) != onLoop(r, func() int { return len(r.h.buffers) }) {
		t.Fatalf("Go to: %+v; want a row for each buffer", g)
	}
	if s, _ := tabRow(rows, "buffer.tabs.save"); s.Enabled {
		t.Fatal("Save offered for a.md, which is clean")
	}
	if s, _ := tabRow(onLoop(r, func() []widget.MenuItemModel { return r.h.bufferTabRows(tabID(b)) }), "buffer.tabs.save"); !s.Enabled {
		t.Fatal("Save not offered for b.md, which is modified")
	}
	if rows := onLoop(r, func() []widget.MenuItemModel { return r.h.bufferTabRows("") }); len(rows) != 4 {
		t.Fatalf("the menu off a tab: %d rows, want New buffer, Open file, Layout and Go to", len(rows))
	}

	runTabRow(t, r, a, "buffer.tabs.goto."+tabID(a))
	r.waitBuffer(t, "a.md")
	runTabRow(t, r, b, "buffer.tabs.save")
	r.s.WaitForText(t, "saved b.md")
	if got := d.read(t, "kb", "b.md"); got != "Xbbb\n" || r.file().path != "b.md" {
		t.Fatalf("Save from b.md's tab: disk %q, shown %s", got, r.file().path)
	}

	r.typeInEditor(t, "Y") // b.md modified again
	r.waitTyped(t)
	runTabRow(t, r, c, "buffer.tabs.closeothers")
	r.s.WaitForText(t, "b.md has unsaved changes")
	r.keys(t, key('d'))
	r.s.WaitFor(t, "c.md alone", func(string) bool {
		return onLoop(r, func() bool { return len(r.h.buffers) == 1 && r.h.buffers[0] == c && r.h.active == c })
	})
	if got := d.read(t, "kb", "b.md"); got != "Xbbb\n" {
		t.Errorf("Discard wrote b.md: %q", got)
	}
}

// TestBufferTabsAreToldApart: no two tabs read alike: one path open in two workspaces shows each
// tab's workspace, and two drafts each show their number.
func TestBufferTabsAreToldApart(t *testing.T) {
	d := startDaemon(t, map[string][]string{"kb": {"notes/a.md", "a\n"}, "kb2": {"notes/a.md", "other a\n"}})
	r := attached(t, d)
	r.waitListed(t, 1)
	r.openByPicker(t, "notes/a.md")
	r.waitFile(t, "notes/a.md")
	ws := r.file().ws
	otherWS := map[string]string{"kb": "kb2", "kb2": "kb"}[ws]
	onLoop(r, func() bool { r.h.openRef(fileRef{otherWS, "notes/a.md"}, false, nil); return true })
	r.s.WaitFor(t, "notes/a.md of "+otherWS+" open", func(string) bool { f := r.file(); return f.open && f.ws == otherWS })
	onLoop(r, func() bool {
		r.h.restoreAsDraft(recovered{Text: "one\n"}, "")
		r.h.restoreAsDraft(recovered{Text: "two\n"}, "")
		return true
	})
	labels := onLoop(r, func() []string {
		var out []string
		for _, b := range r.h.buffers {
			out = append(out, r.h.bufferTabLabel(b, bufferTabWidth-2))
		}
		return out
	})
	seen := map[string]bool{}
	for _, l := range labels {
		if seen[l] {
			t.Fatalf("tabs %q: %q twice", labels, l)
		}
		seen[l] = true
	}
	want := []string{ws + ": notes/a.md", otherWS + ": notes/a.md"}
	for _, w := range want {
		if !seen[w] {
			t.Fatalf("tabs %q: want %q", labels, w)
		}
	}
	drafts := 0
	for _, l := range labels {
		if strings.HasPrefix(l, untitled+" ") && strings.HasSuffix(l, " [+]") {
			drafts++
		}
	}
	if drafts != 2 {
		t.Fatalf("tabs %q: want two numbered, modified drafts", labels)
	}
}

// TestABufferTabsMenuMakesANewBuffer: New buffer shows a new, empty draft, the other buffers as they
// were; Open file in a new buffer… shows a new one and File › Open, and the file chosen loads into
// it, the buffer shown before keeping its file.
func TestABufferTabsMenuMakesANewBuffer(t *testing.T) {
	d := startDaemon(t, map[string][]string{"kb": {"a.md", "aaa\n", "b.md", "bbb\n"}})
	r := attached(t, d)
	r.waitListed(t, 2)
	r.openByPicker(t, "a.md")
	r.waitFile(t, "a.md")
	a := bufferOn(r, "a.md")
	count := func() int { return onLoop(r, func() int { return len(r.h.buffers) }) }
	before := count()
	runTabRow(t, r, a, "buffer.tabs.new")
	r.s.WaitFor(t, "a new, empty buffer shown", func(string) bool {
		return onLoop(r, func() bool { return !r.h.file.open && r.h.core.Value() == "" && r.h.active != a })
	})
	if count() != before+1 || bufferOn(r, "a.md") != a {
		t.Fatalf("New buffer: %d buffers (want %d), a.md's kept %v", count(), before+1, bufferOn(r, "a.md") == a)
	}

	r.openByPicker(t, "a.md") // shown again: the empty draft stays
	r.waitBuffer(t, "a.md")
	runTabRow(t, r, a, "buffer.tabs.open")
	r.s.WaitForText(t, "open a document")
	r.keys(t, decltest.Type("b.md")...)
	r.keys(t, enter())
	r.waitFile(t, "b.md")
	if bufferOn(r, "a.md") != a || onLoop(r, func() bool { return a.file.path != "a.md" }) {
		t.Fatal("Open file in a new buffer… loaded over a.md's buffer")
	}
}

// TestATabsCloseMarkClosesItsBuffer: a tab's × (closeBufferTab, the strip's SetOnClose) closes a
// clean buffer at once, and asks first about a modified one, which stays until answered. A tab no
// buffer has (closed meanwhile) closes nothing.
func TestATabsCloseMarkClosesItsBuffer(t *testing.T) {
	d := startDaemon(t, map[string][]string{"kb": {"a.md", "aaa\n", "b.md", "bbb\n"}})
	r := attached(t, d)
	r.waitListed(t, 2)
	r.openByPicker(t, "a.md")
	r.waitFile(t, "a.md")
	r.typeInEditor(t, "X")
	r.waitTyped(t)
	r.openByPicker(t, "b.md")
	r.waitFile(t, "b.md")
	a, b := bufferOn(r, "a.md"), bufferOn(r, "b.md")
	count := func() int { return onLoop(r, func() int { return len(r.h.buffers) }) }
	before := count()
	onLoop(r, func() bool { r.h.closeBufferTab("999"); return true })
	if count() != before {
		t.Fatalf("a tab no buffer has closed one: %d buffers, want %d", count(), before)
	}
	onLoop(r, func() bool { r.h.closeBufferTab(tabID(b)); return true }) // clean: closed at once
	r.s.WaitFor(t, "b.md closed", func(string) bool { return bufferOn(r, "b.md") == nil })
	onLoop(r, func() bool { r.h.closeBufferTab(tabID(a)); return true }) // modified: asked first
	r.s.WaitForText(t, "unsaved file")
	if bufferOn(r, "a.md") != a {
		t.Fatal("a.md, modified, closed without the question")
	}
}

// TestALongTabKeepsItsMarkAndWhatTellsItApart: a long name is cut in its middle within the tab, its
// end (the file's name and kind) and [+] kept; two long paths that cut alike take their numbers.
func TestALongTabKeepsItsMarkAndWhatTellsItApart(t *testing.T) {
	long, one, two := "loooong-file-title-to-test-the-cut.md", "projects-one/notes/very-long-name.md", "projects-two/notes/very-long-name.md"
	d := startDaemon(t, map[string][]string{"kb": {long, "l\n", one, "1\n", two, "2\n"}})
	r := attached(t, d)
	r.waitListed(t, 3)
	r.openByPicker(t, long)
	r.waitFile(t, long)
	r.typeInEditor(t, "X")
	r.waitTyped(t)
	for _, p := range []string{one, two} {
		r.openByPicker(t, p)
		r.waitFile(t, p)
	}
	labels := onLoop(r, func() []string {
		var out []string
		for _, b := range r.h.buffers {
			out = append(out, r.h.bufferTabLabel(b, bufferTabWidth-2))
		}
		return out
	})
	room := bufferTabWidth - 2 // the tab's padding
	seen := map[string]bool{}
	for _, l := range labels {
		if w := len([]rune(l)); w > room {
			t.Fatalf("tab %q is %d cells, more than the %d a tab has", l, w, room)
		}
		if seen[l] {
			t.Fatalf("tabs %q: %q twice", labels, l)
		}
		seen[l] = true
	}
	if l := labels[0]; !strings.HasSuffix(l, "-cut.md [+]") || !strings.Contains(l, "…") {
		t.Fatalf("the long, modified tab is %q: want its middle cut, its end and [+] kept", l)
	}
	for _, l := range labels[1:] {
		if !strings.Contains(l, "…") || !strings.Contains(l, " #") {
			t.Fatalf("tabs %q: want the two long paths cut and numbered", labels)
		}
	}
}

// TestOpenFileInANewBufferAlwaysMakesOne: Open file in a new buffer… opens the file chosen in a
// buffer of its own though the buffer shown is an empty, new one, which stays; a plain File › Open
// after one that was closed unchosen fills the empty buffer, as before.
func TestOpenFileInANewBufferAlwaysMakesOne(t *testing.T) {
	d := startDaemon(t, map[string][]string{"kb": {"a.md", "aaa\n", "b.md", "bbb\n"}})
	r := attached(t, d)
	r.waitListed(t, 2)
	r.openByPicker(t, "a.md")
	r.waitFile(t, "a.md")
	count := func() int { return onLoop(r, func() int { return len(r.h.buffers) }) }
	onLoop(r, func() bool { r.h.newEmptyBuffer(); return true })
	empty := onLoop(r, func() *openBuffer { return r.h.active })
	before := count()
	runTabRow(t, r, empty, "buffer.tabs.open")
	r.s.WaitForText(t, "open a document")
	r.keys(t, decltest.Type("b.md")...)
	r.keys(t, enter())
	r.waitFile(t, "b.md")
	if n := count(); n != before+1 || onLoop(r, func() bool { return r.h.active == empty || !slices.Contains(r.h.buffers, empty) }) {
		t.Fatalf("Open file in a new buffer…: %d buffers (want %d), the empty one kept and not used", n, before+1)
	}

	// asked for, then closed unchosen: the next plain File › Open fills an empty buffer again
	onLoop(r, func() bool { r.h.activate(empty); r.h.openPickerNewBuffer(); return true })
	r.s.WaitForText(t, "open a document")
	r.keys(t, esc())
	r.s.WaitFor(t, "the picker closed", func(sc string) bool { return !strings.Contains(sc, "open a document") })
	before = count()
	d.write(t, "kb", "c.md", "ccc\n")
	r.waitListed(t, 3)
	r.openByPicker(t, "c.md")
	r.waitFile(t, "c.md")
	if n := count(); n != before || onLoop(r, func() bool { return r.h.active != empty }) {
		t.Fatalf("a plain File › Open: %d buffers (want %d), c.md in the empty buffer %v", n, before, onLoop(r, func() bool { return r.h.active == empty }))
	}
}

// TestACancelledHTMLQuestionTakesTheNewBufferRequestWithIt: Open file in a new buffer… choosing an
// HTML file asks how to open it; Cancel drops the open and its request for a new buffer with it,
// so a later ordinary open (an explorer's, a link's) fills an empty buffer shown, as it always does.
func TestACancelledHTMLQuestionTakesTheNewBufferRequestWithIt(t *testing.T) {
	d := startDaemon(t, map[string][]string{"kb": {"a.md", "aaa\n", "b.md", "bbb\n"}})
	r := attached(t, d)
	r.waitListed(t, 2)
	r.openByPicker(t, "a.md")
	r.waitFile(t, "a.md")
	onLoop(r, func() bool { r.h.newEmptyBuffer(); return true })
	empty := onLoop(r, func() *openBuffer { return r.h.active })
	onLoop(r, func() bool {
		r.h.openPickerNewBuffer()
		r.h.closeDialog(r.h.openPick.dialog)
		r.h.openPick.choose("page.html") // as Enter on it does
		return true
	})
	r.s.WaitForText(t, "open an HTML file")
	onLoop(r, func() bool { r.h.openHTMLAs("cancel"); return true })
	r.s.WaitFor(t, "the question closed", func(sc string) bool { return !strings.Contains(sc, "open an HTML file") })
	count := onLoop(r, func() int { return len(r.h.buffers) })
	onLoop(r, func() bool { r.h.openPath("b.md"); return true }) // an ordinary open, not File › Open's
	r.waitFile(t, "b.md")
	if n, in := onLoop(r, func() int { return len(r.h.buffers) }), onLoop(r, func() bool { return r.h.active == empty }); n != count || !in {
		t.Fatalf("after the cancelled question, an ordinary open made %d buffers (want %d), filled the empty one %v", n, count, in)
	}
}
