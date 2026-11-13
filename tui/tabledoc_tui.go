//go:build !gui

package tui

import (
	tuicore "github.com/yongjohnlee80/golib/tui"
	"github.com/yongjohnlee80/golib/tui/widget/editor"
	"github.com/yongjohnlee80/golib/tui/widget/table"
)

// newTableDocument is a table of core, drawn in cells: a terminal build's editor.
func newTableDocument(_ editorWidget, core *editor.EditorCore, comma rune, opts ...table.TableOption) *table.TableDocument {
	return table.NewTableDocument(core, comma, opts...)
}

// setDocumentView gives the terminal editor its document view v.
func setDocumentView(e editorWidget, v tuicore.Component) {
	if ed, ok := e.(*editor.Editor); ok {
		if dv, ok := v.(editor.DocumentView); ok {
			ed.SetDocumentView(dv)
		}
	}
}
