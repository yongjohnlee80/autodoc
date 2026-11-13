//go:build linux || darwin

package tui

import (
	"testing"

	"github.com/yongjohnlee80/autodoc/tui/agents"
	"github.com/yongjohnlee80/golib/tui/pty"
	"github.com/yongjohnlee80/golib/tui/widget/terminal"
)

// TestAnAgentsSpawnHookSetOnTheLoopWhileItStarts: an agent starts (its starter off the loop, which
// reads the Spawn hook) while the loop sets that hook again and again, as a test may while its
// program runs. Under -race nothing is reported: the program reads and sets its own copy of the
// hook atomically. The agent runs.
func TestAnAgentsSpawnHookSetOnTheLoopWhileItStarts(t *testing.T) {
	t.Parallel()
	agent, _ := fakeAgent(t)
	spawn := func(c pty.Cmd) (terminal.TerminalProcess, error) { return pty.Start(c) }
	profiles := []agents.AgentProfile{{Name: "heron", Command: agents.ShellQuote(agent), Codename: "heron"}}
	_, r, _ := runAgentTUI(t, t.TempDir(), profiles, "heron")
	onLoop(r, func() bool {
		p, _ := r.h.agents.Profile("heron")
		if _, err := r.h.agents.StartInstance(p); err != nil {
			t.Error(err)
		}
		for range 400 { // while its start is out, off the loop
			r.h.seams.setAgentSpawn(spawn)
		}
		return true
	})
	r.waitWithin(t, "the agent running", daemonWait, func() bool { return r.agentRunningNow() })
}
