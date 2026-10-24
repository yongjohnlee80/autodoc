package tui

import (
	"cmp"
	"fmt"
	"strings"

	tuidecl "github.com/yongjohnlee80/golib/tui/decl"
	"github.com/yongjohnlee80/golib/tui/widget"
)

// GOING TO A WIDGET — :go, the Go-to picker (SPC Tab) and F6 / Shift+F6 (ADR 1792256102).
//
// The targets are the places the keyboard can go, listed by the host each time it is asked: the
// editor, every panel in the registry's order, and each tab of the terminal's and the agent's
// TerminalTabs. The menu bar, a dialog and the status line are never targets: the cycle visits the
// widgets only, and the menu bar keeps its own ways in (F10, Alt+letter, SPC m). Going to a target
// never closes it: a closed panel opens its own way, an open one takes the keyboard.

// widgetTarget is one place :go, the Go-to picker and F6 reach.
type widgetTarget struct {
	name    string               // what :go takes: "editor", "autodb", "terminal/shell 2"
	title   string               // what the picker shows, in English: "the editor", "AutoDB", "shell 2 (terminal)"
	panel   string               // the panel's id; "" for the editor
	tab     widget.TerminalTabID // a tab of the panel's TerminalTabs; "" for the panel itself
	showTab func()               // shows a tabbed panel's tab (PanelSpec.GoTabs); nil for none
	open    bool                 // the editor always; a panel by panelOpen; a tab by its panel's
	focused bool
	// the picker's row in the UI's language (openWidgets)
	label, state string
}

// widgetNames are the panel ids whose :go name is not the id.
var widgetNames = map[string]string{"htmlPane": "preview", luaCardPanel: "card"}

// widgets are the targets now.
func (h *Host) widgets() []widgetTarget {
	out := []widgetTarget{{name: "editor", title: inEnglish{}.Trf("autodoc.text.widgets.the_editor"), open: true, focused: h.within("editor")}}
	for _, s := range h.panelSpecs() {
		name := h.widgetName(s.ID)
		out = append(out, widgetTarget{name: name, title: h.panelTitleIn(inEnglish{}.Trf, s.ID), panel: s.ID,
			open: h.panelOpen[s.ID], focused: h.within(s.focusView())})
		for tab, goName := range s.GoTabs { // the explorer's Links tab
			if goName != "" {
				out = append(out, widgetTarget{name: goName, title: goName + " (" + name + ")", panel: s.ID,
					showTab: func() { s.ShowTab(tab) }, open: h.panelOpen[s.ID],
					focused: h.within(s.focusView()) && s.Tab() == tab})
			}
		}
		if tabs, ok := h.tabsOf(s.View); ok { // the terminal's and the agent's
			for _, tab := range tabs.Tabs() {
				out = append(out, widgetTarget{name: name + "/" + tab.Label, title: tab.Label + " (" + name + ")",
					panel: s.ID, tab: tab.ID, open: h.panelOpen[s.ID],
					focused: h.within(s.View) && tabs.Active() == tab.ID})
			}
		}
	}
	return out
}

// widgetName is panel id's :go name: a card plugin's its plugin's, unless a built-in target has that
// name (then the panel's id); else widgetNames', else the id.
func (h *Host) widgetName(id string) string {
	if n, ok := widgetNames[id]; ok {
		return n
	}
	if h.plugins != nil {
		for _, c := range h.plugins.cardOrder {
			if cardPanelID(c.name) == id {
				if h.builtinWidgetName(c.name) {
					return id
				}
				return c.name
			}
		}
	}
	return id
}

// builtinWidgetName reports whether name is the :go name of the editor or of a panel no plugin made.
func (h *Host) builtinWidgetName(name string) bool {
	if name == "editor" {
		return true
	}
	for _, s := range h.panelSpecs() {
		if strings.HasPrefix(s.ID, cardPanelPrefix) {
			continue
		}
		if s.ID == name || widgetNames[s.ID] == name {
			return true
		}
	}
	return false
}

// isTab reports whether t is a panel's tab, not the panel: the dock and F6 visit panels only.
func (t widgetTarget) isTab() bool { return t.tab != "" || t.showTab != nil }

// within reports whether the component id holds the keyboard.
func (h *Host) within(id string) bool {
	c, ok := h.p.Find(id)
	return ok && h.p.App().FocusWithin(c)
}

// tabsOf is view's TerminalTabs, when it is one.
func (h *Host) tabsOf(view string) (*widget.TerminalTabs, bool) {
	c, ok := h.p.Find(view)
	if !ok {
		return nil, false
	}
	return tuidecl.TerminalTabsOf(c)
}

// goTo puts the keyboard on t: a closed panel opened its own way, never an open one closed.
func (h *Host) goTo(t widgetTarget) {
	if t.panel == "" {
		h.focusEditor()
		return
	}
	s := h.panelSpec(t.panel)
	if s == nil {
		return
	}
	if t.showTab != nil {
		t.showTab() // first, so the panel opens on it
	}
	if h.panelOpen[t.panel] && h.panelHidden(t.panel) {
		h.showPanel(t.panel) // hidden by the dock: shown as it was
	} else if !h.panelOpen[t.panel] {
		if s.Open != nil {
			s.Open(h.panelHost(t.panel)) // the terminal's opens a shell when it has none; the agent's asks
		} else {
			h.openDrawer(t.panel) // opens and focuses
		}
	}
	// a TerminalTabs view gives the keyboard to its tab's terminal by selecting it (golib's Select
	// focuses the terminal); forceActiveFocus on the view would focus its strip
	if tabs, ok := h.tabsOf(s.View); ok {
		tab := cmp.Or(t.tab, tabs.Active())
		if tab != "" {
			h.keep(tabs.Select(tab))
			return
		}
	}
	h.keep(h.p.Call(s.focusView(), "forceActiveFocus"))
}

// widgetNamed is the target arg names: the one named exactly, else the one whose name holds it.
func (h *Host) widgetNamed(arg string) (widgetTarget, error) {
	var found []widgetTarget
	for _, t := range h.widgets() {
		if t.name == arg {
			return t, nil
		}
		if strings.Contains(t.name, arg) {
			found = append(found, t)
		}
	}
	switch len(found) {
	case 0:
		return widgetTarget{}, fmt.Errorf("go: no widget %q", arg)
	case 1:
		return found[0], nil
	}
	return widgetTarget{}, fmt.Errorf("go: %d widgets match %q: give more of the name", len(found), arg)
}

// widgetEntries are :go, as a catalog entry: builtinEntries adds it beside bufferEntries.
func (h *Host) widgetEntries() []Entry {
	return []Entry{{ID: "widget.go", Title: "go to a widget", Verb: "go", Args: "<name>", Twin: "App.openWidgets()",
		Help: "put the keyboard on a widget: a panel opens if closed; Tab lists the names; alone, the Go-to picker",
		Complete: func(widget.CompleteContext) []string {
			var out []string
			for _, t := range h.widgets() {
				out = append(out, t.name)
			}
			return out
		},
		Run: func(x Invocation) error {
			if x.Line == "" {
				h.openWidgets()
				return nil
			}
			t, err := h.widgetNamed(x.Line)
			if err != nil {
				return err
			}
			h.goTo(t)
			return nil
		}}}
}

// THE GO-TO PICKER (GoPick.qml): the targets as rows, the focused one under the cursor. Enter goes;
// Tab and Shift+Tab move the cursor, wrapping, as Down and Up do. Its cursor is App.widgetIndex,
// which the host moves (widgetStep) and the list reports (widgetMoved).

// widgetRowOf is a target's row: its name and title, and "here", "open" or "closed", as
// withRowText composed them.
func widgetRowOf(_ int, t widgetTarget) rowOf {
	return rowOf{"key": t.name, "label": t.label, "state": t.state}
}

// withRowText is t with its picker row composed in the UI's language: its name and title, and
// "here", "open" or "closed".
func (h *Host) withRowText(t widgetTarget) widgetTarget {
	t.state = h.tr("autodoc.text.widgets.closed")
	switch {
	case t.focused:
		t.state = h.tr("autodoc.text.widgets.here")
	case t.open:
		t.state = h.tr("autodoc.text.widgets.open")
	}
	t.label = t.name
	if t.title != "" && t.title != thePanel(inEnglish{}.Trf, t.name) { // its title, English (Lua's), says more than its name
		title := t.title // a tab's: its label and its panel's name
		switch {
		case t.panel == "":
			title = h.tr("autodoc.text.widgets.the_editor")
		case !t.isTab():
			title = h.panelTitle(t.panel)
		}
		t.label += " — " + title
	}
	return t
}

func newWidgetRows() *rows[widgetTarget] {
	return newRows(tuidecl.NewListModel("key", "label", "state"), widgetRowOf)
}

// openWidgets is SPC Tab and :go alone: the picker over the targets now, its cursor on the focused one.
func (h *Host) openWidgets() {
	all := h.widgets()
	for i := range all {
		all[i] = h.withRowText(all[i])
	}
	h.widgetRows.Reset(all)
	at := 0
	for i, t := range all {
		if t.focused {
			at = i
			break
		}
	}
	h.setWidgetIndex(at)
	h.open("widgetPick")
}

// setWidgetIndex moves the picker's cursor to i: moved away first, so the same index twice still
// moves it after the list's own cursor wandered.
func (h *Host) setWidgetIndex(i int) {
	h.widgetAt = i
	h.set("App.widgetIndex", -1)
	h.set("App.widgetIndex", i)
}

// widgetMoved is the list's cursor landing on row i (the arrows, a click).
func (h *Host) widgetMoved(i int) {
	if i >= 0 {
		h.widgetAt = i
	}
}

// widgetStep is Tab (1) and Shift+Tab (-1) in the picker: the cursor a row on, wrapping.
func (h *Host) widgetStep(dir int) {
	n := h.widgetRows.Len()
	if n == 0 {
		return
	}
	h.setWidgetIndex(((h.widgetAt+dir)%n + n) % n)
}

// goWidget is Enter on row i: the picker closes, and the keyboard goes to that target as it is now.
func (h *Host) goWidget(i int) {
	t, ok := h.widgetRows.At(i)
	h.closeDialog("widgetPick")
	if !ok {
		return
	}
	now, err := h.widgetNamed(t.name)
	if err != nil {
		h.notify(err.Error())
		return
	}
	h.goTo(now)
}

// cycleWidget is F6 (dir 1) and Shift+F6 (dir -1): the keyboard to the next open target after the
// focused one, wrapping. A tab is its panel's: the cycle visits panels, never a panel's tabs one by
// one. With no target focused (a dialog has the keyboard), it steps from the editor.
func (h *Host) cycleWidget(dir int) {
	var open []widgetTarget
	at := 0
	for _, t := range h.widgets() {
		if !t.open || t.isTab() {
			continue
		}
		if t.focused {
			at = len(open)
		}
		open = append(open, t)
	}
	if len(open) < 2 {
		return
	}
	h.goTo(open[((at+dir)%len(open)+len(open))%len(open)])
}
