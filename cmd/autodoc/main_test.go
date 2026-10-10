package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime/debug"
	"strings"
	"testing"
)

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

// TestAStampedBuildSaysItsVersion: the Makefile and release.yml stamp main.version with -ldflags,
// and the build answers --version with it, exactly as the TUI's versionOf reads it.
func TestAStampedBuildSaysItsVersion(t *testing.T) {
	if testing.Short() {
		t.Skip("builds the binary")
	}
	bin := filepath.Join(t.TempDir(), "autodoc")
	build := exec.Command("go", "build", "-ldflags", "-X main.version=v9.8.7-stamped", "-o", bin, ".")
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("go build: %v\n%s", err, out)
	}
	out, err := exec.Command(bin, "--version").Output()
	if err != nil || string(out) != "autodoc v9.8.7-stamped\n" {
		t.Fatalf("--version: %q, %v", out, err)
	}
	if out, err := exec.Command(bin, "kb").CombinedOutput(); err == nil || !strings.Contains(string(out), "unexpected arguments") {
		t.Fatalf("a usage error: %q, %v", out, err)
	}
}

// TestMainExitsWithAppMainsCode: main hands app.Main its arguments and the stamped version, and
// exits with what it returns.
func TestMainExitsWithAppMainsCode(t *testing.T) {
	args, was := os.Args, exit
	defer func() { os.Args, exit = args, was }()
	code := -1
	exit = func(c int) { code = c }
	os.Args = []string{"autodoc", "--version"}
	main()
	if code != 0 {
		t.Fatalf("--version exited %d", code)
	}
	os.Args = []string{"autodoc", "kb"}
	main()
	if code != 2 {
		t.Fatalf("a usage error exited %d", code)
	}
}
