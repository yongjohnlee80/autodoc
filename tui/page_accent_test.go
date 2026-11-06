package tui

import (
	"strconv"
	"testing"

	tuicore "github.com/yongjohnlee80/golib/tui"
	"github.com/yongjohnlee80/golib/tui/decl/decltest"
)

// THE PAGE'S ACCENT IS THE DOCUMENT'S, and the menu's stays the menu's: the root Window binds
// palette.documentAccent to the theme's document.accent — golib fills it from menu.accent where a
// theme writes none — so retro's page reads its own red on the navy page where the menu's dark one
// sits under the 3:1 floor, an open menu's access keys keep the menu's, and sepia, which writes no
// document.accent, reads its menu's own: nothing moves on the nine.

// accentSource is a note with the page's accent-bearing cells: an h3, and inline code.
const accentSource = "### Three\n\nplain with `zq` inline\n"

// fgIs reports the attrs' foreground as r,g,b, or "" for default.
func fgIs(a tuicore.CellAttrs) string {
	if a.FG.Kind == 0 {
		return ""
	}
	return strconv.Itoa(int(a.FG.R)) + "," + strconv.Itoa(int(a.FG.G)) + "," + strconv.Itoa(int(a.FG.B))
}

func TestThePagesAccentIsTheThemesDocumentAccent(t *testing.T) {
	for _, theme := range []string{"retro", "sepia"} {
		t.Run(theme, func(t *testing.T) {
			// the theme as the user picks it: the preference, which the TUI applies once connected
			d := startDaemonWith(t, "", map[string][]string{"kb": {"a.md", accentSource}},
				daemonOpts{prefs: map[string]string{"tui.theme": theme}})
			r := runTUISized(t, NewSession(d.sock, nil), Options{}, 80, 20)
			r.connected(t)
			r.h.p.Post(func() { r.h.openPath("a.md") })
			r.waitOpened(t, "zq")
			pageAccent := themeValue(t, r, theme, "document.accent") // golib fills it from menu.accent when unset
			menuAccent := themeValue(t, r, theme, "menu.accent")
			if theme == "retro" && pageAccent == menuAccent {
				t.Fatalf("retro's document accent %q is its menu's %q; the theme's own red never reached the app", pageAccent, menuAccent)
			}
			if theme == "sepia" && pageAccent != menuAccent {
				t.Fatalf("sepia's page accent %q is not its menu's %q; the fill moved on a theme with no document.accent", pageAccent, menuAccent)
			}
			// (1) the page's inline code reads the document's accent
			r.s.WaitFor(t, "the inline code in "+theme+"'s document accent", func(string) bool {
				snap := r.s.Backend.Snapshot()
				x, y, ok := find(snap, "z")
				return ok && fgIs(snap[y][x].Attrs) == pageAccent
			})
			// (2) an open menu's access keys keep the menu's accent — the blast radius, pinned:
			// the document's red does not reach the chrome
			r.keys(t, decltest.Alt('o'))
			r.s.WaitForText(t, "Editor mode")
			r.keys(t, key('t'))
			r.s.WaitFor(t, theme+"'s theme menu open with its rows", func(string) bool {
				return hasText(r.s.Backend.Snapshot(), "Dark")
			})
			r.s.WaitFor(t, "the menu's access keys in "+theme+"'s menu accent", func(string) bool {
				snap := r.s.Backend.Snapshot()
				var wrong []string
				for _, row := range snap {
					for _, c := range row {
						if c.Attrs.Mask&tuicore.AttrUnderline == 0 {
							continue
						}
						if fg := fgIs(c.Attrs); fg != menuAccent {
							wrong = append(wrong, fg)
						}
					}
				}
				return len(wrong) == 0
			})
		})
	}
}

// hasText reports whether s holds text as one run of cells.
func hasText(snap [][]tuicore.Cell, text string) bool {
	rs := []rune(text)
	for _, row := range snap {
	cols:
		for x := 0; x+len(rs) <= len(row); x++ {
			for i, r := range rs {
				if row[x+i].Content != string(r) {
					continue cols
				}
			}
			return true
		}
	}
	return false
}
