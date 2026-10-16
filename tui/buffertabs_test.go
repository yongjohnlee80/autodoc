package tui

import (
	"strconv"
	"strings"
	"testing"

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
	if rows := onLoop(r, func() []widget.MenuItemModel { return r.h.bufferTabRows("") }); len(rows) != 1 {
		t.Fatalf("the menu off a tab: %d rows, want Go to alone", len(rows))
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
