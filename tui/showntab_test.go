package tui

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	tuicore "github.com/yongjohnlee80/golib/tui"
	"github.com/yongjohnlee80/golib/tui/decl/themes"
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

// devSaveStylesTheShownTab: run from a copy of the QML (-dev, dev), main.qml importing dark, two
// buffers open and the theme switched to nord, a save of main.qml (a comment added, its import
// kept), caught by the program's own watcher, puts dark back on screen, the file being the
// authority (theme.go): the shown tab wears dark's highlight again, never nord's, and the host
// knows the theme on screen is dark (the menu's check, a plugin's theme). at is where a tab's
// label starts on the screen.
func devSaveStylesTheShownTab(t *testing.T, r *running, dev string, at func(label string) (x, y int, ok bool)) {
	t.Helper()
	main := filepath.Join(dev, "main.qml")
	src, err := os.ReadFile(main)
	if err != nil {
		t.Fatal(err)
	}
	if got := themeOf(src); got != "dark" {
		t.Fatalf("main.qml imports %q: want dark, the theme the save brings back", got)
	}
	r.ready(t)
	for _, p := range []string{"a.md", "b.md"} {
		onLoop(r, func() bool { r.h.openPath(p); return true })
		r.waitFile(t, p)
	}
	nord, err := themes.Values("nord")
	if err != nil {
		t.Fatal(err)
	}
	dark, err := themes.Values("dark")
	if err != nil {
		t.Fatal(err)
	}
	nordShown, darkShown := cellColor(t, nord["app.highlight"]), cellColor(t, dark["app.highlight"])
	shown := func() tuicore.CellColor {
		x, y, ok := at("b.md")
		snap := r.s.Backend.Snapshot()
		if !ok || y >= len(snap) || x >= len(snap[y]) {
			return tuicore.CellColor{}
		}
		return snap[y][x].Attrs.BG
	}
	r.waitWithin(t, "the shown tab in dark's highlight, from the start", daemonWait, func() bool { return shown() == darkShown })
	onLoop(r, func() bool { r.h.switchTheme("nord"); return true })
	r.waitWithin(t, "the shown tab in nord's highlight, switched", daemonWait, func() bool { return shown() == nordShown })
	// written whole and renamed in, as an editor saves: the watcher never reads half a file
	if err := os.WriteFile(main+".tmp", append(src, "\n// a comment: the import is kept\n"...), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(main+".tmp", main); err != nil {
		t.Fatal(err)
	}
	r.waitWithin(t, "the save reloaded: the shown tab in dark's highlight, the host's theme dark", daemonWait, func() bool {
		return shown() == darkShown && onLoop(r, func() string { return r.h.theme }) == "dark"
	})
}

// TestADevSaveStylesTheShownTabInTheThemeItImports: the terminal's row of tabs
// (devSaveStylesTheShownTab).
func TestADevSaveStylesTheShownTabInTheThemeItImports(t *testing.T) {
	dev := t.TempDir()
	if err := os.CopyFS(dev, qmlFiles); err != nil {
		t.Fatal(err)
	}
	d := startDaemon(t, map[string][]string{"kb": {"a.md", "a\n", "b.md", "b\n"}})
	r := runTUI(t, NewSession(d.sock, nil), Options{Dev: dev, ProgramOptions: tabStyleOptions()})
	devSaveStylesTheShownTab(t, r, dev, r.labelOnScreen)
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
