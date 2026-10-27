//go:build gui

package app

import (
	"context"
	"errors"

	"github.com/yongjohnlee80/golib/gui"
	tuidecl "github.com/yongjohnlee80/golib/tui/decl"

	"github.com/yongjohnlee80/autodoc/tui"
)

// runGUI is --gui: the UI in a native window (golib/gui), its widgets drawn in the native style.
// The document's Editor is gui's Editor there (guidecl.Native): proportional text, and a Rendered
// view of Markdown beside the Raw one. The terminal keeps tui's Editor for the same main.qml.
// Gio needs the process's main thread for windows (macOS requires it), so gui.Main takes it and
// ends the process when the UI quits: it never returns.
func runGUI(ctx context.Context, o tui.LaunchOptions, owned []owner) error {
	configureGUIPreferences()
	gui.Main(func() error {
		opts := append([]gui.Option{gui.WithTitle("AutoDoc")}, rendererOptions(tui.GUIRendererFile(o.ConfigPath))...)
		if o.DebugRecorder != nil {
			// --debug: the window's input path records into the same file the TUI's
			// trace does, but wrapped: the backend closes what it is given on window
			// teardown, and the TUI is still recording then. The owner closes below.
			opts = append(opts, gui.WithRecorder(nonClosingRecorder{rec: o.DebugRecorder}))
		}
		b := gui.NewBackend(opts...)
		o.Backend, o.Fonts, o.FontFamilies, o.DefaultCellFont = b, b, gui.Families, gui.MonospaceFamily()
		o.GUI = true
		// the note editor's Rendered view draws the open note's embeds (tui/embeds_gui.go): the
		// host tells it which note is open, and its diagrams draw through one chain, as golib's
		// renderer's do. Every other Editor keeps golib's MarkdownRenderer.
		notes := &tui.NoteEmbeds{}
		o.NoteFile, o.NoteTranslator = notes.SetNote, notes.SetTranslator
		o.ProgramOptions = append(o.ProgramOptions, tuidecl.WithStyle(tui.GUIStyle(notes)))
		// gui.Main ends the process when this callback returns, so this is the last
		// code that runs: the run's owners close here (the recording, with the TUI's
		// trace stopped, and the profile monitor), Launch having returned. gui.Main
		// prints a returned error and exits 1.
		if err := finishRun(tui.Launch(ctx, o), owned...); err != nil && !errors.Is(err, context.Canceled) {
			return err
		}
		return nil
	})
	return nil // not reached: gui.Main exits the process
}

// rendererOptions are the window's renderer, chosen as it opens: Vulkan first where it can have it,
// OpenGL where it can't (gui.WithZeroCopyFrames), unless tui.gui.renderer's mirror at path names
// OpenGL (prefersVulkan).
func rendererOptions(path string) []gui.Option {
	if prefersVulkan(path) {
		return []gui.Option{gui.WithZeroCopyFrames()}
	}
	return nil
}
