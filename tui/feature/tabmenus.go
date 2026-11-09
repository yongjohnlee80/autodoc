package feature

import (
	"fmt"
	"strconv"

	tuicore "github.com/yongjohnlee80/golib/tui"
	widgetcore "github.com/yongjohnlee80/golib/tui/widget/core"
	"github.com/yongjohnlee80/golib/tui/widget/terminal"
)

// THE TAB MENUS — one shape for the strips of the agent and terminal panels (golib's
// TerminalTabs): + offers what to add, and a tab's menu (≡, a right click anywhere on the strip or
// on an empty panel, Enter on the strip) starts with Go to ›, every tab of the panel, and ends with
// the panel's own rows: Theme and font… (the terminals' look, tui/terminallook.go), in a window Text
// size › (the shells' text, tui/fonts.go), Maximize (Restore) and Hide (the panel closed, every tab
// still running). Each panel says what its other rows are and do; a row runs on
// the loop with the tab its menu was opened for ("" with none).

// tabMenuRow is a row of a strip's menu: run, with the tab the menu was opened for, when chosen.
// An & in label marks its key ("Hi&de"); never h, j, k or l, which move in the menu.
func TabMenuRow(id, label string, run func(terminal.TerminalTabID)) widgetcore.MenuItemModel {
	text, key, at := tuicore.ParseMnemonic(label)
	row := widgetcore.NewCommand(widgetcore.ItemID(id), text, terminal.TerminalTabsMenuAction{ID: tuicore.ActionID(id), Run: run})
	row.Hotkey, row.HotkeyIdx = key, at
	return WithLabelMessage(row, label)
}

// tabRowOf is tabMenuRow as a rowMaker: a row whatever tab its menu was opened for (Layout ›).
func TabRowOf(id, label string, run func()) widgetcore.MenuItemModel {
	return TabMenuRow(id, label, func(terminal.TerminalTabID) { run() })
}

// panelRows end panel's tab menu: Resume, a SIGCONT to tab id's program on tabs (disabled with no
// tab); Theme and font…, which opens the terminals' look (nil where the host has none); Text size ›
// in a window, the shells' share of its text (textSizeMenu); Maximize, or Restore while it is
// maximized, its Drawer's own toggleMaximize (none where the host has none); and Hide, the panel's
// own toggle (hide), its tabs running on.
func PanelRows(c any, panel string, tabs TerminalPanel, id terminal.TerminalTabID, hide func()) []widgetcore.MenuItemModel {
	resume := TabMenuRow("tabs.resume", "Res&ume", func(id terminal.TerminalTabID) {
		if err := tabs.Resume(id); err != nil {
			keepFailure(c, fmt.Errorf("Resume a tab of the %s panel: %w", panel, err))
		}
	})
	resume.Enabled = id != ""
	rows := []widgetcore.MenuItemModel{widgetcore.NewSeparator("tabs.panel"), resume}
	if look := LookOf(c); look != nil {
		rows = append(rows, TabMenuRow("tabs.look", "&Theme and font…", func(terminal.TerminalTabID) {
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
		rows = append(rows, TabMenuRow("tabs.maximize", label, func(terminal.TerminalTabID) {
			if err := m.ToggleMaximize(panel); err != nil {
				keepFailure(c, fmt.Errorf("%s the %s panel: %w", op, panel, err))
			}
		}))
	}
	return append(rows, TabMenuRow("tabs.hide", "Hi&de", func(terminal.TerminalTabID) { hide() }))
}

// textScaler is a host whose window draws the agent and terminal panels' text at a share of its own
// (tui/fonts.go): the share now, in percent, and whether it is drawn; and a step of it, -1 or +1.
type textScaler interface {
	PanelTextScale() (int, bool)
	StepPanelTextScale(dir int) error
}

// textSizeMenu is Text size ›: the share now, then Smaller and Larger, a step of 10% each, each
// disabled at its end. The share is the agent and terminal panels' both, whichever menu moves it.
func textSizeMenu(c any, panel string, pct int, step func(int) error) widgetcore.MenuItemModel {
	run := func(dir int, op string) func(terminal.TerminalTabID) {
		return func(terminal.TerminalTabID) {
			if err := step(dir); err != nil {
				keepFailure(c, fmt.Errorf("Text size › %s for the %s panel: %w", op, panel, err))
			}
		}
	}
	now := TabMenuRow("tabs.text.now", strconv.Itoa(pct)+"%", func(terminal.TerminalTabID) {})
	now.Enabled = false
	smaller := TabMenuRow("tabs.text.smaller", "&Smaller", run(-1, "Smaller"))
	smaller.Enabled = pct > AgentScales[0]
	larger := TabMenuRow("tabs.text.larger", "La&rger", run(+1, "Larger"))
	larger.Enabled = pct < AgentScales[len(AgentScales)-1]
	sub := widgetcore.NewSubmenu("tabs.text", "Text size", []widgetcore.MenuItemModel{now, smaller, larger})
	sub.Label, sub.Hotkey, sub.HotkeyIdx = tuicore.ParseMnemonic("Te&xt size")
	return WithLabelMessage(sub, "Te&xt size")
}

// keepFailure gives err to the host to keep, where c keeps errors.
func keepFailure(c any, err error) {
	if k, ok := c.(interface{ Keep(error) }); ok {
		k.Keep(err)
	}
}

// lookOf is the host's Theme and font… (Host.openTerminalLook), nil where c has none.
func LookOf(c any) func() error {
	if l, ok := c.(interface{ OpenTerminalLook() error }); ok {
		return l.OpenTerminalLook
	}
	return nil
}

// goToRows is Go to ›: every tab of panel, as label names it, going to it by pick. A tab closing
// is shown, not offered.
func GoToRows(panel TerminalPanel, label func(terminal.TerminalTab) string, pick func(terminal.TerminalTabID)) widgetcore.MenuItemModel {
	var rows []widgetcore.MenuItemModel
	for _, tab := range panel.Tabs() {
		target := tab.ID
		row := TabMenuRow("tabs.goto."+string(target), label(tab), func(terminal.TerminalTabID) { pick(target) })
		row.Enabled = !tab.Closing
		rows = append(rows, row)
	}
	sub := WithLabelMessage(widgetcore.NewSubmenu("tabs.goto", "Go to", rows), "Go to")
	sub.Enabled = len(rows) > 0
	return sub
}

// menuMessage is the catalog's message for a menu row's English label, when it has one: the host's
// English catalog (tui/featurehost.go), set as the package starts.
var MenuMessage func(label string) (tuicore.Message, bool)

// withLabelMessage gives row its label's catalog message, so the menu shows it in the UI's
// language; a label the catalog lacks stays as it is.
func WithLabelMessage(row widgetcore.MenuItemModel, label string) widgetcore.MenuItemModel {
	if MenuMessage != nil {
		if id, ok := MenuMessage(label); ok {
			row.LabelMsg = id
		}
	}
	return row
}

// agentScales are the agent and terminal panels' text sizes, in percent of the window's text: the
// Fonts dialog offers them, and each panel's Text size menu steps through them. defaultAgentScale
// is slightly smaller than the editor's.
var AgentScales = Percents(50, 150, 10)

// percents are lo to hi in steps of step.
func Percents(lo, hi, step int) []int {
	var out []int
	for p := lo; p <= hi; p += step {
		out = append(out, p)
	}
	return out
}
