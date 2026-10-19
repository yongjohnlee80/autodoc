//go:build !gui

package tui

// agentText is nothing in the terminal build: the host terminal owns the font (ADR 1792350322).
type agentText struct{}

func (*agentText) apply(*Host, int) {}
