package crew

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/yongjohnlee80/autodoc/tui/agents"
	"github.com/yongjohnlee80/autodoc/tui/feature"
)

// TestAnAgentWorksInItsOwnFolderUnderTheWindowsScope (docs/decisions/crew.md#the-session-context):
// while the window holds a crew scope, each agent starts in its own folder under it, the same
// across its sessions; without a scope, in the workspace's folder as before. A stopped tab whose
// folder the scope moved is not started there again: its process would run in the old scope's
// folder.
func TestAnAgentWorksInItsOwnFolderUnderTheWindowsScope(t *testing.T) {
	a, c := modelAgents(t)
	ws := feature.Workspace{Name: "kb", Root: t.TempDir()}
	shared := a.AgentFolder(ws, "one")
	a.Crew = &CrewHost{}
	if got := a.AgentFolder(ws, "one"); got != shared {
		t.Fatalf("before a scope is held: %s, want the workspace's %s", got, shared)
	}
	crewOf(a).scope, crewOf(a).epoch = "autodoc:0123456789abcdef", 1
	p, _ := a.Profile("one")
	instance, err := a.StartInstance(p)
	if err != nil {
		t.Fatal(err)
	}
	want := filepath.Join(agents.AgentBase(nil, a.Snapshot().AgentDir), agents.SafeName(c.ws.Name), agents.CrewFileName("autodoc:0123456789abcdef"), agents.CrewFileName("one"))
	if got := c.panel.configs[instance.State().Tab].Dir; got != want {
		t.Fatalf("the agent's folder: %s, want its own under the scope %s", got, want)
	}
	if _, err := os.Stat(filepath.Join(want, "CLAUDE.md")); err != nil {
		t.Fatalf("the guide was not written in the agent's folder: %v", err)
	}
	c.panel.exit(instance.State().Tab, 0)
	if again, err := a.StartInstance(p); err != nil || again != instance {
		t.Fatalf("a restart under the same scope: %v, want its tab started again", err)
	}
	c.panel.exit(instance.State().Tab, 0)
	crewOf(a).scope = "autodoc:fedcba9876543210"
	if _, err := a.StartInstance(p); !errors.Is(err, agents.ErrAgentStale) {
		t.Fatalf("a start after the scope moved: %v, want ErrAgentStale (its tab runs in the old folder)", err)
	}
}
