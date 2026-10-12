//go:build !gui

package tui

// startRendered does nothing in a terminal build: its editor has no Rendered view.
func startRendered(editorWidget) {}
