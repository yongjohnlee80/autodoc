//go:build linux || darwin

package tui

import (
	"strings"
	"testing"

	"github.com/yongjohnlee80/autodoc/tui/agents"
	"github.com/yongjohnlee80/golib/tui/decl/decltest"
)

// TestAnAgentTabTakesWhatIsTypedWhileItsCLIStarts: keys typed into an agent's tab while its CLI's
// spawn is out are the CLI's, never the window's: once it runs it reads them, in order.
func TestAnAgentTabTakesWhatIsTypedWhileItsCLIStarts(t *testing.T) {
	t.Parallel()
	agent, _ := fakeAgent(t)
	s := newHeldShell()
	sm := seamsWithEnv(t, nil)
	sm.setAgentSpawn(s.start)
	profiles := []agents.AgentProfile{{Name: "heron", Command: agents.ShellQuote(agent), Codename: "heron"}}
	_, r, _ := runAgentTUIWith(t, t.TempDir(), profiles, "heron", nil, Options{seams: sm})
	t.Cleanup(s.free)
	r.leader(t, 'g')
	s.spawnOut(t)
	r.s.WaitFor(t, "the agent's keyboard", func(string) bool { return r.focused("agentView") })
	r.keys(t, decltest.Type("hello\r")...)
	s.free()
	r.waitWithin(t, "the CLI reading what was typed", daemonWait, func() bool { return strings.Contains(s.typed(), "hello\r") })
}
