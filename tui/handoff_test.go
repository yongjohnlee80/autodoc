package tui

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/yongjohnlee80/autodoc/rpc"
)

// livePID is a process that stays alive for the test, as another TUI restarting the daemon would.
func livePID(t *testing.T) int64 {
	t.Helper()
	cmd := exec.Command("sleep", "60")
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = cmd.Process.Kill(); _ = cmd.Wait() })
	return int64(cmd.Process.Pid)
}

// deadPID is a process that has exited.
func deadPID(t *testing.T) int64 {
	t.Helper()
	cmd := exec.Command("true")
	if err := cmd.Run(); err != nil {
		t.Fatal(err)
	}
	return int64(cmd.Process.Pid)
}

// handoffSession is a session to sock keeping handoffs in state, with the timings shortened: a
// probe window of window, a cap of cap; spawns counts its spawns, which start nothing.
func handoffSession(sock, state string, window, cap time.Duration, spawns *atomic.Int32) *Session {
	s := NewSession(sock, func() (string, error) { spawns.Add(1); return "", nil }).UseHandoffs(state)
	s.window, s.handoffCap = window, cap
	return s
}

// putHandoff writes h as another requester would (its socket, when unset, sock's).
func putHandoff(t *testing.T, state, sock string, h handoff) {
	t.Helper()
	if err := writeHandoffAs(state, sock, h); err != nil {
		t.Fatal(err)
	}
}

func writeHandoffAs(state, sock string, h handoff) error {
	if h.Socket == "" {
		h.Socket = socketID(sock)
	}
	b, err := json.Marshal(h)
	if err != nil {
		return err
	}
	return os.WriteFile(handoffPath(state, sock), b, 0o600)
}

// TestTheSocketHasOneSpelling: a socket reached through a symlinked directory is the same socket,
// so both spellings find one handoff; a directory that cannot be resolved falls back to the cleaned
// absolute path.
func TestTheSocketHasOneSpelling(t *testing.T) {
	dir := t.TempDir()
	link := filepath.Join(t.TempDir(), "link")
	if err := os.Symlink(dir, link); err != nil {
		t.Fatal(err)
	}
	if a, b := socketID(filepath.Join(dir, "s.sock")), socketID(filepath.Join(link, "x", "..", "s.sock")); a != b {
		t.Fatalf("one socket, two ids: %s, %s", a, b)
	}
	if got := socketID("/no/such/dir/../s.sock"); got != "/no/such/s.sock" {
		t.Fatalf("an unresolvable directory: %s", got)
	}
	state := t.TempDir()
	if handoffPath(state, filepath.Join(dir, "s.sock")) != handoffPath(state, filepath.Join(link, "s.sock")) {
		t.Fatal("two spellings, two handoffs")
	}
	if handoffPath(state, filepath.Join(dir, "s.sock")) == handoffPath(state, filepath.Join(dir, "t.sock")) {
		t.Fatal("two sockets share a handoff")
	}
}

// TestWhichHandoffsHoldBack: only another live process's, for this socket, with a deadline ahead
// and no further than handoffSpan; the requester's own is skipped.
func TestWhichHandoffsHoldBack(t *testing.T) {
	sock := filepath.Join(t.TempDir(), "s.sock")
	now := time.Now()
	live, dead := livePID(t), deadPID(t)
	ok := handoff{PID: live, Socket: socketID(sock), Deadline: now.Add(time.Second).UnixNano()}
	if !ok.holdsBack(int64(os.Getpid()), sock, now) {
		t.Fatal("a live handoff does not hold back")
	}
	for name, h := range map[string]handoff{
		"its own":          {PID: int64(os.Getpid()), Socket: ok.Socket, Deadline: ok.Deadline},
		"a dead pid":       {PID: dead, Socket: ok.Socket, Deadline: ok.Deadline},
		"past":             {PID: live, Socket: ok.Socket, Deadline: now.Add(-time.Millisecond).UnixNano()},
		"too far":          {PID: live, Socket: ok.Socket, Deadline: now.Add(handoffSpan + time.Second).UnixNano()},
		"another socket":   {PID: live, Socket: socketID(sock + "2"), Deadline: ok.Deadline},
		"another spelling": {PID: live, Socket: sock + "/", Deadline: ok.Deadline},
	} {
		if h.holdsBack(int64(os.Getpid()), sock, now) {
			t.Errorf("%s holds back", name)
		}
	}
	// written and removed by its requester alone
	state := t.TempDir()
	if err := writeHandoff(state, sock, 42, now); err != nil {
		t.Fatal(err)
	}
	removeHandoff(state, sock, 43)
	if h, found := readHandoff(state, sock); !found || h.PID != 42 || h.Deadline != now.Add(handoffSpan).UnixNano() {
		t.Fatalf("another requester removed it, or it reads %+v", h)
	}
	removeHandoff(state, sock, 42)
	if _, found := readHandoff(state, sock); found {
		t.Fatal("its requester did not remove it")
	}
}

// TestConnectWaitsOutARestart: Connect's probe window waits while another live process's handoff
// stands: a daemon first answering after one window, inside the handoff, is reached with nothing
// spawned and no ConnectError.
func TestConnectWaitsOutARestart(t *testing.T) {
	dir, state := shortDir(t), t.TempDir()
	sock := filepath.Join(dir, "s.sock")
	putHandoff(t, state, sock, handoff{PID: livePID(t), Deadline: time.Now().Add(20 * time.Second).UnixNano()})
	var spawns atomic.Int32
	s := handoffSession(sock, state, 300*time.Millisecond, 10*time.Second, &spawns)
	done := make(chan error, 1)
	go func() { done <- s.Connect(context.Background()) }()
	time.Sleep(time.Second) // past one window
	otherDaemon(t, sock, rpc.Protocol)
	if err := <-done; err != nil {
		t.Fatalf("Connect: %v", err)
	}
	if spawns.Load() != 0 {
		t.Fatalf("spawned %d during another's restart", spawns.Load())
	}
}

// TestALapsedHandoffSpawnsOnce: a handoff that lapses with no daemon is followed by one spawn and
// a full window; a deadline too far ahead, a dead pid and another socket's are ignored at once;
// a cancelled ctx returns at once while held.
func TestALapsedHandoffSpawnsOnce(t *testing.T) {
	dir, state := shortDir(t), t.TempDir()
	sock := filepath.Join(dir, "s.sock")
	const window = 300 * time.Millisecond
	putHandoff(t, state, sock, handoff{PID: livePID(t), Deadline: time.Now().Add(500 * time.Millisecond).UnixNano()})
	var spawns atomic.Int32
	start := time.Now()
	err := handoffSession(sock, state, window, 10*time.Second, &spawns).Connect(context.Background())
	var ce *ConnectError
	if !errors.As(err, &ce) || spawns.Load() != 1 || time.Since(start) < 500*time.Millisecond+window {
		t.Fatalf("after a lapsed handoff: %v, %d spawns, %s; want one spawn and a full window after it", err, spawns.Load(), time.Since(start))
	}
	for name, h := range map[string]handoff{
		"too far":        {PID: livePID(t), Deadline: time.Now().Add(handoffSpan + time.Minute).UnixNano()},
		"a dead pid":     {PID: deadPID(t), Deadline: time.Now().Add(20 * time.Second).UnixNano()},
		"another socket": {PID: livePID(t), Socket: socketID(sock + "2"), Deadline: time.Now().Add(20 * time.Second).UnixNano()},
	} {
		putHandoff(t, state, sock, h)
		spawns.Store(0)
		start := time.Now()
		_ = handoffSession(sock, state, window, 10*time.Second, &spawns).Connect(context.Background())
		if spawns.Load() != 1 || time.Since(start) > 3*window {
			t.Errorf("%s: %d spawns in %s; want one, at once", name, spawns.Load(), time.Since(start))
		}
	}
	putHandoff(t, state, sock, handoff{PID: livePID(t), Deadline: time.Now().Add(20 * time.Second).UnixNano()})
	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel()
	start = time.Now()
	if err := handoffSession(sock, state, window, 10*time.Second, &spawns).Connect(ctx); !errors.Is(err, context.DeadlineExceeded) || time.Since(start) > 2*time.Second {
		t.Fatalf("a cancelled Connect: %v after %s", err, time.Since(start))
	}
}

// TestTheCapIsPerConnect: one Connect waits on handoffs for its cap in all, on its own clock: a
// handoff naming a live process that never binds, then another written before the first's cap
// ends, hold it back for the cap, not twice, and then it spawns.
func TestTheCapIsPerConnect(t *testing.T) {
	dir, state := shortDir(t), t.TempDir()
	sock := filepath.Join(dir, "s.sock")
	const capWait = 700 * time.Millisecond
	putHandoff(t, state, sock, handoff{PID: livePID(t), Deadline: time.Now().Add(20 * time.Second).UnixNano()})
	second := livePID(t)
	go func() {
		time.Sleep(capWait / 2)
		_ = writeHandoffAs(state, sock, handoff{PID: second, Deadline: time.Now().Add(20 * time.Second).UnixNano()})
	}()
	var spawned atomic.Int64 // when, since start
	start := time.Now()
	s := NewSession(sock, func() (string, error) { spawned.Store(int64(time.Since(start))); return "", nil }).UseHandoffs(state)
	s.window, s.handoffCap = 200*time.Millisecond, capWait
	_ = s.Connect(context.Background())
	at := time.Duration(spawned.Load())
	if at < capWait || at > capWait+capWait/2+300*time.Millisecond {
		t.Fatalf("spawned at %s; want after the cap (%s) once, not after two", at, capWait)
	}
}

// shortDir is a directory whose socket path fits a unix socket's limit on every platform.
func shortDir(t *testing.T) string {
	t.Helper()
	dir, err := os.MkdirTemp("", "ado")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	return dir
}
