//go:build linux

package crew_test

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/yongjohnlee80/autodoc/core/crew"
)

// inboxSocket is a stand-in for a Claude session's inbox socket, in a fresh short folder: it hands
// on what each connection wrote before it closed.
func inboxSocket(t *testing.T) (dir, path string, got <-chan string) {
	t.Helper()
	dir, err := os.MkdirTemp("", "cc")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	path = filepath.Join(dir, "1.sock")
	ln, err := net.Listen("unix", path)
	if err != nil {
		t.Fatal(err)
	}
	out := make(chan string, 8)
	done := make(chan struct{})
	go func() {
		defer close(done)
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			b, _ := io.ReadAll(c)
			_ = c.Close()
			out <- string(b)
		}
	}()
	t.Cleanup(func() { _ = ln.Close(); <-done })
	return dir, path, out
}

func claudeEndpoint(t *testing.T, socket string, pid int, start string) crew.Endpoint {
	t.Helper()
	b, err := json.Marshal(map[string]any{"socket": socket, "pid": pid, "start": start, "session": "c-1"})
	if err != nil {
		t.Fatal(err)
	}
	return crew.Endpoint{Session: "s1", Generation: 1, Kind: "claude", Locator: string(b)}
}

// TestTheClaudeWakerBindsToTheSessionsProcess
// (docs/decisions/crew.md#wakers-and-the-wake-scheduler): a wake writes the one notice line to a
// socket whose other end is the registered process, and to nothing else. Another process at the
// socket, a pid now held by a later process, a socket gone, one outside Claude's folders, or a
// deadline already passed fails the wake with nothing written; the first three for certain (gone:
// no one will ever answer there), the others as a failure that may pass. Only a write cut off by the
// deadline would be unknown.
func TestTheClaudeWakerBindsToTheSessionsProcess(t *testing.T) {
	ctx := context.Background()
	dir, sock, got := inboxSocket(t)
	w := crew.NewClaudeWaker(os.Getuid())
	w.SetRoots(dir)
	self, err := crew.ProcRef(os.Getpid()) // this test holds the socket: its process is the session's
	if err != nil {
		t.Fatal(err)
	}
	if err := w.Wake(ctx, claudeEndpoint(t, sock, os.Getpid(), self), crew.WakeNotice); err != nil {
		t.Fatalf("a wake bound to its process: %v", err)
	}
	want := `{"type":"user","from":"autodoc","message":{"role":"user","content":"` + crew.WakeNotice + `"}}` + "\n"
	if line := <-got; line != want {
		t.Fatalf("the socket read %q, want %q", line, want)
	}

	parent, err := crew.ProcRef(os.Getppid())
	if err != nil {
		t.Fatal(err)
	}
	later := self[:strings.LastIndexByte(self, ':')+1] + "1" // this pid, as a process that started at tick 1
	refused := map[string]crew.Endpoint{
		"another process's pid":               claudeEndpoint(t, sock, os.Getppid(), parent),
		"the pid now held by a later process": claudeEndpoint(t, sock, os.Getpid(), later),
	}
	for name, ep := range refused {
		err := w.Wake(ctx, ep, crew.WakeNotice)
		if err == nil || crew.Classify(err) != crew.OutcomeGone {
			t.Errorf("%s: %v (%v), want a wake failed as gone: the session's process is not there", name, err, crew.Classify(err))
		}
		if line := <-got; line != "" {
			t.Errorf("%s: the socket read %q, want nothing", name, line)
		}
	}

	other, err := os.MkdirTemp("", "cx")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(other)
	past, cancel := context.WithDeadline(ctx, time.Now().Add(-time.Second))
	defer cancel()
	// a socket file left with nothing listening, as a killed process leaves it
	stale := filepath.Join(dir, "3.sock")
	if l, err := net.Listen("unix", stale); err != nil {
		t.Fatal(err)
	} else {
		l.(*net.UnixListener).SetUnlinkOnClose(false)
		l.Close()
	}
	unsent := map[string]struct {
		ctx  context.Context
		ep   crew.Endpoint
		want crew.Outcome
	}{
		"a socket gone":                {ctx, claudeEndpoint(t, filepath.Join(dir, "2.sock"), os.Getpid(), self), crew.OutcomeGone},
		"a socket nothing listens on":  {ctx, claudeEndpoint(t, stale, os.Getpid(), self), crew.OutcomeGone},
		"a socket outside the folders": {ctx, claudeEndpoint(t, filepath.Join(other, "1.sock"), os.Getpid(), self), crew.OutcomeFailed},
		"a deadline already passed":    {past, claudeEndpoint(t, sock, os.Getpid(), self), crew.OutcomeFailed},
	}
	for name, c := range unsent {
		err := w.Wake(c.ctx, c.ep, crew.WakeNotice)
		if err == nil || crew.Classify(err) != c.want {
			t.Errorf("%s: %v (%v), want %v: nothing was sent", name, err, crew.Classify(err), c.want)
		}
	}
	if !errors.Is(w.Wake(ctx, unsent["a socket outside the folders"].ep, crew.WakeNotice), crew.ErrBadEndpoint) {
		t.Error("a socket outside Claude's folders: want ErrBadEndpoint")
	}
	select {
	case line := <-got:
		t.Errorf("a refused wake reached the socket: %q", line)
	default:
	}
}

// TestAClaudeLocatorNamesItsProcess: registration takes a Claude locator only with start as its
// pid's process reference, so the waker can tell the registered process from a later one.
func TestAClaudeLocatorNamesItsProcess(t *testing.T) {
	uid := os.Getuid()
	sock := "/run/user/" + strconv.Itoa(uid) + "/cc-socks/42.sock"
	self, err := crew.ProcRef(os.Getpid())
	if err != nil {
		t.Fatal(err)
	}
	loc := func(pid int, start string) string {
		b, _ := json.Marshal(map[string]any{"socket": sock, "pid": pid, "start": start, "session": "c-1"})
		return string(b)
	}
	if _, err := crew.CheckLocator("claude", loc(os.Getpid(), self), uid); err != nil {
		t.Fatalf("a locator with its pid's reference: %v", err)
	}
	for name, l := range map[string]string{
		"start as bare ticks":           loc(os.Getpid(), "12345"),
		"start another pid's reference": loc(os.Getpid()+1, self),
	} {
		if _, err := crew.CheckLocator("claude", l, uid); !errors.Is(err, crew.ErrBadEndpoint) {
			t.Errorf("%s: %v, want ErrBadEndpoint", name, err)
		}
	}
}
