package tui

import (
	tuidecl "github.com/yongjohnlee80/golib/tui/decl"
	"github.com/yongjohnlee80/golib/tui/widget/terminal"

	"github.com/yongjohnlee80/autodoc/tui/feature"
)

// agentsState is what the agents hold at a moment (Snapshot): the agent running in the panel and
// the one selected (codenames), how many agent CLIs the first run's setup found, the host the
// feature was bound with, its profiles' list model, and each agent's instance by codename.
type agentsState struct {
	Running, Selected string
	Found             int
	Host              agentHost
	Rows              *tuidecl.ListModel
	Instances         map[string]instanceState
}

// instanceState is an agent's instance as Snapshot reads it: its tab, its token on the editor
// socket and the workspace it was started in.
type instanceState struct {
	Tab       terminal.TerminalTabID
	Token     string
	Workspace feature.Workspace
}

// Snapshot is what the agents hold now (agentsState), a copy the caller may keep. On the loop.
func (a *agents) Snapshot() agentsState {
	s := agentsState{Running: a.agentRunning, Selected: a.selectedAgent, Found: len(a.setupFound), Host: a.c,
		Rows: a.agentRows, Instances: make(map[string]instanceState, len(a.instances))}
	for code, in := range a.instances {
		s.Instances[code] = instanceState{Tab: in.tab, Token: in.token, Workspace: in.workspace}
	}
	return s
}
