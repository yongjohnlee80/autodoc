package tui

import "fmt"

// PanelHost is the UI service boundary a panel feature uses. It exposes view
// operations and preference persistence, not Host fields or the program tree.
// Every method runs on the UI loop.
type PanelHost interface {
	Core
	CallView(id, method string, args ...any) error
	Keep(err error)
	Focus(id string)
	Focused(id string) bool
	KeyboardPane() string
	PanelOpen(id string) bool
	TogglePanel(id string)
	OpenDrawer(id string)
	CloseDrawer(id string)
	DockPanel(id string)
	KeepPreference(name, value string, change func(*prefs))
	KeepPanelGeometry(id, dimension string, percent int)
	ActiveWorkspace() (wsInfo, bool)
	RefreshRelations()
}

// agentHost adds only the services needed to configure an agent terminal and
// populate its profile form. Process ownership remains in the agents feature.
type agentHost interface {
	PanelHost
	SetField(name, value string)
	ConfigureTerminal(id, command, dir string, args ...string) error
	DaemonAddress() string
}

func (c core) CallView(id, method string, args ...any) error { return c.h.p.Call(id, method, args...) }
func (c core) Keep(err error)                                { c.h.keep(err) }
func (c core) Focused(id string) bool                        { return c.h.paneHasKeyboard(id) }
func (c core) KeyboardPane() string                          { return c.h.keyboardPane() }
func (c core) PanelOpen(id string) bool                      { return c.h.panelOpen[id] }
func (c core) TogglePanel(id string)                         { c.h.togglePanel(id) }
func (c core) OpenDrawer(id string)                          { c.h.openDrawer(id) }
func (c core) CloseDrawer(id string)                         { c.h.closeDrawer(id) }
func (c core) DockPanel(id string)                           { c.h.dockPanel(id) }
func (c core) KeepPreference(name, value string, change func(*prefs)) {
	c.h.setPref(name, value, change)
}
func (c core) KeepPanelGeometry(id, dimension string, percent int) {
	c.h.keepPanelGeo(id, dimension, percent)
}
func (c core) ActiveWorkspace() (wsInfo, bool) { return c.h.activeWorkspaceInfo() }
func (c core) RefreshRelations()               { c.h.relationsOfOpenFile() }
func (c core) SetField(name, value string)     { c.h.setField(name, value) }
func (c core) DaemonAddress() string           { return c.h.session.Address() }
func (c core) ConfigureTerminal(id, command, dir string, args ...string) error {
	view, ok := c.h.p.Find(id)
	if !ok {
		return fmt.Errorf("no terminal view %q", id)
	}
	t, ok := view.(interface {
		SetCommand(string, ...string)
		SetDir(string)
	})
	if !ok {
		return fmt.Errorf("view %q is not a configurable terminal", id)
	}
	t.SetCommand(command, args...)
	t.SetDir(dir)
	return nil
}
