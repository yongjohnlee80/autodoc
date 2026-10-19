//go:build gui

package tui

import (
	"maps"
	"slices"
)

// THE MINDMAP'S SETTINGS — Relations › Mindmap settings…, as the GlobeMap's: its theme (the page's,
// by default; the terminals'; or one named) and the depth it opens at, kept as tui.mindmap.*, and
// its groups (groups.go), each a query, a colour and an outline, kept in the workspace's graph file
// and edited in the same dialog (groupeditor_gui.go).

const (
	prefMindTheme = "tui.mindmap.theme"
	prefMindDepth = "tui.mindmap.depth"
)

func (m *mindmap) Prefs() []PrefSpec {
	names, _ := viewThemeChoices(m.c)
	return []PrefSpec{
		ValuePref(prefMindTheme, "page", nil).shows(func(p prefs) map[string]any {
			return map[string]any{"App.mindThemeIndex": max(slices.Index(names, p.Value(prefMindTheme)), 0)}
		}).then(func(prefs) { m.relayout() }),
		ValuePref(prefMindDepth, "1", []string{"1", "2"}).chooser("App.mindDepthIndex", "App.setMindDepth"),
	}
}

// relayout lays the map out again, in its theme, while it is open.
func (m *mindmap) relayout() {
	if m.open {
		m.load()
	}
}

// prefDepth is the depth the settings open the map at.
func (m *mindmap) prefDepth() int {
	if m.c.Prefs().Value(prefMindDepth) == "2" {
		return 2
	}
	return 1
}

// readGroups takes ws's groups from the store: its graph file's, or its first, made of types and
// the workspace's folders. The map is laid out again in them.
func (m *mindmap) readGroups(ws string, types []string) {
	if m.store == nil {
		return
	}
	m.store.groups(ws, viewMindmap, types, m.palette().background, func(list []relGroup) {
		if ws == m.c.Workspace() {
			m.editor.listed(ws, types, list)
			m.groupsWS = ws
			m.groupsEdited(list)
		}
	})
}

// groupsEdited lays the map out again in list.
func (m *mindmap) groupsEdited(list []relGroup) {
	gs := newRelGroups(list)
	m.groups = &gs
	m.relayout()
}

// openSettings is Relations › Mindmap settings…: the workspace's groups, read first when this
// workspace's are not yet (the page's neighbourhood names the schema's types).
func (m *mindmap) openSettings() {
	ws := m.c.Workspace()
	m.c.Open("mindmapSettings")
	if ws == "" || m.groupsWS == ws {
		m.editor.show("")
		return
	}
	var types []string
	if m.cur != nil && m.cur.root.ws == ws {
		types = m.cur.m.Types
	}
	m.readGroups(ws, types)
}

// setTheme is the theme chooser's row i.
func (m *mindmap) setTheme(i int) {
	if names, _ := viewThemeChoices(m.c); i >= 0 && i < len(names) {
		m.c.SetPref(prefMindTheme, names[i])
	}
}

// settingsState is the dialog's lists.
func (m *mindmap) settingsState() map[string]any {
	_, themes := viewThemeChoices(m.c)
	var shapes []string
	for _, s := range mindShapes {
		shapes = append(shapes, s.name)
	}
	st := m.editor.state()
	maps.Copy(st, map[string]any{
		"App.mindThemes": choices(themes...), "App.mindDepths": choices("one link out", "two links out"),
		"App.mindShapeChoices": choices(shapes...),
	})
	return st
}

func (m *mindmap) settingsCommands() map[string]Command {
	c := m.editor.commands()
	c["App.openMindmapSettings"] = none(m.openSettings)
	c["App.setMindTheme"] = oneNumber("a row", m.setTheme)
	return c
}
