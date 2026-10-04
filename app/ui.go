package app

import (
	"context"

	"github.com/yongjohnlee80/autodoc/tui"
)

// runUI is --ui: AutoDoc's TUI (tui.Launch), attached to the local daemon, which it starts (once)
// when nothing answers on the socket. It opens workspace: one the daemon must have, checked before
// the TUI starts, or, when "", the one the TUI last entered. dev, when set, reads the TUI's QML from
// that directory and follows it.
func runUI(ctx context.Context, configPath, dev, workspace string, b build) error {
	return tui.Launch(ctx, tui.LaunchOptions{ConfigPath: configPath, Dev: dev, Workspace: workspace, Version: b.version,
		TermOptions: termOptions()})
}
