//go:build gui

package tui

import "testing"

// wholeTables: the GUI build's Go tables are all of AutoDoc's (language_catalog_test.go).
const wholeTables = true

// The GUI build's own commands are in the English catalog, so its command line and F1 name them in
// the UI's language: the GlobeMap's and the Mindmap's, which a terminal never declares.
func TestTheGUIsCommandsAreInTheCatalog(t *testing.T) {
	en := readEnglish(t)
	for _, id := range []string{"autodoc.cmd.globemap.toggle", "autodoc.cmd.globemap.settings", "autodoc.cmd.mindmap.toggle", "autodoc.cmd.mindmap.settings"} {
		if _, ok := en[id]; !ok {
			t.Errorf("%s is not in %s", id, enCatalog)
		}
	}
}
