package tui

import (
	"github.com/yongjohnlee80/golib/tui/pty"
	"github.com/yongjohnlee80/golib/tui/widget/terminal"
)

// Copies of the agents' test fakes agentfixtures_crewsession_test.go declares, for the host's cells.

const oneTwoCopy = `[{"name":"One","command":"one","codename":"one"},{"name":"Two","command":"two","codename":"two"}]`

// startedEnv is the variables instance's tab starts its program with (its environment, a later
// entry winning), through the tab's own start (runAgent), its program not run; err is the start's
// refusal.
func startedEnvCopy(a *agents, c *identityHostCopy, instance *agentInstance) (env []string, spawned bool, err error) {
	cmd, spawned, err := startedCmdCopy(a, c, instance)
	return cmd.Env, spawned, err
}

// startedCmd is the command instance's tab starts its program with, as startedEnv. Its process
// is a fake terminal, never nil: a Codex agent's server is bound to it, and closes it when it
// ends (as the cell's cleanup ends it).
func startedCmdCopy(a *agents, c *identityHostCopy, instance *agentInstance) (started pty.Cmd, spawned bool, err error) {
	cfg := c.panel.configs[instance.tab]
	spawn := a.spawn
	defer func() { a.spawn = spawn }()
	a.spawn = func(cmd pty.Cmd) (terminal.TerminalProcess, error) {
		started, spawned = cmd, true
		return newFakeTerminalCopy(), nil
	}
	_, err = cfg.Start(pty.Cmd{Path: cfg.Command, Args: cfg.Args, Dir: cfg.Dir})
	return started, spawned, err
}
