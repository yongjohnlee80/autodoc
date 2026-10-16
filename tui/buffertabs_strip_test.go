package tui

import (
	"slices"
	"testing"

	"github.com/yongjohnlee80/golib/tui"
	"github.com/yongjohnlee80/golib/tui/widget"
)

// buffertabs_strip_test.go: feedBufferTabs, which the GUI calls with its editor's strip, on a strip
// of the TUI's own (the TUI's editor has none): what the strip is given and what its presses do.

// TestTheStripIsFedTheBuffers: a tab per buffer, by its number, labelled as bufferTabLabel cuts it,
// the shown buffer's active; a press on a tab shows its buffer, on its × closes it, and + makes a
// new one; fed again, the strip keeps the wiring it has, and a new strip is wired.
func TestTheStripIsFedTheBuffers(t *testing.T) {
	long := "a-long-file-name-for-the-strip-to-cut.md"
	d := startDaemon(t, map[string][]string{"kb": {"a.md", "a\n", "b.md", "b\n", long, "l\n"}})
	r := attached(t, d)
	r.waitListed(t, 3)
	for _, p := range []string{"a.md", "b.md", long} {
		r.openByPicker(t, p)
		r.waitFile(t, p)
	}
	strip := widget.NewTabStrip()
	// fed is the strip's tabs and active one after a feed, and their spans on a bar wide enough for all
	type fed struct {
		tabs   []widget.StripTab
		active string
		spans  []widget.TabSpan
	}
	feed := func(s *widget.TabStrip) fed {
		return onLoop(r, func() fed {
			r.h.feedBufferTabs(s)
			s.Layout(tui.Constraints{MaxW: 200, MaxH: 1})
			return fed{s.Tabs(), s.Active(), s.TabSpans()}
		})
	}
	press := func(s *widget.TabStrip, x int) {
		onLoop(r, func() bool {
			return s.HandleEvent(tui.MouseEvent{Kind: tui.MousePress, Button: tui.MouseLeft, X: x, Y: 0})
		})
	}
	shown := func() string { return r.file().path }
	waitShown := func(p string) {
		t.Helper()
		r.s.WaitFor(t, p+" shown", func(string) bool { return shown() == p })
	}

	got := feed(strip)
	want := onLoop(r, func() []widget.StripTab {
		var out []widget.StripTab
		for _, b := range r.h.buffers {
			out = append(out, widget.StripTab{ID: tabID(b), Label: r.h.bufferTabLabel(b, bufferTabWidth-2)})
		}
		return out
	})
	if !slices.Equal(got.tabs, want) || got.active != want[2].ID {
		t.Fatalf("fed tabs %q, active %q: want %q, the last opened active", got.tabs, got.active, want)
	}
	if l := got.tabs[2].Label; l == long || len([]rune(l)) > bufferTabWidth-2 {
		t.Fatalf("the long name's tab is %q: want it cut to %d cells", l, bufferTabWidth-2)
	}
	// the strip's row: a.md, b.md, the long one, then + and ≡; a tab's × is its first cells
	if len(got.spans) != 5 {
		t.Fatalf("spans %v: want three tabs, + and ≡", got.spans)
	}

	a := got.spans[0]
	press(strip, a.X+a.W-1)
	waitShown("a.md")
	if got := feed(strip); got.active != want[0].ID {
		t.Fatalf("after a press on a.md's tab: active %q, want %q", got.active, want[0].ID)
	}

	b := got.spans[1]
	press(strip, b.X)
	r.s.WaitFor(t, "b.md closed by its ×", func(string) bool {
		return onLoop(r, func() bool {
			return len(r.h.buffers) == 2 && !slices.ContainsFunc(r.h.buffers, func(o *openBuffer) bool { return tabID(o) == want[1].ID })
		})
	})
	got = feed(strip)
	if len(got.tabs) != 2 || len(got.spans) != 4 {
		t.Fatalf("after b.md's ×: tabs %q, spans %v", got.tabs, got.spans)
	}

	plus := got.spans[2]
	press(strip, plus.X+plus.W/2)
	r.s.WaitFor(t, "a new, empty buffer from +", func(string) bool {
		return onLoop(r, func() bool { return len(r.h.buffers) == 3 && !r.h.file.open })
	})

	// fed again, the strip is not wired again: a host's own handler stays
	var chosen []string
	onLoop(r, func() bool { strip.SetOnSelect(func(id string) { chosen = append(chosen, id) }); return true })
	got = feed(strip)
	press(strip, got.spans[0].X+got.spans[0].W-1)
	if len(chosen) != 1 || chosen[0] != want[0].ID || shown() == "a.md" {
		t.Fatalf("a press on a strip fed twice: chosen %q, shown %q: want the host's handler, not a re-wiring", chosen, shown())
	}

	// a new strip, a new editor's after a reload, is wired
	fresh := widget.NewTabStrip()
	got = feed(fresh)
	if onLoop(r, func() bool { return r.h.tabStrip != fresh }) {
		t.Fatal("a new strip was not taken as the editor's")
	}
	press(fresh, got.spans[0].X+got.spans[0].W-1)
	waitShown("a.md")
}
