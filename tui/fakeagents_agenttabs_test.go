package tui

import widgetcore "github.com/yongjohnlee80/golib/tui/widget/core"

// Copies of the agents' test fakes agenttabs_test.go declares, for the host's cells.

// menuRow is the row id of rows (or of a submenu's), and whether it is there.
func menuRow(rows []widgetcore.MenuItemModel, id string) (widgetcore.MenuItemModel, bool) {
	for _, row := range rows {
		if string(row.ID) == id {
			return row, true
		}
		if found, ok := menuRow(row.Children, id); ok {
			return found, true
		}
	}
	return widgetcore.MenuItemModel{}, false
}

func rowIDs(rows []widgetcore.MenuItemModel) []string {
	var ids []string
	for _, row := range rows {
		ids = append(ids, string(row.ID))
		ids = append(ids, rowIDs(row.Children)...)
	}
	return ids
}
