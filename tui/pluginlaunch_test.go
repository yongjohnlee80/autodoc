package tui

import (
	"context"
	"maps"
	"strings"
	"testing"
)

// A plugin that starts with AutoDoc (start = "launch") asks once (docs/plugins.md#starting-with-autodoc).

// launchStore is a daemon whose store the TUIs below share, as restarts of one do, and a plugins
// folder with atstart: a service whose manifest says start = "launch", with a command.
func launchStore(t *testing.T) (*daemon, string) {
	t.Helper()
	dir := t.TempDir()
	installPlugin2(t, dir, "atstart", "service", "echo", "start = \"launch\"\n[[commands]]\nid = \"run\"\ntitle = \"Run\"\n")
	return startDaemonWith(t, "", map[string][]string{"kb": {"a.md", "a\n"}}, daemonOpts{prefs: maps.Clone(testPrefs)}), dir
}

const launchQuestion = "Start Atstart with AutoDoc?"

// launchPref is the store's value of name, "" when it has none.
func launchPref(t *testing.T, d *daemon, name string) string {
	t.Helper()
	all, err := d.db.Preferences(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	return all[name]
}

// decided waits until r's TUI has read the answers given and decided the launches: no question for
// atstart is open or waiting.
func decided(t *testing.T, r *running) {
	t.Helper()
	r.waitWithin(t, "the launches decided", daemonWait, func() bool {
		return onLoop(r, func() bool { return r.h.luaPrefsRead && !r.h.launchAsking["atstart"] && len(r.h.launchWaiting) == 0 })
	})
}

// Asked, the plugin is not running; Yes starts it, kept as the grant launch for plugin:atstart; a
// TUI started again on the store starts it without asking; the grant taken back (Agent
// permissions), the next start asks again.
func TestAPluginStartingWithAutoDocAsksOnceAndAYesIsKept(t *testing.T) {
	d, dir := launchStore(t)
	r, logs := pluginTUIOn(t, d, dir, "")
	r.s.WaitForText(t, launchQuestion)
	if onLoop(r, func() bool { return r.h.plugins.running["atstart"] != nil }) {
		t.Fatal("atstart runs before it was allowed")
	}
	onLoop(r, func() bool { r.h.asker.accepted(); return true })
	waitSaid(t, logs, "atstart", "open 0x0")
	r.waitWithin(t, "the grant in the store", daemonWait, func() bool {
		return strings.Contains(launchPref(t, d, prefLuaGrants), `"plugin:atstart":{"launch":true}`)
	})

	again, logs2 := pluginTUIOn(t, d, dir, "")
	waitSaid(t, logs2, "atstart", "open 0x0")
	if strings.Contains(again.s.String(), launchQuestion) {
		t.Fatalf("allowed before, and asked again:\n%s", again.s)
	}

	onLoop(again, func() bool { again.h.revokeLua(launchHolder("atstart"), []string{launchCapability}); return true })
	again.waitWithin(t, "the grant taken back in the store", daemonWait, func() bool {
		return !strings.Contains(launchPref(t, d, prefLuaGrants), `"launch"`)
	})
	third, _ := pluginTUIOn(t, d, dir, "")
	third.s.WaitForText(t, launchQuestion)
}

// No keeps the plugin from starting with AutoDoc, kept in tui.plugins.launchDeclined: a TUI started
// again on the store neither asks nor starts it, and its command still starts it by hand.
func TestAPluginRefusedDoesNotStartWithAutoDocButStartsByHand(t *testing.T) {
	d, dir := launchStore(t)
	r, _ := pluginTUIOn(t, d, dir, "")
	r.s.WaitForText(t, launchQuestion)
	onLoop(r, func() bool { r.h.asker.rejected(); return true })
	r.waitWithin(t, "the refusal in the store", daemonWait, func() bool {
		return launchPref(t, d, prefPluginLaunchDeclined) == `["atstart"]`
	})
	if onLoop(r, func() bool { return r.h.plugins.running["atstart"] != nil }) {
		t.Fatal("refused, and started")
	}
	if strings.Contains(launchPref(t, d, prefLuaGrants), "plugin:atstart") {
		t.Fatal("refused, and granted")
	}

	again, logs2 := pluginTUIOn(t, d, dir, "")
	decided(t, again)
	if onLoop(again, func() bool { return again.h.plugins.running["atstart"] != nil || again.h.asker.pending }) {
		t.Fatalf("refused before: started, or asked again:\n%s", again.s)
	}
	onLoop(again, func() bool { again.h.plugins.runPluginCommand("atstart", "run"); return true })
	waitSaid(t, logs2, "atstart", "command run")
}

// A card that goes away without an answer (another question in its place; the TUI quitting)
// remembers nothing: the plugin does not start, and the next start asks again.
func TestALaunchQuestionClosedUnansweredRemembersNothing(t *testing.T) {
	d, dir := launchStore(t)
	r, _ := pluginTUIOn(t, d, dir, "")
	r.s.WaitForText(t, launchQuestion)
	onLoop(r, func() bool { r.h.asker.ask(Question{Title: "something else?"}); return true })
	r.s.WaitForText(t, "something else?")
	decided(t, r)
	if onLoop(r, func() bool { return r.h.plugins.running["atstart"] != nil }) {
		t.Fatal("unanswered, and started")
	}
	if got := launchPref(t, d, prefPluginLaunchDeclined); got != "" && got != "[]" {
		t.Fatalf("unanswered, and remembered as refused: %s", got)
	}
	if strings.Contains(launchPref(t, d, prefLuaGrants), "plugin:atstart") {
		t.Fatal("unanswered, and granted")
	}
	again, _ := pluginTUIOn(t, d, dir, "")
	again.s.WaitForText(t, launchQuestion)
}

// The grant shows in Agent permissions with what it lets the plugin do; the question's text says
// that a plugin is a program of its own, with the daemon's whole socket.
func TestTheLaunchGrantSaysWhatItAllows(t *testing.T) {
	if got := (inEnglish{}).Trf(luaCapabilityText[launchCapability]); !strings.Contains(got, "start with AutoDoc") {
		t.Fatalf("the launch grant reads %q", got)
	}
	if got := (inEnglish{}).Trf("autodoc.question.pluginlaunch.it_is_a_program_of_its_own", "atstart"); !strings.Contains(got, "whole socket") ||
		!strings.Contains(got, "atstart") {
		t.Fatalf("the question's text reads %q", got)
	}
}
