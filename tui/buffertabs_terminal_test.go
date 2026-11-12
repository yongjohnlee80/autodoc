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

// buffertabs_terminal_test.go: the buffer tabs on the terminal's page, on the first row inside its
// frame, under its title row (buffertabs.go), as the agent and terminal panels carry their tabs:
// the window's model, drawn in cells.

// frameTab is where the tab labelled label is drawn on the page's row of tabs, the row under its
// frame's title row: the column of its × and of its label, and the row; ok is false while it isn't
// drawn there.
func (r *running) frameTab(label string) (closeX, x, y int, ok bool) {
	rows := strings.Split(r.s.String(), "\n")
	for y, row := range rows {
		if y == 0 || !strings.Contains(rows[y-1], "┌") {
			continue
		}
		if j := strings.Index(row, "× "+label); j >= 0 {
			cx := len([]rune(row[:j]))
			return cx, cx + 2, y, true
		}
	}
	return 0, 0, 0, false
}

// screenRow is the screen's row y, "" past its last.
func (r *running) screenRow(y int) string {
	if rows := strings.Split(r.s.String(), "\n"); y >= 0 && y < len(rows) {
		return rows[y]
	}
	return ""
}

// noTabOnABorder fails t when a tab (its ×) is drawn on any frame's title row or bottom border: the
// page's tabs are on their own row inside its frame.
func noTabOnABorder(t *testing.T, r *running) {
	t.Helper()
	for _, row := range strings.Split(r.s.String(), "\n") {
		if (strings.Contains(row, "┌") || strings.Contains(row, "└")) && strings.Contains(row, "× ") {
			t.Fatalf("a tab on a border %q: want the tabs on the row under the title:\n%s", row, r.s)
		}
	}
}

// press is a left press and release at x, y.
func (r *running) press(t *testing.T, x, y int) {
	t.Helper()
	r.keys(t, tuicore.MouseEvent{Kind: tuicore.MousePress, Button: tuicore.MouseLeft, X: x, Y: y},
		tuicore.MouseEvent{Kind: tuicore.MouseRelease, Button: tuicore.MouseLeft, X: x, Y: y})
}

// fileTitle is the page's frame's title as the host set it.
func (r *running) fileTitle() string {
	return onLoop(r, func() string { v, _ := r.h.p.Tree().Source("App.fileTitle"); return v.Raw })
}

// The terminal shows the buffers as tabs on the first row inside the page's frame, under its title
// row, with one buffer as with more, in opening order, as the agent and terminal panels carry
// theirs: the title row keeps the file's name (its badges and breadcrumb after it) and no tab, and
// no tab is on any bottom border. A press on a tab shows its buffer, and a switch made elsewhere
// (SPC b 2) moves the tabs' active one with it.
func TestTheTerminalShowsTheBuffersAsTabsUnderThePagesTitle(t *testing.T) {
	d := startDaemon(t, map[string][]string{"kb": {"a.md", "a\n", "b.md", "b\n"}})
	r := attached(t, d)
	r.waitListed(t, 2)
	// one buffer: its tab, on the row under the title, the title row naming the file
	r.openByPicker(t, "a.md")
	r.waitFile(t, "a.md")
	if n := onLoop(r, func() int { return len(r.h.buffers) }); n != 1 {
		t.Fatalf("%d buffers after one open: want 1", n)
	}
	r.s.WaitFor(t, "a.md's tab under the title, with one buffer", func(string) bool { _, _, _, ok := r.frameTab("a.md"); return ok })
	_, _, y1, _ := r.frameTab("a.md")
	if !strings.Contains(r.screenRow(y1-1), "a.md") {
		t.Fatalf("the title row %q with one buffer: want the file's name", r.screenRow(y1-1))
	}
	noTabOnABorder(t, r)
	r.openByPicker(t, "b.md")
	r.waitFile(t, "b.md")
	r.s.WaitFor(t, "a.md's and b.md's tabs on one row under the title", func(string) bool {
		_, _, ya, oka := r.frameTab("a.md")
		_, _, yb, okb := r.frameTab("b.md")
		return oka && okb && ya == yb
	})
	_, ax, y, _ := r.frameTab("a.md")
	_, bx, _, _ := r.frameTab("b.md")
	if ax >= bx || y != y1 {
		t.Fatalf("a.md's tab at %d, b.md's at %d, on row %d (one buffer's on %d): want them in opening order, the row where it was", ax, bx, y, y1)
	}
	noTabOnABorder(t, r)
	if title := r.fileTitle(); !strings.HasPrefix(title, "b.md") || !strings.Contains(r.screenRow(y-1), "b.md") {
		t.Fatalf("the title %q, its row %q: want the shown file's name, as before there were tabs", title, r.screenRow(y-1))
	}
	r.press(t, ax, y)
	r.s.WaitFor(t, "a.md shown by its tab", func(string) bool { return r.file().path == "a.md" })
	// switched elsewhere (SPC b 2, the picker, H and L in Vim's keys), the tabs follow
	onLoop(r, func() bool { r.h.showBufferAt(2); return true })
	r.s.WaitFor(t, "b.md shown, its tab active", func(string) bool {
		return r.file().path == "b.md" && onLoop(r, func() bool { return r.h.bufferStrip().Active() == tabID(r.h.active) })
	})
}

// A press on the shown buffer's × closes it: its tab goes, the buffer before it is shown, and the
// one left keeps its tab on the row.
func TestARowTabsCloseClosesTheShownBuffer(t *testing.T) {
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
	r.s.WaitFor(t, "b.md closed, a.md shown, its tab still on the row", func(string) bool {
		_, _, _, still := r.frameTab("b.md")
		_, _, ya, oka := r.frameTab("a.md")
		return !still && oka && ya == y && r.file().path == "a.md"
	})
	if n := onLoop(r, func() int { return len(r.h.buffers) }); n != 1 {
		t.Fatalf("%d buffers after closing b.md: want 1", n)
	}
}

// The row's + and ≡, after the tabs, take their presses there, with one buffer: + makes a new,
// empty buffer, shown, its tab added; ≡ opens the tabs' menu.
func TestTheRowsPlusAndMenuTakeTheirPresses(t *testing.T) {
	d := startDaemon(t, map[string][]string{"kb": {"a.md", "a\n"}})
	r := attached(t, d)
	r.waitListed(t, 1)
	r.openByPicker(t, "a.md")
	r.waitFile(t, "a.md")
	r.s.WaitFor(t, "a.md's tab", func(string) bool { _, _, _, ok := r.frameTab("a.md"); return ok })
	_, ax, y, _ := r.frameTab("a.md")
	// after is the column of glyph on the row of tabs past column x, -1 for none
	after := func(glyph string, x int) int {
		row := []rune(r.screenRow(y))
		for i := x; i < len(row); i++ {
			if string(row[i]) == glyph {
				return i
			}
		}
		return -1
	}
	plus, menu := after("+", ax), after("≡", ax)
	if plus < 0 || menu <= plus {
		t.Fatalf("the row of tabs %q: want a.md's tab, then + and ≡", r.screenRow(y))
	}
	r.press(t, plus, y)
	r.s.WaitFor(t, "a new buffer, shown, its tab added", func(string) bool {
		return onLoop(r, func() bool { return len(r.h.buffers) == 2 && r.h.active != nil && !r.h.active.file.open })
	})
	r.s.WaitFor(t, "the row holds two tabs", func(screen string) bool { return strings.Count(r.screenRow(y), "× ") == 2 })
	menu = after("≡", ax)
	r.press(t, menu, y)
	r.s.WaitFor(t, "the tabs' menu, opened by ≡", func(screen string) bool {
		return strings.Contains(screen, "New buffer") && strings.Contains(screen, "Open file in a new buffer")
	})
}

// In a narrow terminal the tabs scroll between the arrows on their row, the shown buffer's tab in
// view; the title row keeps Raw | Rendered whole and no arrow; and no tab is wider than
// terminalTabWidth.
func TestTheRowTabsScrollInANarrowRoom(t *testing.T) {
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
	label := onLoop(r, func() string { return r.h.bufferTabLabel(r.h.active, terminalTabWidth-2) })
	r.s.WaitFor(t, "the tabs scrolled under the title, the shown one's in view", func(string) bool {
		_, _, y, ok := r.frameTab(strings.TrimSuffix(label, " [+]"))
		if !ok {
			return false
		}
		row, title := r.screenRow(y), r.screenRow(y-1)
		return (strings.Contains(row, "→") || strings.Contains(row, "←")) &&
			strings.Contains(row, "…") && !strings.Contains(row, "× one.md") &&
			strings.Contains(title, "[ Raw | Rendered ]") && !strings.ContainsAny(title, "←→")
	})
	if w := tuicore.StringWidth(label); w > terminalTabWidth-2 {
		t.Fatalf("the long name's tab %q is %d cells: want at most %d", label, w, terminalTabWidth-2)
	}
	noTabOnABorder(t, r)
}

// The tabs read in sepia and in dark: the shown buffer's tab stands apart from the others, its text
// apart from its own paper, and each theme draws it in its own colours.
func TestTheRowTabsReadInSepiaAndDark(t *testing.T) {
	d := startDaemon(t, map[string][]string{"kb": {"a.md", "a\n", "b.md", "b\n"}})
	r := runTUISized(t, NewSession(d.sock, nil), Options{}, 100, 24)
	r.waitAttached(t, "kb")
	for _, p := range []string{"a.md", "b.md"} {
		onLoop(r, func() bool { r.h.openPath(p); return true })
		r.waitFile(t, p)
	}
	// look is the shown tab's and the other's colours once they stand apart and the shown one's
	// differ from was: the theme switched to repainted, never the theme before it still on screen
	look := func(name string, was tuicore.CellAttrs) (active, other tuicore.CellAttrs) {
		r.s.WaitFor(t, name+": the shown tab apart from the other, in this theme's colours", func(string) bool {
			_, bx, by, okb := r.frameTab("b.md")
			_, ax, ay, oka := r.frameTab("a.md")
			if !okb || !oka {
				return false
			}
			snap := r.s.Backend.Snapshot()
			active, other = snap[by][bx].Attrs, snap[ay][ax].Attrs
			return active.BG != other.BG && active.FG != active.BG && other.FG != other.BG && active != was
		})
		return active, other
	}
	start := onLoop(r, func() string { return r.h.theme })
	if start == "sepia" {
		t.Fatalf("the theme is %s from the start: want another, so sepia's repaint is seen", start)
	}
	was, _ := look(start, tuicore.CellAttrs{})
	for _, theme := range []string{"sepia", "dark"} {
		onLoop(r, func() bool { r.h.switchTheme(theme); return true })
		r.s.WaitFor(t, theme, func(string) bool { return onLoop(r, func() string { return r.h.theme }) == theme })
		active, other := look(theme, was)
		t.Logf("%s: the shown tab %+v, the other %+v", theme, active, other)
		dumpScreen(t, r, "buffer-tabs-"+theme)
		was = active
	}
}

// With the tabs shown, Raw | Rendered keeps the title row's right, over them, and takes its presses
// as it does without tabs: Raw opens the text, Rendered the page again.
func TestRawRenderedStaysClickableOverTheRowTabs(t *testing.T) {
	d := startDaemon(t, map[string][]string{"kb": {"a.md", "# A\n\nalpha\n", "b.md", "# B\n\nbeta\n"}})
	r := attached(t, d)
	r.waitListed(t, 2)
	for _, p := range []string{"a.md", "b.md"} {
		r.openByPicker(t, p)
		r.waitFile(t, p)
	}
	r.s.WaitFor(t, "b.md's page", func(string) bool { return shownView(r) == editor.ViewRendered })
	var col, row int
	r.s.WaitFor(t, "the switch on the title row, the tabs on the row under it", func(string) bool {
		var ok bool
		if col, row, ok = find(r.s.Backend.Snapshot(), "[ Raw | Rendered ]"); !ok {
			return false
		}
		_, _, by, okb := r.frameTab("b.md")
		return okb && by == row+1
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
// row under the frame's title row, is the tab's: it shows its buffer, and the page stays where it
// is.
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
	r.s.WaitFor(t, "a.md's tab under the title row", func(string) bool {
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
