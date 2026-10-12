package tui

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/yongjohnlee80/golib/gui/record"
	tuicore "github.com/yongjohnlee80/golib/tui"
)

// A launch with a debug recorder holds the TUI's own trace in the recording: tui
// records, one per trace event, the kinds tui names them by — the same file a
// window build's input path records into — and every line parses back.
func TestADebugLaunchRecordsTheTuisTrace(t *testing.T) {
	dir, err := os.MkdirTemp("", "adbg")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	sock, state := filepath.Join(dir, "s.sock"), filepath.Join(dir, "state")
	conf := filepath.Join(dir, "config.toml")
	body := "[server]\nsocket = \"" + sock + "\"\nstate_dir = \"" + state + "\"\ndata_dir = \"" + filepath.Join(dir, "data") + "\"\n"
	if err := os.WriteFile(conf, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	var spawns atomic.Int32
	spawn := func(configPath, stateDir string) (string, error) {
		spawns.Add(1)
		startDaemonOn(t, sock, map[string][]string{"kb": {"a.md", "a\n"}})
		return filepath.Join(stateDir, "serve.log"), nil
	}
	var buf bytes.Buffer
	rec := record.NewJSONLRecorder(&buf)
	backend := tuicore.NewTestBackend(100, 30)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		done <- Launch(ctx, LaunchOptions{ConfigPath: conf, Version: "v-client", Spawn: spawn, Backend: backend, DebugRecorder: rec,
			Installed: func() (string, error) { return "v-client", nil }})
	}()
	deadline := time.Now().Add(10 * time.Second)
	for !strings.Contains(backend.String(), "connected — autodoc v-test") {
		if time.Now().After(deadline) {
			cancel()
			t.Fatalf("the launched TUI never showed the daemon:\n%s", backend.String())
		}
		time.Sleep(10 * time.Millisecond)
	}
	// a key goes through the trace too, consumed or not
	if err := backend.Inject(tuicore.KeyEvent{Kind: tuicore.KeyPress, Code: 'x'}); err != nil {
		t.Fatal(err)
	}
	time.Sleep(200 * time.Millisecond) // the trace fires on the UI loop, promptly
	cancel()
	if err := <-done; err != nil && err != context.Canceled {
		t.Fatalf("Launch: %v", err)
	}
	if err := rec.Close(); err != nil {
		t.Fatal(err)
	}

	kinds := map[string]bool{"focus": true, "focus-repair": true, "mount": true, "unmount": true,
		"key": true, "scope": true, "action": true, "capture": true}
	var tuiRecords, keyRecords int
	for _, line := range strings.Split(strings.TrimRight(buf.String(), "\n"), "\n") {
		if strings.TrimSpace(line) == "" {
			continue
		}
		var r record.Record
		if err := json.Unmarshal([]byte(line), &r); err != nil {
			t.Fatalf("a recording line does not parse: %v\n%s", err, line)
		}
		if r.Stage != record.StageTui {
			continue
		}
		tuiRecords++
		if !kinds[r.Kind] {
			t.Fatalf("a tui record has kind %q, which the trace never says", r.Kind)
		}
		if r.Kind == "key" {
			keyRecords++
		}
	}
	if tuiRecords == 0 {
		t.Fatal("the recording holds no tui records; the trace never reached the recorder")
	}
	if keyRecords == 0 {
		t.Fatal("the recording holds no key record; the typed key never traced")
	}
}
