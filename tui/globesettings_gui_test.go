//go:build gui

package tui

import (
	"context"
	"errors"
	"fmt"
	"image/color"
	"math"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	graphlayout "github.com/yongjohnlee80/golib/graph/layout"
	"github.com/yongjohnlee80/golib/gui"
	pm "github.com/yongjohnlee80/golib/parse/mermaid"
)

// valuesPrefs is preferences of the features' values v alone.
func valuesPrefs(v map[string]string) prefs { return prefs{values: v} }

func rgb(v uint32) color.NRGBA {
	return color.NRGBA{R: uint8(v >> 16), G: uint8(v >> 8), B: uint8(v), A: 0xff}
}

// The look is the theme's colours with the user's over them: a document no group matches, the
// links and the ring; a field that does not read is the theme's. The names, the dots and the pace
// are each their setting's. A dot is its first matching group's colour, else no group's.
func TestTheGlobeLookIsItsSettingsOverTheTheme(t *testing.T) {
	pal := defaultPalette()
	def := newGlobeLook(prefs{}, pal)
	if !def.names || def.dot != 1 || def.steps != 2 || def.ring != pal.superseded || def.other != pal.text ||
		def.edges[edgeBody] != mixNRGBA(pal.background, pal.muted, 0.8) {
		t.Fatalf("no settings: %+v; want the theme's colours, names, medium dots, gentle pace", def)
	}
	l := newGlobeLook(valuesPrefs(map[string]string{
		prefGlobeNames: "false", prefGlobeDots: "large", prefGlobeSpeed: "slow",
		prefGlobeColour + "other": "#445566", prefGlobeColour + "link.body": "#778899",
		prefGlobeColour + "link.relation": "#12", // reads not: the theme's
		prefGlobeColour + "superseded":    "#aabbcc",
	}), pal)
	if l.names || l.dot != 1.4 || l.steps != 1 || l.ring != rgb(0xaabbcc) || l.edges[edgeBody] != rgb(0x778899) ||
		l.edges[edgeRelation] != def.edges[edgeRelation] || l.other != rgb(0x445566) {
		t.Fatalf("settings: names %v, dot %v, steps %v, ring %v, edges %v, other %v", l.names, l.dot, l.steps, l.ring, l.edges, l.other)
	}
	gs := newRelGroups([]relGroup{{Query: "tag:#project", Colour: rgb(0x010101)}, {Query: "type:adr", Colour: rgb(0x020202)}})
	for _, c := range []struct {
		n    mapNode
		want color.NRGBA
	}{
		{mapNode{Path: "a.md", Type: "adr"}, rgb(0x020202)},
		{mapNode{Path: "a.md", Type: "adr", Tags: []string{"project"}}, rgb(0x010101)}, // the first group wins
		{mapNode{Path: "a.md", Type: "memo"}, rgb(0x445566)},
	} {
		if got := l.colourOf(c.n, &gs); got != c.want {
			t.Errorf("%+v: %v; want %v", c.n, got, c.want)
		}
	}
	if got := l.colourOf(mapNode{Type: "adr"}, nil); got != l.other {
		t.Errorf("no groups read yet: %v; want no group's colour", got)
	}
}

type fakeThemes map[string]map[string]string

func (fakeThemes) Theme() string { return "page" }
func (f fakeThemes) ThemeValues(name string) (map[string]string, error) {
	if v, ok := f[name]; ok {
		return v, nil
	}
	return nil, errors.New("no theme " + name)
}
func (fakeThemes) ThemeNames() []string  { return nil }
func (fakeThemes) ThemeLabels() []string { return nil }

// The GlobeMap wears the terminals' theme unless it chose its own: the page's, or one named. One
// that does not read is the page's.
func TestTheGlobeMapsThemeIsTheTerminalsUnlessChosen(t *testing.T) {
	ts := fakeThemes{"page": {"document.base": "#000001"}, "term": {"document.base": "#000002"}, "own": {"document.base": "#000003"}}
	for _, c := range []struct {
		term, globe, want string
	}{
		{"", "", "#000001"},         // the terminals' theme, the page's
		{"term", "", "#000002"},     // the terminals' theme, one chosen
		{"term", "page", "#000001"}, // the page's, chosen
		{"term", "own", "#000003"},  // one of its own
		{"term", "gone", "#000001"}, // one that does not read: the page's
		{"gone", "terminal", "#000001"},
	} {
		p := valuesPrefs(map[string]string{prefGlobeTheme: c.globe})
		p.termTheme = c.term
		v, ok := globeThemeValues(p, ts)
		if !ok || v["document.base"] != c.want {
			t.Errorf("terminals %q, GlobeMap %q: %v %v; want %s", c.term, c.globe, v, ok, c.want)
		}
	}
}

// The pace is the settings': slow takes a picture each step, fast one every eight, so a slow map
// shows several times the pictures to settle. Either way its progress counts the steps run.
func TestTheGlobeMapSettlesAtItsSpeed(t *testing.T) {
	m := relMapOf(30)
	pictures := func(speed string) int {
		in := globeForceInput(m)
		sim, err := graphlayout.NewSimulation(in, globeLimits)
		if err != nil {
			t.Fatal(err)
		}
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		views := make(chan globeViewState, 1)
		views <- globeViewState{size: gui.Size{W: 640, H: 480}, hover: -1,
			look: newGlobeLook(valuesPrefs(map[string]string{prefGlobeSpeed: speed}), defaultPalette())}
		n := 0
		push := func(shot *globeShot, _ func(gui.Canvas)) globeReceipt {
			if n++; shot.f.final || n > 100000 {
				cancel()
			}
			return doneReceipt(gui.Drawn)
		}
		var progress []int
		stepped := func(done int) { progress = append(progress, done) }
		if err := settle(ctx, sim, "kb", m, push, views, stepped, func(*globeShot) {}, func(*globeShot) {}, func() {}); err != nil {
			t.Fatal(err)
		}
		// the progress counts every step run, rising to the whole
		if len(progress) == 0 || progress[len(progress)-1] < globeSteps || !slices.IsSorted(progress) {
			t.Errorf("%s: progress %v; want rising to %d", speed, progress, globeSteps)
		}
		return n
	}
	slow, fast := pictures("slow"), pictures("fast")
	if slow < 4*fast {
		t.Errorf("slow settled in %d pictures, fast in %d; want slow several times fast's", slow, fast)
	}
}

// One fill a colour: groups of one colour share a fill, and a group every document wears is the one
// fill.
func TestTheGlobeMapsDotsAreOneFillAColour(t *testing.T) {
	m := relMapOf(24)
	for i := range m.Nodes {
		m.Nodes[i].Path = "notes/" + m.Nodes[i].Path
	}
	gl := settledGlobe(t, m)
	same := newRelGroups([]relGroup{{Query: "type:adr", Colour: rgb(0x101010)}, {Query: "type:review", Colour: rgb(0x101010)}})
	if n := len(dotsAt(globeDots{}, gl, defaultLook, &same, 1, 1).fills); n != 2 {
		t.Errorf("ADRs and reviews one colour, the rest no group's: %d fills; want 2", n)
	}
	all := newRelGroups([]relGroup{{Query: "path:notes/", Colour: rgb(0x202020)}})
	if d := dotsAt(globeDots{}, gl, defaultLook, &all, 1, 1); len(d.fills) != 1 || d.fills[0].c != rgb(0x202020) {
		t.Errorf("one group for every document: %d fills", len(d.fills))
	}
	if d := dotsAt(globeDots{}, gl, defaultLook, nil, 1, 1); len(d.fills) != 1 || d.fills[0].c != defaultLook.other {
		t.Errorf("no groups: %d fills; want one, no group's colour", len(d.fills))
	}
}

// The settings, as the user sets them: the dialog opens from Relations, a colour that does not read
// says so and is not kept, one that does is kept and drawn, and the map opens with the relations,
// following the links chosen once another is. A terminals' theme set from their own menu recolours the GlobeMap too.
func TestTheGlobeMapsSettingsApplyAtOnce(t *testing.T) {
	d := startDaemon(t, map[string][]string{"kb": {"a.md", "[[b]]\n", "b.md", "bbb\n"}})
	r := windowed(t, d)
	var g *globeMap
	onLoop(r, func() bool {
		g = r.h.relationViews[0].(*globeMap)
		g.openSettings()
		return true
	})
	r.s.WaitForText(t, "GlobeMap settings")
	r.s.WaitForText(t, "settling speed")

	onLoop(r, func() bool { g.setColour("other", "#12345"); return true })
	if v, msg := onLoop(r, func() string { return r.h.prefs.Value(prefGlobeColour + "other") }), onLoop(r, func() string {
		v, _ := r.h.p.Tree().Source("App.globeSettingsError")
		return v.Raw
	}); v != "" || !strings.Contains(msg, "#rrggbb") {
		t.Fatalf("a colour that does not read: kept %q, said %q", v, msg)
	}
	before := onLoop(r, func() *globeLook { return g.currentLook() })
	onLoop(r, func() bool { g.setColour("other", " #123456 "); return true })
	l := onLoop(r, func() *globeLook { return g.currentLook() })
	if kept := onLoop(r, func() string { return r.h.prefs.Value(prefGlobeColour + "other") }); l == before ||
		l.other != rgb(0x123456) || kept != "#123456" {
		t.Fatalf("a colour that reads: look %v, kept %q", l.other, kept)
	}

	onLoop(r, func() bool { core{r.h}.TogglePanel(globeMapPanelID); return true })
	r.s.WaitForText(t, "kb · 2 documents, 0 links · relations") // the relations by default
	r.s.WaitFor(t, "the settled notice", func(string) bool {    // its progress toast ends saying so, kept
		return onLoop(r, func() bool {
			return slices.ContainsFunc(r.h.noticeLog.items, func(n notice) bool { return n.text == "GlobeMap: kb settled · 2 documents, 0 links" })
		})
	})
	onLoop(r, func() bool { r.h.choosePrefNamed(prefGlobeFilter, 1); return true })
	r.s.WaitForText(t, "kb · 2 documents, 1 links · every link") // the open map follows the choice
	r.s.WaitFor(t, "the GlobeMap has the keyboard", func(string) bool { return r.focusedOn(globeMapViewID) })
	r.keys(t, key('l')) // the legend hidden, and shown again
	r.s.WaitFor(t, "l hid the legend", func(string) bool { return onLoop(r, func() bool { return g.noKey }) })
	r.keys(t, key('l'))
	r.s.WaitFor(t, "l showed the legend", func(string) bool { return onLoop(r, func() bool { return !g.noKey }) })
	r.keys(t, key('f')) // this opening's own: body links
	r.s.WaitForText(t, "kb · 2 documents, 1 links · body links")
	onLoop(r, func() bool { core{r.h}.TogglePanel(globeMapPanelID); return true })
	r.s.WaitFor(t, "the GlobeMap closed", func(string) bool { return !r.panelIsOpen(globeMapPanelID) })
	onLoop(r, func() bool { core{r.h}.TogglePanel(globeMapPanelID); return true })
	r.s.WaitForText(t, "kb · 2 documents, 1 links · every link") // opened again: the settings' choice

	was := onLoop(r, func() color.NRGBA { return g.currentLook().pal.background })
	onLoop(r, func() bool { core{r.h}.SetPref(prefTermTheme, "light"); return true })
	if now := onLoop(r, func() color.NRGBA { return g.currentLook().pal.background }); now == was {
		t.Error("the terminals' theme set alone left the GlobeMap in the old one")
	}
}

// Names off: no picture names a document, whatever is in view; the hovered one is still named.
func TestTheGlobeMapNamesNoneWhenItsSettingsSaySo(t *testing.T) {
	gl := settledGlobe(t, relMapOf(12))
	view := globeView800
	if len(pictureOf(gl, view, globeDots{}).nameRows(view.size)) == 0 {
		t.Fatal("names on: none drawn")
	}
	view.look = newGlobeLook(valuesPrefs(map[string]string{prefGlobeNames: "false"}), defaultPalette())
	p := pictureOf(gl, view, globeDots{})
	if rows := p.nameRows(view.size); rows != nil {
		t.Errorf("names off: %d rows drawn", len(rows))
	}
	p.hover = 0
	rc := gui.NewRecordingCanvas(view.size, gui.Size{W: 8, H: 16})
	p.paint(rc)
	if n := countOps(rc, "DrawText"); n != 1 {
		t.Errorf("names off, one hovered: %d texts; want the hovered name alone", n)
	}
}

// The GlobeMap's legend names what its marks stand for: each group the map's documents wear, in
// the groups' order, then no group's colour, the superseded ring, only the links its filter
// follows, and the dots' size. It is drawn on the view's own paint, a text a row, and l hides it.
func TestTheGlobeMapsLegendIsItsGroups(t *testing.T) {
	l := defaultLook
	m := relMap{Nodes: []mapNode{{Path: "a.md", Type: "adr"}, {Path: "notes/b.md"}, {Path: "c.md", Tags: []string{"x"}}}}
	gs := newRelGroups([]relGroup{{Query: "type:adr"}, {Query: "type:pr"}, {Query: "path:notes/"}, {Query: "#x"}})
	texts := func(rows []legendRow) []string {
		var out []string
		for _, r := range rows {
			out = append(out, r.text)
		}
		return out
	}
	common := []string{"adr", "notes/", "#x", "any other document", "superseded"} // type:pr worn by none
	for filter, links := range map[int][]string{
		0: {"supersedes", "other relation"},              // relations
		1: {"body link", "supersedes", "other relation"}, // every link
		2: {"body link"},                                 // body links
	} {
		g := &globeMap{filter: filter, groups: &gs, cur: &globe{m: m}}
		want := append(append(slices.Clone(common), links...), "larger: more links")
		if got := texts(g.legend(l)); !slices.Equal(got, want) {
			t.Errorf("%s: legend %q; want %q", globeFilters[filter].name, got, want)
		}
	}
	g := &globeMap{groups: &gs, cur: &globe{m: m}}
	rows := g.legend(l)
	rc := gui.NewRecordingCanvas(gui.Size{W: 800, H: 600}, gui.Size{W: 8, H: 16})
	globeOverlay{pal: l.pal, legend: rows}.paint(rc)
	if n := countOps(rc, "DrawText"); n != len(rows) {
		t.Errorf("the legend drew %d texts for %d rows", n, len(rows))
	}
	small := gui.NewRecordingCanvas(gui.Size{W: 120, H: 90}, gui.Size{W: 8, H: 16})
	globeOverlay{pal: l.pal, legend: rows}.paint(small)
	if n := countOps(small, "DrawText"); n != 0 {
		t.Errorf("a view too small for the legend drew %d of its texts", n)
	}
	// many groups worn: groupLegendRows of them, then how many more; none at all says how to have some
	var many []relGroup
	var mm relMap
	for i := range groupLegendRows + 3 {
		many = append(many, relGroup{Query: fmt.Sprintf("type:t%d", i)})
		mm.Nodes = append(mm.Nodes, mapNode{Type: fmt.Sprintf("t%d", i)})
	}
	mgs := newRelGroups(many)
	got := texts(groupLegend(&mgs, mm, func(relGroup) func(gui.Canvas, gui.Rect) { return func(gui.Canvas, gui.Rect) {} }))
	if len(got) != groupLegendRows+1 || got[groupLegendRows] != "… 3 more groups" {
		t.Errorf("many groups: %q", got)
	}
	if none := texts(groupLegend(nil, mm, nil)); len(none) != 1 || !strings.Contains(none[0], "no groups") {
		t.Errorf("no groups: %q", none)
	}
}

// The Mindmap's legend is its groups the map's documents wear, each its outline in its colour, then
// any other document's box.
func TestTheMindmapsLegendIsItsGroups(t *testing.T) {
	th := mindTheme(defaultPalette())
	m := relMap{Nodes: []mapNode{{Type: "adr"}, {Type: "review"}}}
	gs := newRelGroups([]relGroup{{Query: "type:adr", Colour: rgb(0xff0000), Shape: pm.MindDefault},
		{Query: "type:review", Colour: rgb(0x0000ff), Shape: pm.MindHexagon}, {Query: "type:pr"}})
	rows := mindLegend(th, &gs, m)
	var texts []string
	for _, r := range rows {
		texts = append(texts, r.text)
	}
	if !slices.Equal(texts, []string{"adr", "review", "any other document"}) {
		t.Fatalf("legend %q", texts)
	}
	draw := func(i int) *gui.RecordingCanvas {
		rc := gui.NewRecordingCanvas(gui.Size{W: 40, H: 20}, gui.Size{W: 8, H: 16})
		rows[i].mark(rc, gui.Rect{W: legendMarkW, H: legendMarkH})
		return rc
	}
	if rc := draw(0); countOps(rc, "FillRRect") != 1 || countOps(rc, "FillPath") != 0 {
		t.Error("adr's group a box: its legend mark is not one")
	}
	if rc := draw(1); countOps(rc, "FillPath") != 1 || countOps(rc, "FillRRect") != 0 {
		t.Error("review's group a hexagon: its legend mark is not one")
	}
}

// The GlobeMap's simulation keeps its unlinked documents near the linked ones (golib's Reach and
// LinkPull): their median distance from the core's centre is within twice the core's r90, where
// pushed by every document they would ring it far out.
func TestTheGlobeMapsForceIsSpacedByReach(t *testing.T) {
	const core, loose = 600, 200
	var m relMap
	for i := range core + loose {
		m.Nodes = append(m.Nodes, mapNode{Path: fmt.Sprintf("d%04d.md", i)})
	}
	for i := 1; i < core; i++ {
		m.Edges = append(m.Edges, mapEdge{Src: i, Dst: (i*37 + 11) % i}, mapEdge{Src: i, Dst: i - 1})
	}
	gl := settledGlobe(t, m)
	var cx, cy float64
	for _, p := range gl.at[:core] {
		cx, cy = cx+float64(p.X), cy+float64(p.Y)
	}
	cx, cy = cx/core, cy/core
	radii := func(ps []graphlayout.Point) []float64 {
		var out []float64
		for _, p := range ps {
			out = append(out, math.Hypot(float64(p.X)-cx, float64(p.Y)-cy))
		}
		slices.Sort(out)
		return out
	}
	in, out := radii(gl.at[:core]), radii(gl.at[core:])
	if ratio := out[len(out)/2] / in[len(in)*9/10]; ratio > 2 {
		t.Errorf("the unlinked documents' median distance is %.2f times the core's r90; want within 2", ratio)
	}
}

// A view's groups are the workspace's graph file's: with none, the first view to open writes them,
// a group each schema type then each folder, each a colour; its settings' edits are written there
// too, and the other view keeps a list of its own (with outlines) in the same file.
func TestTheViewsGroupsAreTheWorkspacesGraphFile(t *testing.T) {
	kb := fileDir(t, "schema.yaml", "version: 2\ndiscriminator: type\ncommon:\n  type: {type: string, enum: [note, adr]}\ntypes:\n  note: {}\n  adr: {}\n",
		"a.md", "---\ntype: adr\n---\n[[b]]\n")
	if err := os.MkdirAll(filepath.Join(kb, "notes"), 0o700); err != nil {
		t.Fatal(err)
	}
	for name, text := range map[string]string{"b.md": "---\ntype: memo\n---\nbbb\n", "c.md": "ccc\n"} {
		if err := os.WriteFile(filepath.Join(kb, "notes", name), []byte(text), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	d := startManaged(t, map[string]string{"kb": kb})
	r := runTUI(t, NewSession(d.sock, nil), Options{GUI: true})
	r.s.WaitForText(t, "· kb")
	r.waitListed(t, 3)
	if _, err := (core{r.h}).Call(context.Background(), "workspace.set_schema", "kb", "schema.yaml"); err != nil {
		t.Fatal(err)
	}
	var g *globeMap
	var m *mindmap
	onLoop(r, func() bool {
		g, m = r.h.relationViews[0].(*globeMap), r.h.relationViews[1].(*mindmap)
		core{r.h}.TogglePanel(globeMapPanelID)
		return true
	})
	queries := func(list []relGroup) []string {
		var out []string
		for _, x := range list {
			out = append(out, x.Query)
		}
		return out
	}
	r.s.WaitFor(t, "the GlobeMap's groups", func(string) bool { return onLoop(r, func() bool { return g.groups != nil }) })
	if got := onLoop(r, func() []string { return queries(g.groups.list) }); !slices.Equal(got, []string{"type:note", "type:adr", "path:notes/"}) {
		t.Fatalf("groups %q; want the schema's types, then the folder", got)
	}
	file := func() string { b, _ := os.ReadFile(filepath.Join(kb, ".autodoc", "graph.toml")); return string(b) }
	r.s.WaitFor(t, "the graph file written", func(string) bool { return strings.Contains(file(), `query = "path:notes/"`) })
	if f := file(); !strings.Contains(f, "[[globemap.groups]]") || strings.Contains(f, "[[mindmap.groups]]") || !strings.Contains(f, "palette = [") {
		t.Fatalf("the graph file:\n%s", f)
	}
	// an edit in the settings: written, and drawn
	onLoop(r, func() bool { g.editor.moved(1); g.editor.setColour("#123456"); return true })
	r.s.WaitFor(t, "the edit written", func(string) bool { return strings.Contains(file(), `colour = "#123456"`) })
	if c := onLoop(r, func() color.NRGBA { return g.groups.list[1].Colour }); c != rgb(0x123456) {
		t.Errorf("type:adr's colour in the view: %v", c)
	}
	onLoop(r, func() bool { g.editor.setQuery("nope"); return true })
	if q := onLoop(r, func() string { return g.groups.list[1].Query }); q != "type:adr" {
		t.Errorf("a query that does not read was kept: %q", q)
	}
	// the Mindmap: its own list, with outlines, in the same file
	r.openByPicker(t, "a.md")
	r.waitFile(t, "a.md")
	onLoop(r, func() bool { core{r.h}.TogglePanel(mindmapPanelID); return true })
	r.s.WaitFor(t, "the Mindmap's groups", func(string) bool { return strings.Contains(file(), "[[mindmap.groups]]") })
	if f := file(); !strings.Contains(f, `shape = "hexagon"`) {
		t.Errorf("the Mindmap's groups have no outlines:\n%s", f)
	}
	if got := onLoop(r, func() []string { return queries(m.groups.list) }); !slices.Equal(got, []string{"type:note", "type:adr", "path:notes/"}) {
		t.Errorf("the Mindmap's groups %q", got)
	}
}
