//go:build !unix

package tui

import "github.com/yongjohnlee80/golib/tui/widget"

// resumeTab refuses: there is no SIGCONT here, and no program stops itself on Ctrl+Z.
func resumeTab(*widget.TerminalTabs, widget.TerminalTabID) error { return ErrResumeUnsupported }
