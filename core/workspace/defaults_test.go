package workspace_test

import (
	"testing"

	"github.com/yongjohnlee80/autodoc/core/config"
	"github.com/yongjohnlee80/autodoc/core/workspace"
)

// TestTheDefaultsLeaveDependenciesOut: a workspace with the default patterns indexes its notes but
// not a node_modules at any depth, nor .git — and treats those directories as excluded, so they are
// never walked or watched.
func TestTheDefaultsLeaveDependenciesOut(t *testing.T) {
	m := workspace.NewMatcher(config.DefaultInclude, config.DefaultExclude)
	for p, want := range map[string]bool{
		"notes/a.md":                         true,
		"notes/a.txt":                        true,
		"notes/a.yaml":                       true,
		"notes/a.yml":                        true,
		"README.md":                          true,
		"node_modules/pkg/README.md":         false,
		"app/web/node_modules/dep/README.md": false,
		".git/info/a.md":                     false,
	} {
		if got := m.Match(p); got != want {
			t.Errorf("Match(%q) = %v, want %v", p, got, want)
		}
	}
	for p, want := range map[string]bool{
		"node_modules":         true,
		"app/web/node_modules": true,
		".git":                 true,
		"app/web":              false,
		"node_modules_notes":   false,
	} {
		if got := m.Excluded(p); got != want {
			t.Errorf("Excluded(%q) = %v, want %v", p, got, want)
		}
	}
}
