//go:build !gui

package tui

// relationViewChoices are the GlobeMap's and the Mindmap's settings' choosers, by their App.*
// source: relationChoice's rows, and the host's featureChoiceLabels for a language switch.
func relationViewChoices() map[string][]string {
	out := mindChoices()
	out["App.globeFilters"] = globeFilterNames()
	return out
}
