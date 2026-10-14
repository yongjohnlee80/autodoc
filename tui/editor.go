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

// renderedSwitch is an editor whose Rendered view the host can withhold: golib's gui Editor. A plain
// bool, so this package names no gui type.
type renderedSwitch interface{ SetRenderedEnabled(on bool) }

// editorMenu is the editor's right-click menu: the stock rows, then the related documents and back.
// Its rows act on the core, so every editor widget runs them (installEditorMenu).
func (h *Host) editorMenu(c *widget.EditorCore) []widget.MenuItemModel {
	items := widget.CoreContextItems(c)
	l, onLink := linkAt([]byte(c.Value()), h.cursorBytes())
	link := menuCommand("autodoc.link", "&Go to link", widget.CoreMenuAction{ID: "autodoc.link",
		Run: func(*widget.EditorCore) { h.goToLink(l) }})
	link.Enabled = onLink
	back := menuCommand("autodoc.back", "&Back", widget.CoreMenuAction{ID: "autodoc.back",
		Run: func(*widget.EditorCore) { h.goBack() }})
	_, back.Enabled = h.backToDocument()
	// the diagram at the cursor, else the file's first, as File › Preview Mermaid diagram shows it
	diagram := menuCommand("autodoc.diagram", "View &diagram", widget.CoreMenuAction{ID: "autodoc.diagram",
		Run: func(*widget.EditorCore) { h.previewDiagram() }})
	_, diagram.Enabled = mermaidBlock([]byte(c.Value()), h.cursorBytes(), h.diagramReader())
	items = append(items, widget.NewSeparator("autodoc.sep.diagram"), diagram,
		widget.NewSeparator("autodoc.sep.relations"), link, back)
	if h.fonts != nil { // the window's zoom; a terminal's is its own
		items = append(items, widget.NewSeparator("autodoc.sep.zoom"), h.zoomMenu())
	}
	return append(items, h.luaFeature().editorRows()...) // a script's rows (luakeys.go)
}
