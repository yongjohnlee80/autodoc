package tui

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
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
			p, _ := a.profile(codename)
			instance, err := a.startInstance(p)
			if err == nil {
				awaitStart(t, c, instance)
			}
			if err != nil || !instance.running {
				t.Fatalf("start: %v", err)
			}
			dir := c.panel.configs[instance.tab].Dir
			want := crew.GuideOfVia(kind, instance.setup.MailVia).Markdown // the mode its setup delivered
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
	if g := agentGuide(wsInfo{name: "kb", root: "/kb"}, "/bin/autodoc", "", "/w/bin/autodoc", "", crew.MailViaCLI); strings.Contains(g, "## Mail and your crew") {
		t.Fatal("an agent outside a crew scope is told the crew procedure")
	}
}

// TestACrewOpenCodeAgentLoadsTheAutoDocPlugin: a crew OpenCode agent starts with its setup's
// OPENCODE_CONFIG_CONTENT (AutoDoc's plugin; on MCP mail its server too); a setup that gives none,
// or one that fails, starts nothing, rather than an agent no mail can reach.
func TestACrewOpenCodeAgentLoadsTheAutoDocPlugin(t *testing.T) {
	const v = `{"plugin":["file:///p/autodoc-crew.js"]}`
	for name, tc := range map[string]struct {
		setup crew.Setup
		err   error
		runs  bool
	}{
		"its setup's env":    {setup: crew.Setup{Env: map[string]string{"OPENCODE_CONFIG_CONTENT": v}}, runs: true},
		"a setup with none":  {setup: crew.Setup{}},
		"a setup that fails": {err: errors.New("crew setup: exit status 1")},
	} {
		t.Run(name, func(t *testing.T) {
			a, c, _ := crewAgents(t, crewKinds)
			a.setupRun = func(string, string, []string) (crew.Setup, error) { return tc.setup, tc.err }
			p, _ := a.profile("opencode-one")
			instance, err := a.startInstance(p)
			if err != nil {
				t.Fatal(err)
			}
			awaitStart(t, c, instance)
			if instance.running != tc.runs {
				t.Fatalf("running %v, want %v (notes %v)", instance.running, tc.runs, c.notes)
			}
			if tc.runs {
				if args, _, err := startedEnv(a, c, instance); err != nil || !slices.Contains(args, "OPENCODE_CONFIG_CONTENT="+v) {
					t.Fatalf("the spawn: %v, args %v; want the setup's env", err, args)
				}
			}
		})
	}
}

// TestTheWindowAsksTheAgentsOwnCrewSetup: crewSetupOf runs the agent's own autodoc as `crew setup
// --kind K --host window --autodoc <its wrapper> --agent-dir <its folder>` on the TUI's config, in
// the agent's folder, the profile's environment its own: no value of the profile's is on its command
// line, which any user can read. Its answer is the setup, mail_via included; output that is no setup
// is refused.
func TestTheWindowAsksTheAgentsOwnCrewSetup(t *testing.T) {
	rec, agentDir := t.TempDir(), t.TempDir()
	bin := filepath.Join(rec, "autodoc")
	script := "#!/bin/sh\nprintf '%s\\n' \"$@\" > " + shellQuote(filepath.Join(rec, "args")) + "\nenv > " + shellQuote(filepath.Join(rec, "env")) +
		"\npwd > " + shellQuote(filepath.Join(rec, "pwd")) + "\necho '{\"argv\":[],\"env\":{},\"mail_via\":\"mcp\"}'\n"
	if err := os.WriteFile(bin, []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	s, err := crewSetupOf(bin, "/c/autodoc.toml", crew.KindClaude, agentDir, []string{"PATH=/usr/bin:/bin", "ANTHROPIC_API_KEY=s3cr3t-value"})
	if err != nil || s.MailVia != crew.MailViaMCP {
		t.Fatalf("crewSetupOf: %+v %v, want its answer, mail via MCP", s, err)
	}
	read := func(name string) string {
		b, err := os.ReadFile(filepath.Join(rec, name))
		if err != nil {
			t.Fatal(err)
		}
		return string(b)
	}
	want := strings.Join([]string{"--config", "/c/autodoc.toml", "crew", "setup", "--kind", "claude", "--host", "window",
		"--autodoc", shellQuote(filepath.Join(agentDir, "bin", "autodoc")), "--agent-dir", agentDir, "--mail-via", "mcp"}, "\n") + "\n"
	if got := read("args"); got != want {
		t.Errorf("its command line:\n%s\nwant\n%s", got, want)
	}
	if strings.Contains(read("args"), "s3cr3t") || !strings.Contains(read("env"), "ANTHROPIC_API_KEY=s3cr3t-value") {
		t.Error("the profile's value is on the command line, or not in the environment")
	}
	if got := strings.TrimSpace(read("pwd")); got != agentDir {
		t.Errorf("it ran in %q, want the agent's folder %q", got, agentDir)
	}
	if err := os.WriteFile(bin, []byte("#!/bin/sh\necho 'something else'\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	if _, err := crewSetupOf(bin, "", crew.KindClaude, agentDir, nil); err == nil {
		t.Error("output that is no setup was taken")
	}
}

// TestTwoAgentsOfOneKindAskTheirOwnSetup: every crew start asks its own setup, with its own folder
// (agy's trust and folder files, an MCP server's command name it), and starts with its own answer:
// nothing one start's setup answered is kept for another's.
func TestTwoAgentsOfOneKindAskTheirOwnSetup(t *testing.T) {
	a, c, _ := crewAgents(t, `[{"name":"One","command":"opencode","codename":"oc-one"},{"name":"Two","command":"opencode","codename":"oc-two"}]`)
	var dirs []string
	a.setupRun = func(kind, dir string, environ []string) (crew.Setup, error) {
		dirs = append(dirs, dir)
		return crew.Setup{Env: map[string]string{"OPENCODE_CONFIG_CONTENT": `{"for":"` + filepath.Base(dir) + `"}`}}, nil
	}
	for _, codename := range []string{"oc-one", "oc-two"} {
		p, _ := a.profile(codename)
		instance, err := a.startInstance(p)
		if err != nil {
			t.Fatal(err)
		}
		awaitStart(t, c, instance)
		args, _, err := startedEnv(a, c, instance)
		want := `OPENCODE_CONFIG_CONTENT={"for":"` + filepath.Base(instance.dir) + `"}`
		if err != nil || !slices.Contains(args, want) {
			t.Fatalf("%s's spawn: %v, args %v; want its own setup's %s", codename, err, args, want)
		}
	}
	if len(dirs) != 2 || dirs[0] == dirs[1] {
		t.Fatalf("the setups asked: %q, want one each, with their own folders", dirs)
	}
}

// TestACrewClaudeAgentOnMCPMailStartsWithItsServer: a Claude agent whose setup delivers the crew MCP
// server starts with it on its command line (--mcp-config); its folder's settings allow exactly the
// crew MCP tools, never a guide shell command (least privilege); its guide is the MCP form; and its
// session is registered as reading its mail via MCP, which crew.list says.
func TestACrewClaudeAgentOnMCPMailStartsWithItsServer(t *testing.T) {
	a, c, w := crewAgents(t, crewKinds)
	data := t.TempDir()
	a.setupRun = func(kind, dir string, environ []string) (crew.Setup, error) {
		return crew.SetupFor(crew.SetupInput{Kind: kind, Host: crew.HostWindow, Env: map[string]string{"PATH": "/nonexistent"},
			DataDir: data, Dir: dir, AgentDir: dir, Servers: []crew.MCPServer{{Name: crew.MCPServerName,
				Command: []string{filepath.Join(dir, "bin", "autodoc"), "crew", "mcp"}, Tools: crew.MCPToolNames()}}}, "")
	}
	p, _ := a.profile("claude-one")
	instance, err := a.startInstance(p)
	if err != nil {
		t.Fatal(err)
	}
	awaitStart(t, c, instance)
	args, _, err := startedEnv(a, c, instance)
	if err != nil || len(args) == 0 || !strings.Contains(args[len(args)-1], "--mcp-config") || strings.Contains(args[len(args)-1], "--strict-mcp-config") {
		t.Fatalf("the spawn: %v, args %v; want --mcp-config on its command line, never --strict-mcp-config", err, args)
	}
	b, err := os.ReadFile(filepath.Join(instance.dir, ".claude", "settings.json"))
	if err != nil {
		t.Fatal(err)
	}
	var settings struct {
		Permissions struct {
			Allow []string `json:"allow"`
		} `json:"permissions"`
	}
	if err := json.Unmarshal(b, &settings); err != nil || !slices.Equal(settings.Permissions.Allow, crew.ClaudeMCPAllow()) {
		t.Errorf("the folder's allow %q (%v), want exactly the crew MCP tools %q", settings.Permissions.Allow, err, crew.ClaudeMCPAllow())
	}
	g, err := os.ReadFile(filepath.Join(instance.dir, "AGENTS.md"))
	if err != nil || !strings.Contains(string(g), crew.GuideOfVia(crew.KindClaude, crew.MailViaMCP).Markdown) {
		t.Errorf("the guide isn't the MCP form (%v)", err)
	}
	entries, err := w.d.m.List(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if e.Codename == "claude-one" && e.MailVia != "mcp" {
			t.Errorf("crew.list says claude-one reads its mail via %q, want mcp", e.MailVia)
		}
	}
}

// awaitStart runs what the loop is posted until instance's crew start is answered: a crew start
// continues when its setup and then its registration answer, which post from off the loop.
func awaitStart(t *testing.T, c *identityHost, instance *agentInstance) {
	t.Helper()
	for deadline := time.Now().Add(5 * time.Second); instance.starting; time.Sleep(time.Millisecond) {
		if time.Now().After(deadline) {
			t.Fatal("the agent's start was never answered")
		}
		c.drainPosts()
	}
}
