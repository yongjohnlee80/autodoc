package tui

import (
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// claudeHookCommands are the hook commands of the Claude settings in dir, by event.
func claudeHookCommands(t *testing.T, dir string) map[string][]string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(dir, ".claude", "settings.json"))
	if err != nil {
		t.Fatal(err)
	}
	var s struct {
		Hooks map[string][]struct {
			Hooks []struct{ Command string }
		}
	}
	if err := json.Unmarshal(b, &s); err != nil {
		t.Fatal(err)
	}
	out := map[string][]string{}
	for event, groups := range s.Hooks {
		for _, g := range groups {
			for _, h := range g.Hooks {
				out[event] = append(out[event], h.Command)
			}
		}
	}
	return out
}

// A hook of an earlier build (the agent folder's wrapper, or a build since moved) is taken out when
// this build writes its own: one program word and AutoDoc's command after it are AutoDoc's.
func TestAnEarlierBuildsHooksAreTakenOut(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, ".claude", "settings.json")
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	old := shellQuote(filepath.Join(dir, "bin", "autodoc"))
	earlier := `{"hooks":{"UserPromptSubmit":[{"hooks":[{"type":"command","command":"` + strings.ReplaceAll(old, `\`, `\\`) + ` --workspace-notice"},{"type":"command","command":"mine --workspace-notice --x"}]}]}}`
	if err := os.WriteFile(path, []byte(earlier), 0o600); err != nil {
		t.Fatal(err)
	}
	build := shellQuote(filepath.Join(t.TempDir(), "autodoc"))
	if err := mergeClaudeHook(path, build+" --workspace-notice"); err != nil {
		t.Fatal(err)
	}
	got := claudeHookCommands(t, dir)["UserPromptSubmit"]
	if !slices.Contains(got, build+" --workspace-notice") || slices.Contains(got, old+" --workspace-notice") || !slices.Contains(got, "mine --workspace-notice --x") {
		t.Errorf("the notice hooks %q: want this build's, the user's own kept, the earlier build's gone", got)
	}
}
