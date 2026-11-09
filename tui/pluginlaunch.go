package tui

import (
	"encoding/json"
	"slices"
)

// PLUGINS STARTED WITH AUTODOC (docs/plugins.md#starting-with-autodoc): a service whose manifest
// says start = "launch" is a program of its own with the daemon's whole socket, so AutoDoc asks
// once before it starts one with the TUI, and remembers the answer. A Yes is a grant, the
// capability "launch" held by plugin:<name> in lua.grants, where Agent permissions shows it and can
// take it back (the next start asks again). A No is kept in tui.plugins.launchDeclined: the
// plugin is not started with AutoDoc, and the Plugins menu still starts it by hand. A card another
// question displaces is asked again once the card is free (askWhenFree); one that goes away
// unanswered (closed, the TUI quitting) remembers nothing and asks at the next start. The question
// waits for the answers already given to be read, as a Lua grant's does (luagate.go).

const (
	// launchCapability is the grant a plugin's start with AutoDoc is (lua.grants, plugin:<name>)
	launchCapability = "launch"
	// prefPluginLaunchDeclined is the plugins whose start with AutoDoc the user refused: a JSON
	// list of names. Outside tui.plugin.*, which a plugin may set itself (SetPluginPref).
	prefPluginLaunchDeclined = "tui.plugins.launchDeclined"
)

// launchAsk is one plugin's start with AutoDoc, waiting for its answer.
type launchAsk struct {
	name, title string
	start       func()
}

// launchHolder is the grants' holder a plugin's start is: the one its Lua package's grants have.
func launchHolder(name string) string { return "plugin:" + name }

// Prefs is the refused plugins' list, as the store keeps it (no chooser: the question sets it), and
// whether a plugin's pixel surface shows in a terminal (pluginsurface_cells.go): on by default.
func (pl *plugins) Prefs() []PrefSpec {
	return []PrefSpec{ValuePref(prefPluginLaunchDeclined, "[]", nil),
		ValuePref(prefPluginPixels, "true", []string{"true", "false"}).chooser("App.pluginPixelsIndex", "App.setPluginPixelsIndex").
			then(func(PrefsView) { pl.repaintCards() })}
}

// LaunchConsent is launchConsent, for the plugins (pluginHost).
func (c core) LaunchConsent(name, title string, start func()) { c.h.launchConsent(name, title, start) }

// launchConsent queues name's start with AutoDoc, decided once the answers given are read.
func (h *Host) launchConsent(name, title string, start func()) {
	if h.launchAsking[name] || slices.ContainsFunc(h.launchWaiting, func(a launchAsk) bool { return a.name == name }) {
		return // asked already, or waiting: the plugins read anew ask once
	}
	h.launchWaiting = append(h.launchWaiting, launchAsk{name, title, start})
	h.decideLaunches()
}

// decideLaunches starts each waiting plugin allowed before, leaves each refused before, and asks
// about the rest, one card at a time after any question already open (askWhenFree). Before the
// answers are read it waits: loadPrefs calls it again.
func (h *Host) decideLaunches() {
	if !h.luaPrefsRead {
		return
	}
	waiting := h.launchWaiting
	h.launchWaiting = nil
	declined := launchDeclinedOf(h.prefs.Value(prefPluginLaunchDeclined))
	for _, a := range waiting {
		switch {
		case h.prefs.luaGrants[launchHolder(a.name)][launchCapability]:
			a.start()
		case slices.Contains(declined, a.name):
		default:
			h.askLaunch(a)
		}
	}
}

// askLaunch asks whether a's plugin starts with AutoDoc: Yes grants it and starts it, No remembers
// the refusal; neither ends with the card closed another way.
func (h *Host) askLaunch(a launchAsk) {
	if h.launchAsking == nil {
		h.launchAsking = map[string]bool{}
	}
	h.launchAsking[a.name] = true
	done := func() { delete(h.launchAsking, a.name) }
	h.asker.askWhenFree(Question{
		Title: h.trf("autodoc.question.pluginlaunch.start_the_plugin_with_autodoc", a.title),
		Text:  h.trf("autodoc.question.pluginlaunch.it_is_a_program_of_its_own", a.name),
		Yes:   h.tr("autodoc.question.pluginlaunch.yes_start_it"),
		No:    h.tr("autodoc.question.pluginlaunch.no_not_with_autodoc"),
		OnYes: func() {
			done()
			h.grantLua(launchHolder(a.name), []string{launchCapability})
			a.start()
		},
		OnNo:     func() { h.declineLaunch(a.name) },
		OnCancel: done,
	})
}

// declineLaunch remembers that name does not start with AutoDoc.
func (h *Host) declineLaunch(name string) {
	declined := launchDeclinedOf(h.prefs.Value(prefPluginLaunchDeclined))
	if slices.Contains(declined, name) {
		return
	}
	b, _ := json.Marshal(append(declined, name))
	core{h}.SetPref(prefPluginLaunchDeclined, string(b))
}

// launchDeclinedOf reads the preference's JSON list; anything else is none refused.
func launchDeclinedOf(s string) []string {
	var names []string
	if json.Unmarshal([]byte(s), &names) != nil {
		return nil
	}
	return names
}
