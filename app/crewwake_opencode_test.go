package app

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/yongjohnlee80/autodoc/core/crew"
)

// TestServeWakesOpenCodeSessions: the scheduler serve starts, with serve's own wakers (crewWakers),
// wakes an OpenCode session through AutoDoc's plugin socket, bound to its process: the request names
// the session alone, and the plugin's ok makes the wake accepted. The socket is a stand-in the test
// serves in the plugin's own folder, named by the test's pid, so the test process is its peer and
// the binding (pid, process reference) is real.
func TestServeWakesOpenCodeSessions(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("the OpenCode waker binds through Linux's /proc")
	}
	r := newWakeRig(t)
	_, jarvis := r.agent("jarvis")
	r.n++
	secret := strings.Repeat("s", 32) + "noctis"
	reg, err := r.deps.crew.Register(r.ctx, crew.Registration{Scope: "autodoc:aaaa", Run: "run-a", Epoch: r.epoch,
		Codename: "noctis", Kind: "opencode", Workspace: "w", Instance: fmt.Sprintf("agent-%d", r.n), Secret: secret})
	if err != nil {
		t.Fatal(err)
	}
	noctis := reg.Session
	// noctis's endpoint: a socket this test holds, in /tmp/autodoc-opencode-<uid> (made 0700 if absent)
	dir := crew.OpenCodeSocketDirs(os.Getuid())[1]
	if _, err := os.Stat(dir); errors.Is(err, os.ErrNotExist) {
		if err := os.Mkdir(dir, 0o700); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = os.Remove(dir) })
	}
	sock := filepath.Join(dir, strconv.Itoa(os.Getpid())+".sock")
	_ = os.Remove(sock) // this pid's, from nothing but an earlier run of this process: never another's
	ln, err := net.Listen("unix", sock)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = ln.Close(); _ = os.Remove(sock) })
	lines := make(chan string, 4)
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			line, _ := bufio.NewReader(c).ReadString('\n')
			_, _ = io.WriteString(c, `{"ok":true}`+"\n")
			_ = c.Close()
			lines <- line
		}
	}()
	self, err := crew.ProcRef(os.Getpid())
	if err != nil {
		t.Fatal(err)
	}
	loc, _ := json.Marshal(map[string]any{"socket": sock, "pid": os.Getpid(), "start": self, "session": "ses_noctis"})
	if _, err := r.deps.crew.SetEndpoint(r.ctx, crew.Credential(noctis, secret), "opencode", string(loc)); err != nil {
		t.Fatal(err)
	}
	r.start(crewWakers(r.deps.crew))
	r.send(jarvis, "noctis", "for noctis")
	select {
	case line := <-lines:
		if line != `{"session":"ses_noctis"}`+"\n" {
			t.Fatalf("the plugin socket read %q, want the session alone", line)
		}
	// NOTE: a bound on a failing run only: a wake that never comes fails here rather than hanging.
	case <-time.After(10 * time.Second):
		t.Fatal("no wake reached the plugin socket: serve's scheduler did not wake an OpenCode session")
	}
	r.settled(noctis, func(w crew.Wake) bool { return w.State == crew.WakeAwaiting && w.Failures == 0 })
}
