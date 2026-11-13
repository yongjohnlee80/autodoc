package agents

import (
	"slices"
	"testing"
)

// TestCrewSetupAsksForNoAgentServerWhereTheWindowCantRunOne: the window's crew setup asks for no
// agent server exactly where it can't run one (CanRunAgentServers false, the window on a system
// without process groups), so a Codex agent there starts without the server that would refuse its
// start; elsewhere it asks as before.
func TestCrewSetupAsksForNoAgentServerWhereTheWindowCantRunOne(t *testing.T) {
	args := CrewSetupArgs("", "codex", t.TempDir(), "/x/autodoc")
	if asks := slices.Contains(args, "--no-agent-server"); asks == CanRunAgentServers {
		t.Fatalf("crew setup's arguments %q: --no-agent-server %v, want it exactly where the window can't run an agent's server (can: %v)", args, asks, CanRunAgentServers)
	}
	if args[len(args)-2] != "--mail-via" || args[len(args)-1] != "mcp" {
		t.Fatalf("crew setup's arguments %q: want --mail-via mcp last, as before", args)
	}
}
