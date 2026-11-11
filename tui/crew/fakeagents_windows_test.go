//go:build windows

package crew

import (
	"testing"
)

// A copy of the host's unixFixture (unixfixtures_windows_test.go).
const unixFixture = "needs a POSIX shell fixture, not ported to Windows"

// A copy of the host's fakeServer (unixfixtures_windows_test.go): a cell that needs one is skipped.
func fakeServer(t *testing.T, _ string, _ bool) []string {
	t.Helper()
	t.Skip(unixFixture)
	return nil
}
