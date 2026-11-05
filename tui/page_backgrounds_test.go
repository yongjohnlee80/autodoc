package tui

import (
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/yongjohnlee80/golib/highlight"
	tuicore "github.com/yongjohnlee80/golib/tui"
)

// THE PAGE'S OWN BACKGROUND, under every theme: a style with no background punches the TERMINAL's
// background through the page's fill (Johno's sepia bug — dark patches behind text runs and box
// labels). The page's text styles and the diagram themes carry the page's background now
// (style.PageText and mermaid.TokenTheme), so a text run, a heading, a link, a diagram's label and
// box and the Mindmap pane's boxes paint on the theme's base, never the terminal's own: started in
// the theme and switched into it.

// bgIs reports the attrs' background as r,g,b, or "" for default.
func bgIs(a tuicore.CellAttrs) string {
	if a.BG.Kind == 0 {
		return ""
	}
	return strconv.Itoa(int(a.BG.R)) + "," + strconv.Itoa(int(a.BG.G)) + "," + strconv.Itoa(int(a.BG.B))
}

// themeValue is the named theme's one value, as its values hold it: r,g,b.
func themeValue(t *testing.T, r *running, name, key string) string {
	t.Helper()
	vals, err := r.h.themeValues(name)
	if err != nil {
		t.Fatalf("theme %q: %v", name, err)
	}
	v, ok := vals[key]
	if !ok || !strings.HasPrefix(v, "#") {
		t.Fatalf("theme %q holds no %s", name, key)
	}
	n, err := strconv.ParseInt(v[1:], 16, 64)
	if err != nil {
		t.Fatalf("theme %q's %s %q: %v", name, key, v, err)
	}
	return strconv.Itoa(int(n>>16&0xff)) + "," + strconv.Itoa(int(n>>8&0xff)) + "," + strconv.Itoa(int(n&0xff))
}

// themeBase is the named theme's page base — the document group's, what the page's palette binds.
func themeBase(t *testing.T, r *running, name string) string {
	return themeValue(t, r, name, "document.base")
}

// storeTheme puts the named theme on screen and keeps it, and answers the keyword colour the
// theme's syntax roles carry — the durable signal the posted reload landed.
func storeTheme(t *testing.T, r *running, name string) string {
	t.Helper()
	want := themeValue(t, r, name, "syntax.keyword")           // the theme's own keyword colour: deterministic
	onLoop(r, func() bool { r.h.useTheme(name); return true }) // the View › Theme path: switches AND stores
	for deadline := time.Now().Add(20 * time.Second); ; {      // the posted reload: wait the palette carry it
		if keywordFG(r) == want {
			return want
		}
		if time.Now().After(deadline) {
			t.Fatalf("the switch to %s never reached the palette (keyword %q, want %q)", name, keywordFG(r), want)
		}
		time.Sleep(50 * time.Millisecond)
	}
}

// keywordFG is the window palette's syntax keyword colour, r,g,b ("" when unset).
func keywordFG(r *running) string {
	fg, _ := r.h.p.SyntaxStyles()[highlight.Keyword].GetForeground()
	red, green, blue, ok := fg.RGBValues()
	if !ok {
		return ""
	}
	return strconv.Itoa(int(red)) + "," + strconv.Itoa(int(green)) + "," + strconv.Itoa(int(blue))
}

// themeStartedIn runs a fresh session against the daemon (whose stored preference is the theme) and
// waits the start's posted switch out: the window's palette carries the theme's keyword.
func themeStartedIn(t *testing.T, d *managedDaemon, name, keyword string) *running {
	t.Helper()
	r := runTUI(t, NewSession(d.sock, nil), Options{})
	r.connected(t)
	for deadline := time.Now().Add(20 * time.Second); ; {
		if keywordFG(r) == keyword {
			return r
		}
		if time.Now().After(deadline) {
			t.Fatalf("the start's switch to %s never reached the palette (keyword %q)", name, keywordFG(r))
		}
		time.Sleep(50 * time.Millisecond)
	}
}

// pageThemes are the built-ins the page's background is checked under: the two light pages and
// retro's blue against a dark terminal, and a truecolor dark against any other.
var pageThemes = []string{"sepia", "light", "retro", "dark"}

// pageSource is a note with each thing that paints on the page: a heading, text, a link, and a
// diagram with a box (the rounded root) and a label.
const pageSource = "# Heading\n\nA [link](a.md) and text.\n\n```mermaid\nmindmap\n  hub((hub))\n    leaf\n```\n"

// outlined is where label is drawn in a box of its own: its top-left corner two cells left and one
// row up, or ok false: a label elsewhere on screen (a title, a status line) doesn't count.
func outlined(snap [][]tuicore.Cell, label string) (x, y int, ok bool) {
	rs := []rune(label)
	for y := 1; y < len(snap); y++ {
	cols:
		for x := 2; x+len(rs) <= len(snap[y]); x++ {
			for i, r := range rs {
				if snap[y][x+i].Content != string(r) {
					continue cols
				}
			}
			if c := snap[y-1][x-2].Content; strings.Contains("╭┌┏", c) && c != "" {
				return x, y, true
			}
		}
	}
	return 0, 0, false
}

// wantBase is the cell's background against the theme's base.
func wantBase(t *testing.T, what string, snap [][]tuicore.Cell, x, y int, base string) {
	t.Helper()
	if got := bgIs(snap[y][x].Attrs); got != base {
		t.Errorf("%s at %d,%d (%q): background %q, want the theme's base %q", what, x, y, snap[y][x].Content, got, base)
	}
}

// checkPage is every kind of cell on the shown page against the theme's base. A thing it can't
// find fails: it wasn't checked.
func checkPage(t *testing.T, r *running, theme string) {
	t.Helper()
	r.s.WaitFor(t, "the page drawn, its diagram too", func(string) bool {
		_, _, ok := outlined(r.s.Backend.Snapshot(), "hub")
		return ok
	})
	base := themeBase(t, r, theme)
	snap := r.s.Backend.Snapshot()
	for _, text := range []string{"Heading", "link", "text.", "leaf"} {
		x, y, ok := find(snap, text)
		if !ok {
			t.Fatalf("%q not on the page:\n%s", text, r.s.Backend.String())
		}
		wantBase(t, strconv.Quote(text), snap, x, y, base)
	}
	x, y, _ := outlined(snap, "hub")
	wantBase(t, "the diagram's label", snap, x, y, base)
	wantBase(t, "the diagram's box", snap, x-2, y-1, base)
}

// The page's text, heading, link, and a diagram's label and box carry the theme's own base, never
// the terminal's: switched into the theme with the page open, and started in it.
func TestThePagesTextCarriesItsThemesBase(t *testing.T) {
	for _, theme := range pageThemes {
		t.Run(theme, func(t *testing.T) {
			d := startManagedIncluding(t, map[string]string{"kb": fileDir(t, "page.md", pageSource)}, tableInclude)
			first := runTUI(t, NewSession(d.sock, nil), Options{})
			first.connected(t)
			onLoop(first, func() bool { first.h.openPath("page.md"); return true })
			first.waitFile(t, "page.md")
			kw := storeTheme(t, first, theme)
			t.Run("switched into", func(t *testing.T) { checkPage(t, first, theme) })

			r := themeStartedIn(t, d, theme, kw)
			onLoop(r, func() bool { r.h.openPath("page.md"); return true })
			r.waitFile(t, "page.md")
			t.Run("started in", func(t *testing.T) { checkPage(t, r, theme) })
		})
	}
}

// openMindmap opens the cell Mindmap pane and answers it.
func openMindmap(t *testing.T, r *running) *cellMindmap {
	t.Helper()
	m := onLoop(r, func() *cellMindmap {
		for _, f := range r.h.reg.features {
			if m, ok := f.(*cellMindmap); ok {
				return m
			}
		}
		return nil
	})
	if m == nil {
		t.Fatal("a terminal host has no cell Mindmap registered")
	}
	onLoop(r, func() bool { m.toggle(); return true })
	r.s.WaitFor(t, "the cell Mindmap open", func(string) bool {
		return onLoop(r, func() bool { return m.open && m.view != nil })
	})
	return m
}

// checkMindmap is a box of the pane's map against the theme's base: its label and its outline.
func checkMindmap(t *testing.T, r *running, m *cellMindmap, theme string) {
	t.Helper()
	var label string // the root's, as the laid-out map has it: a title or a file's name
	r.s.WaitFor(t, "a box in the Mindmap", func(string) bool {
		label = onLoop(r, func() string {
			if m.cur == nil || len(m.cur.grid.Boxes) == 0 {
				return ""
			}
			return m.cur.grid.Boxes[0].Label
		})
		if label == "" {
			return false
		}
		_, _, ok := outlined(r.s.Backend.Snapshot(), label)
		return ok
	})
	base := themeBase(t, r, theme)
	snap := r.s.Backend.Snapshot()
	x, y, _ := outlined(snap, label)
	wantBase(t, "the Mindmap box's label", snap, x, y, base)
	wantBase(t, "the Mindmap box's outline", snap, x-2, y-1, base)
}

// The Mindmap pane's boxes carry the theme's base too, switched into it and started in it.
func TestTheMindmapPanesBoxesCarryItsThemesBase(t *testing.T) {
	for _, theme := range pageThemes {
		t.Run(theme, func(t *testing.T) {
			d := startManagedIncluding(t, map[string]string{"kb": fileDir(t, "a.md", "# A\n")}, tableInclude)
			first := runTUI(t, NewSession(d.sock, nil), Options{})
			first.connected(t)
			onLoop(first, func() bool { first.h.openPath("a.md"); return true })
			first.waitFile(t, "a.md")
			m := openMindmap(t, first)
			kw := storeTheme(t, first, theme)
			t.Run("switched into", func(t *testing.T) { checkMindmap(t, first, m, theme) })

			r := themeStartedIn(t, d, theme, kw)
			onLoop(r, func() bool { r.h.openPath("a.md"); return true })
			r.waitFile(t, "a.md")
			m = openMindmap(t, r)
			t.Run("started in", func(t *testing.T) { checkMindmap(t, r, m, theme) })
		})
	}
}
