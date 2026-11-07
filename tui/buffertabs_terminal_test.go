//go:build !gui

package tui

import (
	"strings"
	"testing"

	tuicore "github.com/yongjohnlee80/golib/tui"
)

// buffertabs_terminal_test.go: the buffer tabs on the terminal's page, at the left of its frame's
// title row (buffertabs.go): the window's model, drawn in cells.

// titleTab is where the tab labelled label is drawn on the page's frame's title row: the column of
// its × and of its label, and the row; ok is false while it isn't drawn there.
func (r *running) titleTab(label string) (closeX, x, y int, ok bool) {
	for y, row := range strings.Split(r.s.String(), "\n") {
		if !strings.Contains(row, "┌") {
			continue
		}
		if j := strings.Index(row, "× "+label); j >= 0 {
			cx := len([]rune(row[:j]))
			return cx, cx + 2, y, true
		}
	}
	return 0, 0, 0, false
}

// press is a left press and release at x, y.
func (r *running) press(t *testing.T, x, y int) {
	t.Helper()
	r.keys(t, tuicore.MouseEvent{Kind: tuicore.MousePress, Button: tuicore.MouseLeft, X: x, Y: y},
		tuicore.MouseEvent{Kind: tuicore.MouseRelease, Button: tuicore.MouseLeft, X: x, Y: y})
}

// The terminal shows the buffers as tabs at the left of the page's frame's title row, in opening
// order, the file's title after them: a press on a tab shows its buffer, and H moves to the one
// before, the tabs following it.
func TestTheTerminalShowsTheBuffersAsTitleRowTabs(t *testing.T) {
	d := startDaemon(t, map[string][]string{"kb": {"a.md", "a\n", "b.md", "b\n"}})
	r := attached(t, d)
	r.waitListed(t, 2)
	for _, p := range []string{"a.md", "b.md"} {
		r.openByPicker(t, p)
		r.waitFile(t, p)
	}
	r.s.WaitFor(t, "a.md's and b.md's tabs on the title row", func(string) bool {
		_, _, ya, oka := r.titleTab("a.md")
		_, _, yb, okb := r.titleTab("b.md")
		return oka && okb && ya == yb
	})
	_, ax, y, _ := r.titleTab("a.md")
	_, bx, _, _ := r.titleTab("b.md")
	if ax >= bx {
		t.Fatalf("a.md's tab at %d, b.md's at %d: want them in opening order", ax, bx)
	}
	r.press(t, ax, y)
	r.s.WaitFor(t, "a.md shown by its tab", func(string) bool { return r.file().path == "a.md" })
	// switched elsewhere (SPC b 2, the picker, H and L in Vim's keys), the tabs follow
	onLoop(r, func() bool { r.h.showBufferAt(2); return true })
	r.s.WaitFor(t, "b.md shown, its tab active", func(string) bool {
		return r.file().path == "b.md" && onLoop(r, func() bool { return r.h.bufferStrip().Active() == tabID(r.h.active) })
	})
}

// A press on the shown buffer's × closes it: its tab goes, and the buffer before it is shown.
func TestATitleRowTabsCloseClosesTheShownBuffer(t *testing.T) {
	d := startDaemon(t, map[string][]string{"kb": {"a.md", "a\n", "b.md", "b\n"}})
	r := attached(t, d)
	r.waitListed(t, 2)
	for _, p := range []string{"a.md", "b.md"} {
		r.openByPicker(t, p)
		r.waitFile(t, p)
	}
	r.s.WaitFor(t, "b.md's tab", func(string) bool { _, _, _, ok := r.titleTab("b.md"); return ok })
	cx, _, y, _ := r.titleTab("b.md")
	r.press(t, cx, y)
	r.s.WaitFor(t, "b.md closed, a.md shown", func(string) bool {
		_, _, _, still := r.titleTab("b.md")
		return !still && r.file().path == "a.md"
	})
	if n := onLoop(r, func() int { return len(r.h.buffers) }); n != 1 {
		t.Fatalf("%d buffers after closing b.md: want 1", n)
	}
}

// In a narrow terminal the tabs scroll between the arrows in the room the title row has, the shown
// buffer's tab in view, and no tab is wider than terminalTabWidth.
func TestTheTitleRowTabsScrollInANarrowRoom(t *testing.T) {
	files := []string{}
	names := []string{"one.md", "two.md", "three.md", "four.md", "five.md", "a-rather-long-name-for-a-tab.md"}
	for _, n := range names {
		files = append(files, n, n+"\n")
	}
	d := startDaemon(t, map[string][]string{"kb": files})
	r := runTUISized(t, NewSession(d.sock, nil), Options{}, 70, 20)
	r.ready(t) // 70 columns leave the status line no room for the workspace's name
	r.waitListed(t, len(names))
	for _, p := range names {
		onLoop(r, func() bool { r.h.openPath(p); return true })
		r.waitFile(t, p)
	}
	r.s.WaitFor(t, "the tabs scrolled, the shown one's in view", func(screen string) bool {
		for _, row := range strings.Split(screen, "\n") {
			if strings.Contains(row, "┌") && strings.Contains(row, "→") && strings.Contains(row, "…") && !strings.Contains(row, "× one.md") {
				return true
			}
		}
		return false
	})
	label := onLoop(r, func() string { return r.h.bufferTabLabel(r.h.active, terminalTabWidth-2) })
	if w := tuicore.StringWidth(label); w > terminalTabWidth-2 {
		t.Fatalf("the long name's tab %q is %d cells: want at most %d", label, w, terminalTabWidth-2)
	}
	if _, _, _, ok := r.titleTab(strings.TrimSuffix(label, " [+]")); !ok {
		t.Fatalf("the shown buffer's tab %q isn't on the title row:\n%s", label, r.s)
	}
}

// The tabs read in sepia and in dark: the shown buffer's tab stands apart from the others, its text
// apart from its own paper, and each theme draws it in its own colours.
func TestTheTitleRowTabsReadInSepiaAndDark(t *testing.T) {
	d := startDaemon(t, map[string][]string{"kb": {"a.md", "a\n", "b.md", "b\n"}})
	r := runTUISized(t, NewSession(d.sock, nil), Options{}, 100, 24)
	r.waitAttached(t, "kb")
	for _, p := range []string{"a.md", "b.md"} {
		onLoop(r, func() bool { r.h.openPath(p); return true })
		r.waitFile(t, p)
	}
	var looks []tuicore.CellAttrs
	for _, theme := range []string{"sepia", "dark"} {
		onLoop(r, func() bool { r.h.switchTheme(theme); return true })
		r.s.WaitFor(t, theme, func(string) bool { return onLoop(r, func() string { return r.h.theme }) == theme })
		var active, other tuicore.CellAttrs
		r.s.WaitFor(t, theme+": the shown tab apart from the other, in this theme's colours", func(string) bool {
			_, bx, by, okb := r.titleTab("b.md")
			_, ax, ay, oka := r.titleTab("a.md")
			if !okb || !oka {
				return false
			}
			snap := r.s.Backend.Snapshot()
			active, other = snap[by][bx].Attrs, snap[ay][ax].Attrs
			return active.BG != other.BG && active.FG != active.BG && other.FG != other.BG &&
				(len(looks) == 0 || active != looks[0])
		})
		looks = append(looks, active)
		t.Logf("%s: the shown tab %+v, the other %+v", theme, active, other)
	}
}
