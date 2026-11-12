package tui

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	tuicore "github.com/yongjohnlee80/golib/tui"
)

// showntab_test.go: the shown buffer's tab stands apart from the others in every theme, on the
// terminal's row of tabs and on the window's title bar alike (buffertabs.go styleBufferTabs).

// shownTabStandsOut opens a.md, c.md and b.md, b.md shown, and checks the shown tab stands apart from
// the other two, its background, its text and bold, in the theme on screen and in each one it is
// switched to: nord, catppuccin and mono, whose selection (the page's Editor's highlight, golib's
// look for its strip) is their button, light and sepia, where it is close, and dark. A switch
// restyles the strip, and it is styled again. at is where a tab's label starts on the screen;
// sepia's and dark's screens are dumped as dump-<theme>.
func shownTabStandsOut(t *testing.T, r *running, at func(label string) (x, y int, ok bool), dump string) {
	t.Helper()
	for _, p := range []string{"a.md", "c.md", "b.md"} {
		onLoop(r, func() bool { r.h.openPath(p); return true })
		r.waitFile(t, p)
	}
	// look is the shown tab's and another's colours once the shown one stands apart from both
	// others and differs from was: the theme switched to repainted, never the theme before it
	// still on screen
	look := func(name string, was tuicore.CellAttrs) (active, other tuicore.CellAttrs) {
		r.s.WaitFor(t, name+": the shown tab apart from the others, in this theme's colours", func(string) bool {
			snap := r.s.Backend.Snapshot()
			cell := func(label string) (tuicore.CellAttrs, bool) {
				x, y, ok := at(label)
				if !ok || y >= len(snap) || x >= len(snap[y]) {
					return tuicore.CellAttrs{}, false
				}
				return snap[y][x].Attrs, true
			}
			var ok bool
			if active, ok = cell("b.md"); !ok || active == was || active.Mask&tuicore.AttrBold == 0 || active.FG == active.BG {
				return false
			}
			for _, label := range []string{"a.md", "c.md"} {
				o, ok := cell(label)
				if !ok || o.BG == active.BG || o.FG == active.FG || o.FG == o.BG {
					return false
				}
				other = o
			}
			return true
		})
		return active, other
	}
	start := onLoop(r, func() string { return r.h.theme })
	if start == "sepia" {
		t.Fatalf("the theme is %s from the start: want another, so sepia's repaint is seen", start)
	}
	was, _ := look(start, tuicore.CellAttrs{})
	for _, theme := range []string{"nord", "catppuccin_frappe", "mono", "light", "sepia", "dark"} {
		onLoop(r, func() bool { r.h.switchTheme(theme); return true })
		r.s.WaitFor(t, theme, func(string) bool { return onLoop(r, func() string { return r.h.theme }) == theme })
		active, other := look(theme, was)
		t.Logf("%s: the shown tab %+v, another %+v", theme, active, other)
		if theme == "sepia" || theme == "dark" {
			dumpScreen(t, r, dump+"-"+theme)
		}
		was = active
	}
}

// labelOnScreen is where "× label", a tab, is first drawn on the screen: its label's column and
// row.
func (r *running) labelOnScreen(label string) (x, y int, ok bool) {
	for y, row := range strings.Split(r.s.String(), "\n") {
		if j := strings.Index(row, "× "+label); j >= 0 {
			return len([]rune(row[:j])) + 2, y, true
		}
	}
	return 0, 0, false
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
