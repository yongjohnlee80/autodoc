//go:build gui

package tui

import (
	"slices"
	"strings"
	"testing"

	"github.com/yongjohnlee80/golib/gui"
)

// A GlobeMap node pressed opens its document and hides the GlobeMap, not closed (ADR 1792397302
// r3): its Closed hook does not run, so its load, its view and this opening's filter are kept. It
// stays in the dock, hidden, and its icon shows it again as it was, the keyboard in it.
func TestAGlobeMapNodeHidesItAndTheDockShowsItAsItWas(t *testing.T) {
	d := startDaemon(t, map[string][]string{"kb": {"a.md", "[[b]]\n", "b.md", "bbb\n"}})
	r := windowed(t, d)
	g := onLoop(r, func() *globeMap { return r.h.relationViews[0].(*globeMap) })
	onLoop(r, func() bool { core{r.h}.TogglePanel(globeMapPanelID); return true })
	r.s.WaitForText(t, "kb · 2 documents, 0 links · relations")
	onLoop(r, func() bool { g.sendView(gui.Size{W: 800, H: 600}, true); return true })
	r.s.WaitFor(t, "the map settled", func(string) bool {
		return onLoop(r, func() bool {
			return slices.ContainsFunc(r.h.noticeLog.items, func(n notice) bool { return strings.HasPrefix(n.text, "GlobeMap: kb settled") })
		})
	})
	r.s.WaitFor(t, "the GlobeMap has the keyboard", func(string) bool { return r.focusedOn(globeMapViewID) })
	r.keys(t, key('f'), key('f')) // this opening's own filter: body links
	r.s.WaitForText(t, "kb · 2 documents, 1 links · body links")
	onLoop(r, func() bool { g.zoomBy(2, gui.Pt(100, 80)); return true }) // the view moved by the user
	type view struct {
		zoom   float32
		off    gui.Point
		moved  bool
		filter int
		cur    *globe
	}
	viewOf := func() view {
		return onLoop(r, func() view { return view{g.zoom, g.off, g.moved, g.filter, g.cur} })
	}
	before := viewOf()

	onLoop(r, func() bool { // b.md's dot pressed
		i := slices.IndexFunc(g.cur.m.Nodes, func(n mapNode) bool { return n.Path == "b.md" })
		g.press(&globeShot{f: g.cur}, i)
		return true
	})
	r.s.WaitFor(t, "b.md open, the GlobeMap hidden", func(string) bool {
		return r.shownPath() == "b.md" && r.panelIsOpen(globeMapPanelID) && r.hidden(globeMapPanelID)
	})
	if !onLoop(r, func() bool { return g.open }) {
		t.Fatal("the GlobeMap's Closed hook ran: hiding it closed it")
	}
	name := onLoop(r, func() string { return r.h.widgetName(globeMapPanelID) })
	r.waitDock(t, "the hidden GlobeMap kept", "editor", name)
	r.s.WaitFor(t, "its icon dimmed", func(string) bool { return r.dockHiddenRow(name) })

	onLoop(r, func() bool { r.h.goDock(name); return true })
	r.s.WaitFor(t, "the GlobeMap shown, the keyboard in it", func(string) bool {
		return !r.hidden(globeMapPanelID) && r.focusedOn(globeMapViewID)
	})
	r.s.WaitForText(t, "kb · 2 documents, 1 links · body links")
	if after := viewOf(); after != before {
		t.Fatalf("the GlobeMap shown again is %+v; want it as it was, %+v", after, before)
	}
}
