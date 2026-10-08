package app

import (
	"context"

	"github.com/yongjohnlee80/autodoc/tui"
)

// runUI is --ui: AutoDoc's TUI (tui.Launch), attached to the local daemon, which it starts (once)
// when nothing answers on the socket. It opens workspace: one the daemon must have, checked before
// the TUI starts, or, when "", the one the TUI last entered. dev, when set, reads the TUI's QML from
// that directory and follows it. window (--gui) runs the same UI in a window (runGUI).
func runUI(ctx context.Context, configPath, dev, workspace string, window bool, b build) error {
	o := tui.LaunchOptions{ConfigPath: configPath, Dev: dev, Workspace: workspace, Version: b.version, Registrations: b.reg.Tables(),
		TermOptions: termOptions()}
	if window {
		return runGUI(ctx, o)
	}
	return tui.Launch(ctx, o)
}
