//go:build !gui

package tui

// panelText is nothing in the terminal build: the host terminal owns the font (ADR 1792350322).
type panelText struct{}

func (*panelText) apply(*Host, int) {}
