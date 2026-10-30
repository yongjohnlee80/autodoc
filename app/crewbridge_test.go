//go:build unix

package app

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	golibrpc "github.com/yongjohnlee80/golib/server/rpc"

	"github.com/yongjohnlee80/autodoc/core/crew"
)

// fakeBridge is an agyBridge whose daemon and language server the cell plays: instance answers the
// daemon's instance (or err), dials the language server's connections in turn, and calls records
// each call by method.
type fakeBridge struct {
	*agyBridge
	mu      sync.Mutex // the bridge's goroutine and the cell's both touch what follows
	inst    string
	instErr error
	dials   []error // consumed one per look; nil when empty
	refuse  string  // a method the daemon answers with a credential refusal
	calls   []string
	params  map[string][]any
}

func newFakeBridge() *fakeBridge {
	f := &fakeBridge{inst: "instance-1", params: map[string][]any{}}
	f.agyBridge = &agyBridge{
		c:       sessionContext{Session: "s1", Credential: "s1.secret", Store: "store-1", Kind: "agy"},
		locator: `{"address":"127.0.0.1:4000","conversation":"c-1"}`, token: "tok", addr: "127.0.0.1:4000",
	}
	f.call = func(_ context.Context, method string, params ...any) (any, error) {
		f.mu.Lock()
		defer f.mu.Unlock()
		f.calls = append(f.calls, method)
		f.params[method] = params
		if method == f.refuse {
			return nil, &golibrpc.Error{Code: crew.CodeBadCredential, Message: "refused"}
		}
		return map[string]any{}, nil
	}
	f.instance = func(context.Context) (string, error) {
		f.mu.Lock()
		defer f.mu.Unlock()
		return f.inst, f.instErr
	}
	f.dial = func(context.Context) error {
		f.mu.Lock()
		defer f.mu.Unlock()
		if len(f.dials) == 0 {
			return nil
		}
		err := f.dials[0]
		f.dials = f.dials[1:]
		return err
	}
	f.hosts = func(context.Context) error { return nil }
	return f
}

func (f *fakeBridge) count(method string) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	n := 0
	for _, c := range f.calls {
		if c == method {
			n++
		}
	}
	return n
}

// runFor runs f's bridge for ticks looks after its first, and answers its error, or nil when it was
// still running.
func runFor(t *testing.T, f *fakeBridge, ticks int, between func(i int)) error {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	tick := make(chan time.Time)
	done := make(chan error, 1)
	go func() { done <- f.run(ctx, tick, &bytes.Buffer{}) }()
	for i := 0; i < ticks; i++ {
		if between != nil {
			between(i)
		}
		select {
		case tick <- time.Now():
		case err := <-done:
			return err
		}
	}
	cancel()
	if err := <-done; !errors.Is(err, context.Canceled) {
		return err
	}
	return nil
}

// TestTheBridgeRegistersOnceAndTicksReadOnly: the bridge registers its endpoint, with the token,
// at its start, and its looks after (ten here) only ask whoami: a registration would raise the
// generation and reset the session's wake state, so a tick never makes one.
func TestTheBridgeRegistersOnceAndTicksReadOnly(t *testing.T) {
	t.Parallel()
	f := newFakeBridge()
	if err := runFor(t, f, 10, nil); err != nil {
		t.Fatal(err)
	}
	if n := f.count("crew.endpoint"); n != 1 {
		t.Errorf("%d registrations over ten looks, want the first alone", n)
	}
	if n := f.count("crew.whoami"); n != 10 {
		t.Errorf("%d whoami calls over ten looks, want ten", n)
	}
	p := f.params["crew.endpoint"]
	if len(p) != 4 || p[0] != "s1.secret" || p[1] != "agy" || p[2] != f.locator || p[3] != "tok" {
		t.Errorf("the registration's params %v, want the credential, agy, the locator and the token", p)
	}
}

// TestTheBridgeRegistersAgainForANewDaemon: a daemon of another instance (a restart, which forgets
// the token) is registered with again; a daemon that does not answer is tried again, not left.
func TestTheBridgeRegistersAgainForANewDaemon(t *testing.T) {
	t.Parallel()
	f := newFakeBridge()
	if err := runFor(t, f, 4, func(i int) {
		f.mu.Lock()
		defer f.mu.Unlock()
		switch i {
		case 1:
			f.instErr = errors.New("connection refused")
		case 2:
			f.instErr, f.inst = nil, "instance-2"
		}
	}); err != nil {
		t.Fatal(err)
	}
	if n := f.count("crew.endpoint"); n != 2 {
		t.Errorf("%d registrations, want one at the start and one for the new daemon", n)
	}
}

// TestTheBridgeEndsWithItsSession: the bridge stops when the daemon refuses its credential, at its
// registration or at a look (the session ended, or its folder now holds another's): it never reads
// another context, so it never registers another session.
func TestTheBridgeEndsWithItsSession(t *testing.T) {
	t.Parallel()
	for _, method := range []string{"crew.endpoint", "crew.whoami"} {
		f := newFakeBridge()
		f.refuse = method
		err := runFor(t, f, 3, nil)
		if err == nil || !strings.Contains(err.Error(), "credential is refused") {
			t.Errorf("a refused %s: %v, want the bridge stopped", method, err)
		}
		if n := f.count("crew.endpoint"); n > 1 {
			t.Errorf("a refused %s: %d registrations", method, n)
		}
	}
}

// TestTheBridgeEndsWithItsLanguageServer: the bridge stops when the language server refuses
// bridgeRefusals looks in a row, not on fewer, and not when it answers but does not confirm the
// conversation (a busy or restarting server).
func TestTheBridgeEndsWithItsLanguageServer(t *testing.T) {
	t.Parallel()
	refused := syscall.ECONNREFUSED
	f := newFakeBridge()
	f.dials = []error{refused, refused, nil, refused, refused}
	f.hosts = func(context.Context) error { return errors.New("not confirmed") }
	if err := runFor(t, f, 5, nil); err != nil {
		t.Errorf("two refusals, an answer, two more: %v, want it still running", err)
	}
	f = newFakeBridge()
	f.dials = []error{refused, refused, refused}
	if err := runFor(t, f, 5, nil); err == nil || !strings.Contains(err.Error(), "refused 3 looks in a row") {
		t.Errorf("three refusals in a row: %v, want the bridge stopped", err)
	}
}

// TestTheBridgeLockIsItsSessions: a session's bridge lock is held by one bridge at a time; another
// session's bridge in the same folder takes its own; a lock its holder let go of (or died holding:
// the kernel drops it) is taken again.
func TestTheBridgeLockIsItsSessions(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	unlock, err := lockBridge(dir, "s1")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := lockBridge(dir, "s1"); !errors.Is(err, errBridgeRunning) {
		t.Errorf("a second bridge for s1: %v, want errBridgeRunning", err)
	}
	other, err := lockBridge(dir, "s2")
	if err != nil {
		t.Errorf("s2's bridge beside s1's: %v", err)
	} else {
		other()
	}
	unlock()
	again, err := lockBridge(dir, "s1")
	if err != nil {
		t.Errorf("s1's lock after its holder let go: %v", err)
	} else {
		again()
	}
}

// TestTheBridgesAddressIsLoopback: agy's address is made a loopback IP (localhost is 127.0.0.1,
// never resolved), and any other host is refused.
func TestTheBridgesAddressIsLoopback(t *testing.T) {
	t.Parallel()
	for in, want := range map[string]string{"localhost:4000": "127.0.0.1:4000", "127.0.0.1:4000": "127.0.0.1:4000",
		"[::1]:4000": "[::1]:4000", "http://localhost:4000": "127.0.0.1:4000"} {
		if got, err := loopbackAddress(in); err != nil || got != want {
			t.Errorf("%s: %q %v, want %q", in, got, err, want)
		}
	}
	for _, bad := range []string{"10.0.0.1:4000", "example.com:4000", "4000", "localhost"} {
		if got, err := loopbackAddress(bad); err == nil {
			t.Errorf("%s: %q, want refused", bad, got)
		}
	}
	if _, err := newAgyBridge(sessionContext{Kind: "agy"}, func(string) string { return "" }); err == nil {
		t.Error("a bridge with no language server in its environment was made")
	}
	if _, err := newAgyBridge(sessionContext{Kind: "claude"}, func(string) string { return "x" }); err == nil {
		t.Error("a bridge for a claude session was made")
	}
}
