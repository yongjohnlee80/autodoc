//go:build gui

package tui

import (
	"bytes"
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/yongjohnlee80/golib/gui"
	tuicore "github.com/yongjohnlee80/golib/tui"
)

// Run as a window, the GUI writes its relation views into main.qml once each: the Relations menu's
// two items, and a panel each whose view is its own type. Run in a terminal, the same build writes
// nothing: the terminal has Links alone.
func TestTheGUIWritesTheRelationViews(t *testing.T) {
	if !bytes.Equal(withRelationViews(layout, false), layout) {
		t.Fatal("a terminal run of the GUI build wrote the relation views in")
	}
	doc := string(withRelationViews(layout, true))
	for _, want := range []string{
		`MenuItem { text: "&GlobeMap"; onTriggered: App.toggleGlobeMap() }`,
		`MenuItem { text: "&Mindmap"; onTriggered: App.toggleMindmap() }`,
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

// relMapOf is a map of n documents, each linked to the next and every seventh superseded.
func relMapOf(n int) relMap {
	var m relMap
	types := append([]string{""}, relationTypes...)
	for i := range n {
		m.Nodes = append(m.Nodes, mapNode{Path: fmt.Sprintf("d%04d.md", i), Title: fmt.Sprint("Doc ", i),
			Type: types[i%len(types)], Superseded: i%7 == 3})
		if i > 0 {
			m.Edges = append(m.Edges, mapEdge{Src: i - 1, Dst: i, Kind: []string{"wikilink", "supersedes", "related"}[i%3]})
		}
	}
	return m
}

// A GlobeMap frame is the same handful of operations whatever the map's size: a stroke per edge
// class, a fill per document type, the rings, and the hovered name.
func TestTheGlobeMapPaintsInConstantOperations(t *testing.T) {
	ops := map[int]int{}
	for _, n := range []int{12, 600} {
		gl, err := buildGlobe(context.Background(), "kb", relMapOf(n))
		if err != nil {
			t.Fatal(err)
		}
		for _, hover := range []int{-1, 5} {
			rc := gui.NewRecordingCanvas(gui.Size{W: 800, H: 600}, gui.Size{W: 8, H: 16})
			globeFrame{gl: gl, zoom: 0.5, hover: hover, pal: defaultPalette()}.paint(rc)
			key := hover + 1
			if ops[key] == 0 {
				ops[key] = len(rc.Calls)
			} else if len(rc.Calls) != ops[key] {
				t.Errorf("%d documents, hover %d: %d operations; %d for the smaller map", n, hover, len(rc.Calls), ops[key])
			}
			if fills := countOps(rc, "FillPath"); fills != len(relationTypes)+1 {
				t.Errorf("%d documents: %d path fills, want one per type", n, fills)
			}
		}
	}
}

func countOps(rc *gui.RecordingCanvas, op string) int {
	n := 0
	for _, c := range rc.Calls {
		if c.Op == op {
			n++
		}
	}
	return n
}

// A press finds the document under it, within its dot, and only that.
func TestTheGlobeMapHitsTheDocumentUnderThePointer(t *testing.T) {
	gl, err := buildGlobe(context.Background(), "kb", relMapOf(40))
	if err != nil {
		t.Fatal(err)
	}
	g := &globeMap{cur: gl, zoom: 1}
	for _, i := range []int{0, 17, 39} {
		at := gl.at[i]
		if got := g.hit(gui.Pt(at.X, at.Y)); got != i {
			t.Errorf("at document %d's centre: %d", i, got)
		}
	}
	if got := g.hit(gui.Pt(-1000, -1000)); got != -1 {
		t.Errorf("far off the map: %d", got)
	}
}

// closeHost is a panel's host as its Closed hook uses it.
type closeHost struct {
	PanelHost
	restored []string
}

func (*closeHost) Keep(error) {}
func (c *closeHost) RestoreKeyboardPane(id string) error {
	c.restored = append(c.restored, id)
	return nil
}

// A GlobeMap build lands whole and only when it is the newest: one overtaken by a later load, or
// answered after the panel closed, is dropped.
func TestTheGlobeMapInstallsOnlyItsNewestBuild(t *testing.T) {
	c := newScripted("kb")
	sizes := []int{3, 5, 8}
	c.answers["graph.map"] = func([]any) (any, error) {
		n := sizes[0]
		sizes = sizes[1:]
		return mapAnswer(nodesOf(n), nil), nil
	}
	c.deferred = true
	g := newGlobeMap(c)
	spec := g.Panels()[0]
	spec.Opened(nil) // a load of 3
	g.load()         // a load of 5
	c.deliver(1, 0)  // the newer first, then the older
	if g.cur == nil || len(g.cur.m.Nodes) != 5 {
		t.Fatalf("installed %v; want the newer build, of 5", g.cur)
	}
	g.load() // a load of 8, answered after the panel closed
	h := &closeHost{}
	spec.Closed(h)
	c.deliver(0)
	if len(g.cur.m.Nodes) != 5 || len(h.restored) != 1 || h.restored[0] != "editor" {
		t.Fatalf("after closing: %d documents shown, keyboard %v; want the build of 5 kept, the editor's", len(g.cur.m.Nodes), h.restored)
	}
}

func nodesOf(n int) [][4]any {
	var out [][4]any
	for i := range n {
		out = append(out, [4]any{fmt.Sprintf("d%d.md", i), "", "", false})
	}
	return out
}

// The Mindmap is rooted at the page shown, and only by Shown: a click opens the document under it
// with the keyboard kept on the Mindmap, and reads nothing itself; the page the click shows roots
// it again.
func TestTheMindmapRootsAtThePageShown(t *testing.T) {
	c := newScripted("kb")
	c.answers["graph.map"] = func(p []any) (any, error) {
		root := p[1].(map[string]any)["root"].(string)
		return mapAnswer([][4]any{{"a.md", "A", "adr", false}, {"b.md", "B", "review", false}, {"c.md", "C", "", false}},
			[][3]string{{"a.md", "b.md", "wikilink"}, {"b.md", "c.md", "wikilink"}, {root, root, "wikilink"}}), nil
	}
	m := newMindmap(c)
	m.Shown(Page{Ref: fileRef{"kb", "a.md"}}) // closed: nothing read
	if len(c.calls) != 0 {
		t.Fatalf("a closed Mindmap read %v", c.calls)
	}
	m.Panels()[0].Opened(nil)
	if m.cur == nil || m.cur.root.path != "a.md" || len(c.calls) != 1 || !strings.Contains(c.calls[0], "root:a.md") {
		t.Fatalf("opened over a.md: %+v, calls %v", m.cur, c.calls)
	}
	p, ok := pointOf(m.cur, "b.md")
	if !ok {
		t.Fatal("b.md is nowhere in the drawn map")
	}
	m.click(p, gui.Point{})
	if want := []string{"kb:b.md focus " + mindmapPanelID}; !equalStrings(c.files, want) || len(c.calls) != 1 {
		t.Fatalf("the click opened %v and read %v; want b.md with the keyboard kept, and no read", c.files, c.calls)
	}
	m.Shown(Page{Ref: fileRef{"kb", "b.md"}}) // the page the click opened
	if m.cur.root.path != "b.md" || len(c.calls) != 2 {
		t.Fatalf("after b.md was shown: rooted at %s, calls %v", m.cur.root.path, c.calls)
	}
	m.Shown(Page{Ref: fileRef{"", "/tmp/x.md"}, Outside: true})
	if m.cur != nil || len(c.calls) != 2 {
		t.Fatalf("a file in no workspace: %+v, calls %v", m.cur, c.calls)
	}
}

// pointOf is a point in the build's drawing (its own coordinates) over the node of document p.
func pointOf(cur *mind, p string) (gui.Point, bool) {
	sz := cur.laid.Size()
	for y := float32(0); y < sz.H; y += 2 {
		for x := float32(0); x < sz.W; x += 2 {
			if id, ok := cur.laid.NodeAt(gui.Pt(x, y)); ok {
				if i, ok := cur.m.mindIndex(id); ok && cur.m.Nodes[i].Path == p {
					return gui.Pt(x, y), true
				}
			}
		}
	}
	return gui.Point{}, false
}

func equalStrings(a, b []string) bool { return strings.Join(a, "\x00") == strings.Join(b, "\x00") }

// In the host, Relations › GlobeMap opens over the workspace's map, and Relations › Mindmap over
// the page's document, one link out; d in the Mindmap shows two links out, and d again one. Each
// closes back to the editor.
func TestTheRelationViewsOpenInTheHost(t *testing.T) {
	d := startDaemon(t, map[string][]string{"kb": {"a.md", "[[b]]\n", "b.md", "[[c]]\n", "c.md", "ccc\n"}})
	r := windowed(t, d)
	r.waitListed(t, 3)
	r.openByPicker(t, "a.md")
	r.waitFile(t, "a.md")
	for _, v := range []struct{ panel, view, title string }{
		{globeMapPanelID, globeMapViewID, "kb · 3 documents, 2 links"},
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

// A theme switch builds the views again in the theme's colours: the document reloads, the GlobeMap
// open keeps its map in a new view, and the Mindmap lays its map out again.
func TestTheRelationViewsFollowATheme(t *testing.T) {
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
	r.s.WaitForText(t, "kb · 2 documents, 1 links")
	before := onLoop(r, func() *globeView { return g.view })
	builds := onLoop(r, func() uint64 { return m.seq })
	onLoop(r, func() bool { r.h.useTheme("light"); return true })
	r.s.WaitFor(t, "the light theme, a new GlobeMap view", func(string) bool {
		return onLoop(r, func() bool {
			return r.h.theme == "light" && g.view != nil && g.view != before && g.view.pal.background != before.pal.background
		})
	})
	if !r.panelIsOpen(globeMapPanelID) || onLoop(r, func() bool { return g.cur == nil }) {
		t.Fatal("the theme switch closed the GlobeMap or lost its map")
	}
	if onLoop(r, func() uint64 { return m.seq }) != builds {
		t.Error("a closed Mindmap laid itself out on a theme switch")
	}
}

// windowed runs the host as the GUI runs it (Options.GUI: the program is a window) on d's socket,
// and waits for the workspace's files to be listed.
func windowed(t *testing.T, d *daemon) *running {
	t.Helper()
	r := runTUI(t, NewSession(d.sock, nil), Options{GUI: true})
	r.s.WaitForText(t, "connected — autodoc v-test")
	r.s.WaitFor(t, "the notes listed", func(string) bool { return len(r.listed()) > 0 })
	return r
}

// The GUI build run in a terminal (no --gui) is a terminal: its document names neither view, it
// has neither feature nor command, and Relations › Links is its only item. Run as a window, the
// same build has all three.
func TestTheRelationViewsAreTheWindowsAlone(t *testing.T) {
	d := startDaemon(t, map[string][]string{"kb": {"a.md", "[[b]]\n", "b.md", "bbb\n"}})
	for _, gui := range []bool{false, true} {
		r := runTUI(t, NewSession(d.sock, nil), Options{GUI: gui})
		r.s.WaitForText(t, "connected — autodoc v-test")
		doc, feats, cmds := onLoop(r, func() string { return string(r.h.layoutSrc) }), onLoop(r, func() int { return len(r.h.relationViews) }),
			onLoop(r, func() []string {
				var out []string
				for _, c := range []string{"App.toggleGlobeMap", "App.toggleMindmap", "App.toggleRelations"} {
					if _, ok := r.h.reg.handlers[c]; ok {
						out = append(out, c)
					}
				}
				return out
			})
		views := strings.Contains(doc, "&GlobeMap") || strings.Contains(doc, "GlobeMap {") || strings.Contains(doc, "Mindmap {")
		switch {
		case !strings.Contains(doc, `MenuItem { text: "&Links  (SPC l)"`):
			t.Errorf("gui %v: Relations › Links is gone", gui)
		case gui != views:
			t.Errorf("gui %v: the document has the relation views: %v", gui, views)
		case gui && (feats != 2 || len(cmds) != 3), !gui && (feats != 0 || len(cmds) != 1):
			t.Errorf("gui %v: %d relation view features, commands %v", gui, feats, cmds)
		}
	}
}

// A GlobeMap drag holds the pointer: it ends on its release wherever that happens, and when the
// pointer is lost (the window's focus gone, the panel closed), so a pointer coming back with no
// button held hovers instead of panning. Every event goes through the App's own routing.
func TestTheGlobeMapDragEndsWhereverItIsReleased(t *testing.T) {
	d := startDaemon(t, map[string][]string{"kb": {"a.md", "[[b]]\n", "b.md", "bbb\n"}})
	r := windowed(t, d)
	var g *globeMap
	onLoop(r, func() bool {
		g = r.h.relationViews[0].(*globeMap)
		core{r.h}.TogglePanel(globeMapPanelID)
		return true
	})
	r.s.WaitForText(t, "kb · 2 documents, 1 links")
	const inX, inY, outX, outY = 50, 15, 0, 29 // the panel's middle; the status line, outside it
	mouse := func(k tuicore.MouseKind, b tuicore.MouseButton, x, y int) tuicore.Event {
		return tuicore.MouseEvent{Kind: k, Button: b, X: x, Y: y}
	}
	dragging := func() bool { return onLoop(r, func() bool { return g.drag != nil }) }
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
