// Package usershell is the shell AutoDoc runs commands and terminals with: which program, how it is
// told to run a command line, and how one argument is quoted for it. On Unix a command line is
// /bin/sh's and a terminal starts $SHELL; on Windows both are PowerShell's (pwsh when it is on PATH,
// else Windows PowerShell). A terminal falls back to %COMSPEC% (cmd.exe) where neither is found; a
// command line never does, since a line cmd reads can't reach it whole through a program's command
// line (Go writes one as a program reads it, which is not as cmd does).
//
// The OS-specific part, which shell, is in this package's per-OS files.
package usershell

import "github.com/yongjohnlee80/autodoc/internal/procenv"

// kind is how a shell reads a command line.
type kind int

const (
	posix      kind = iota // sh -c
	powershell             // pwsh -Command: an argument reaches a program as it is (PowerShell 7.3's "Standard" passing)
	winps                  // Windows PowerShell 5.1 -Command: its "Legacy" passing, which a quote and a last backslash need help through
)

// Shell is a shell that runs command lines: its program and how it reads one.
type Shell struct {
	Program string
	kind    kind
}

// Commands is the shell a command line is run with in env (nil: this process's): an agent
// profile's command, an editor.terminal_run line.
func Commands(env procenv.Env) Shell { return commands(env) }

// Interactive is the program and arguments a new terminal tab starts in env (nil: this process's).
func Interactive(env procenv.Env) []string { return interactive(env) }

// Run is the program and arguments that run line in s.
func (s Shell) Run(line string) (string, []string) {
	switch s.kind {
	case powershell, winps:
		return s.Program, []string{"-NoLogo", "-NoProfile", "-Command", line}
	default:
		return s.Program, []string{"-c", line}
	}
}

// Quote is arg as one word of a command line s runs, standing for itself: no character in it is
// the shell's ($, ;, &, |, a quote, a subexpression).
func (s Shell) Quote(arg string) string {
	switch s.kind {
	case powershell:
		return quotePowerShell(arg)
	case winps:
		return quotePowerShell(legacyNative(arg))
	default:
		return quotePOSIX(arg)
	}
}
