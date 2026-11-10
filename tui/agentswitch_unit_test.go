//go:build linux || darwin

package tui

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// recordingAgent is an agent CLI that, its tty raw, writes every byte it is sent to report. With
// bracketed it asks for bracketed paste (mode 2004), as agent CLIs do.
func recordingAgent(t *testing.T, bracketed bool) (agent, report string) {
	t.Helper()
	dir := t.TempDir()
	agent, report = filepath.Join(dir, "recording-agent"), filepath.Join(dir, "report")
	mode := ""
	if bracketed {
		mode = `printf '\033[?2004h'` + "\n"
	}
	script := "#!/bin/sh\nstty raw -echo\n" + mode + "printf 'ready\\r\\n'\nexec cat > " + shellQuote(report) + "\n"
	if err := os.WriteFile(agent, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	return agent, report
}

func reported(report string) string { b, _ := os.ReadFile(report); return string(b) }

// TestTheNoticeHookIsMergedIntoTheUsersSettings (docs/decisions/crew.md#the-workspace-notice-hook):
// the hook is written into a new file; into one the user wrote it is added once, every key, hook,
// number and group of theirs kept (an empty one too), however often the agent starts; a file that
// isn't one JSON object (a second value or text after it included), or whose hooks aren't Claude's
// shape, is left as it is.
func TestTheNoticeHookIsMergedIntoTheUsersSettings(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, ".claude", "settings.json")
	cmd := noticeCommand(dir)
	ours := func(b []byte) int { return strings.Count(string(b), "--workspace-notice") }
	if err := mergeClaudeHook(path, cmd); err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(path)
	var s map[string]any
	if err := json.Unmarshal(b, &s); err != nil || ours(b) != 1 {
		t.Fatalf("a new file: %v\n%s", err, b)
	}
	if fi, _ := os.Stat(path); fi.Mode().Perm() != 0o600 {
		t.Errorf("the file's mode %v, want 0600", fi.Mode().Perm())
	}

	user := `{"model": "opus", "cleanupPeriodDays": 12345678901234567, "hooks": {"Stop": [{"hooks": [{"type": "command", "command": "say done"}]}],` +
		` "UserPromptSubmit": [{"matcher": "kept-empty", "hooks": []}, {"matcher": "", "hooks": [{"type": "command", "command": "my-own"}, {"type": "command", "command": ` + mustJSON(cmd) + `}]}]}}`
	if err := os.WriteFile(path, []byte(user), 0o600); err != nil {
		t.Fatal(err)
	}
	for range 2 {
		if err := mergeClaudeHook(path, cmd); err != nil {
			t.Fatal(err)
		}
	}
	b, _ = os.ReadFile(path)
	for _, keep := range []string{`"model": "opus"`, `12345678901234567`, `"say done"`, `"my-own"`, `"matcher": ""`, `"matcher": "kept-empty"`} {
		if !strings.Contains(string(b), keep) {
			t.Errorf("the user's %s was lost:\n%s", keep, b)
		}
	}
	if ours(b) != 1 {
		t.Errorf("the hook %d times, want once:\n%s", ours(b), b)
	}

	for _, bad := range []string{"not json", "[1, 2]", "null", `{"hooks": []}`, `{"hooks": {"UserPromptSubmit": {}}}`,
		`{"model": "opus"} {"theirs": true}`, `{"model": "opus"} and some notes`} {
		if err := os.WriteFile(path, []byte(bad), 0o600); err != nil {
			t.Fatal(err)
		}
		if err := mergeClaudeHook(path, cmd); err == nil {
			t.Errorf("%q: merged, want left and said", bad)
		}
		if b, _ := os.ReadFile(path); string(b) != bad {
			t.Errorf("%q was rewritten as %q", bad, b)
		}
	}
}

func mustJSON(s string) string { b, _ := json.Marshal(s); return string(b) }
