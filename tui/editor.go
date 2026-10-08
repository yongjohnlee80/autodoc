package tui

import (
	tuicore "github.com/yongjohnlee80/golib/tui"
	"github.com/yongjohnlee80/golib/tui/widget"
)

// editorWidget is the page's editor, whichever widget the program built for main.qml's Editor:
// golib's terminal Editor, or the window's under the native style. The host reaches the text,
// the cursor and the modes through its core; only replacing the text is the widget's, since a
// widget also resets its view for a new document.
type editorWidget interface {
	tuicore.Component
	Core() *widget.EditorCore
	SetValue(string)
}

// gutterWidth is the columns the editor's line numbers take, 0 for an editor that draws none in
// cells.
func (h *Host) gutterWidth() int {
	if g, ok := h.editor.(interface{ GutterWidth() int }); ok {
		return g.GutterWidth()
	}
	return 0
}

// installEditorMenu gives the editor its right-click rows (editorMenu): the stock rows, then the
// related documents and back. Each editor widget takes rows its own way.
func (h *Host) installEditorMenu() {
	switch e := h.editor.(type) {
	case *widget.Editor:
		widget.WithContextMenu(func(e *widget.Editor) []widget.MenuItemModel { return h.editorMenu(e.Core()) })(e)
	case interface {
		SetContextMenuRows(func(*widget.EditorCore) []widget.MenuItemModel)
	}:
		e.SetContextMenuRows(h.editorMenu)
	}
}
