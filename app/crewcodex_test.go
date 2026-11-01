//go:build linux || darwin

package app

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/yongjohnlee80/autodoc/core/crew"
)

// script writes an executable shell script named codex in a fresh folder.
func script(t *testing.T, body string) string {
	t.Helper()
	bin := filepath.Join(t.TempDir(), "codex")
	if err := os.WriteFile(bin, []byte("#!/bin/sh\n"+body+"\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	return bin
}

// TestServeWakesCodexOnlyWithItsQueue: serve registers Codex's waker only for a codex CLI whose
// queue subcommand takes --thread; any other, or none, leaves Codex sessions watch-only.
func TestServeWakesCodexOnlyWithItsQueue(t *testing.T) {
	queue := script(t, `echo "Usage: codex queue [OPTIONS] --thread <THREAD> --message <TEXT>"`)
	if _, ok := codexWaker(context.Background(), os.Getuid(), queue).(*crew.CodexWaker); !ok {
		t.Fatal("a codex with `queue --thread`: no waker")
	}
	for name, bin := range map[string]string{
		"a codex without the queue's --thread": script(t, `echo "Usage: codex [OPTIONS]"`),
		"a codex that fails":                   script(t, "exit 2"),
		"no codex at all":                      "",
	} {
		if w := codexWaker(context.Background(), os.Getuid(), bin); w != nil {
			t.Errorf("%s: a waker %T, want none (watch-only)", name, w)
		}
	}
}

// TestCrewEndpointCodexThreadNamesItsServer: `crew endpoint --codex-thread` gives the thread and the
// app-server Codex's control socket under $CODEX_HOME resolves to; with no thread, or no control
// socket, it refuses rather than register a guess.
func TestCrewEndpointCodexThreadNamesItsServer(t *testing.T) {
	home, run := t.TempDir(), t.TempDir()
	withCodexSocketRoots(t, run)
	server := filepath.Join(run, "abc")
	if err := os.WriteFile(server, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(home, "app-server-control"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(server, filepath.Join(home, "app-server-control", "app-server-control.sock")); err != nil {
		t.Fatal(err)
	}
	env := func(k string) string {
		if k == "CODEX_HOME" {
			return home
		}
		return ""
	}
	loc, err := codexLocator("019a-thread", env)
	if err != nil {
		t.Fatal(err)
	}
	var l struct{ Server, Thread string }
	if err := json.Unmarshal([]byte(loc), &l); err != nil {
		t.Fatal(err)
	}
	want, _ := filepath.EvalSymlinks(server)
	if l.Server != want || l.Thread != "019a-thread" {
		t.Fatalf("the locator %s, want server %s and thread 019a-thread", loc, want)
	}
	if _, err := codexLocator("", env); err == nil {
		t.Error("no thread: registered, rather than refused")
	}
	if _, err := codexLocator("t", func(string) string { return t.TempDir() }); err == nil {
		t.Error("no control socket: registered, rather than refused")
	}
}

// TestACapabilityProbeEndsWithinItsBounds: a probe whose CLI leaves a child holding its output (as a
// shim or a wrapper can) still ends at its deadline plus probeWaitDelay, never when the child does.
func TestACapabilityProbeEndsWithinItsBounds(t *testing.T) {
	bin := script(t, "(sleep 5) &\nsleep 5")
	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	began := time.Now()
	if _, err := probeOutput(ctx, bin); err == nil {
		t.Fatal("a probe killed at its deadline answered no error")
	}
	if took := time.Since(began); took > 200*time.Millisecond+probeWaitDelay+time.Second {
		t.Fatalf("the probe ended after %v: a child holding its output kept it past its deadline and probeWaitDelay", took)
	}
}

// TestServeAnswersBeforeALateWakerIsFound: serve starts and answers at once while a late waker's
// probe is still running, so an installed CLI's capability never delays a daemon's start.
func TestServeAnswersBeforeALateWakerIsFound(t *testing.T) {
	release := make(chan struct{})
	probing := make(chan struct{}, 1)
	lateWakers = map[string]func(context.Context) crew.Waker{"slow": func(ctx context.Context) crew.Waker {
		probing <- struct{}{}
		select {
		case <-release:
		case <-ctx.Done():
		}
		return nil
	}}
	t.Cleanup(func() { lateWakers = nil })
	defer close(release)
	dir := short(t)
	sock := filepath.Join(dir, "a.sock")
	began := time.Now()
	d := start(t, writeConfig(t, sock, filepath.Join(dir, "state"), "", "kb="+t.TempDir()), sock)
	answered := time.Since(began)
	// the probe runs in its own goroutine, which may be scheduled only after serve answered (under the
	// coverage run's instrumentation, it was): wait for it to start; it then stays blocked on release,
	// closed only as the cell ends, so serve answers below with the probe still running
	select {
	case <-probing:
	// NOTE: a bound on a failing run only: a probe that never starts fails here rather than hanging.
	case <-time.After(10 * time.Second):
		t.Fatal("the late waker's probe never started: this cell checks nothing")
	}
	call(t, dial(t, sock), "workspace.list")
	if answered > 5*time.Second {
		t.Errorf("serve answered after %v, with a late waker's probe still running", answered)
	}
	d.stop()
}

// withCodexSocketRoots makes dir (resolved) the one folder a Codex app-server socket may lie in, for
// the cell.
func withCodexSocketRoots(t *testing.T, dir string) {
	t.Helper()
	real, err := filepath.EvalSymlinks(dir)
	if err != nil {
		t.Fatal(err)
	}
	saved := codexSocketRoots
	codexSocketRoots = func() []string { return []string{real} }
	t.Cleanup(func() { codexSocketRoots = saved })
}

// TestCodexServerIsBoundToCodexsSocketFolder: a Codex agent's own app-server
// (AUTODOC_CODEX_SERVER, before the shared control link) is its link resolved to the socket, which
// must lie in Codex's socket folder: a variable anyone can set, or the shared link, naming anything
// else is refused, never dialed or registered (the mutant: the bound dropped).
func TestCodexServerIsBoundToCodexsSocketFolder(t *testing.T) {
	sockets, home, agent, elsewhere := t.TempDir(), t.TempDir(), t.TempDir(), t.TempDir()
	withCodexSocketRoots(t, sockets)
	own, shared, outside := filepath.Join(sockets, "own"), filepath.Join(sockets, "shared"), filepath.Join(elsewhere, "evil")
	for _, f := range []string{own, shared, outside} {
		if err := os.WriteFile(f, nil, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.MkdirAll(filepath.Join(home, "app-server-control"), 0o700); err != nil {
		t.Fatal(err)
	}
	link := func(name, to string) string {
		p := filepath.Join(agent, name)
		if err := os.Symlink(to, p); err != nil {
			t.Fatal(err)
		}
		return p
	}
	if err := os.Symlink(shared, filepath.Join(home, "app-server-control", "app-server-control.sock")); err != nil {
		t.Fatal(err)
	}
	env := func(vars map[string]string) func(string) string { return func(k string) string { return vars[k] } }
	wantOwn, _ := filepath.EvalSymlinks(own)
	if got, err := codexServer(env(map[string]string{crew.EnvCodexServer: link("codex.sock", own), "CODEX_HOME": home})); err != nil || got != wantOwn {
		t.Fatalf("the agent's own server: %q %v, want %s (before the shared link)", got, err, wantOwn)
	}
	wantShared, _ := filepath.EvalSymlinks(shared)
	if got, err := codexServer(env(map[string]string{"CODEX_HOME": home})); err != nil || got != wantShared {
		t.Fatalf("the shared server: %q %v, want %s", got, err, wantShared)
	}
	if got, err := codexServer(env(map[string]string{crew.EnvCodexServer: link("bad.sock", outside)})); err == nil || !strings.Contains(err.Error(), "isn't in Codex's socket folder") {
		t.Fatalf("a variable's link out of Codex's socket folder: %q %v, want it refused", got, err)
	}
	bad := t.TempDir()
	if err := os.MkdirAll(filepath.Join(bad, "app-server-control"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(bad, "app-server-control", "app-server-control.sock")); err != nil {
		t.Fatal(err)
	}
	if got, err := codexServer(env(map[string]string{"CODEX_HOME": bad})); err == nil {
		t.Fatalf("the shared link out of Codex's socket folder: %q, want it refused", got)
	}
}
