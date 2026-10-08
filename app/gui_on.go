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
func runGUI(ctx context.Context, o tui.LaunchOptions) error {
	gui.Main(func() error {
		o.Backend = gui.NewBackend(gui.WithTitle("AutoDoc"))
		o.GUI = true
		o.ProgramOptions = append(o.ProgramOptions, tuidecl.WithStyle(guidecl.Native()))
		if err := tui.Launch(ctx, o); err != nil && !errors.Is(err, context.Canceled) {
			return err
		}
		return nil
	})
	return nil // not reached: gui.Main exits
}
