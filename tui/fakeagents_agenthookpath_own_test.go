package tui

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

// Copies of the agents' test fakes agenthookpath_own_test.go declares, for the host's cells.

// claudeHookCommands are the hook commands of the Claude settings in dir, by event.
func claudeHookCommandsCopy(t *testing.T, dir string) map[string][]string {
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
