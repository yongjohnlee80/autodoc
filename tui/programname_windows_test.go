package tui

// exeSuffix and scriptSuffix are what make a file named for a program that program, compiled or a
// script: on Windows, an extension PATHEXT names. wrapperScript is a wrapper that runs claude
// through mise, as a user writes one there.
const (
	exeSuffix, scriptSuffix = ".exe", ".cmd"
	wrapperScript           = "@echo off\r\nmise x claude -- claude %*\r\n"
	// claudeWorkWrapper is a wrapper that runs claude on another account's settings
	claudeWorkWrapper = "@echo off\r\nset CLAUDE_CONFIG_DIR=%USERPROFILE%\\.claude-work\r\nclaude %*\r\n"
)
