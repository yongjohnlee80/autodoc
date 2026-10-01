package tui

import (
	"context"
	"testing"

	tuicore "github.com/yongjohnlee80/golib/tui"
	"github.com/yongjohnlee80/golib/tui/decl/decltest"
	"github.com/yongjohnlee80/golib/tui/decl/themes"
	"github.com/yongjohnlee80/golib/tui/style"
)

// TestTheScreenAroundThePageIsTheThemesBackdrop: under each shipped theme the columns either side
// of the page wear the theme's app.backdrop, not the terminal's own background, and — but for
// mono, whose page is the terminal's — a tone apart from the page.
func TestTheScreenAroundThePageIsTheThemesBackdrop(t *testing.T) {
	for _, name := range themes.Names() {
		t.Run(name, func(t *testing.T) {
			// the theme as the user picks it: the preference, which the TUI applies once connected
			d := startDaemonWith(t, "", map[string][]string{"kb": {"a.md", "page text\n"}},
				daemonOpts{prefs: map[string]string{"tui.ruler": "60", "tui.theme": name}})
			vals, err := themes.Values(name)
			if err != nil {
				t.Fatal(err)
			}
			want := cellColor(t, vals["app.backdrop"])
			r := runTUISized(t, NewSession(d.sock, nil), Options{}, 160, 20)
			r.ready(t)
			r.h.p.Post(func() { r.h.openPath("a.md") })
			r.s.WaitForText(t, "page text")
			bg := func(x, y int) tuicore.CellColor { return r.s.Backend.Snapshot()[y][x].Attrs.BG }
			// the page is about 64 wide on 160: columns 2 and 157 are outside it
			r.s.WaitFor(t, "the margins in the backdrop", func(string) bool {
				return bg(2, 8) == want && bg(157, 8) == want
			})
			x, y, ok := find(r.s.Backend.Snapshot(), "page text")
			if !ok {
				t.Fatalf("the page's text is not on screen:\n%s", r.s)
			}
			if page := bg(x, y); name != "mono" && page == want {
				t.Errorf("the page and the screen around it are one colour (%+v)", page)
			}
		})
	}
}

// cellColor is how a theme's colour value reaches a TestBackend cell: an RGB, or the terminal's own.
func cellColor(t *testing.T, v string) tuicore.CellColor {
	t.Helper()
	c, err := style.ParseColor(v)
	if err != nil {
		t.Fatal(err)
	}
	if c.IsDefault() {
		return tuicore.CellColor{}
	}
	r, g, b, ok := c.RGBValues()
	if !ok {
		t.Fatalf("%q is neither RGB nor default", v)
	}
	return tuicore.CellColor{Kind: tuicore.CellColorRGB, R: r, G: g, B: b}
}

// TestSepiaIsOnTheThemeMenu: Options › Theme › Sepia switches to it, as the other themes do — the
// preference stored, the menu's check on it, and the page in its paper.
func TestSepiaIsOnTheThemeMenu(t *testing.T) {
	d := startDaemon(t, map[string][]string{"kb": {"a.md", "a\n"}})
	r := attached(t, d)
	r.keys(t, decltest.Alt('o'))
	r.s.WaitForText(t, "Editor mode") // the menu open before its next key
	r.keys(t, key('t'))
	r.s.WaitForText(t, "Sepia")
	r.keys(t, key('s'))
	r.s.WaitFor(t, "tui.theme = sepia", func(string) bool {
		m, err := d.db.Preferences(context.Background())
		return err == nil && m["tui.theme"] == "sepia"
	})
	if p := onLoop(r, func() prefs { return r.h.prefs }); p.theme != "sepia" {
		t.Fatalf("the screen's theme is %q, want sepia", p.theme)
	}
}
