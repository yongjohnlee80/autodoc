//go:build windows

package crew

import (
	"testing"
)

// A copy of the host's fakeServer (unixfixtures_windows_test.go): a crew agent's own server needs
// process groups, so on Windows none is started (agents.CanRunAgentServers), and a cell that needs
// one is skipped, saying so.
func fakeServer(t *testing.T, _ string, _ bool) []string {
	t.Helper()
	t.Skip("starts an agent's own server, which needs process groups (none on Windows)")
	return nil
}
