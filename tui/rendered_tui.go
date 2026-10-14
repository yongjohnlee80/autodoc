//go:build !gui

package tui

import "github.com/yongjohnlee80/golib/tui/widget"

// startRendered does nothing in a terminal build: its editor has no Rendered view.
func startRendered(editorWidget) {}

// editorView is "" in a terminal build: its editor has one view.
func editorView(editorWidget) string { return "" }

// setEditorView does nothing in a terminal build.
func setEditorView(editorWidget, string) {}

// paneDiagrams does nothing in a terminal build: it opens no pane.
func (h *Host) paneDiagrams(*widget.HTMLView) {}
