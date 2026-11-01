package tui

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/yongjohnlee80/autodoc/core/crew"
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

// An agent's PATH has its own autodoc first: split on ':', as every shell splits it, the first
// folder holds the wrapper that runs the window's build on its config, and a shell asking for
// autodoc in that environment finds it. A crew agent's folder holds the window's scope in its name
// (autodoc:<id>), so that folder can't be the entry itself: split there, PATH's first entries named
// nothing, and every bare `autodoc` an agent ran (agy's bridge, its mail) was the user's installed
// one (gold-man's finding, 2026-11-01).
func TestAnAgentsPathHasItsOwnAutodocFirst(t *testing.T) {
	a, c, _ := crewAgents(t, oneTwo)
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
	sh, err := exec.LookPath("sh")
	if err != nil {
		t.Skip("no sh")
	}
	cmd := exec.Command(sh, "-c", "command -v autodoc")
	cmd.Env = []string{"PATH=" + path}
	out, _ := cmd.Output()
	found := strings.TrimSpace(string(out))
	wrapper, _ := os.ReadFile(found)
	if found != filepath.Join(first, "autodoc") || !strings.Contains(string(wrapper), "exec "+shellQuote(a.agentBin())) {
		t.Fatalf("the agent's PATH %q: its first folder %q; a shell finds autodoc at %q (%q), want the window's wrapper there",
			path, first, found, string(wrapper))
	}
	if !strings.Contains(instance.dir, "autodoc:") {
		t.Fatalf("the agent's folder %q holds no scope: the cell doesn't hold the case it is for", instance.dir)
	}
}

// A folder for the agents whose own path holds the PATH separator can't be put on a PATH whole:
// the start is refused, saying why, rather than started on a PATH whose autodoc is someone else's.
func TestAnAgentIsNotStartedOnABrokenPath(t *testing.T) {
	c := newIdentityHost(oneTwo)
	c.ws = wsInfo{id: 7, name: "kb", root: t.TempDir()}
	a := newAgents(c, filepath.Join(t.TempDir(), "agents:odd"), "")
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
			link := a.agentPathDir(dir)
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
