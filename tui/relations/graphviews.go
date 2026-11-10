package relations

import (
	"github.com/yongjohnlee80/autodoc/tui/feature"
	tuidecl "github.com/yongjohnlee80/golib/tui/decl"
)

// THE RELATION VIEWS AS A HOST MAKES THEM: Relations › GlobeMap and Relations › Mindmap, one graph
// store for both, in cells (a terminal's, cellViews) or a window's (windowViews, graphviews_gui.go).
// The host keeps them, writes their QML (relationmap.go) and asks for their types and settings.

// cellViews are a terminal's relation views, in cells: the GlobeMap and the Mindmap. files reads
// the documents' own files, userFile is the user's graph.toml, and page the GlobeMap's page size.
func CellViews(c feature.Core, files AbsoluteFiles, userFile string, page int) []feature.Feature {
	store := NewGraphStore(c, files, userFile)
	return []feature.Feature{newCellGlobeMap(c, store, page), newCellMindmap(c, store)}
}

// viewTypes are the QML types of fs's views: each built view joins its feature.
func ViewTypes(fs []feature.Feature) []tuidecl.Type {
	var out []tuidecl.Type
	for _, f := range fs {
		if v, ok := f.(interface{ viewType() tuidecl.Type }); ok {
			out = append(out, v.viewType())
		}
	}
	return out
}

// openGlobeSettings opens the settings of the GlobeMap among fs; false with none (a build or a
// run without one).
func OpenGlobeSettings(fs []feature.Feature) bool {
	for _, f := range fs {
		if g, ok := globeOf(f); ok {
			g.OpenSettings()
			return true
		}
	}
	return false
}
