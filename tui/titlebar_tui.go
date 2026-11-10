//go:build !gui

package tui

import tuidecl "github.com/yongjohnlee80/golib/tui/decl"

// titleBarTypes: the terminal build has no window, so no title bar of its own (titlebar_gui.go).
func (h *Host) titleBarTypes() []tuidecl.Type { return nil }
