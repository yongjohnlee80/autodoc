package main

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// runMain runs this binary's main with args and env, as TestMain lets a test: its exit code and
// what it wrote.
func runMain(t *testing.T, env []string, args ...string) (int, string) {
	t.Helper()
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(exe, args...)
	cmd.Env = append(append(os.Environ(), "AUTODOC_TEST_MAIN=1"), env...)
	out, err := cmd.CombinedOutput()
	var exit *exec.ExitError
	switch {
	case errors.As(err, &exit):
		return exit.ExitCode(), string(out)
	case err != nil:
		t.Fatal(err)
	}
	return 0, string(out)
}

// TestArguments: a workspace's name is --ui's alone, and one at most; no mode is the usage; and
// --version says which build this is.
func TestArguments(t *testing.T) {
	for _, c := range []struct {
		args []string
		code int
		says string
	}{
		{[]string{"--version"}, 0, "autodoc " + version},
		{[]string{"kb"}, 2, "unexpected arguments: [kb]"},
		{[]string{"--serve", "kb"}, 2, "only --ui takes one"},
		{[]string{"--ui", "kb", "notes"}, 2, "unexpected arguments: [kb notes]"},
		{nil, 2, "-serve"},
	} {
		code, out := runMain(t, nil, c.args...)
		if code != c.code || !strings.Contains(out, c.says) {
			t.Errorf("autodoc %v: exit %d, %q; want exit %d saying %q", c.args, code, out, c.code, c.says)
		}
	}
}

// TestServeRefusesAWorkspaceSection: --serve reads the config where XDG_CONFIG_HOME puts it, and one
// that still lists its workspaces stops it, saying where workspaces are kept now.
func TestServeRefusesAWorkspaceSection(t *testing.T) {
	home := t.TempDir()
	if err := os.MkdirAll(filepath.Join(home, "autodoc"), 0o700); err != nil {
		t.Fatal(err)
	}
	conf := "[[workspace]]\nname = \"kb\"\nroot = \"" + t.TempDir() + "\"\n"
	if err := os.WriteFile(filepath.Join(home, "autodoc", "config.toml"), []byte(conf), 0o600); err != nil {
		t.Fatal(err)
	}
	code, out := runMain(t, []string{"XDG_CONFIG_HOME=" + home, "XDG_RUNTIME_DIR=" + short(t), "XDG_DATA_HOME=" + t.TempDir()}, "--serve")
	if code != 1 || !strings.Contains(out, "workspaces are kept in the store now") {
		t.Errorf("--serve over a [[workspace]] config: exit %d, %q", code, out)
	}
}
