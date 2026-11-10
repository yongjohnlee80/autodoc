package agents

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/yongjohnlee80/autodoc/core/crew"
	"github.com/yongjohnlee80/autodoc/tui/feature"
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

// A folder for the agents whose own path holds the PATH separator (':', or ';' on Windows, where a
// name can't hold ':') can't be put on a PATH whole: the start is refused, saying why, rather than
// started on a PATH whose autodoc is someone else's.
func TestAnAgentIsNotStartedOnABrokenPath(t *testing.T) {
	c := newIdentityHost(oneTwo)
	c.ws = feature.Workspace{ID: 7, Name: "kb", Root: t.TempDir()}
	a := testAgents(c, filepath.Join(t.TempDir(), "agents"+string(os.PathListSeparator)+"odd"), "")
	// the PATH entry is the agents' (Unix) or the build's folder (Windows): each holds the separator
	fakeSeamsOf(a).agentBin = fakeBuild(t, filepath.Join(t.TempDir(), "build"+string(os.PathListSeparator)+"odd"))
	data := t.TempDir()
	fakeSeamsOf(a).setupRun = func(kind, dir string, environ []string) (crew.Setup, error) {
		return crew.SetupFor(crew.SetupInput{Kind: kind, Host: crew.HostWindow, Env: map[string]string{"PATH": "/nonexistent"},
			DataDir: data, Dir: dir, AgentDir: dir}, "")
	}
	p, _ := a.Profile("one")
	if _, err := a.StartInstance(p); err == nil || !strings.Contains(err.Error(), "PATH") {
		t.Fatalf("an agent folder under %q: %v, want the start refused for its PATH", a.agentDir, err)
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

// TestAnAgentCallsTheBuildTheSeamsName: an agent's autodoc ($AUTODOC_BIN in its environment) is
// the build the program's seams name (a test's), else this running program itself.
func TestAnAgentCallsTheBuildTheSeamsName(t *testing.T) {
	c := newIdentityHost(oneTwo)
	c.ws = feature.Workspace{ID: 7, Name: "kb", Root: t.TempDir()}
	a := testAgents(c, t.TempDir(), "")
	p, _ := a.Profile("one")
	bin := func() string {
		for _, kv := range a.AgentEnv(c.ws, t.TempDir(), p) {
			if v, ok := strings.CutPrefix(kv, "AUTODOC_BIN="); ok {
				return v
			}
		}
		return ""
	}
	self, err := os.Executable()
	if err == nil {
		self, err = filepath.EvalSymlinks(self) // the program itself, not a link to it
	}
	if err != nil {
		t.Fatal(err)
	}
	if got := bin(); got != self {
		t.Errorf("no build in the seams: the agent calls %q, want this program %q", got, self)
	}
	build := filepath.Join(t.TempDir(), "autodoc")
	fakeSeamsOf(a).agentBin = build
	if got := bin(); got != build {
		t.Errorf("the seams' build: the agent calls %q, want %q", got, build)
	}
}
