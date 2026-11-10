package tui

import (
	"sync"
	"time"

	"github.com/yongjohnlee80/autodoc/tui/feature"
)

// Copies of the agents' test fakes agentidentity_test.go declares, for the host's cells.

// Exercise profile persistence/validation without starting a terminal. Other
// panel services remain absent so an invalid target cannot accidentally spawn.
type identityHostCopy struct {
	agentHost
	p                 prefs
	state             map[string]any
	notes             []string
	stopErr           error
	ws                wsInfo
	configuredCommand string
	panel             *modelPanelCopy
	postMu            sync.Mutex // a held removal's retirement posts from its own goroutine
	posts             []func()
	afters            []func()
	delays            []time.Duration
	questions         []feature.Question
	epoch             int
	closed            []string // the drawers CloseDrawer closed, in order
	opened            []string // the drawers OpenDrawer opened, in order
}

func newIdentityHostCopy(raw string) *identityHostCopy {
	return &identityHostCopy{p: prefs{values: map[string]string{
		prefAgentProfiles: raw, prefAgentDefaultCodename: "",
	}}, state: map[string]any{}}
}

func (c *identityHostCopy) Prefs() feature.PrefsView { return c.p }

func (c *identityHostCopy) SetPref(name, value string) { c.p.values[name] = value }

func (c *identityHostCopy) Set(name string, value any) { c.state[name] = value }

func (c *identityHostCopy) SetFormText(name, value string) { c.Set(name, value) }

func (*identityHostCopy) Open(string) {}

func (*identityHostCopy) Close(string) {}

func (*identityHostCopy) Focus(string) error { return nil }

func (*identityHostCopy) DaemonAddress() string { return "unused" }

func (c *identityHostCopy) ActiveWorkspace() (feature.Workspace, bool) {
	return workspaceOf(c.ws), c.ws.name != ""
}

func (c *identityHostCopy) ActiveWorkspaceInfo() (feature.Workspace, bool) {
	return workspaceOf(c.ws), c.ws.name != ""
}

func (c *identityHostCopy) ConfigureTerminal(_, _, _ string, args ...string) error {
	if len(args) > 0 {
		c.configuredCommand = args[len(args)-1]
	}
	return nil
}

func (c *identityHostCopy) Notify(msg string) { c.notes = append(c.notes, msg) }

func (*identityHostCopy) Tr(id string) string { return englishText(id) }

func (f *identityHostCopy) Trf(id string, args ...any) string {
	return feature.Fill(englishText(id), args...)
}

func (c *identityHostCopy) WidgetsChanged() {}

func (c *identityHostCopy) CallView(string, string, ...any) error { return c.stopErr }

func (c *identityHostCopy) Keep(err error) {
	if err != nil {
		c.notes = append(c.notes, err.Error())
	}
}
