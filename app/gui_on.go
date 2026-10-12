//go:build gui

package app

import (
	"context"
	"errors"

	"github.com/yongjohnlee80/golib/gui"
	guidecl "github.com/yongjohnlee80/golib/gui/decl"
	tuidecl "github.com/yongjohnlee80/golib/tui/decl"

	"github.com/yongjohnlee80/autodoc/tui"
)

// runGUI is --gui: the UI in a native window (golib/gui), its widgets drawn in the native style.
// The document's Editor is gui's Editor there (guidecl.Native): proportional text, and a Rendered
// view of Markdown beside the Raw one. The terminal keeps tui's Editor for the same main.qml.
// Gio needs the process's main thread for windows (macOS requires it), so gui.Main takes it and
// ends the process when the UI quits: it never returns.
func runGUI(ctx context.Context, o tui.LaunchOptions, dbg *debugRecording) error {
	configureGUIPreferences()
	gui.Main(func() error {
		opts := []gui.Option{gui.WithTitle("AutoDoc")}
		if o.DebugRecorder != nil {
			// --debug: the window's input path records into the same file the TUI's
			// trace does, but wrapped: the backend closes what it is given on window
			// teardown, and the TUI is still recording then. The owner closes below.
			opts = append(opts, gui.WithRecorder(nonClosingRecorder{rec: o.DebugRecorder}))
		}
		b := gui.NewBackend(opts...)
		o.Backend, o.Fonts, o.FontFamilies, o.DefaultCellFont = b, b, gui.Families, gui.MonospaceFamily()
		o.GUI = true
		o.ProgramOptions = append(o.ProgramOptions, tuidecl.WithStyle(guidecl.Native()))
		// gui.Main ends the process when this callback returns, so this is the last
		// code that runs: the recording's owner closes here, with the TUI's trace
		// stopped, Launch having returned. gui.Main prints a returned error and
		// exits 1.
		if err := finishUI(tui.Launch(ctx, o), dbg); err != nil && !errors.Is(err, context.Canceled) {
			return err
		}
		return nil
	})
	return nil // not reached: gui.Main exits the process
}
