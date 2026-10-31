//go:build linux || darwin

package app

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/yongjohnlee80/golib/logger"

	"github.com/yongjohnlee80/autodoc/core/crew"
	"github.com/yongjohnlee80/autodoc/core/store"
	"github.com/yongjohnlee80/autodoc/core/todo"
)

// serveLateWakers are serve's own late wakers, kept before TestMain empties them.
var serveLateWakers = lateWakers

// TestCodexIsMisesElsePATHs: the codex CLI is the one mise names, else the first on PATH, else none;
// serve's codex late waker, with no codex installed, finds no waker.
func TestCodexIsMisesElsePATHs(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	t.Setenv("PATH", dir)
	put := func(name, body string) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(dir, name), []byte("#!/bin/sh\n"+body+"\n"), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if bin := codexBin(ctx); bin != "" {
		t.Errorf("no mise and no codex: %q, want none", bin)
	}
	if w := serveLateWakers["codex"](ctx); w != nil {
		t.Errorf("the codex late waker with no codex installed: %T, want none", w)
	}
	onPath := filepath.Join(dir, "codex")
	put("codex", "exit 0")
	for _, c := range []struct{ mise, want string }{
		{"", onPath},       // no mise
		{"exit 1", onPath}, // mise failing
		{"echo", onPath},   // mise naming none
		{`echo "  /opt/mise/codex  "`, "/opt/mise/codex"}, // mise's, trimmed
	} {
		if c.mise != "" {
			put("mise", c.mise)
		}
		if bin := codexBin(ctx); bin != c.want {
			t.Errorf("mise %q: codex is %q, want %q", c.mise, bin, c.want)
		}
	}
}

// TestALateWakerFoundJoinsTheScheduler: a late waker whose probe finds one joins the scheduler, and
// serve says so; one whose probe finds none adds nothing.
func TestALateWakerFoundJoinsTheScheduler(t *testing.T) {
	r := newWakeRig(t)
	s, stop := startScheduler(r.ctx, r.deps.crew, nil, logger.Nop{})
	defer stop()
	lateWakers = map[string]func(context.Context) crew.Waker{
		"found": func(context.Context) crew.Waker { return r.waker },
		"none":  func(context.Context) crew.Waker { return nil },
	}
	t.Cleanup(func() { lateWakers = nil })
	var log bytes.Buffer
	addLateWakers(r.ctx, s, logger.New(logger.WithWriter(&log)))()
	if said := log.String(); !strings.Contains(said, "wakes found sessions too") || strings.Contains(said, "none sessions") {
		t.Errorf("the late wakers' log: %q; want the found one's line alone", said)
	}
}

// TestAWaitForThePredecessorEndsWithItsContext: a start waiting for the daemon before it to let go
// of the store ends when its context does, opening nothing.
func TestAWaitForThePredecessorEndsWithItsContext(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if db, err := openAfterPredecessor(ctx, filepath.Join(t.TempDir(), "autodoc.db")); db != nil || !errors.Is(err, context.Canceled) {
		t.Errorf("a wait with its context ended: %v, %v; want nothing opened and the context's error", db, err)
	}
}

// TestATodoEventNotRecordedIsLogged: an event the todo system saw that the store cannot record is
// logged as such, never dropped silently.
func TestATodoEventNotRecordedIsLogged(t *testing.T) {
	db, err := store.Open(context.Background(), filepath.Join(t.TempDir(), "autodoc.db"))
	if err != nil {
		t.Fatal(err)
	}
	_ = db.Close()
	var log bytes.Buffer
	todoEvent(&moduleDeps{store: db, log: logger.New(logger.WithWriter(&log))}, todo.Event{Kind: "todos.ambiguous", Path: "a.md"})
	if !strings.Contains(log.String(), "logging event todos.ambiguous") {
		t.Errorf("an event the closed store could not record: log %q, want it said", log.String())
	}
}

// TestServeRefusesAStateDirectoryItCannotMake: serve's first step makes the state directory; one
// under a file fails the start, naming it, before anything binds.
func TestServeRefusesAStateDirectoryItCannotMake(t *testing.T) {
	dir := short(t)
	file := filepath.Join(dir, "file")
	if err := os.WriteFile(file, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	sock, state := filepath.Join(dir, "a.sock"), filepath.Join(file, "state")
	cfg := filepath.Join(t.TempDir(), "config.toml")
	body := fmt.Sprintf("[server]\nsocket = %q\nstate_dir = %q\ndata_dir = %q\n", sock, state, filepath.Join(dir, "data"))
	if err := os.WriteFile(cfg, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second) // a start that went on would serve
	defer cancel()
	if err := runServe(ctx, cfg, io.Discard, testBuild, nil, ""); err == nil || !strings.Contains(err.Error(), state) {
		t.Fatalf("serve with its state directory under a file: %v; want the start refused, naming %s", err, state)
	}
	if _, err := os.Stat(sock); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("the socket after a failed start: %v, want none bound", err)
	}
}
