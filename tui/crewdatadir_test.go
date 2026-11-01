package tui

import (
	"errors"
	"os"
	"path/filepath"
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
	a, c, _ := crewAgents(t, crewKinds)
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
	if _, _, err := startedEnv(a, c, instance); err != nil {
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

// TestFolderFilesStayInTheAgentsFolder: a setup's folder files are written into the agent's folder,
// 0600 (an MCP config names the session's context), and one whose path would leave it (.. or
// absolute) is refused, nothing written outside.
func TestFolderFilesStayInTheAgentsFolder(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "agent")
	if err := writeFolderFiles(dir, []crew.FolderFile{{Path: ".agents/mcp_config.json", Content: "{}"}}); err != nil {
		t.Fatal(err)
	}
	fi, err := os.Stat(filepath.Join(dir, ".agents", "mcp_config.json"))
	if err != nil || fi.Mode().Perm() != 0o600 {
		t.Fatalf("the folder file: %v %v, want it written 0600", fi, err)
	}
	for _, p := range []string{"../outside.json", filepath.Join(root, "abs.json")} {
		if err := writeFolderFiles(dir, []crew.FolderFile{{Path: p, Content: "{}"}}); !errors.Is(err, crew.ErrFolderFile) {
			t.Errorf("a folder file at %q: %v, want ErrFolderFile", p, err)
		}
	}
	for _, p := range []string{filepath.Join(root, "outside.json"), filepath.Join(root, "abs.json")} {
		if _, err := os.Stat(p); err == nil {
			t.Errorf("%s was written, outside the agent's folder", p)
		}
	}
}
