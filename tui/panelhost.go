package tui

import "fmt"

// PanelHost is the UI service boundary a panel feature uses. It exposes view
// operations and preference persistence, not Host fields or the program tree.
// Every method runs on the UI loop.
type PanelHost interface {
	Set(name string, v any)
	Open(id string)
	Close(id string)
	Notify(msg string)
	Ask(q Question) Asked
	Prefs() prefs
	CallView(id, method string, args ...any) error
	Keep(err error)
	Focus(id string) error
	Focused(id string) (bool, error)
	// RestoreKeyboardPane is navigation to the editor or a receiving pane,
	// never an arbitrary component's method or focus query.
	RestoreKeyboardPane(id string) error
	KeyboardPane() string
	PanelOpen(id string) bool
	TogglePanel(id string)
	OpenDrawer(id string)
	CloseDrawer(id string)
	DockPanel(id string)
	SetPref(name, value string)
	KeepPanelGeometry(id, dimension string, percent int)
	ActiveWorkspace() (wsInfo, bool)
	// RefreshRelations is transitional while links still belongs to legacy.
	// Remove it when the relations Feature owns its panel's open hook.
	RefreshRelations()
}

// agentHost adds only the services needed to configure an agent terminal and
// populate its profile form. Agent-specific state remains in the agents feature.
type agentHost interface {
	PanelHost
	// SetFormText resets edited form text even if its source value is unchanged.
	SetFormText(name, value string)
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
func (c core) KeepPanelGeometry(id, dimension string, percent int) {
	c.h.keepPanelGeo(id, dimension, percent)
}
func (c core) ActiveWorkspace() (wsInfo, bool) { return c.h.activeWorkspaceInfo() }
func (c core) RefreshRelations()               { c.h.relationsOfOpenFile() }
func (c core) SetFormText(name, value string)  { c.h.setField(name, value) }
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
