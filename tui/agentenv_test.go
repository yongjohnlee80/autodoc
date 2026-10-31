//go:build linux || darwin

package tui

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// An agent AutoDoc starts is a session of its own: the identity of the agent host that started
// AutoDoc (auto-agents' mailbox and KB scope, the parent Claude Code session, the parent Neovim's
// socket) does not reach it. AutoDoc's own variables do, and so do PATH, HOME and the user's own
// settings, Claude Code's included.
func TestAnAgentStartsWithoutItsSpawnersIdentity(t *testing.T) {
	polluted, kept := pollute(t)
	dir := t.TempDir()
	agent, report := filepath.Join(dir, "env-agent"), filepath.Join(dir, "env")
	script := "#!/bin/sh\nenv > " + shellQuote(report) + ".part && mv " + shellQuote(report) + ".part " + shellQuote(report) + "\nwhile read line; do :; done\n"
	if err := os.WriteFile(agent, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	_, r, _ := runAgentTUI(t, t.TempDir(), []agentProfile{{Name: "heron", Command: shellQuote(agent), Codename: "heron"}}, "heron")
	onLoop(r, func() bool { r.h.agents.toggleAgent(); return true })
	env := envReported(t, r, report)
	wantOwnEnv(t, "the agent", env, polluted, kept)
	for _, k := range []string{"AUTODOC_WORKSPACE", "AUTODOC_SOCKET", "AUTODOC_BIN", "PATH", "HOME"} {
		if env[k] == "" {
			t.Errorf("the agent has no %s", k)
		}
	}
}

// A profile whose command sets one of the spawner's variables itself (NAME=value before its
// program) has it, as it set it: only the inherited one is dropped.
func TestAProfileKeepsWhatItSetsItself(t *testing.T) {
	pollute(t)
	dir := t.TempDir()
	agent, report := filepath.Join(dir, "env-agent"), filepath.Join(dir, "env")
	script := "#!/bin/sh\nenv > " + shellQuote(report) + ".part && mv " + shellQuote(report) + ".part " + shellQuote(report) + "\nwhile read line; do :; done\n"
	if err := os.WriteFile(agent, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	command := "ENABLE_IDE_INTEGRATION=false " + shellQuote(agent)
	_, r, _ := runAgentTUI(t, t.TempDir(), []agentProfile{{Name: "heron", Command: command, Codename: "heron"}}, "heron")
	onLoop(r, func() bool { r.h.agents.toggleAgent(); return true })
	env := envReported(t, r, report)
	if env["ENABLE_IDE_INTEGRATION"] != "false" || env["CLAUDE_CODE_SSE_PORT"] != "" {
		t.Fatalf("the profile's own ENABLE_IDE_INTEGRATION %q, the inherited CLAUDE_CODE_SSE_PORT %q; want false, none",
			env["ENABLE_IDE_INTEGRATION"], env["CLAUDE_CODE_SSE_PORT"])
	}
}

// A profile's env (docs/decisions/crew.md#a-profiles-environment) reaches its agent's process: its
// values with ~ and $VAR expanded from AutoDoc's own environment, never by a shell; a spawner
// variable it sets itself as it set it; its PATH after the agent's own folder, so the autodoc there
// is still the first found; and an AutoDoc variable it names is AutoDoc's.
func TestAProfilesEnvReachesItsAgent(t *testing.T) {
	pollute(t)
	dir := t.TempDir()
	agent, report := filepath.Join(dir, "env-agent"), filepath.Join(dir, "env")
	script := "#!/bin/sh\nenv > " + shellQuote(report) + ".part && mv " + shellQuote(report) + ".part " + shellQuote(report) + "\nwhile read line; do :; done\n"
	if err := os.WriteFile(agent, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	extra := t.TempDir()
	env := map[string]string{
		"CLAUDE_CONFIG_DIR": "~/.claude-work",
		"GREETING":          "${FAKE_GREETING}; $HOME too; $(whoami) `id` not run",
		"CLAUDE_EFFORT":     "low",           // a spawner variable the profile sets itself
		"AUTODOC_ROOT":      "/not/autodocs", // AutoDoc's own, which a profile never sets
		"PATH":              extra + ":$PATH",
	}
	_, r, _ := runAgentTUI(t, t.TempDir(), []agentProfile{{Name: "heron", Command: shellQuote(agent), Codename: "heron", Env: env}}, "heron")
	home, path := os.Getenv("HOME"), os.Getenv("PATH") // runAgentTUI's own, which the spawn expands from
	onLoop(r, func() bool { r.h.agents.toggleAgent(); return true })
	got := envReported(t, r, report)
	if want := filepath.Join(home, ".claude-work"); got["CLAUDE_CONFIG_DIR"] != want {
		t.Errorf("CLAUDE_CONFIG_DIR %q, want %q (~ expanded)", got["CLAUDE_CONFIG_DIR"], want)
	}
	if want := "the user's shell; " + home + " too; $(whoami) `id` not run"; got["GREETING"] != want {
		t.Errorf("GREETING %q, want %q ($VAR expanded, nothing run)", got["GREETING"], want)
	}
	if got["CLAUDE_EFFORT"] != "low" {
		t.Errorf("CLAUDE_EFFORT %q, want the profile's own low", got["CLAUDE_EFFORT"])
	}
	if got["AUTODOC_ROOT"] == "" || got["AUTODOC_ROOT"] == "/not/autodocs" {
		t.Errorf("AUTODOC_ROOT %q, want AutoDoc's workspace root", got["AUTODOC_ROOT"])
	}
	parts := filepath.SplitList(got["PATH"])
	if len(parts) < 2 || filepath.Base(parts[0]) != "bin" || parts[1] != extra || strings.Join(parts[2:], ":") != path {
		t.Fatalf("PATH %q, want the agent's own bin, then the profile's %s:%s", got["PATH"], extra, path)
	}
	if _, err := os.Stat(filepath.Join(parts[0], "autodoc")); err != nil {
		t.Errorf("PATH's first folder %s holds no autodoc: %v", parts[0], err)
	}
}

// A plugin AutoDoc runs is third-party code started on AutoDoc's behalf: the spawner's identity,
// its session's messaging socket and secret among it, does not reach it either. AutoDoc's
// AUTODOC_PLUGIN_* do, and so do the user's own settings.
func TestAPluginRunsWithoutItsSpawnersIdentity(t *testing.T) {
	polluted, kept := pollute(t)
	dir := t.TempDir()
	report := filepath.Join(t.TempDir(), "env")
	plug := filepath.Join(dir, "envdump")
	if err := os.MkdirAll(plug, 0o755); err != nil {
		t.Fatal(err)
	}
	dump := "env > " + shellQuote(report) + ".part && mv " + shellQuote(report) + ".part " + shellQuote(report) + "; cat >/dev/null"
	manifest := fmt.Sprintf("name = %q\ntitle = %q\nkind = %q\nprotocol = 2\ncommand = [%q, %q, %q]\n", "envdump", "Envdump", "dialog", "/bin/sh", "-c", dump)
	if err := os.WriteFile(filepath.Join(plug, "plugin.toml"), []byte(manifest), 0o644); err != nil {
		t.Fatal(err)
	}
	r, _ := pluginTUI(t, dir)
	r.openPlugin("envdump")
	env := envReported(t, r, report)
	wantOwnEnv(t, "the plugin", env, polluted, kept)
	for _, k := range []string{"AUTODOC_PLUGIN_DIR", "AUTODOC_PLUGIN_PROTOCOL", "AUTODOC_SOCKET"} {
		if env[k] == "" {
			t.Errorf("the plugin has no %s", k)
		}
	}
}

// pollute sets, for t, the variables an agent host gives AutoDoc (polluted), and the user's own
// ones beside them (kept).
func pollute(t *testing.T) (polluted, kept map[string]string) {
	t.Helper()
	polluted = map[string]string{
		"AUTO_AGENTS_MAILBOX_ID":      "agent:jarvis:1",
		"AUTO_AGENTS_KB_ROOT":         "/the/spawner's/kb",
		"CLAUDECODE":                  "1",
		"CLAUDE_CODE_SESSION_ID":      "the-parent-session",
		"CLAUDE_CODE_MESSAGING_TOKEN": "the-parent's-secret",
		"CLAUDE_PID":                  "4242",
		"NVIM":                        "/tmp/the-parent-nvim.sock",
		"NVIM_LISTEN_ADDRESS":         "/tmp/the-parent-nvim.sock",
		// a Neovim-hosted Claude Code's IDE connection: an agent with them dials the parent's IDE
		"CLAUDE_CODE_SSE_PORT":   "41234",
		"ENABLE_IDE_INTEGRATION": "true",
		"CLAUDE_CODE_ENTRYPOINT": "cli",
		"CLAUDE_CODE_EXECPATH":   "/the/parent's/claude",
		"CLAUDE_EFFORT":          "high",
		"AUTO_AGENTS_MCP_URL":    "http://127.0.0.1:41235/mcp",
	}
	kept = map[string]string{
		"CLAUDE_CODE_USE_BEDROCK": "1",                // the user's own setting, not a session's
		"NVIM_LOG_FILE":           "/tmp/nvim-log",    // a log's path, no socket
		"MCP_TIMEOUT":             "30000",            // the user's own MCP tuning, no connection
		"FAKE_GREETING":           "the user's shell", // any other variable
	}
	for k, v := range polluted {
		t.Setenv(k, v)
	}
	for k, v := range kept {
		t.Setenv(k, v)
	}
	return polluted, kept
}

// envReported is the environment a process wrote to report, as env prints it, once it is there.
func envReported(t *testing.T, r *running, report string) map[string]string {
	t.Helper()
	var env map[string]string
	r.s.WaitFor(t, "the environment written", func(string) bool {
		b, err := os.ReadFile(report)
		if err != nil {
			return false
		}
		env = map[string]string{}
		for _, line := range strings.Split(string(b), "\n") {
			if k, v, ok := strings.Cut(line, "="); ok {
				env[k] = v
			}
		}
		return true
	})
	return env
}

// wantOwnEnv fails unless env has none of polluted and every one of kept, as set.
func wantOwnEnv(t *testing.T, who string, env, polluted, kept map[string]string) {
	t.Helper()
	for k := range polluted {
		if v, ok := env[k]; ok {
			t.Errorf("%s has the spawner's %s=%q", who, k, v)
		}
	}
	for k, v := range kept {
		if env[k] != v {
			t.Errorf("%s's %s is %q, want the user's %q", who, k, env[k], v)
		}
	}
}

// A plugin's install build is the plugin's code, run on AutoDoc's behalf: the spawner's identity
// does not reach it. AUTODOC_PLUGIN_DIR does, and so do the user's own settings.
func TestAPluginsBuildRunsWithoutItsSpawnersIdentity(t *testing.T) {
	polluted, kept := pollute(t)
	report := filepath.Join(t.TempDir(), "env")
	repo := pluginRepo(t, "Echo", "env > "+shellQuote(report)+".part && mv "+shellQuote(report)+".part "+shellQuote(report))
	r, _ := pluginTUI(t, t.TempDir())
	r.h.p.Post(func() { r.h.plugins.addPlugin("file://" + repo) })
	r.question(t)
	r.keys(t, key('y'))
	env := envReported(t, r, report)
	wantOwnEnv(t, "the plugin's build", env, polluted, kept)
	if env["AUTODOC_PLUGIN_DIR"] == "" {
		t.Error("the plugin's build has no AUTODOC_PLUGIN_DIR")
	}
}

// git, which a plugin's install runs and which may run configured helpers, gets no spawner identity
// either: a git on PATH before the real one records what its clone was started with.
func TestAPluginsCloneRunsWithoutItsSpawnersIdentity(t *testing.T) {
	realGit, err := exec.LookPath("git")
	if err != nil {
		t.Skip("no git")
	}
	repo := pluginRepo(t, "Echo", "true") // made with the real git, before the wrapper is on PATH
	polluted, kept := pollute(t)
	bin, report := t.TempDir(), filepath.Join(t.TempDir(), "env")
	wrapper := "#!/bin/sh\ncase \"$*\" in *clone*) env > " + shellQuote(report) + ".part && mv " + shellQuote(report) + ".part " + shellQuote(report) + " ;; esac\nexec " + shellQuote(realGit) + " \"$@\"\n"
	if err := os.WriteFile(filepath.Join(bin, "git"), []byte(wrapper), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	r, _ := pluginTUI(t, t.TempDir())
	r.h.p.Post(func() { r.h.plugins.addPlugin("file://" + repo) })
	env := envReported(t, r, report)
	wantOwnEnv(t, "git's clone", env, polluted, kept)
	if env["GIT_TERMINAL_PROMPT"] != "0" {
		t.Errorf("git's clone has GIT_TERMINAL_PROMPT %q, want AutoDoc's 0", env["GIT_TERMINAL_PROMPT"])
	}
}
