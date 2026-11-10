package tui

import (
	"os"
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

// A folder for the agents whose own path holds the PATH separator (':', or ';' on Windows, where a
// name can't hold ':') can't be put on a PATH whole: the start is refused, saying why, rather than
// started on a PATH whose autodoc is someone else's.
func TestAnAgentIsNotStartedOnABrokenPath(t *testing.T) {
	c := newIdentityHost(oneTwo)
	c.ws = wsInfo{id: 7, name: "kb", root: t.TempDir()}
	a := testAgents(c, filepath.Join(t.TempDir(), "agents"+string(os.PathListSeparator)+"odd"), "")
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
