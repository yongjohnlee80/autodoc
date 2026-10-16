package tui

import (
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
					out = append(out, r.h.bufferTabLabel(b))
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
	if rows := onLoop(r, func() []widget.MenuItemModel { return r.h.bufferTabRows("") }); len(rows) != 3 {
		t.Fatalf("the menu off a tab: %d rows, want New buffer, Open file and Go to", len(rows))
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
			out = append(out, r.h.bufferTabLabel(b))
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
