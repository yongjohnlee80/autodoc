//go:build unix

package crew_test

import (
	"encoding/json"
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
	if err := json.Unmarshal([]byte(s.Env["OPENCODE_CONFIG_CONTENT"]), &cfg); err != nil || !slices.Equal(keys, []string{"OPENCODE_CONFIG_CONTENT"}) ||
		cfg.Theme != "x" || len(cfg.Plugin) != 1 || !strings.HasSuffix(cfg.Plugin[0], "/opencode/"+crew.OpenCodePluginName) || len(s.Argv) != 0 {
		t.Errorf("OpenCode's setup: %+v %v, want OPENCODE_CONFIG_CONTENT alone, the agent's own kept and the plugin added", s, err)
	}
}

// TestSetupForAnotherKindIsNothing: a kind with no setup yet answers nothing to add, as a value a
// host can always merge.
func TestSetupForAnotherKindIsNothing(t *testing.T) {
	for _, k := range []string{"agy", "generic"} {
		s, err := crew.SetupFor(crew.SetupInput{Kind: k, Host: crew.HostNvim, Env: map[string]string{"PATH": "/x"}}, "")
		if err != nil || s.Argv == nil || s.Env == nil || s.Files == nil || s.Notices == nil || s.After == nil ||
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
