package tui

import (
	"strings"
)

// THE AGENT PROFILES — Options › Agent profiles…: the profiles, each a name and a command, the
// default marked; Add…, Edit…, Make default, Use and Remove…. A refused save opens the form again,
// saying why.

// openAgentProfiles lists the profiles and opens the dialog.
func (h *Host) openAgentProfiles() {
	h.listAgents()
	h.open("agentProfiles")
}

// listAgents shows the profiles, the default marked, and keeps the cursor on a row that exists.
func (h *Host) listAgents() {
	rows := make([]rowOf, len(h.prefs.agents))
	for i, a := range h.prefs.agents {
		mark := ""
		if a.Name == h.prefs.agentDefault {
			mark = "●"
		}
		if a.Name == h.agentRunning {
			mark += "▶"
		}
		rows[i] = rowOf{"key": a.Name, "default": mark, "name": a.Name, "command": a.Command}
	}
	h.agentRows.Reset(rows)
	switch {
	case len(rows) == 0:
		h.set("App.agentProfilesStatus", "no profiles yet: Add… one, a name and the command that starts the agent")
	case h.prefs.agentDefault == "":
		h.set("App.agentProfilesStatus", "Make default marks the one SPC g opens")
	default:
		h.set("App.agentProfilesStatus", "SPC g opens "+h.prefs.agentDefault+" · {root} and {workspace} in a command are filled in")
	}
}

func (h *Host) agentAt(i int) (agentProfile, bool) {
	if i < 0 || i >= len(h.prefs.agents) {
		return agentProfile{}, false
	}
	return h.prefs.agents[i], true
}

// keepAgents writes the profiles and the default, and lists them again.
func (h *Host) keepAgents(agents []agentProfile, def string) {
	h.setPref(prefAgentProfiles, agentProfilesJSON(agents), func(p *prefs) { p.agents = agents })
	if def != h.prefs.agentDefault {
		h.setPref(prefAgentDefault, def, func(p *prefs) { p.agentDefault = def })
	}
	h.listAgents()
}

// startAddAgent and startEditAgent open the form, empty or on profile i.
func (h *Host) startAddAgent() {
	h.editingAgent = ""
	h.fillAgentForm("add an agent profile", agentProfile{})
}

func (h *Host) startEditAgent(i int) {
	a, ok := h.agentAt(i)
	if !ok {
		return
	}
	h.editingAgent = a.Name
	h.fillAgentForm("edit "+a.Name, a)
}

func (h *Host) fillAgentForm(title string, a agentProfile) {
	h.set("App.agentFormTitle", title)
	h.set("App.agentFormError", "")
	h.setField("App.agentName", a.Name)
	h.setField("App.agentCommand", a.Command)
	h.open("agentEdit")
}

// saveAgent adds the profile the form describes, or rewrites the one it edits: a name another
// profile has, or an empty name or command, opens the form again, saying why. The first profile
// added becomes the default.
func (h *Host) saveAgent(name, command string) {
	name, command = strings.TrimSpace(name), strings.TrimSpace(command)
	refuse := func(why string) {
		h.set("App.agentFormError", "not saved: "+why)
		h.open("agentEdit")
	}
	if name == "" || command == "" {
		refuse("a profile is a name and the command that starts the agent")
		return
	}
	agents := append([]agentProfile(nil), h.prefs.agents...)
	at := -1
	for i, a := range agents {
		if a.Name == name && a.Name != h.editingAgent {
			refuse("another profile is named " + name)
			return
		}
		if a.Name == h.editingAgent {
			at = i
		}
	}
	def := h.prefs.agentDefault
	if at >= 0 {
		if def == agents[at].Name {
			def = name
		}
		agents[at] = agentProfile{Name: name, Command: command}
	} else {
		agents = append(agents, agentProfile{Name: name, Command: command})
		if def == "" {
			def = name
		}
	}
	h.keepAgents(agents, def)
	h.say("saved the agent profile " + name)
}

// removeAgent removes profile i; it is the default no more.
func (h *Host) removeAgent(i int) {
	a, ok := h.agentAt(i)
	if !ok {
		return
	}
	agents := append(append([]agentProfile(nil), h.prefs.agents[:i]...), h.prefs.agents[i+1:]...)
	def := h.prefs.agentDefault
	if def == a.Name {
		def = ""
	}
	h.keepAgents(agents, def)
	h.say("removed the agent profile " + a.Name)
}

// makeAgentDefault marks profile i the one SPC g opens.
func (h *Host) makeAgentDefault(i int) {
	a, ok := h.agentAt(i)
	if !ok {
		return
	}
	h.keepAgents(h.prefs.agents, a.Name)
	h.say("SPC g opens " + a.Name)
}

// useAgent starts profile i now. Another running is stopped first, after asking.
func (h *Host) useAgent(i int) {
	a, ok := h.agentAt(i)
	if !ok {
		return
	}
	if h.agentRunning == a.Name {
		h.closeDialog("agentProfiles")
		h.toggleAgentShown()
		return
	}
	if h.agentRunning != "" {
		h.switchingAgent = a.Name
		h.set("App.agentSwitchQuestion", "Stop "+h.agentRunning+" and start "+a.Name+"? What "+h.agentRunning+" was doing ends.")
		h.open("agentSwitch")
		return
	}
	h.closeDialog("agentProfiles")
	h.startAgent(a)
}

// switchAgentConfirmed stops the agent running and starts the one asked for.
func (h *Host) switchAgentConfirmed() {
	a, ok := h.prefs.agentProfile(h.switchingAgent)
	if !ok {
		return
	}
	h.stopAgent()
	h.closeDialog("agentProfiles")
	h.startAgent(a)
}

// toggleAgentShown shows the agent running, its panel open.
func (h *Host) toggleAgentShown() {
	if !h.panelOpen["agent"] {
		h.keep(h.p.Call("agent", "open"))
	}
	h.keep(h.p.Call("agentView", "forceActiveFocus"))
}
