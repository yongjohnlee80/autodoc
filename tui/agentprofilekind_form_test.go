package tui

import (
	"slices"
	"testing"
)

// TestTheFormSetsAProfilesKind: the form's dropdown lists the command's kind first, then each CLI;
// a pick is what Save keeps; editing shows the kind saved; a save that names no kind keeps the
// edited one's; a kind AutoDoc doesn't know is refused by the form and dropped from a hand-written
// profile; and a kind changed is a new launch (sameLaunch), so a running agent restarts on it.
func TestTheFormSetsAProfilesKind(t *testing.T) {
	a, c := modelAgents(t)
	a.startAddAgent()
	if n := a.kindList().Len(); n != len(agentKinds) {
		t.Fatalf("the dropdown has %d rows, want %d (agentKinds)", n, len(agentKinds))
	}
	if i, _ := c.state["App.agentKindIndex"].(int); i != 0 {
		t.Fatalf("a new profile's dropdown is at %d, want 0 (the command's)", i)
	}
	a.agentKindPicked(slices.Index(agentKinds, "claude"))
	a.saveAgentProfileWithEnv("Three", "claude-work", "three", "")
	p, ok := a.profile("three")
	if !ok || p.Kind != "claude" || p.kind() != "claude" {
		t.Fatalf("saved %+v %v, want kind claude", p, ok)
	}

	rows := a.profiles()
	a.startEditAgent(slices.IndexFunc(rows, func(q agentProfile) bool { return q.Codename == "three" }))
	if i, _ := c.state["App.agentKindIndex"].(int); agentKinds[i] != "claude" {
		t.Fatalf("editing it, the dropdown is at %q, want claude", agentKinds[i])
	}
	a.saveAgentProfile("Three", "claude-work", "three") // a caller that names no kind
	if p, _ := a.profile("three"); p.Kind != "claude" {
		t.Fatalf("a save naming no kind: %+v, want the edited profile's kind kept", p)
	}

	rows = a.profiles()
	a.startEditAgent(slices.IndexFunc(rows, func(q agentProfile) bool { return q.Codename == "three" }))
	a.agentKindPicked(0)
	a.saveAgentProfileWithEnv("Three", "claude-work", "three", "")
	if p, _ := a.profile("three"); p.Kind != "" || p.kind() != "claude-work" {
		t.Fatalf("set back to the command's: %+v (kind %q), want the command's name", p, p.kind())
	}

	before := a.profiles()
	if _, ok := a.storeAgentProfile("Four", "four", "four", "", "gemini"); ok {
		t.Fatal("a kind AutoDoc doesn't know was saved")
	}
	if why, _ := c.state["App.agentFormError"].(string); why != "not saved: gemini is not a kind AutoDoc knows" {
		t.Errorf("the form says %q", why)
	}
	if !slices.EqualFunc(a.profiles(), before, agentProfile.same) {
		t.Errorf("a refused kind changed the profiles: %+v", a.profiles())
	}
	if got := agentProfilesOf(`[{"name":"Five","command":"five","kind":"gemini"}]`); len(got) != 1 || got[0].Kind != "" {
		t.Errorf("a hand-written unknown kind: %+v, want it dropped (the command's)", got)
	}

	one := agentProfile{Name: "One", Command: "claude-work", Codename: "one"}
	set := one
	set.Kind = "claude"
	if one.sameLaunch(set) {
		t.Error("a kind changed is the same launch: a running agent would keep its old setup")
	}
}
