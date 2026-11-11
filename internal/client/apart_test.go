package client

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	golibrpc "github.com/yongjohnlee80/golib/server/rpc"
	"github.com/yongjohnlee80/golib/server/rpc/msgpackrpc"

	"github.com/yongjohnlee80/autodoc/internal/ipc"
	"github.com/yongjohnlee80/autodoc/rpc"
)

// pollDaemon is a daemon for the calls kept apart: its hellos are numbered (the session's primary
// is 1), each answered as hello says, and "w" waits, as a long-poll does, until its connection or
// its caller ends it, telling each one it holds on held. "o" answers at once with its connection.
type pollDaemon struct {
	mu     sync.Mutex
	hellos int
	hello  func(n int) (instance string, err error) // nil: every hello is instance "i1"
	held   chan polled
}

// polled is a "w" held by the daemon: its connection, and its ctx, which ends when the daemon ends
// the request (its connection closed).
type polled struct {
	conn *golibrpc.Session
	ctx  context.Context
}

func startPollDaemon(t *testing.T, hello func(n int) (string, error)) (*pollDaemon, string) {
	t.Helper()
	d := &pollDaemon{hello: hello, held: make(chan polled, 16)}
	sock := ipc.ScratchAddr(shortDir(t), "s.sock")
	ln, err := ipc.Listen(sock, func(error) {})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	srv := golibrpc.New(msgpackrpc.New(nil), golibrpc.WithListener(ln))
	srv.Handle("sys.hello", func(_ context.Context, req *golibrpc.Request) (any, error) {
		d.mu.Lock()
		d.hellos++
		n := d.hellos
		d.mu.Unlock()
		instance := "i1"
		if d.hello != nil {
			var err error
			if instance, err = d.hello(n); err != nil {
				return nil, err
			}
		}
		return map[string]any{"protocol": rpc.Protocol, "server_protocol": rpc.Protocol, "version": "v-test", "instance": instance,
			"pid": int64(7), "store_id": "", "client": fmt.Sprintf("autodoc-tui#%d", n), "events": int64(n)}, nil
	})
	srv.Handle("w", func(ctx context.Context, req *golibrpc.Request) (any, error) {
		d.held <- polled{req.Session, ctx}
		<-ctx.Done()
		return nil, ctx.Err()
	})
	srv.Handle("o", func(_ context.Context, req *golibrpc.Request) (any, error) {
		return fmt.Sprintf("%p", req.Session), nil
	})
	done := make(chan struct{})
	go func() { _ = srv.Run(ctx); close(done) }()
	t.Cleanup(func() { cancel(); <-done })
	return d, sock
}

func (d *pollDaemon) helloCount() int { d.mu.Lock(); defer d.mu.Unlock(); return d.hellos }

// nextHeld is the next "w" the daemon holds, within a few seconds.
func (d *pollDaemon) nextHeld(t *testing.T) polled {
	t.Helper()
	select {
	case p := <-d.held:
		return p
	case <-time.After(5 * time.Second):
		t.Fatal("no call kept apart reached the daemon")
	}
	return polled{}
}

// ended waits for the daemon to end a held call (its connection closed).
func ended(t *testing.T, what string, p polled) {
	t.Helper()
	select {
	case <-p.ctx.Done():
	case <-time.After(5 * time.Second):
		t.Fatalf("%s: the daemon still holds the call kept apart", what)
	}
}

// apartSession is a session keeping "w" apart, connected to the daemon on sock.
func apartSession(t *testing.T, sock string) *Session {
	t.Helper()
	s := NewSession(sock, nil).Apart("w")
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := s.Connect(ctx); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(s.Close)
	return s
}

// TestCallsKeptApartWaitOnAConnectionOfTheirOwn: the calls kept apart go on one connection of the
// session's, not the primary's, and not one each; an ordinary call stays on the primary. The
// session's own token, generation, event head and Done are the primary's, the polling
// connection's hello changing none of them; the call hooks see the calls kept apart too.
func TestCallsKeptApartWaitOnAConnectionOfTheirOwn(t *testing.T) {
	d, sock := startPollDaemon(t, nil)
	s := apartSession(t, sock)
	var seen []string
	var mu sync.Mutex
	s.SetBeforeCall(func(method string, _ []any) { mu.Lock(); seen = append(seen, method); mu.Unlock() })
	token, gen, head, done := s.Client(), s.Gen(), s.EventsHead(), s.Done()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	for range 2 {
		go func() { _, _ = s.Call(ctx, "w") }()
	}
	a, b := d.nextHeld(t), d.nextHeld(t)
	primary, err := s.Call(ctx, "o")
	if err != nil {
		t.Fatal(err)
	}
	if a.conn != b.conn {
		t.Error("the two calls kept apart went on two connections; want one shared")
	}
	if fmt.Sprintf("%p", a.conn) == primary {
		t.Error("the calls kept apart went on the primary connection")
	}
	if n := d.helloCount(); n != 2 {
		t.Errorf("%d hellos; want 2 (the primary's and the polling connection's)", n)
	}
	if s.Client() != token || s.Gen() != gen || s.EventsHead() != head || s.Done() != done {
		t.Errorf("after the polling connection's hello: token %q gen %d head %d; want the primary's %q %d %d, and its Done",
			s.Client(), s.Gen(), s.EventsHead(), token, gen, head)
	}
	mu.Lock()
	if want := []string{"w", "w", "o"}; fmt.Sprint(seen) != fmt.Sprint(want) {
		t.Errorf("the call hooks saw %v; want %v", seen, want)
	}
	mu.Unlock()
	s.SetCallError(func(method string) error {
		if method == "w" {
			return errors.New("refused here")
		}
		return nil
	})
	if _, err := s.Call(ctx, "w"); err == nil || err.Error() != "refused here" {
		t.Errorf("a call kept apart the error hook refuses: %v", err)
	}
}

// TestAPollingConnectionThatFailsItsHelloIsNeverUsed: a polling connection whose hello fails, or
// that answers from another daemon, fails the call and is never kept: the next call kept apart
// dials again, and is served once a hello succeeds.
func TestAPollingConnectionThatFailsItsHelloIsNeverUsed(t *testing.T) {
	d, sock := startPollDaemon(t, func(n int) (string, error) {
		switch n {
		case 2:
			return "", &golibrpc.Error{Code: golibrpc.CodeInternal, Message: "no hello today"}
		case 3:
			return "i2", nil // another daemon's run
		}
		return "i1", nil
	})
	s := apartSession(t, sock)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	// bounded: a hello that is wrongly accepted holds the call on the daemon
	failed, done := context.WithTimeout(ctx, 5*time.Second)
	defer done()
	if _, err := s.Call(failed, "w"); err == nil {
		t.Fatal("a call kept apart, its connection's hello failed: answered")
	}
	if _, err := s.Call(failed, "w"); !errors.Is(err, errAnotherDaemon) {
		t.Fatalf("a call kept apart, its connection's hello another daemon's: %v; want errAnotherDaemon", err)
	}
	s.mu.Lock()
	kept := s.poll
	s.mu.Unlock()
	if kept != nil {
		t.Fatal("a polling connection whose hello failed was kept")
	}
	go func() { _, _ = s.Call(ctx, "w") }()
	d.nextHeld(t)
	if n := d.helloCount(); n != 4 {
		t.Errorf("%d hellos; want 4 (the primary's, two failed, the one that served)", n)
	}
}

// TestAPollingDialEndsWithItsPrimary: a reconnect, a Close or the primary's own end while the
// polling connection's hello is in flight ends that dial at once, the daemon still holding the
// hello: its call answers not connected, nothing is kept, and after a reconnect the next call kept
// apart dials its own, under the new primary.
func TestAPollingDialEndsWithItsPrimary(t *testing.T) {
	for _, how := range []string{"reconnect", "close", "primary ends"} {
		t.Run(how, func(t *testing.T) {
			inHello, release := make(chan struct{}), make(chan struct{})
			d, sock := startPollDaemon(t, func(n int) (string, error) {
				if n == 2 {
					close(inHello)
					<-release
				}
				return "i1", nil
			})
			t.Cleanup(func() { close(release) }) // the held hello ends only once the cell has looked
			s := apartSession(t, sock)
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			answered := make(chan error, 1)
			go func() { _, err := s.Call(ctx, "w"); answered <- err }()
			<-inHello
			switch how {
			case "reconnect":
				if err := s.Connect(ctx); err != nil { // the primary again: hello 3
					t.Fatal(err)
				}
			case "close":
				s.Close()
			case "primary ends":
				s.mu.Lock()
				primary := s.client
				s.mu.Unlock()
				_ = primary.Close()
			}
			select {
			case err := <-answered:
				if !errors.Is(err, errNotConnected) {
					t.Fatalf("a call kept apart whose dial was for the primary before: %v; want not connected", err)
				}
			case <-time.After(5 * time.Second):
				t.Fatal("a call kept apart whose dial was for the primary before: still waiting on the held hello")
			}
			s.mu.Lock()
			kept, flight := s.poll, s.pollDialing
			s.mu.Unlock()
			if kept != nil || flight != nil { // nil: no dial left in flight either
				t.Fatalf("after the dial for a gone primary: polling connection %v, dial in flight %v; want neither", kept, flight)
			}
			if how == "reconnect" {
				go func() { _, _ = s.Call(ctx, "w") }()
				d.nextHeld(t)
				if n := d.helloCount(); n != 4 {
					t.Errorf("%d hellos; want 4 (primary, the held polling one, primary again, its polling)", n)
				}
			}
		})
	}
}

// TestThePollingConnectionEndsWithThePrimary: the primary connection ending, by Close, by a
// reconnect, or by itself (the daemon gone from under it), ends the polling connection too: the
// daemon ends the call it held there, and the next call kept apart dials afresh.
func TestThePollingConnectionEndsWithThePrimary(t *testing.T) {
	for _, how := range []string{"close", "reconnect", "primary ends"} {
		t.Run(how, func(t *testing.T) {
			d, sock := startPollDaemon(t, nil)
			s := apartSession(t, sock)
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			answered := make(chan error, 1)
			go func() { _, err := s.Call(ctx, "w"); answered <- err }()
			held := d.nextHeld(t)
			s.mu.Lock()
			poll := s.poll
			s.mu.Unlock()
			switch how {
			case "close":
				s.Close()
				if !isDone(poll) {
					t.Fatal("Close returned with the polling connection still open")
				}
			case "reconnect":
				if err := s.Connect(ctx); err != nil {
					t.Fatal(err)
				}
				if !isDone(poll) {
					t.Fatal("a reconnect returned with the old polling connection still open")
				}
			case "primary ends":
				s.mu.Lock()
				primary := s.client
				s.mu.Unlock()
				_ = primary.Close() // not through the session: as a connection the daemon dropped
			}
			ended(t, how, held)
			select {
			case err := <-answered:
				if err == nil {
					t.Error("the call kept apart, its connection ended: answered without an error")
				}
			case <-time.After(5 * time.Second):
				t.Fatal("the call kept apart, its connection ended: never answered")
			}
			if how == "reconnect" {
				go func() { _, _ = s.Call(ctx, "w") }()
				if again := d.nextHeld(t); again.conn == held.conn {
					t.Error("after a reconnect, a call kept apart went on the old polling connection")
				}
			}
		})
	}
}

// TestAnEndedPrimaryHandsOutNoPollingConnection: once the primary has ended, a call kept apart is
// not connected, even while its polling connection is still the session's: between the primary's
// end and the watcher closing that connection (endPollWith), the session must not hand it out.
// That moment is held still here: the session's primary is swapped for one already ended, while
// the watcher waits on the live one.
func TestAnEndedPrimaryHandsOutNoPollingConnection(t *testing.T) {
	d, sock := startPollDaemon(t, nil)
	s := apartSession(t, sock)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	go func() { _, _ = s.Call(ctx, "w") }()
	d.nextHeld(t) // the polling connection is the session's
	ended, err := s.dial(ctx)
	if err != nil {
		t.Fatal(err)
	}
	_ = ended.Close()
	s.mu.Lock()
	live, poll := s.client, s.poll
	s.client = ended
	s.mu.Unlock()
	t.Cleanup(func() { _ = live.Close() })
	if poll == nil {
		t.Fatal("no polling connection to hand out: the cell holds nothing still")
	}
	if cli, err := s.pollClient(ctx); !errors.Is(err, errNotConnected) {
		t.Fatalf("an ended primary's polling connection: %v, %v; want not connected", cli, err)
	}
	if _, err := s.Call(ctx, "w"); !errors.Is(err, errNotConnected) {
		t.Fatalf("a call kept apart under an ended primary: %v; want not connected", err)
	}
}
