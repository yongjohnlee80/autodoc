//go:build gui

package tui

import (
	"strings"
	"testing"

	tuidecl "github.com/yongjohnlee80/golib/tui/decl"
	"github.com/yongjohnlee80/golib/tui/widget"
)

// In the GUI the agents panel's terminals are views at the stored share of the cell size (ADR
// 1792350322): their text is drawn off the window's grid, natively, so none of it is in the grid's
// cells; a Fonts change rescales them, and a panel without them gets them at the next apply.
func TestTheAgentsTextIsAViewOfItsOwnInTheGUI(t *testing.T) {
	agent, _ := fakeAgent(t)
	_, r, _ := runAgentTUIWith(t, t.TempDir(), []agentProfile{{Name: "fake", Command: shellQuote(agent)}}, "fake",
		map[string]string{"tui.agent.text.scale": "80"}, Options{GUI: true, Fonts: &fakeFonts{}})
	tabs := onLoop(r, func() *widget.TerminalTabs {
		c, _ := r.h.p.Find("agentView")
		tabs, _ := tuidecl.TerminalTabsOf(c)
		return tabs
	})
	scale := func() float32 {
		return onLoop(r, func() float32 {
			if r.h.agentText.tabs != tabs || r.h.agentText.views == nil {
				return 0
			}
			return r.h.agentText.views.Scale()
		})
	}
	r.s.WaitFor(t, "the agents panel's views at 80%", func(string) bool { return scale() == 0.8 })

	r.leader(t, 'g')
	r.s.WaitForText(t, "agent · fake")
	// without its view the agent's text is in the grid: what it printed has reached the terminal
	onLoop(r, func() bool { tabs.SetTerminalViews(nil); return true })
	r.s.WaitForText(t, "started")
	// the views given again (as to a rebuilt panel), the same text leaves the grid
	onLoop(r, func() bool { r.h.agentText.tabs = nil; r.h.setAgentScaleIndex(1); return true })
	r.s.WaitFor(t, "the agent's text off the grid", func(sc string) bool { return !strings.Contains(sc, "started") })
	if got := scale(); got != 0.75 {
		t.Errorf("75%% chosen, the views are at %v", got)
	}
}
