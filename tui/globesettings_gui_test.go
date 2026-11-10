//go:build gui

package tui

import (
	"context"
	"image/color"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/yongjohnlee80/autodoc/tui/feature"
	"github.com/yongjohnlee80/golib/gui"
)

func rgb(v uint32) color.NRGBA {
	return color.NRGBA{R: uint8(v >> 16), G: uint8(v >> 8), B: uint8(v), A: 0xff}
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
	before := onLoop(r, func() any { return g.Snapshot().Look })
	onLoop(r, func() bool { g.setColour("other", " #123456 "); return true })
	l := onLoop(r, func() GlobeState { return g.Snapshot() })
	if kept := onLoop(r, func() string { return r.h.prefs.Value(prefGlobeColour + "other") }); l.Look == before ||
		l.Other != rgb(0x123456) || kept != "#123456" {
		t.Fatalf("a colour that reads: look %v, kept %q", l.Other, kept)
	}

	// the dialog closed, as the user does, so the panel's heading shows
	onLoop(r, func() bool { core{r.h}.Close("globeMapSettings"); return true })
	r.s.WaitFor(t, "the settings closed", func(sc string) bool { return !strings.Contains(sc, "GlobeMap settings") })
	onLoop(r, func() bool { core{r.h}.TogglePanel(globeMapPanelID); return true })
	r.s.WaitForText(t, "kb · 2 documents, 0 links · relations") // the relations by default
	// a text screen paints no native view, and a window's paint is what hands the load its view: the
	// cell hands it one, and the load settles as it does in a window
	onLoop(r, func() bool { g.sendView(gui.Size{W: 800, H: 600}, true); return true })
	r.s.WaitFor(t, "the settled notice", func(string) bool { // its progress toast ends saying so, kept
		return onLoop(r, func() bool {
			return slices.ContainsFunc(r.h.noticeLog.Items, func(n feature.Notice) bool { return n.Text == "GlobeMap: kb settled · 2 documents, 0 links" })
		})
	})
	onLoop(r, func() bool { r.h.choosePrefNamed(prefGlobeFilter, 1); return true })
	r.s.WaitForText(t, "kb · 2 documents, 1 links · every link") // the open map follows the choice
	r.s.WaitFor(t, "the GlobeMap has the keyboard", func(string) bool { return r.focusedOn(globeMapViewID) })
	r.keys(t, key('l')) // the legend hidden, and shown again
	r.s.WaitFor(t, "l hid the legend", func(string) bool { return onLoop(r, func() bool { return g.Snapshot().LegendHidden }) })
	r.keys(t, key('l'))
	r.s.WaitFor(t, "l showed the legend", func(string) bool { return onLoop(r, func() bool { return !g.Snapshot().LegendHidden }) })
	r.keys(t, key('f')) // this opening's own: body links
	r.s.WaitForText(t, "kb · 2 documents, 1 links · body links")
	onLoop(r, func() bool { core{r.h}.TogglePanel(globeMapPanelID); return true })
	r.s.WaitFor(t, "the GlobeMap closed", func(string) bool { return !r.panelIsOpen(globeMapPanelID) })
	onLoop(r, func() bool { core{r.h}.TogglePanel(globeMapPanelID); return true })
	r.s.WaitForText(t, "kb · 2 documents, 1 links · every link") // opened again: the settings' choice

	was := onLoop(r, func() color.NRGBA { return g.Snapshot().Background })
	onLoop(r, func() bool { core{r.h}.SetPref(feature.PrefTermTheme, "light"); return true })
	if now := onLoop(r, func() color.NRGBA { return g.Snapshot().Background }); now == was {
		t.Error("the terminals' theme set alone left the GlobeMap in the old one")
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
	r.waitAttached(t, "kb")
	r.waitListed(t, 4) // schema.yaml is listed too: the default patterns admit **/*.yaml
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
	queries := func(list []Group) []string {
		var out []string
		for _, x := range list {
			out = append(out, x.Query)
		}
		return out
	}
	r.s.WaitFor(t, "the GlobeMap's groups", func(string) bool { return onLoop(r, func() bool { return g.Snapshot().Groups != nil }) })
	if got := onLoop(r, func() []string { return queries(g.Snapshot().Groups) }); !slices.Equal(got, []string{"type:note", "type:adr", "path:notes/"}) {
		t.Fatalf("groups %q; want the schema's types, then the folder", got)
	}
	file := func() string { b, _ := os.ReadFile(filepath.Join(kb, ".autodoc", "graph.toml")); return string(b) }
	r.s.WaitFor(t, "the graph file written", func(string) bool { return strings.Contains(file(), `query = "path:notes/"`) })
	if f := file(); !strings.Contains(f, "[[globemap.groups]]") || strings.Contains(f, "[[mindmap.groups]]") || !strings.Contains(f, "palette = [") {
		t.Fatalf("the graph file:\n%s", f)
	}
	// an edit in the settings: written, and drawn
	onLoop(r, func() bool { g.EditGroups().moved(1); g.EditGroups().setColour("#123456"); return true })
	r.s.WaitFor(t, "the edit written", func(string) bool { return strings.Contains(file(), `colour = "#123456"`) })
	if c := onLoop(r, func() color.NRGBA { return g.Snapshot().Groups[1].Colour }); c != rgb(0x123456) {
		t.Errorf("type:adr's colour in the view: %v", c)
	}
	onLoop(r, func() bool { g.EditGroups().setQuery("nope"); return true })
	if q := onLoop(r, func() string { return g.Snapshot().Groups[1].Query }); q != "type:adr" {
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
	if got := onLoop(r, func() []string { return queries(m.Snapshot().Groups) }); !slices.Equal(got, []string{"type:note", "type:adr", "path:notes/"}) {
		t.Errorf("the Mindmap's groups %q", got)
	}
}
