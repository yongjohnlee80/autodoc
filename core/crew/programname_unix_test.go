//go:build !windows

package crew_test

// exeSuffix and scriptSuffix are what make a file named for a program that program, compiled or a
// script: on Unix, its execute bit alone. tuiWrapper is a wrapper of another name, as a user names
// one.
const (
	exeSuffix, scriptSuffix = "", ""
	tuiWrapper              = "claude_tui.sh"
)
