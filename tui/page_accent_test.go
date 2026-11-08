package tui

import (
	"strconv"
	"strings"
	"testing"

	tuicore "github.com/yongjohnlee80/golib/tui"
	"github.com/yongjohnlee80/golib/tui/decl/decltest"
)

// THE PAGE'S ACCENT IS THE DOCUMENT'S, and the menu's stays the menu's: the root Window binds
// palette.documentAccent to the theme's document.accent — golib fills it from menu.accent where a
// theme writes none — so retro's page reads its own red on the navy page where the menu's dark one
// sits under the 3:1 floor, an open menu's access keys keep the menu's, and sepia, which writes no
// document.accent, reads its menu's own: nothing moves on the nine. The page is an HTML file's; a
// note's Rendered view is the editor's, its inline code in the Raw view's colour.

// accentSource is a note with the page's accent-bearing cells: an h3, and inline code; accentPage
// is the same as an HTML file.
const (
	accentSource = "### Three\n\nplain with `zq` inline\n"
	accentPage   = "<h3>Three</h3>\n<p>plain with <code>zq</code> inline</p>\n"
)

// codeFG is the foreground of the first "z" on the screen, the inline code's, or false while none is.
func codeFG(r *running) (string, bool) {
	snap := r.s.Backend.Snapshot()
	x, y, ok := find(snap, "z")
	if !ok {
		return "", false
	}
	return fgIs(snap[y][x].Attrs), true
}

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
			d := startDaemonWith(t, "", map[string][]string{"kb": {"a.md", accentSource, "a.html", accentPage}},
				daemonOpts{prefs: map[string]string{"tui.theme": theme}})
			r := runTUISized(t, NewSession(d.sock, nil), Options{}, 80, 20)
			r.connected(t)
			// (0) a note's Rendered view is the editor's: its inline code reads as Raw's does
			r.h.p.Post(func() { r.h.openPath("a.md") })
			r.s.WaitFor(t, "a.md Rendered by the editor", func(sc string) bool {
				return renderedByTheEditor(r) && strings.Contains(sc, "plain with zq inline")
			})
			rendered, _ := codeFG(r)
			r.keys(t, decltest.Ctrl('t'))
			r.s.WaitForText(t, "plain with `zq` inline")
			if raw, _ := codeFG(r); raw != rendered {
				t.Fatalf("a note's Rendered inline code reads %q, its Raw %q", rendered, raw)
			}
			// the HTML file's page, simplified
			r.h.p.Post(func() { r.h.openPath("a.html") })
			r.s.WaitForText(t, "open an HTML file")
			r.h.p.Post(func() { r.h.openHTMLAs("simplified") })
			r.s.WaitFor(t, "a.html's page", func(sc string) bool {
				return r.file().path == "a.html" && strings.Contains(sc, "plain with zq inline")
			})
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
				fg, ok := codeFG(r)
				return ok && fg == pageAccent
			})
			// (2) a menu's access key still reads the menu's accent — the blast radius, pinned:
			// the document's red does not reach the chrome. The theme menu's Sepia row: a plain
			// row (neither the first, which the cursor highlights inverted, nor the check on
			// the current theme), so its key wears the menu's accent, underlined.
			r.keys(t, decltest.Alt('o'))
			r.s.WaitForText(t, "Editor mode")
			r.keys(t, key('t'))
			r.s.WaitFor(t, "the theme menu's Sepia access key in "+theme+"'s menu accent", func(string) bool {
				snap := r.s.Backend.Snapshot()
				x, y, ok := find(snap, "Sepia")
				return ok && snap[y][x].Attrs.Mask&tuicore.AttrUnderline != 0 && fgIs(snap[y][x].Attrs) == menuAccent
			})
		})
	}
}
