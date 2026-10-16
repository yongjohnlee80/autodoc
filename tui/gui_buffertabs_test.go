//go:build gui

package tui

import (
	"strings"
	"testing"

	guiwidget "github.com/yongjohnlee80/golib/gui/widget"
	tuicore "github.com/yongjohnlee80/golib/tui"
	tuidecl "github.com/yongjohnlee80/golib/tui/decl"
	"github.com/yongjohnlee80/golib/tui/widget"
)

// TestTheGUIEditorsTabsAreTheBuffers: under --gui's style the editor's title bar has a tab for each
// buffer, the shown one active, [+] on a modified one; a press on a tab shows its buffer; + opens
// File › Open.
func TestTheGUIEditorsTabsAreTheBuffers(t *testing.T) {
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
	press(spans[0].X + 1)
	r.waitBuffer(t, "a.md")
	if got := tabs(); got != "*a.md [+]|b.md" || !strings.Contains(r.editorText(), "Xaaa") {
		t.Fatalf("after a press on a.md's tab: tabs %q, text %q", got, r.editorText())
	}
	press(spans[2].X + 1)
	r.s.WaitForText(t, "open a document")
}
