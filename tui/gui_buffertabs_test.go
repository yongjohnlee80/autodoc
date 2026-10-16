//go:build gui

package tui

import (
	"regexp"
	"slices"
	"strconv"
	"strings"
	"testing"

	guiwidget "github.com/yongjohnlee80/golib/gui/widget"
	tuicore "github.com/yongjohnlee80/golib/tui"
	tuidecl "github.com/yongjohnlee80/golib/tui/decl"
	"github.com/yongjohnlee80/golib/tui/widget"
)

// TestTheGUIEditorsTabsAreTheBuffers: under --gui's style the editor's title bar has a tab for each
// buffer, the shown one active, [+] on a modified one; a press on a tab shows its buffer; + makes a
// new, empty buffer, shown.
func TestTheGUIEditorsTabsAreTheBuffers(t *testing.T) {
	d := startDaemon(t, map[string][]string{"kb": {"a.md", "aaa\n", "b.md", "bbb\n", "loooong-file-title-to-test-the-cut.md", "long\n"}})
	r := runTUI(t, NewSession(d.sock, nil), Options{ProgramOptions: []tuidecl.ProgramOption{tuidecl.WithStyle(GUIStyle(&NoteEmbeds{}))}})
	r.waitListed(t, 3)
	strip := onLoop(r, func() *widget.TabStrip { return r.h.editor.(*guiwidget.Editor).Tabs() })
	r.openByPicker(t, "a.md")
	r.waitFile(t, "a.md")
	r.typeInEditor(t, "X")
	r.waitTyped(t)
	r.openByPicker(t, "b.md")
	r.waitFile(t, "b.md")
	tabs := func() string {
		return onLoop(r, func() string {
			var out []string
			for _, tb := range strip.Tabs() {
				mark := ""
				if tb.ID == strip.Active() {
					mark = "*"
				}
				out = append(out, mark+tb.Label)
			}
			return strings.Join(out, "|")
		})
	}
	// the first buffer was the untitled draft, made the file a.md opened into
	r.s.WaitFor(t, "the buffers as tabs", func(string) bool { return tabs() == "a.md [+]|*b.md" })

	spans := onLoop(r, func() []widget.TabSpan { return strip.TabSpans() })
	if len(spans) != 4 {
		t.Fatalf("spans %v: want two tabs, + and ≡", spans)
	}
	press := func(x int) {
		onLoop(r, func() bool {
			return strip.HandleEvent(tuicore.MouseEvent{Kind: tuicore.MousePress, Button: tuicore.MouseLeft, X: x, Y: 0})
		})
	}
	press(spans[0].X + spans[0].W - 2) // on a.md's name: its start is its ×
	r.waitBuffer(t, "a.md")
	if got := tabs(); got != "*a.md [+]|b.md" || !strings.Contains(r.editorText(), "Xaaa") {
		t.Fatalf("after a press on a.md's tab: tabs %q, text %q", got, r.editorText())
	}
	press(spans[2].X + 1) // +
	r.s.WaitFor(t, "a new, empty buffer shown", func(string) bool {
		return onLoop(r, func() bool { return len(r.h.buffers) == 3 && !r.h.file.open && r.h.core.Value() == "" })
	})
	if got := tabs(); got != "a.md [+]|b.md|*"+untitled {
		t.Fatalf("after +: tabs %q", got)
	}
}

// TestTheGUITabsKeepOneWidth: a buffer tab is never wider than bufferTabWidth, a longer name cut in
// its middle, and keeps its width however many tabs are open and however wide the window is: tabs
// that do not fit scroll between ← and →, none re-fitted (Johno: consistent, never re-flowed). The
// window is never narrower than the TUI's 80 columns, under which nothing is laid out.
func TestTheGUITabsKeepOneWidth(t *testing.T) {
	long := []string{"a-long-file-name-for-the-first-tab.md", "another-long-file-name-for-a-tab.md", "a-third-long-file-name-to-overflow.md"}
	d := startDaemon(t, map[string][]string{"kb": {long[0], "a\n", long[1], "b\n", long[2], "c\n"}})
	r := runTUI(t, NewSession(d.sock, nil), Options{ProgramOptions: []tuidecl.ProgramOption{tuidecl.WithStyle(GUIStyle(&NoteEmbeds{}))}})
	r.waitListed(t, 3)
	r.s.Backend.InjectResize(220, 30)
	strip := onLoop(r, func() *widget.TabStrip { return r.h.editor.(*guiwidget.Editor).Tabs() })
	// the tabs' widths, told from the strip's other cells by place, not width: the row is
	// [←] tab… [→] + ≡, and a tab the window cuts can be as narrow as an arrow
	look := func(arrows bool) []int {
		spans := onLoop(r, func() []widget.TabSpan { return strip.TabSpans() })
		lead, trail := 0, 2 // + ≡
		if arrows {
			lead, trail = 1, 3 // ←, and → + ≡
		}
		if len(spans) <= lead+trail {
			return nil
		}
		for _, sp := range append(spans[:lead:lead], spans[len(spans)-trail:]...) {
			if sp.W > 3 {
				return nil // not this layout: a tab where an arrow, + or ≡ should be
			}
		}
		var tabs []int
		for _, sp := range spans[lead : len(spans)-trail] {
			tabs = append(tabs, sp.W)
		}
		return tabs
	}
	for _, f := range long {
		r.openByPicker(t, f)
		r.waitFile(t, f)
	}
	var wide []int
	r.s.WaitFor(t, "wide: three tabs, each cut to the cap, no arrows", func(string) bool {
		tabs := look(false)
		wide = tabs
		return len(tabs) == 3 && slices.Max(tabs) <= bufferTabWidth+2 && slices.Min(tabs) == slices.Max(tabs)
	})
	r.s.Backend.InjectResize(100, 30)
	r.s.WaitFor(t, "narrow: the same widths, scrolling between the arrows", func(sc string) bool {
		tabs := look(true)
		if !strings.Contains(sc, "←") || !strings.Contains(sc, "→") || len(tabs) == 0 {
			return false
		}
		// every tab the window shows whole is as wide as before; the one cut at the window's edge,
		// or the one before the first in the cells left over, is narrower, as the window draws it
		whole := 0
		for _, w := range tabs {
			if w > wide[0] {
				return false
			}
			if w == wide[0] {
				whole++
			}
		}
		return whole > 0
	})
	// cut in the middle, by AutoDoc: the title row (the one with the arrows) keeps a name's end, its
	// kind after the ellipsis, as a cut at its end would not
	r.s.WaitFor(t, "a tab cut in its middle", func(sc string) bool {
		for _, line := range strings.Split(sc, "\n") {
			if strings.Contains(line, "←") && middleCut.MatchString(line) {
				return true
			}
		}
		return false
	})
}

// TestLInTheGUIScrollsTheStripToTheTab: tabs overflowing a narrow bar, the last shown; L wraps to
// the first, and the strip scrolls back to show it (bufferChord, then the strip's reveal).
func TestLInTheGUIScrollsTheStripToTheTab(t *testing.T) {
	long := []string{"a-long-file-name-for-the-first-tab.md", "another-long-file-name-for-a-tab.md", "a-third-long-file-name-to-overflow.md"}
	d := startDaemon(t, map[string][]string{"kb": {long[0], "a\n", long[1], "b\n", long[2], "c\n"}})
	r := runTUI(t, NewSession(d.sock, nil), Options{ProgramOptions: []tuidecl.ProgramOption{tuidecl.WithStyle(GUIStyle(&NoteEmbeds{}))}})
	r.waitListed(t, 3)
	r.s.Backend.InjectResize(220, 30)
	strip := onLoop(r, func() *widget.TabStrip { return r.h.editor.(*guiwidget.Editor).Tabs() })
	for _, f := range long {
		r.openByPicker(t, f)
		r.waitFile(t, f)
	}
	r.s.Backend.InjectResize(100, 30) // the TUI lays nothing out under 80 columns
	first, last := onLoop(r, func() string { return strconv.Itoa(r.h.buffers[0].n) }), onLoop(r, func() string { return strconv.Itoa(r.h.buffers[2].n) })
	r.s.WaitFor(t, "narrow: the last tab active and shown, the strip scrolled to it", func(string) bool {
		return onLoop(r, func() bool { return strip.Active() == last && strip.ActiveTabIndex() >= 0 })
	})
	r.keys(t, esc(), key('L'))
	r.s.WaitFor(t, "L: the first tab active, and shown", func(string) bool {
		return onLoop(r, func() bool { return strip.Active() == first && strip.ActiveTabIndex() >= 0 })
	})
}

// middleCut is a tab's name cut in its middle: an ellipsis, then the name's end, its kind kept.
var middleCut = regexp.MustCompile(`…[^ ×…]*\.md`)

// TestAGUITabsCloseButton: a press on a tab's × closes its buffer: at once when it is clean, after
// the unsaved question when it is modified.
func TestAGUITabsCloseButton(t *testing.T) {
	d := startDaemon(t, map[string][]string{"kb": {"a.md", "aaa\n", "b.md", "bbb\n"}})
	r := runTUI(t, NewSession(d.sock, nil), Options{ProgramOptions: []tuidecl.ProgramOption{tuidecl.WithStyle(GUIStyle(&NoteEmbeds{}))}})
	r.waitListed(t, 2)
	strip := onLoop(r, func() *widget.TabStrip { return r.h.editor.(*guiwidget.Editor).Tabs() })
	r.openByPicker(t, "a.md")
	r.waitFile(t, "a.md")
	r.typeInEditor(t, "X")
	r.waitTyped(t)
	r.openByPicker(t, "b.md")
	r.waitFile(t, "b.md")
	pressX := func(i int) {
		t.Helper()
		onLoop(r, func() bool {
			s := strip.TabSpans()[i]
			return strip.HandleEvent(tuicore.MouseEvent{Kind: tuicore.MousePress, Button: tuicore.MouseLeft, X: s.X + 1, Y: 0})
		})
	}
	pressX(1) // b.md's ×: clean, closed at once
	r.s.WaitFor(t, "b.md closed", func(string) bool { return onLoop(r, func() bool { return r.h.bufferOf(fileRef{"kb", "b.md"}) == nil }) })
	pressX(0) // a.md's ×: modified, asked first
	r.s.WaitForText(t, "unsaved file")
	if onLoop(r, func() bool { return r.h.bufferOf(fileRef{"kb", "a.md"}) == nil }) {
		t.Fatal("a.md, modified, closed without the question")
	}
}
