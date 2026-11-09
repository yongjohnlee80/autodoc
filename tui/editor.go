package tui

import (
	tuicore "github.com/yongjohnlee80/golib/tui"
	widgetcore "github.com/yongjohnlee80/golib/tui/widget/core"
	"github.com/yongjohnlee80/golib/tui/widget/editor"
)

// editorWidget is the page's editor, whichever widget the program built for main.qml's Editor:
// golib's terminal Editor, or the window's under the native style. The host reaches the text,
// the cursor and the modes through its core; only replacing the text is the widget's, since a
// widget also resets its view for a new document.
type editorWidget interface {
	tuicore.Component
	Core() *editor.EditorCore
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
	case *editor.Editor:
		editor.WithContextMenu(func(e *editor.Editor) []widgetcore.MenuItemModel { return h.editorMenu(e.Core()) })(e)
	case interface {
		SetContextMenuRows(func(*editor.EditorCore) []widgetcore.MenuItemModel)
	}:
		e.SetContextMenuRows(h.editorMenu)
	}
}

// renderedSwitch is an editor whose Rendered view the host can withhold: golib's gui Editor. A plain
// bool, so this package names no gui type.
type renderedSwitch interface{ SetRenderedEnabled(on bool) }

// relayouter is an editor whose Rendered view the host can have laid out again, its text unchanged:
// golib's gui Editor.
type relayouter interface{ Relayout() }

// editorMenu is the editor's right-click menu: the stock rows, then the related documents and back.
// Its rows act on the core, so every editor widget runs them (installEditorMenu).
func (h *Host) editorMenu(c *editor.EditorCore) []widgetcore.MenuItemModel {
	items := editor.CoreContextItems(c)
	l, onLink := linkAt([]byte(c.Value()), h.cursorBytes())
	link := menuCommand("autodoc.link", "&Go to link", editor.CoreMenuAction{ID: "autodoc.link",
		Run: func(*editor.EditorCore) { h.goToLink(l) }})
	link.Enabled = onLink
	back := menuCommand("autodoc.back", "&Back", editor.CoreMenuAction{ID: "autodoc.back",
		Run: func(*editor.EditorCore) { h.goBack() }})
	_, back.Enabled = h.backToDocument()
	// the diagram at the cursor, else the file's first, as File › Preview Mermaid diagram shows it
	diagram := menuCommand("autodoc.diagram", "View &diagram", editor.CoreMenuAction{ID: "autodoc.diagram",
		Run: func(*editor.EditorCore) { h.previewDiagram() }})
	_, diagram.Enabled = mermaidBlock([]byte(c.Value()), h.cursorBytes(), h.diagramReader())
	items = append(items, widgetcore.NewSeparator("autodoc.sep.diagram"), diagram,
		widgetcore.NewSeparator("autodoc.sep.relations"), link, back)
	if h.fonts != nil { // the window's zoom; a terminal's is its own
		items = append(items, widgetcore.NewSeparator("autodoc.sep.zoom"), h.zoomMenu())
	}
	s := h.luaFeature()
	return append(items, s.rows.editorItems(s, h.menuCtxOf(c))...) // scripts' and plugins' rows (menurows.go)
}

// menuCtxOf is the open file's context for the right-click menu opening on c.
func (h *Host) menuCtxOf(c *editor.EditorCore) menuCtx {
	var ctx menuCtx
	if h.file != nil {
		ctx.path, ctx.workspace = h.file.path, h.file.ws
	}
	if ctx.path != "" {
		ctx.kind = h.resolver().Of(ctx.path).String()
	}
	_, ctx.selection = c.Selection()
	return ctx
}
