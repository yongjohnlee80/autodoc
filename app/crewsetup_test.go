//go:build unix

package app

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/yongjohnlee80/autodoc/core/crew"
)

// TestCrewSetupPrintsTheAgentsSetup: `autodoc --config F crew setup --kind K` prints crew.SetupFor's
// answer as JSON, AutoDoc's own files in F's data folder; the profile's environment is the one it
// runs under, read and never printed back; a missing kind, a stray argument, an unknown host, or
// an --env (a profile's variable on the command line, which any user can read) is usage (exit 2).
func TestCrewSetupPrintsTheAgentsSetup(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("XDG_DATA_HOME", t.TempDir())
	data := t.TempDir()
	cfg := filepath.Join(t.TempDir(), "c.toml")
	if err := os.WriteFile(cfg, []byte("[server]\ndata_dir = \""+data+"\"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	run1 := func(args ...string) (int, string, string) {
		var out, errOut bytes.Buffer
		code := run(t.Context(), append([]string{"--config", cfg}, args...), Options{Version: "v1.2.3"}, &out, &errOut)
		return code, strings.TrimSpace(out.String()), errOut.String()
	}
	t.Setenv("OPENCODE_CONFIG_CONTENT", `{"theme":"x"}`)
	t.Setenv("CLAUDE_CONFIG_DIR", "/accounts/work/.claude")
	code, out, errOut := run1("crew", "setup", "--kind", "opencode")
	if code != 0 {
		t.Fatalf("crew setup --kind opencode: exit %d, %s", code, errOut)
	}
	var s crew.Setup
	if err := json.Unmarshal([]byte(out), &s); err != nil {
		t.Fatalf("%q: %v", out, err)
	}
	if !strings.Contains(s.Env["OPENCODE_CONFIG_CONTENT"], `"theme":"x"`) ||
		!strings.Contains(s.Env["OPENCODE_CONFIG_CONTENT"], filepath.Join(data, "opencode", crew.OpenCodePluginName)) {
		t.Errorf("OPENCODE_CONFIG_CONTENT %q: want the profile's own value extended with the plugin in --config's data folder", s.Env["OPENCODE_CONFIG_CONTENT"])
	}
	if strings.Contains(out, "/accounts/work") {
		t.Errorf("the profile's environment was printed back: %s", out)
	}
	for _, bad := range [][]string{{"crew", "setup"}, {"crew", "setup", "--kind", "claude", "extra"},
		{"crew", "setup", "--kind", "claude", "--host", "emacs"}, {"crew", "setup", "--kind", "claude", "--env", "CLAUDE_CONFIG_DIR=/x"}} {
		if code, out, _ := run1(bad...); code != 2 || out != "" {
			t.Errorf("%v: exit %d, %q; want usage", bad, code, out)
		}
	}
}

// TestCrewSetupOffersTheMCPServerOnlyToAHostThatRegistersItsMode: without --mail-via mcp a host
// gets the CLI's setup (Claude: the guide's commands allowed, no --mcp-config; OpenCode: no MCP
// server), mail_via cli, so a host that registers no mail_via never has a session allowed the MCP
// tools alone while its notice names the CLI's commands; with it, the kind is given the crew MCP
// server and mail_via mcp. agy, whose mail is MCP alone, is given it either way. A mode that is
// neither is usage.
func TestCrewSetupOffersTheMCPServerOnlyToAHostThatRegistersItsMode(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("XDG_DATA_HOME", t.TempDir())
	t.Setenv("PATH", "/nonexistent")
	data := t.TempDir()
	cfg := filepath.Join(t.TempDir(), "c.toml")
	if err := os.WriteFile(cfg, []byte("[server]\ndata_dir = \""+data+"\"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	setup := func(args ...string) crew.Setup {
		t.Helper()
		var out, errOut bytes.Buffer
		if code := run(t.Context(), append([]string{"--config", cfg, "crew", "setup"}, args...), Options{Version: "v1"}, &out, &errOut); code != 0 {
			t.Fatalf("crew setup %v: exit %d, %s", args, code, errOut.String())
		}
		var s crew.Setup
		if err := json.Unmarshal(out.Bytes(), &s); err != nil {
			t.Fatalf("%q: %v", out.String(), err)
		}
		return s
	}
	agent := t.TempDir()
	if s := setup("--kind", "claude", "--host", "nvim"); slices.Contains(s.Argv, "--mcp-config") || !slices.Equal(s.Allow, crew.ClaudeAllow()) || s.MailVia != crew.MailViaCLI {
		t.Errorf("claude without --mail-via: argv %q, allow %q, mail via %v; want the CLI's setup", s.Argv, s.Allow, s.MailVia)
	}
	if s := setup("--kind", "claude", "--host", "nvim", "--mail-via", "mcp"); !slices.Contains(s.Argv, "--mcp-config") || !slices.Equal(s.Allow, crew.ClaudeMCPAllow()) || s.MailVia != crew.MailViaMCP {
		t.Errorf("claude with --mail-via mcp: argv %q, allow %q, mail via %v; want the MCP server, its tools alone", s.Argv, s.Allow, s.MailVia)
	}
	if s := setup("--kind", "claude", "--host", "nvim", "--mail-via", "cli"); slices.Contains(s.Argv, "--mcp-config") || s.MailVia != crew.MailViaCLI {
		t.Errorf("claude with --mail-via cli: argv %q, mail via %v; want the CLI's setup", s.Argv, s.MailVia)
	}
	if s := setup("--kind", "opencode"); strings.Contains(s.Env["OPENCODE_CONFIG_CONTENT"], `"mcp"`) || s.MailVia != crew.MailViaCLI {
		t.Errorf("opencode without --mail-via: %q, mail via %v; want no MCP server", s.Env["OPENCODE_CONFIG_CONTENT"], s.MailVia)
	}
	if s := setup("--kind", "opencode", "--mail-via", "mcp"); !strings.Contains(s.Env["OPENCODE_CONFIG_CONTENT"], `"mcp"`) || s.MailVia != crew.MailViaMCP {
		t.Errorf("opencode with --mail-via mcp: %q, mail via %v; want the MCP server", s.Env["OPENCODE_CONFIG_CONTENT"], s.MailVia)
	}
	if s := setup("--kind", "agy", "--agent-dir", agent); len(s.FolderFiles) != 2 || s.MailVia != crew.MailViaMCP { // its MCP config, its crew rules
		t.Errorf("agy without --mail-via: folder files %d, mail via %v; want its MCP setup either way", len(s.FolderFiles), s.MailVia)
	}
	var out, errOut bytes.Buffer
	if code := run(t.Context(), []string{"--config", cfg, "crew", "setup", "--kind", "claude", "--mail-via", "smtp"}, Options{Version: "v1"}, &out, &errOut); code != 2 {
		t.Errorf("--mail-via smtp: exit %d, want usage", code)
	}
}
