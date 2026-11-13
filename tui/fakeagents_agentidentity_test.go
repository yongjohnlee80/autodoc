package tui

import (
	"sync"
	"time"

	"github.com/yongjohnlee80/autodoc/tui/agents"
	"github.com/yongjohnlee80/autodoc/tui/feature"
)

// Copies of the agents' test fakes agentidentity_test.go declares, for the host's cells.

// Exercise profile persistence/validation without starting a terminal. Other
// panel services remain absent so an invalid target cannot accidentally spawn.
type identityHost struct {
	agents.AgentHost
	p                 prefs
	state             map[string]any
	notes             []string
	stopErr           error
	ws                wsInfo
	configuredCommand string
	panel             *modelPanel
	postMu            sync.Mutex // a held removal's retirement posts from its own goroutine
	posts             []func()
	afters            []func()
	delays            []time.Duration
	questions         []feature.Question
	epoch             int
	closed            []string // the drawers CloseDrawer closed, in order
	opened            []string // the drawers OpenDrawer opened, in order
}

func newIdentityHost(raw string) *identityHost {
	return &identityHost{p: prefs{values: map[string]string{
		agents.PrefAgentProfiles: raw, agents.PrefAgentDefaultCodename: "",
	}}, state: map[string]any{}}
}

func (c *identityHost) Prefs() feature.PrefsView { return c.p }

func (c *identityHost) SetPref(name, value string) { c.p.values[name] = value }

func (c *identityHost) Set(name string, value any) { c.state[name] = value }
func (c *identityHost) Say(msg string)             { c.state["App.status"] = msg }

func (c *identityHost) SetFormText(name, value string) { c.Set(name, value) }

func (*identityHost) Open(string) {}

func (*identityHost) Close(string) {}

func (*identityHost) Focus(string) error { return nil }

func (*identityHost) DaemonAddress() string { return "unused" }

func (c *identityHost) ActiveWorkspace() (feature.Workspace, bool) {
	return workspaceOf(c.ws), c.ws.name != ""
}

func (c *identityHost) ActiveWorkspaceInfo() (feature.Workspace, bool) {
	return workspaceOf(c.ws), c.ws.name != ""
}

func (c *identityHost) ConfigureTerminal(_, _, _ string, args ...string) error {
	if len(args) > 0 {
		c.configuredCommand = args[len(args)-1]
	}
	return nil
}

func (c *identityHost) Notify(msg string) { c.notes = append(c.notes, msg) }

func (*identityHost) Tr(id string) string { return englishText(id) }

func (f *identityHost) Trf(id string, args ...any) string {
	return feature.Fill(englishText(id), args...)
}

func (c *identityHost) WidgetsChanged() {}

func (c *identityHost) CallView(string, string, ...any) error { return c.stopErr }

func (c *identityHost) Keep(err error) {
	if err != nil {
		c.notes = append(c.notes, err.Error())
	}
}
