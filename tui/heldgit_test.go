package tui

import (
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"syscall"
	"testing"
	"time"

	"github.com/yongjohnlee80/autodoc/internal/procenv"
)

// heldGitEnv names the real git when the test binary runs as heldGit's git (TestMain).
const heldGitEnv = "AUTODOC_TEST_HELD_GIT"

// heldGitBound is how long a held clone waits to be released before it gives up: a test that ends
// without releasing it (heldGit's cleanup releases it) leaves no git waiting for ever.
const heldGitBound = 2 * time.Minute

// heldGit is env with a git first on its PATH whose clone waits, once it is running, until release
// is called: a worker caught between holding its directory and finishing its work. That git is the
// test binary itself, copied in as git (git.exe on Windows), so it needs no shell: run with
// heldGitEnv set, it is heldGitMain. The test's end releases it too, so a test that fails before
// its release leaves no clone waiting.
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
	// Its own folders, not t.TempDir's: a test's cleanups run newest first, so t.TempDir's removal would
	// come right after the release below and take the release file with it before the clone saw it.
	// Removed once the clone acknowledged its release (or it never started), the binary retried while
	// Windows still holds the running copy.
	bin, err := os.MkdirTemp("", "autodoc-heldgit-bin-")
	if err != nil {
		t.Fatal(err)
	}
	controls, err := os.MkdirTemp("", "autodoc-heldgit-")
	if err != nil {
		t.Fatal(err)
	}
	ready, go_, seen := filepath.Join(controls, "ready"), filepath.Join(controls, "release"), filepath.Join(controls, "released")
	t.Cleanup(func() {
		if !gone(ready) { // the clone ran: wait for it to take its release
			for deadline := time.Now().Add(5 * time.Second); gone(seen) && time.Now().Before(deadline); time.Sleep(10 * time.Millisecond) {
			}
		}
		for deadline := time.Now().Add(5 * time.Second); os.RemoveAll(bin) != nil && time.Now().Before(deadline); time.Sleep(50 * time.Millisecond) {
		}
		_ = os.RemoveAll(controls)
	})
	name := "git"
	if runtime.GOOS == "windows" {
		name = "git.exe"
	}
	git := filepath.Join(bin, name)
	if err := copyFile(exe, git); err != nil {
		t.Fatal(err)
	}
	startsOnceFree(t, git, real, nil)
	held = env.With("PATH="+bin+string(os.PathListSeparator)+env.Get("PATH"),
		heldGitEnv+"="+real, heldGitEnv+"_READY="+ready, heldGitEnv+"_RELEASE="+go_, heldGitEnv+"_SEEN="+seen)
	release = func() { _ = os.WriteFile(go_, nil, 0o600) } // idempotent: a second release writes it again
	t.Cleanup(release)
	return held, func() bool { return !gone(ready) }, release
}

// startsOnceFree runs heldGit's copied git (as git version) until it starts. A parallel test that
// forked while the copy was still open for writing holds that descriptor until its child's exec
// closes it, and until then an exec of the copy fails with "text file busy". Once one exec starts,
// no writer is left (heldGit's is closed, and no later fork can inherit it), so the test's own
// clone cannot fail so. Bounded: a copy that never starts fails its test. busy, when not nil, is
// called after each start the system refused as busy, before the next try.
func startsOnceFree(t *testing.T, git, real string, busy func()) {
	t.Helper()
	for deadline := time.Now().Add(10 * time.Second); ; time.Sleep(10 * time.Millisecond) {
		cmd := exec.Command(git, "version")
		cmd.Env = append(os.Environ(), heldGitEnv+"="+real)
		err := cmd.Run()
		if err == nil {
			return
		}
		if !errors.Is(err, syscall.ETXTBSY) || time.Now().After(deadline) {
			t.Fatalf("the held git does not start: %v", err)
		}
		if busy != nil {
			busy()
		}
	}
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
		// NOTE: bounded, so a clone nobody releases ends on its own rather than outliving its test
		for deadline := time.Now().Add(heldGitBound); gone(os.Getenv(heldGitEnv + "_RELEASE")); time.Sleep(10 * time.Millisecond) {
			if time.Now().After(deadline) {
				fmt.Fprintln(os.Stderr, "held git: never released")
				return 1
			}
		}
		_ = os.WriteFile(os.Getenv(heldGitEnv+"_SEEN"), nil, 0o600) // heldGit's cleanup waits for this
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

// TestAHeldGitEndsWithItsTest: a held clone that its test never released (here a subtest that ends
// with it still waiting, as a failed one would) is released by the test's end, and exits.
func TestAHeldGitEndsWithItsTest(t *testing.T) {
	t.Parallel()
	repo := pluginRepo(t, "Echo", "true")
	dest := filepath.Join(t.TempDir(), "clone") // the outer test's: it outlives the subtest's cleanups
	var cmd *exec.Cmd
	t.Run("held, never released", func(t *testing.T) {
		env, running, _ := heldGit(t, procenv.Env(nil))
		cmd = exec.Command(procenv.Resolve(env, "git"), "clone", "-q", "file://"+repo, dest)
		cmd.Env = env.Environ()
		if err := cmd.Start(); err != nil {
			t.Fatal(err)
		}
		for deadline := time.Now().Add(10 * time.Second); !running(); time.Sleep(10 * time.Millisecond) {
			if time.Now().After(deadline) {
				t.Fatal("the held clone never started")
			}
		}
	}) // ends without its release: the subtest's cleanup releases the clone
	if cmd == nil || cmd.Process == nil {
		t.Fatal("the held clone did not start")
	}
	exited := make(chan error, 1)
	go func() { exited <- cmd.Wait() }()
	select {
	case <-exited:
	case <-time.After(30 * time.Second):
		_ = cmd.Process.Kill()
		t.Fatal("the held clone still waits after its test ended")
	}
}
