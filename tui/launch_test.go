package tui

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	tuicore "github.com/yongjohnlee80/golib/tui"
)

// TestLaunchRunsAnotherClientsLayoutAndDaemon: a program launching AutoDoc's TUI with a layout and
// a daemon start of its own gets both: nothing answers on the configured socket, so its Spawn
// starts the daemon, and the screen is its layout.
func TestLaunchRunsAnotherClientsLayoutAndDaemon(t *testing.T) {
	dir, err := os.MkdirTemp("", "adl")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	sock, state := filepath.Join(dir, "s.sock"), filepath.Join(dir, "state")
	conf := filepath.Join(dir, "config.toml")
	// a data_dir of its own: without one the store is the user's, and resolving the endpoint by the
	// store's lease-info would find the user's daemon serving it instead of spawning this test's
	body := "[server]\nsocket = \"" + sock + "\"\nstate_dir = \"" + state + "\"\ndata_dir = \"" + filepath.Join(dir, "data") + "\"\n"
	if err := os.WriteFile(conf, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	var spawns atomic.Int32
	spawn := func(configPath, stateDir string) (string, error) {
		if configPath != conf || stateDir != state {
			t.Errorf("spawned with %q, %q", configPath, stateDir)
		}
		spawns.Add(1)
		startDaemonOn(t, sock, map[string][]string{"kb": {"a.md", "a\n"}})
		return filepath.Join(stateDir, "serve.log"), nil
	}
	marker := "a layout of its own"
	own := bytes.Replace(layout, []byte("    MenuBar {"), []byte("    Text { text: \""+marker+"\"; Dock.edge: Tui.Top }\n    MenuBar {"), 1)
	if bytes.Equal(own, layout) {
		t.Fatal("main.qml has no MenuBar to put the marker above")
	}
	backend := tuicore.NewTestBackend(100, 30)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		done <- Launch(ctx, LaunchOptions{ConfigPath: conf, Version: "v-client", Spawn: spawn, Layout: own, Backend: backend,
			Installed: func() (string, error) { return "v-client", nil }})
	}()
	deadline := time.Now().Add(10 * time.Second)
	for !strings.Contains(backend.String(), marker) || !strings.Contains(backend.String(), "connected — autodoc v-test") {
		if time.Now().After(deadline) {
			cancel()
			t.Fatalf("the launched TUI shows neither its layout nor the spawned daemon:\n%s", backend.String())
		}
		time.Sleep(20 * time.Millisecond)
	}
	cancel()
	if err := <-done; err != nil && !errors.Is(err, context.Canceled) {
		t.Errorf("Launch: %v", err)
	}
	if n := spawns.Load(); n != 1 {
		t.Errorf("spawned %d times, want once", n)
	}
}

// TestLaunchChecksTheWorkspaceNamed: a workspace the daemon has is opened; one it lacks is refused
// with the names it has; a daemon with none says so.
func TestLaunchChecksTheWorkspaceNamed(t *testing.T) {
	ctx := context.Background()
	d := startDaemon(t, map[string][]string{"kb": {"a.md", "a\n"}, "notes": {"b.md", "b\n"}})
	if err := checkWorkspace(ctx, NewSession(d.sock, nil), "notes"); err != nil {
		t.Errorf("a workspace the daemon has: %v", err)
	}
	err := checkWorkspace(ctx, NewSession(d.sock, nil), "nope")
	if err == nil || !strings.Contains(err.Error(), `no workspace named "nope"; the workspaces are: kb, notes`) {
		t.Errorf("a workspace it lacks: %v", err)
	}
	none := startDaemon(t, map[string][]string{})
	if err := checkWorkspace(ctx, NewSession(none.sock, nil), "kb"); err == nil || !strings.Contains(err.Error(), "none yet") {
		t.Errorf("a daemon with none: %v", err)
	}
}

// TestTheInstalledVersionIsAutodocsAnswer: a binary that answers --version otherwise, or none at all,
// gives no version.
func TestTheInstalledVersionIsAutodocsAnswer(t *testing.T) {
	sh := filepath.Join(t.TempDir(), "not-autodoc")
	if err := os.WriteFile(sh, []byte("#!/bin/sh\necho something else\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	if v, err := versionOf(sh); err == nil {
		t.Errorf("a binary that is not autodoc gave %q", v)
	}
	if _, err := versionOf(filepath.Join(t.TempDir(), "absent")); err == nil {
		t.Error("an absent binary gave a version")
	}
	ok := filepath.Join(t.TempDir(), "autodoc")
	if err := os.WriteFile(ok, []byte("#!/bin/sh\necho autodoc v9.9.9\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	if v, err := versionOf(ok); err != nil || v != "v9.9.9" {
		t.Errorf("an autodoc's answer: %q, %v", v, err)
	}
}
