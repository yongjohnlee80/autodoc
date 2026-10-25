package tui

import (
	"fmt"
	"strconv"

	tuicore "github.com/yongjohnlee80/golib/tui"
	"github.com/yongjohnlee80/golib/tui/widget"
)

// THE TAB MENUS — one shape for the strips of the agent and terminal panels (golib's
// TerminalTabs): + offers what to add, and a tab's menu (≡, a right click anywhere on the strip or
// on an empty panel, Enter on the strip) starts with Go to ›, every tab of the panel, and ends with
// the panel's own rows: Theme and font… (the terminals' look, terminallook.go), in a window Text
// size › (the shells' text, fonts.go), Maximize (Restore) and Hide (the panel closed, every tab
// still running). Each panel says what its other rows are and do; a row runs on
// the loop with the tab its menu was opened for ("" with none).

// tabMenuRow is a row of a strip's menu: run, with the tab the menu was opened for, when chosen.
// An & in label marks its key ("Hi&de"); never h, j, k or l, which move in the menu.
func tabMenuRow(id, label string, run func(widget.TerminalTabID)) widget.MenuItemModel {
	text, key, at := tuicore.ParseMnemonic(label)
	row := widget.NewCommand(widget.ItemID(id), text, widget.TerminalTabsMenuAction{ID: tuicore.ActionID(id), Run: run})
	row.Hotkey, row.HotkeyIdx = key, at
	return withLabelMessage(row, label)
}

// panelRows end panel's tab menu: Resume, a SIGCONT to tab id's program on tabs (disabled with no
// tab); Theme and font…, which opens the terminals' look (nil where the host has none); Text size ›
// in a window, the shells' share of its text (textSizeMenu); Maximize, or Restore while it is
// maximized, its Drawer's own toggleMaximize (none where the host has none); and Hide, the panel's
// own toggle (hide), its tabs running on.
func panelRows(c any, panel string, tabs TerminalPanel, id widget.TerminalTabID, hide func()) []widget.MenuItemModel {
	resume := tabMenuRow("tabs.resume", "Res&ume", func(id widget.TerminalTabID) {
		if err := tabs.Resume(id); err != nil {
			keepFailure(c, fmt.Errorf("Resume a tab of the %s panel: %w", panel, err))
		}
	})
	resume.Enabled = id != ""
	rows := []widget.MenuItemModel{widget.NewSeparator("tabs.panel"), resume}
	if look := lookOf(c); look != nil {
		rows = append(rows, tabMenuRow("tabs.look", "&Theme and font…", func(widget.TerminalTabID) {
			if err := look(); err != nil {
				keepFailure(c, fmt.Errorf("Theme and font… for the %s panel: %w", panel, err))
			}
		}))
	}
	if ts, ok := c.(textScaler); ok {
		if pct, drawn := ts.PanelTextScale(); drawn {
			rows = append(rows, textSizeMenu(c, panel, pct, ts.StepPanelTextScale))
		}
	}
	if m, ok := c.(interface {
		PanelMaximized(string) bool
		ToggleMaximize(string) error
	}); ok {
		label, op := "&Maximize", "Maximize"
		if m.PanelMaximized(panel) {
			label, op = "Re&store", "Restore"
		}
		rows = append(rows, tabMenuRow("tabs.maximize", label, func(widget.TerminalTabID) {
			if err := m.ToggleMaximize(panel); err != nil {
				keepFailure(c, fmt.Errorf("%s the %s panel: %w", op, panel, err))
			}
		}))
	}
	return append(rows, tabMenuRow("tabs.hide", "Hi&de", func(widget.TerminalTabID) { hide() }))
}

// textScaler is a host whose window draws the agent and terminal panels' text at a share of its own
// (fonts.go): the share now, in percent, and whether it is drawn; and a step of it, -1 or +1.
type textScaler interface {
	PanelTextScale() (int, bool)
	StepPanelTextScale(dir int) error
}

// textSizeMenu is Text size ›: the share now, then Smaller and Larger, a step of 10% each, each
// disabled at its end. The share is the agent and terminal panels' both, whichever menu moves it.
func textSizeMenu(c any, panel string, pct int, step func(int) error) widget.MenuItemModel {
	run := func(dir int, op string) func(widget.TerminalTabID) {
		return func(widget.TerminalTabID) {
			if err := step(dir); err != nil {
				keepFailure(c, fmt.Errorf("Text size › %s for the %s panel: %w", op, panel, err))
			}
		}
	}
	now := tabMenuRow("tabs.text.now", strconv.Itoa(pct)+"%", func(widget.TerminalTabID) {})
	now.Enabled = false
	smaller := tabMenuRow("tabs.text.smaller", "&Smaller", run(-1, "Smaller"))
	smaller.Enabled = pct > agentScales[0]
	larger := tabMenuRow("tabs.text.larger", "La&rger", run(+1, "Larger"))
	larger.Enabled = pct < agentScales[len(agentScales)-1]
	sub := widget.NewSubmenu("tabs.text", "Text size", []widget.MenuItemModel{now, smaller, larger})
	sub.Label, sub.Hotkey, sub.HotkeyIdx = tuicore.ParseMnemonic("Te&xt size")
	return withLabelMessage(sub, "Te&xt size")
}

// keepFailure gives err to the host to keep, where c keeps errors.
func keepFailure(c any, err error) {
	if k, ok := c.(interface{ Keep(error) }); ok {
		k.Keep(err)
	}
}

// lookOf is the host's Theme and font… (Host.openTerminalLook), nil where c has none.
func lookOf(c any) func() error {
	if l, ok := c.(interface{ OpenTerminalLook() error }); ok {
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
	sub := withLabelMessage(widget.NewSubmenu("tabs.goto", "Go to", rows), "Go to")
	sub.Enabled = len(rows) > 0
	return sub
}
