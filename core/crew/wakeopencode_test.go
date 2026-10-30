//go:build linux

package crew_test

import (
	"bufio"
	"context"
	"encoding/json"
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

// pluginSocket is a stand-in for AutoDoc's OpenCode plugin: it listens in a fresh short folder,
// reads one line from each connection, hands it on, and answers with answer (nil: none, the
// connection closed; a func, to hold or misbehave). The test process holds it, so it is its peer.
func pluginSocket(t *testing.T, answer func(c net.Conn)) (dir, path string, got <-chan string) {
	t.Helper()
	dir, err := os.MkdirTemp("", "oc")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	path = filepath.Join(dir, strconv.Itoa(os.Getpid())+".sock")
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
			line, _ := bufio.NewReader(c).ReadString('\n')
			out <- line
			if answer != nil {
				answer(c)
			}
			_ = c.Close()
		}
	}()
	t.Cleanup(func() { _ = ln.Close(); <-done })
	return dir, path, out
}

func says(line string) func(net.Conn) {
	return func(c net.Conn) { _, _ = io.WriteString(c, line) }
}

func opencodeEndpoint(t *testing.T, socket string, pid int, start string) crew.Endpoint {
	t.Helper()
	b, err := json.Marshal(map[string]any{"socket": socket, "pid": pid, "start": start, "session": "oc-1"})
	if err != nil {
		t.Fatal(err)
	}
	return crew.Endpoint{Session: "s1", Generation: 1, Kind: "opencode", Locator: string(b)}
}

// TestTheOpenCodeWakersOutcomes: each row of OpenCodeWaker's outcome table, against a stand-in
// plugin socket the test process holds. The request names the session alone, never a notice: the
// plugin wakes with its own.
func TestTheOpenCodeWakersOutcomes(t *testing.T) {
	t.Parallel()
	self, err := crew.ProcRef(os.Getpid())
	if err != nil {
		t.Fatal(err)
	}
	hold := func(c net.Conn) { _, _ = io.Copy(io.Discard, c) } // no answer until the waker goes
	long := func(c net.Conn) { _, _ = io.WriteString(c, `{"ok":true,"x":"`+strings.Repeat("y", 5000)+"\"}\n") }
	for _, tc := range []struct {
		name   string
		answer func(net.Conn)
		want   crew.Outcome
	}{
		{"ok: accepted", says(`{"ok":true}` + "\n"), crew.OutcomeAccepted},
		{"refused (not its session): failed", says(`{"ok":false,"refused":"not this plugin's session"}` + "\n"), crew.OutcomeFailed},
		{"promptAsync failed: unknown", says(`{"ok":false,"error":"promptAsync failed"}` + "\n"), crew.OutcomeUnknown},
		{"not ok, no reason: unknown", says(`{"ok":false}` + "\n"), crew.OutcomeUnknown},
		{"no answer, the connection closed: unknown", nil, crew.OutcomeUnknown},
		{"an answer cut short: unknown", says(`{"ok":tr`), crew.OutcomeUnknown},
		{"a malformed answer: unknown", says("yes\n"), crew.OutcomeUnknown},
		{"an answer over the bound: unknown", long, crew.OutcomeUnknown},
		{"no answer in time: unknown", hold, crew.OutcomeUnknown},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			dir, sock, got := pluginSocket(t, tc.answer)
			w := crew.NewOpenCodeWaker(os.Getuid())
			w.SetRoots(dir)
			ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
			defer cancel()
			err := w.Wake(ctx, opencodeEndpoint(t, sock, os.Getpid(), self), crew.WakeNotice)
			if o := crew.Classify(err); o != tc.want {
				t.Errorf("outcome %v (%v), want %v", o, err, tc.want)
			}
			select {
			case line := <-got:
				if line != `{"session":"oc-1"}`+"\n" {
					t.Errorf("the request was %q, want the session alone", line)
				}
			case <-time.After(2 * time.Second):
				t.Error("nothing reached the plugin")
			}
		})
	}
}

// TestTheOpenCodeWakerBindsToTheSessionsProcess: a socket outside the plugin's folders, one held by
// another process than the registered one (or by a later process at its pid), or one gone, fails
// the wake with nothing written.
func TestTheOpenCodeWakerBindsToTheSessionsProcess(t *testing.T) {
	t.Parallel()
	self, err := crew.ProcRef(os.Getpid())
	if err != nil {
		t.Fatal(err)
	}
	dir, sock, got := pluginSocket(t, says(`{"ok":true}`+"\n"))
	later := self[:strings.LastIndex(self, ":")+1] + "1" // the same pid, another start: a later process
	for name, ep := range map[string]crew.Endpoint{
		"another process":            opencodeEndpoint(t, sock, os.Getpid(), "b:n:1:9000"),
		"a later process at the pid": opencodeEndpoint(t, sock, os.Getpid(), later),
		"a socket gone":              opencodeEndpoint(t, filepath.Join(dir, "1.sock"), 1, self),
	} {
		w := crew.NewOpenCodeWaker(os.Getuid())
		w.SetRoots(dir)
		if err := w.Wake(context.Background(), ep, crew.WakeNotice); crew.Classify(err) != crew.OutcomeFailed {
			t.Errorf("%s: %v, want a failure", name, err)
		}
	}
	w := crew.NewOpenCodeWaker(os.Getuid()) // its own folders: the stand-in's is not one
	if err := w.Wake(context.Background(), opencodeEndpoint(t, sock, os.Getpid(), self), crew.WakeNotice); crew.Classify(err) != crew.OutcomeFailed {
		t.Errorf("a socket outside the plugin's folders: %v, want a failure", err)
	}
	// each refused wake connected (the binding is read from the connection) and wrote nothing: the
	// stand-in hands on what each connection wrote, "" for none
	for {
		select {
		case line := <-got:
			if line != "" {
				t.Errorf("a refused wake wrote %q", line)
			}
		case <-time.After(200 * time.Millisecond):
			return
		}
	}
}

// TestAnOpenCodeLocatorNamesItsSocketAndProcess: an opencode locator is the plugin's socket in its
// folders for this user, named by the pid, the pid's process reference and a session id; the shape
// before the socket, another user's folder, a socket not named by the pid, another pid's reference,
// a session that could be read as a flag, and an unknown field are refused.
func TestAnOpenCodeLocatorNamesItsSocketAndProcess(t *testing.T) {
	t.Parallel()
	loc := func(socket string, pid int, start, session string) string {
		b, _ := json.Marshal(map[string]any{"socket": socket, "pid": pid, "start": start, "session": session})
		return string(b)
	}
	if got, err := crew.CheckLocator("opencode", loc("/run/user/1000/autodoc-opencode/42.sock", 42, "b:n:42:9000", "ses_1"), 1000); err != nil {
		t.Errorf("a good locator: %v", err)
	} else if got != `{"socket":"/run/user/1000/autodoc-opencode/42.sock","pid":42,"start":"b:n:42:9000","session":"ses_1"}` {
		t.Errorf("canonical %s", got)
	}
	if _, err := crew.CheckLocator("opencode", loc("/tmp/autodoc-opencode-1000/42.sock", 42, "b:n:42:9000", "ses_1"), 1000); err != nil {
		t.Errorf("the /tmp folder: %v", err)
	}
	for name, l := range map[string]string{
		"the shape before the socket": `{"session":"ses_1","pid":42,"start":"b:n:42:9000"}`,
		"another user's folder":       loc("/run/user/1001/autodoc-opencode/42.sock", 42, "b:n:42:9000", "ses_1"),
		"Claude's folder":             loc("/run/user/1000/cc-socks/42.sock", 42, "b:n:42:9000", "ses_1"),
		"a path climbing out":         loc("/run/user/1000/autodoc-opencode/../cc-socks/42.sock", 42, "b:n:42:9000", "ses_1"),
		"a socket not the pid's":      loc("/run/user/1000/autodoc-opencode/43.sock", 42, "b:n:42:9000", "ses_1"),
		"another pid's reference":     loc("/run/user/1000/autodoc-opencode/42.sock", 42, "b:n:43:9000", "ses_1"),
		"no session":                  loc("/run/user/1000/autodoc-opencode/42.sock", 42, "b:n:42:9000", ""),
		"a session like a flag":       loc("/run/user/1000/autodoc-opencode/42.sock", 42, "b:n:42:9000", "--x"),
		"an unknown field":            `{"socket":"/run/user/1000/autodoc-opencode/42.sock","pid":42,"start":"b:n:42:9000","session":"ses_1","notice":"x"}`,
	} {
		if _, err := crew.CheckLocator("opencode", l, 1000); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
}
