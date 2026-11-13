//go:build !gui

package relations

import "github.com/yongjohnlee80/autodoc/tui/feature"

// relationViewChoices are the GlobeMap's and the Mindmap's settings' choosers, by their App.*
// source: relationChoice's rows, and the host's featureChoiceLabels for a language switch.
func RelationViewChoices() map[string][]string {
	out := mindChoices()
	out["App.globeFilters"] = globeFilterNames()
	return out
}

// globeOf is f as a GlobeMap: a terminal build's is in cells.
func globeOf(f feature.Feature) (interface{ OpenSettings() }, bool) {
	g, ok := f.(*CellGlobeMap)
	return g, ok
}
