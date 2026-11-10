package tui

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// TestAClaudeProfileOnAnotherAccountIsAClaudeAgent (the Sephiroth profile): `claude` with
// CLAUDE_CONFIG_DIR in its env, and the Claude Code found on its env's PATH alone, is a crew Claude
// agent: kind claude, the SessionStart hook and crossSessionInbound written, its prompts reported,
// and its process given CLAUDE_CONFIG_DIR expanded, with AutoDoc's own folder first on its PATH.
func TestAClaudeProfileOnAnotherAccountIsAClaudeAgent(t *testing.T) {
	bin := t.TempDir()
	fakeClaudeBin(t, bin, append(claudeHookEvents, "crossSessionInbound")...)
	tuneEnv(t, "PATH=/usr/bin:/bin") // the Claude Code is on the profile's PATH, not AutoDoc's
	a, c, w := crewAgents(t, `[{"name":"Sephiroth","command":"claude","codename":"sephiroth","env":{"CLAUDE_CONFIG_DIR":"~/.claude-work","PATH":"`+bin+`:$PATH"}}]`)
	p, _ := a.profile("sephiroth")
	instance, err := a.startInstance(p)
	if err != nil {
		t.Fatal(err)
	}
	dir := c.panel.configs[instance.State().Tab].Dir
	if ctx, _, ok := readContext(t, dir); !ok || ctx.Kind != "claude" {
		t.Fatalf("its session's kind: %+v (%v), want claude", ctx, ok)
	}
	s, b := claudeSettingsOf(t, dir)
	if s["crossSessionInbound"] != "accept" || !strings.Contains(b, "crew endpoint --claude") || !strings.Contains(b, "PermissionRequest") {
		t.Fatalf("its settings, for the Claude Code on its PATH, don't accept wakes or report prompts:\n%s", b)
	}
	if !promptsReported(t, w, "sephiroth") {
		t.Error("its prompts aren't reported: its Claude Code was not found on the profile's PATH")
	}
	home, _ := os.UserHomeDir()
	args, _, err := startedEnvCopy(a, c, instance)
	if err != nil || !slices.Contains(args, "CLAUDE_CONFIG_DIR="+filepath.Join(home, ".claude-work")) ||
		!slices.Contains(args, "PATH="+a.agentPathDir(dir)+string(os.PathListSeparator)+bin+string(os.PathListSeparator)+"/usr/bin:/bin") {
		t.Fatalf("its process starts with %v (%v), want CLAUDE_CONFIG_DIR expanded and its own bin, then the profile's PATH", args, err)
	}
	if n := unreportedNotices(c.notes); n != 0 {
		t.Errorf("notices %v: its Claude Code was read", c.notes)
	}
}
