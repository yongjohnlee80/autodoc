package tui

import (
	"slices"

	tuidecl "github.com/yongjohnlee80/golib/tui/decl"
	"github.com/yongjohnlee80/golib/tui/widget"
)

// THE DOCK — the menu bar's corner (ADR 1792397302): one icon per widget that is open, or that
// runs on with its drawer closed (the terminal's shells, the agents, a plugin card's plugin), the
// editor first. A click is Go-to's own path (goTo): a closed drawer opens its own way and takes the
// keyboard, and the editor's icon gives the editor the keyboard. While the dock lists more
// than the editor, the bar stays shown however tui.menu.autohide is set.

// editorIcon is the editor's dock icon.
const editorIcon = "✎"

// dockTargets are the dock's widgets: the editor, then each panel open or alive, one per panel (a
// tab is its panel's: the Go-to picker lists tabs, the dock does not), in the registry's order.
func (h *Host) dockTargets() []widgetTarget {
	var out []widgetTarget
	for _, t := range h.widgets() {
		if t.tab != "" {
			continue
		}
		if t.panel == "" || t.open {
			out = append(out, t)
		} else if s := h.panelSpec(t.panel); s != nil && h.panelAlive(*s) {
			out = append(out, t)
		}
	}
	return out
}

// panelAlive reports that s's panel lives on with its drawer closed: its own Alive says so, or its
// view is a TerminalTabs with a tab running (the terminal's shells, the agents' instances).
func (h *Host) panelAlive(s PanelSpec) bool {
	if s.Alive != nil && s.Alive() {
		return true
	}
	if tabs, ok := h.tabsOf(s.View); ok {
		return slices.ContainsFunc(tabs.Tabs(), func(t widget.TerminalTab) bool { return t.Running })
	}
	return false
}

// dockRowOf is a widget's dock row: key its :go name, its icon, its title for the tooltip.
func (h *Host) dockRowOf(_ int, t widgetTarget) rowOf {
	icon, title := editorIcon, t.title
	if s := h.panelSpec(t.panel); s != nil {
		icon = s.Icon
	}
	if title == "" {
		title = t.name
	}
	return rowOf{"key": t.name, "icon": icon, "title": title}
}

// newDockRows is App.dock's model.
func (h *Host) newDockRows() *rows[widgetTarget] {
	return newRows(tuidecl.NewListModel("key", "icon", "title"), h.dockRowOf)
}

// refreshDock lists the dock again and holds the bar shown while it lists more than the editor.
func (h *Host) refreshDock() {
	if h.dockRows == nil || h.p == nil {
		return
	}
	h.dockRows.Reset(h.dockTargets())
	h.set("App.menuAutoHide", h.menuAutoHide(h.prefs))
}

// menuAutoHide is App.menuAutoHide: the preference, unless the dock lists more than the editor.
func (h *Host) menuAutoHide(p prefs) bool {
	return p.menuHidden && (h.p == nil || len(h.dockTargets()) <= 1)
}

// goDock is a dock icon's click: the widget by its :go name, then goTo, Go-to's own path.
func (h *Host) goDock(key string) {
	t, err := h.widgetNamed(key)
	if err != nil {
		h.notify(err.Error())
		return
	}
	h.goTo(t)
}
