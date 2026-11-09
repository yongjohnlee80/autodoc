package tui

// PanelHost is the UI service boundary a panel feature uses. It exposes view
// operations and preference persistence, not Host fields or the program tree.
// Every method runs on the UI loop.
type PanelHost interface {
	Set(name string, v any)
	Open(id string)
	Close(id string)
	// Tr and Trf are Core's: catalog text in the UI's language (language.go)
	Tr(id string) string
	Trf(id string, args ...any) string
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
	// RestoreKeyboardHome is a closed panel's way home: the keyboard to the topmost window still
	// open, a panel, else the page, as a window manager focuses the next window down.
	RestoreKeyboardHome() error
	PanelOpen(id string) bool
	TogglePanel(id string)
	OpenDrawer(id string)
	CloseDrawer(id string)
	DockPanel(id string)
	SetPref(name, value string)
	KeepPanelGeometry(id, dimension string, percent int)
	ActiveWorkspace() (wsInfo, bool)
	// WidgetsChanged: what of the panel runs on with its drawer closed changed (dock.go)
	WidgetsChanged()
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
