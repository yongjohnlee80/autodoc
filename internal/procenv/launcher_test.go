package procenv

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

// argvHelper, set, makes this test binary print its arguments and the config it was given by
// $AUTODOC_CONFIG as JSON, and exit: the program these cells run as autodoc.
const argvHelper = "AUTODOC_PROCENV_ARGV"

func TestMain(m *testing.M) {
	if os.Getenv(argvHelper) == "1" {
		_ = json.NewEncoder(os.Stdout).Encode(map[string]any{"args": os.Args[1:], "config": os.Getenv("AUTODOC_CONFIG")})
		os.Exit(0)
	}
	os.Exit(m.Run())
}

// A launcher needs a program to run.
func TestALauncherNeedsAProgram(t *testing.T) {
	if _, err := Launcher("autodoc", nil); err == nil {
		t.Fatal("a launcher of nothing was made")
	}
}

// Self is the program running, any link it was started through followed.
func TestSelfIsTheRunningProgram(t *testing.T) {
	self, err := Self()
	if err != nil {
		t.Fatal(err)
	}
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	want, err := filepath.EvalSymlinks(exe)
	if err != nil {
		t.Fatal(err)
	}
	if self != want || !filepath.IsAbs(self) {
		t.Fatalf("Self = %q, want %q", self, want)
	}
}
