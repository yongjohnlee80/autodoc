package agents

import (
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/yongjohnlee80/autodoc/tui/feature"
	"github.com/yongjohnlee80/golib/parse/qml"
	"github.com/yongjohnlee80/golib/tui/i18n"
)

// Exercise profile persistence/validation without starting a terminal. Other
// panel services remain absent so an invalid target cannot accidentally spawn.
type identityHost struct {
	AgentHost
	p                 fakePrefs
	state             map[string]any
	notes             []string
	stopErr           error
	ws                feature.Workspace
	configuredCommand string
	panel             *modelPanel
	postMu            sync.Mutex // a held removal's retirement posts from its own goroutine
	posts             []func()
	afters            []func()
	delays            []time.Duration
	questions         []feature.Question
	epoch             int
	closed            []string // the drawers CloseDrawer closed, in order
	opened            []string // the drawers OpenDrawer opened, in order
}

// testEnglish is id's English, or id when the catalog has none: the catalog the host's words come
// from (tui/qml/i18n), read from the module's files, as the agents' fake host says them.
func testEnglish(id string) string {
	if t, ok := testCatalog().Lookup(i18n.English, id); ok {
		return t
	}
	return id
}

// testCatalog is the English catalog, found from the module's root above the test's folder.
var testCatalog = sync.OnceValue(func() *i18n.Set {
	dir, err := os.Getwd()
	if err != nil {
		panic(err)
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			break
		}
		if filepath.Dir(dir) == dir {
			panic("the agents' cells: no go.mod above the test's folder")
		}
		dir = filepath.Dir(dir)
	}
	s := &i18n.Set{}
	if err := s.LoadDir(os.DirFS(filepath.Join(dir, "tui", "qml")), "i18n", "autodoc"); err != nil {
		panic("the agents' cells: the English catalog: " + err.Error())
	}
	return s
})

// fakePrefs is the preferences a fake host answers (feature.PrefsView): each value by its name, no
// panel's geometry.
type fakePrefs struct{ values map[string]string }

func (p fakePrefs) Value(name string) string { return p.values[name] }

func (fakePrefs) PanelGeometry(string, string) (int, int) { return 0, 0 }

func newIdentityHost(raw string) *identityHost {
	return &identityHost{p: fakePrefs{values: map[string]string{
		PrefAgentProfiles: raw, PrefAgentDefaultCodename: "",
	}}, state: map[string]any{}}
}
func (c *identityHost) Prefs() feature.PrefsView       { return c.p }
func (c *identityHost) SetPref(name, value string)     { c.p.values[name] = value }
func (c *identityHost) Set(name string, value any)     { c.state[name] = value }
func (c *identityHost) Say(msg string)                 { c.state["App.status"] = msg }
func (c *identityHost) SetFormText(name, value string) { c.Set(name, value) }
func (*identityHost) Open(string)                      {}
func (*identityHost) Close(string)                     {}
func (*identityHost) Focus(string) error               { return nil }
func (*identityHost) DaemonAddress() string            { return "unused" }
func (c *identityHost) ActiveWorkspace() (feature.Workspace, bool) {
	return c.ws, c.ws.Name != ""
}
func (c *identityHost) ActiveWorkspaceInfo() (feature.Workspace, bool) {
	return c.ws, c.ws.Name != ""
}
func (c *identityHost) ConfigureTerminal(_, _, _ string, args ...string) error {
	if len(args) > 0 {
		c.configuredCommand = args[len(args)-1]
	}
	return nil
}
func (c *identityHost) Notify(msg string) { c.notes = append(c.notes, msg) }
func (*identityHost) Tr(id string) string { return testEnglish(id) }
func (f *identityHost) Trf(id string, args ...any) string {
	return feature.Fill(testEnglish(id), args...)
}
func (c *identityHost) WidgetsChanged()                       {}
func (c *identityHost) CallView(string, string, ...any) error { return c.stopErr }
func (c *identityHost) Keep(err error) {
	if err != nil {
		c.notes = append(c.notes, err.Error())
	}
}

func TestLegacyCodenamesAreUniqueDeterministicAndPersisted(t *testing.T) {
	raw := `[{"name":"Claude Opus","command":"one"},{"name":"claude_opus","command":"two"},{"name":"Claude Opus","command":"three"}]`
	c := newIdentityHost(raw)
	a := testAgents(c, "", "")
	before := a.Profiles()
	if !reflect.DeepEqual(before, AgentProfilesOf(raw)) || before[0].Codename != "claude-opus" || before[1].Codename != "claude-opus-2" || before[2].Codename != "claude-opus-3" {
		t.Fatalf("migration is not deterministic in stored order: %+v", before)
	}
	a.SaveAgentProfile("Other", "other", "other")
	profiles := a.Profiles()
	if len(profiles) != 4 || !reflect.DeepEqual(profiles[:3], before) {
		t.Fatalf("an unrelated save dropped or renumbered legacy profiles: %+v", profiles)
	}
	for _, p := range profiles[:3] {
		if why := a.profileProblem(p); why != "" {
			t.Fatalf("migrated profile is not a valid target: %q", why)
		}
	}
	if raw := c.p.Value(PrefAgentProfiles); !strings.Contains(raw, `"codename":"claude-opus-3"`) {
		t.Fatal("first list save did not persist allocated identities")
	}
	a.StartEditAgent(3)
	a.SaveAgent("Other renamed", "other")
	if !reflect.DeepEqual(a.Profiles()[:3], before) {
		t.Fatal("renaming an unrelated profile renumbered persisted suffixes")
	}
}

// ranAs is command as the agent's shell is given it, its last argument (agentArgv): what
// ConfigureTerminal records. The line itself under /bin/sh; under pwsh behind its Standard
// argument passing (internal/usershell's Run).
func ranAs(a *Agents, command string) string {
	_, args := agentArgv(a.seams.Env(), command)
	return args[len(args)-1]
}

func TestDuplicateLabelsEditEachExactProfileAndResolveTargetsByCodename(t *testing.T) {
	for _, row := range []int{0, 1} {
		c := newIdentityHost(`[{"name":"Same","command":"one"},{"name":"Same","command":"two"}]`)
		c.ws = feature.Workspace{Name: "kb", Root: t.TempDir()}
		a := testAgents(c, t.TempDir(), "")
		before := a.Profiles()
		a.StartEditAgent(row)
		a.SaveAgentProfile("Same", before[row].Command+" edited", before[row].Codename)
		got := a.Profiles()
		if !got[1-row].Same(before[1-row]) || got[row].Command != before[row].Command+" edited" {
			t.Fatalf("edit row%d overwrote another profile: %+v", row, got)
		}
		for _, p := range got {
			if why := a.profileProblem(p); why != "" {
				t.Fatalf("repeated label blocked a unique identity: %s", why)
			}
		}
		a.MakeAgentDefault(row)
		p, ok := a.Profile(a.DefaultProfile())
		if !ok || !p.Same(got[row]) || c.p.Value(PrefAgentDefaultCodename) != got[row].Codename {
			t.Fatalf("default selected another Same label: %+v", p)
		}
		a.openAgentPanel()
		if c.configuredCommand != ranAs(a, got[row].Command) || a.agentRunning != got[row].Codename {
			t.Fatal("opening the default started another profile with the same label")
		}
		a.stopAgent()
		c.configuredCommand = ""
		a.UseAgent(row)
		if c.configuredCommand != ranAs(a, got[row].Command) || a.agentRunning != got[row].Codename {
			t.Fatal("Use started another profile with the same label")
		}
		a.stopAgent()
		c.configuredCommand = ""
		retainModelInstance(a, c, "running-orphan", "orphan")
		a.UseAgent(row)
		if !a.instances["running-orphan"].running {
			t.Fatal("starting a saved profile stopped its orphan peer")
		}
		a.switchingAgent = got[row].Codename
		a.switchAgentConfirmed() // retained compatibility command selects without stopping peers
		if c.configuredCommand != ranAs(a, got[row].Command) || a.agentRunning != got[row].Codename {
			t.Fatalf("switch started another Same label: command %q, identity %q", c.configuredCommand, a.agentRunning)
		}
	}
}

func TestGoneProfileActionsLeaveOtherRunningInstancesAlone(t *testing.T) {
	c := newIdentityHost(`[{"name":"Same","command":"one"}]`)
	a := testAgents(c, "", "")
	retainModelInstance(a, c, "orphan", "orphan label")
	a.StartEditAgent(-1)
	a.MakeAgentDefault(9)
	a.UseAgent(-1)
	a.switchingAgent = "gone"
	a.switchAgentConfirmed()
	if a.agentRunning != "orphan" || a.editingAgent != "" {
		t.Fatal("a stale row/identity changed the running agent")
	}
}

func TestMigrationDoesNotStealAnExplicitPersistedCodename(t *testing.T) {
	profiles := AgentProfilesOf(`[{"name":"Taken","command":"legacy"},{"name":"Explicit","command":"saved","codename":"taken"}]`)
	if profiles[0].Codename != "taken-2" || profiles[1].Codename != "taken" {
		t.Fatalf("migration renumbered an already persisted identity: %+v", profiles)
	}
}

func TestAnEditedProfileSnapshotCannotOverwriteANewerRecord(t *testing.T) {
	c := newIdentityHost(`[{"name":"Same","command":"one"},{"name":"Same","command":"two"}]`)
	a := testAgents(c, "", "")
	a.StartEditAgent(0)
	profiles := a.Profiles()
	profiles[0].Command = "changed elsewhere"
	c.SetPref(PrefAgentProfiles, AgentProfilesJSON(profiles))
	a.SaveAgentProfile("Edited", "old form", "same")
	if !reflect.DeepEqual(profiles, a.Profiles()) || !strings.Contains(c.state["App.agentFormError"].(string), "profile changed") {
		t.Fatal("a stale form overwrote the changed record")
	}
}

func TestRunningOrphanReservesItsCodename(t *testing.T) {
	c := newIdentityHost(`[]`)
	a := testAgents(c, "", "")
	retainModelInstance(a, c, "reserved", "removed profile")
	a.SaveAgentProfile("Replacement", "replacement", "reserved")
	if len(a.Profiles()) != 0 || !strings.Contains(c.state["App.agentFormError"].(string), "held by a running agent") {
		t.Fatal("a new profile reused a running orphan's codename")
	}
	if err := a.RemoveInstance("reserved"); err != nil {
		t.Fatal(err)
	}
	a.SaveAgentProfile("Replacement", "replacement", "reserved")
	if len(a.Profiles()) != 1 || a.Profiles()[0].Codename != "reserved" {
		t.Fatal("a stopped orphan kept its old identity reserved")
	}
}

func TestLiveProfileCodenameCannotChangeUntilItStops(t *testing.T) {
	c := newIdentityHost(`[{"name":"Original","command":"one","codename":"original"}]`)
	a := testAgents(c, "", "")
	retainModelInstance(a, c, "original", "Original")
	a.StartEditAgent(0)
	a.SaveAgentProfile("Original", "one", "renamed")
	if a.Profiles()[0].Codename != "original" || !strings.Contains(c.state["App.agentFormError"].(string), "stop the running agent") {
		t.Fatal("a live profile changed its delivery identity")
	}
	a.SaveAgentProfile("Displayed differently", "one", "original")
	if a.Profiles()[0].Name != "Displayed differently" || a.Profiles()[0].Codename != "original" {
		t.Fatal("a live profile could not rename its display label without changing identity")
	}
}

func TestARefusedStopDoesNotReleaseTheRunningCodename(t *testing.T) {
	c := newIdentityHost(`[]`)
	c.stopErr = errors.New("stop refused")
	a := testAgents(c, "", "")
	retainModelInstance(a, c, "reserved", "removed profile")
	if a.stopAgent() || a.agentRunning != "reserved" {
		t.Fatal("a failed stop released the running identity")
	}
	a.SaveAgentProfile("Replacement", "replacement", "reserved")
	if len(a.Profiles()) != 0 {
		t.Fatal("a failed stop allowed another profile to reuse its identity")
	}
	c.stopErr = nil
	if !a.stopAgent() || a.agentRunning != "" {
		t.Fatal("a successful stop did not release the running identity")
	}
}

func TestExplicitCodenamesRefuseInvalidAndDuplicateTargets(t *testing.T) {
	c := newIdentityHost(`[{"name":"First","command":"one","codename":"taken"}]`)
	a := testAgents(c, "", "")
	for _, name := range []string{"UPPER", "two--words", "trailing-", "taken"} {
		a.SaveAgentProfile("Second", "two", name)
		if len(a.Profiles()) != 1 {
			t.Fatalf("invalid or duplicate identity %q was saved", name)
		}
	}
	a.SaveAgentProfile("A New Agent", "three", "")
	if got := a.Profiles()[1].Codename; got != "a-new-agent" {
		t.Fatalf("default codename %q", got)
	}
}

func TestProfileFormSaveKeepsAnExplicitDeliveryIdentity(t *testing.T) {
	c := newIdentityHost(`[]`)
	a := testAgents(c, "", "")
	args := []qml.SpecValue{
		{Kind: qml.SpecValueString, Raw: "Display Label"},
		{Kind: qml.SpecValueString, Raw: "command"},
		{Kind: qml.SpecValueString, Raw: "work-helper"},
	}
	if err := a.Commands()["App.saveAgentProfile"].Handler("App.saveAgentProfile")(args); err != nil {
		t.Fatal(err)
	}
	if p := a.Profiles()[0]; p.Name != "Display Label" || p.Command != "command" || p.Codename != "work-helper" {
		t.Fatalf("form save discarded or reordered its explicit identity: %+v", p)
	}
	a.StartEditAgent(0)
	a.SaveAgent("Different Label", "command")
	if p := a.Profiles()[0]; p.Codename != "work-helper" {
		t.Fatalf("a subsequent display edit changed the explicit identity: %+v", p)
	}
}
