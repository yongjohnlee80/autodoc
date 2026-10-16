//go:build !gui

package tui

import "github.com/yongjohnlee80/golib/tui/widget"

// bufferStrip is nil: the TUI's editor has no title bar, so no tabs (buffertabs.go).
func (h *Host) bufferStrip() *widget.TabStrip { return nil }
