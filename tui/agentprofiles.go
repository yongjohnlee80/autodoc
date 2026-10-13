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
		if a.Codename == def {
			mark = "●"
		}
		if instance := h.instances[a.Codename]; instance != nil && instance.running {
			mark += "▶"
		}
		rows[i] = rowOf{"key": a.Codename, "default": mark, "name": a.Name, "codename": a.Codename,
			"command": a.Command}
	}
	h.agentRows.Reset(rows)
	switch {
	case len(rows) == 0:
		h.c.Set("App.agentProfilesStatus", "no profiles yet: Add… one, a name and the command that starts the agent")
	case def == "":
		h.c.Set("App.agentProfilesStatus", "Make default marks the one SPC g opens")
	default:
		label := def
		if p, ok := h.profile(def); ok {
			label = p.Name
		}
		h.c.Set("App.agentProfilesStatus", "SPC g opens "+label+" · {root} and {workspace} in a command are filled in")
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
	if def != h.c.Prefs().Value(prefAgentDefaultCodename) {
		h.c.SetPref(prefAgentDefaultCodename, def)
	}
	// Keep a legacy label mirror for old readers. The identity preference above
	// is authoritative, including an explicit empty default.
	label := ""
	for _, p := range agents {
		if p.Codename == def {
			label = p.Name
			break
		}
	}
	if label != h.c.Prefs().Value(prefAgentDefault) {
		h.c.SetPref(prefAgentDefault, label)
	}
	h.listAgents()
}

// startAddAgent and startEditAgent open the form, empty or on profile i.
func (h *agents) startAddAgent() {
	h.editingAgent = ""
	h.editingAgentProfile = nil
	h.fillAgentForm("add an agent profile", agentProfile{})
}

func (h *agents) startEditAgent(i int) {
	a, ok := h.agentAt(i)
	if !ok {
		return
	}
	h.editingAgent = a.Codename
	h.editingAgentProfile = &a
	h.fillAgentForm("edit "+a.Name, a)
}

func (h *agents) fillAgentForm(title string, a agentProfile) {
	h.c.Set("App.agentFormTitle", title)
	h.c.Set("App.agentFormError", "")
	h.c.SetFormText("App.agentName", a.Name)
	h.c.SetFormText("App.agentCodename", a.Codename)
	h.c.SetFormText("App.agentCommand", a.Command)
	h.c.Open("agentEdit")
}

// saveAgent keeps the selected profile's identity when a legacy caller edits its label.
func (h *agents) saveAgent(name, command string) {
	codename := ""
	if p, ok := h.profile(h.editingAgent); ok {
		codename = p.Codename
	}
	h.saveAgentProfile(name, command, codename)
}

// saveAgentProfile is the form's save, including the explicit stable identity.
func (h *agents) saveAgentProfile(name, command, codename string) {
	name, command = strings.TrimSpace(name), strings.TrimSpace(command)
	codename = strings.TrimSpace(codename)
	if codename == "" {
		codename = agentCodename(name)
	}
	refuse := func(why string) {
		h.c.Set("App.agentFormError", "not saved: "+why)
		h.c.Open("agentEdit")
	}
	if name == "" || command == "" {
		refuse("a profile is a name and the command that starts the agent")
		return
	}
	if !validAgentCodename(codename) {
		refuse("codename needs lower-case letters or digits, separated by hyphens")
		return
	}
	agents := h.profiles()
	at := -1
	for i, a := range agents {
		if a.Codename == h.editingAgent {
			at = i
		} else if a.Codename == codename {
			refuse("codename taken by " + a.Name)
			return
		}
	}
	if h.editingAgent != "" && (at < 0 || (h.editingAgentProfile != nil && agents[at] != *h.editingAgentProfile)) {
		refuse("the agent profile changed; open it again before saving")
		return
	}
	if at >= 0 {
		if instance := h.instances[agents[at].Codename]; instance != nil && codename != agents[at].Codename {
			refuse("stop the running agent before changing its codename")
			return
		}
	}
	if instance := h.instances[codename]; instance != nil && (at < 0 || agents[at].Codename != codename) {
		refuse("codename held by a running agent")
		return
	}
	def := h.defaultProfile()
	if at >= 0 {
		if def == agents[at].Codename {
			def = codename
		}
		agents[at] = agentProfile{Name: name, Command: command, Codename: codename}
	} else {
		agents = append(agents, agentProfile{Name: name, Command: command, Codename: codename})
		if def == "" {
			def = codename
		}
	}
	h.keepAgents(agents, def)
	h.editingAgent, h.editingAgentProfile = "", nil
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
	if def == a.Codename {
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
	h.keepAgents(h.profiles(), a.Codename)
	h.c.Set("App.status", "SPC g opens "+a.Name)
}

// useAgent starts or selects profile i without stopping other profiles.
func (h *agents) useAgent(i int) {
	a, ok := h.agentAt(i)
	if !ok {
		return
	}
	if instance := h.instances[a.Codename]; instance != nil && instance.running {
		h.c.Close("agentProfiles")
		h.c.OpenDrawer("agent")
		h.c.Keep(h.selectInstance(instance))
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
	h.c.Close("agentProfiles")
	h.startAgent(a)
}
