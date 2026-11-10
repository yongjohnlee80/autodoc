package tui

import (
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/yongjohnlee80/autodoc/tui/feature"
)

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
	// AutoDoc's commands by their tails, whichever build ran them: this write's is another program
	// (a build moved, upgraded or once a wrapper), and still the earlier write's are taken out
	ours := []string{" ours-start", " ours-prompt"}
	unset := map[string]any{"crossSessionInbound": "accept"}
	full := claudeMerge{events: map[string]string{"SessionStart": "'/old/autodoc' ours-start", "PermissionRequest": "'/old/autodoc' ours-prompt", "Stop": "'/old/autodoc' ours-prompt"},
		set: map[string]any{"crossSessionInbound": "accept"}, ours: ours, unset: unset}
	old := claudeMerge{events: map[string]string{"SessionStart": "/new/autodoc ours-start"}, ours: ours, unset: unset}
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
	if strings.Count(string(b), `"mine"`) != 1 || strings.Count(string(b), "ours-start") != 1 || strings.Contains(string(b), "/old/autodoc") || s["theme"] != "dark" {
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
	if got := feature.SpawnerIdentity([]string{"AUTODOC_CREW_CONTEXT=/x", "HOME=/h"}); !slices.Contains(got, "AUTODOC_CREW_CONTEXT") {
		t.Fatalf("spawnerIdentity: %v, want AUTODOC_CREW_CONTEXT stripped", got)
	}
}
