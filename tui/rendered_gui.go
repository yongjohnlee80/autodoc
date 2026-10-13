//go:build gui

package tui

import guiwidget "github.com/yongjohnlee80/golib/gui/widget"

// startRendered puts the GUI's editor in its Rendered view as the window opens: a note reads as it
// will look, and Ctrl+T shows its source. A page the Rendered view does not read (a Go file, say)
// shows Raw all the same (readPage withholds the view), and the user's switch holds from then on.
func startRendered(e editorWidget) {
	if g, ok := e.(*guiwidget.Editor); ok {
		g.SetMode(guiwidget.Rendered)
	}
}

// editorView is the GUI editor's view as the jump list records it: "rendered", or "" for its source.
func editorView(e editorWidget) string {
	if g, ok := e.(*guiwidget.Editor); ok && g.Mode() == guiwidget.Rendered {
		return "rendered"
	}
	return ""
}

// setEditorView puts the GUI editor in the view a jump list recorded.
func setEditorView(e editorWidget, view string) {
	g, ok := e.(*guiwidget.Editor)
	if !ok {
		return
	}
	if view == "rendered" {
		g.SetMode(guiwidget.Rendered)
	} else {
		g.SetMode(guiwidget.Raw)
	}
}
