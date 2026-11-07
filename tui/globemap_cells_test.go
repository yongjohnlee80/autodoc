package tui

import (
	"image/color"
	"strings"
	"testing"

	graphlayout "github.com/yongjohnlee80/golib/graph/layout"
	tuicore "github.com/yongjohnlee80/golib/tui"
)

// smallGlobe is a small placed map: a.md in the middle linked to b, c, d and e around it.
func smallGlobe() *cellGlobe {
	m := relMap{
		Nodes: []mapNode{{Path: "a.md", Title: "Alpha"}, {Path: "b.md", Title: "Beta"}, {Path: "c.md", Title: "Gamma"},
			{Path: "d.md", Title: "Delta"}, {Path: "e.md", Title: "Epsilon"}},
		Edges: []mapEdge{{Src: 0, Dst: 1, Kind: "related"}, {Src: 0, Dst: 2, Kind: "related"}, {Src: 0, Dst: 3, Kind: "supersedes"}, {Src: 0, Dst: 4, Kind: "wikilink"}},
	}
	at := []graphlayout.Point{{X: 0, Y: 0}, {X: -40, Y: -10}, {X: 40, Y: -12}, {X: -35, Y: 20}, {X: 30, Y: 25}}
	return &cellGlobe{ws: "kb", m: m, at: at, bounds: graphlayout.Rect{X: -40, Y: -12, W: 80, H: 37}}
}

// builtGlobe is a terminal's GlobeMap with g installed, its view w×h cells, as a load installs a
// build.
func builtGlobe(t *testing.T, gl *cellGlobe, w, h int) (*cellGlobeMap, *cellGlobeView) {
	t.Helper()
	host := newHost(NewSession("unused", nil), Options{})
	var g *cellGlobeMap
	for _, f := range host.relationViewFeatures() {
		if c, ok := f.(*cellGlobeMap); ok {
			g = c
		}
	}
	if g == nil {
		t.Fatal("a terminal host has no cell GlobeMap")
	}
	v := g.newView().(*cellGlobeView)
	v.cells = tuicore.Size{W: w, H: h}
	g.open, g.cur, g.selected = true, gl, hub(gl.m)
	g.fit()
	return g, v
}

// TestTheCellGlobeMapsKeysReachEveryDocument: from the hub, the arrows and h j k l alike reach every
// document of the map, the view following; Enter opens the selected one, in the map's workspace.
func TestTheCellGlobeMapsKeysReachEveryDocument(t *testing.T) {
	g, v := builtGlobe(t, smallGlobe(), 60, 20)
	if g.selected != 0 {
		t.Fatalf("the selection starts at %d, want the hub (a.md)", g.selected)
	}
	for _, keys := range [][]tuicore.KeyEvent{
		{press(tuicore.KeyLeft, ""), press(tuicore.KeyRight, ""), press(tuicore.KeyUp, ""), press(tuicore.KeyDown, "")},
		{press('h', "h"), press('l', "l"), press('k', "k"), press('j', "j")},
	} {
		seen := map[int]bool{0: true}
		for queue := []int{0}; len(queue) > 0; queue = queue[1:] {
			for _, k := range keys {
				g.selected = queue[0]
				if !v.HandleEvent(k) {
					t.Fatalf("%+v was not taken", k)
				}
				if s := g.selected; !seen[s] {
					seen[s] = true
					queue = append(queue, s)
				}
			}
		}
		if len(seen) != len(g.cur.m.Nodes) {
			t.Errorf("keys %q reach %d of the %d documents from the hub", keys[0].Text, len(seen), len(g.cur.m.Nodes))
		}
	}
	var opened []string
	g.opens = func(ws, path string) { opened = append(opened, ws+":"+path) }
	g.selected = 2
	v.HandleEvent(press(tuicore.KeyEnter, ""))
	if len(opened) != 1 || opened[0] != "kb:c.md" {
		t.Errorf("Enter opened %v, want kb:c.md", opened)
	}
}

// TestTheCellGlobeMapsViewKeys: + zooms in and - out, 0 fits the whole map again, H J K L pan, and g
// (not l, a direction) shows and hides the legend.
func TestTheCellGlobeMapsViewKeys(t *testing.T) {
	g, v := builtGlobe(t, smallGlobe(), 60, 20)
	s0, cx0 := g.scale(), g.cx
	v.HandleEvent(press('+', "+"))
	if g.scale() <= s0 {
		t.Fatalf("+ left the scale at %v (was %v)", g.scale(), s0)
	}
	v.HandleEvent(press('L', "L"))
	if g.cx <= cx0 {
		t.Fatalf("L left the view's centre at %v (was %v)", g.cx, cx0)
	}
	v.HandleEvent(press('0', "0"))
	if g.scale() != s0 || g.cx != cx0 {
		t.Fatalf("0 left the scale %v and centre %v; want the fit, %v and %v", g.scale(), g.cx, s0, cx0)
	}
	before := g.selected
	v.HandleEvent(press('g', "g"))
	if !g.noLegend || g.selected != before {
		t.Fatalf("g: legend hidden %v, selection %d (was %d); want the legend hidden, the selection kept", g.noLegend, g.selected, before)
	}
	v.HandleEvent(press('g', "g"))
	if g.noLegend {
		t.Fatal("g again left the legend hidden")
	}
}

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
	if !strings.Contains(string(withRelationViews(layout, false)), `onTriggered: App.toggleGlobeMap()`) {
		t.Fatal("a terminal's Relations menu has no GlobeMap item")
	}
	g := onLoop(r, func() *cellGlobeMap {
		for _, f := range r.h.reg.features {
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
