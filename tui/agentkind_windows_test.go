package tui

import "testing"

// On Windows a profile's command is read as PowerShell reads it: a quoted path with spaces, run
// through the call operator with quoted arguments, is that program, and claude.cmd is a Claude agent.
func TestAPathInAProfilesCommandIsItsProgramOnWindows(t *testing.T) {
	cmd := `& 'C:\Program Files\x\claude.cmd' --model "haiku 4"`
	if got := agentProgram(cmd); got != `C:\Program Files\x\claude.cmd` {
		t.Errorf("agentProgram(%q) = %q, want the path", cmd, got)
	}
	if got := agentKind(cmd); got != "claude" {
		t.Errorf("agentKind(%q) = %q, want claude", cmd, got)
	}
}
