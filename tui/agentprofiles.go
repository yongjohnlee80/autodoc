package tui

import (
	"fmt"
	"slices"
	"strings"
)

// THE AGENT PROFILES — System › Agent profiles…: the profiles, each a name and a command, the
// default marked; Add…, Edit…, Make default, Use and Remove…. A refused save opens the form again,
// saying why.

// openAgentProfiles lists the profiles and opens the dialog.
func (h *agents) openAgentProfiles() {
	h.listAgents()
	h.c.Open("agentProfiles")
}

// listAgents shows the profiles, the default marked, and keeps the cursor on a row that exists.
func (h *agents) listAgents() {
	profiles, def := h.profiles(), h.defaultProfile()
	rows := make([]rowOf, len(profiles))
	for i, a := range profiles {
		mark := ""
		if a.Name == def {
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
		h.c.Set("App.agentProfilesStatus", "no profiles yet: Add… one, a name and the command that starts the agent")
	case def == "":
		h.c.Set("App.agentProfilesStatus", "Make default marks the one SPC g opens")
	default:
		h.c.Set("App.agentProfilesStatus", "SPC g opens "+def+" · {root} and {workspace} in a command are filled in")
	}
}

func (h *agents) agentAt(i int) (agentProfile, bool) {
	profiles := h.profiles()
	if i < 0 || i >= len(profiles) {
		return agentProfile{}, false
	}
	return profiles[i], true
}

// keepAgents writes the profiles and the default, and lists them again.
func (h *agents) keepAgents(agents []agentProfile, def string) {
	h.c.SetPref(prefAgentProfiles, agentProfilesJSON(agents))
	if def != h.defaultProfile() {
		h.c.SetPref(prefAgentDefault, def)
	}
	h.listAgents()
}

// startAddAgent and startEditAgent open the form, empty or on profile i.
func (h *agents) startAddAgent() {
	h.editingAgent = ""
	h.fillAgentForm("add an agent profile", agentProfile{})
}

func (h *agents) startEditAgent(i int) {
	a, ok := h.agentAt(i)
	if !ok {
		return
	}
	h.editingAgent = a.Name
	h.fillAgentForm("edit "+a.Name, a)
}

func (h *agents) fillAgentForm(title string, a agentProfile) {
	h.c.Set("App.agentFormTitle", title)
	h.c.Set("App.agentFormError", "")
	h.c.SetFormText("App.agentName", a.Name)
	h.c.SetFormText("App.agentCommand", a.Command)
	h.c.Open("agentEdit")
}

// saveAgent adds the profile the form describes, or rewrites the one it edits: a name another
// profile has, or an empty name or command, opens the form again, saying why. The first profile
// added becomes the default.
func (h *agents) saveAgent(name, command string) {
	name, command = strings.TrimSpace(name), strings.TrimSpace(command)
	refuse := func(why string) {
		h.c.Set("App.agentFormError", "not saved: "+why)
		h.c.Open("agentEdit")
	}
	if name == "" || command == "" {
		refuse("a profile is a name and the command that starts the agent")
		return
	}
	agents := h.profiles()
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
	def := h.defaultProfile()
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
	h.c.Set("App.status", "saved the agent profile "+name)
}

// removeAgent asks about the profile at i. The answer holds that profile, not
// the row: a list refresh or a changed selection cannot remove a different one.
func (h *agents) removeAgent(i int) {
	a, ok := h.agentAt(i)
	if !ok {
		return
	}
	h.c.Ask(Question{
		Title: "remove the agent profile?",
		Text: fmt.Sprintf("Remove the saved agent profile %q? It will no longer be available to start. "+
			"A running agent is not stopped.", a.Name),
		Yes:   "&Yes, remove it",
		OnYes: func() { h.removeAgentConfirmed(a) },
	})
}

// removeAgentConfirmed removes the exact profile the question named. A profile
// edited or removed while the card was open needs a new question.
func (h *agents) removeAgentConfirmed(a agentProfile) {
	profiles := h.profiles()
	i := slices.Index(profiles, a)
	if i < 0 {
		h.c.Set("App.status", "the agent profile changed; ask to remove it again")
		return
	}
	agents := append(append([]agentProfile(nil), profiles[:i]...), profiles[i+1:]...)
	def := h.defaultProfile()
	if def == a.Name {
		def = ""
	}
	h.keepAgents(agents, def)
	h.c.Set("App.status", "removed the agent profile "+a.Name)
}

// makeAgentDefault marks profile i the one SPC g opens.
func (h *agents) makeAgentDefault(i int) {
	a, ok := h.agentAt(i)
	if !ok {
		return
	}
	h.keepAgents(h.profiles(), a.Name)
	h.c.Set("App.status", "SPC g opens "+a.Name)
}

// useAgent starts profile i now. Another running is stopped first, after asking.
func (h *agents) useAgent(i int) {
	a, ok := h.agentAt(i)
	if !ok {
		return
	}
	if h.agentRunning == a.Name {
		h.c.Close("agentProfiles")
		h.toggleAgentShown()
		return
	}
	if h.agentRunning != "" {
		h.switchingAgent = a.Name
		h.c.Set("App.agentSwitchQuestion", "Stop "+h.agentRunning+" and start "+a.Name+"? What "+h.agentRunning+" was doing ends.")
		h.c.Open("agentSwitch")
		return
	}
	h.c.Close("agentProfiles")
	h.startAgent(a)
}

// switchAgentConfirmed stops the agent running and starts the one asked for.
func (h *agents) switchAgentConfirmed() {
	a, ok := h.profile(h.switchingAgent)
	if !ok {
		return
	}
	h.stopAgent()
	h.c.Close("agentProfiles")
	h.startAgent(a)
}

// toggleAgentShown shows the agent running, its panel open.
func (h *agents) toggleAgentShown() {
	if !h.c.PanelOpen("agent") {
		h.c.OpenDrawer("agent")
	}
	h.c.Keep(h.c.Focus("agentView"))
}
