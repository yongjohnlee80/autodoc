//go:build gui

package relations

import (
	"context"
	"errors"
	"fmt"
	"image/color"
	"math"
	"slices"
	"strings"
	"testing"

	graphlayout "github.com/yongjohnlee80/golib/graph/layout"
	"github.com/yongjohnlee80/golib/gui"
	pm "github.com/yongjohnlee80/golib/parse/mermaid"
)

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
	l := newGlobeLook(mapPrefs(map[string]string{
		PrefGlobeNames: "false", prefGlobeDots: "large", prefGlobeSpeed: "slow",
		PrefGlobeColour + "other": "#445566", PrefGlobeColour + "link.body": "#778899",
		PrefGlobeColour + "link.relation": "#12", // reads not: the theme's
		PrefGlobeColour + "superseded":    "#aabbcc",
	}), pal)
	if l.names || l.dot != 1.4 || l.steps != 1 || l.ring != rgb(0xaabbcc) || l.edges[edgeBody] != rgb(0x778899) ||
		l.edges[edgeRelation] != def.edges[edgeRelation] || l.other != rgb(0x445566) {
		t.Fatalf("settings: names %v, dot %v, steps %v, ring %v, edges %v, other %v", l.names, l.dot, l.steps, l.ring, l.edges, l.other)
	}
	gs := newRelGroups([]Group{{Query: "tag:#project", Colour: rgb(0x010101)}, {Query: "type:adr", Colour: rgb(0x020202)}})
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

func (fakeThemes) ThemeNames() []string { return nil }

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
		p := mapPrefs(map[string]string{prefGlobeTheme: c.globe})
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
			look: newGlobeLook(mapPrefs(map[string]string{prefGlobeSpeed: speed}), defaultPalette())}
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
	same := newRelGroups([]Group{{Query: "type:adr", Colour: rgb(0x101010)}, {Query: "type:review", Colour: rgb(0x101010)}})
	if n := len(dotsAt(globeDots{}, gl, defaultLook, &same, 1, 1).fills); n != 2 {
		t.Errorf("ADRs and reviews one colour, the rest no group's: %d fills; want 2", n)
	}
	all := newRelGroups([]Group{{Query: "path:notes/", Colour: rgb(0x202020)}})
	if d := dotsAt(globeDots{}, gl, defaultLook, &all, 1, 1); len(d.fills) != 1 || d.fills[0].c != rgb(0x202020) {
		t.Errorf("one group for every document: %d fills", len(d.fills))
	}
	if d := dotsAt(globeDots{}, gl, defaultLook, nil, 1, 1); len(d.fills) != 1 || d.fills[0].c != defaultLook.other {
		t.Errorf("no groups: %d fills; want one, no group's colour", len(d.fills))
	}
}

// Names off: no picture names a document, whatever is in view; the hovered one is still named.
func TestTheGlobeMapNamesNoneWhenItsSettingsSaySo(t *testing.T) {
	gl := settledGlobe(t, relMapOf(12))
	view := globeView800
	if len(pictureOf(gl, view, globeDots{}).nameRows(view.size)) == 0 {
		t.Fatal("names on: none drawn")
	}
	view.look = newGlobeLook(mapPrefs(map[string]string{PrefGlobeNames: "false"}), defaultPalette())
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
	gs := newRelGroups([]Group{{Query: "type:adr"}, {Query: "type:pr"}, {Query: "path:notes/"}, {Query: "#x"}})
	texts := func(rows []legendRow) []string {
		var out []string
		for _, r := range legendIn(rows, englishf) {
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
		g := &GlobeMap{filter: filter, groups: &gs, cur: &globe{m: m}}
		want := append(append(slices.Clone(common), links...), "larger: more links")
		if got := texts(g.legend(l)); !slices.Equal(got, want) {
			t.Errorf("%s: legend %q; want %q", globeFilters[filter].name, got, want)
		}
	}
	g := &GlobeMap{groups: &gs, cur: &globe{m: m}}
	rows := legendIn(g.legend(l), englishf)
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
	var many []Group
	var mm relMap
	for i := range groupLegendRows + 3 {
		many = append(many, Group{Query: fmt.Sprintf("type:t%d", i)})
		mm.Nodes = append(mm.Nodes, mapNode{Type: fmt.Sprintf("t%d", i)})
	}
	mgs := newRelGroups(many)
	got := texts(groupLegend(&mgs, mm, func(Group) func(gui.Canvas, gui.Rect) { return func(gui.Canvas, gui.Rect) {} }))
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
	gs := newRelGroups([]Group{{Query: "type:adr", Colour: rgb(0xff0000), Shape: pm.MindDefault},
		{Query: "type:review", Colour: rgb(0x0000ff), Shape: pm.MindHexagon}, {Query: "type:pr"}})
	rows := legendIn(mindLegend(th, &gs, m), englishf)
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
