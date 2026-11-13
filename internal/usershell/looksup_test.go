package usershell

import "testing"

// A shell looks up a bare name as it is written; everything else in a line it resolves itself, in
// its own syntax or as one of its own words (a builtin, a keyword, PowerShell's aliases and
// cmdlets), so nobody else can judge it. PowerShell's syntax is not /bin/sh's: its braces and
// @ are its own, and on Windows a backslash is a separator.
func TestAShellLooksUpOnlyABareName(t *testing.T) {
	cases := []struct {
		kind    kind
		line    string
		program string // "" when the shell resolves it itself
	}{
		{posix, "claude --resume", "claude"},
		{posix, "  claude", "claude"},
		{posix, "", ""},
		{posix, "FOO=1 claude", ""},
		{posix, "claude | tee log", ""},
		{posix, "$HOME/bin/claude", ""},
		{posix, `claude\ x`, ""},
		{posix, "~/bin/claude", ""},
		{posix, "./claude", ""},
		{posix, "bin/claude", ""},
		{posix, "claude*", ""},
		{posix, "claude{a,b}", "claude{a,b}"}, // sh -c has no braces: a name, if a strange one
		{powershell, "claude --resume", "claude"},
		{powershell, "$env:CLAUDE_CONFIG_DIR='x'; claude", ""},
		{powershell, "& claude", ""},
		{powershell, "claude @args", ""},
		{powershell, "{ claude }", ""},
		{powershell, "~/bin/claude", ""},
		{powershell, "./claude", ""},
		{powershell, "claude*", ""},
		{winps, "claude --resume", "claude"},
		{winps, "claude; claude", ""},
		// the shell's own words: never on PATH, never judged
		{posix, "exec claude", ""},
		{posix, "command claude", ""},
		{posix, "cd x", ""},
		{posix, "claude-work", "claude-work"},
		{powershell, "Write-Output hi", ""},
		{powershell, "write-output hi", ""},
		{powershell, "Get-ChildItem", ""},
		{winps, "ls -x", ""},
		{winps, "LS", ""},
		{powershell, "foreach x", ""},
		{powershell, "if x", ""},
		{powershell, "claude-work", "claude-work"}, // no approved verb: a program's name
		{winps, "My-Tool --x", "My-Tool"},
		{powershell, "exec claude", "exec"}, // /bin/sh's word, PowerShell's program
	}
	for _, c := range cases {
		got, ok := Shell{kind: c.kind}.LooksUp(c.line)
		if ok != (c.program != "") || got != c.program {
			t.Errorf("kind %d, %q: %q %v; want %q", c.kind, c.line, got, ok, c.program)
		}
	}
}

// Describe says how a line is run: the program and the arguments before the line.
func TestDescribeIsTheProgramAndItsArguments(t *testing.T) {
	if got := (Shell{Program: "/bin/sh", kind: posix}).Describe(); got != "/bin/sh -c" {
		t.Errorf("posix: %q", got)
	}
	if got := (Shell{Program: "pwsh", kind: powershell}).Describe(); got != "pwsh -NoLogo -NoProfile -Command" {
		t.Errorf("powershell: %q", got)
	}
}
