//go:build gui

package tui

import (
	"context"
	"errors"
	"image/color"
	"strings"
	"testing"

	graphlayout "github.com/yongjohnlee80/golib/graph/layout"
	"github.com/yongjohnlee80/golib/gui"
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
// shows several times the pictures to settle.
func TestTheGlobeMapSettlesAtItsSpeed(t *testing.T) {
	m := relMapOf(30)
	pictures := func(speed string) int {
		in := graphlayout.ForceInput{Nodes: len(m.Nodes), Seed: 1}
		for _, e := range m.Edges {
			in.Edges = append(in.Edges, graphlayout.Edge{From: e.Src, To: e.Dst})
		}
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
		if err := settle(ctx, sim, "kb", m, push, views, func(*globeShot) {}, func(*globeShot) {}, func() {}); err != nil {
			t.Fatal(err)
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
// says so and is not kept, one that does is kept and drawn, and the map opens with the links its
// settings choose. A terminals' theme set from their own menu recolours the GlobeMap too.
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

	onLoop(r, func() bool { r.h.choosePrefNamed(prefGlobeFilter, 2); return true })
	onLoop(r, func() bool { core{r.h}.TogglePanel(globeMapPanelID); return true })
	r.s.WaitForText(t, "· relations ·")

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
