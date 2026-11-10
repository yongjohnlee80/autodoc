//go:build gui

package tui

import (
	"bytes"
	"image/color"
	"regexp"
	"strings"
	"testing"

	"github.com/yongjohnlee80/autodoc/tui/relations"

	tuicore "github.com/yongjohnlee80/golib/tui"
)

// In the host, Relations › GlobeMap opens over the workspace's map, and Relations › Mindmap over
// the page's document, one link out; d in the Mindmap shows two links out, and d again one. Each
// closes back to the editor.
func TestTheRelationViewsOpenInTheHost(t *testing.T) {
	t.Parallel()
	d := startDaemon(t, map[string][]string{"kb": {"a.md", "[[b]]\n", "b.md", "[[c]]\n", "c.md", "ccc\n"}})
	r := windowed(t, d)
	r.waitListed(t, 3)
	r.openByPicker(t, "a.md")
	r.waitFile(t, "a.md")
	for _, v := range []struct{ panel, view, title string }{
		{relations.GlobeMapPanelID, relations.GlobeMapViewID, "kb · 3 documents, 0 links · relations"},
		{relations.MindmapPanelID, relations.MindmapViewID, "a.md · 2 documents · depth 1 · d depth"},
	} {
		onLoop(r, func() bool { core{r.h}.TogglePanel(v.panel); return true })
		r.s.WaitForText(t, v.title)
		r.s.WaitFor(t, v.panel+" open with the keyboard", func(string) bool { return r.panelIsOpen(v.panel) && r.focusedOn(v.view) })
		if v.panel == relations.MindmapPanelID {
			r.keys(t, key('d'))
			r.s.WaitForText(t, "a.md · 3 documents · depth 2 · d depth")
			r.keys(t, key('d'))
			r.s.WaitForText(t, "a.md · 2 documents · depth 1 · d depth")
		}
		onLoop(r, func() bool { core{r.h}.TogglePanel(v.panel); return true })
		r.s.WaitFor(t, v.panel+" closed, the editor's keyboard", func(string) bool { return !r.panelIsOpen(v.panel) && r.focusedOn("editor") })
	}
}

// A theme switch recolours the views: the document reloads, the GlobeMap open keeps its map and
// takes the theme (it follows the terminals', the page's until another is chosen), and the Mindmap
// lays its map out again.
func TestTheRelationViewsFollowATheme(t *testing.T) {
	t.Parallel()
	d := startDaemon(t, map[string][]string{"kb": {"a.md", "[[b]]\n", "b.md", "bbb\n"}})
	r := windowed(t, d)
	r.waitListed(t, 2)
	r.openByPicker(t, "a.md")
	r.waitFile(t, "a.md")
	var g *relations.GlobeMap
	var m *relations.Mindmap
	onLoop(r, func() bool {
		for _, f := range r.h.relationViews {
			switch f := f.(type) {
			case *relations.GlobeMap:
				g = f
			case *relations.Mindmap:
				m = f
			}
		}
		core{r.h}.TogglePanel(relations.GlobeMapPanelID)
		return true
	})
	r.s.WaitForText(t, "kb · 2 documents, 0 links · relations")
	before := onLoop(r, func() color.NRGBA { return g.Snapshot().Background })
	builds := onLoop(r, func() uint64 { return m.Snapshot().Builds })
	onLoop(r, func() bool { r.h.useTheme("light"); return true })
	r.s.WaitFor(t, "the light theme, the GlobeMap in it", func(string) bool {
		return onLoop(r, func() bool {
			s := g.Snapshot()
			return r.h.theme == "light" && s.Viewed && s.Background != before
		})
	})
	if !r.panelIsOpen(relations.GlobeMapPanelID) || onLoop(r, func() bool { return g.Snapshot().Counts[0] != 2 }) {
		t.Fatal("the theme switch closed the GlobeMap or lost its map")
	}
	if onLoop(r, func() uint64 { return m.Snapshot().Builds }) != builds {
		t.Error("a closed Mindmap laid itself out on a theme switch")
	}
}

// windowed runs the host as the GUI runs it (Options.GUI: the program is a window) on d's socket,
// and waits for the workspace's files to be listed.
func windowed(t *testing.T, d *daemon) *running {
	t.Helper()
	r := runTUI(t, NewSession(d.sock, nil), Options{GUI: true})
	r.connected(t)
	r.s.WaitFor(t, "the notes listed", func(string) bool { return len(r.listed()) > 0 })
	return r
}

// The GUI build run in a terminal (no --gui) is a terminal: its document names neither window view
// (the window's GlobeMap and Mindmap), and its relation views are the terminal's GlobeMap and
// Mindmap in cells, with their commands beside Relations › Links (docs/decisions/editor.md). Run as
// a window, the same build has the window's two views and their three commands. Preferences'
// GlobeMap… opens each one's settings.
func TestEachRunWritesItsOwnRelationViews(t *testing.T) {
	t.Parallel()
	windowView := regexp.MustCompile(`(^|[^A-Za-z])(GlobeMap|Mindmap) \{`) // the window's, not the cells'
	d := startDaemon(t, map[string][]string{"kb": {"a.md", "[[b]]\n", "b.md", "bbb\n"}})
	for _, gui := range []bool{false, true} {
		r := runTUI(t, NewSession(d.sock, nil), Options{GUI: gui})
		r.connected(t)
		doc, feats, cmds := onLoop(r, func() string { return string(r.h.layoutSrc) }), onLoop(r, func() int { return len(r.h.relationViews) }),
			onLoop(r, func() []string {
				var out []string
				for _, c := range []string{"App.toggleGlobeMap", "App.toggleMindmap", "App.toggleRelations"} {
					if _, ok := r.h.reg.Handlers[c]; ok {
						out = append(out, c)
					}
				}
				return out
			})
		views := windowView.MatchString(doc)
		cells := strings.Contains(doc, "CellGlobeMap {") && strings.Contains(doc, "CellMindmap {")
		switch {
		case !strings.Contains(doc, `MenuItem { text: qsTrId("autodoc.main.links_spc_l")`):
			t.Errorf("gui %v: Relations › Links is gone", gui)
		case gui != views:
			t.Errorf("gui %v: the document has the window's relation views: %v", gui, views)
		case gui == cells:
			t.Errorf("gui %v: the document has the terminal's cell views: %v", gui, cells)
		case feats != 2 || len(cmds) != 3:
			t.Errorf("gui %v: %d relation view features, commands %v", gui, feats, cmds)
		}
		// Preferences › GlobeMap… opens the GlobeMap's settings in every build: the window's dialog,
		// or the terminal's
		onLoop(r, func() bool { r.h.openGlobeSettings(); return true })
		r.s.WaitForText(t, "GlobeMap settings")
	}
}

// A GlobeMap drag holds the pointer: it ends on its release wherever that happens, and when the
// pointer is lost (the window's focus gone, the panel closed), so a pointer coming back with no
// button held hovers instead of panning. Every event goes through the App's own routing.
func TestTheGlobeMapDragEndsWhereverItIsReleased(t *testing.T) {
	t.Parallel()
	d := startDaemon(t, map[string][]string{"kb": {"a.md", "[[b]]\n", "b.md", "bbb\n"}})
	r := windowed(t, d)
	var g *relations.GlobeMap
	onLoop(r, func() bool {
		g = r.h.relationViews[0].(*relations.GlobeMap)
		core{r.h}.TogglePanel(relations.GlobeMapPanelID)
		return true
	})
	r.s.WaitForText(t, "kb · 2 documents, 0 links · relations")
	const inX, inY, outX, outY = 50, 15, 0, 29 // the panel's middle; the status line, outside it
	mouse := func(k tuicore.MouseKind, b tuicore.MouseButton, x, y int) tuicore.Event {
		return tuicore.MouseEvent{Kind: k, Button: b, X: x, Y: y}
	}
	dragging := func() bool { return onLoop(r, func() bool { return g.Snapshot().Dragging }) }
	press := func(what string) {
		t.Helper()
		r.keys(t, mouse(tuicore.MousePress, tuicore.MouseLeft, inX, inY), mouse(tuicore.MouseMotion, tuicore.MouseLeft, inX+3, inY+1))
		r.s.WaitFor(t, what+": the press began a drag", func(string) bool { return dragging() })
	}
	ended := func(what string) {
		t.Helper()
		r.s.WaitFor(t, what+": the drag ended", func(string) bool { return !dragging() })
		r.keys(t, mouse(tuicore.MouseMotion, tuicore.MouseNone, inX+1, inY)) // back over the view, no button held
		if dragging() {
			t.Fatalf("%s: a motion with no button held found a drag", what)
		}
	}

	press("released inside")
	r.keys(t, mouse(tuicore.MouseRelease, tuicore.MouseLeft, inX+3, inY+1))
	ended("released inside")

	press("released outside")
	r.keys(t, mouse(tuicore.MouseMotion, tuicore.MouseLeft, outX, outY), mouse(tuicore.MouseRelease, tuicore.MouseLeft, outX, outY))
	ended("released outside")

	press("the window's focus lost")
	r.keys(t, tuicore.FocusEvent{Gained: false, Terminal: true})
	r.keys(t, tuicore.FocusEvent{Gained: true, Terminal: true})
	ended("the window's focus lost")

	press("the panel closed")
	onLoop(r, func() bool { core{r.h}.TogglePanel(relations.GlobeMapPanelID); return true })
	r.s.WaitFor(t, "the GlobeMap closed", func(string) bool { return !r.panelIsOpen(relations.GlobeMapPanelID) })
	onLoop(r, func() bool { core{r.h}.TogglePanel(relations.GlobeMapPanelID); return true })
	r.s.WaitFor(t, "the GlobeMap open again", func(string) bool { return r.panelIsOpen(relations.GlobeMapPanelID) })
	ended("the panel closed")
}

// The GlobeMap reads a map of several pages whole
// (docs/decisions/search.md#clients-read-every-page): at two documents a page, seven documents
// whose relations cross pages both ways (a's to g on the last page, g's and e's back to earlier
// ones) lay out with every document and every link, the counts the one-reply map has, in the
// panel's title.
func TestTheGlobeMapReadsEveryPage(t *testing.T) {
	t.Parallel()
	tune(t).globeMapPage = 2
	rel := func(to string) string { return "---\nrelated: [" + to + "]\n---\nx\n" }
	d := startDaemon(t, map[string][]string{"kb": {
		"a.md", rel("g.md"), "b.md", rel("c.md"), "c.md", rel("f.md"), "d.md", "plain\n",
		"e.md", rel("b.md"), "f.md", rel("d.md"), "g.md", rel("a.md"),
	}})
	r := windowed(t, d)
	r.waitListed(t, 7)
	var g *relations.GlobeMap
	onLoop(r, func() bool {
		for _, f := range r.h.relationViews {
			if f, ok := f.(*relations.GlobeMap); ok {
				g = f
			}
		}
		core{r.h}.TogglePanel(relations.GlobeMapPanelID)
		return true
	})
	r.s.WaitForText(t, "kb · 7 documents, 6 links · relations")
	if c := onLoop(r, func() [2]int { return g.Snapshot().Counts }); c != [2]int{7, 6} {
		t.Errorf("the GlobeMap counts %v, want 7 documents and 6 links", c)
	}
}

// Run as a window, the GUI writes its relation views into main.qml once each: the Relations menu's
// items, a panel each whose view is its own type, and the GlobeMap's settings dialog. Run in a
// terminal, the same build writes the terminal's: its GlobeMap and Mindmap in cells, never the
// window's views.
func TestTheGUIWritesTheRelationViews(t *testing.T) {
	t.Parallel()
	term := withRelationViews(layout, false)
	if !bytes.Contains(term, []byte("CellGlobeMap {")) || !bytes.Contains(term, []byte("CellMindmap {")) ||
		bytes.Contains(term, []byte("        GlobeMap {\n")) || bytes.Contains(term, []byte("        Mindmap {\n")) {
		t.Fatal("a terminal run of the GUI build did not write the terminal's cell views alone")
	}
	doc := string(withRelationViews(layout, true))
	for _, want := range []string{
		`MenuItem { text: qsTrId("autodoc.gui.relations.globemap"); onTriggered: App.toggleGlobeMap() }`,
		`MenuItem { text: qsTrId("autodoc.gui.relations.mindmap"); onTriggered: App.toggleMindmap() }`,
		`MenuItem { text: qsTrId("autodoc.gui.relations.globemap_settings"); onTriggered: App.openGlobeSettings() }`,
		"Dialog {\n        id: globeMapSettings\n", `title: qsTrId("autodoc.gui.globeMapSettings.globemap_settings")`,
		"Dialog {\n        id: mindmapSettings\n", `title: qsTrId("autodoc.gui.mindmapSettings.mindmap_settings")`,
		`MenuItem { text: qsTrId("autodoc.gui.relations.mindmap_settings"); onTriggered: App.openMindmapSettings() }`,
		"id: globeMap\n", "GlobeMap {\n            id: globeMapView", `App.panelOpened("globeMap")`,
		"id: mindmap\n", "Mindmap {\n            id: mindmapView", `App.panelOpened("mindmap")`,
	} {
		if n := strings.Count(doc, want); n != 1 {
			t.Errorf("main.qml holds %q %d times, want once", want, n)
		}
	}
	if !bytes.Equal(withRelationViews([]byte("Window {}"), true), []byte("Window {}")) {
		t.Error("a layout without the markers was written to")
	}
}
