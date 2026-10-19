//go:build gui

package tui

import (
	guiwidget "github.com/yongjohnlee80/golib/gui/widget"
	tuidecl "github.com/yongjohnlee80/golib/tui/decl"
	"github.com/yongjohnlee80/golib/tui/widget"
)

// agentText gives the agents panel's terminals their text at a share of the window's (ADR
// 1792350322): golib's TerminalViews on that panel's TerminalTabs alone; the terminal panel keeps
// the window's cells. A reload's new panel gets the views again.
type agentText struct {
	views *guiwidget.TerminalViews
	tabs  *widget.TerminalTabs
}

// apply sets the agents panel's scale (in percent), making its views the first time and again for a
// panel rebuilt since.
func (a *agentText) apply(h *Host, pct int) {
	scale := float32(pct) / 100
	c, ok := h.p.Find("agentView")
	if !ok {
		return
	}
	tabs, ok := tuidecl.TerminalTabsOf(c)
	if !ok {
		return
	}
	if a.views == nil {
		a.views = guiwidget.NewTerminalViews(scale)
	} else {
		a.views.SetScale(scale)
	}
	if a.tabs != tabs {
		a.tabs = tabs
		tabs.SetTerminalViews(a.views.Make)
	}
}
