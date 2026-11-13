//go:build unix

package usershell

import "testing"

// On Unix an absolute path is looked up as it is written.
func TestAnAbsolutePathIsLookedUpOnUnix(t *testing.T) {
	if got, ok := (Shell{kind: posix}).LooksUp("/usr/local/bin/claude --x"); !ok || got != "/usr/local/bin/claude" {
		t.Errorf("%q %v", got, ok)
	}
}
