package tui

import (
	"strings"
	"testing"

	tuicore "github.com/yongjohnlee80/golib/tui"
)

// TestTheBuffersHaveKeysAndMenuEntries: SPC B opens the Buffers picker, SPC C closes the buffer
// shown; Go › Buffers… and File › Close buffer are on the menus.
func TestTheBuffersHaveKeysAndMenuEntries(t *testing.T) {
	d := startDaemon(t, map[string][]string{"kb": {"a.md", "aaa\n", "b.md", "bbb\n"}})
	r := attached(t, d)
	r.waitListed(t, 2)
	for _, p := range []string{"a.md", "b.md"} {
		r.openByPicker(t, p)
		r.waitFile(t, p)
	}
	r.leader(t, 'B')
	r.s.WaitForText(t, "switch buffer")
	r.keys(t, esc())
	r.s.WaitFor(t, "the picker closed", func(sc string) bool { return !strings.Contains(sc, "switch buffer") })

	r.leader(t, 'C')
	r.s.WaitFor(t, "b.md closed", func(string) bool { return onLoop(r, func() bool { return bufferOnLoop(r, "b.md") == nil }) })
	if r.file().path != "a.md" {
		t.Fatalf("after SPC C: %q shown, want a.md", r.file().path)
	}

	r.leader(t, 'm')   // the menu bar, focused
	r.keys(t, enter()) // File
	r.s.WaitForText(t, "Close buffer")
	r.keys(t, tuicore.KeyEvent{Kind: tuicore.KeyPress, Code: tuicore.KeyRight}) // Go
	r.s.WaitForText(t, "Buffers…")
}
