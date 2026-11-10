package tui

import (
	tuidecl "github.com/yongjohnlee80/golib/tui/decl"
	"github.com/yongjohnlee80/golib/tui/widget/terminal"

	"github.com/yongjohnlee80/autodoc/core/crew"
	"github.com/yongjohnlee80/autodoc/tui/feature"
)

// agentsState is what the agents hold at a moment (Snapshot): the agent running in the panel and
// the one selected (codenames), how many agent CLIs the first run's setup found, the host the
// feature was bound with, its profiles' list model, each agent's instance by codename, and the
// folder its agents' folders are made in.
type agentsState struct {
	Running, Selected string
	AgentDir          string
	Found             int
	Host              agentHost
	Rows              *tuidecl.ListModel
	Instances         map[string]instanceState
}

// instanceState is an agent's instance as it is at a moment (Snapshot, State): its tab, its token
// on the editor socket and the workspace it was started in; its folder; whether it is starting,
// running or closing; its crew session and its crew start's setup; the session it said it started
// with (a Claude agent's), and why its start was refused.
type instanceState struct {
	Tab                        terminal.TerminalTabID
	Token                      string
	Workspace                  feature.Workspace
	Dir                        string
	Starting, Running, Closing bool
	Session                    crewSession
	Setup                      crew.Setup
	Started                    string
	Refusal                    error
}

// State is the instance now (instanceState), a copy the caller may keep. On the loop.
func (in *agentInstance) State() instanceState {
	return instanceState{Tab: in.tab, Token: in.token, Workspace: in.workspace, Dir: in.dir, Starting: in.starting,
		Running: in.running, Closing: in.closing, Session: in.session, Setup: in.setup, Started: in.started, Refusal: in.refusal}
}

// Snapshot is what the agents hold now (agentsState), a copy the caller may keep. On the loop.
func (a *agents) Snapshot() agentsState {
	s := agentsState{Running: a.agentRunning, Selected: a.selectedAgent, AgentDir: a.agentDir, Found: len(a.setupFound), Host: a.c,
		Rows: a.agentRows, Instances: make(map[string]instanceState, len(a.instances))}
	for code, in := range a.instances {
		s.Instances[code] = in.State()
	}
	return s
}
