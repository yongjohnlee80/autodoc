//go:build linux || darwin

package crew_test

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/yongjohnlee80/autodoc/core/crew"
)

// fakeCodex is a stand-in for the codex CLI: it records its arguments, one a line, to calls, then
// exits with code, after sleeping when asked.
func fakeCodex(t *testing.T, code int, sleep string) (bin, calls string) {
	t.Helper()
	dir := t.TempDir()
	bin, calls = filepath.Join(dir, "codex"), filepath.Join(dir, "calls")
	script := "#!/bin/sh\nfor a in \"$@\"; do printf '%s\\n' \"$a\"; done >> " + calls + "\n"
	if sleep != "" {
		script += "sleep " + sleep + "\n"
	}
	script += "exit " + string(rune('0'+code)) + "\n"
	if err := os.WriteFile(bin, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	return bin, calls
}

func codexEndpoint(t *testing.T, server, thread string) crew.Endpoint {
	t.Helper()
	b, _ := json.Marshal(map[string]string{"server": server, "thread": thread})
	return crew.Endpoint{Session: "s1", Generation: 1, Kind: "codex", Locator: string(b)}
}

// TestTheCodexWakerQueuesOnTheUsersOwnServer
// (docs/decisions/crew.md#wakers-and-the-wake-scheduler): a wake runs codex queue, as argv with
// each value in --flag=value form, against a socket of the daemon's user in a folder only it can
// enter. A server that is not a socket, is a link to one, lies in a folder others can enter, or
// lies outside Codex's folders is refused without running codex. A failing codex fails the wake;
// one killed at the deadline is unknown, since the queue may have taken the notice.
func TestTheCodexWakerQueuesOnTheUsersOwnServer(t *testing.T) {
	ctx := context.Background()
	root, err := os.MkdirTemp("", "cx")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(root) })
	if err := os.Chmod(root, 0o700); err != nil {
		t.Fatal(err)
	}
	sock := filepath.Join(root, "s.sock")
	ln, err := net.Listen("unix", sock)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = ln.Close() })
	waker := func(bin string) *crew.CodexWaker {
		w := crew.NewCodexWaker(os.Getuid(), bin)
		w.SetRoots(root)
		return w
	}

	bin, calls := fakeCodex(t, 0, "")
	if err := waker(bin).Wake(ctx, codexEndpoint(t, sock, "019a-thread"), crew.WakeNotice); err != nil {
		t.Fatalf("a wake on the user's own server: %v", err)
	}
	got, _ := os.ReadFile(calls)
	want := strings.Join([]string{"queue", "--remote=unix://" + sock, "--thread=019a-thread", "--message=" + crew.WakeNotice}, "\n") + "\n"
	if string(got) != want {
		t.Fatalf("codex ran with\n%s\nwant\n%s", got, want)
	}

	plain := filepath.Join(root, "plain")
	link := filepath.Join(root, "link.sock")
	if err := os.WriteFile(plain, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(sock, link); err != nil {
		t.Fatal(err)
	}
	open, err := os.MkdirTemp(root, "open")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(open, 0o755); err != nil {
		t.Fatal(err)
	}
	openSock := filepath.Join(open, "s.sock")
	ln2, err := net.Listen("unix", openSock)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = ln2.Close() })
	other, err := os.MkdirTemp("", "cy")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(other) })
	bin, calls = fakeCodex(t, 0, "")
	for name, ep := range map[string]crew.Endpoint{
		"a server that is not a socket":         codexEndpoint(t, plain, "t"),
		"a link to a socket":                    codexEndpoint(t, link, "t"),
		"a socket in a folder others can enter": codexEndpoint(t, openSock, "t"),
		"a socket outside Codex's folders":      codexEndpoint(t, filepath.Join(other, "s.sock"), "t"),
		"a thread that would read as a flag":    codexEndpoint(t, sock, "--help"),
	} {
		if err := waker(bin).Wake(ctx, ep, crew.WakeNotice); err == nil || crew.Classify(err) != crew.OutcomeFailed {
			t.Errorf("%s: %v (%v), want a failed wake", name, err, crew.Classify(err))
		}
	}
	if b, _ := os.ReadFile(calls); len(b) != 0 {
		t.Errorf("a refused wake ran codex: %q", b)
	}

	failing, _ := fakeCodex(t, 1, "")
	if err := waker(failing).Wake(ctx, codexEndpoint(t, sock, "t"), crew.WakeNotice); err == nil || crew.Classify(err) != crew.OutcomeFailed {
		t.Errorf("codex exiting 1: %v (%v), want a failed wake", err, crew.Classify(err))
	}
	slow, _ := fakeCodex(t, 0, "5")
	short, cancel := context.WithTimeout(ctx, 200*time.Millisecond)
	defer cancel()
	began := time.Now()
	if err := waker(slow).Wake(short, codexEndpoint(t, sock, "t"), crew.WakeNotice); !errors.Is(err, crew.ErrWakeUnknown) {
		t.Errorf("codex killed at the deadline: %v, want unknown (it may have queued)", err)
	}
	// the stand-in's sleep outlives the killed shell and holds stderr: the call still ends at once
	if took := time.Since(began); took > 3*time.Second {
		t.Errorf("a wake killed at its 200 ms deadline returned after %v: a child holding codex's output kept the call", took)
	}
}

// TestACodexLocatorNamesAThread: registration takes a Codex thread that is one argv value's worth of
// session id or name, never one that would read as a flag or carry a control character.
func TestACodexLocatorNamesAThread(t *testing.T) {
	uid := os.Getuid()
	server := crew.CodexSocketDirs(uid)[0] + "/abc"
	loc := func(thread string) string {
		b, _ := json.Marshal(map[string]string{"server": server, "thread": thread})
		return string(b)
	}
	if _, err := crew.CheckLocator("codex", loc("019a5c3e-thread"), uid); err != nil {
		t.Fatalf("a session id: %v", err)
	}
	for name, thread := range map[string]string{
		"empty": "", "a leading dash": "--help", "a newline": "a\nb", "too long": strings.Repeat("t", crew.MaxIdent+1),
	} {
		if _, err := crew.CheckLocator("codex", loc(thread), uid); !errors.Is(err, crew.ErrBadEndpoint) {
			t.Errorf("%s: %v, want ErrBadEndpoint", name, err)
		}
	}
}
