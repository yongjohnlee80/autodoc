//go:build gui

package tui

import (
	"image/color"
	"regexp"
	"strings"
	"testing"

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
		{globeMapPanelID, globeMapViewID, "kb · 3 documents, 0 links · relations"},
		{mindmapPanelID, mindmapViewID, "a.md · 2 documents · depth 1 · d depth"},
	} {
		onLoop(r, func() bool { core{r.h}.TogglePanel(v.panel); return true })
		r.s.WaitForText(t, v.title)
		r.s.WaitFor(t, v.panel+" open with the keyboard", func(string) bool { return r.panelIsOpen(v.panel) && r.focusedOn(v.view) })
		if v.panel == mindmapPanelID {
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
	var g *globeMap
	var m *mindmap
	onLoop(r, func() bool {
		for _, f := range r.h.relationViews {
			switch f := f.(type) {
			case *globeMap:
				g = f
			case *mindmap:
				m = f
			}
		}
		core{r.h}.TogglePanel(globeMapPanelID)
		return true
	})
	r.s.WaitForText(t, "kb · 2 documents, 0 links · relations")
	before := onLoop(r, func() color.NRGBA { return g.State().Background })
	builds := onLoop(r, func() uint64 { return m.State().Builds })
	onLoop(r, func() bool { r.h.useTheme("light"); return true })
	r.s.WaitFor(t, "the light theme, the GlobeMap in it", func(string) bool {
		return onLoop(r, func() bool {
			s := g.State()
			return r.h.theme == "light" && s.Viewed && s.Background != before
		})
	})
	if !r.panelIsOpen(globeMapPanelID) || onLoop(r, func() bool { return g.State().Counts[0] != 2 }) {
		t.Fatal("the theme switch closed the GlobeMap or lost its map")
	}
	if onLoop(r, func() uint64 { return m.State().Builds }) != builds {
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
	var g *globeMap
	onLoop(r, func() bool {
		g = r.h.relationViews[0].(*globeMap)
		core{r.h}.TogglePanel(globeMapPanelID)
		return true
	})
	r.s.WaitForText(t, "kb · 2 documents, 0 links · relations")
	const inX, inY, outX, outY = 50, 15, 0, 29 // the panel's middle; the status line, outside it
	mouse := func(k tuicore.MouseKind, b tuicore.MouseButton, x, y int) tuicore.Event {
		return tuicore.MouseEvent{Kind: k, Button: b, X: x, Y: y}
	}
	dragging := func() bool { return onLoop(r, func() bool { return g.State().Dragging }) }
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
	onLoop(r, func() bool { core{r.h}.TogglePanel(globeMapPanelID); return true })
	r.s.WaitFor(t, "the GlobeMap closed", func(string) bool { return !r.panelIsOpen(globeMapPanelID) })
	onLoop(r, func() bool { core{r.h}.TogglePanel(globeMapPanelID); return true })
	r.s.WaitFor(t, "the GlobeMap open again", func(string) bool { return r.panelIsOpen(globeMapPanelID) })
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
	var g *globeMap
	onLoop(r, func() bool {
		for _, f := range r.h.relationViews {
			if f, ok := f.(*globeMap); ok {
				g = f
			}
		}
		core{r.h}.TogglePanel(globeMapPanelID)
		return true
	})
	r.s.WaitForText(t, "kb · 7 documents, 6 links · relations")
	if c := onLoop(r, func() [2]int { return g.State().Counts }); c != [2]int{7, 6} {
		t.Errorf("the GlobeMap counts %v, want 7 documents and 6 links", c)
	}
}
