package tui

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"sync"
	"testing"
	"unicode"

	tuicore "github.com/yongjohnlee80/golib/tui"
	"github.com/yongjohnlee80/golib/tui/widget"
)

// fakeFonts records what the window is told, as golib's gui.Backend would take it.
type fakeFonts struct {
	mu    sync.Mutex
	calls []string
}

func (f *fakeFonts) SetFont(typeface string, size float32) {
	f.add(fmt.Sprintf("font %s %v", typeface, size))
}
func (f *fakeFonts) SetProseFont(family string) { f.add("prose " + family) }
func (f *fakeFonts) SetZoom(pct int)            { f.add(fmt.Sprintf("zoom %d", pct)) }
func (f *fakeFonts) add(c string)               { f.mu.Lock(); f.calls = append(f.calls, c); f.mu.Unlock() }
func (f *fakeFonts) all() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return slices.Clone(f.calls)
}

// runWindowTUI is the TUI as --gui runs it, its fonts told to fonts.
func runWindowTUI(t *testing.T, prefs map[string]string, fonts *fakeFonts) (*daemon, *running) {
	t.Helper()
	d := startDaemonWith(t, "", map[string][]string{"kb": {"a.md", "a\n"}}, daemonOpts{prefs: prefs})
	r := runTUI(t, NewSession(d.sock, nil), Options{GUI: true, Fonts: fonts, DefaultCellFont: "Go Mono",
		FontFamilies: func(mono bool) []string {
			if mono {
				return []string{"Go Mono", "JetBrains Mono"}
			}
			return []string{"Go", "Go Mono", "JetBrains Mono", "Noto Serif"}
		}})
	r.s.WaitForText(t, "connected — autodoc v-test")
	return d, r
}

// TestTheWindowsFontsComeFromTheStore: the stored fonts and zoom are told to the window when the
// preferences load, once each; one changed is told again alone.
func TestTheWindowsFontsComeFromTheStore(t *testing.T) {
	fonts := &fakeFonts{}
	d, r := runWindowTUI(t, map[string]string{"gui.font.cell": "JetBrains Mono", "gui.font.size": "16",
		"gui.font.prose": "Noto Serif", "gui.zoom": "125"}, fonts)
	r.s.WaitFor(t, "the window told", func(string) bool { return len(fonts.all()) == 3 })
	if got := strings.Join(fonts.all(), "; "); got != "font JetBrains Mono 16; prose Noto Serif; zoom 125" {
		t.Fatalf("told %q", got)
	}
	onLoop(r, func() bool { r.h.zoomStep(1); return true })
	storedPref(t, d, r, "gui.zoom", "150")
	if got := fonts.all(); len(got) != 4 || got[3] != "zoom 150" {
		t.Errorf("Ctrl+= told %q, want zoom 150 alone", got)
	}
}

// TestZoomStepsStopAtTheEnds: Ctrl+= and Ctrl+- move between the steps and stop at 100% and 200%;
// Ctrl+0 is 100%.
func TestZoomStepsStopAtTheEnds(t *testing.T) {
	fonts := &fakeFonts{}
	d, r := runWindowTUI(t, map[string]string{"gui.zoom": "175"}, fonts)
	r.s.WaitFor(t, "the zoom told", func(string) bool { return slices.Contains(fonts.all(), "zoom 175") })
	for range 3 {
		onLoop(r, func() bool { r.h.zoomStep(1); return true })
	}
	storedPref(t, d, r, "gui.zoom", "200")
	r.keys(t, tuicore.KeyEvent{Kind: tuicore.KeyPress, Code: '0', Mods: tuicore.ModCtrl})
	storedPref(t, d, r, "gui.zoom", "100")
	onLoop(r, func() bool { r.h.zoomStep(-1); return true })
	if got := onLoop(r, func() int { return zoomOf(r.h.prefs) }); got != 100 {
		t.Errorf("Ctrl+- under 100%%: %d", got)
	}
}

// TestTheFontsDialogListsTheFamilies: Options › Fonts and zoom… lists the default and the installed
// families, the stored one chosen; a choice is stored and told to the window at once.
func TestTheFontsDialogListsTheFamilies(t *testing.T) {
	fonts := &fakeFonts{}
	d, r := runWindowTUI(t, map[string]string{"gui.font.cell": "JetBrains Mono"}, fonts)
	onLoop(r, func() bool { r.h.openFonts(); return true })
	r.s.WaitForText(t, "fonts and zoom")
	if got := onLoop(r, func() []string { return r.h.cellFonts }); !slices.Equal(got, []string{"", "Go Mono", "JetBrains Mono"}) {
		t.Fatalf("cell fonts %q", got)
	}
	onLoop(r, func() bool { r.h.setProseFontIndex(4); return true }) // Noto Serif
	storedPref(t, d, r, "gui.font.prose", "Noto Serif")
	r.s.WaitFor(t, "told", func(string) bool { return slices.Contains(fonts.all(), "prose Noto Serif") })
	onLoop(r, func() bool { r.h.setCellFontIndex(0); return true }) // the default
	r.s.WaitFor(t, "the default told", func(string) bool { return slices.Contains(fonts.all(), "font Go Mono 14") })
}

// TestATerminalHasNoFonts: in a terminal there are no fonts to set: Options' entries hide, the zoom
// keys and Fonts do nothing but say why, and the right-click menu has no Zoom.
func TestATerminalHasNoFonts(t *testing.T) {
	d := startDaemon(t, map[string][]string{"kb": {"a.md", "a\n"}})
	r := attached(t, d)
	onLoop(r, func() bool { r.h.zoomStep(1); r.h.setZoom(150); r.h.setExplorerEdge(1); return true })
	// the store's writes are in order: a zoom written would be there before the explorer's edge
	storedPref(t, d, r, "tui.explorer.edge", "right")
	if m, _ := d.db.Preferences(context.Background()); m["gui.zoom"] != "" {
		t.Errorf("a terminal stored a zoom: %q", m["gui.zoom"])
	}
	said := onLoop(r, func() string { r.h.openFonts(); return r.h.notices[0].text })
	if !strings.Contains(said, "its own settings choose them") {
		t.Errorf("Fonts in a terminal said %q", said)
	}
	rows := onLoop(r, func() int { return len(r.h.editorMenu(r.h.core)) })
	fontsRows := onLoop(r, func() int {
		r.h.fonts = &fakeFonts{}
		defer func() { r.h.fonts = nil }()
		return len(r.h.editorMenu(r.h.core))
	})
	if fontsRows != rows+2 {
		t.Errorf("the right-click menu has %d rows, %d with the window's fonts: want the Zoom and its separator only there", rows, fontsRows)
	}
}

// TestTheTutorialPagesStopAtTheEnds: Help › Tutorial… opens at page 1; Back does nothing there, Next
// goes on to the last page and stops.
func TestTheTutorialPagesStopAtTheEnds(t *testing.T) {
	d := startDaemon(t, map[string][]string{"kb": {"a.md", "a\n"}})
	r := attached(t, d)
	onLoop(r, func() bool { r.h.openTutorial(); return true })
	r.s.WaitForText(t, fmt.Sprintf("1 of %d · The page, and its two editor modes", len(tutorialPages)))
	onLoop(r, func() bool { r.h.tutorialStep(-1); return true })
	for range len(tutorialPages) + 2 {
		onLoop(r, func() bool { r.h.tutorialStep(1); return true })
	}
	r.s.WaitForText(t, fmt.Sprintf("%d of %d · The window", len(tutorialPages), len(tutorialPages)))
	if got := onLoop(r, func() int { return r.h.tutorialAt }); got != len(tutorialPages)-1 {
		t.Errorf("page %d after too many Nexts", got)
	}
	for _, p := range tutorialPages {
		if !strings.HasPrefix(p, "# ") {
			t.Errorf("a page without its heading: %.40q", p)
		}
	}
}

// TestSpacePeriodOpensTheRightClickMenu: SPC . opens the page's right-click menu at the cursor, and
// the menu, not the page, has the keyboard.
func TestSpacePeriodOpensTheRightClickMenu(t *testing.T) {
	d := startDaemon(t, map[string][]string{"kb": {"a.md", "a\n"}})
	r := attached(t, d)
	r.leader(t, '.')
	r.s.WaitForText(t, "View diagram")
	// the menu has the keyboard, not the page the leader card gave it back to: Escape closes it
	r.keys(t, esc())
	r.s.WaitFor(t, "the menu closed by Escape", func(sc string) bool { return !strings.Contains(sc, "View diagram") })
}

// TestTheFontsDialogShowsTheDefaultAtFirst: with no font chosen yet, the choosers read "(the
// default)" the first time the dialog opens, not a blank field.
func TestTheFontsDialogShowsTheDefaultAtFirst(t *testing.T) {
	fonts := &fakeFonts{}
	_, r := runWindowTUI(t, nil, fonts)
	onLoop(r, func() bool { r.h.openFonts(); return true })
	r.s.WaitForText(t, "fonts and zoom")
	r.s.WaitFor(t, "both choosers at (the default)", func(sc string) bool { return strings.Count(sc, "(the default)") == 2 })
}

// TestTheRightClickRowsHaveAccessKeysButNotHJKL: AutoDoc's rows in the page's right-click menu
// each have an access key, none twice in a level, and none of h j k l, which move the cursor there.
func TestTheRightClickRowsHaveAccessKeysButNotHJKL(t *testing.T) {
	d := startDaemon(t, map[string][]string{"kb": {"a.md", "a\n"}})
	r := attached(t, d)
	rows := onLoop(r, func() []widget.MenuItemModel {
		r.h.fonts = &fakeFonts{} // the GUI's rows too: the Zoom
		defer func() { r.h.fonts = nil }()
		return r.h.editorMenu(r.h.core)
	})
	var check func(level []widget.MenuItemModel)
	check = func(level []widget.MenuItemModel) {
		seen := map[rune]string{}
		for _, row := range level {
			if row.Kind == widget.ItemKindSeparator || !strings.HasPrefix(string(row.ID), "autodoc.") {
				continue
			}
			k := unicode.ToLower(row.Hotkey)
			switch {
			case k == 0:
				t.Errorf("%q has no access key", row.Label)
			case strings.ContainsRune("hjkl", k):
				t.Errorf("%q takes %q, which moves the menu's cursor", row.Label, k)
			case seen[k] != "":
				t.Errorf("%q and %q both take %q", seen[k], row.Label, k)
			}
			seen[k] = row.Label
			check(row.Children)
		}
	}
	check(rows)
}
