package tui

import (
	"os"
	"testing"

	"github.com/yongjohnlee80/autodoc/core/crew"
)

// TestACrewOpenCodeAgentWritesNothingIntoTheWorkingFolder: a crew OpenCode agent started from the
// window has its setup asked for its own folder (crew setup, the agent's build, which keeps AutoDoc's
// plugin in the config's data folder), and nothing lands in the window's working folder, which may
// be the user's project.
func TestACrewOpenCodeAgentWritesNothingIntoTheWorkingFolder(t *testing.T) {
	cwd := t.TempDir()
	t.Chdir(cwd)
	a, c, _ := crewAgents(t, crewKindsCopy)
	run := a.setupRun
	var asked string
	a.setupRun = func(kind, dir string, environ []string) (crew.Setup, error) {
		asked = dir
		return run(kind, dir, environ)
	}
	p, _ := a.profile("opencode-one")
	instance, err := a.startInstance(p)
	if err != nil {
		t.Fatal(err)
	}
	awaitStart(t, c, instance)
	if _, _, err := startedEnvCopy(a, c, instance); err != nil {
		t.Fatal(err)
	}
	if asked != instance.dir {
		t.Errorf("its setup was asked for %q, want its own folder %q", asked, instance.dir)
	}
	if entries, _ := os.ReadDir(cwd); len(entries) != 0 {
		var names []string
		for _, e := range entries {
			names = append(names, e.Name())
		}
		t.Errorf("the window's working folder holds %q after an OpenCode agent's start, want nothing", names)
	}
}
