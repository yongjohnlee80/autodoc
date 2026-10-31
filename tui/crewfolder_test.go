package tui

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// TestAnAgentWorksInItsOwnFolderUnderTheWindowsScope (docs/decisions/crew.md#the-session-context):
// while the window holds a crew scope, each agent starts in its own folder under it, the same
// across its sessions; without a scope, in the workspace's folder as before. A stopped tab whose
// folder the scope moved is not started there again: its process would run in the old scope's
// folder.
func TestAnAgentWorksInItsOwnFolderUnderTheWindowsScope(t *testing.T) {
	a, c := modelAgents(t)
	ws := wsInfo{name: "kb", root: t.TempDir()}
	shared := a.agentFolder(ws, "one")
	a.crew = &crewHost{}
	if got := a.agentFolder(ws, "one"); got != shared {
		t.Fatalf("before a scope is held: %s, want the workspace's %s", got, shared)
	}
	a.crew.scope, a.crew.epoch = "autodoc:0123456789abcdef", 1
	p, _ := a.profile("one")
	instance, err := a.startInstance(p)
	if err != nil {
		t.Fatal(err)
	}
	want := filepath.Join(agentBase(a.agentDir), safeName(c.ws.name), safeName("autodoc:0123456789abcdef"), safeName("one"))
	if got := c.panel.configs[instance.tab].Dir; got != want {
		t.Fatalf("the agent's folder: %s, want its own under the scope %s", got, want)
	}
	if _, err := os.Stat(filepath.Join(want, "CLAUDE.md")); err != nil {
		t.Fatalf("the guide was not written in the agent's folder: %v", err)
	}
	c.panel.exit(instance.tab, 0)
	if again, err := a.startInstance(p); err != nil || again != instance {
		t.Fatalf("a restart under the same scope: %v, want its tab started again", err)
	}
	c.panel.exit(instance.tab, 0)
	a.crew.scope = "autodoc:fedcba9876543210"
	if _, err := a.startInstance(p); !errors.Is(err, ErrAgentStale) {
		t.Fatalf("a start after the scope moved: %v, want ErrAgentStale (its tab runs in the old folder)", err)
	}
}

// TestClaudeSettingsMergeEveryHookAndKey: AutoDoc's hooks, one per event, and its top-level settings
// are added once to the user's settings, however often, every hook and key of theirs kept.
func TestClaudeSettingsMergeEveryHookAndKey(t *testing.T) {
	path := filepath.Join(t.TempDir(), ".claude", "settings.json")
	user := `{"model": "opus", "hooks": {"SessionStart": [{"hooks": [{"type": "command", "command": "mine"}]}]}}`
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(user), 0o600); err != nil {
		t.Fatal(err)
	}
	events := map[string]string{"SessionStart": "ours-start", "UserPromptSubmit": "ours-prompt"}
	for range 2 {
		if err := mergeClaudeSettings(path, claudeMerge{events: events, set: map[string]any{"crossSessionInbound": "accept"}}); err != nil {
			t.Fatal(err)
		}
	}
	b, _ := os.ReadFile(path)
	var s map[string]any
	if err := json.Unmarshal(b, &s); err != nil {
		t.Fatal(err)
	}
	if s["model"] != "opus" || s["crossSessionInbound"] != "accept" {
		t.Fatalf("top-level keys: %v", s)
	}
	for _, c := range []struct {
		text string
		n    int
	}{{`"mine"`, 1}, {`"ours-start"`, 1}, {`"ours-prompt"`, 1}} {
		if got := strings.Count(string(b), c.text); got != c.n {
			t.Errorf("%s %d times, want %d:\n%s", c.text, got, c.n, b)
		}
	}
}

// TestClaudeSettingsTakeOutAutoDocsOldHooks: a merge takes AutoDoc's hooks of an earlier write out
// of every event before it writes the ones it writes now, so a Claude Code that no longer knows an
// event is never left naming it: an event left with no hook goes, one with the user's hooks keeps
// them, and an old key AutoDoc set goes unless it is set again; a key the user set otherwise stays.
func TestClaudeSettingsTakeOutAutoDocsOldHooks(t *testing.T) {
	path := filepath.Join(t.TempDir(), ".claude", "settings.json")
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	user := `{"theme": "dark", "hooks": {"Stop": [{"hooks": [{"type": "command", "command": "mine"}]}]}}`
	if err := os.WriteFile(path, []byte(user), 0o600); err != nil {
		t.Fatal(err)
	}
	ours := []string{"ours-start", "ours-prompt"}
	unset := map[string]any{"crossSessionInbound": "accept"}
	full := claudeMerge{events: map[string]string{"SessionStart": "ours-start", "PermissionRequest": "ours-prompt", "Stop": "ours-prompt"},
		set: map[string]any{"crossSessionInbound": "accept"}, ours: ours, unset: unset}
	old := claudeMerge{events: map[string]string{"SessionStart": "ours-start"}, ours: ours, unset: unset}
	for _, m := range []claudeMerge{full, old} {
		if err := mergeClaudeSettings(path, m); err != nil {
			t.Fatal(err)
		}
	}
	b, _ := os.ReadFile(path)
	var s map[string]any
	if err := json.Unmarshal(b, &s); err != nil {
		t.Fatal(err)
	}
	hooks, _ := s["hooks"].(map[string]any)
	if _, named := hooks["PermissionRequest"]; named || strings.Contains(string(b), "ours-prompt") {
		t.Errorf("the old write's prompt hooks are still there:\n%s", b)
	}
	if strings.Count(string(b), `"mine"`) != 1 || strings.Count(string(b), `"ours-start"`) != 1 || s["theme"] != "dark" {
		t.Errorf("the user's hook and key, or this write's hook, are not there once:\n%s", b)
	}
	if _, set := s["crossSessionInbound"]; set {
		t.Errorf("crossSessionInbound outlived a write that doesn't set it:\n%s", b)
	}
	// a value the user chose is theirs
	if err := os.WriteFile(path, []byte(`{"crossSessionInbound": "ask"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := mergeClaudeSettings(path, old); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(path); !strings.Contains(string(b), `"ask"`) {
		t.Errorf("the user's own crossSessionInbound was taken out:\n%s", b)
	}
}

// TestAgentsNeverInheritACrewContext: an AutoDoc started by a crew agent doesn't hand that agent's
// session context to the agents it starts.
func TestAgentsNeverInheritACrewContext(t *testing.T) {
	if got := spawnerIdentity([]string{"AUTODOC_CREW_CONTEXT=/x", "HOME=/h"}); !slices.Contains(got, "AUTODOC_CREW_CONTEXT") {
		t.Fatalf("spawnerIdentity: %v, want AUTODOC_CREW_CONTEXT stripped", got)
	}
}
