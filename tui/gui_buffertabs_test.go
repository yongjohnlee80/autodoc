//go:build gui

package tui

import (
	"regexp"
	"slices"
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

// TestTheGUITabsTakeTheBarsRoom: the buffer tabs take the title bar's room up to Raw | Rendered.
// Wide, a long name shows whole; narrowed, every tab is cut in its middle to fit, never under
// bufferTabWidth cells, and under that they scroll between ← and →. (was: two thirds of the bar, and
// every tab cut at bufferTabWidth however much room was left)
func TestTheGUITabsTakeTheBarsRoom(t *testing.T) {
	long := []string{"a-long-file-name-for-the-first-tab.md", "another-long-file-name-for-a-tab.md", "a-third-long-file-name-to-overflow.md"}
	d := startDaemon(t, map[string][]string{"kb": {long[0], "a\n", long[1], "b\n", long[2], "c\n"}})
	r := runTUI(t, NewSession(d.sock, nil), Options{ProgramOptions: []tuidecl.ProgramOption{tuidecl.WithStyle(GUIStyle(&NoteEmbeds{}))}})
	r.waitListed(t, 3)
	r.s.Backend.InjectResize(220, 30)
	strip := onLoop(r, func() *widget.TabStrip { return r.h.editor.(*guiwidget.Editor).Tabs() })
	// the tabs' widths, and the arrows', +'s and ≡'s count: those are 3 cells, a tab more
	look := func() (tabs []int, small int) {
		for _, sp := range onLoop(r, func() []widget.TabSpan { return strip.TabSpans() }) {
			if sp.W > 3 {
				tabs = append(tabs, sp.W)
			} else {
				small++
			}
		}
		return tabs, small
	}
	whole := func(name string) int { return len(name) + 4 } // " × " before it, a cell after
	for _, f := range long[:2] {
		r.openByPicker(t, f)
		r.waitFile(t, f)
	}
	r.s.WaitFor(t, "wide: both long names whole", func(string) bool {
		tabs, small := look()
		return len(tabs) == 2 && small == 2 && slices.Contains(tabs, whole(long[0])) && slices.Contains(tabs, whole(long[1]))
	})

	r.s.Backend.InjectResize(60, 30)
	r.openByPicker(t, long[2])
	r.waitFile(t, long[2])
	r.s.WaitFor(t, "narrow: cut, and scrolling between the arrows", func(string) bool {
		tabs, small := look()
		if small != 4 || len(tabs) == 0 {
			return false
		}
		for _, w := range tabs {
			if w >= whole(long[2]) || w < bufferTabWidth+2 { // cut, but never under the floor (and its ×)
				return false
			}
		}
		return true
	})
	// cut in the middle, by AutoDoc's fit: the title row (the one with the arrows) keeps a name's
	// end, its kind after the ellipsis, as a cut at its end would not
	r.s.WaitFor(t, "a tab cut in its middle", func(sc string) bool {
		for _, line := range strings.Split(sc, "\n") {
			if strings.Contains(line, "←") && middleCut.MatchString(line) {
				return true
			}
		}
		return false
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
