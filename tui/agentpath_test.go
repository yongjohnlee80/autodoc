package tui

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/yongjohnlee80/autodoc/internal/procenv"
)

// An agent's PATH has its own autodoc first: split on the OS's separator, the first folder holds the
// autodoc that runs the window's build on its config, found there as the OS finds a program (on Unix
// the launcher, through the agent folder's link; on Windows the build itself, its config through
// $AUTODOC_CONFIG). A crew agent's folder holds the window's scope in its name, so on Unix that
// folder can't be the entry itself.
func TestAnAgentsPathHasItsOwnAutodocFirst(t *testing.T) {
	a, c, _ := crewAgents(t, oneTwo)
	a.agentBinPath = fakeBuildCopy(t, filepath.Join(t.TempDir(), "build"))
	p, _ := a.profile("one")
	instance, err := a.startInstance(p)
	if err != nil {
		t.Fatal(err)
	}
	args, _, err := startedEnv(a, c, instance)
	if err != nil {
		t.Fatal(err)
	}
	path := pathOfCopy(args)
	first, _, _ := strings.Cut(path, string(os.PathListSeparator))
	// found as the OS finds a program on that PATH: the launcher on Unix, the build itself on Windows
	found, err := procenv.LookPath(procenv.Env(nil).With("PATH="+path), "autodoc")
	if err != nil || filepath.Dir(found) != first {
		t.Fatalf("the agent's PATH %q: its first folder %q; autodoc is found at %q, %v; want it there", path, first, found, err)
	}
	if b, _ := os.ReadFile(found); found != a.agentBin() && !strings.Contains(string(b), a.agentBin()) {
		t.Fatalf("autodoc at %s is neither the build %s nor a launcher of it: %q", found, a.agentBin(), b)
	}
	if !strings.Contains(instance.dir, crewFileName("autodoc:")) { // a scope by its file name
		t.Fatalf("the agent's folder %q holds no scope: the cell doesn't hold the case it is for", instance.dir)
	}
}

// The agent's PATH link is made again at each start: one left pointing elsewhere (another folder,
// one since removed) or at nothing is replaced, so the agent's first PATH folder is its own bin.
func TestAnAgentsStalePathLinkIsReplaced(t *testing.T) {
	for _, stale := range []string{"elsewhere", "nowhere"} {
		t.Run(stale, func(t *testing.T) {
			a, c, _ := crewAgents(t, oneTwo)
			p, _ := a.profile("one")
			dir := a.agentFolder(workspaceOf(c.ws), "one")
			link := a.agentPathLink(dir)
			if a.agentPathDir(dir) != link {
				t.Skip("this OS puts the build's folder on the agent's PATH, with no link")
			}
			if err := os.MkdirAll(filepath.Dir(link), 0o700); err != nil {
				t.Fatal(err)
			}
			target := filepath.Join(t.TempDir(), "gone")
			if stale == "elsewhere" {
				target = t.TempDir()
			}
			if err := os.Symlink(target, link); err != nil {
				t.Fatal(err)
			}
			instance, err := a.startInstance(p)
			if err != nil {
				t.Fatal(err)
			}
			if got, err := os.Readlink(link); err != nil || got != filepath.Join(instance.dir, "bin") {
				t.Fatalf("the agent's PATH link after its start: %q, %v; want %s", got, err, filepath.Join(instance.dir, "bin"))
			}
			args, _, err := startedEnv(a, c, instance)
			if err != nil {
				t.Fatal(err)
			}
			if first, _, _ := strings.Cut(pathOfCopy(args), string(os.PathListSeparator)); first != link {
				t.Fatalf("the agent's PATH starts %q, want its link %q", first, link)
			}
		})
	}
}
