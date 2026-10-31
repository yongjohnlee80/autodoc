//go:build !gui

package tui

// panelText is nothing in the terminal build: the host terminal owns the font
// (docs/decisions/editor.md#the-terminal-text-size).
type panelText struct{}

func (*panelText) apply(*Host, int) {}
