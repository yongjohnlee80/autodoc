package app

import (
	"context"
	"os"

	"github.com/yongjohnlee80/autodoc/tui"
)

// runUI is --ui: AutoDoc's TUI (tui.Launch), attached to the local daemon, which it starts (once)
// when nothing answers on the socket. It opens workspace: one the daemon must have, checked before
// the TUI starts, or, when "", the one the TUI last entered. dev, when set, reads the TUI's QML from
// that directory and follows it. window (--gui) runs the same UI in a window (runGUI).
//
// debug (--debug) records the input and event stream to one file, the window's and
// the TUI's both: the recorder is handed to whichever UI runs, and its owner
// closes it when the UI exits, which flushes. A recording that could not be
// written whole fails the command. profile (--profile, or AUTODOC_PROFILE) runs a memory monitor
// beside the UI, closed with the recording.
func runUI(ctx context.Context, configPath, dev, workspace string, window bool, b build, debug *debugFlag, profile *profileFlag) error {
	o := tui.LaunchOptions{ConfigPath: configPath, Dev: dev, Workspace: workspace, Version: b.version, BuildDate: b.buildDate, Registrations: b.reg.Tables(),
		TermOptions: termOptions()}
	mode := "ui"
	if window {
		mode = "gui"
	}
	prof, err := startProfile(ctx, profile, mode, os.Stderr)
	if err != nil {
		return err
	}
	var dbg *debugRecording
	if debug.given {
		path := debug.path
		if path == "" {
			path = defaultDebugPath()
		}
		f, rec, err := openDebugRecording(path)
		if err != nil {
			return finishRun(err, prof)
		}
		o.DebugRecorder = rec
		dbg = &debugRecording{f: f, rec: rec}
	}
	owned := []owner{dbg, prof}
	if window {
		return runGUI(ctx, o, owned)
	}
	return finishRun(tui.Launch(ctx, o), owned...)
}
