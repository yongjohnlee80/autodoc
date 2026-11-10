package tui

import "strings"

// THE PAGE'S FRAME — its title, and the cursor's moves the features hear: the host's, beside the
// outline that puts the breadcrumb there (outline.go).

// showTitle titles the page's frame: the file's name, then the breadcrumb a feature put there.
func (h *Host) showTitle() {
	title := h.file.title(h.tr)
	if len(h.crumbs) > 0 {
		title += crumbSep + strings.Join(h.crumbs, crumbSep)
	}
	h.set("App.fileTitle", title)
}

// cursorMoved is the editor's cursor landing elsewhere: the watchers hear it (the breadcrumb follows).
func (h *Host) cursorMoved() {
	h.followCursorInPane()
	line, _ := h.core.Line()
	h.reg.Moved(line, h.cursorBytes())
}
