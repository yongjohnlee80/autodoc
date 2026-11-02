package tui

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestAProfileSetToClaudeUnderAnotherCommandIsAClaudeAgent (the claude-work profile): a profile whose
// command is a wrapper of another name, `claude-work`, set to kind claude, is a crew Claude agent: its
// session is kind claude, its settings take wakes and report prompts for the Claude Code its wrapper
// runs (found as claude on its PATH), and its prompts are reported. Its kind is never the command's
// name.
func TestAProfileSetToClaudeUnderAnotherCommandIsAClaudeAgent(t *testing.T) {
	bin := t.TempDir()
	fakeClaudeBin(t, bin, append(claudeHookEvents, "crossSessionInbound")...)
	wrapper := "#!/bin/sh\nCLAUDE_CONFIG_DIR=\"$HOME/.claude-work\" exec claude \"$@\"\n"
	if err := os.WriteFile(filepath.Join(bin, "claude-work"), []byte(wrapper), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", "/usr/bin:/bin") // the wrapper and its Claude Code are on the profile's PATH alone
	a, c, w := crewAgents(t, `[{"name":"Sephiroth","command":"claude-work","codename":"sephiroth","kind":"claude","env":{"PATH":"`+bin+`:$PATH"}}]`)
	p, _ := a.profile("sephiroth")
	instance, err := a.startInstance(p)
	if err != nil {
		t.Fatal(err)
	}
	dir := c.panel.configs[instance.tab].Dir
	if ctx, _, ok := readContext(t, dir); !ok || ctx.Kind != "claude" {
		t.Fatalf("its session's kind: %+v (%v), want claude, the profile's, never the command's name", ctx, ok)
	}
	s, b := claudeSettingsOf(t, dir)
	if s["crossSessionInbound"] != "accept" || !strings.Contains(b, "crew endpoint --claude") || !strings.Contains(b, "PermissionRequest") {
		t.Fatalf("its settings, for the Claude Code its wrapper runs, don't accept wakes or report prompts:\n%s", b)
	}
	if !promptsReported(t, w, "sephiroth") {
		t.Error("its prompts aren't reported: the Claude Code behind its wrapper was not found")
	}
	if n := unreportedNotices(c.notes); n != 0 {
		t.Errorf("notices %v: its Claude Code was read", c.notes)
	}
}
