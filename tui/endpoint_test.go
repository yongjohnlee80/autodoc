package tui

import (
	"context"
	"errors"
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"

	golibrpc "github.com/yongjohnlee80/golib/server/rpc"
	"github.com/yongjohnlee80/golib/server/rpc/msgpackrpc"

	"github.com/yongjohnlee80/autodoc/rpc"
)

// helloDaemon serves sys.hello at sock with hello's answer to a declared protocol, and its own
// protocol to a probe (no protocol declared).
func helloDaemon(t *testing.T, sock string, own int64, hello func(declared int64) map[string]any) {
	t.Helper()
	ln, err := net.Listen("unix", sock)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	srv := golibrpc.New(msgpackrpc.New(nil), golibrpc.WithListener(ln))
	srv.Handle("sys.hello", func(_ context.Context, req *golibrpc.Request) (any, error) {
		m, _ := req.Params[0].(map[string]any)
		p, declared := m["protocol"].(int64)
		if !declared {
			return map[string]any{"protocol": own, "server": rpc.ServerName, "version": "v-fake", "pid": int64(os.Getpid())}, nil
		}
		return hello(p), nil
	})
	stopped := make(chan struct{})
	go func() { _ = srv.Run(ctx); close(stopped) }()
	t.Cleanup(func() { cancel(); <-stopped })
}

// TestTheTUIRefusesADaemonOfAnotherProtocol: a daemon that admits the TUI's hello but serves another
// protocol is a mismatch to the TUI all the same: Connect takes the mismatch path, never a session
// that half works.
func TestTheTUIRefusesADaemonOfAnotherProtocol(t *testing.T) {
	sock := filepath.Join(shortDir(t), "s.sock")
	newer := rpc.Protocol + 1
	helloDaemon(t, sock, newer, func(declared int64) map[string]any {
		return map[string]any{"protocol": declared, "server_protocol": newer, "version": "v-fake", "pid": int64(os.Getpid())}
	})
	s := NewSession(sock, nil)
	err := s.Connect(context.Background())
	var mm *MismatchError
	if !errors.As(err, &mm) || mm.Server != newer {
		t.Fatalf("Connect to a daemon serving protocol %d: %v, want a mismatch", newer, err)
	}
}

// TestAResolvedDaemonGoneByTheDialCostsOneSpawn: the address a resolver found answers nothing by the
// time it is dialed; Connect spawns once, dials the configured socket the spawned daemon binds, and
// connects there.
func TestAResolvedDaemonGoneByTheDialCostsOneSpawn(t *testing.T) {
	dir := shortDir(t)
	configured, gone := filepath.Join(dir, "c.sock"), filepath.Join(dir, "gone.sock")
	spawns := 0
	s := NewSession(configured, func() (string, error) {
		spawns++
		helloDaemon(t, configured, rpc.Protocol, func(declared int64) map[string]any {
			return map[string]any{"protocol": declared, "server_protocol": rpc.Protocol, "version": "v-spawned", "pid": int64(os.Getpid())}
		})
		return "", nil
	}).UseResolver(configured, func(context.Context) string { return gone })
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := s.Connect(ctx); err != nil {
		t.Fatalf("Connect: %v", err)
	}
	if spawns != 1 || s.Address() != configured || s.Version() != "v-spawned" {
		t.Errorf("spawns %d, address %s, version %s; want one spawn and the configured socket", spawns, s.Address(), s.Version())
	}
}
