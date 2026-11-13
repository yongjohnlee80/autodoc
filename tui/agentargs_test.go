package tui

import (
	"errors"
	"testing"

	"github.com/yongjohnlee80/autodoc/core/crew"
	"github.com/yongjohnlee80/autodoc/internal/usershell"
)

// A command refused before it runs (a {workspace} its shell can't carry, a setup's argument it
// can't carry) is never spawned: the start is refused in its tab, nothing registers for a fill's
// refusal, and the tab's starter answers the refusal without calling spawn. A NUL, which no
// shell can carry, makes the case on every OS.
func TestARefusedAgentCommandIsNeverSpawned(t *testing.T) {
	t.Run("a workspace the command can't carry", func(t *testing.T) {
		a, c, w := crewAgents(t, `[{"name":"One","command":"one {workspace}","codename":"one"}]`)
		c.ws.name = "kb\x00x"
		p, _ := a.Profile("one")
		instance, err := a.StartInstance(p)
		if err != nil {
			t.Fatal(err)
		}
		if instance.State().Refusal == nil || !errors.Is(instance.State().Refusal, usershell.ErrNotCarried) || calledTimes(w, "crew.register") != 0 {
			t.Fatalf("refusal %v, registered %d; want refused before anything registers", instance.State().Refusal, calledTimes(w, "crew.register"))
		}
		if _, spawned, err := startedCmd(a, c, instance); spawned || !errors.Is(err, usershell.ErrNotCarried) {
			t.Fatalf("its tab's start: spawned %v, %v; want the refusal and no spawn", spawned, err)
		}
	})
	t.Run("a setup's argument the command can't carry", func(t *testing.T) {
		a, c, _ := crewAgents(t, crewKinds)
		setup := seamsOf(a).agentSetupRun
		seamsOf(a).agentSetupRun = func(kind, dir string, environ []string) (crew.Setup, error) {
			s, err := setup(kind, dir, environ)
			s.Argv = append(s.Argv, "--settings", "x\x00y")
			return s, err
		}
		p, _ := a.Profile("claude-one")
		instance, err := a.StartInstance(p)
		if err != nil {
			t.Fatal(err)
		}
		awaitStart(t, c, instance)
		if _, spawned, err := startedCmd(a, c, instance); spawned || !errors.Is(err, usershell.ErrNotCarried) {
			t.Fatalf("its tab's start: spawned %v, %v; want the setup's refusal and no spawn", spawned, err)
		}
	})
}
