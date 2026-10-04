package main

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime/debug"
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

// TestTheVersionIsTheModulesWhenNothingStampedIt: go install records the module's version and
// stamps nothing; a stamp wins; a build with no module version keeps "dev".
func TestTheVersionIsTheModulesWhenNothingStampedIt(t *testing.T) {
	for _, c := range []struct{ stamped, module, want string }{
		{"dev", "v0.1.0", "v0.1.0"},
		{"v0.1.0-3-gabc1234", "v0.1.0", "v0.1.0-3-gabc1234"},
		{"dev", "(devel)", "dev"},
		{"dev", "", "dev"},
	} {
		info := &debug.BuildInfo{Main: debug.Module{Path: "github.com/yongjohnlee80/autodoc", Version: c.module}}
		if got := moduleVersion(c.stamped, info); got != c.want {
			t.Errorf("stamped %q, module %q: %q, want %q", c.stamped, c.module, got, c.want)
		}
	}
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
		{[]string{"--serve", "kb"}, 2, "only --ui, --call and --export take one"},
		{[]string{"--call", "workspace.list", "[]", "extra"}, 2, "only --ui, --call and --export take one"},
		// refused before anything is dialled: exit 1, saying why
		{[]string{"--config", "/nonexistent/autodoc.toml", "--call", "workspace.list", "{"}, 1, "--call: the parameters are not JSON"},
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
