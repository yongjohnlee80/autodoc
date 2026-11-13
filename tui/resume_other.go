//go:build !unix

package tui

import (
	"github.com/yongjohnlee80/autodoc/tui/feature"
	"github.com/yongjohnlee80/golib/tui/widget/terminal"
)

// resumeTab refuses: there is no SIGCONT here, and no program stops itself on Ctrl+Z.
func resumeTab(*terminal.TerminalTabs, terminal.TerminalTabID) error {
	return feature.ErrResumeUnsupported
}
