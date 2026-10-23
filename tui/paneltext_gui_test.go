//go:build gui

package tui

import (
	"strings"
	"testing"

	tuidecl "github.com/yongjohnlee80/golib/tui/decl"
	"github.com/yongjohnlee80/golib/tui/widget"
)

// In the GUI the agent and terminal panels' terminals are views at the stored share of the cell size
// (ADR 1792350322): their text is drawn off the window's grid, natively, so none of it is in the
// grid's cells; a Fonts change rescales both panels' views, and a panel without them gets them at
// the next apply.
func TestTheShellsTextIsAViewOfItsOwnInTheGUI(t *testing.T) {
	agent, _ := fakeAgent(t)
	_, r, _ := runAgentTUIWith(t, t.TempDir(), []agentProfile{{Name: "fake", Command: shellQuote(agent)}}, "fake",
		map[string]string{"tui.agent.text.scale": "80"}, Options{GUI: true, Fonts: &fakeFonts{}})
	tabs := onLoop(r, func() *widget.TerminalTabs {
		c, _ := r.h.p.Find("agentView")
		tabs, _ := tuidecl.TerminalTabsOf(c)
		return tabs
	})
	scaleOf := func(id string) float32 {
		return onLoop(r, func() float32 {
			c, _ := r.h.p.Find(id)
			panelTabs, _ := tuidecl.TerminalTabsOf(c)
			p := r.h.panelText.panels[id]
			if p == nil || p.tabs != panelTabs || panelTabs == nil {
				return 0
			}
			return p.views.Scale()
		})
	}
	scale := func() float32 { return scaleOf("agentView") }
	r.s.WaitFor(t, "the agents panel's views at 80%", func(string) bool { return scale() == 0.8 })
	r.s.WaitFor(t, "the terminal panel's views at 80% too", func(string) bool { return scaleOf("terminalView") == 0.8 })

	r.leader(t, 'g')
	r.s.WaitForText(t, "agent · fake")
	// without its view the agent's text is in the grid: what it printed has reached the terminal
	onLoop(r, func() bool { tabs.SetTerminalViews(nil); return true })
	r.s.WaitForText(t, "started")
	// the views given again (as to a rebuilt panel), the same text leaves the grid
	onLoop(r, func() bool { r.h.panelText.panels["agentView"].tabs = nil; r.h.setAgentScaleIndex(1); return true })
	r.s.WaitFor(t, "the agent's text off the grid", func(sc string) bool { return !strings.Contains(sc, "started") })
	if got := scale(); got != 0.6 {
		t.Errorf("60%% chosen, the views are at %v", got)
	}
	if got := scaleOf("terminalView"); got != 0.6 {
		t.Errorf("60%% chosen, the terminal panel's views are at %v", got)
	}
}
