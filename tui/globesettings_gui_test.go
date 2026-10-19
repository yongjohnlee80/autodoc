//go:build gui

package tui

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

// valuesPrefs is preferences of the features' values v alone.
func valuesPrefs(v map[string]string) prefs { return prefs{values: v} }

func rgb(v uint32) color.NRGBA {
	return color.NRGBA{R: uint8(v >> 16), G: uint8(v >> 8), B: uint8(v), A: 0xff}
}

// The look is the theme's colours with the user's over them. A dot's colour is the first rule it
// matches (a folder, a tag or a nested one of it), else its type's, given or the theme's; a field
// that does not read is the theme's. The links, the ring, the names, the dots and the pace are
// each their setting's.
func TestTheGlobeLookIsItsSettingsOverTheTheme(t *testing.T) {
	pal := defaultPalette()
	def := newGlobeLook(prefs{}, pal)
	if !def.names || def.dot != 1 || def.steps != 2 || def.ring != pal.superseded || def.types["adr"] != pal.types["adr"] ||
		def.other != pal.text || def.edges[edgeBody] != mixNRGBA(pal.background, pal.muted, 0.8) {
		t.Fatalf("no settings: %+v; want the theme's colours, names, medium dots, gentle pace", def)
	}
	l := newGlobeLook(valuesPrefs(map[string]string{
		prefGlobeNames: "false", prefGlobeDots: "large", prefGlobeSpeed: "slow",
		prefGlobeColour + "adr": "#112233", prefGlobeColour + "review": "#12", // the second reads not: the theme's
		prefGlobeColour + "other": "#445566", prefGlobeColour + "link.body": "#778899",
		prefGlobeColour + "superseded": "#aabbcc",
		prefGlobeRules:                 "#Project=#010101; notes/=#020202 ; #area/work=#030303",
	}), pal)
	if l.names || l.dot != 1.4 || l.steps != 1 || l.ring != rgb(0xaabbcc) || l.edges[edgeBody] != rgb(0x778899) ||
		l.edges[edgeRelation] != def.edges[edgeRelation] {
		t.Fatalf("settings: names %v, dot %v, steps %v, ring %v, edges %v", l.names, l.dot, l.steps, l.ring, l.edges)
	}
	for _, c := range []struct {
		n    mapNode
		want color.NRGBA
	}{
		{mapNode{Path: "a.md", Type: "adr"}, rgb(0x112233)},
		{mapNode{Path: "a.md", Type: "review"}, pal.types["review"]},
		{mapNode{Path: "a.md", Type: "memo"}, rgb(0x445566)},
		{mapNode{Path: "a.md", Type: "adr", Tags: []string{"project"}}, rgb(0x010101)},
		{mapNode{Path: "a.md", Tags: []string{"#project/alpha"}}, rgb(0x010101)}, // nested: its parent's
		{mapNode{Path: "a.md", Tags: []string{"projects"}}, rgb(0x445566)},       // not a nested tag
		{mapNode{Path: "notes/x.md", Type: "adr"}, rgb(0x020202)},
		{mapNode{Path: "notes/x.md", Tags: []string{"project"}}, rgb(0x010101)}, // the first rule wins
		{mapNode{Path: "notesy/x.md"}, rgb(0x445566)},
		{mapNode{Path: "b.md", Tags: []string{"area/work/q4"}}, rgb(0x030303)},
	} {
		if got := l.colourOf(c.n); got != c.want {
			t.Errorf("%+v: %v; want %v", c.n, got, c.want)
		}
	}
	if bad := newGlobeLook(valuesPrefs(map[string]string{prefGlobeRules: "#a=#010101; nope"}), pal); bad.rules != nil {
		t.Errorf("rules that do not read: %v; want none", bad.rules)
	}
}

// A rule is #tag=#rrggbb or folder/=#rrggbb; anything else says so.
func TestGlobeRulesRead(t *testing.T) {
	rules, err := parseGlobeRules(" #A=#0a0b0c ;\nkb/notes=#ffffff;; /x/=#000000 ")
	if err != nil || len(rules) != 3 || rules[0].tag != "a" || rules[1].folder != "kb/notes/" || rules[2].folder != "x/" ||
		rules[0].c != rgb(0x0a0b0c) {
		t.Fatalf("rules: %+v, %v", rules, err)
	}
	for _, bad := range []string{"#a", "#a=red", "=#000000", "#=#000000", "a=#0000000", "/=#000000"} {
		if _, err := parseGlobeRules(bad); err == nil {
			t.Errorf("%q read", bad)
		}
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

// One fill a colour: types given one colour share a fill, and a rule's documents are its fill.
func TestTheGlobeMapsDotsAreOneFillAColour(t *testing.T) {
	m := relMapOf(24)
	for i := range m.Nodes {
		m.Nodes[i].Path = "notes/" + m.Nodes[i].Path
	}
	gl := settledGlobe(t, m)
	same := newGlobeLook(valuesPrefs(map[string]string{
		prefGlobeColour + "adr": "#101010", prefGlobeColour + "review": "#101010",
	}), defaultPalette())
	if n := len(dotsAt(globeDots{}, gl, same, 1, 1).fills); n != len(relationTypes) {
		t.Errorf("ADRs and reviews one colour: %d fills; want %d", n, len(relationTypes))
	}
	all := newGlobeLook(valuesPrefs(map[string]string{prefGlobeRules: "notes/=#202020"}), defaultPalette())
	if d := dotsAt(globeDots{}, gl, all, 1, 1); len(d.fills) != 1 || d.fills[0].c != rgb(0x202020) {
		t.Errorf("one rule for every document: %d fills", len(d.fills))
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

	onLoop(r, func() bool { g.setColour("adr", "#12345"); return true })
	if v, msg := onLoop(r, func() string { return r.h.prefs.Value(prefGlobeColour + "adr") }), onLoop(r, func() string {
		v, _ := r.h.p.Tree().Source("App.globeSettingsError")
		return v.Raw
	}); v != "" || !strings.Contains(msg, "#rrggbb") {
		t.Fatalf("a colour that does not read: kept %q, said %q", v, msg)
	}
	before := onLoop(r, func() *globeLook { return g.currentLook() })
	onLoop(r, func() bool { g.setColour("adr", " #123456 "); return true })
	l := onLoop(r, func() *globeLook { return g.currentLook() })
	if kept := onLoop(r, func() string { return r.h.prefs.Value(prefGlobeColour + "adr") }); l == before ||
		l.types["adr"] != rgb(0x123456) || kept != "#123456" {
		t.Fatalf("a colour that reads: look %v, kept %q", l.types["adr"], kept)
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

// The GlobeMap's legend names what its marks stand for in its look: each type's colour, every
// rule's, the superseded ring, only the links its filter follows, and the dots' size. It is drawn
// on the view's own paint, a text a row, and l hides it.
func TestTheGlobeMapsLegendIsItsLook(t *testing.T) {
	l := newGlobeLook(valuesPrefs(map[string]string{prefGlobeRules: "#project=#010101; notes/=#020202"}), defaultPalette())
	texts := func(rows []legendRow) []string {
		var out []string
		for _, r := range rows {
			out = append(out, r.text)
		}
		return out
	}
	common := []string{"ADR", "review", "convention", "synthesis", "PR", "any other document", "#project", "notes/", "superseded"}
	for filter, links := range map[int][]string{
		0: {"supersedes", "other relation"},              // relations
		1: {"body link", "supersedes", "other relation"}, // every link
		2: {"body link"},                                 // body links
	} {
		g := &globeMap{filter: filter}
		want := append(append(slices.Clone(common), links...), "larger: more links")
		if got := texts(g.legend(l)); !slices.Equal(got, want) {
			t.Errorf("%s: legend %q; want %q", globeFilters[filter].name, got, want)
		}
	}
	g := &globeMap{}
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
}

// The Mindmap's outlines tell each type apart: no two legend rows share a shape, and none is
// MindRounded, which golib draws as MindCloud's (a synthesis's) stadium.
func TestTheMindmapsLegendTellsEachTypeApart(t *testing.T) {
	seen := map[string]bool{}
	for _, typ := range append(slices.Clone(relationTypes), "", "memo") {
		s := shapeOf(typ)
		if s == pm.MindRounded && typ != "synthesis" {
			t.Errorf("%q: MindRounded, drawn as a synthesis's cloud", typ)
		}
		key := fmt.Sprint(s)
		if typ != "memo" && seen[key] { // "" and memo are both any other
			t.Errorf("%q: shape %v shared", typ, s)
		}
		seen[key] = true
	}
	rows := mindLegend(mindTheme(defaultPalette()))
	if len(rows) != len(relationTypes)+1 || rows[len(rows)-1].text != "any other document" {
		t.Errorf("Mindmap legend: %d rows", len(rows))
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
