package app

import (
	"context"

	"github.com/yongjohnlee80/autodoc/tui"
)

// runUI is --ui: AutoDoc's TUI (tui.Launch), attached to the local daemon, which it starts (once)
// when nothing answers on the socket. It opens workspace: one the daemon must have, checked before
// the TUI starts, or, when "", the one the TUI last entered. dev, when set, reads the TUI's QML from
// that directory and follows it. window (--gui) runs the same UI in a window (runGUI).
//
// debug (--debug) records the input and event stream to one file, the window's and
// the TUI's both: the recorder is handed to whichever UI runs, and closed when it
// ends, which flushes.
func runUI(ctx context.Context, configPath, dev, workspace string, window bool, b build, debug *debugFlag) error {
	o := tui.LaunchOptions{ConfigPath: configPath, Dev: dev, Workspace: workspace, Version: b.version, BuildDate: b.buildDate, Registrations: b.reg.Tables(),
		TermOptions: termOptions()}
	if debug.given {
		path := debug.path
		if path == "" {
			path = defaultDebugPath()
		}
		f, rec, err := openDebugRecording(path)
		if err != nil {
			return err
		}
		defer f.Close()
		o.DebugRecorder = rec
		defer rec.Close() // the window's backend closes it too; this is the TUI-only flush
	}
	if window {
		return runGUI(ctx, o)
	}
	return tui.Launch(ctx, o)
}
