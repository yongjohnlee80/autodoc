package tui

import (
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/yongjohnlee80/golib/parse/qml"
)

// Exercise profile persistence/validation without starting a terminal. Other
// panel services remain absent so an invalid target cannot accidentally spawn.
type identityHost struct {
	agentHost
	p                 prefs
	state             map[string]any
	notes             []string
	stopErr           error
	ws                wsInfo
	configuredCommand string
}

func newIdentityHost(raw string) *identityHost {
	return &identityHost{p: prefs{values: map[string]string{
		prefAgentProfiles: raw, prefAgentDefaultCodename: "",
	}}, state: map[string]any{}}
}
func (c *identityHost) Prefs() prefs                    { return c.p }
func (c *identityHost) SetPref(name, value string)      { c.p.values[name] = value }
func (c *identityHost) Set(name string, value any)      { c.state[name] = value }
func (c *identityHost) SetFormText(name, value string)  { c.Set(name, value) }
func (*identityHost) Open(string)                       {}
func (*identityHost) Close(string)                      {}
func (*identityHost) Focus(string) error                { return nil }
func (*identityHost) DaemonAddress() string             { return "unused" }
func (c *identityHost) ActiveWorkspace() (wsInfo, bool) { return c.ws, c.ws.name != "" }
func (c *identityHost) ConfigureTerminal(_, _, _ string, args ...string) error {
	if len(args) > 0 {
		c.configuredCommand = args[len(args)-1]
	}
	return nil
}
func (c *identityHost) Notify(msg string)                     { c.notes = append(c.notes, msg) }
func (c *identityHost) CallView(string, string, ...any) error { return c.stopErr }
func (c *identityHost) Keep(err error) {
	if err != nil {
		c.notes = append(c.notes, err.Error())
	}
}

func TestLegacyCodenamesAreUniqueDeterministicAndPersisted(t *testing.T) {
	raw := `[{"name":"Claude Opus","command":"one"},{"name":"claude_opus","command":"two"},{"name":"Claude Opus","command":"three"}]`
	c := newIdentityHost(raw)
	a := newAgents(c, "", "")
	before := a.profiles()
	if !reflect.DeepEqual(before, agentProfilesOf(raw)) || before[0].Codename != "claude-opus" || before[1].Codename != "claude-opus-2" || before[2].Codename != "claude-opus-3" {
		t.Fatalf("migration is not deterministic in stored order: %+v", before)
	}
	a.saveAgentProfile("Other", "other", "other")
	profiles := a.profiles()
	if len(profiles) != 4 || !reflect.DeepEqual(profiles[:3], before) {
		t.Fatalf("an unrelated save dropped or renumbered legacy profiles: %+v", profiles)
	}
	for _, p := range profiles[:3] {
		if why := a.profileProblem(p); why != "" {
			t.Fatalf("migrated profile is not a valid target: %q", why)
		}
	}
	if raw := c.p.Value(prefAgentProfiles); !strings.Contains(raw, `"codename":"claude-opus-3"`) {
		t.Fatal("first list save did not persist allocated identities")
	}
	a.startEditAgent(3)
	a.saveAgent("Other renamed", "other")
	if !reflect.DeepEqual(a.profiles()[:3], before) {
		t.Fatal("renaming an unrelated profile renumbered persisted suffixes")
	}
}

func TestProfileRenamesKeepOwnAndUntouchedLegacyCodenames(t *testing.T) {
	c := newIdentityHost(`[{"name":"Claude Opus","command":"one"},{"name":"Other Agent","command":"two"}]`)
	c.p = prefsFor(map[string]any{prefAgentProfiles: c.p.Value(prefAgentProfiles), prefAgentDefault: "Other Agent"}, false)
	a := newAgents(c, "", "")
	a.startEditAgent(1)
	a.saveAgent("Renamed", "two")
	profiles := a.profiles()
	if profiles[0].Codename != "claude-opus" || profiles[1].Codename != "other-agent" || a.defaultProfile() != "other-agent" {
		t.Fatalf("display rename changed an identity or lost the name-based default: %+v, default %q", profiles, a.defaultProfile())
	}
	if raw := c.p.Value(prefAgentProfiles); !strings.Contains(raw, `"codename":"other-agent"`) {
		t.Fatalf("edit did not persist its derived codename: %s", raw)
	}
}

func TestDuplicateLabelsEditEachExactProfileAndResolveTargetsByCodename(t *testing.T) {
	for _, row := range []int{0, 1} {
		c := newIdentityHost(`[{"name":"Same","command":"one"},{"name":"Same","command":"two"}]`)
		c.ws = wsInfo{name: "kb", root: t.TempDir()}
		a := newAgents(c, t.TempDir(), "")
		before := a.profiles()
		a.startEditAgent(row)
		a.saveAgentProfile("Same", before[row].Command+" edited", before[row].Codename)
		got := a.profiles()
		if got[1-row] != before[1-row] || got[row].Command != before[row].Command+" edited" {
			t.Fatalf("edit row%d overwrote another profile: %+v", row, got)
		}
		for _, p := range got {
			if why := a.profileProblem(p); why != "" {
				t.Fatalf("repeated label blocked a unique identity: %s", why)
			}
		}
		a.makeAgentDefault(row)
		p, ok := a.profile(a.defaultProfile())
		if !ok || p != got[row] || c.p.Value(prefAgentDefaultCodename) != got[row].Codename {
			t.Fatalf("default selected another Same label: %+v", p)
		}
		a.openAgentPanel()
		if c.configuredCommand != got[row].Command || a.agentRunning != got[row].Codename {
			t.Fatal("opening the default started another profile with the same label")
		}
		a.stopAgent()
		c.configuredCommand = ""
		a.useAgent(row)
		if c.configuredCommand != got[row].Command || a.agentRunning != got[row].Codename {
			t.Fatal("Use started another profile with the same label")
		}
		a.stopAgent()
		c.configuredCommand = ""
		a.agentRunning, a.agentRunningLabel = "running-orphan", "orphan"
		a.useAgent(row)
		if a.switchingAgent != got[row].Codename {
			t.Fatal("switch remembered a label instead of the selected identity")
		}
		a.switchAgentConfirmed()
		if c.configuredCommand != got[row].Command || a.agentRunning != got[row].Codename {
			t.Fatalf("switch started another Same label: command %q, identity %q", c.configuredCommand, a.agentRunning)
		}
	}
}

func TestGoneProfileActionsAndARefusedSwitchLeaveTheRunningAgentAlone(t *testing.T) {
	c := newIdentityHost(`[{"name":"Same","command":"one"}]`)
	a := newAgents(c, "", "")
	a.agentRunning, a.agentRunningLabel = "orphan", "orphan label"
	a.startEditAgent(-1)
	a.makeAgentDefault(9)
	a.useAgent(-1)
	a.switchingAgent = "gone"
	a.switchAgentConfirmed()
	if a.agentRunning != "orphan" || a.editingAgent != "" {
		t.Fatal("a stale row/identity changed the running agent")
	}
	c.stopErr = errors.New("stop dispatch refused")
	a.switchingAgent = "same"
	a.switchAgentConfirmed()
	if a.agentRunning != "orphan" || c.configuredCommand != "" {
		t.Fatal("a refused stop still configured the replacement")
	}
}

func TestLegacyDefaultNameMigratesWithoutConfusingAnotherCodename(t *testing.T) {
	c := newIdentityHost(`[{"name":"Foo","command":"one"},{"name":"foo","command":"two"},{"name":"foo-2","command":"three"}]`)
	c.p = prefsFor(map[string]any{prefAgentProfiles: c.p.Value(prefAgentProfiles), prefAgentDefault: "foo-2"}, false)
	a := newAgents(c, "", "")
	if p, ok := a.profile(a.defaultProfile()); !ok || p.Command != "three" {
		t.Fatalf("legacy name was mistaken for another allocated codename: %+v", p)
	}
	a.saveAgentProfile("Other", "other", "other")
	if a.defaultProfile() != "foo-2-2" || c.p.Value(prefAgentDefaultCodename) != "foo-2-2" {
		t.Fatal("list save did not persist the intended default identity")
	}
	a.makeAgentDefault(1)
	if p, ok := a.profile(a.defaultProfile()); !ok || p.Command != "two" {
		t.Fatal("canonical default still resolved through display labels")
	}
	a.removeAgentConfirmed(a.profiles()[1])
	if a.defaultProfile() != "" || c.p.Value(prefAgentDefaultCodename) != "" {
		t.Fatal("removing the default resurrected the legacy label")
	}
}

func TestMigrationDoesNotStealAnExplicitPersistedCodename(t *testing.T) {
	profiles := agentProfilesOf(`[{"name":"Taken","command":"legacy"},{"name":"Explicit","command":"saved","codename":"taken"}]`)
	if profiles[0].Codename != "taken-2" || profiles[1].Codename != "taken" {
		t.Fatalf("migration renumbered an already persisted identity: %+v", profiles)
	}
}

func TestAnEditedProfileSnapshotCannotOverwriteANewerRecord(t *testing.T) {
	c := newIdentityHost(`[{"name":"Same","command":"one"},{"name":"Same","command":"two"}]`)
	a := newAgents(c, "", "")
	a.startEditAgent(0)
	profiles := a.profiles()
	profiles[0].Command = "changed elsewhere"
	c.SetPref(prefAgentProfiles, agentProfilesJSON(profiles))
	a.saveAgentProfile("Edited", "old form", "same")
	if !reflect.DeepEqual(profiles, a.profiles()) || !strings.Contains(c.state["App.agentFormError"].(string), "profile changed") {
		t.Fatal("a stale form overwrote the changed record")
	}
}

func TestAgentDefaultMigrationDistinguishesMissingAndExplicitEmptyAtRestart(t *testing.T) {
	for _, tc := range []struct {
		name            string
		present         bool
		canonical, want string
	}{
		{"legacy absent", false, "", "a"},
		{"explicit clear", true, "", ""},
		{"canonical selection", true, "b", "b"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			raw := map[string]any{
				prefAgentProfiles: `[{"name":"A","command":"one"},{"name":"B","command":"two"}]`,
				prefAgentDefault:  "A",
			}
			if tc.present {
				raw[prefAgentDefaultCodename] = tc.canonical
			}
			values, migrated := migrateAgentDefault(raw)
			if migrated == tc.present {
				t.Fatal("migration ignored canonical key presence")
			}
			var rows prefs
			readRows(&rows, values, agentPrefRows(nil))
			for _, p := range []prefs{rows, prefsFor(raw, false), prefsFor(raw, true)} {
				c := newIdentityHost(`[]`)
				c.p = p
				if got := newAgents(c, "", "").defaultProfile(); got != tc.want {
					t.Fatalf("restart default %q, want %q", got, tc.want)
				}
			}
			if _, changed := migrateAgentDefault(values); changed {
				t.Fatal("canonical preferences migrated more than once")
			}
			if _, added := raw[prefAgentDefaultCodename]; added != tc.present {
				t.Fatal("migration mutated the raw store map")
			}
		})
	}
}

func TestRunningOrphanReservesItsCodename(t *testing.T) {
	c := newIdentityHost(`[]`)
	a := newAgents(c, "", "")
	a.agentRunning, a.agentRunningLabel = "reserved", "removed profile"
	a.saveAgentProfile("Replacement", "replacement", "reserved")
	if len(a.profiles()) != 0 || !strings.Contains(c.state["App.agentFormError"].(string), "held by a running agent") {
		t.Fatal("a new profile reused a running orphan's codename")
	}
	a.agentRunning, a.agentRunningLabel = "", ""
	a.saveAgentProfile("Replacement", "replacement", "reserved")
	if len(a.profiles()) != 1 || a.profiles()[0].Codename != "reserved" {
		t.Fatal("a stopped orphan kept its old identity reserved")
	}
}

func TestLiveProfileCodenameCannotChangeUntilItStops(t *testing.T) {
	c := newIdentityHost(`[{"name":"Original","command":"one","codename":"original"}]`)
	a := newAgents(c, "", "")
	a.agentRunning, a.agentRunningLabel = "original", "Original"
	a.startEditAgent(0)
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
	a.agentRunning, a.agentRunningLabel = "reserved", "removed profile"
	if a.stopAgent() || a.agentRunning != "reserved" {
		t.Fatal("a failed stop released the running identity")
	}
	a.saveAgentProfile("Replacement", "replacement", "reserved")
	if len(a.profiles()) != 0 {
		t.Fatal("a failed stop allowed another profile to reuse its identity")
	}
	c.stopErr = nil
	if !a.stopAgent() || a.agentRunning != "" {
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
	a.startEditAgent(0)
	a.saveAgent("Different Label", "command")
	if p := a.profiles()[0]; p.Codename != "work-helper" {
		t.Fatalf("a subsequent display edit changed the explicit identity: %+v", p)
	}
}
