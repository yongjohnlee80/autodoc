package tui

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/yongjohnlee80/autodoc/core/crew"
)

// TestACrewOpenCodeAgentWritesNothingIntoTheWorkingFolder: a crew OpenCode agent started from the
// window has its setup keep AutoDoc's plugin in the config's data folder (Options.DataDir), and
// nothing lands in the window's working folder, which may be the user's project.
func TestACrewOpenCodeAgentWritesNothingIntoTheWorkingFolder(t *testing.T) {
	cwd := t.TempDir()
	t.Chdir(cwd)
	a, c, _ := crewAgents(t, crewKinds)
	a.pluginEnv = func() (string, error) { return `OPENCODE_CONFIG_CONTENT={}`, nil }
	a.dataDir = t.TempDir()
	p, _ := a.profile("opencode-one")
	instance, err := a.startInstance(p)
	if err != nil {
		t.Fatal(err)
	}
	awaitStart(t, c, instance) // an OpenCode agent's session is published once its plugin's env is in
	if _, _, err := startedEnv(a, c, instance); err != nil {
		t.Fatal(err)
	}
	if entries, _ := os.ReadDir(cwd); len(entries) != 0 {
		var names []string
		for _, e := range entries {
			names = append(names, e.Name())
		}
		t.Errorf("the window's working folder holds %q after an OpenCode agent's start, want nothing", names)
	}
	if _, err := os.Stat(filepath.Join(a.dataDir, "opencode", crew.OpenCodePluginName)); err != nil {
		t.Errorf("AutoDoc's OpenCode plugin isn't in the config's data folder: %v", err)
	}
}
