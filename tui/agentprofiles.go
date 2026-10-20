package tui

import (
	"slices"
	"strconv"
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
		h.c.Set("App.agentProfilesStatus", h.c.Tr("autodoc.text.agentprofiles.no_profiles_yet_add_one_a_name_and_the_command_t"))
	case def == "":
		h.c.Set("App.agentProfilesStatus", h.c.Tr("autodoc.text.agentprofiles.make_default_marks_the_one_spc_g_opens"))
	default:
		label := def
		if p, ok := h.profile(def); ok {
			label = p.Name
		}
		h.c.Set("App.agentProfilesStatus", h.c.Trf("autodoc.text.agentprofiles.spc_g_opens_root_and_workspace_in_a_command_are", label))
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
	h.fillAgentForm(h.c.Tr("autodoc.text.agentprofiles.add_an_agent_profile"), agentProfile{})
}

func (h *agents) startEditAgent(i int) {
	a, ok := h.agentAt(i)
	if !ok {
		return
	}
	h.editingAgent = a.Codename
	h.editingAgentProfile = &a
	h.fillAgentForm(h.c.Trf("autodoc.text.agentprofiles.edit", a.Name), a)
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

// saveAgentProfile is the profile form's Save (App.saveAgentProfile): storeAgentProfile, its
// answer read by nothing, since a refusal has already reopened the form with its reason.
func (h *agents) saveAgentProfile(name, command, codename string) {
	h.storeAgentProfile(name, command, codename)
}

// storeAgentProfile is the form's save, including the explicit stable identity.
// It returns the profile it stored and true, or false when it refused: the one save path, the form's
// and the agent setup's (agentsetup.go), so both share every validation and neither infers success.
func (h *agents) storeAgentProfile(name, command, codename string) (agentProfile, bool) {
	name, command = strings.TrimSpace(name), strings.TrimSpace(command)
	codename = strings.TrimSpace(codename)
	if codename == "" {
		codename = agentCodename(name)
	}
	refuse := func(why string) (agentProfile, bool) {
		h.c.Set("App.agentFormError", h.c.Trf("autodoc.text.agentprofiles.not_saved", why))
		h.c.Open("agentEdit")
		return agentProfile{}, false
	}
	if name == "" || command == "" {
		return refuse(h.c.Tr("autodoc.text.agentprofiles.a_profile_is_a_name_and_the_command_that_starts"))
	}
	if !validAgentCodename(codename) {
		return refuse(h.c.Tr("autodoc.text.agentprofiles.codename_needs_lower_case_letters_or_digits_sepa"))
	}
	agents := h.profiles()
	at := -1
	for i, a := range agents {
		if a.Codename == h.editingAgent {
			at = i
		} else if a.Codename == codename {
			return refuse(h.c.Trf("autodoc.text.agentprofiles.codename_taken_by", a.Name))
		}
	}
	if h.editingAgent != "" && (at < 0 || (h.editingAgentProfile != nil && agents[at] != *h.editingAgentProfile)) {
		return refuse(h.c.Tr("autodoc.text.agentprofiles.the_agent_profile_changed_open_it_again_before_s"))
	}
	if at >= 0 {
		if instance := h.instances[agents[at].Codename]; instance != nil && codename != agents[at].Codename {
			return refuse(h.c.Tr("autodoc.text.agentprofiles.stop_the_running_agent_before_changing_its_coden"))
		}
	}
	if instance := h.instances[codename]; instance != nil && (at < 0 || agents[at].Codename != codename) {
		return refuse(h.c.Tr("autodoc.text.agentprofiles.codename_held_by_a_running_agent"))
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
	h.c.Set("App.status", h.c.Trf("autodoc.text.agentprofiles.saved_the_agent_profile", name))
	return agentProfile{Name: name, Command: command, Codename: codename}, true
}

// removeAgent asks about the profile at i. The answer holds that profile, not
// the row: a list refresh or a changed selection cannot remove a different one.
func (h *agents) removeAgent(i int) {
	a, ok := h.agentAt(i)
	if !ok {
		return
	}
	h.c.Ask(Question{
		Title: h.c.Tr("autodoc.question.agentprofiles.remove_the_agent_profile"),
		Text:  h.c.Trf("autodoc.question.agentprofiles.remove_the_saved_profile", strconv.Quote(a.Name)),
		Yes:   h.c.Tr("autodoc.question.agentprofiles.yes_remove_it"),
		OnYes: func() { h.removeAgentConfirmed(a) },
	})
}

// removeAgentConfirmed removes the exact profile the question named. A profile
// edited or removed while the card was open needs a new question.
func (h *agents) removeAgentConfirmed(a agentProfile) {
	profiles := h.profiles()
	i := slices.Index(profiles, a)
	if i < 0 {
		h.c.Set("App.status", h.c.Tr("autodoc.text.agentprofiles.the_agent_profile_changed_ask_to_remove_it_again"))
		return
	}
	agents := append(append([]agentProfile(nil), profiles[:i]...), profiles[i+1:]...)
	def := h.defaultProfile()
	if def == a.Codename {
		def = ""
	}
	h.keepAgents(agents, def)
	h.c.Set("App.status", h.c.Trf("autodoc.text.agentprofiles.removed_the_agent_profile", a.Name))
}

// makeAgentDefault marks profile i the one SPC g opens.
func (h *agents) makeAgentDefault(i int) {
	a, ok := h.agentAt(i)
	if !ok {
		return
	}
	h.keepAgents(h.profiles(), a.Codename)
	h.c.Set("App.status", h.c.Trf("autodoc.text.agentprofiles.spc_g_opens", a.Name))
}

// useAgent starts or selects profile i without stopping other profiles.
func (h *agents) useAgent(i int) {
	a, ok := h.agentAt(i)
	if !ok {
		return
	}
	h.c.Close("agentProfiles")
	h.useProfile(a)
}

// useProfile goes to a's tab while it runs, and starts it otherwise: the dialog's Use and the
// strip's +.
func (h *agents) useProfile(a agentProfile) {
	// the terminal panel may have been rebuilt since the dialog listed it: an instance of the old
	// parent is retired here, never selected by its old tab on the new one
	h.syncInstances()
	if instance := h.instances[a.Codename]; instance != nil && instance.running {
		h.c.OpenDrawer("agent")
		h.c.Keep(h.selectInstance(instance))
		return
	}
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
