//go:build linux || darwin

package tui

import "testing"

// On Unix a profile's command is read as /bin/sh reads it: a path with a backslash-escaped space and
// a quoted argument is that program, and its kind its name.
func TestAPathInAProfilesCommandIsItsProgramOnUnix(t *testing.T) {
	cmd := `/opt/a\ b/claude --model "haiku 4"`
	if got := agentProgram(cmd); got != "/opt/a b/claude" {
		t.Errorf("agentProgram(%q) = %q, want the path", cmd, got)
	}
	if got := agentKind(cmd); got != "claude" {
		t.Errorf("agentKind(%q) = %q, want claude", cmd, got)
	}
}
