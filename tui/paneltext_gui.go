//go:build gui

package tui

import (
	guiwidget "github.com/yongjohnlee80/golib/gui/widget"
	tuidecl "github.com/yongjohnlee80/golib/tui/decl"
	"github.com/yongjohnlee80/golib/tui/widget"
)

// textPanels are the panels whose terminals draw their text at the shells' share of the window's:
// the agent panel's and the terminal panel's TerminalTabs.
var textPanels = []string{"agentView", "terminalView"}

// panelText gives the agent and terminal panels' terminals their text at a share of the window's
// (docs/decisions/editor.md#the-terminal-text-size): golib's TerminalViews on each panel's
// TerminalTabs, one set of views a panel. A reload's new panel gets the views again.
type panelText struct {
	panels map[string]*panelViews
}

// panelViews are one panel's views, and the TerminalTabs they were given to.
type panelViews struct {
	views *guiwidget.TerminalViews
	tabs  *widget.TerminalTabs
}

// apply sets the panels' scale (in percent), making each panel's views the first time and again for
// a panel rebuilt since.
func (a *panelText) apply(h *Host, pct int) {
	scale := float32(pct) / 100
	for _, id := range textPanels {
		a.applyTo(h, id, scale)
	}
}

func (a *panelText) applyTo(h *Host, id string, scale float32) {
	c, ok := h.p.Find(id)
	if !ok {
		return
	}
	tabs, ok := tuidecl.TerminalTabsOf(c)
	if !ok {
		return
	}
	if a.panels == nil {
		a.panels = map[string]*panelViews{}
	}
	p := a.panels[id]
	if p == nil {
		p = &panelViews{views: guiwidget.NewTerminalViews(scale)}
		a.panels[id] = p
	} else {
		p.views.SetScale(scale)
	}
	if p.tabs != tabs {
		p.tabs = tabs
		tabs.SetTerminalViews(p.views.Make)
	}
}
