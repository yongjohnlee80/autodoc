package tui

import (
	tuicore "github.com/yongjohnlee80/golib/tui"
	"github.com/yongjohnlee80/golib/tui/widget"
)

// THE TAB MENUS — one shape for the strips of the agent and terminal panels (golib's
// TerminalTabs): + offers what to add, and a tab's menu (≡, a right click on the tab, Enter on the
// strip) starts with Go to ›, every tab of the panel. Each panel says what its rows are and do;
// a row runs on the loop with the tab its menu was opened for.

// tabMenuRow is a row of a strip's menu: run, with the tab the menu was opened for, when chosen.
func tabMenuRow(id, label string, run func(widget.TerminalTabID)) widget.MenuItemModel {
	return widget.NewCommand(widget.ItemID(id), label, widget.TerminalTabsMenuAction{ID: tuicore.ActionID(id), Run: run})
}

// goToRows is Go to ›: every tab of panel, as label names it, going to it by pick. A tab closing
// is shown, not offered.
func goToRows(panel TerminalPanel, label func(widget.TerminalTab) string, pick func(widget.TerminalTabID)) widget.MenuItemModel {
	var rows []widget.MenuItemModel
	for _, tab := range panel.Tabs() {
		target := tab.ID
		row := tabMenuRow("tabs.goto."+string(target), label(tab), func(widget.TerminalTabID) { pick(target) })
		row.Enabled = !tab.Closing
		rows = append(rows, row)
	}
	sub := widget.NewSubmenu("tabs.goto", "Go to", rows)
	sub.Enabled = len(rows) > 0
	return sub
}
