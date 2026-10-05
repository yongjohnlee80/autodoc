package app

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"net"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	golibrpc "github.com/yongjohnlee80/golib/server/rpc"
	"github.com/yongjohnlee80/golib/server/rpc/msgpackrpc"

	"github.com/yongjohnlee80/autodoc/core/config"
	"github.com/yongjohnlee80/autodoc/rpc"
	"github.com/yongjohnlee80/autodoc/tui"
)

// olderDaemon serves sock as an AutoDoc of protocol proto would for a restart: a probe hello is
// answered whatever it declares, a declared one only at proto, and sys.shutdown only after it.
// declared reports the protocol a hello declared; stopped closes once it has shut down.
func olderDaemon(t *testing.T, sock string, proto int64) (stopped chan struct{}, declared *atomic.Int64) {
	t.Helper()
	ln, err := net.Listen("unix", sock)
	if err != nil {
		t.Fatal(err)
	}
	declared = &atomic.Int64{}
	ctx, cancel := context.WithCancel(context.Background())
	srv := golibrpc.New(msgpackrpc.New(nil), golibrpc.WithListener(ln))
	srv.Handle("sys.hello", func(_ context.Context, req *golibrpc.Request) (any, error) {
		reply := map[string]any{"server": rpc.ServerName, "protocol": proto, "min_protocol": proto,
			"version": fmt.Sprintf("v-p%d", proto), "pid": int64(4242)}
		m, _ := req.Params[0].(map[string]any)
		p, ok := m["protocol"]
		if !ok {
			return reply, nil
		}
		declared.Store(p.(int64))
		if p != proto {
			return nil, &golibrpc.Error{Code: rpc.CodeProtocolMismatch, Message: fmt.Sprintf("protocol mismatch: client %v, server %d", p, proto)}
		}
		req.Session.SetValue("hello", true)
		return reply, nil
	})
	srv.Handle("sys.shutdown", func(_ context.Context, req *golibrpc.Request) (any, error) {
		if ok, _ := req.Session.Value("hello").(bool); !ok {
			return nil, &golibrpc.Error{Code: rpc.CodeHandshakeRequired, Message: "handshake required"}
		}
		go cancel()
		return nil, nil
	})
	stopped = make(chan struct{})
	go func() { _ = srv.Run(ctx); close(stopped) }()
	t.Cleanup(func() { cancel(); <-stopped })
	return stopped, declared
}

func loadConfig(t *testing.T, path string) *config.Config {
	t.Helper()
	cfg, err := config.Load(path)
	if err != nil {
		t.Fatal(err)
	}
	return cfg
}

// spawnCounting is a restart's spawn that starts this build's daemon in-process, counting spawns.
func spawnCounting(t *testing.T, cfgPath, sock string, n *atomic.Int32, started **daemon) func() (string, error) {
	return func() (string, error) {
		n.Add(1)
		*started = start(t, cfgPath, sock)
		return "", nil
	}
}

func restartOnce(t *testing.T, r restarter, cfgPath string) (string, error) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	var out bytes.Buffer
	err := r.run(ctx, loadConfig(t, cfgPath), &out)
	return out.String(), err
}

// TestRestartReplacesAnOlderDaemon: a daemon of an older protocol, which this build's hello would be
// refused by, is stopped over a hello at ITS protocol, waited on by its pid, and this build starts in
// its place on the same socket: once, and only after the old one is gone.
func TestRestartReplacesAnOlderDaemon(t *testing.T) {
	dir := short(t)
	sock := filepath.Join(dir, "s.sock")
	cfgPath := configSharing(t, sock, filepath.Join(dir, "state"), filepath.Join(dir, "data"))
	stopped, declared := olderDaemon(t, sock, rpc.Protocol-1)
	var spawns atomic.Int32
	var fresh *daemon
	var waitedOn int64
	r := restarter{
		spawn: spawnCounting(t, cfgPath, sock, &spawns, &fresh),
		waitGone: func(ctx context.Context, pid int64) bool {
			waitedOn = pid
			select {
			case <-stopped:
				return true
			case <-ctx.Done():
				return false
			}
		},
	}
	got, err := restartOnce(t, r, cfgPath)
	if err != nil {
		t.Fatalf("--restart: %v", err)
	}
	if got != "unix\t"+sock+"\n" {
		t.Errorf("--restart printed %q, want the socket", got)
	}
	if d := declared.Load(); d != rpc.Protocol-1 {
		t.Errorf("the older daemon's hello declared protocol %d, want its own %d", d, rpc.Protocol-1)
	}
	if waitedOn != 4242 {
		t.Errorf("waited on pid %d, want the older daemon's 4242", waitedOn)
	}
	if n := spawns.Load(); n != 1 {
		t.Fatalf("%d daemons spawned, want one", n)
	}
	info, err := rpc.Probe(context.Background(), "unix", sock)
	if err != nil || info.Protocol != rpc.Protocol {
		t.Errorf("after the restart the socket answers %+v (%v), want this build's protocol %d", info, err, rpc.Protocol)
	}
}

// TestRestartReplacesACurrentDaemon: a forced restart of a daemon of this build's own protocol stops
// it and starts another: a new instance answers where the old one did.
func TestRestartReplacesACurrentDaemon(t *testing.T) {
	dir := short(t)
	sock := filepath.Join(dir, "s.sock")
	cfgPath := configSharing(t, sock, filepath.Join(dir, "state"), filepath.Join(dir, "data"))
	old := start(t, cfgPath, sock)
	before, err := rpc.Probe(context.Background(), "unix", sock)
	if err != nil {
		t.Fatal(err)
	}
	var spawns atomic.Int32
	var fresh *daemon
	r := restarter{
		spawn: spawnCounting(t, cfgPath, sock, &spawns, &fresh),
		waitGone: func(ctx context.Context, _ int64) bool {
			select {
			case err := <-old.done: // runServe returned: the store lease is released
				old.done <- err
				return true
			case <-ctx.Done():
				return false
			}
		},
	}
	if _, err := restartOnce(t, r, cfgPath); err != nil {
		t.Fatalf("--restart: %v", err)
	}
	after, err := rpc.Probe(context.Background(), "unix", sock)
	if err != nil {
		t.Fatalf("nothing answers after the restart: %v", err)
	}
	if after.Instance == before.Instance || spawns.Load() != 1 {
		t.Errorf("instance %s → %s with %d spawns: want a new daemon, spawned once", before.Instance, after.Instance, spawns.Load())
	}
}

// TestRestartWithNothingServingStarts: with no daemon, a restart is a start.
func TestRestartWithNothingServingStarts(t *testing.T) {
	dir := short(t)
	sock := filepath.Join(dir, "s.sock")
	cfgPath := configSharing(t, sock, filepath.Join(dir, "state"), filepath.Join(dir, "data"))
	var spawns atomic.Int32
	var fresh *daemon
	r := restarter{
		spawn:    spawnCounting(t, cfgPath, sock, &spawns, &fresh),
		waitGone: func(context.Context, int64) bool { t.Error("waited on a daemon that was not there"); return true },
	}
	got, err := restartOnce(t, r, cfgPath)
	if err != nil || got != "unix\t"+sock+"\n" || spawns.Load() != 1 {
		t.Errorf("--restart with nothing serving: %q, %v, %d spawns; want the socket, started once", got, err, spawns.Load())
	}
}

// TestRestartLeavesAnotherStoresDaemonAlone: a daemon answering on the socket for another store is
// neither stopped nor replaced: the restart refuses with the store mismatch, as --ensure does.
func TestRestartLeavesAnotherStoresDaemonAlone(t *testing.T) {
	dir := short(t)
	sock := filepath.Join(dir, "s.sock")
	start(t, configSharing(t, sock, filepath.Join(dir, "state-a"), filepath.Join(dir, "data-a")), sock)
	before, err := rpc.Probe(context.Background(), "unix", sock)
	if err != nil {
		t.Fatal(err)
	}
	cfgB := configSharing(t, sock, filepath.Join(dir, "state-b"), filepath.Join(dir, "data-b"))
	r := restarter{
		spawn:    func() (string, error) { t.Error("spawned over another store's daemon"); return "", nil },
		waitGone: func(context.Context, int64) bool { t.Error("waited on another store's daemon"); return true },
	}
	_, err = restartOnce(t, r, cfgB)
	var mm *tui.StoreMismatchError
	if !errors.As(err, &mm) {
		t.Errorf("--restart over another store's daemon: %v, want a store mismatch", err)
	}
	after, perr := rpc.Probe(context.Background(), "unix", sock)
	if perr != nil || after.Instance != before.Instance {
		t.Errorf("another store's daemon was disturbed: %+v (%v)", after, perr)
	}
}

// TestRestartStartsNothingWhileTheOldDaemonLives: when the stopped daemon's process has not gone in
// time, the restart fails saying so, and starts nothing into a store lease still held.
func TestRestartStartsNothingWhileTheOldDaemonLives(t *testing.T) {
	dir := short(t)
	sock := filepath.Join(dir, "s.sock")
	cfgPath := configSharing(t, sock, filepath.Join(dir, "state"), filepath.Join(dir, "data"))
	olderDaemon(t, sock, rpc.Protocol-1)
	r := restarter{
		spawn:    func() (string, error) { t.Error("spawned while the old daemon lives"); return "", nil },
		waitGone: func(context.Context, int64) bool { return false },
	}
	_, err := restartOnce(t, r, cfgPath)
	if err == nil || !strings.Contains(err.Error(), "has not stopped") {
		t.Errorf("--restart with a daemon that does not exit: %v, want it to say so", err)
	}
}
