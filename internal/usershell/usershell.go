// Package usershell is the shell AutoDoc runs commands and terminals with: which program, how it is
// told to run a command line, and how one argument is quoted for it. On Unix a command line is
// /bin/sh's and a terminal starts $SHELL; on Windows both are PowerShell's (pwsh when it is on PATH,
// else Windows PowerShell). A terminal falls back to %COMSPEC% (cmd.exe) where neither is found; a
// command line never does, since a line cmd reads can't reach it whole through a program's command
// line (Go writes one as a program reads it, which is not as cmd does).
//
// The OS-specific part, which shell, is in this package's per-OS files.
package usershell

import (
	"errors"
	"fmt"
	"strings"

	"github.com/yongjohnlee80/autodoc/internal/procenv"
)

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

// Run is the program and arguments that run line in s. pwsh is told to pass a program's arguments
// in its Standard way whatever the program is: by default it passes a batch file's (an agent CLI's
// .cmd shim) in its Legacy way, which Quote's words are not written for.
func (s Shell) Run(line string) (string, []string) {
	switch s.kind {
	case powershell:
		return s.Program, []string{"-NoLogo", "-NoProfile", "-Command", "$PSNativeCommandArgumentPassing = 'Standard'; " + line}
	case winps:
		return s.Program, []string{"-NoLogo", "-NoProfile", "-Command", line}
	default:
		return s.Program, []string{"-c", line}
	}
}

// ErrNotCarried is an argument a program s starts can't be given as itself (Carries).
var ErrNotCarried = errors.New("an argument the shell can't hand a program as itself")

// Carries reports whether arg can be given, as itself, to any program a line s runs starts. Under
// either PowerShell on Windows the program may be a batch file (an agent CLI's npm .cmd shim),
// which cmd.exe runs, reading its arguments again through %*: there a %, &, |, <, > or ^ is
// cmd.exe's, whatever the quoting, and a line break ends the line. Such an argument is refused
// (ErrNotCarried, naming the character) rather than handed on changed. Under /bin/sh only a NUL,
// which no argument can hold, is refused.
func (s Shell) Carries(arg string) error {
	bad := "\x00"
	if s.kind == powershell || s.kind == winps {
		bad = "%&|<>^\r\n\x00"
	}
	if i := strings.IndexAny(arg, bad); i >= 0 {
		return fmt.Errorf("%w: %q holds %q", ErrNotCarried, arg, arg[i:i+1])
	}
	return nil
}

// Args is args written after a command of a line s runs, so the program it starts receives each one
// as itself: each Quote'd, or under Windows PowerShell 5.1 after its stop-parsing token (--%), each
// as Windows writes a program's argument (quoteWindowsArg), since 5.1's own passing splits an
// argument holding a quote and a space and drops an empty one. An argument Carries refuses is an
// error, and nothing is written. "" when args is empty.
func (s Shell) Args(args []string) (string, error) {
	for _, a := range args {
		if err := s.Carries(a); err != nil {
			return "", err
		}
	}
	var b strings.Builder
	if s.kind == winps && len(args) > 0 {
		b.WriteString(" --%")
	}
	for _, a := range args {
		b.WriteByte(' ')
		if s.kind == winps {
			b.WriteString(quoteWindowsArg(a))
		} else {
			b.WriteString(s.Quote(a))
		}
	}
	return b.String(), nil
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
