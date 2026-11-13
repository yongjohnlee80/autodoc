package tui

import (
	"errors"
	"testing"

	"github.com/yongjohnlee80/autodoc/tui/agents"
	"github.com/yongjohnlee80/golib/tui/pty"
	"github.com/yongjohnlee80/golib/tui/widget/terminal"
)

// retryTab starts codename's tab again as the terminal's Enter does: its starter (runAgent), off
// the agents' own start. It answers the starter's error.
func retryTab(t *testing.T, a *agents.Agents, c *identityHost, codename string) error {
	t.Helper()
	in, ok := a.Snapshot().Instances[codename]
	if !ok {
		t.Fatalf("no %s instance", codename)
	}
	return c.panel.Start(in.Tab)
}

// TestARetryAfterAFailedCrewStartSpawnsNothing: a crew agent whose program failed to start has
// its session ended and its context cleared; a retry of its tab (the terminal's Enter) is refused
// as a crew start the window didn't set up, and spawns nothing.
func TestARetryAfterAFailedCrewStartSpawnsNothing(t *testing.T) {
	a, c, w := startingCrewAgents(t, oneTwo)
	spawns := 0
	seamsOf(a).setAgentSpawn(func(pty.Cmd) (terminal.TerminalProcess, error) {
		spawns++
		return nil, errors.New("no such program")
	})
	p, _ := a.Profile("one")
	if _, err := a.StartInstance(p); err != nil {
		t.Fatal(err)
	}
	if spawns != 1 || calledTimes(w, "crew.exit") != 1 {
		t.Fatalf("spawns %d, crew.exit %d: want the start failed and its session ended", spawns, calledTimes(w, "crew.exit"))
	}
	if err := retryTab(t, a, c, "one"); !errors.Is(err, agents.ErrCrewStartHere) {
		t.Fatalf("the retry answered %v, want ErrCrewStartHere", err)
	}
	if spawns != 1 {
		t.Fatalf("the retry spawned (%d spawns): it started under the ended session's context", spawns)
	}
}

// TestARetryAfterACrewAgentExitedSpawnsNothing: a crew agent's program exits, its session ending
// with it; a retry of its tab (the terminal's Enter) is refused as a crew start the window didn't
// set up, and spawns nothing: a next start is the window's, which registers a new session.
func TestARetryAfterACrewAgentExitedSpawnsNothing(t *testing.T) {
	a, c, _ := startingCrewAgents(t, oneTwo)
	spawns := 0
	seamsOf(a).setAgentSpawn(func(pty.Cmd) (terminal.TerminalProcess, error) {
		spawns++
		return newFakeTerminal(), nil
	})
	p, _ := a.Profile("one")
	if _, err := a.StartInstance(p); err != nil {
		t.Fatal(err)
	}
	in := a.Snapshot().Instances["one"]
	if spawns != 1 || !in.Running {
		t.Fatalf("spawns %d running %v: want it running", spawns, in.Running)
	}
	c.panel.configs[in.Tab].Exited(0)
	if err := retryTab(t, a, c, "one"); !errors.Is(err, agents.ErrCrewStartHere) {
		t.Fatalf("the retry answered %v, want ErrCrewStartHere", err)
	}
	if spawns != 1 {
		t.Fatalf("the retry spawned (%d spawns): it started under the exited session's context", spawns)
	}
}
