//go:build windows

package tui

import "testing"

// The fixtures below start a POSIX shell or a shell-script stand-in for an agent's CLI
// (terminal_test.go, agent_test.go, agentswitch_test.go, agentserver_test.go, terminalkeymap_test.go).
// They are not ported: on Windows a cell that needs one is skipped, and says so, rather than
// keeping every other cell in this package from compiling.

const unixFixture = "needs a POSIX shell fixture, not ported to Windows"

func runShellTUI(t *testing.T, _ map[string]string) (*daemon, *running) {
	t.Helper()
	t.Skip(unixFixture)
	return nil, nil
}

func runAgentTUI(t *testing.T, _ string, _ []agentProfile, _ string) (*daemon, *running, string) {
	t.Helper()
	t.Skip(unixFixture)
	return nil, nil, ""
}

func runAgentTUIWith(t *testing.T, _ string, _ []agentProfile, _ string, _ map[string]string, _ Options) (*daemon, *running, string) {
	t.Helper()
	t.Skip(unixFixture)
	return nil, nil, ""
}

func runAgentTUIIn(t *testing.T, _ string, _ []agentProfile, _ string, _ map[string]string, _ Options, _ map[string][]string) (*daemon, *running, string) {
	t.Helper()
	t.Skip(unixFixture)
	return nil, nil, ""
}

func fakeAgent(t *testing.T) (string, string) {
	t.Helper()
	t.Skip(unixFixture)
	return "", ""
}

func recordingAgent(t *testing.T, _ bool) (string, string) {
	t.Helper()
	t.Skip(unixFixture)
	return "", ""
}

func fakeServer(t *testing.T, _ string, _ bool) []string {
	t.Helper()
	t.Skip(unixFixture)
	return nil
}

func catInTheTerminal(t *testing.T, _ *running) {
	t.Helper()
	t.Skip(unixFixture)
}
