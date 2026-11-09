package tui

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	tuicore "github.com/yongjohnlee80/golib/tui"
	"github.com/yongjohnlee80/golib/tui/decl/decltest"
	"github.com/yongjohnlee80/golib/tui/widget/editor"
)

// THE PAGE'S ACCENT IS THE DOCUMENT'S, and the menu's stays the menu's: the root Window binds
// palette.documentAccent to the theme's document.accent — golib fills it from menu.accent where a
// theme writes none — so retro's page reads its own red on the navy page where the menu's dark one
// sits under the 3:1 floor, an open menu's access keys keep the menu's, and sepia, which writes no
// document.accent, reads its menu's own: nothing moves on the nine. A note's Rendered view is the
// editor's: its diagrams' box outlines wear the document accent, and its inline code reads the Raw
// view's syntax colour, no longer the document accent (an HTML file's page keeps that:
// TestARawHTMLFilesPageWearsTheDocumentAccent).

// accentSource is a note with the page's accent-bearing cells: inline code, and a flowchart's boxes.
const accentSource = "### Three\n\nplain with `zq` inline\n\n```mermaid\nflowchart LR\n  A[Gull] --> B[Tern]\n```\n"

// outlineFG is the foreground of the box outline beside the flowchart's "Gull" ("│ Gull"), or false
// while it is not drawn.
func outlineFG(r *running) (string, bool) {
	snap := r.s.Backend.Snapshot()
	x, y, ok := find(snap, "Gull")
	if !ok || x < 2 || snap[y][x-2].Content != "│" {
		return "", false
	}
	return fgIs(snap[y][x-2].Attrs), true
}

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
			d := startDaemonWith(t, "", map[string][]string{"kb": {"a.md", accentSource}},
				daemonOpts{prefs: map[string]string{"tui.theme": theme}})
			r := runTUISized(t, NewSession(d.sock, nil), Options{}, 80, 20)
			r.connected(t)
			r.h.p.Post(func() { r.h.openPath("a.md") })
			r.s.WaitFor(t, "a.md Rendered by the editor, its flowchart drawn", func(sc string) bool {
				_, drawn := outlineFG(r)
				return renderedByTheEditor(r) && strings.Contains(sc, "plain with zq inline") && drawn
			})
			pageAccent := themeValue(t, r, theme, "document.accent") // golib fills it from menu.accent when unset
			menuAccent := themeValue(t, r, theme, "menu.accent")
			if theme == "retro" && pageAccent == menuAccent {
				t.Fatalf("retro's document accent %q is its menu's %q; the theme's own red never reached the app", pageAccent, menuAccent)
			}
			if theme == "sepia" && pageAccent != menuAccent {
				t.Fatalf("sepia's page accent %q is not its menu's %q; the fill moved on a theme with no document.accent", pageAccent, menuAccent)
			}
			// (1) the flowchart's box outline reads the document's accent
			r.s.WaitFor(t, "the box outline in "+theme+"'s document accent", func(string) bool {
				fg, ok := outlineFG(r)
				return ok && fg == pageAccent
			})
			// (1a) its inline code reads the Raw view's colour
			rendered, _ := codeFG(r)
			r.keys(t, decltest.Ctrl('t'))
			r.s.WaitForText(t, "plain with `zq` inline")
			if raw, _ := codeFG(r); raw != rendered {
				t.Fatalf("a note's Rendered inline code reads %q, its Raw %q", rendered, raw)
			}
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

// TestARawHTMLFilesPageWearsTheDocumentAccent: an HTML file opened raw is its page, Rendered, and its
// inline code wears the document accent, retro's own red apart from its menu's, sepia's its menu's.
func TestARawHTMLFilesPageWearsTheDocumentAccent(t *testing.T) {
	for _, theme := range []string{"retro", "sepia"} {
		t.Run(theme, func(t *testing.T) {
			r, _ := runHTML(t)
			storeTheme(t, r, theme)
			page := filepath.Join(t.TempDir(), "page.html")
			if err := os.WriteFile(page, []byte("<h3>Three</h3>\n<p>plain with <code>zq</code> inline</p>\n"), 0o600); err != nil {
				t.Fatal(err)
			}
			r.openHTMLAnswer(t, page, "raw")
			r.waitOpen(t, "", page)
			r.s.WaitFor(t, "the file's page, Rendered", func(sc string) bool {
				shown := onLoop(r, func() editor.EditorView { return r.h.editor.(*editor.Editor).ShownView() })
				return shown == editor.ViewRendered && strings.Contains(sc, "plain with zq inline") && !strings.Contains(sc, "<code>")
			})
			pageAccent := themeValue(t, r, theme, "document.accent")
			menuAccent := themeValue(t, r, theme, "menu.accent")
			if theme == "retro" && pageAccent == menuAccent {
				t.Fatalf("retro's document accent %q is its menu's %q", pageAccent, menuAccent)
			}
			if theme == "sepia" && pageAccent != menuAccent {
				t.Fatalf("sepia's page accent %q is not its menu's %q", pageAccent, menuAccent)
			}
			r.s.WaitFor(t, "the page's inline code in "+theme+"'s document accent", func(string) bool {
				fg, ok := codeFG(r)
				return ok && fg == pageAccent
			})
		})
	}
}
