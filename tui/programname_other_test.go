//go:build !windows

package tui

// exeSuffix and scriptSuffix are what make a file named for a program that program, compiled or a
// script: on Unix, the name alone (programname_windows_test.go has Windows' extensions).
// wrapperScript is a wrapper that runs claude through mise, as a user writes one there.
const (
	exeSuffix, scriptSuffix = "", ""
	wrapperScript           = "#!/bin/bash\nexec mise x claude -- claude \"$@\"\n"
	// claudeWorkWrapper is a wrapper that runs claude on another account's settings
	claudeWorkWrapper = "#!/bin/sh\nCLAUDE_CONFIG_DIR=\"$HOME/.claude-work\" exec claude \"$@\"\n"
)
