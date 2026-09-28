package tui

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	golibrpc "github.com/yongjohnlee80/golib/server/rpc"
	"github.com/yongjohnlee80/golib/server/rpc/msgpackrpc"

	"github.com/yongjohnlee80/autodoc/rpc"
)

// spawnProbeWindow bounds how long Connect keeps dialing after the first refusal, a daemon it
// spawned included.
const spawnProbeWindow = 15 * time.Second

// ConnectError is a daemon that did not answer within the probe window.
type ConnectError struct {
	Addr    string        // the socket dialed
	Window  time.Duration // how long it was dialed for
	LogPath string        // the spawned daemon's log; "" when none was spawned
	Last    error         // the last dial's error
}

func (e *ConnectError) Error() string {
	msg := fmt.Sprintf("connect %s: the daemon did not answer within %s (last: %v)", e.Addr, e.Window, e.Last)
	if e.LogPath != "" {
		msg += " — see " + e.LogPath
	}
	return msg
}

func (e *ConnectError) Unwrap() error { return e.Last }

// errNotConnected is a call with no connection: the session is between connections.
var errNotConnected = errors.New("not connected to the daemon")

// Session is the TUI's ONLY path to the core: a golib rpc client, the handshake, and the
// connection's generation. Every result the host applies was asked under a generation; a
// reconnect moves it on, so an answer from an old connection is never applied.
type Session struct {
	addr   string
	spawn  func() (logPath string, err error) // start `autodoc --serve`; nil: never
	window time.Duration
	// beforeCall, when set (tests only), runs on the worker before each call: the seam that holds
	// one answer back while a later one lands
	beforeCall func(method string, params []any)

	mu      sync.Mutex
	client  *golibrpc.Client
	gen     uint64
	version string
}

// NewSession is the session to the daemon on the unix socket addr. spawn, when not nil, starts the
// daemon once when nothing answers.
func NewSession(addr string, spawn func() (string, error)) *Session {
	return &Session{addr: addr, spawn: spawn, window: spawnProbeWindow}
}

// Connect dials the daemon: on refusal it spawns --serve (once, when it may) and retries with
// backoff, 100 ms doubling to 2 s, within the probe window; then it says hello at this build's
// protocol. The generation moves as soon as Connect begins.
func (s *Session) Connect(ctx context.Context) error {
	s.mu.Lock()
	old := s.client
	s.client = nil
	s.gen++
	s.mu.Unlock()
	if old != nil {
		_ = old.Close()
	}
	backoff := 100 * time.Millisecond
	spawned, logPath := false, ""
	var deadline time.Time
	var cli *golibrpc.Client
	for {
		var err error
		cli, err = golibrpc.Dial(ctx, s.addr, msgpackrpc.New(nil), golibrpc.ClientNetwork("unix"))
		if err == nil {
			break
		}
		if deadline.IsZero() {
			deadline = time.Now().Add(s.window)
		}
		if s.spawn != nil && !spawned {
			if logPath, err = s.spawn(); err != nil {
				return fmt.Errorf("starting autodoc --serve: %w", err)
			}
			spawned = true
		}
		if time.Now().After(deadline) {
			return &ConnectError{Addr: s.addr, Window: s.window, LogPath: logPath, Last: err}
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(backoff):
		}
		backoff = min(backoff*2, 2*time.Second)
	}
	res, err := cli.Call(ctx, "sys.hello", map[string]any{"protocol": rpc.Protocol, "name": "autodoc-tui"})
	if err != nil {
		_ = cli.Close()
		return fmt.Errorf("hello: %w", err)
	}
	m, _ := res.(map[string]any)
	v, _ := m["version"].(string)
	s.mu.Lock()
	s.client, s.version = cli, v
	s.mu.Unlock()
	return nil
}

// Call calls method on the connection there is now.
func (s *Session) Call(ctx context.Context, method string, params ...any) (any, error) {
	s.mu.Lock()
	cli := s.client
	s.mu.Unlock()
	if cli == nil {
		return nil, errNotConnected
	}
	if s.beforeCall != nil {
		s.beforeCall(method, params)
	}
	return cli.Call(ctx, method, params...)
}

// Gen is the connection's generation.
func (s *Session) Gen() uint64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.gen
}

// Version is the daemon's, as its hello said.
func (s *Session) Version() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.version
}

// Done is closed when the connection ends; nil (never ready) with none.
func (s *Session) Done() <-chan struct{} {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.client == nil {
		return nil
	}
	return s.client.Done()
}

// Close ends the connection.
func (s *Session) Close() {
	s.mu.Lock()
	cli := s.client
	s.client = nil
	s.gen++
	s.mu.Unlock()
	if cli != nil {
		_ = cli.Close()
	}
}

// wireMessage is what a failed call says to the user: the daemon's constant message, or the error.
func wireMessage(err error) string {
	var e *golibrpc.Error
	if errors.As(err, &e) {
		return e.Message
	}
	return err.Error()
}

// code is a failed call's error code, 0 when it is not the daemon's.
func code(err error) int64 {
	var e *golibrpc.Error
	if errors.As(err, &e) {
		return e.Code
	}
	return 0
}
