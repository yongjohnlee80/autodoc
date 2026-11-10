//go:build linux || darwin

package tui

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

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
	var set []string
	for k, v := range polluted {
		set = append(set, k+"="+v)
	}
	for k, v := range kept {
		set = append(set, k+"="+v)
	}
	tuneEnv(t, set...) // the program's environment: the process's own is never polluted
	return polluted, kept
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
