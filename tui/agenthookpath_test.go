package tui

import (
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/yongjohnlee80/autodoc/core/crew"
)

// claudeHookCommands are the hook commands of the Claude settings in dir, by event.
func claudeHookCommands(t *testing.T, dir string) map[string][]string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(dir, ".claude", "settings.json"))
	if err != nil {
		t.Fatal(err)
	}
	var s struct {
		Hooks map[string][]struct {
			Hooks []struct{ Command string }
		}
	}
	if err := json.Unmarshal(b, &s); err != nil {
		t.Fatal(err)
	}
	out := map[string][]string{}
	for event, groups := range s.Hooks {
		for _, g := range groups {
			for _, h := range g.Hooks {
				out[event] = append(out[event], h.Command)
			}
		}
	}
	return out
}

// The commands AutoDoc writes name the build by its absolute path, never a bare autodoc a PATH could
// make another: crew setup's --autodoc (its MCP server and its hooks run it), a crew Claude agent's
// hooks and its workspace notice in its folder's settings; and the agent's environment carries the
// build as AUTODOC_BIN.
func TestTheCommandsAutoDocWritesNameTheBuildByItsPath(t *testing.T) {
	build := fakeBuild(t, filepath.Join(t.TempDir(), "o'brien build"))
	args := crewSetupArgs("", crew.KindClaude, t.TempDir(), build)
	if i := slices.Index(args, "--autodoc"); i < 0 || i+1 >= len(args) || args[i+1] != shellQuote(build) {
		t.Errorf("crew setup's arguments %q: want --autodoc %s", args, shellQuote(build))
	}
	a, c, _ := crewAgents(t, crewKinds)
	a.agentBinPath = build
	p, _ := a.profile("claude-one")
	instance, err := a.startInstance(p)
	if err != nil {
		t.Fatal(err)
	}
	awaitStart(t, c, instance)
	hooks := claudeHookCommands(t, instance.dir)
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
	env, _, err := startedEnv(a, c, instance)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Contains(env, "AUTODOC_BIN="+build) {
		t.Errorf("the agent's environment has no AUTODOC_BIN=%s", build)
	}
}

// A hook of an earlier build (the agent folder's wrapper, or a build since moved) is taken out when
// this build writes its own: one program word and AutoDoc's command after it are AutoDoc's.
func TestAnEarlierBuildsHooksAreTakenOut(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, ".claude", "settings.json")
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	old := shellQuote(filepath.Join(dir, "bin", "autodoc"))
	earlier := `{"hooks":{"UserPromptSubmit":[{"hooks":[{"type":"command","command":"` + strings.ReplaceAll(old, `\`, `\\`) + ` --workspace-notice"},{"type":"command","command":"mine --workspace-notice --x"}]}]}}`
	if err := os.WriteFile(path, []byte(earlier), 0o600); err != nil {
		t.Fatal(err)
	}
	build := shellQuote(filepath.Join(t.TempDir(), "autodoc"))
	if err := mergeClaudeHook(path, build+" --workspace-notice"); err != nil {
		t.Fatal(err)
	}
	got := claudeHookCommands(t, dir)["UserPromptSubmit"]
	if !slices.Contains(got, build+" --workspace-notice") || slices.Contains(got, old+" --workspace-notice") || !slices.Contains(got, "mine --workspace-notice --x") {
		t.Errorf("the notice hooks %q: want this build's, the user's own kept, the earlier build's gone", got)
	}
}
