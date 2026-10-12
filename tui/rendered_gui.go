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
