package tui

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/yongjohnlee80/autodoc/core/crew"
	"github.com/yongjohnlee80/autodoc/internal/procenv"
)

// pathOf is the PATH an agent's start sets (its argv's PATH=), "" for none.
func pathOf(args []string) string {
	path := ""
	for _, a := range args {
		if v, ok := strings.CutPrefix(a, "PATH="); ok {
			path = v // the last one is what the agent gets
		}
	}
	return path
}

// An agent's PATH has its own autodoc first: split on the OS's separator, the first folder holds the
// autodoc that runs the window's build on its config, found there as the OS finds a program (on Unix
// the launcher, through the agent folder's link; on Windows the build itself, its config through
// $AUTODOC_CONFIG). A crew agent's folder holds the window's scope in its name, so on Unix that
// folder can't be the entry itself.
func TestAnAgentsPathHasItsOwnAutodocFirst(t *testing.T) {
	a, c, _ := crewAgents(t, oneTwo)
	a.agentBinPath = fakeBuild(t, filepath.Join(t.TempDir(), "build"))
	p, _ := a.profile("one")
	instance, err := a.startInstance(p)
	if err != nil {
		t.Fatal(err)
	}
	args, _, err := startedEnv(a, c, instance)
	if err != nil {
		t.Fatal(err)
	}
	path := pathOf(args)
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

// A folder for the agents whose own path holds the PATH separator (':', or ';' on Windows, where a
// name can't hold ':') can't be put on a PATH whole: the start is refused, saying why, rather than
// started on a PATH whose autodoc is someone else's.
func TestAnAgentIsNotStartedOnABrokenPath(t *testing.T) {
	c := newIdentityHost(oneTwo)
	c.ws = wsInfo{id: 7, name: "kb", root: t.TempDir()}
	a := testAgents(c, filepath.Join(t.TempDir(), "agents"+string(os.PathListSeparator)+"odd"), "")
	// the PATH entry is the agents' (Unix) or the build's folder (Windows): each holds the separator
	a.agentBinPath = fakeBuild(t, filepath.Join(t.TempDir(), "build"+string(os.PathListSeparator)+"odd"))
	data := t.TempDir()
	a.setupRun = func(kind, dir string, environ []string) (crew.Setup, error) {
		return crew.SetupFor(crew.SetupInput{Kind: kind, Host: crew.HostWindow, Env: map[string]string{"PATH": "/nonexistent"},
			DataDir: data, Dir: dir, AgentDir: dir}, "")
	}
	p, _ := a.profile("one")
	if _, err := a.startInstance(p); err == nil || !strings.Contains(err.Error(), "PATH") {
		t.Fatalf("an agent folder under %q: %v, want the start refused for its PATH", a.agentDir, err)
	}
}

// The agent's PATH link is made again at each start: one left pointing elsewhere (another folder,
// one since removed) or at nothing is replaced, so the agent's first PATH folder is its own bin.
func TestAnAgentsStalePathLinkIsReplaced(t *testing.T) {
	for _, stale := range []string{"elsewhere", "nowhere"} {
		t.Run(stale, func(t *testing.T) {
			a, c, _ := crewAgents(t, oneTwo)
			p, _ := a.profile("one")
			dir := a.agentFolder(c.ws, "one")
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
			if first, _, _ := strings.Cut(pathOf(args), string(os.PathListSeparator)); first != link {
				t.Fatalf("the agent's PATH starts %q, want its link %q", first, link)
			}
		})
	}
}

// fakeBuild makes an empty autodoc build in a new folder dir, named as this OS names a program
// (autodoc.exe on Windows), and answers its path: what an agent's PATH leads to.
func fakeBuild(t *testing.T, dir string) string {
	t.Helper()
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	name := "autodoc"
	if filepath.Separator == '\\' {
		name += ".exe"
	}
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, []byte("#!/bin/sh\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	return path
}
