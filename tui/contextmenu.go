package tui

// openContextMenu is SPC .: the page's right-click menu at the cursor, as Shift+F10 opens it from
// the page (golib's Editor), for a keyboard that has no Shift+F10 at hand in Vim's Normal mode.
func (h *Host) openContextMenu() {
	c, ok := h.p.Find("editor")
	e, isMenu := c.(interface{ OpenContextMenu() bool })
	if !ok || !isMenu || !e.OpenContextMenu() {
		h.notify(h.tr("autodoc.notice.contextmenu.the_right_click_menu_opens_where_the_cursor_is_o"))
	}
}
