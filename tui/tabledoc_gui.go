//go:build gui

package tui

import (
	guiwidget "github.com/yongjohnlee80/golib/gui/widget"
	tuicore "github.com/yongjohnlee80/golib/tui"
	"github.com/yongjohnlee80/golib/tui/widget"
)

// newTableDocument is a table of core for e: the GUI editor's drawn in pixels, a terminal
// editor's in cells (a GUI build run with --ui).
func newTableDocument(e editorWidget, core *widget.EditorCore, comma rune, opts ...widget.TableOption) *widget.TableDocument {
	if _, ok := e.(*guiwidget.Editor); ok {
		return guiwidget.NewTableDocument(core, comma, opts...)
	}
	return widget.NewTableDocument(core, comma, opts...)
}

// setDocumentView gives e its document view v: the GUI editor's and the terminal editor's take
// one each by their own interface.
func setDocumentView(e editorWidget, v tuicore.Component) {
	switch ed := e.(type) {
	case *guiwidget.Editor:
		if dv, ok := v.(guiwidget.DocumentView); ok {
			ed.SetDocumentView(dv)
		}
	case *widget.Editor:
		if dv, ok := v.(widget.DocumentView); ok {
			ed.SetDocumentView(dv)
		}
	}
}
