//go:build !gui

package tui

import "github.com/yongjohnlee80/golib/tui/widget"

// startRendered chooses the terminal editor's Rendered view, a table file's table (rendered_term.go).
func startRendered(e editorWidget) { startTermRendered(e) }

// editorView is the terminal editor's chosen view as the jump list records it.
func editorView(e editorWidget) string { v, _ := termView(e); return v }

// setEditorView puts the terminal editor in the view a jump list recorded.
func setEditorView(e editorWidget, view string) { setTermView(e, view) }

// paneDiagrams does nothing in a terminal build: it opens no pane.
func (h *Host) paneDiagrams(*widget.HTMLView) {}
