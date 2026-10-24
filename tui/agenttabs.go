package tui

import "github.com/yongjohnlee80/golib/tui/widget"

// THE AGENTS' STRIP (tabmenus.go): each agent's tab is named by its codename. + offers the
// profiles: a running one goes to its tab, a stopped one starts in one. A tab's menu goes to
// another tab, restarts or closes its agent (asking first while it runs), or opens the profiles.
// Closing the last tab leaves the panel open on its empty text.

// addRows are what + offers: every profile, then Profiles….
func (a *agents) addRows() []widget.MenuItemModel {
	var rows []widget.MenuItemModel
	for _, p := range a.profiles() {
		code, label := p.Codename, p.Name+" ("+p.Codename+")"
		if instance := a.instances[code]; instance != nil && instance.running {
			label += " ▶"
		}
		rows = append(rows, tabMenuRow("agents.add."+code, label, func(widget.TerminalTabID) {
			if p, ok := a.profile(code); ok {
				a.useProfile(p)
			} else {
				a.c.Notify(a.c.Trf("autodoc.notice.agenttabs.the_agent_profile_is_gone_system_agent_profiles", code))
			}
		}))
	}
	if len(rows) > 0 {
		rows = append(rows, widget.NewSeparator("agents.add.profiles"))
	}
	return append(rows, a.profilesRow())
}

// tabRows are tab id's menu: Go to ›, Restart and Close for its agent, Profiles….
func (a *agents) tabRows(id widget.TerminalTabID) []widget.MenuItemModel {
	panel, err := a.runtime()
	if err != nil {
		return nil
	}
	rows := []widget.MenuItemModel{goToRows(panel, func(tab widget.TerminalTab) string { return tab.Label }, a.goToTab)}
	if instance := a.instanceOn(panel, id); instance != nil {
		code := instance.profile.Codename
		restart := tabMenuRow("agents.restart", "Restart", func(widget.TerminalTabID) { a.restartAgent(code) })
		closing := tabMenuRow("agents.close", "Close", func(widget.TerminalTabID) { a.closeAgent(code) })
		restart.Enabled, closing.Enabled = !instance.closing, !instance.closing
		rows = append(rows, restart, closing)
	}
	rows = append(rows, widget.NewSeparator("agents.tab.profiles"), a.profilesRow())
	// Hide is SPC g's own toggle (agent.toggle): the panel closed, every agent running on
	return append(rows, panelRows(a.c, "agent", a.toggleAgent)...)
}

func (a *agents) profilesRow() widget.MenuItemModel {
	return tabMenuRow("agents.profiles", "Profiles…", func(widget.TerminalTabID) { a.openAgentProfiles() })
}

// instanceOn is the agent whose tab id is on panel, nil for none.
func (a *agents) instanceOn(panel TerminalPanel, id widget.TerminalTabID) *agentInstance {
	for _, instance := range a.instances {
		if instance.tab == id && instance.owner != nil && instance.owner.SameOwner(panel) {
			return instance
		}
	}
	return nil
}

// goToTab is Go to › on the strip: the agent's tab selected, as Use selects it.
func (a *agents) goToTab(id widget.TerminalTabID) {
	panel, err := a.runtime()
	if err != nil {
		return
	}
	if instance := a.instanceOn(panel, id); instance != nil {
		a.c.Keep(a.selectInstance(instance))
		return
	}
	a.c.Keep(panel.Select(id))
}

// restartAgent is Restart: a stopped agent starts again in its tab; a running one, once asked,
// closes, and its profile starts again in a new tab once the old process is gone. A stopped one
// whose command or workspace has changed since is closed and started afresh too, as a start in
// its old tab would be refused.
func (a *agents) restartAgent(code string) {
	a.syncInstances()
	instance := a.instances[code]
	if instance == nil || instance.closing {
		return
	}
	p, saved := a.profile(code)
	if !saved {
		p = instance.profile
	}
	ws, _ := a.c.ActiveWorkspace()
	if !instance.running && instance.profile.Command == p.Command && instance.workspace.name == ws.name && instance.workspace.root == ws.root {
		a.startAgent(p)
		return
	}
	again := func() {
		if a.instances[code] != instance || instance.closing {
			return
		}
		instance.restart = true
		if err := a.removeInstance(code); err != nil {
			instance.restart = false
			a.c.Notify(a.c.Trf("autodoc.notice.agenttabs.the_agent_could_not_restart", err.Error()))
		}
	}
	if !instance.running {
		again()
		return
	}
	a.c.Ask(Question{Title: a.c.Tr("autodoc.question.agenttabs.restart_the_agent"), Text: a.c.Trf("autodoc.question.agenttabs.restart_its_process_is_stopped_and_the_agent_sta", p.Name),
		Yes: a.c.Tr("autodoc.question.agenttabs.yes_restart_it"), OnYes: again})
}

// closeAgent is Close: the agent's tab closed, its process stopped; asked first while it runs.
func (a *agents) closeAgent(code string) {
	a.syncInstances()
	instance := a.instances[code]
	if instance == nil || instance.closing {
		return
	}
	closeIt := func() {
		if a.instances[code] != instance || instance.closing {
			return
		}
		if err := a.removeInstance(code); err != nil {
			a.c.Notify(a.c.Trf("autodoc.notice.agenttabs.the_agent_s_tab_could_not_close", err.Error()))
		}
	}
	if !instance.running {
		closeIt()
		return
	}
	a.c.Ask(Question{Title: a.c.Tr("autodoc.question.agenttabs.close_the_agent"), Text: a.c.Trf("autodoc.question.agenttabs.close_its_process_is_stopped", instance.profile.Name),
		Yes: a.c.Tr("autodoc.question.agenttabs.yes_close_it"), OnYes: closeIt})
}

// stopAllAgents is the strip's x: every agent in the panel stopped and its tab closed, then the panel
// closed; asked once while any of them runs. _ beside it is the panel's own hide, every agent kept.
func (a *agents) stopAllAgents() {
	a.syncInstances()
	running := 0
	for _, instance := range a.instances {
		if instance.running && !instance.closing {
			running++
		}
	}
	stopAll := func() {
		for code := range a.instances { // an agent already closing is left to close (removeInstance)
			if err := a.removeInstance(code); err != nil {
				a.c.Notify(a.c.Trf("autodoc.notice.agenttabs.the_agent_s_tab_could_not_close", err.Error()))
			}
		}
		a.c.CloseDrawer("agent")
	}
	if running == 0 {
		stopAll()
		return
	}
	a.c.Ask(Question{Title: a.c.Tr("autodoc.question.agenttabs.stop_the_agents_and_close_the_panel"),
		Text: a.c.Trf("autodoc.question.agenttabs.stop_every_agent_running_here", running),
		Yes:  a.c.Tr("autodoc.question.agenttabs.yes_stop_them"), OnYes: stopAll})
}

// THE AGENTS' CARD — SPC a: the page to an agent (b, :agent-buffer), the selection or the
// clipboard (f, :agent-forward), the agent panel (g) and the profiles (G). b and f run agents.lua's
// commands by their ids, which a user's script of the same name replaces.

const agentsCard = "agentsCard"

// Keys are SPC a, and the card it opens.
func (*agents) Keys() []Key {
	return []Key{
		{Card: leader, Seq: "a", Show: "a", Does: "agents…", Opens: agentsCard, Group: groupAgents},
		{Card: agentsCard, Seq: "b", Show: "b", Does: "send the page to an agent", Run: `App.run("lua.agent-buffer")`},
		{Card: agentsCard, Seq: "f", Show: "f", Does: "send the selection or the clipboard", Run: `App.run("lua.agent-forward")`},
		{Card: agentsCard, Seq: "g", Show: "g", Does: "the agent panel", Run: "App.toggleAgent()"},
		{Card: agentsCard, Seq: "Shift+G", Show: "G", Does: "agent profiles", Run: "App.openAgentProfiles()"},
	}
}

// Cards are the agents' card, Backspace returning to SPC's.
func (*agents) Cards() []Card {
	return []Card{{ID: agentsCard, View: "AgentsCard", Title: "SPC a — agents", Back: leader, Help: "THE AGENTS CARD (SPC a)"}}
}
