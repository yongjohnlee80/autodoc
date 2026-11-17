//go:build windows

package tui

import "testing"

// The fixtures below stand for what Windows has not: a cell that needs one is skipped, saying why,
// rather than keeping every other cell in this package from compiling. A cell that needs only a
// shell running uses the line shell (runLineShellTUI), and an agent's cells fakeAgent: both run on
// every OS.

// realShell is why a cell that types shell commands (seq, printf, cat -v, $((…))) is Unix's: the line
// shell other OSes run does not run them.
const realShell = "types real shell commands, which only a POSIX shell runs"

// agentServer is why a cell that starts a crew agent's own server is Unix's: the server needs process
// groups, so on Windows none is started (agents.CanRunAgentServers) and a Codex agent's setup asks
// for none.
const agentServer = "starts an agent's own server, which needs process groups (none on Windows)"

func runShellTUI(t *testing.T, _ map[string]string) (*daemon, *running) {
	t.Helper()
	t.Skip(realShell)
	return nil, nil
}

func fakeServer(t *testing.T, _ string, _ bool) []string {
	t.Helper()
	t.Skip(agentServer)
	return nil
}

func catInTheTerminal(t *testing.T, _ *running) {
	t.Helper()
	t.Skip(realShell)
}
