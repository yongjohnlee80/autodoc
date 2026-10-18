package tui

import "github.com/yongjohnlee80/golib/tui/widget"

// The terminal editor's views, in either build (a GUI build run with --ui too): Raw is the text,
// Rendered a document view, a CSV or TSV file's table. It starts Rendered, as the GUI's does, so a
// table file opens as its table; a page with no document view (a note, YAML, code) is drawn as its
// text all the same, the choice kept, and the user's switch holds from then on.

// startTermRendered chooses Rendered for a terminal editor; false for any other editor.
func startTermRendered(e editorWidget) bool {
	t, ok := e.(*widget.Editor)
	if ok {
		t.SetView(widget.ViewRendered)
	}
	return ok
}

// termView is a terminal editor's chosen view as the jump list records it; false for another editor.
func termView(e editorWidget) (string, bool) {
	t, ok := e.(*widget.Editor)
	if !ok {
		return "", false
	}
	if t.View() == widget.ViewRendered {
		return "rendered", true
	}
	return "", true
}

// setTermView puts a terminal editor in the view a jump list recorded; false for another editor.
func setTermView(e editorWidget, view string) bool {
	t, ok := e.(*widget.Editor)
	if !ok {
		return false
	}
	if view == "rendered" {
		t.SetView(widget.ViewRendered)
	} else {
		t.SetView(widget.ViewRaw)
	}
	return true
}
