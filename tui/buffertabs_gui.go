//go:build gui

package tui

import (
	guiwidget "github.com/yongjohnlee80/golib/gui/widget"
	"github.com/yongjohnlee80/golib/tui/widget"
)

// bufferStrip is the GUI editor's title-bar strip, where the buffers are its tabs (buffertabs.go).
func (h *Host) bufferStrip() *widget.TabStrip {
	if g, ok := h.editor.(*guiwidget.Editor); ok {
		return g.Tabs()
	}
	return nil
}
