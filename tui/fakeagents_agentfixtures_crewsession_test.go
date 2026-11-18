package tui

import (
	"github.com/yongjohnlee80/autodoc/tui/agents"
	"github.com/yongjohnlee80/golib/tui/pty"
	"github.com/yongjohnlee80/golib/tui/widget/terminal"
)

// Copies of the agents' test fakes agentfixtures_crewsession_test.go declares, for the host's cells.

const oneTwo = `[{"name":"One","command":"one","codename":"one"},{"name":"Two","command":"two","codename":"two"}]`

// startedEnv is the variables instance's tab starts its program with (its environment, a later
// entry winning), through the tab's own start (runAgent), its program not run; err is the start's
// refusal.
func startedEnv(a *agents.Agents, c *identityHost, instance *agents.AgentInstance) (env []string, spawned bool, err error) {
	cmd, spawned, err := startedCmd(a, c, instance)
	return cmd.Env, spawned, err
}

// startedCmd is the command instance's tab starts its program with, as startedEnv. Its process
// is a fake terminal, never nil: a Codex agent's server is bound to it, and closes it when it
// ends (as the cell's cleanup ends it).
func startedCmd(a *agents.Agents, c *identityHost, instance *agents.AgentInstance) (started pty.Cmd, spawned bool, err error) {
	cfg := c.panel.configs[instance.State().Tab]
	spawn := seamsOf(a).Spawn()
	defer func() { seamsOf(a).setAgentSpawn(spawn) }()
	seamsOf(a).setAgentSpawn(func(cmd pty.Cmd) (terminal.TerminalProcess, error) {
		started, spawned = cmd, true
		return newFakeTerminal(), nil
	})
	_, err = cfg.Start(pty.Cmd{Path: cfg.Command, Args: cfg.Args, Dir: cfg.Dir})
	return started, spawned, err
}
