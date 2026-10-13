//go:build !gui

package tui

// startRendered does nothing in a terminal build: its editor has no Rendered view.
func startRendered(editorWidget) {}

// editorView is "" in a terminal build: its editor has one view.
func editorView(editorWidget) string { return "" }

// setEditorView does nothing in a terminal build.
func setEditorView(editorWidget, string) {}
