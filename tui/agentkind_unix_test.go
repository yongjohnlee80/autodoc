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

// TestAgentKindReadsTheShellsWords: an agent's kind is the program its command runs, its words read
// as /bin/sh reads them (as they always were on Unix), past assignments and the wrappers named; an unknown wrapper is its kind.
func TestAgentKindReadsTheShellsWords(t *testing.T) {
	for command, want := range map[string]string{
		"claude --model haiku":                   "claude",
		"NOTE='two words' claude --model haiku":  "claude",
		`A=1 B="x y" /usr/local/bin/codex`:       "codex",
		`'/opt/my tools/claude' --resume`:        "claude",
		`"cla"ude`:                               "claude",
		`claude\ code`:                           "claude code",
		"env -i HOME=/tmp claude":                "claude",
		"env -u FOO -C /tmp NOTE='a b' codex":    "codex",
		"/usr/bin/env -- codex":                  "codex",
		"env -S 'X=1 claude --model haiku'":      "claude",
		"exec -a name claude":                    "claude",
		"nohup codex":                            "codex",
		"command -- claude":                      "claude",
		"mise exec node@22 -- codex --full-auto": "codex",
		"mise x -- claude":                       "claude",
		"mise exec node@22":                      "mise",
		"'NOTE=x' claude":                        "NOTE=x", // a quoted NAME is no assignment: the shell runs it
		"firejail claude":                        "firejail",
		"":                                       "agent",
		"A=1":                                    "agent",
	} {
		if got := agentKind(command); got != want {
			t.Errorf("agentKind(%q) = %q, want %q", command, got, want)
		}
	}
}
