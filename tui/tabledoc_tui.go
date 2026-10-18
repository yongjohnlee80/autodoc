//go:build !gui

package tui

import (
	tuicore "github.com/yongjohnlee80/golib/tui"
	"github.com/yongjohnlee80/golib/tui/widget"
)

// newTableDocument is a table of core, drawn in cells: a terminal build's editor.
func newTableDocument(_ editorWidget, core *widget.EditorCore, comma rune, opts ...widget.TableOption) *widget.TableDocument {
	return widget.NewTableDocument(core, comma, opts...)
}

// setDocumentView gives the terminal editor its document view v.
func setDocumentView(e editorWidget, v tuicore.Component) {
	if ed, ok := e.(*widget.Editor); ok {
		if dv, ok := v.(widget.DocumentView); ok {
			ed.SetDocumentView(dv)
		}
	}
}
