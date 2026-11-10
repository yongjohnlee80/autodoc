package tui

import (
	"image/color"
	"os"
	"path/filepath"
	"strings"
	"testing"

	tuicore "github.com/yongjohnlee80/golib/tui"
)

// TestTheTerminalsGlobeMapWearsItsGroups: Relations › GlobeMap opens in a terminal and draws the
// workspace in Braille: Beta, in notes/, a dot in its group's colour made legible on the page
// (onGround), its name beside it; the menu has the item. In sepia, then in dark switched to with the
// map open, the dot reads at 3:1 at least on its page; the map's rows are logged, each theme's.
func TestTheTerminalsGlobeMapWearsItsGroups(t *testing.T) {
	const yellow = 0xedc948 // illegible on sepia's paper as it is: onGround darkens it there
	kb := groupsKB(t, "[[globemap.groups]]\nquery = \"path:notes/\"\ncolour = \"#edc948\"\n")
	d := startManaged(t, map[string]string{"kb": kb})
	r := runTUISized(t, NewSession(d.sock, nil), Options{}, 120, 36)
	r.waitAttached(t, "kb")
	r.waitListed(t, 2)
	// the store's preferences first: they carry a theme, which applyPrefs switches to as they land,
	// so a switch made before them would be undone under the steps below
	r.prefsIn(t)
	if !strings.Contains(string(withRelationViews(layout, false)), `onTriggered: App.toggleGlobeMap()`) {
		t.Fatal("a terminal's Relations menu has no GlobeMap item")
	}
	g := onLoop(r, func() *cellGlobeMap {
		for _, f := range r.h.reg.Features {
			if g, ok := f.(*cellGlobeMap); ok {
				return g
			}
		}
		return nil
	})
	if g == nil {
		t.Fatal("a terminal host has no cell GlobeMap registered")
	}
	onLoop(r, func() bool { r.h.switchTheme("sepia"); return true })
	r.s.WaitFor(t, "sepia", func(string) bool { return onLoop(r, func() string { return r.h.theme }) == "sepia" })
	onLoop(r, func() bool { g.toggle(); return true })
	r.s.WaitFor(t, "the GlobeMap built in the workspace's groups", func(string) bool {
		return onLoop(r, func() bool { return g.open && g.cur != nil && len(g.cur.m.Nodes) == 2 && g.groupsWS == "kb" })
	})
	var seen []tuicore.CellColor
	for _, theme := range []string{"sepia", "dark"} {
		if theme != "sepia" {
			onLoop(r, func() bool { r.h.switchTheme(theme); return true })
			r.s.WaitFor(t, theme, func(string) bool { return onLoop(r, func() string { return r.h.theme }) == theme })
		}
		var fg, bg tuicore.CellColor
		r.s.WaitFor(t, theme+": Beta's dot in its group's colour, its name beside it", func(string) bool {
			snap := r.s.Backend.Snapshot()
			x, y, ok := find(snap, "Beta")
			if !ok {
				return false
			}
			// the name is " Beta " right of the dot, or left of it at the view's right edge
			for _, dx := range []int{x - 2, x + len("Beta") + 1} {
				if dx < 0 || dx >= len(snap[y]) {
					continue
				}
				c := snap[y][dx]
				if r := []rune(c.Content + " ")[0]; r < 0x2800 || r > 0x28ff {
					continue
				}
				fg, bg = c.Attrs.FG, snap[y][x].Attrs.BG
				if bg.Kind != tuicore.CellColorRGB {
					return false
				}
				want := onGround(hexColour(yellow), color.NRGBA{R: bg.R, G: bg.G, B: bg.B, A: 0xff})
				if theme == "sepia" && want == hexColour(yellow) {
					return false // a frame still on a page that leaves the yellow as it is: not sepia's paper yet
				}
				if fg == (tuicore.CellColor{Kind: tuicore.CellColorRGB, R: want.R, G: want.G, B: want.B}) && (len(seen) == 0 || bg != seen[0]) {
					return true
				}
			}
			return false
		})
		if c := contrast(color.NRGBA{R: fg.R, G: fg.G, B: fg.B, A: 0xff}, color.NRGBA{R: bg.R, G: bg.G, B: bg.B, A: 0xff}); c < 3 {
			t.Errorf("%s: Beta's dot %v on its page %v is %.2f:1; want 3:1 at least", theme, fg, bg, c)
		}
		seen = append(seen, bg)
		t.Logf("%s: Beta's dot %v on %v; the screen:\n%s", theme, fg, bg, r.s)
	}
	// f: every link, the title saying so
	r.keys(t, key('f'))
	r.s.WaitFor(t, "every link followed", func(s string) bool { return strings.Contains(s, "every link") })
}

// TestATerminalsGlobeMapSettingsEditItsGroups: Relations › GlobeMap settings… opens in a terminal,
// the window's dialog less the rows cells can't use (dots, pace, colours): its groups listed, and an
// edit there is written to the graph file and drawn at once; turning its names off leaves only the
// selected document's name on the map.
func TestATerminalsGlobeMapSettingsEditItsGroups(t *testing.T) {
	kb := groupsKB(t, "[[globemap.groups]]\nquery = \"path:notes/\"\ncolour = \"#edc948\"\n")
	d := startManaged(t, map[string]string{"kb": kb})
	r := runTUISized(t, NewSession(d.sock, nil), Options{}, 120, 36)
	r.waitAttached(t, "kb")
	r.waitListed(t, 2)
	if !strings.Contains(string(withRelationViews(layout, false)), `onTriggered: App.openGlobeSettings()`) {
		t.Fatal("a terminal's Relations menu has no GlobeMap settings… item")
	}
	g := onLoop(r, func() *cellGlobeMap {
		for _, f := range r.h.reg.Features {
			if g, ok := f.(*cellGlobeMap); ok {
				return g
			}
		}
		return nil
	})
	onLoop(r, func() bool { g.toggle(); return true })
	r.s.WaitFor(t, "the GlobeMap built in its groups", func(s string) bool {
		return strings.Contains(s, "Beta") && onLoop(r, func() bool { return g.cur != nil && g.groupsWS == "kb" })
	})
	onLoop(r, func() bool { g.openSettings(); return true })
	r.s.WaitFor(t, "the settings, the group listed", func(s string) bool {
		return strings.Contains(s, "GlobeMap settings") && strings.Contains(s, "path:notes/  #edc948")
	})
	if s := r.s.String(); strings.Contains(s, "dot size") || strings.Contains(s, "settling speed") {
		t.Fatalf("a terminal's GlobeMap settings show a window's rows:\n%s", s)
	}
	t.Logf("the settings:\n%s", r.s)
	r.keys(t, key('q'))
	r.s.WaitFor(t, "the settings closed", func(s string) bool { return !strings.Contains(s, "GlobeMap settings") })

	onLoop(r, func() bool { g.editor.setColour("#123456"); return true })
	file := func() string { b, _ := os.ReadFile(graphFile(kb)); return string(b) }
	r.s.WaitFor(t, "the edit written", func(string) bool { return strings.Contains(file(), `colour = "#123456"`) })
	if c := onLoop(r, func() color.NRGBA { return g.groups.list[0].Colour }); c != hexColour(0x123456) {
		t.Fatalf("the map's group colour %v; want the edit's", c)
	}

	// names off: the selected document's name alone
	sel := onLoop(r, func() string { return g.cur.m.label(g.selected) })
	other := "Beta"
	if sel == "Beta" {
		other = "Alpha"
	}
	onLoop(r, func() bool { core{r.h}.SetPref(prefGlobeNames, "false"); return true })
	r.s.WaitFor(t, "only the selected document named", func(s string) bool {
		return strings.Contains(s, " "+sel+" ") && !strings.Contains(s, " "+other+" ")
	})
}

// TestTheGlobeMapSettingsListACachedWorkspacesGroups: b's groups read, then a entered and b again
// (the editor cleared for each), the settings opened in b list b's groups, read already, and take
// an edit: never an empty editor that refuses every edit.
func TestTheGlobeMapSettingsListACachedWorkspacesGroups(t *testing.T) {
	kb := func(folder string) string {
		root := t.TempDir()
		for _, p := range []string{folder, ".autodoc"} {
			if err := os.MkdirAll(filepath.Join(root, p), 0o700); err != nil {
				t.Fatal(err)
			}
		}
		if err := os.WriteFile(filepath.Join(root, folder, "x.md"), []byte("# x\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		graph := "[[globemap.groups]]\nquery = \"path:" + folder + "/\"\ncolour = \"#4e79a7\"\n"
		if err := os.WriteFile(graphFile(root), []byte(graph), 0o600); err != nil {
			t.Fatal(err)
		}
		return root
	}
	a, b := kb("alpha"), kb("beta")
	d := startManaged(t, map[string]string{"a": a, "b": b})
	r := runTUISized(t, NewSession(d.sock, nil), Options{}, 120, 36)
	r.waitAttached(t, "a")
	g := onLoop(r, func() *cellGlobeMap {
		for _, f := range r.h.reg.Features {
			if c, ok := f.(*cellGlobeMap); ok {
				return c
			}
		}
		return nil
	})
	enter := func(ws string) {
		r.h.p.Post(func() { r.h.enter(ws) })
		r.waitWithin(t, ws+" in use", daemonWait, func() bool { return onLoop(r, func() bool { return r.h.ws == ws }) })
	}
	listedIn := func(ws string) func() bool {
		return func() bool {
			return onLoop(r, func() bool { return g.editor.ws == ws && !g.editor.loading && len(g.editor.list) == 1 })
		}
	}
	enter("b")
	onLoop(r, func() bool { g.openSettings(); return true })
	r.waitWithin(t, "b's groups listed", daemonWait, listedIn("b"))
	r.keys(t, key('q'))
	enter("a")
	enter("b")
	onLoop(r, func() bool { g.openSettings(); return true })
	r.waitWithin(t, "b's groups listed again on its revisit", daemonWait, listedIn("b"))
	onLoop(r, func() bool { g.editor.setColour("#123456"); return true })
	file := func() string { bs, _ := os.ReadFile(graphFile(b)); return string(bs) }
	r.waitWithin(t, "the edit written to b", daemonWait, func() bool { return strings.Contains(file(), `colour = "#123456"`) })
}
