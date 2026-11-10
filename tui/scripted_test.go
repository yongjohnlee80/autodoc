package tui

import "github.com/yongjohnlee80/autodoc/tui/feature/featuretest"

// newScripted is featuretest's Core answering the host's preferences.
func newScripted(ws string) *featuretest.ScriptedCore {
	c := featuretest.NewScripted(ws)
	c.Preferences = prefs{} // the host's preferences, catalog and agents, as the cells were written against
	c.Text, c.Textf, c.AgentErr = englishText, (&Host{}).trf, ErrAgentUnavailable
	return c
}
