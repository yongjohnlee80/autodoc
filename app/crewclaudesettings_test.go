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
func TestCrewClaudeSettingsPrintOnlyWhatTheClaudeKnows(t *testing.T) {
	dir := t.TempDir()
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
	var out, errOut bytes.Buffer
	if code := crewMain(t.Context(), []string{"crew", "claude-settings", "--claude", write("script", "#!/bin/sh")}, nil, &out, &errOut); code != 1 || out.Len() != 0 {
		t.Errorf("a file that is no Claude Code: exit %d, %q; want 1 and nothing printed", code, out.String())
	}
}
