package tui

import (
	"slices"
	"time"

	tuidecl "github.com/yongjohnlee80/golib/tui/decl"
	"github.com/yongjohnlee80/golib/tui/widget"
)

// THE DOCK — the menu bar's corner (docs/decisions/editor.md#the-dock-of-open-widgets): one icon
// per widget that is open, or that runs on with its drawer closed (the terminal's shells, the
// agents, a plugin card's plugin), the editor first. A click shows or hides
// (docs/decisions/editor.md#dock-clicks-hide-and-show): a shown panel's icon hides it, the keyboard
// back in the editor; a hidden or closed one's is Go-to's own path (goTo), which shows it as it
// was, or opens it, and gives it the keyboard. The editor's icon hides every shown panel, and its
// next click shows those again. Hidden is golib's window minimize: the panel stays open, its state
// kept and its Closed hook not run, and its icon is dimmed. While the dock lists more than the
// editor, the bar stays shown however tui.menu.autohide is set.

// editorIcon is the editor's dock icon.
const editorIcon = "✎"

// dockTargets are the dock's widgets: the editor, then each panel open or alive, one per panel (a
// tab is its panel's: the Go-to picker lists tabs, the dock does not), in the registry's order.
func (h *Host) dockTargets() []widgetTarget {
	var out []widgetTarget
	for _, t := range h.widgets() {
		if t.isTab() {
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

// dockRowOf is a widget's dock row: key its :go name, its icon, its title for the tooltip; hidden
// while its panel is in the dock and not shown (hiddenInDock: minimized, or closed and running on, as
// the terminal's and the agents' hide leaves them), which dims its icon; flash while it lights.
func (h *Host) dockRowOf(_ int, t widgetTarget) rowOf {
	icon, title := editorIcon, t.title
	if s := h.panelSpec(t.panel); s != nil {
		icon = s.Icon
	}
	if title == "" {
		title = t.name
	}
	return rowOf{"key": t.name, "icon": icon, "title": title, "hidden": t.panel != "" && h.hiddenInDock(t.panel),
		"flash": t.panel != "" && h.dockFlashLit(t.panel)}
}

// newDockRows is App.dock's model.
func (h *Host) newDockRows() *rows[widgetTarget] {
	return newRows(tuidecl.NewListModel("key", "icon", "title", "hidden", "flash"), h.dockRowOf)
}

// A HIDE FLASHES ITS ICON: a panel hidden into the dock, however (the dock's
// click, the editor's icon, a strip's _, the terminal's and the agents' toggles, Escape), lights its
// icon twice, a step apart, so the eye finds where it went: lit, unlit, lit, then its hidden look.
// A panel closed out of the dock flashes nothing. Shown again mid-flash, its icon is its own at
// once; hidden again, the flash starts over; gone from the dock, the flash ends with it.

// dockFlashStep is each step of a hide's flash, and dockFlashSteps their number: lit on the even ones.
const (
	dockFlashStep  = 150 * time.Millisecond
	dockFlashSteps = 4
)

// dockFlash is one hide's flash of a panel's icon: the step it is at.
type dockFlash struct{ step int }

// flashed, when a test sets it, hears each step of a flash on the loop: the panel, and its icon lit
// or not.
var flashed func(id string, lit bool)

// flashDock starts panel id's flash, over any under way: the older one's steps find it replaced.
func (h *Host) flashDock(id string) {
	if h.dockFlash == nil {
		h.dockFlash = map[string]*dockFlash{}
	}
	f := &dockFlash{}
	h.dockFlash[id] = f
	h.refreshDock()
	if flashed != nil {
		flashed(id, h.dockFlashLit(id))
	}
	h.after(dockFlashStep, func() { h.flashNext(id, f) })
}

// flashNext is a flash's next step, on the loop: none once the flash was replaced, and the last, or
// the panel no longer hidden in the dock, ends it.
func (h *Host) flashNext(id string, f *dockFlash) {
	if h.dockFlash[id] != f {
		return
	}
	f.step++
	if f.step == dockFlashSteps-1 || !h.hiddenInDock(id) { // the last step, unlit, is the hidden look
		delete(h.dockFlash, id)
	} else {
		h.after(dockFlashStep, func() { h.flashNext(id, f) })
	}
	h.refreshDock()
	if flashed != nil {
		flashed(id, h.dockFlashLit(id))
	}
}

// dockFlashLit reports whether panel id's icon is lit now: its flash at an even step, and the panel
// still hidden in the dock.
func (h *Host) dockFlashLit(id string) bool {
	f := h.dockFlash[id]
	return f != nil && f.step%2 == 0 && h.hiddenInDock(id)
}

// hiddenInDock reports whether panel id is in the dock and not shown: open and hidden, or closed and
// living on (panelAlive).
func (h *Host) hiddenInDock(id string) bool {
	s := h.panelSpec(id)
	switch {
	case s == nil:
		return false
	case h.panelOpen[id]:
		return h.panelHidden(id)
	}
	return h.panelAlive(*s)
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

// goDock is a dock icon's click: the widget by its :go name; the editor's shows its window when it
// is minimized, and otherwise hides or shows the panels; a shown panel's hides it, any other goes
// there (goTo).
func (h *Host) goDock(key string) {
	t, err := h.widgetNamed(key)
	if err != nil {
		h.notify(err.Error())
		return
	}
	switch {
	case t.panel == "" && h.editorMinimized():
		h.keep(h.p.Call("editorWindow", "restore"))
		h.focusEditor()
	case t.panel == "":
		h.hideOrShowPanels()
	case h.panelOpen[t.panel] && !h.panelHidden(t.panel):
		h.hidePanel(t.panel)
		h.focusEditor()
	default:
		h.goTo(t)
	}
}

// hideOrShowPanels is the editor's icon: the panels it hid shown again, those still hidden, or else
// every shown panel hidden and remembered. The keyboard is the editor's either way.
func (h *Host) hideOrShowPanels() {
	if hid := h.dockHidden; len(hid) > 0 {
		h.dockHidden = nil
		for _, id := range hid {
			if h.panelOpen[id] && h.panelHidden(id) {
				h.showPanel(id)
			}
		}
	} else {
		var hid []string
		for _, s := range h.panelSpecs() {
			if h.panelOpen[s.ID] && !h.panelHidden(s.ID) {
				h.hidePanel(s.ID)
				hid = append(hid, s.ID)
			}
		}
		h.dockHidden = hid
	}
	h.focusEditor()
}

// hidePanel hides an open panel, not closed (golib's window minimize): it keeps all it holds.
func (h *Host) hidePanel(id string) {
	if err := h.p.Call(id, "minimize"); err != nil {
		h.keep(err)
		return
	}
	h.flashDock(id)
}

// showPanel shows a hidden panel again as it was (a minimized Drawer's open restores it).
func (h *Host) showPanel(id string) { h.keep(h.p.Call(id, "open")) }

// editorMinimized reports whether the page's window is minimized, its icon in the dock.
func (h *Host) editorMinimized() bool { return h.panelHidden("editorWindow") }

// panelHidden reports whether panel id is hidden by the dock: open, minimized.
func (h *Host) panelHidden(id string) bool {
	c, ok := h.p.Find(id)
	w, isWindow := c.(interface{ WindowBehavior() *widget.WindowCore })
	return ok && isWindow && w.WindowBehavior() != nil && w.WindowBehavior().State().Minimized
}

// panelsHiddenChanged follows the window collector: a panel hidden or shown again, by the dock or
// any other way. After the editor's icon hid them every open panel is hidden, so an open panel
// shown, or one it hid closed, means a panel was shown or closed since: its next click hides.
func (h *Host) panelsHiddenChanged() {
	shown := slices.ContainsFunc(h.panelSpecs(), func(s PanelSpec) bool { return h.panelOpen[s.ID] && !h.panelHidden(s.ID) })
	if shown || slices.ContainsFunc(h.dockHidden, func(id string) bool { return !h.panelOpen[id] }) {
		h.dockHidden = nil
	}
	h.refreshDock()
}
