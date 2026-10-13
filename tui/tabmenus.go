package tui

import (
	tuicore "github.com/yongjohnlee80/golib/tui"
	"github.com/yongjohnlee80/golib/tui/widget"
)

// THE TAB MENUS — one shape for the strips of the agent and terminal panels (golib's
// TerminalTabs): + offers what to add, and a tab's menu (≡, a right click anywhere on the strip or
// on an empty panel, Enter on the strip) starts with Go to ›, every tab of the panel, and ends with
// the panel's own rows: Theme and font… (the terminals' look, terminallook.go) and Hide (the panel
// closed, every tab still running). Each panel says what its other rows are and do; a row runs on
// the loop with the tab its menu was opened for ("" with none).

// tabMenuRow is a row of a strip's menu: run, with the tab the menu was opened for, when chosen.
// An & in label marks its key ("Hi&de"); never h, j, k or l, which move in the menu.
func tabMenuRow(id, label string, run func(widget.TerminalTabID)) widget.MenuItemModel {
	text, key, at := tuicore.ParseMnemonic(label)
	row := widget.NewCommand(widget.ItemID(id), text, widget.TerminalTabsMenuAction{ID: tuicore.ActionID(id), Run: run})
	row.Hotkey, row.HotkeyIdx = key, at
	return row
}

// panelRows end a panel's tab menu: Theme and font…, which opens the terminals' look (look; nil
// where the host has none), and Hide, the panel's own toggle (hide), its tabs running on.
func panelRows(look, hide func()) []widget.MenuItemModel {
	rows := []widget.MenuItemModel{widget.NewSeparator("tabs.panel")}
	if look != nil {
		rows = append(rows, tabMenuRow("tabs.look", "&Theme and font…", func(widget.TerminalTabID) { look() }))
	}
	return append(rows, tabMenuRow("tabs.hide", "Hi&de", func(widget.TerminalTabID) { hide() }))
}

// lookOf is the host's Theme and font… (Host.openTerminalLook), nil where c has none.
func lookOf(c any) func() {
	if l, ok := c.(interface{ OpenTerminalLook() }); ok {
		return l.OpenTerminalLook
	}
	return nil
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
