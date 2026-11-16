//go:build unix

package crew_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/yongjohnlee80/autodoc/core/crew"
)

// setup_unix_test.go: the setup cells whose stand-in is a shell script by what it tests (a roster's
// wrapper script, run by its #! line), which Windows does not run.

// TestSetupForAClaudeWrapperOfAnotherName: a Claude agent whose program is a wrapper of another name
// (a roster's cmd = ["claude_tui.sh", "work"]) gets the prompt hooks of the claude the wrapper runs,
// on Neovim's command line, with no notice: its prompts are reported.
func TestSetupForAClaudeWrapperOfAnotherName(t *testing.T) {
	wrappers := t.TempDir()
	if err := os.WriteFile(filepath.Join(wrappers, "claude_tui.sh"), []byte("#!/bin/bash\nexec claude \"$@\"\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	path := wrappers + string(os.PathListSeparator) + claudeOnPath(t, everyClaudeEvent...)
	s, err := crew.SetupFor(crew.SetupInput{Kind: crew.KindClaude, Host: crew.HostNvim, Program: "claude_tui.sh", Autodoc: "/opt/autodoc wrap", Env: map[string]string{"PATH": path}, Dir: t.TempDir()}, "")
	if err != nil {
		t.Fatal(err)
	}
	if !s.Prompts || len(s.Notices) != 0 || len(s.Argv) != 2 || s.Argv[0] != "--settings" || !strings.Contains(s.Argv[1], "PermissionRequest") {
		t.Fatalf("claude_tui.sh's setup: prompts %v, notices %q, argv %q; want --settings naming PermissionRequest, prompts, no notice", s.Prompts, s.Notices, s.Argv)
	}
}
