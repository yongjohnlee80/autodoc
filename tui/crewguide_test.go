package tui

import (
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/yongjohnlee80/autodoc/core/crew"
)

// The crew agent's guide and launch: an agent started under the window's crew scope is told its
// CLI's crew and mail procedure, how it registers to be woken among it, and a crew OpenCode agent
// loads AutoDoc's OpenCode plugin, which registers it. Without them only a Claude agent, whose
// SessionStart hook registers it, was ever reachable.

const crewKinds = `[{"name":"Codex","command":"codex","codename":"codex-one"},` +
	`{"name":"Agy","command":"agy","codename":"agy-one"},` +
	`{"name":"OpenCode","command":"opencode","codename":"opencode-one"},` +
	`{"name":"Claude","command":"claude","codename":"claude-one"}]`

// TestACrewAgentIsToldItsCLIsCrewProcedure: each CLI's agent's AGENTS.md and CLAUDE.md hold the crew
// guide for its own kind (crew.GuideOf), its wake step included; an agent outside a crew scope is
// told none of it.
func TestACrewAgentIsToldItsCLIsCrewProcedure(t *testing.T) {
	for codename, kind := range map[string]string{"codex-one": "codex", "agy-one": "agy", "opencode-one": "opencode", "claude-one": "claude"} {
		t.Run(kind, func(t *testing.T) {
			a, c, _ := crewAgents(t, crewKinds)
			a.pluginEnv = func() (string, error) { return `OPENCODE_CONFIG_CONTENT={"plugin":["file:///p/autodoc-crew.js"]}`, nil }
			p, _ := a.profile(codename)
			instance, err := a.startInstance(p)
			if err != nil || !instance.running {
				t.Fatalf("start: %v", err)
			}
			dir := c.panel.configs[instance.tab].Dir
			want := crew.GuideOf(kind).Markdown
			for _, name := range []string{"AGENTS.md", "CLAUDE.md"} {
				b, err := os.ReadFile(filepath.Join(dir, name))
				if err != nil {
					t.Fatal(err)
				}
				if !strings.Contains(string(b), "## Mail between agents") || !strings.Contains(string(b), want) {
					t.Fatalf("%s's %s doesn't hold its crew procedure", kind, name)
				}
			}
		})
	}
	// each kind's wake step is its own, so the guide names the right command
	for kind, step := range map[string]string{
		"codex": "autodoc crew endpoint --codex-thread", "agy": "autodoc crew bridge --agy",
		"opencode": "OpenCode's AutoDoc plugin registers on its own", "claude": "SessionStart",
	} {
		if !strings.Contains(crew.GuideOf(kind).Markdown, step) {
			t.Errorf("%s's guide has no %q", kind, step)
		}
	}
	// outside a crew scope: no crew procedure
	if g := agentGuide(wsInfo{name: "kb", root: "/kb"}, "/bin/autodoc", "", "/w/bin/autodoc", ""); strings.Contains(g, "## Mail between agents") {
		t.Fatal("an agent outside a crew scope is told the crew procedure")
	}
}

// TestACrewOpenCodeAgentLoadsTheAutoDocPlugin: a crew OpenCode agent starts with the
// OPENCODE_CONFIG_CONTENT that loads AutoDoc's plugin; another CLI's doesn't, and doesn't run the
// plugin's setup; a plugin that can't be had refuses the start, rather than start an agent no mail
// can reach.
func TestACrewOpenCodeAgentLoadsTheAutoDocPlugin(t *testing.T) {
	const env = `OPENCODE_CONFIG_CONTENT={"plugin":["file:///p/autodoc-crew.js"]}`
	a, c, _ := crewAgents(t, crewKinds)
	calls := 0
	a.pluginEnv = func() (string, error) { calls++; return env, nil }
	p, _ := a.profile("opencode-one")
	instance, err := a.startInstance(p)
	if err != nil {
		t.Fatal(err)
	}
	args, _, err := startedEnv(a, c, instance)
	if err != nil || !slices.Contains(args, env) || calls != 1 {
		t.Fatalf("the OpenCode agent's start: %v, plugin setups %d, args %v", err, calls, args)
	}

	calls = 0
	p, _ = a.profile("claude-one")
	claude, err := a.startInstance(p)
	if err != nil {
		t.Fatal(err)
	}
	args, _, err = startedEnv(a, c, claude)
	if err != nil || calls != 0 || slices.ContainsFunc(args, func(s string) bool { return strings.HasPrefix(s, "OPENCODE_CONFIG_CONTENT=") }) {
		t.Fatalf("the Claude agent's start: %v, plugin setups %d, args %v", err, calls, args)
	}

	a.pluginEnv = func() (string, error) { return "", errors.New("no data folder") }
	if _, spawned, err := startedEnv(a, c, instance); err == nil || spawned || !strings.Contains(err.Error(), "OpenCode plugin") {
		t.Fatalf("a plugin that can't be had: %v, spawned %v; want the start refused", err, spawned)
	}
}

// TestTheOpenCodePluginEnvIsTheAgentsBuilds: opencodeEnv runs the agent's own autodoc (its build,
// on the TUI's config) as `crew plugin --opencode --print-env`, and passes on its one line; any
// other output is refused.
func TestTheOpenCodePluginEnvIsTheAgentsBuilds(t *testing.T) {
	dir := t.TempDir()
	bin := filepath.Join(dir, "autodoc")
	script := "#!/bin/sh\n" +
		"[ \"$*\" = \"--config /c/autodoc.toml crew plugin --opencode --print-env\" ] || { echo \"args: $*\" >&2; exit 3; }\n" +
		"echo 'OPENCODE_CONFIG_CONTENT={\"plugin\":[\"file:///p/autodoc-crew.js\"]}'\n"
	if err := os.WriteFile(bin, []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	a := &agents{agentBinPath: bin, agentConfig: "/c/autodoc.toml"}
	env, err := a.opencodeEnv()
	if err != nil || env != `OPENCODE_CONFIG_CONTENT={"plugin":["file:///p/autodoc-crew.js"]}` {
		t.Fatalf("opencodeEnv: %q %v", env, err)
	}
	if err := os.WriteFile(bin, []byte("#!/bin/sh\necho something else\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	if _, err := a.opencodeEnv(); err == nil {
		t.Fatal("unexpected output was passed on")
	}
}
