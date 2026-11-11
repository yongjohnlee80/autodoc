//go:build gui

package app

import (
	"bytes"
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/yongjohnlee80/autodoc/internal/ipc"
)

// guiBuild is whether this test binary is the GUI build's, whose --gui opens a window (gui_on.go).
const guiBuild = true

// windowBound is how long a window run may take, its window opened, drawn and quit, before it is
// taken for one that never quits.
const windowBound = 90 * time.Second

// TestTheWindowReturnsItsExitCodes: the GUI build's --gui exits as the program does, run in a process
// of its own (gui.Main ends the process it runs in, so the window can't run in the test's): a config
// that can't be read exits 1, saying so, before any window opens; a window that opens on its
// daemon, draws its first frame and is quit exits 0. That one needs a display (the gate's xvfb). A
// window that never quits fails the cell once windowBound runs out, never hangs it (the mutant: the
// first frame never told).
func TestTheWindowReturnsItsExitCodes(t *testing.T) {
	bad := filepath.Join(t.TempDir(), "autodoc.toml")
	if err := os.WriteFile(bad, []byte("not = [toml\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if code, stderr := runWindow(t, nil, "--gui", "--config", bad); code != 1 || !strings.Contains(stderr, "config: invalid") {
		t.Errorf("--gui on a config it can't read: exit %d, stderr %q; want 1, saying the config is invalid", code, stderr)
	}

	if os.Getenv("DISPLAY") == "" && os.Getenv("WAYLAND_DISPLAY") == "" {
		t.Skip("a window needs a display: neither DISPLAY nor WAYLAND_DISPLAY is set")
	}
	dir := short(t)
	sock := ipc.ScratchAddr(dir, "a.sock")
	state := filepath.Join(dir, "state")
	if err := os.MkdirAll(state, 0o700); err != nil {
		t.Fatal(err)
	}
	cfg := writeConfig(t, sock, state, "", "kb="+t.TempDir())
	start(t, cfg, sock) // its daemon, so the window spawns none
	home := t.TempDir()
	env := []string{"HOME=" + home, "XDG_CONFIG_HOME=" + filepath.Join(home, ".config"), "XDG_DATA_HOME=" + filepath.Join(home, ".local", "share"),
		"XDG_STATE_HOME=" + filepath.Join(home, ".local", "state"), "XDG_CACHE_HOME=" + filepath.Join(home, ".cache"), "XDG_RUNTIME_DIR=" + short(t)}
	if code, stderr := runWindow(t, env, "--gui", "--config", cfg); code != 0 {
		t.Errorf("--gui, its window drawn and quit: exit %d, stderr %q; want 0", code, stderr)
	} else {
		t.Logf("--gui, its window drawn and quit: exit 0, stderr %q", stderr)
	}
}

// runWindow runs this test binary as autodoc with args (TestMain), its environment's with env set
// over it, its window quitting once drawn (windowQuitsEnv), and answers its exit code and its
// standard error. A run that outlasts windowBound is killed, and fails t.
func runWindow(t *testing.T, env []string, args ...string) (int, string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), windowBound)
	defer cancel()
	cmd := exec.CommandContext(ctx, os.Args[0], args...)
	cmd.Env = append(append(os.Environ(), "AUTODOC_TEST_MAIN=1", windowQuitsEnv+"=1"), env...)
	var stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stderr, &stderr
	err := cmd.Run()
	if ctx.Err() != nil {
		t.Fatalf("autodoc %v: still running after %s, killed: its window never quit; it said %q", args, windowBound, stderr.String())
	}
	var exit *exec.ExitError
	if err != nil && !errors.As(err, &exit) {
		t.Fatalf("autodoc %v: %v", args, err)
	}
	return cmd.ProcessState.ExitCode(), stderr.String()
}
