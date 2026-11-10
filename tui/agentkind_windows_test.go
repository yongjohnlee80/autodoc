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

// TestAgentKindReadsPowerShellsWordsOnWindows: on Windows an agent's kind is the program its command
// runs, its words read as PowerShell reads them (a backslash a path's separator, a program named
// without its PATHEXT extension), past its call operator and the wrappers named.
func TestAgentKindReadsPowerShellsWordsOnWindows(t *testing.T) {
	for command, want := range map[string]string{
		"claude --model haiku":                         "claude",
		`C:\Users\u\AppData\Roaming\npm\claude.cmd`:    "claude",
		`& 'C:\Program Files\x\claude.cmd' --resume`:   "claude",
		`& "C:\Program Files\x\codex.exe" --full-auto`: "codex",
		`'it''s' --x`:                    "it's",
		"mise x -- claude":               "claude",
		"mise.exe exec node@22 -- codex": "codex",
		"firejail claude":                "firejail",
		"":                               "agent",
	} {
		if got := agentKind(command); got != want {
			t.Errorf("agentKind(%q) = %q, want %q", command, got, want)
		}
	}
}
