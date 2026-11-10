//go:build gui

package tui

import (
	"github.com/yongjohnlee80/autodoc/tui/feature"
	"github.com/yongjohnlee80/golib/gui"
	tuicore "github.com/yongjohnlee80/golib/tui"
	tuidecl "github.com/yongjohnlee80/golib/tui/decl"
)

// windowViews are a window's relation views, native views drawn over their cells: the GlobeMap and
// the Mindmap. backend is the host's backend when asked: the GlobeMap's pictures go to its window
// (presenterOf).
func windowViews(c feature.Core, files absoluteFiles, userFile string, page int, backend func() any) []feature.Feature {
	store := newGraphStore(c, files, userFile)
	g, m := newGlobeMap(c, func() globePublisher { return presenterOf(backend()) }, page), newMindmap(c)
	g.store, g.editor.store, m.store = store, store, store
	return []feature.Feature{g, m}
}

// presenterOf is where the GlobeMap's pictures go: a stream of the window it runs in, or, with no
// window (a test's host on a terminal backend), its view's own paint.
func presenterOf(backend any) globePublisher {
	if b, ok := backend.(*gui.Backend); ok {
		return streamPublisher{cs: b.NewCanvasStream()}
	}
	return &paintPresenter{}
}

// globeOf is f as a GlobeMap: a window's, or one in cells (a GUI build run as a terminal, --ui).
func globeOf(f feature.Feature) (interface{ openSettings() }, bool) {
	switch g := f.(type) {
	case *globeMap:
		return g, true
	case *cellGlobeMap:
		return g, true
	}
	return nil, false
}

// viewType is the GlobeMap's QML type; a view dropped takes its gesture with it.
func (g *globeMap) viewType() tuidecl.Type {
	return relationViewType("GlobeMap", g.newView, func(c tuicore.Component) {
		if g.view == c {
			g.view, g.drag = nil, nil
		}
	})
}

// viewType is the Mindmap's QML type.
func (m *mindmap) viewType() tuidecl.Type {
	return relationViewType("Mindmap", m.newView, func(c tuicore.Component) {
		if m.view == c {
			m.view = nil
		}
	})
}
