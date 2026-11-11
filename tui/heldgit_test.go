package tui

import (
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"github.com/yongjohnlee80/autodoc/internal/procenv"
)

// heldGitEnv names the real git when the test binary runs as heldGit's git (TestMain).
const heldGitEnv = "AUTODOC_TEST_HELD_GIT"

// heldGit is env with a git first on its PATH whose clone waits, once it is running, until release
// is called: a worker caught between holding its directory and finishing its work. That git is the
// test binary itself, copied in as git (git.exe on Windows), so it needs no shell: run with
// heldGitEnv set, it is heldGitMain.
func heldGit(t *testing.T, env procenv.Env) (held procenv.Env, running func() bool, release func()) {
	t.Helper()
	real, err := exec.LookPath("git")
	if err != nil {
		t.Fatal(err)
	}
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	bin, controls := t.TempDir(), t.TempDir()
	ready, go_ := filepath.Join(controls, "ready"), filepath.Join(controls, "release")
	name := "git"
	if runtime.GOOS == "windows" {
		name = "git.exe"
	}
	if err := copyFile(exe, filepath.Join(bin, name)); err != nil {
		t.Fatal(err)
	}
	held = env.With("PATH="+bin+string(os.PathListSeparator)+env.Get("PATH"),
		heldGitEnv+"="+real, heldGitEnv+"_READY="+ready, heldGitEnv+"_RELEASE="+go_)
	return held, func() bool { return !gone(ready) }, func() { _ = os.WriteFile(go_, nil, 0o600) }
}

// heldGitMain is the test binary as heldGit's git: a clone marks itself running and waits to be
// released; then, as every other git command, it runs the real git with the same arguments, its
// standard streams and its exit status.
func heldGitMain() int {
	args := os.Args[1:]
	if len(args) > 0 && args[0] == "clone" {
		if err := os.WriteFile(os.Getenv(heldGitEnv+"_READY"), nil, 0o600); err != nil {
			fmt.Fprintln(os.Stderr, "held git:", err)
			return 1
		}
		for gone(os.Getenv(heldGitEnv + "_RELEASE")) {
			time.Sleep(10 * time.Millisecond)
		}
	}
	cmd := exec.Command(os.Getenv(heldGitEnv), args...)
	cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, os.Stdout, os.Stderr
	if err := cmd.Run(); err != nil {
		var ee *exec.ExitError
		if errors.As(err, &ee) {
			return ee.ExitCode()
		}
		fmt.Fprintln(os.Stderr, "held git:", err)
		return 1
	}
	return 0
}

// copyFile copies the file from to to, executable.
func copyFile(from, to string) error {
	in, err := os.Open(from)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.OpenFile(to, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o755)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		return err
	}
	return out.Close()
}
