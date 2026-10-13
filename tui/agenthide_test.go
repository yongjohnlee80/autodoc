//go:build linux || darwin

package tui

import (
	"testing"

	"github.com/yongjohnlee80/golib/tui/widget"
)

// TestHideClosesTheAgentPanelAndKeepsTheAgents: Hide on an agent tab's menu closes the agent panel
// (SPC g's own toggle), the agent running on, the same incarnation; opening it again shows its tab.
func TestHideClosesTheAgentPanelAndKeepsTheAgents(t *testing.T) {
	agent, _ := fakeAgent(t)
	_, r, _ := runAgentTUI(t, t.TempDir(), []agentProfile{{Name: "heron", Command: shellQuote(agent), Codename: "heron"}}, "heron")
	onLoop(r, func() bool { r.h.agents.toggleAgent(); return true })
	r.s.WaitFor(t, "the agent running", func(string) bool { return r.agentRunningNow() })
	shown := func() bool { return onLoop(r, func() bool { return r.h.panelOpen["agent"] }) }
	token := func() string { return onLoop(r, func() string { return r.h.agents.instances["heron"].token }) }
	tab := onLoop(r, func() widget.TerminalTabID { return r.h.agents.instances["heron"].tab })
	was := token()
	rows := onLoop(r, func() []widget.MenuItemModel { return r.h.agents.tabRows(tab) })
	row, ok := menuRow(rows, "tabs.hide")
	if !ok {
		t.Fatalf("no Hide in %v", rowIDs(rows))
	}
	if _, ok := menuRow(rows, "tabs.look"); !ok {
		t.Fatalf("no Theme and font… in %v", rowIDs(rows))
	}
	// Maximize first: the agent panel fills the dock area, and Hide still keeps the agent
	max, ok := menuRow(rows, "tabs.maximize")
	if !ok || max.Label != "Maximize" {
		t.Fatalf("no Maximize in %v", rowIDs(rows))
	}
	onLoop(r, func() bool { max.Action.(widget.TerminalTabsMenuAction).Run(tab); return true })
	r.s.WaitFor(t, "the agent panel maximized", func(string) bool { return onLoop(r, func() bool { return r.h.panelMax["agent"] }) })
	if again, _ := menuRow(onLoop(r, func() []widget.MenuItemModel { return r.h.agents.tabRows(tab) }), "tabs.maximize"); again.Label != "Restore" {
		t.Errorf("maximized, the row reads %q", again.Label)
	}
	onLoop(r, func() bool { row.Action.(widget.TerminalTabsMenuAction).Run(tab); return true })
	r.s.WaitFor(t, "hidden", func(string) bool { return !shown() })
	if !r.agentRunningNow() || token() != was {
		t.Fatalf("Hide stopped or restarted the agent: running %v, token %s (was %s)", r.agentRunningNow(), token(), was)
	}
	onLoop(r, func() bool { r.h.agents.toggleAgent(); return true })
	r.s.WaitFor(t, "shown again", func(string) bool { return shown() })
	if token() != was || onLoop(r, func() widget.TerminalTabID { p, _ := r.h.agents.runtime(); return p.Active() }) != tab {
		t.Fatal("reopened on another agent or tab")
	}
}

// TestAClickOnMaximizeInTheAgentsMenu: with an agent running (the panel opens as it starts), a click
// on Maximize in the agents' ≡ menu maximizes the panel, and a click on Restore restores it; nothing
// is refused.
func TestAClickOnMaximizeInTheAgentsMenu(t *testing.T) {
	agent, _ := fakeAgent(t)
	_, r, _ := runAgentTUI(t, t.TempDir(), []agentProfile{{Name: "heron", Command: shellQuote(agent), Codename: "heron"}}, "heron")
	onLoop(r, func() bool { r.h.agents.toggleAgent(); return true })
	r.s.WaitFor(t, "the agent running", func(string) bool { return r.agentRunningNow() })
	r.s.WaitFor(t, "the agents' panel open", func(string) bool { return onLoop(r, func() bool { return r.h.panelOpen["agent"] }) })
	clickMaximizeAndRestore(t, r, "agent")
}
