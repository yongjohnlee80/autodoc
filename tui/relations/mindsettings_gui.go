//go:build gui

package relations

import (
	"maps"
	"slices"

	"github.com/yongjohnlee80/autodoc/tui/feature"
)

// THE MINDMAP'S SETTINGS — Relations › Mindmap settings…, as the GlobeMap's: its theme (the page's,
// by default; the terminals'; or one named) and the depth it opens at, kept as tui.mindmap.*, and
// its groups (groups.go), each a query, a colour and an outline, kept in the workspace's graph file
// and edited in the same dialog (groupeditor_gui.go).

func (m *Mindmap) Prefs() []feature.PrefSpec {
	names, _ := viewThemeChoices(m.c)
	return []feature.PrefSpec{
		feature.ValuePref(prefMindTheme, "page", nil).Shows(func(p feature.PrefsView) map[string]any {
			return map[string]any{"App.mindThemeIndex": max(slices.Index(names, p.Value(prefMindTheme)), 0)}
		}).Then(func(feature.PrefsView) { m.relayout() }),
		feature.ValuePref(PrefMindDepth, "1", []string{"1", "2"}).Chooser("App.mindDepthIndex", "App.setMindDepth"),
	}
}

// relayout lays the map out again, in its theme, while it is open.
func (m *Mindmap) relayout() {
	if m.open {
		m.Load()
	}
}

// prefDepth is the depth the settings open the map at.
func (m *Mindmap) prefDepth() int { return mindDepth(m.c) }

// readGroups takes ws's groups from the store: its graph file's, or its first, made of types and
// the workspace's folders. The map is laid out again in them.
func (m *Mindmap) readGroups(ws string, types []string) {
	if m.store == nil {
		return
	}
	m.store.Groups(ws, ViewMindmap, types, m.palette().background, func(list []Group) {
		if ws == m.c.Workspace() {
			m.editor.listed(ws, types, list)
			m.groupsWS = ws
			m.groupsEdited(list)
		}
	})
}

// groupsEdited lays the map out again in list.
func (m *Mindmap) groupsEdited(list []Group) {
	gs := newRelGroups(list)
	m.groups = &gs
	m.relayout()
}

// openSettings is Relations › Mindmap settings…: the workspace's groups, read first when this
// workspace's are not yet (the page's neighbourhood names the schema's types).
func (m *Mindmap) OpenSettings() {
	ws := m.c.Workspace()
	m.c.Open("mindmapSettings")
	if ws == "" || (m.groupsWS == ws && m.editor.ws == ws && !m.editor.loading) {
		m.editor.show("") // its groups listed already: as they are
		return
	}
	var types []string
	if m.cur != nil && m.cur.root.WS == ws {
		types = m.cur.m.Types
	}
	m.editor.loadingFor(ws) // not the last workspace's groups while ws's are read
	m.readGroups(ws, types)
}

// setTheme is the theme chooser's row i.
func (m *Mindmap) setTheme(i int) {
	if names, _ := viewThemeChoices(m.c); i >= 0 && i < len(names) {
		m.c.SetPref(prefMindTheme, names[i])
	}
}

// settingsState is the dialog's lists.
func (m *Mindmap) settingsState() map[string]any {
	names, themes := viewThemeChoices(m.c)
	st := m.editor.state()
	maps.Copy(st, map[string]any{
		"App.mindThemes": feature.KeyedChoices(names, themes), "App.mindDepths": relationChoice(m.c, "App.mindDepths"),
		"App.mindShapeChoices": relationChoice(m.c, "App.mindShapeChoices"),
	})
	return st
}

func (m *Mindmap) settingsCommands() map[string]feature.Command {
	c := m.editor.commands()
	c["App.openMindmapSettings"] = feature.None(m.OpenSettings)
	c["App.setMindTheme"] = feature.OneNumber("a row", m.setTheme)
	return c
}
