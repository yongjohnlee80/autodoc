package feature

import widgetcore "github.com/yongjohnlee80/golib/tui/widget/core"

// RowMaker makes a row of the menu Layout › goes in, run when chosen. Each menu runs its own action
// type (a buffer strip's, a panel's tabs', a TabView's, a table's), so Layout › is built of rows its
// menu makes.
type RowMaker func(id, label string, run func()) widgetcore.MenuItemModel

// LayoutRows is Layout › for the window id, as its host builds it (c's LayoutRows: the host knows
// which windows tile, and where); none from a host that builds none.
func LayoutRows(c any, id string, row RowMaker) []widgetcore.MenuItemModel {
	if h, ok := c.(interface {
		LayoutRows(id string, row RowMaker) []widgetcore.MenuItemModel
	}); ok {
		return h.LayoutRows(id, row)
	}
	return nil
}
