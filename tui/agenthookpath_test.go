package tui

import (
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/yongjohnlee80/autodoc/core/crew"
)

// The commands AutoDoc writes name the build by its absolute path, never a bare autodoc a PATH could
// make another: crew setup's --autodoc (its MCP server and its hooks run it), a crew Claude agent's
// hooks and its workspace notice in its folder's settings; and the agent's environment carries the
// build as AUTODOC_BIN.
func TestTheCommandsAutoDocWritesNameTheBuildByItsPath(t *testing.T) {
	build := fakeBuildCopy(t, filepath.Join(t.TempDir(), "o'brien build"))
	args := crewSetupArgs("", crew.KindClaude, t.TempDir(), build)
	if i := slices.Index(args, "--autodoc"); i < 0 || i+1 >= len(args) || args[i+1] != shellQuote(build) {
		t.Errorf("crew setup's arguments %q: want --autodoc %s", args, shellQuote(build))
	}
	a, c, _ := crewAgents(t, crewKindsCopy)
	seamsOf(a).agentBin = build
	p, _ := a.profile("claude-one")
	instance, err := a.startInstance(p)
	if err != nil {
		t.Fatal(err)
	}
	awaitStart(t, c, instance)
	hooks := claudeHookCommandsCopy(t, instance.State().Dir)
	for _, event := range []string{crew.ClaudeSessionStart, "UserPromptSubmit"} {
		if len(hooks[event]) == 0 {
			t.Errorf("no %s hook: %v", event, hooks)
		}
	}
	for event, commands := range hooks {
		for _, command := range commands {
			if !strings.HasPrefix(command, shellQuote(build)+" ") {
				t.Errorf("the %s hook runs %q, not the build by its path %s", event, command, build)
			}
		}
	}
	env, _, err := startedEnvCopy(a, c, instance)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Contains(env, "AUTODOC_BIN="+build) {
		t.Errorf("the agent's environment has no AUTODOC_BIN=%s", build)
	}
}
