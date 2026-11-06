package tui

import (
	"strings"
	"testing"
	"time"

	tuicore "github.com/yongjohnlee80/golib/tui"
	"github.com/yongjohnlee80/golib/tui/decl/decltest"
)

// formAt is the screen cell just inside label's first occurrence, for a click.
func formAt(sc, label string) (int, int) {
	for y, line := range strings.Split(sc, "\n") {
		if before, _, ok := strings.Cut(line, label); ok {
			return len([]rune(before)) + 2, y
		}
	}
	return -1, -1
}

func formClick(t *testing.T, r *running, label string) {
	t.Helper()
	x, y := formAt(r.s.String(), label)
	if x < 0 {
		t.Fatalf("no %q on screen:\n%s", label, r.s.String())
	}
	r.keys(t, tuicore.MouseEvent{Kind: tuicore.MousePress, Button: tuicore.MouseLeft, X: x, Y: y},
		tuicore.MouseEvent{Kind: tuicore.MouseRelease, Button: tuicore.MouseLeft, X: x, Y: y})
	time.Sleep(150 * time.Millisecond)
}

func openAddAgentForm(t *testing.T, profiles []agentProfile) *running {
	t.Helper()
	_, r, _ := runAgentTUI(t, t.TempDir(), profiles, profiles[0].Codename)
	onLoop(r, func() bool { r.h.agents.openAgentProfiles(); return true })
	r.s.WaitForText(t, "CODENAME")
	formClick(t, r, "[ Add… ]")
	r.s.WaitForText(t, "add an agent profile")
	return r
}

func storedProfile(r *running, codename string) (agentProfile, bool) {
	return onLoop(r, func() agentProfile { p, _ := r.h.agents.profile(codename); return p }),
		onLoop(r, func() bool { _, ok := r.h.agents.profile(codename); return ok })
}

// TestChoosingAKindIsEnoughToAddAnAgent is the add-agent form as the window shows it: a name typed,
// Antigravity chosen, no command typed, Save — refused with "a profile is a name and the command
// that starts the agent". Choosing the kind fills in its command, and the profile saves.
func TestChoosingAKindIsEnoughToAddAnAgent(t *testing.T) {
	r := openAddAgentForm(t, []agentProfile{{Name: "claude", Command: "claude", Codename: "claude"}})
	r.keys(t, decltest.Type("Tifa")...)
	formClick(t, r, "the command's")
	formClick(t, r, "Antigravity")
	r.s.WaitForText(t, "agy") // the command field, filled in
	formClick(t, r, "Save")
	p, ok := storedProfile(r, "tifa")
	if !ok || p.Name != "Tifa" || p.Command != "agy" || p.Kind != "agy" {
		t.Fatalf("stored %+v %v, want Tifa / agy, kind agy\n%s", p, ok, r.s.String())
	}
}

// TestAKindAloneNamesTheAgentToo: nothing typed at all, Codex chosen, Save — the profile takes the
// kind's name and its CLI.
func TestAKindAloneNamesTheAgentToo(t *testing.T) {
	r := openAddAgentForm(t, []agentProfile{{Name: "claude", Command: "claude", Codename: "claude"}})
	formClick(t, r, "the command's")
	formClick(t, r, "Codex")
	formClick(t, r, "Save")
	p, ok := storedProfile(r, "codex")
	if !ok || p.Name != "Codex" || p.Command != "codex" || p.Kind != "codex" {
		t.Fatalf("stored %+v %v, want Codex / codex\n%s", p, ok, r.s.String())
	}
}

// TestChoosingAKindKeepsATypedCommand: a command the user typed is theirs; choosing a kind (a
// wrapper of Claude Code is still Claude Code) never replaces it.
func TestChoosingAKindKeepsATypedCommand(t *testing.T) {
	r := openAddAgentForm(t, []agentProfile{{Name: "claude", Command: "claude", Codename: "claude"}})
	tab := tuicore.KeyEvent{Kind: tuicore.KeyPress, Code: tuicore.KeyTab}
	r.keys(t, decltest.Type("Work")...)
	r.keys(t, tab, tab)
	r.keys(t, decltest.Type("claude-work")...)
	formClick(t, r, "the command's")
	formClick(t, r, "Claude Code")
	formClick(t, r, "Save")
	p, ok := storedProfile(r, "work")
	if !ok || p.Command != "claude-work" || p.Kind != "claude" {
		t.Fatalf("stored %+v %v, want the typed claude-work, kind claude", p, ok)
	}
}
