//go:build linux || darwin

package tui

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// The SessionStart hook AutoDoc writes runs the build, not an autodoc earlier on the agent's PATH:
// run as its shell runs it, with a fake autodoc first on the PATH, the build is what answers.
func TestAHookRunsTheBuildNotAnAutodocEarlierOnPath(t *testing.T) {
	mark := filepath.Join(t.TempDir(), "ran")
	script := func(dir, said string) string {
		t.Helper()
		if err := os.MkdirAll(dir, 0o700); err != nil {
			t.Fatal(err)
		}
		p := filepath.Join(dir, "autodoc")
		if err := os.WriteFile(p, []byte("#!/bin/sh\necho "+said+" > \"$MARK\"\n"), 0o700); err != nil {
			t.Fatal(err)
		}
		return p
	}
	build := script(filepath.Join(t.TempDir(), "build"), "build")
	fake := script(filepath.Join(t.TempDir(), "fake"), "fake")
	dir := t.TempDir()
	if _, err := writeWakeAcceptance(shellQuote(build), dir, "claude", nil, nil); err != nil {
		t.Fatal(err)
	}
	starts := claudeHookCommands(t, dir)["SessionStart"]
	if len(starts) != 1 {
		t.Fatalf("SessionStart hooks %q", starts)
	}
	cmd := exec.Command("/bin/sh", "-c", starts[0])
	cmd.Env = []string{"PATH=" + filepath.Dir(fake) + ":/usr/bin:/bin", "MARK=" + mark}
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("%s: %v (%s)", starts[0], err, out)
	}
	if b, _ := os.ReadFile(mark); strings.TrimSpace(string(b)) != "build" {
		t.Errorf("the hook ran %q, want the build's answer, not the fake's on PATH", b)
	}
}
