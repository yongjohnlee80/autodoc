package tui

import (
	"maps"

	"github.com/yongjohnlee80/golib/tui/panelgeometry"
)

// migrateLinksPanel carries the links panel's kept place onto the explorer's, which holds the Links
// tab now (explorerPanelSpec): its edge, its floating rect, and its size and length at each edge.
// A key the explorer keeps wins, an empty one too (a rect cleared by docking it); one it lacks takes
// the links' value. Nothing is deleted or written back: the links' keys are read on every load, until
// the explorer keeps its own.
func migrateLinksPanel(values map[string]any) map[string]any {
	keys := [][2]string{
		{panelgeometry.EdgeKey("links"), panelgeometry.EdgeKey("explorer")},
		{panelgeometry.FloatKey("links"), panelgeometry.FloatKey("explorer")},
	}
	for _, edge := range edges {
		keys = append(keys,
			[2]string{panelgeometry.SizeKey("links", edge), panelgeometry.SizeKey("explorer", edge)},
			[2]string{panelgeometry.LengthKey("links", edge), panelgeometry.LengthKey("explorer", edge)})
	}
	var out map[string]any
	for _, k := range keys {
		v, kept := values[k[0]]
		if _, own := values[k[1]]; !kept || own {
			continue
		}
		if out == nil {
			out = maps.Clone(values)
		}
		out[k[1]] = v
	}
	if out == nil {
		return values
	}
	return out
}
