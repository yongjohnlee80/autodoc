//go:build unix

package crew_test

import (
	"encoding/json"
	"errors"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/yongjohnlee80/autodoc/core/crew"
)

// claudeOnPath is a folder holding an executable named claude that reads as a Claude Code knowing
// events (claude_test.go's fakeClaude), to stand as an agent's PATH.
func claudeOnPath(t *testing.T, events ...string) string {
	t.Helper()
	dir := t.TempDir()
	fakeClaude(t, dir, "claude", events...)
	return dir
}

// TestSetupForClaudeIsItsSettingsForTheHost: a Claude agent's setup is crew.ClaudeSettings for the
// Claude Code on the agent's PATH, run by the host's autodoc command: on Neovim's command line as
// --settings, nothing appended for a window (which writes them into the agent's folder), and
// prompts reported only when that Claude Code knows the prompt hooks. With none on PATH, the floor's
// settings, prompts unreported, and a notice; never a failure. The profile's environment is read,
// and nothing of it is answered.
func TestSetupForClaudeIsItsSettingsForTheHost(t *testing.T) {
	full := claudeOnPath(t, everyClaudeEvent...)
	env := map[string]string{"PATH": full, "CLAUDE_CONFIG_DIR": "/accounts/work/.claude"}
	s, err := crew.SetupFor(crew.SetupInput{Kind: crew.KindClaude, Host: crew.HostNvim, Autodoc: "/opt/autodoc wrap", Env: env, Dir: t.TempDir()}, "")
	if err != nil {
		t.Fatal(err)
	}
	if len(s.Argv) != 2 || s.Argv[0] != "--settings" || !s.Prompts || len(s.Env) != 0 || len(s.Notices) != 0 {
		t.Fatalf("Neovim's Claude setup: %+v, want --settings <json>, prompts, no env, no notice", s)
	}
	var settings struct {
		Hooks map[string][]struct {
			Hooks []struct{ Type, Command string }
		}
		CrossSessionInbound string
	}
	if err := json.Unmarshal([]byte(s.Argv[1]), &settings); err != nil {
		t.Fatalf("--settings %q: %v", s.Argv[1], err)
	}
	start := settings.Hooks["SessionStart"]
	if settings.CrossSessionInbound != "accept" || len(start) != 1 || len(start[0].Hooks) != 1 ||
		!strings.HasPrefix(start[0].Hooks[0].Command, "/opt/autodoc wrap crew endpoint --claude") {
		t.Errorf("--settings: %+v, want crossSessionInbound accept and the SessionStart hook run by the host's autodoc", settings)
	}
	if _, ok := settings.Hooks["PermissionRequest"]; !ok {
		t.Errorf("--settings: %v, want the prompt hooks for a Claude Code that knows them", settings.Hooks)
	}
	if strings.Contains(strings.Join(s.Argv, " "), "/accounts/work") {
		t.Errorf("the setup answered the profile's environment back: %v", s.Argv)
	}

	w, err := crew.SetupFor(crew.SetupInput{Kind: crew.KindClaude, Host: crew.HostWindow, Autodoc: "a", Env: env, Dir: t.TempDir()}, "")
	if err != nil || len(w.Argv) != 0 || !w.Prompts {
		t.Errorf("a window's Claude setup: %+v %v, want nothing appended, prompts", w, err)
	}

	old := claudeOnPath(t, "SessionStart", "crossSessionInbound", "Notification")
	o, err := crew.SetupFor(crew.SetupInput{Kind: crew.KindClaude, Host: crew.HostNvim, Autodoc: "a", Env: map[string]string{"PATH": old}, Dir: t.TempDir()}, "")
	if err != nil || o.Prompts || len(o.Argv) != 2 || strings.Contains(o.Argv[1], "PermissionRequest") {
		t.Errorf("a Claude Code without the prompt hooks: %+v %v, want SessionStart alone, prompts unreported", o, err)
	}

	n, err := crew.SetupFor(crew.SetupInput{Kind: crew.KindClaude, Host: crew.HostNvim, Autodoc: "a", Env: map[string]string{"PATH": t.TempDir()}, Dir: t.TempDir()}, "")
	if err != nil || n.Prompts || len(n.Argv) != 2 || len(n.Notices) != 1 {
		t.Errorf("no Claude Code on PATH: %+v %v, want the floor's settings, prompts unreported, one notice", n, err)
	}
}

// TestSetupForOpenCodeLoadsAutoDocsPlugin: an OpenCode agent's setup writes AutoDoc's plugin into
// AutoDoc's data folder (its own file) and answers the OPENCODE_CONFIG_CONTENT that loads it, the
// agent's own value extended, never replaced; nothing goes on its command line, and no other
// variable of the profile's is answered.
func TestSetupForOpenCodeLoadsAutoDocsPlugin(t *testing.T) {
	data := t.TempDir()
	env := map[string]string{"OPENCODE_CONFIG_CONTENT": `{"theme":"x"}`, "XDG_CONFIG_HOME": "/accounts/work"}
	s, err := crew.SetupFor(crew.SetupInput{Kind: crew.KindOpenCode, Host: crew.HostNvim, Autodoc: "a", Env: env, DataDir: data}, "")
	if err != nil {
		t.Fatal(err)
	}
	plugin := filepath.Join(data, "opencode", crew.OpenCodePluginName)
	if _, err := os.Stat(plugin); err != nil {
		t.Fatalf("the plugin is not in AutoDoc's data folder: %v", err)
	}
	keys := make([]string, 0, len(s.Env))
	for k := range s.Env {
		keys = append(keys, k)
	}
	var cfg struct {
		Theme  string
		Plugin []string
	}
	slices.Sort(keys)
	if err := json.Unmarshal([]byte(s.Env["OPENCODE_CONFIG_CONTENT"]), &cfg); err != nil || !slices.Equal(keys, []string{crew.EnvMailVia, "OPENCODE_CONFIG_CONTENT"}) ||
		s.Env[crew.EnvMailVia] != "cli" ||
		cfg.Theme != "x" || len(cfg.Plugin) != 1 || !strings.HasSuffix(cfg.Plugin[0], "/opencode/"+crew.OpenCodePluginName) || len(s.Argv) != 0 {
		t.Errorf("OpenCode's setup: %+v %v, want OPENCODE_CONFIG_CONTENT (the agent's own kept and the plugin added) and the plugin's mail mode, cli", s, err)
	}
}

// TestSetupForAnotherKindIsNothing: a kind with no setup yet answers nothing to add, as a value a
// host can always merge.
func TestSetupForAnotherKindIsNothing(t *testing.T) {
	for _, k := range []string{"generic"} {
		s, err := crew.SetupFor(crew.SetupInput{Kind: k, Host: crew.HostNvim, Env: map[string]string{"PATH": "/x"}}, "")
		if err != nil || s.Argv == nil || s.Env == nil || s.Files == nil || s.Notices == nil || s.After == nil || s.FolderFiles == nil ||
			len(s.Argv)+len(s.Env)+len(s.Files)+len(s.Notices)+len(s.After) != 0 || s.Prompts {
			t.Errorf("%s: %+v %v, want empty lists and maps, no prompts", k, s, err)
		}
	}
}

// TestSetupForCodexIsTheWaitingRegistration: a Codex agent's setup is one step after its start, for
// any host: the agent's autodoc registering the folder's thread, looking until it is found (crew
// endpoint --codex-here --wait), since the start; nothing on its command line or in its env.
func TestSetupForCodexIsTheWaitingRegistration(t *testing.T) {
	for _, host := range []string{crew.HostNvim, crew.HostWindow} {
		s, err := crew.SetupFor(crew.SetupInput{Kind: crew.KindCodex, Host: host, Env: map[string]string{"PATH": "/x"}}, "")
		want := []string{"crew", "endpoint", "--codex-here", "--since", crew.SetupSince, "--wait", "60s"}
		if err != nil || len(s.After) != 1 || !slices.Equal(s.After[0], want) || len(s.Argv)+len(s.Env) != 0 || s.Prompts {
			t.Errorf("%s: %+v %v, want the one step %q", host, s, err, want)
		}
	}
}

// TestSetupForAllowsTheGuidesCommandsInTheSessionAlone: Claude's setup carries the allowance
// (exactly crew.ClaudeAllow) where the session's own CLI reads it: Neovim's --settings, and
// Setup.Allow for a window to merge into the agent's folder. OpenCode's carries none until its
// compound-command probe passes: the agent's own rules alone. Nothing is written beyond AutoDoc's
// own data folder.
func TestSetupForAllowsTheGuidesCommandsInTheSessionAlone(t *testing.T) {
	full := claudeOnPath(t, everyClaudeEvent...)
	s, err := crew.SetupFor(crew.SetupInput{Kind: crew.KindClaude, Host: crew.HostNvim, Autodoc: "a", Env: map[string]string{"PATH": full}, Dir: t.TempDir()}, "")
	if err != nil {
		t.Fatal(err)
	}
	var settings struct{ Permissions struct{ Allow []string } }
	if err := json.Unmarshal([]byte(s.Argv[1]), &settings); err != nil || !slices.Equal(settings.Permissions.Allow, crew.ClaudeAllow()) ||
		!slices.Equal(s.Allow, crew.ClaudeAllow()) {
		t.Errorf("Neovim's Claude allowance: --settings %s, Allow %q; want exactly crew.ClaudeAllow", s.Argv[1], s.Allow)
	}
	w, err := crew.SetupFor(crew.SetupInput{Kind: crew.KindClaude, Host: crew.HostWindow, Autodoc: "a", Env: map[string]string{"PATH": full}, Dir: t.TempDir()}, "")
	if err != nil || len(w.Argv) != 0 || !slices.Equal(w.Allow, crew.ClaudeAllow()) {
		t.Errorf("a window's Claude allowance: %+v %v, want Allow for the folder's settings and nothing appended", w, err)
	}

	home := t.TempDir()
	t.Setenv("HOME", home)
	data := t.TempDir()
	o, err := crew.SetupFor(crew.SetupInput{Kind: crew.KindOpenCode, Host: crew.HostNvim, Autodoc: "a", DataDir: data,
		Env: map[string]string{"OPENCODE_CONFIG_CONTENT": `{"permission":{"bash":{"git push*":"ask"},"edit":"ask"}}`}}, "")
	if err != nil {
		t.Fatal(err)
	}
	var cfg struct {
		Permission struct {
			Bash map[string]string
			Edit string
		}
	}
	if err := json.Unmarshal([]byte(o.Env["OPENCODE_CONFIG_CONTENT"]), &cfg); err != nil {
		t.Fatal(err)
	}
	// OpenCode's probe passed (KB ledger 2026-11-01-probe-compound-contracts-PASS): the guide's
	// commands are allowed beside the agent's own rules, which stay
	want := map[string]string{"git push*": "ask"}
	var rules []string
	for rule := range crew.OpenCodeAllow() {
		want[rule] = "allow"
		rules = append(rules, rule)
	}
	slices.Sort(rules)
	if !maps.Equal(cfg.Permission.Bash, want) || cfg.Permission.Edit != "ask" || !slices.Equal(o.Allow, rules) {
		t.Errorf("OpenCode's setup: %+v, Allow %q; want the agent's own rules and the guide's commands %q", cfg.Permission, o.Allow, rules)
	}
	if es, _ := os.ReadDir(home); len(es) != 0 {
		t.Errorf("the setup wrote into the user's home: %v", es)
	}
}

// TestOpenCodeWithAllowKeepsTheAgentsOwnPermission: a bash that is one action for every command
// keeps that action for the rest ("*"), the guide's commands allowed; a permission or bash of a
// shape no rule can be added to is left as the agent wrote it.
func TestOpenCodeWithAllowKeepsTheAgentsOwnPermission(t *testing.T) {
	got, err := crew.OpenCodeWithAllow(`{"permission":{"bash":"ask"}}`)
	var cfg struct {
		Permission struct{ Bash map[string]string }
	}
	if err != nil || json.Unmarshal([]byte(got), &cfg) != nil || cfg.Permission.Bash["*"] != "ask" ||
		cfg.Permission.Bash["autodoc mail receive *"] != "allow" || len(cfg.Permission.Bash) != len(crew.OpenCodeAllow())+1 {
		t.Errorf("a bash of one action: %s %v, want it as \"*\" and the guide's commands allowed", got, err)
	}
	for _, kept := range []string{`{"permission":"allow"}`, `{"permission":{"bash":["x"]}}`} {
		if got, err := crew.OpenCodeWithAllow(kept); err != nil || got != kept {
			t.Errorf("%s: %s %v, want it left as it is", kept, got, err)
		}
	}
	if _, err := crew.OpenCodeWithAllow("[]"); err == nil {
		t.Error("content that is no JSON object: want refused")
	}
}

// TestSetupForAgyGivesItsMailAsMCPTools: an agy agent's setup puts the crew MCP servers' config in
// its own folder (.agents/mcp_config.json, the servers' command, args and env), and on its command
// line, for the session alone: each tool allowed by its exact mcp(<server>/<tool>) and the bridge by
// its exact command, nothing by a wildcard; the agent's folder trusted; from Neovim, that folder
// added (its cwd is the user's project); and the launch prompt that runs the bridge alone. Its
// prompts are reported (the bridge), and nothing goes in its env.
func TestSetupForAgyGivesItsMailAsMCPTools(t *testing.T) {
	servers := []crew.MCPServer{{Name: "autodoc", Command: []string{"/x/autodoc", "crew", "mcp"},
		Env: map[string]string{"AUTODOC_CREW_CONTEXT": "/c/ctx"}, Tools: []string{"mail_receive", "mail_ack"}}}
	for _, host := range []string{crew.HostWindow, crew.HostNvim} {
		s, err := crew.SetupFor(crew.SetupInput{Kind: crew.KindAgy, Host: host, AgentDir: "/a", Dir: "/p", Servers: servers,
			Env: map[string]string{"PATH": "/x", "SECRET": "s3"}}, "")
		if err != nil {
			t.Fatal(err)
		}
		if len(s.FolderFiles) != 2 || s.FolderFiles[0].Path != ".agents/mcp_config.json" || s.FolderFiles[1].Path != ".agents/rules/autodoc-crew.md" {
			t.Fatalf("%s: folder files %+v, want .agents/mcp_config.json and its crew rules", host, s.FolderFiles)
		}
		var cfg map[string]map[string]struct {
			Command string            `json:"command"`
			Args    []string          `json:"args"`
			Env     map[string]string `json:"env"`
		}
		if err := json.Unmarshal([]byte(s.FolderFiles[0].Content), &cfg); err != nil {
			t.Fatal(err)
		}
		if a := cfg["mcpServers"]["autodoc"]; a.Command != "/x/autodoc" || !slices.Equal(a.Args, []string{"crew", "mcp"}) || a.Env["AUTODOC_CREW_CONTEXT"] != "/c/ctx" || len(a.Env) != 1 {
			t.Errorf("%s: the folder's MCP config %s, want the server's command, args and its env alone", host, s.FolderFiles[0].Content)
		}
		want := []string{"--settings", "", "-i", crew.AgyLaunchPrompt}
		if host == crew.HostNvim {
			want = []string{"--settings", "", "--add-dir", "/a", "-i", crew.AgyLaunchPrompt}
		}
		if len(s.Argv) != len(want) {
			t.Fatalf("%s: argv %q, want the shape %q", host, s.Argv, want)
		}
		for i, w := range want {
			if w != "" && s.Argv[i] != w {
				t.Errorf("%s: argv[%d] %q, want %q", host, i, s.Argv[i], w)
			}
		}
		var settings struct {
			Permissions struct {
				Allow []string `json:"allow"`
			} `json:"permissions"`
			Trusted []string `json:"trustedWorkspaces"`
		}
		if err := json.Unmarshal([]byte(s.Argv[1]), &settings); err != nil {
			t.Fatal(err)
		}
		rules := []string{"command(autodoc crew bridge --agy)", "command(autodoc --contracts)", "mcp(autodoc/mail_receive)", "mcp(autodoc/mail_ack)"}
		trusted := []string{"/a"} // the window's working folder is the agent's folder
		if host == crew.HostNvim {
			trusted = []string{"/a", "/p"} // Neovim's is the user's project, the agent's folder added
		}
		if !slices.Equal(settings.Permissions.Allow, rules) || !slices.Equal(s.Allow, rules) || !slices.Equal(settings.Trusted, trusted) {
			t.Errorf("%s: --settings %s (Allow %q), want exactly %q and %q trusted", host, s.Argv[1], s.Allow, rules, trusted)
		}
		if !s.Prompts || len(s.Env) != 0 || strings.Contains(strings.Join(s.Argv, " "), "s3") {
			t.Errorf("%s: prompts %v, env %v, argv %q: want prompts reported, no env, the profile's env nowhere", host, s.Prompts, s.Env, s.Argv)
		}
	}
	// the crew's own server has no env of its own (it inherits the agent's): its config says none
	s, err := crew.SetupFor(crew.SetupInput{Kind: crew.KindAgy, Host: crew.HostWindow, AgentDir: "/a",
		Servers: []crew.MCPServer{{Name: "autodoc", Command: []string{"/x/autodoc", "crew", "mcp"}, Tools: []string{"mail_receive"}}}}, "")
	if err != nil || len(s.FolderFiles) != 2 || s.FolderFiles[0].Path != ".agents/mcp_config.json" || strings.Contains(s.FolderFiles[0].Content, `"env"`) {
		t.Errorf("a server with no env: %+v %v, want its config without an env", s.FolderFiles, err)
	}
	if !strings.Contains(crew.AgyLaunchPrompt, crew.AgyBridgeCommand) || !strings.Contains(crew.GuideOf(crew.KindAgy).Markdown, "`"+crew.AgyBridgeCommand+"`") {
		t.Error("the launch prompt and the guide don't name the bridge's command, the one its rule allows")
	}
	// agy records the launch prompt as the user's own input (USER_EXPLICIT): it says it is AutoDoc's,
	// the host's, so the agent never takes it for the user starting the bridge by hand (2026-11-01,
	// tifa-lockhart), and it still ends with the exact command the rule allows
	if !strings.HasPrefix(crew.AgyLaunchPrompt, "AutoDoc, your host") || !strings.HasSuffix(crew.AgyLaunchPrompt, ": "+crew.AgyBridgeCommand) {
		t.Errorf("the launch prompt %q, want it to say it comes from AutoDoc, the host, and end with the bridge's exact command", crew.AgyLaunchPrompt)
	}
}

// TestSetupForAgyTrustsNeovimsWorkingFolderOnlyWhenItIsAPath: from Neovim, the working folder is
// trusted for the session only when it is an absolute path other than the agent's own folder; a
// relative or missing one adds nothing, and nothing is ever trusted beyond the session's --settings.
func TestSetupForAgyTrustsNeovimsWorkingFolderOnlyWhenItIsAPath(t *testing.T) {
	servers := []crew.MCPServer{{Name: "autodoc", Command: []string{"autodoc", "crew", "mcp"}, Tools: []string{"mail_receive"}}}
	for dir, want := range map[string][]string{"": {"/a"}, "p": {"/a"}, "/a": {"/a"}, "/p": {"/a", "/p"}} {
		s, err := crew.SetupFor(crew.SetupInput{Kind: crew.KindAgy, Host: crew.HostNvim, AgentDir: "/a", Dir: dir, Servers: servers}, "")
		if err != nil {
			t.Fatal(err)
		}
		var settings struct {
			Trusted []string `json:"trustedWorkspaces"`
		}
		if err := json.Unmarshal([]byte(s.Argv[1]), &settings); err != nil || !slices.Equal(settings.Trusted, want) {
			t.Errorf("working folder %q: trusted %q (%v), want %q", dir, settings.Trusted, err, want)
		}
	}
}

// TestSetupForAgyWithoutServersStartsWithANotice: a host that gives no MCP servers, or no folder of
// the agent's, starts its agy agent all the same, with a notice, and nothing else.
func TestSetupForAgyWithoutServersStartsWithANotice(t *testing.T) {
	servers := []crew.MCPServer{{Name: "autodoc", Command: []string{"autodoc", "crew", "mcp"}, Tools: []string{"mail_receive"}}}
	for _, in := range []crew.SetupInput{{Kind: crew.KindAgy, Host: crew.HostNvim, AgentDir: "/a"}, {Kind: crew.KindAgy, Host: crew.HostNvim, Servers: servers}} {
		s, err := crew.SetupFor(in, "")
		if err != nil || len(s.Notices) != 1 || len(s.Argv)+len(s.FolderFiles)+len(s.Env)+len(s.Allow) != 0 || s.Prompts {
			t.Errorf("servers %d, folder %q: %+v %v, want a notice alone", len(in.Servers), in.AgentDir, s, err)
		}
	}
}

// TestSetupForRefusesADataDirThatIsntAbsolute: a setup asked with a relative data folder, for any
// kind, or with none for OpenCode (whose plugin is a file of AutoDoc's own), is ErrDataDir, and
// writes nothing into the process's working folder; an absolute one keeps the plugin there.
func TestSetupForRefusesADataDirThatIsntAbsolute(t *testing.T) {
	cwd := t.TempDir()
	t.Chdir(cwd)
	for _, k := range []string{crew.KindClaude, crew.KindCodex, crew.KindOpenCode, crew.KindAgy} {
		if _, err := crew.SetupFor(crew.SetupInput{Kind: k, Host: crew.HostWindow, DataDir: "data", Env: map[string]string{"PATH": "/x"}}, ""); !errors.Is(err, crew.ErrDataDir) {
			t.Errorf("%s with a relative data folder: %v, want ErrDataDir", k, err)
		}
	}
	if _, err := crew.SetupFor(crew.SetupInput{Kind: crew.KindOpenCode, Host: crew.HostWindow, Env: map[string]string{"PATH": "/x"}}, ""); !errors.Is(err, crew.ErrDataDir) {
		t.Errorf("OpenCode with no data folder: %v, want ErrDataDir", err)
	}
	if entries, _ := os.ReadDir(cwd); len(entries) != 0 {
		t.Errorf("the working folder holds %d entries after the refused setups, want none", len(entries))
	}
	data := t.TempDir()
	if _, err := crew.SetupFor(crew.SetupInput{Kind: crew.KindOpenCode, Host: crew.HostWindow, DataDir: data, Env: map[string]string{"PATH": "/x"}}, ""); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(data, "opencode", crew.OpenCodePluginName)); err != nil {
		t.Errorf("OpenCode's plugin isn't in its absolute data folder: %v", err)
	}
}

// TestSetupForClaudeTakesTheCrewMCPServer: a Claude agent given the crew's MCP server gets it on its
// command line on both hosts (--mcp-config, the server's command and args, no env of its own), never
// --strict-mcp-config (which would take the user's own servers away), exactly its tools allowed by
// their mcp__<server>__<tool> rules and no shell rule for its mail (least privilege; Neovim's
// --settings carries them), and its mail via MCP; without servers it keeps the guide's commands
// allowed (P6's), its mail via the CLI.
func TestSetupForClaudeTakesTheCrewMCPServer(t *testing.T) {
	full := claudeOnPath(t, everyClaudeEvent...)
	servers := []crew.MCPServer{{Name: "autodoc", Command: []string{"/x/autodoc", "crew", "mcp"}, Tools: []string{"mail_receive", "mail_ack"}}}
	rules := []string{"Bash(autodoc --contracts:*)", "mcp__autodoc__mail_receive", "mcp__autodoc__mail_ack"}
	for _, host := range []string{crew.HostWindow, crew.HostNvim} {
		in := crew.SetupInput{Kind: crew.KindClaude, Host: host, Autodoc: "a", Env: map[string]string{"PATH": full}, Dir: t.TempDir(), DataDir: t.TempDir(), Servers: servers}
		s, err := crew.SetupFor(in, "")
		if err != nil {
			t.Fatal(err)
		}
		i := slices.Index(s.Argv, "--mcp-config")
		if i < 0 || i+1 >= len(s.Argv) || slices.Contains(s.Argv, "--strict-mcp-config") {
			t.Fatalf("%s: argv %q, want --mcp-config and never --strict-mcp-config", host, s.Argv)
		}
		var cfg struct {
			Servers map[string]map[string]any `json:"mcpServers"`
		}
		if err := json.Unmarshal([]byte(s.Argv[i+1]), &cfg); err != nil || len(cfg.Servers) != 1 || cfg.Servers["autodoc"]["command"] != "/x/autodoc" || cfg.Servers["autodoc"]["env"] != nil {
			t.Errorf("%s: --mcp-config %s (%v), want the autodoc server's command, args, and no env", host, s.Argv[i+1], err)
		}
		if !slices.Equal(s.Allow, rules) { // least privilege: its tools and the read-only contracts, no shell rule for its mail
			t.Errorf("%s: Allow %q, want exactly %q", host, s.Allow, rules)
		}
		if slices.ContainsFunc(s.Allow, func(r string) bool { return strings.HasPrefix(r, "mcp__") && strings.Contains(r, "*") }) {
			t.Errorf("%s: a wildcard MCP rule in %q", host, s.Allow)
		}
		if host == crew.HostNvim {
			j := slices.Index(s.Argv, "--settings")
			if j < 0 || !strings.Contains(s.Argv[j+1], `"mcp__autodoc__mail_receive"`) {
				t.Errorf("nvim: --settings %q, want its allow to carry the MCP rules", s.Argv)
			}
		}
		if s.MailVia != crew.MailViaMCP {
			t.Errorf("%s: mail via %v, want MCP", host, s.MailVia)
		}
		in.Servers = nil
		without, err := crew.SetupFor(in, "")
		if err != nil || slices.Contains(without.Argv, "--mcp-config") || without.MailVia != crew.MailViaCLI || !slices.Equal(without.Allow, crew.ClaudeAllow()) {
			t.Errorf("%s without servers: %+v %v, want no MCP, the guide's commands allowed (P6's), and mail via the CLI", host, without, err)
		}
	}
}

// TestSetupForOpenCodeTakesTheCrewMCPServer: an OpenCode agent given the crew's MCP server gets it in
// OPENCODE_CONFIG_CONTENT's mcp key (a local server: its command and args as one list, enabled), the
// user's own MCP servers and plugins kept, no permission written (OpenCode gates no MCP tool), and its
// mail via MCP, its plugin told so (EnvMailVia, the wake's notice); without servers there is no mcp
// key, its mail via the CLI, whatever the host's own environment says (the mutant: the mode unset).
func TestSetupForOpenCodeTakesTheCrewMCPServer(t *testing.T) {
	servers := []crew.MCPServer{{Name: "autodoc", Command: []string{"/x/autodoc", "crew", "mcp"}, Tools: []string{"mail_receive"}}}
	own := `{"mcp":{"mine":{"type":"remote","url":"https://example.invalid/mcp"}},"plugin":["file:///p/mine.js"]}`
	in := crew.SetupInput{Kind: crew.KindOpenCode, Host: crew.HostWindow, DataDir: t.TempDir(), Servers: servers,
		Env: map[string]string{"PATH": "/x", "OPENCODE_CONFIG_CONTENT": own}}
	s, err := crew.SetupFor(in, "")
	if err != nil {
		t.Fatal(err)
	}
	var cfg struct {
		MCP        map[string]map[string]any `json:"mcp"`
		Plugin     []string                  `json:"plugin"`
		Permission struct {
			Bash map[string]string `json:"bash"`
		} `json:"permission"`
	}
	if err := json.Unmarshal([]byte(s.Env["OPENCODE_CONFIG_CONTENT"]), &cfg); err != nil {
		t.Fatal(err)
	}
	a := cfg.MCP["autodoc"]
	cmd, _ := a["command"].([]any)
	if a["type"] != "local" || a["enabled"] != true || len(cmd) != 3 || cmd[0] != "/x/autodoc" || cmd[1] != "crew" || cmd[2] != "mcp" || a["environment"] != nil {
		t.Errorf("the autodoc server %v, want local, enabled, its command and args, no environment", a)
	}
	if cfg.MCP["mine"]["url"] != "https://example.invalid/mcp" || !slices.Contains(cfg.Plugin, "file:///p/mine.js") {
		t.Errorf("the user's own MCP server or plugin was lost: %+v", cfg)
	}
	// least privilege, as Claude's: on MCP mail none of the guide's mail commands, only the read-only
	// contracts its guide points to (OpenCode gates no MCP tool)
	contracts := map[string]string{"autodoc --contracts": "allow", "autodoc --contracts *": "allow"}
	if !maps.Equal(cfg.Permission.Bash, contracts) || !slices.Equal(s.Allow, []string{"autodoc --contracts", "autodoc --contracts *"}) ||
		s.MailVia != crew.MailViaMCP || s.Env[crew.EnvMailVia] != "mcp" {
		t.Errorf("permission.bash %v, Allow %q, mail via %v, the plugin's mode %q: want the contracts alone and MCP, for the plugin too (its wake's notice)",
			cfg.Permission.Bash, s.Allow, s.MailVia, s.Env[crew.EnvMailVia])
	}
	in.Servers = nil
	in.Env[crew.EnvMailVia] = "mcp" // the host's own value never decides the plugin's mode
	without, err := crew.SetupFor(in, "")
	if err != nil || strings.Contains(without.Env["OPENCODE_CONFIG_CONTENT"], `"autodoc":{`) || without.MailVia != crew.MailViaCLI || without.Env[crew.EnvMailVia] != "cli" {
		t.Errorf("without servers: %v %v, want no autodoc MCP server and mail via the CLI, for the plugin too", without.Env, err)
	}
}

// TestMailViaIsCLIOrMCPOnTheWire: a mail mode reads and writes as cli or mcp, a setup's answer names
// it as mail_via, and any other name is refused, never taken as the CLI's.
func TestMailViaIsCLIOrMCPOnTheWire(t *testing.T) {
	for v, name := range map[crew.MailVia]string{crew.MailViaCLI: "cli", crew.MailViaMCP: "mcp"} {
		b, err := json.Marshal(crew.Setup{MailVia: v})
		if err != nil || !strings.Contains(string(b), `"mail_via":"`+name+`"`) {
			t.Errorf("%v: %s %v, want mail_via %q", v, b, err, name)
		}
		var back crew.MailVia
		if err := back.UnmarshalText([]byte(name)); err != nil || back != v {
			t.Errorf("%q reads as %v, %v", name, back, err)
		}
	}
	var v crew.MailVia
	if err := v.UnmarshalText([]byte("smtp")); err == nil {
		t.Error("an unknown mail mode was taken")
	}
}

// TestSetupForCodexSpellsOnlyWhatTOMLCanTakeBare: what a Codex agent's server gets on its command line
// is spelled as TOML `-c` keys, so each name in a key (the MCP server's, an env name, a tool's) must
// be a bare key, or it could close the key and set another (`autodoc.command=x`); a server with no
// command has nothing to run. Each is refused with its name, and no server is given. An env value
// goes in quoted (a quote in it escaped); a program by path starts the server. With no agent folder
// the MCP server can't be given (its socket would have nowhere to go): the setup says so once, and
// the agent stays on the CLI's mail.
func TestSetupForCodexSpellsOnlyWhatTOMLCanTakeBare(t *testing.T) {
	agent := t.TempDir()
	server := func(mut func(*crew.MCPServer)) []crew.MCPServer {
		sv := crew.MCPServer{Name: "autodoc", Command: []string{"/x/autodoc", "crew", "mcp"}, Env: map[string]string{"AUTODOC_X": `a"b`}, Tools: []string{"mail_receive"}}
		if mut != nil {
			mut(&sv)
		}
		return []crew.MCPServer{sv}
	}
	s, err := crew.SetupFor(crew.SetupInput{Kind: crew.KindCodex, Host: crew.HostNvim, Program: "/opt/codex/bin/codex", AgentDir: agent, Servers: server(nil)}, "")
	if err != nil {
		t.Fatal(err)
	}
	if len(s.Server) == 0 || s.Server[0] != "/opt/codex/bin/codex" || !slices.Contains(s.Server, `mcp_servers.autodoc.env.AUTODOC_X="a\"b"`) {
		t.Fatalf("its server %q: want it started by /opt/codex/bin/codex, the env value quoted with its quote escaped", s.Server)
	}
	for name, c := range map[string]struct {
		mut  func(*crew.MCPServer)
		want string
	}{
		"no command":             {func(sv *crew.MCPServer) { sv.Command = nil }, `MCP server "autodoc" has no command`},
		"a server name not bare": {func(sv *crew.MCPServer) { sv.Name = "autodoc.command" }, `MCP server name "autodoc.command" isn't a bare TOML key`},
		"an env name not bare":   {func(sv *crew.MCPServer) { sv.Env = map[string]string{"A=B": "v"} }, `env name "A=B" isn't a bare TOML key`},
		"a tool name not bare":   {func(sv *crew.MCPServer) { sv.Tools = []string{"mail receive"} }, `MCP tool name "mail receive" isn't a bare TOML key`},
	} {
		s, err := crew.SetupFor(crew.SetupInput{Kind: crew.KindCodex, Host: crew.HostNvim, AgentDir: agent, Servers: server(c.mut)}, "")
		if err == nil || !strings.Contains(err.Error(), c.want) || len(s.Server) != 0 {
			t.Errorf("%s: %v, server %q; want refused with %q and no server", name, err, s.Server, c.want)
		}
	}
	nf, err := crew.SetupFor(crew.SetupInput{Kind: crew.KindCodex, Host: crew.HostNvim, Servers: server(nil)}, "")
	if err != nil || len(nf.Server) != 0 || nf.MailVia != crew.MailViaCLI || len(nf.Notices) != 1 || !strings.Contains(nf.Notices[0], "needs the agent's own folder") {
		t.Fatalf("no agent folder: %+v %v; want no server, the CLI's mail, and it said once", nf, err)
	}
}

// TestTheCodexServerRunsCommandsWithoutALoginShell: the app-server a Codex agent's commands run in is
// started with allow_login_shell=false, on its own command line (never in ~/.codex): a login shell
// sources the user's profile, whose PATH (~/.local/bin first) puts the user's own autodoc ahead of the
// agent's, so the agent spoke to the crew through another build than its host's.
func TestTheCodexServerRunsCommandsWithoutALoginShell(t *testing.T) {
	servers := []crew.MCPServer{{Name: "autodoc", Command: []string{"/x/autodoc", "crew", "mcp"}}}
	for _, host := range []string{crew.HostWindow, crew.HostNvim} {
		s, err := crew.SetupFor(crew.SetupInput{Kind: crew.KindCodex, Host: host, AgentDir: t.TempDir(), Servers: servers}, "")
		if err != nil {
			t.Fatal(err)
		}
		at := slices.Index(s.Server, "allow_login_shell=false")
		if at < 1 || s.Server[at-1] != "-c" {
			t.Fatalf("%s: its server %q, want -c allow_login_shell=false", host, s.Server)
		}
		if slices.Contains(s.Argv, "allow_login_shell=false") {
			t.Errorf("%s: the TUI's argv %q has it too: a TUI given -c runs embedded, off its own server", host, s.Argv)
		}
	}
}

// TestSetupForCodexGetsItsOwnAppServer: a Codex agent given the crew's MCP server gets an app-server
// of its own (Server, its socket in the agent's folder) with the server on its command line and each
// crew tool approved by name, never the server's whole default; its TUI attaches to it (--remote);
// its autodoc is told the socket (EnvCodexServer); its mail is MCP's; its registration step stays.
// The session's context reaches the MCP server by name (env_vars): Codex starts a stdio server with a
// filtered environment, and `crew mcp` without it found no session (R4).
// Without servers it is as before: the step alone, the CLI's mail (the mutants: a whole-server
// default; the TUI given -c overrides, which runs it embedded, off every app-server).
func TestSetupForCodexGetsItsOwnAppServer(t *testing.T) {
	agent := t.TempDir()
	servers := []crew.MCPServer{{Name: "autodoc", Command: []string{"/x/autodoc", "crew", "mcp"}, Tools: []string{"mail_receive", "crew_whoami"}}}
	s, err := crew.SetupFor(crew.SetupInput{Kind: crew.KindCodex, Host: crew.HostWindow, AgentDir: agent, Servers: servers}, "")
	if err != nil {
		t.Fatal(err)
	}
	sock := filepath.Join(agent, crew.CodexServerSocket)
	want := []string{"codex", "app-server", "--listen", "unix://" + sock, "-c", "allow_login_shell=false",
		"-c", `mcp_servers.autodoc.command="/x/autodoc"`, "-c", `mcp_servers.autodoc.args=["crew","mcp"]`,
		"-c", `mcp_servers.autodoc.env_vars=["AUTODOC_CREW_CONTEXT"]`,
		"-c", `mcp_servers.autodoc.tools.mail_receive.approval_mode="approve"`, "-c", `mcp_servers.autodoc.tools.crew_whoami.approval_mode="approve"`}
	if !slices.Equal(s.Server, want) || s.ServerSocket != sock {
		t.Fatalf("the server %q at %q, want %q at %q", s.Server, s.ServerSocket, want, sock)
	}
	if !slices.Equal(s.Argv, []string{"--remote", "unix://" + sock}) || s.Env[crew.EnvCodexServer] != sock || s.MailVia != crew.MailViaMCP {
		t.Fatalf("argv %q, env %v, mail via %v: want the TUI on its server, its autodoc told it, MCP mail", s.Argv, s.Env, s.MailVia)
	}
	for _, a := range append(slices.Clone(s.Argv), s.Server...) {
		if strings.Contains(a, "default_tools_approval_mode") {
			t.Fatalf("a whole-server approval default: %q", a)
		}
	}
	if len(s.After) != 1 || s.After[0][2] != "--codex-here" {
		t.Fatalf("its registration step: %q", s.After)
	}
}

// TestSetupForCodexOnCLIMailGetsItsOwnAppServerToo: a Codex agent with its own folder and no crew MCP
// server (CLI mail: the user's choice, or a daemon that offers none) still gets its own app-server,
// the TUI on it, and its registration step: its commands run with its own environment, and its
// thread is found there to wake it, never on Codex's shared daemon (item 14, N1).
func TestSetupForCodexOnCLIMailGetsItsOwnAppServerToo(t *testing.T) {
	agent := t.TempDir()
	sock := filepath.Join(agent, crew.CodexServerSocket)
	cli, err := crew.SetupFor(crew.SetupInput{Kind: crew.KindCodex, Host: crew.HostWindow, AgentDir: agent}, "")
	if err != nil || !slices.Equal(cli.Server, []string{"codex", "app-server", "--listen", "unix://" + sock, "-c", "allow_login_shell=false"}) || cli.ServerSocket != sock {
		t.Fatalf("CLI mail: the server %q at %q (%v), want its own app-server with no MCP server", cli.Server, cli.ServerSocket, err)
	}
	if !slices.Equal(cli.Argv, []string{"--remote", "unix://" + sock}) || cli.Env[crew.EnvCodexServer] != sock || cli.MailVia != crew.MailViaCLI {
		t.Fatalf("CLI mail: argv %q, env %v, mail via %v: want the TUI on its server, its autodoc told it, the CLI's mail", cli.Argv, cli.Env, cli.MailVia)
	}
	if len(cli.After) != 1 || cli.After[0][2] != "--codex-here" || len(cli.Notices) != 0 {
		t.Fatalf("CLI mail: steps %q, notices %q: want its registration, nothing to say", cli.After, cli.Notices)
	}
	p, err := crew.SetupFor(crew.SetupInput{Kind: crew.KindCodex, Host: crew.HostNvim, Program: "/opt/codex/bin/codex", AgentDir: agent}, "")
	if err != nil || len(p.Server) == 0 || p.Server[0] != "/opt/codex/bin/codex" {
		t.Fatalf("a program by path: the server %q (%v), want it started by that program", p.Server, err)
	}
}

// TestSetupForCodexWithNoFolderStaysOffTheSharedDaemon: a Codex agent with no folder of its own has
// no server of its own. A Codex that takes --no-daemon (its --help says) is started with it, so it
// runs its own embedded server, never Codex's shared daemon with another agent's identity; it has no
// registration step, since there is no server to find its thread on, and the setup says once that
// it can't be woken. A Codex before the flag is started as before (the step, no flag): it would
// refuse the flag, and it has no shared daemon to keep off.
func TestSetupForCodexWithNoFolderStaysOffTheSharedDaemon(t *testing.T) {
	codexOnPath := func(help string) string {
		bin := t.TempDir()
		script := "#!/bin/sh\n[ \"$1\" = --help ] && printf '%s\\n' \"" + help + "\"\nexit 0\n"
		if err := os.WriteFile(filepath.Join(bin, "codex"), []byte(script), 0o755); err != nil {
			t.Fatal(err)
		}
		return bin
	}
	newer := codexOnPath("      --no-daemon   Run without the shared background server")
	for _, servers := range [][]crew.MCPServer{nil, {{Name: "autodoc", Command: []string{"/x/autodoc", "crew", "mcp"}}}} {
		s, err := crew.SetupFor(crew.SetupInput{Kind: crew.KindCodex, Host: crew.HostNvim, Env: map[string]string{"PATH": newer}, Dir: t.TempDir(), Servers: servers}, "")
		if err != nil || !slices.Equal(s.Argv, []string{"--no-daemon"}) || len(s.Server) != 0 || len(s.After) != 0 || s.MailVia != crew.MailViaCLI {
			t.Fatalf("no folder, %d servers: %+v %v; want --no-daemon, no server, no step, the CLI's mail", len(servers), s, err)
		}
		woken := slices.IndexFunc(s.Notices, func(n string) bool { return strings.Contains(n, "can't wake it") })
		if woken < 0 || slices.IndexFunc(s.Notices[woken+1:], func(n string) bool { return strings.Contains(n, "can't wake it") }) >= 0 {
			t.Fatalf("no folder, %d servers: notices %q; want it said once that crew mail can't wake it", len(servers), s.Notices)
		}
	}
	older := codexOnPath("      --remote <ADDR>   Connect the TUI to a remote app server endpoint")
	s, err := crew.SetupFor(crew.SetupInput{Kind: crew.KindCodex, Host: crew.HostNvim, Env: map[string]string{"PATH": older}, Dir: t.TempDir()}, "")
	if err != nil || len(s.Argv) != 0 || len(s.After) != 1 || s.After[0][2] != "--codex-here" {
		t.Fatalf("a Codex without --no-daemon: %+v %v; want no flag and the registration step, as before", s, err)
	}
}
