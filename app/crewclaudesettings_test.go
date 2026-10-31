package app

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// TestCrewClaudeSettingsPrintOnlyWhatTheClaudeKnows: `autodoc crew claude-settings --claude P`
// prints the settings `claude --settings` takes, for the Claude Code at P: every hook, with
// prompts, for one that knows them; SessionStart alone, without prompts, for one lacking
// PermissionRequest; each hook run by --autodoc's command. --prompts prints whether they report.
// What each binary knows is kept beside the store, for the next run.
func TestCrewClaudeSettingsPrintOnlyWhatTheClaudeKnows(t *testing.T) {
	dir, data := t.TempDir(), t.TempDir()
	t.Setenv("XDG_DATA_HOME", data)
	t.Setenv("XDG_CONFIG_HOME", t.TempDir()) // the default config: none, so its data folder is XDG_DATA_HOME's
	write := func(name string, events ...string) string {
		var b strings.Builder
		for _, e := range events {
			b.WriteString(`"` + e + `" `)
		}
		path := filepath.Join(dir, name)
		if err := os.WriteFile(path, []byte(b.String()), 0o755); err != nil {
			t.Fatal(err)
		}
		return path
	}
	full := write("full", "SessionStart", "crossSessionInbound", "PermissionRequest", "Notification", "PostToolUse",
		"PostToolUseFailure", "UserPromptSubmit", "Stop")
	old := write("old", "SessionStart", "crossSessionInbound", "Notification", "PostToolUse", "UserPromptSubmit", "Stop")
	for _, c := range []struct {
		bin     string
		events  []string
		prompts string
	}{
		{full, []string{"Notification", "PermissionRequest", "PostToolUse", "PostToolUseFailure", "SessionStart", "Stop", "UserPromptSubmit"}, "true"},
		{old, []string{"SessionStart"}, "false"},
	} {
		var out, errOut bytes.Buffer
		if code := crewMain(t.Context(), []string{"crew", "claude-settings", "--claude", c.bin, "--autodoc", "/x/autodoc"}, nil, &out, &errOut); code != 0 {
			t.Fatalf("%s: exit %d, %s", c.bin, code, errOut.String())
		}
		var s struct {
			Hooks map[string][]struct {
				Hooks []struct{ Type, Command string }
			}
			CrossSessionInbound string
		}
		if err := json.Unmarshal(out.Bytes(), &s); err != nil {
			t.Fatalf("%s: %v: %s", c.bin, err, out.String())
		}
		var named []string
		for e, groups := range s.Hooks {
			named = append(named, e)
			if len(groups) != 1 || len(groups[0].Hooks) != 1 || !strings.HasPrefix(groups[0].Hooks[0].Command, "/x/autodoc crew ") {
				t.Errorf("%s: %s's hooks %+v", c.bin, e, groups)
			}
		}
		slices.Sort(named)
		if !slices.Equal(named, c.events) || s.CrossSessionInbound != "accept" {
			t.Errorf("%s: hooks %v, crossSessionInbound %q; want %v, accept", c.bin, named, s.CrossSessionInbound, c.events)
		}
		out.Reset()
		if code := crewMain(t.Context(), []string{"crew", "claude-settings", "--claude", c.bin, "--prompts"}, nil, &out, &errOut); code != 0 || out.String() != c.prompts+"\n" {
			t.Errorf("%s --prompts: exit %d, %q; want %s", c.bin, code, out.String(), c.prompts)
		}
	}
	if b, err := os.ReadFile(filepath.Join(data, "autodoc", "claude-hooks.json")); err != nil || !strings.Contains(string(b), full) || !strings.Contains(string(b), old) {
		t.Errorf("the kept answers: %q, %v; want both binaries'", b, err)
	}
	var out, errOut bytes.Buffer
	if code := crewMain(t.Context(), []string{"crew", "claude-settings", "--claude", write("script", "#!/bin/sh")}, nil, &out, &errOut); code != 1 || out.Len() != 0 {
		t.Errorf("a file that is no Claude Code: exit %d, %q; want 1 and nothing printed", code, out.String())
	}
}

// TestCrewFilesGoInTheConfigsDataFolder: the files crew plugin and crew claude-settings keep go in
// the data folder of the config they run under: --config's server.data_dir, else the default
// config's, never $XDG_DATA_HOME's when a config names another; a config that can't be read
// writes nothing (the plugin is refused, the settings print with nothing kept).
func TestCrewFilesGoInTheConfigsDataFolder(t *testing.T) {
	xdg, home := t.TempDir(), t.TempDir()
	t.Setenv("XDG_DATA_HOME", xdg)
	t.Setenv("XDG_CONFIG_HOME", home)
	writeFile := func(path, text string, mode os.FileMode) string {
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(text), mode); err != nil {
			t.Fatal(err)
		}
		return path
	}
	claude := writeFile(filepath.Join(t.TempDir(), "claude"), `"SessionStart" "crossSessionInbound" `, 0o755)
	inXDG := func() []string {
		var found []string
		for _, f := range []string{filepath.Join(xdg, "autodoc", "opencode", opencodePluginName), filepath.Join(xdg, "autodoc", "claude-hooks.json")} {
			if _, err := os.Stat(f); err == nil {
				found = append(found, f)
			}
		}
		return found
	}
	crewRun := func(args ...string) (int, string, string) {
		var out, errOut bytes.Buffer
		code := run(t.Context(), args, Options{Version: "v1.2.3"}, &out, &errOut)
		return code, strings.TrimSpace(out.String()), errOut.String()
	}

	other := t.TempDir()
	cfg := writeFile(filepath.Join(t.TempDir(), "other.toml"), "[server]\ndata_dir = \""+other+"\"\n", 0o600)
	for _, flagForm := range [][]string{{"--config", cfg}, {"--config=" + cfg}} {
		if code, out, errOut := crewRun(append(flagForm, "crew", "plugin", "--opencode")...); code != 0 || out != filepath.Join(other, "opencode", opencodePluginName) {
			t.Errorf("%v crew plugin: exit %d, %q, %s; want the plugin in --config's data_dir", flagForm, code, out, errOut)
		}
	}
	if code, _, errOut := crewRun("--config", cfg, "crew", "claude-settings", "--claude", claude); code != 0 {
		t.Fatalf("crew claude-settings: exit %d, %s", code, errOut)
	}
	if _, err := os.Stat(filepath.Join(other, "claude-hooks.json")); err != nil {
		t.Errorf("crew claude-settings kept nothing in --config's data_dir: %v", err)
	}
	if found := inXDG(); len(found) > 0 {
		t.Errorf("a run under --config wrote into $XDG_DATA_HOME: %v", found)
	}

	// no --config: the default config's data_dir, when it names one
	def := t.TempDir()
	writeFile(filepath.Join(home, "autodoc", "config.toml"), "[server]\ndata_dir = \""+def+"\"\n", 0o600)
	if code, out, errOut := crewRun("crew", "plugin", "--opencode"); code != 0 || out != filepath.Join(def, "opencode", opencodePluginName) {
		t.Errorf("crew plugin under the default config: exit %d, %q, %s; want its data_dir", code, out, errOut)
	}

	// a config that can't be read: nothing written anywhere
	bad := writeFile(filepath.Join(t.TempDir(), "bad.toml"), "[server]\nno_such_setting = 1\n", 0o600)
	if code, out, _ := crewRun("--config", bad, "crew", "plugin", "--opencode"); code != 1 || out != "" {
		t.Errorf("crew plugin under an invalid config: exit %d, %q; want refused", code, out)
	}
	if code, out, errOut := crewRun("--config", bad, "crew", "claude-settings", "--claude", claude); code != 0 || !strings.Contains(out, "SessionStart") {
		t.Errorf("crew claude-settings under an invalid config: exit %d, %q, %s; want the settings, kept nowhere", code, out, errOut)
	}
	if found := inXDG(); len(found) > 0 {
		t.Errorf("a run under an invalid config wrote into $XDG_DATA_HOME: %v", found)
	}
}
