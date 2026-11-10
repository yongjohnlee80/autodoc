//go:build gui

package tui

import (
	guidecl "github.com/yongjohnlee80/golib/gui/decl"
	tuidecl "github.com/yongjohnlee80/golib/tui/decl"
)

// titleBarTypes are golib/gui's own QML types, TitleBar among them, when the layout has the
// window's own title bar written in (windowLayout); none otherwise, so a window with the system's
// title bar, and a terminal run of this build, declare nothing new.
func (h *Host) titleBarTypes() []tuidecl.Type {
	if !h.gui || !h.titleBar {
		return nil
	}
	return guidecl.Types()
}
