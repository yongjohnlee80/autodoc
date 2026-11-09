//go:build windows

package crew_test

import "testing"

// claudeOnPath's fixture (setup_test.go) is a shell-script stand-in for Claude's CLI; not ported, so
// a cell that needs it is skipped on Windows, and says so.
func claudeOnPath(t *testing.T, _ ...string) string {
	t.Helper()
	t.Skip("needs a POSIX shell fixture, not ported to Windows")
	return ""
}
