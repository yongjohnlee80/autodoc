//go:build !gui

package tui

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	tuicore "github.com/yongjohnlee80/golib/tui"
	"github.com/yongjohnlee80/golib/tui/decl/decltest"
	"github.com/yongjohnlee80/golib/tui/widget/editor"
)

// buffertabs_terminal_test.go: the buffer tabs on the terminal's page, at its frame's title row's
// left (buffertabs.go), where every panel carries its tabs: the window's model, drawn in cells.

// frameTab is where the tab labelled label is drawn on the page's frame's title row: the column of
// its × and of its label, and the row; ok is false while it isn't drawn there.
func (r *running) frameTab(label string) (closeX, x, y int, ok bool) {
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

// The terminal shows the buffers as tabs at the left of the page's frame's title row once two are
// open (one shows none), in opening order, as every panel carries its tabs: the file's name leaves
// the row (the shown tab names it, and the status line), its badges and breadcrumb staying after
// the tabs, and no tab is on any bottom border. A press on a tab shows its buffer, and a switch made
// elsewhere (SPC b 2) moves the tabs' active one with it.
func TestTheTerminalShowsTheBuffersAsTabsOnThePagesTitleRow(t *testing.T) {
	d := startDaemon(t, map[string][]string{"kb": {"a.md", "a\n", "b.md", "b\n"}})
	r := attached(t, d)
	r.waitListed(t, 2)
	// one buffer: no tabs, the page as it was before there were tabs (its name is the title row's)
	r.openByPicker(t, "a.md")
	r.waitFile(t, "a.md")
	if n := onLoop(r, func() int { return len(r.h.buffers) }); n != 1 {
		t.Fatalf("%d buffers after one open: want 1", n)
	}
	if _, _, _, ok := r.frameTab("a.md"); ok {
		t.Fatalf("a tab with one buffer open:\n%s", r.s)
	}
	r.openByPicker(t, "b.md")
	r.waitFile(t, "b.md")
	r.s.WaitFor(t, "a.md's and b.md's tabs on the title row", func(string) bool {
		_, _, ya, oka := r.frameTab("a.md")
		_, _, yb, okb := r.frameTab("b.md")
		return oka && okb && ya == yb
	})
	_, ax, y, _ := r.frameTab("a.md")
	_, bx, _, _ := r.frameTab("b.md")
	if ax >= bx {
		t.Fatalf("a.md's tab at %d, b.md's at %d: want them in opening order", ax, bx)
	}
	for _, row := range strings.Split(r.s.String(), "\n") {
		if strings.Contains(row, "└") && strings.Contains(row, "× ") {
			t.Fatalf("a tab on a bottom border %q: want the tabs on the page's title row alone", row)
		}
	}
	if title := onLoop(r, func() string { v, _ := r.h.p.Tree().Source("App.fileTitle"); return v.Raw }); strings.Contains(title, ".md") {
		t.Fatalf("the title with the tabs shown %q: want no file name (the shown tab names it)", title)
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
	r.s.WaitFor(t, "b.md's tab", func(string) bool { _, _, _, ok := r.frameTab("b.md"); return ok })
	cx, _, y, _ := r.frameTab("b.md")
	r.press(t, cx, y)
	r.s.WaitFor(t, "b.md closed, a.md shown", func(string) bool {
		_, _, _, still := r.frameTab("b.md")
		return !still && r.file().path == "a.md"
	})
	if n := onLoop(r, func() int { return len(r.h.buffers) }); n != 1 {
		t.Fatalf("%d buffers after closing b.md: want 1", n)
	}
}

// In a narrow terminal the tabs scroll between the arrows in the room the title row has, the shown
// buffer's tab in view, Raw | Rendered keeping its place after them, and no tab is wider than
// terminalTabWidth.
func TestTheTitleRowTabsScrollInANarrowRoom(t *testing.T) {
	files := []string{}
	names := []string{"one.md", "two.md", "three.md", "four.md", "five.md", "a-rather-long-name-for-a-tab.md"}
	for _, n := range names {
		files = append(files, n, n+"\n")
	}
	d := startDaemon(t, map[string][]string{"kb": files})
	// 80 columns: AutoDoc's least (narrower shows "Screen too small"); six tabs of up to 20 cells
	// still overflow it
	r := runTUISized(t, NewSession(d.sock, nil), Options{}, 80, 20)
	r.waitAttached(t, "kb")
	r.waitListed(t, len(names))
	for _, p := range names {
		onLoop(r, func() bool { r.h.openPath(p); return true })
		r.waitFile(t, p)
	}
	r.s.WaitFor(t, "the tabs scrolled, the shown one's in view", func(screen string) bool {
		for _, row := range strings.Split(screen, "\n") {
			if strings.Contains(row, "┌") && strings.Contains(row, "→") && strings.Contains(row, "…") && !strings.Contains(row, "× one.md") &&
				strings.Index(row, "→") < strings.Index(row, "[ Raw | Rendered ]") {
				return true
			}
		}
		return false
	})
	label := onLoop(r, func() string { return r.h.bufferTabLabel(r.h.active, terminalTabWidth-2) })
	if w := tuicore.StringWidth(label); w > terminalTabWidth-2 {
		t.Fatalf("the long name's tab %q is %d cells: want at most %d", label, w, terminalTabWidth-2)
	}
	if _, _, _, ok := r.frameTab(strings.TrimSuffix(label, " [+]")); !ok {
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
			_, bx, by, okb := r.frameTab("b.md")
			_, ax, ay, oka := r.frameTab("a.md")
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
		dumpScreen(t, r, "buffer-tabs-"+theme)
	}
}

// With the tabs shown, Raw | Rendered keeps the title row's right, after them, and takes its presses
// as it does without tabs: Raw opens the text, Rendered the page again.
func TestRawRenderedStaysClickableBesideTheTitleRowTabs(t *testing.T) {
	d := startDaemon(t, map[string][]string{"kb": {"a.md", "# A\n\nalpha\n", "b.md", "# B\n\nbeta\n"}})
	r := attached(t, d)
	r.waitListed(t, 2)
	for _, p := range []string{"a.md", "b.md"} {
		r.openByPicker(t, p)
		r.waitFile(t, p)
	}
	r.s.WaitFor(t, "b.md's page", func(string) bool { return shownView(r) == editor.ViewRendered })
	var col, row int
	r.s.WaitFor(t, "the tabs, then the switch, on the title row", func(string) bool {
		var ok bool
		if col, row, ok = find(r.s.Backend.Snapshot(), "[ Raw | Rendered ]"); !ok {
			return false
		}
		_, bx, by, okb := r.frameTab("b.md")
		return okb && by == row && bx < col
	})
	if strings.Count(r.s.String(), "[ Raw | Rendered ]") != 1 {
		t.Fatalf("the switch shows more than once:\n%s", r.s)
	}
	r.press(t, col+2, row)
	r.s.WaitFor(t, "Raw after a press on Raw", func(string) bool { return shownView(r) == editor.ViewRaw })
	r.press(t, col+len("[ Raw ")+3, row)
	r.s.WaitFor(t, "the page after a press on Rendered", func(string) bool { return shownView(r) == editor.ViewRendered })
	if _, _, _, ok := r.frameTab("b.md"); !ok || r.file().path != "b.md" {
		t.Fatalf("the switch's presses moved the tabs or the buffer:\n%s", r.s)
	}
}

// dumpScreen writes the screen, in its colours (SGR), to $AUTODOC_SCREEN_DUMP/<name>.ansi and as
// text to <name>.txt, when that variable names a directory: a look at a layout, for a person; no
// cell reads it.
func dumpScreen(t *testing.T, r *running, name string) {
	t.Helper()
	dir := os.Getenv("AUTODOC_SCREEN_DUMP")
	if dir == "" {
		return
	}
	sgr := func(c tuicore.CellColor, fg bool) string {
		switch c.Kind {
		case tuicore.CellColorRGB:
			if fg {
				return fmt.Sprintf("38;2;%d;%d;%d", c.R, c.G, c.B)
			}
			return fmt.Sprintf("48;2;%d;%d;%d", c.R, c.G, c.B)
		case tuicore.CellColorANSI256, tuicore.CellColorANSI:
			if fg {
				return fmt.Sprintf("38;5;%d", c.Index)
			}
			return fmt.Sprintf("48;5;%d", c.Index)
		}
		if fg {
			return "39"
		}
		return "49"
	}
	var b strings.Builder
	for _, line := range r.s.Backend.Snapshot() {
		for _, c := range line {
			if c.Continuation() {
				continue
			}
			bold := ""
			if c.Attrs.Mask&tuicore.AttrBold != 0 {
				bold = ";1"
			}
			text := c.Content
			if text == "" {
				text = " "
			}
			fmt.Fprintf(&b, "\x1b[0;%s;%s%sm%s", sgr(c.Attrs.FG, true), sgr(c.Attrs.BG, false), bold, text)
		}
		b.WriteString("\x1b[0m\n")
	}
	for ext, body := range map[string]string{".ansi": b.String(), ".txt": r.s.String() + "\n"} {
		if err := os.WriteFile(filepath.Join(dir, name+ext), []byte(body), 0o644); err != nil {
			t.Errorf("the screen dump: %v", err)
		}
	}
}

// On a terminal whose panels take plain frame drags, as AutoDoc's does, a press on a tab, on the
// frame's title row, is the tab's: it shows its buffer, and the page stays where it is.
func TestATabTakesItsPressOnAFrameDragTerminal(t *testing.T) {
	d := startDaemonWith(t, "", map[string][]string{"kb": {"a.md", "a\n", "b.md", "b\n"}}, daemonOpts{})
	h := newHost(NewSession(d.sock, nil), Options{seams: seamsFor(t)})
	s := decltest.RunOn(t, tuicore.NewTestBackend(100, 30, tuicore.WithTestCapabilities(tuicore.Capabilities{FrameDrag: true})), h.attach, h.options(Options{})...)
	t.Cleanup(h.cancel)
	r := &running{h: h, s: s}
	r.connected(t)
	r.waitWithin(t, "the notes listed", daemonWait, func() bool { return len(r.listed()) > 1 })
	for _, p := range []string{"a.md", "b.md"} {
		onLoop(r, func() bool { r.h.openPath(p); return true })
		r.waitFile(t, p)
	}
	var ax, y0 int
	r.s.WaitFor(t, "a.md's tab on the title row", func(string) bool {
		var ok bool
		_, ax, y0, ok = r.frameTab("a.md")
		return ok
	})
	r.press(t, ax, y0)
	r.s.WaitFor(t, "a.md shown by its tab", func(string) bool { return r.file().path == "a.md" })
	if _, x, y, ok := r.frameTab("a.md"); !ok || x != ax || y != y0 {
		t.Fatalf("a press on a tab moved the page (its tab from %d,%d to %d,%d):\n%s", ax, y0, x, y, r.s)
	}
}
