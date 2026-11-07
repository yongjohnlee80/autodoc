package tui

import (
	"fmt"
	"image/color"
	"math/rand/v2"
	"os"
	"strings"
	"testing"
	"time"

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
		for _, f := range r.h.reg.features {
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

// TestTheGlobeMapsNamesTakeTheirWidth: a name is drawn a grapheme cluster at a time, each at its
// own width: a CJK name's characters two cells apart, never one over another's second half, and a
// letter with a combining accent one cell.
func TestTheGlobeMapsNamesTakeTheirWidth(t *testing.T) {
	gl := &cellGlobe{ws: "kb", m: relMap{Nodes: []mapNode{{Path: "a.md", Title: "日本語の文書"}, {Path: "b.md", Title: "Café"}}},
		at: []graphlayout.Point{{X: 0, Y: 0}, {X: 100, Y: 40}}, bounds: graphlayout.Rect{W: 100, H: 40}}
	_, v := builtGlobe(t, gl, 80, 16)
	s := newCellMindSurface(80, 16)
	v.Render(s)
	found := 0
	for y := range s.h {
		for x := range s.w {
			switch s.text[y][x] {
			case "日":
				found++
				if s.text[y][x+1] != " " || s.text[y][x+2] != "本" || s.text[y][x+4] != "語" {
					t.Errorf("the CJK name's cells: %q; want each character two cells on", strings.Join(s.text[y][x:x+6], "|"))
				}
			case "é":
				found++
				if s.text[y][x-1] != "f" || s.text[y][x+1] != " " {
					t.Errorf("the accented letter's neighbours: %q; want it one cell, between f and the margin", strings.Join(s.text[y][x-1:x+2], "|"))
				}
			}
		}
	}
	if found != 2 {
		t.Fatalf("found %d of the 2 names' marks:\n%v", found, s.text)
	}
}

// TestALinkIsClippedToTheView: a link's segment is cut to the view's dots, a link wholly outside it
// is none, and one inside is itself.
func TestALinkIsClippedToTheView(t *testing.T) {
	for _, c := range []struct {
		x0, y0, x1, y1 int
		want           [4]int
		ok             bool
	}{
		{-1000, 5, 1000, 5, [4]int{0, 5, 19, 5}, true},
		{5, -1000, 5, 1000, [4]int{5, 0, 5, 9}, true},
		{2, 2, 8, 6, [4]int{2, 2, 8, 6}, true},
		{-100, -100, -10, -50, [4]int{}, false},
		{30, 0, 40, 9, [4]int{}, false},
	} {
		x0, y0, x1, y1, ok := clipLine(c.x0, c.y0, c.x1, c.y1, 20, 10)
		if ok != c.ok || (ok && [4]int{x0, y0, x1, y1} != c.want) {
			t.Errorf("%v: %v %v; want %v %v", c, [4]int{x0, y0, x1, y1}, ok, c.want, c.ok)
		}
	}
}

// TestAZoomedInGlobeMapDrawsQuickly: zoomed in 64 times on a map of fifty thousand long links, nearly
// all running far off the view, a paint takes well under a second's work: the links are clipped to
// the view before they are drawn.
func TestAZoomedInGlobeMapDrawsQuickly(t *testing.T) {
	const nodes, links = 2000, 50000
	gl := &cellGlobe{ws: "kb", bounds: graphlayout.Rect{X: -1000, Y: -1000, W: 2000, H: 2000}}
	rnd := rand.New(rand.NewPCG(7, 11))
	for i := range nodes {
		gl.m.Nodes = append(gl.m.Nodes, mapNode{Path: fmt.Sprintf("n%d.md", i)})
		gl.at = append(gl.at, graphlayout.Point{X: float32(rnd.IntN(2000) - 1000), Y: float32(rnd.IntN(2000) - 1000)})
	}
	for range links {
		gl.m.Edges = append(gl.m.Edges, mapEdge{Src: rnd.IntN(nodes), Dst: rnd.IntN(nodes), Kind: "related"})
	}
	g, v := builtGlobe(t, gl, 120, 40)
	g.zoom = 64
	s := newCellMindSurface(120, 40)
	start := time.Now()
	v.Render(s)
	if d := time.Since(start); d > 2*time.Second*raceFactor {
		t.Fatalf("a paint at 64x of %d links took %v; want well under a second's work", links, d)
	}
}
