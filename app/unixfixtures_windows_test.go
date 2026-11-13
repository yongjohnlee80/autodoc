//go:build windows

package app

import "testing"

// withCodexSocketRoots's fixture (crewcodex_test.go) is a Unix socket folder; not ported, so a cell
// that needs it is skipped on Windows, and says so.
func withCodexSocketRoots(t *testing.T, _ string) {
	t.Helper()
	t.Skip("needs Codex's Unix socket folder, not ported to Windows")
}
