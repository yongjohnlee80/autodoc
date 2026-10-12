package tui

import (
	"errors"
	"strings"
	"testing"

	"github.com/yongjohnlee80/golib/parse/qml"
)

// Exercise profile persistence/validation without starting a terminal. Other
// panel services remain absent so an invalid target cannot accidentally spawn.
type identityHost struct {
	agentHost
	p       prefs
	state   map[string]any
	notes   []string
	stopErr error
}

func newIdentityHost(raw string) *identityHost {
	return &identityHost{p: prefs{values: map[string]string{prefAgentProfiles: raw}}, state: map[string]any{}}
}
func (c *identityHost) Prefs() prefs                          { return c.p }
func (c *identityHost) SetPref(name, value string)            { c.p.values[name] = value }
func (c *identityHost) Set(name string, value any)            { c.state[name] = value }
func (c *identityHost) SetFormText(name, value string)        { c.Set(name, value) }
func (*identityHost) Open(string)                             {}
func (c *identityHost) Notify(msg string)                     { c.notes = append(c.notes, msg) }
func (c *identityHost) CallView(string, string, ...any) error { return c.stopErr }
func (c *identityHost) Keep(err error) {
	if err != nil {
		c.notes = append(c.notes, err.Error())
	}
}

func TestLegacyCodenameCollisionsSurviveAnUnrelatedSave(t *testing.T) {
	c := newIdentityHost(`[{"name":"Claude Opus","command":"one"},{"name":"claude_opus","command":"two"}]`)
	a := newAgents(c, "", "")
	a.saveAgentProfile("Other", "other", "other")
	profiles := a.profiles()
	if len(profiles) != 3 || profiles[0].Command != "one" || profiles[1].Command != "two" || profiles[0].Codename != "claude-opus" || profiles[1].Codename != "claude-opus" {
		t.Fatalf("an unrelated save dropped or renamed colliding legacy profiles: %+v", profiles)
	}
	for _, p := range profiles[:2] {
		if why := a.profileProblem(p); !strings.Contains(why, "codename taken by") {
			t.Fatalf("%s's collision is not explained: %q", p.Name, why)
		}
		a.startAgent(p) // must refuse before any workspace/terminal operation
	}
	if len(c.notes) != 2 || a.agentRunning != "" {
		t.Fatalf("colliding profiles became start targets: notes %v, running %q", c.notes, a.agentRunning)
	}
	a.editingAgent = profiles[1].Name
	a.saveAgentProfile(profiles[1].Name, "two", "opus-two")
	for _, p := range a.profiles() {
		if why := a.profileProblem(p); why != "" {
			t.Fatalf("explicit repair left %s unavailable: %s", p.Name, why)
		}
	}
}

func TestProfileRenamesKeepOwnAndUntouchedLegacyCodenames(t *testing.T) {
	c := newIdentityHost(`[{"name":"Claude Opus","command":"one"},{"name":"Other Agent","command":"two"}]`)
	c.SetPref(prefAgentDefault, "Other Agent")
	a := newAgents(c, "", "")
	a.editingAgent = "Other Agent"
	a.saveAgent("Renamed", "two")
	profiles := a.profiles()
	if profiles[0].Codename != "claude-opus" || profiles[1].Codename != "other-agent" || a.defaultProfile() != "Renamed" {
		t.Fatalf("display rename changed an identity or lost the name-based default: %+v, default %q", profiles, a.defaultProfile())
	}
	if raw := c.p.Value(prefAgentProfiles); !strings.Contains(raw, `"codename":"other-agent"`) {
		t.Fatalf("edit did not persist its derived codename: %s", raw)
	}
}

func TestLegacyDuplicateLabelsAreNotDeliveryTargets(t *testing.T) {
	c := newIdentityHost(`[{"name":"Same","command":"one"},{"name":"Same","command":"two"}]`)
	a := newAgents(c, "", "")
	for _, p := range a.profiles() {
		if why := a.profileProblem(p); !strings.Contains(why, "codename taken by") {
			t.Fatalf("duplicate labels hid a duplicate delivery identity: %q", why)
		}
	}
}

func TestRunningOrphanReservesItsCodename(t *testing.T) {
	c := newIdentityHost(`[]`)
	a := newAgents(c, "", "")
	a.agentRunning, a.agentRunningCodename = "removed profile", "reserved"
	a.saveAgentProfile("Replacement", "replacement", "reserved")
	if len(a.profiles()) != 0 || !strings.Contains(c.state["App.agentFormError"].(string), "held by a running agent") {
		t.Fatal("a new profile reused a running orphan's codename")
	}
	a.agentRunning, a.agentRunningCodename = "", ""
	a.saveAgentProfile("Replacement", "replacement", "reserved")
	if len(a.profiles()) != 1 || a.profiles()[0].Codename != "reserved" {
		t.Fatal("a stopped orphan kept its old identity reserved")
	}
}

func TestLiveProfileCodenameCannotChangeUntilItStops(t *testing.T) {
	c := newIdentityHost(`[{"name":"Original","command":"one","codename":"original"}]`)
	a := newAgents(c, "", "")
	a.agentRunning, a.agentRunningCodename, a.editingAgent = "Original", "original", "Original"
	a.saveAgentProfile("Original", "one", "renamed")
	if a.profiles()[0].Codename != "original" || !strings.Contains(c.state["App.agentFormError"].(string), "stop the running agent") {
		t.Fatal("a live profile changed its delivery identity")
	}
	a.saveAgentProfile("Displayed differently", "one", "original")
	if a.profiles()[0].Name != "Displayed differently" || a.profiles()[0].Codename != "original" {
		t.Fatal("a live profile could not rename its display label without changing identity")
	}
}

func TestARefusedStopDoesNotReleaseTheRunningCodename(t *testing.T) {
	c := newIdentityHost(`[]`)
	c.stopErr = errors.New("stop refused")
	a := newAgents(c, "", "")
	a.agentRunning, a.agentRunningCodename = "removed profile", "reserved"
	if a.stopAgent() || a.agentRunningCodename != "reserved" || a.agentRunning == "" {
		t.Fatal("a failed stop released the running identity")
	}
	a.saveAgentProfile("Replacement", "replacement", "reserved")
	if len(a.profiles()) != 0 {
		t.Fatal("a failed stop allowed another profile to reuse its identity")
	}
	c.stopErr = nil
	if !a.stopAgent() || a.agentRunningCodename != "" {
		t.Fatal("a successful stop did not release the running identity")
	}
}

func TestExplicitCodenamesRefuseInvalidAndDuplicateTargets(t *testing.T) {
	c := newIdentityHost(`[{"name":"First","command":"one","codename":"taken"}]`)
	a := newAgents(c, "", "")
	for _, name := range []string{"UPPER", "two--words", "trailing-", "taken"} {
		a.saveAgentProfile("Second", "two", name)
		if len(a.profiles()) != 1 {
			t.Fatalf("invalid or duplicate identity %q was saved", name)
		}
	}
	a.saveAgentProfile("A New Agent", "three", "")
	if got := a.profiles()[1].Codename; got != "a-new-agent" {
		t.Fatalf("default codename %q", got)
	}
}

func TestProfileFormSaveKeepsAnExplicitDeliveryIdentity(t *testing.T) {
	c := newIdentityHost(`[]`)
	a := newAgents(c, "", "")
	args := []qml.SpecValue{
		{Kind: qml.SpecValueString, Raw: "Display Label"},
		{Kind: qml.SpecValueString, Raw: "command"},
		{Kind: qml.SpecValueString, Raw: "work-helper"},
	}
	if err := a.Commands()["App.saveAgentProfile"].handler("App.saveAgentProfile")(args); err != nil {
		t.Fatal(err)
	}
	if p := a.profiles()[0]; p.Name != "Display Label" || p.Command != "command" || p.Codename != "work-helper" {
		t.Fatalf("form save discarded or reordered its explicit identity: %+v", p)
	}
	a.editingAgent = "Display Label"
	a.saveAgent("Different Label", "command")
	if p := a.profiles()[0]; p.Codename != "work-helper" {
		t.Fatalf("a subsequent display edit changed the explicit identity: %+v", p)
	}
}
