package tui

import (
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync/atomic"
	"testing"
	"time"

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
// guide for its own kind (crew.GuideOf), its wake step included, as their first section, at most 25
// lines; an agent outside a crew scope is told none of it.
func TestACrewAgentIsToldItsCLIsCrewProcedure(t *testing.T) {
	for codename, kind := range map[string]string{"codex-one": "codex", "agy-one": "agy", "opencode-one": "opencode", "claude-one": "claude"} {
		t.Run(kind, func(t *testing.T) {
			a, c, _ := crewAgents(t, crewKinds)
			a.pluginEnv = func() (string, error) { return `OPENCODE_CONFIG_CONTENT={"plugin":["file:///p/autodoc-crew.js"]}`, nil }
			p, _ := a.profile(codename)
			instance, err := a.startInstance(p)
			if err == nil {
				awaitStart(t, c, instance)
			}
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
				g := string(b)
				at, ws := strings.Index(g, "## Mail and your crew\n"), strings.Index(g, "## The workspace\n")
				if at < 0 || !strings.Contains(g, want) {
					t.Fatalf("%s's %s doesn't hold its crew procedure", kind, name)
				}
				// first, under the header, before anything it might read past; and short
				if ws < 0 || at > ws {
					t.Fatalf("%s's %s has its crew section at %d, after the workspace's at %d", kind, name, at, ws)
				}
				section := g[at:]
				section = section[:strings.Index(section[3:], "\n## ")+3]
				if n := strings.Count(section, "\n"); n > 25 {
					t.Fatalf("%s's crew section is %d lines, at most 25", kind, n)
				}
			}
		})
	}
	// each kind's wake step is its own, so the guide names the right command
	for kind, step := range map[string]string{
		"codex": "autodoc crew endpoint --codex-thread", "agy": "autodoc crew bridge --agy",
		"opencode": "OpenCode's AutoDoc plugin registers how you're woken on its own", "claude": "SessionStart",
	} {
		if !strings.Contains(crew.GuideOf(kind).Markdown, step) {
			t.Errorf("%s's guide has no %q", kind, step)
		}
	}
	// outside a crew scope: no crew procedure
	if g := agentGuide(wsInfo{name: "kb", root: "/kb"}, "/bin/autodoc", "", "/w/bin/autodoc", ""); strings.Contains(g, "## Mail and your crew") {
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
	awaitStart(t, c, instance)
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

	a.pluginEnv, a.pluginAnswer = func() (string, error) { return "", errors.New("no data folder") }, nil
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
	env, err := opencodeEnv(bin, "/c/autodoc.toml")
	if err != nil || env != `OPENCODE_CONFIG_CONTENT={"plugin":["file:///p/autodoc-crew.js"]}` {
		t.Fatalf("opencodeEnv: %q %v", env, err)
	}
	if err := os.WriteFile(bin, []byte("#!/bin/sh\necho something else\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	if _, err := opencodeEnv(bin, "/c/autodoc.toml"); err == nil {
		t.Fatal("unexpected output was passed on")
	}
}

// TestTheOpenCodePluginEnvRunsOnceOffTheLoop: the plugin's env is the process's one run, started
// off the loop when the agents feature starts with an OpenCode profile (Started returns while it
// runs; a window with none runs none); every crew OpenCode
// spawn takes that answer, one waiting for it only while it is on its way; a failure is not kept,
// so the next spawn runs it again.
func TestTheOpenCodePluginEnvRunsOnceOffTheLoop(t *testing.T) {
	const env = `OPENCODE_CONFIG_CONTENT={"plugin":["file:///p/autodoc-crew.js"]}`
	a, c, _ := crewAgents(t, crewKinds)
	var runs atomic.Int32
	release := make(chan struct{})
	a.pluginEnv = func() (string, error) { runs.Add(1); <-release; return env, nil }
	a.Started() // returns with the run still held: it is off the loop
	for deadline := time.Now().Add(2 * time.Second); runs.Load() == 0; time.Sleep(time.Millisecond) {
		if time.Now().After(deadline) {
			t.Fatal("the feature's start never ran it")
		}
	}
	p, _ := a.profile("opencode-one")
	instance, err := a.startInstance(p)
	if err != nil {
		t.Fatal(err)
	}
	close(release)
	awaitStart(t, c, instance)
	for range 2 {
		if args, _, err := startedEnv(a, c, instance); err != nil || !slices.Contains(args, env) {
			t.Fatalf("an OpenCode spawn: %v, args %v", err, args)
		}
	}
	if runs.Load() != 1 {
		t.Fatalf("two spawns after the warm-up ran it %d times, want the one", runs.Load())
	}
	// a crew window with no OpenCode profile runs none: nothing it starts needs it
	b, _, _ := crewAgents(t, oneTwo)
	var others atomic.Int32
	b.pluginEnv = func() (string, error) { others.Add(1); return env, nil }
	b.Started()
	time.Sleep(50 * time.Millisecond)
	if others.Load() != 0 || b.pluginAnswer != nil {
		t.Fatalf("a window with no OpenCode profile warmed the plugin's env (%d runs)", others.Load())
	}
	// a failure is not kept: the next spawn runs it again, and takes its answer
	fails := atomic.Int32{}
	a.pluginEnv, a.pluginAnswer = func() (string, error) {
		if fails.Add(1) == 1 {
			return "", errors.New("no data folder")
		}
		return env, nil
	}, nil
	if _, spawned, err := startedEnv(a, c, instance); err == nil || spawned {
		t.Fatalf("the failing run: %v, spawned %v", err, spawned)
	}
	if args, _, err := startedEnv(a, c, instance); err != nil || !slices.Contains(args, env) || fails.Load() != 2 {
		t.Fatalf("after a failure: %v, args %v, %d runs", err, args, fails.Load())
	}
}

// TestAnOpenCodeStartContinuesOnThePluginsAnswer: a crew OpenCode agent whose registration is
// answered while the plugin's env is still on its way is not started, and no timer is set to look
// again: the start continues when the env's answer posts, never waiting on the loop, and takes
// that answer.
func TestAnOpenCodeStartContinuesOnThePluginsAnswer(t *testing.T) {
	const env = `OPENCODE_CONFIG_CONTENT={"plugin":["file:///p/autodoc-crew.js"]}`
	a, c, _ := crewAgents(t, crewKinds)
	release := make(chan struct{})
	a.pluginEnv = func() (string, error) { <-release; return env, nil }
	p, _ := a.profile("opencode-one")
	instance, err := a.startInstance(p)
	if err != nil {
		t.Fatal(err)
	}
	if instance.running || !instance.starting || len(c.afters) != 0 {
		t.Fatalf("before the env's answer: running %v, starting %v, %d waits; want it starting, and no timer",
			instance.running, instance.starting, len(c.afters))
	}
	close(release)
	for deadline := time.Now().Add(5 * time.Second); ; time.Sleep(time.Millisecond) {
		c.postMu.Lock()
		n := len(c.posts)
		c.postMu.Unlock()
		if n > 0 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("the env's answer posted nothing to the loop")
		}
	}
	c.drainPosts()
	if !instance.running || instance.starting || len(c.afters) != 0 {
		t.Fatalf("after the env's answer: running %v, starting %v, %d waits; want it running", instance.running, instance.starting, len(c.afters))
	}
	if args, _, err := startedEnv(a, c, instance); err != nil || !slices.Contains(args, env) {
		t.Fatalf("the spawn: %v, args %v; want the env's answer", err, args)
	}
}

// awaitStart runs what the loop is posted until instance's crew start is answered: an OpenCode
// start continues when the plugin's env answers (whenStartable), which posts from off the loop.
func awaitStart(t *testing.T, c *identityHost, instance *agentInstance) {
	t.Helper()
	for deadline := time.Now().Add(5 * time.Second); instance.starting; time.Sleep(time.Millisecond) {
		if time.Now().After(deadline) {
			t.Fatal("the agent's start was never answered")
		}
		c.drainPosts()
	}
}
