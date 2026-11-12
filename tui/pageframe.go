package tui

import (
	"strings"

	"github.com/yongjohnlee80/autodoc/tui/explorer"
)

// THE PAGE'S FRAME — its title, and the cursor's moves the features hear: the host's, beside the
// outline that puts the breadcrumb there (outline.go).

// showTitle titles the page's frame: the file's name, then the breadcrumb a feature put there. While
// the terminal's buffer tabs lead the title row (showTerminalTabs), the name leaves it (the shown
// tab names the file, and the status line), and its badges and the breadcrumb follow the tabs.
func (h *Host) showTitle() {
	title := h.file.title(h.tr)
	if h.terminalTabsShown() {
		title = h.file.badges(h.tr)
	}
	if len(h.crumbs) > 0 {
		if title == "" {
			title = strings.Join(h.crumbs, explorer.CrumbSep)
		} else {
			title += explorer.CrumbSep + strings.Join(h.crumbs, explorer.CrumbSep)
		}
	}
	h.set("App.fileTitle", title)
}

// cursorMoved is the editor's cursor landing elsewhere: the watchers hear it (the breadcrumb follows).
func (h *Host) cursorMoved() {
	h.followCursorInPane()
	line, _ := h.core.Line()
	h.reg.Moved(line, h.cursorBytes())
}
