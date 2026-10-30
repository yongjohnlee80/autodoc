//go:build linux || darwin

package app

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

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
	if _, ok := codexWaker(os.Getuid(), queue).(*crew.CodexWaker); !ok {
		t.Fatal("a codex with `queue --thread`: no waker")
	}
	for name, bin := range map[string]string{
		"a codex without the queue's --thread": script(t, `echo "Usage: codex [OPTIONS]"`),
		"a codex that fails":                   script(t, "exit 2"),
		"no codex at all":                      "",
	} {
		if w := codexWaker(os.Getuid(), bin); w != nil {
			t.Errorf("%s: a waker %T, want none (watch-only)", name, w)
		}
	}
}

// TestCrewEndpointCodexThreadNamesItsServer: `crew endpoint --codex-thread` gives the thread and the
// app-server Codex's control socket under $CODEX_HOME resolves to; with no thread, or no control
// socket, it refuses rather than register a guess.
func TestCrewEndpointCodexThreadNamesItsServer(t *testing.T) {
	home, run := t.TempDir(), t.TempDir()
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
