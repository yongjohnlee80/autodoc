//go:build unix

package tui

import (
	"syscall"

	"github.com/yongjohnlee80/golib/tui/widget/terminal"
)

// resumeTab sends tab id's program on tabs its SIGCONT, which a stopped program runs again on.
func resumeTab(tabs *terminal.TerminalTabs, id terminal.TerminalTabID) error {
	return tabs.Signal(id, syscall.SIGCONT)
}
