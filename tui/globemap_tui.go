//go:build !gui

package tui

import "github.com/yongjohnlee80/autodoc/tui/feature"

// relationViewChoices are the GlobeMap's and the Mindmap's settings' choosers, by their App.*
// source: relationChoice's rows, and the host's featureChoiceLabels for a language switch.
func relationViewChoices() map[string][]string {
	out := mindChoices()
	out["App.globeFilters"] = globeFilterNames()
	return out
}

// globeOf is f as a GlobeMap: a terminal build's is in cells.
func globeOf(f feature.Feature) (interface{ openSettings() }, bool) {
	g, ok := f.(*cellGlobeMap)
	return g, ok
}
