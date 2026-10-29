package client

import (
	"context"
	"errors"
	"net"
	"path/filepath"
	"testing"
	"time"

	golibrpc "github.com/yongjohnlee80/golib/server/rpc"
	"github.com/yongjohnlee80/golib/server/rpc/msgpackrpc"

	"github.com/yongjohnlee80/autodoc/core/store"
	"github.com/yongjohnlee80/autodoc/internal/endpoint"
	"github.com/yongjohnlee80/autodoc/rpc"
)

// daemonSaying is a daemon of this build's protocol at sock whose hello says storeID ("" leaves
// store_id out).
func daemonSaying(t *testing.T, sock, storeID string) {
	t.Helper()
	ln, err := net.Listen("unix", sock)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	srv := golibrpc.New(msgpackrpc.New(nil), golibrpc.WithListener(ln))
	srv.Handle("sys.hello", func(context.Context, *golibrpc.Request) (any, error) {
		reply := map[string]any{"protocol": rpc.Protocol, "server_protocol": rpc.Protocol, "min_protocol": rpc.MinProtocol, "version": "v"}
		if storeID != "" {
			reply["store_id"] = storeID
		}
		return reply, nil
	})
	done := make(chan struct{})
	go func() { _ = srv.Run(ctx); close(done) }()
	t.Cleanup(func() { cancel(); <-done })
}

// TestADaemonThatSaysNoStoreIsRefused: a session for a store refuses a daemon whose hello, at a
// protocol that says store_id, leaves it out, rather than take it for the store's; the same daemon
// saying the store's id is connected to.
func TestADaemonThatSaysNoStoreIsRefused(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	storePath := filepath.Join(t.TempDir(), "autodoc.db")
	db, err := store.Open(ctx, storePath)
	if err != nil {
		t.Fatal(err)
	}
	_ = db.Close()
	id, err := store.Identity(storePath)
	if err != nil {
		t.Fatal(err)
	}

	sock := filepath.Join(shortDir(t), "s.sock")
	daemonSaying(t, sock, "")
	s := NewSession(sock, nil).As(rpc.CLIName).ForStore(sock, storePath)
	if err := s.Connect(ctx); !errors.Is(err, endpoint.ErrNoStoreID) {
		s.Close()
		t.Fatalf("a daemon saying no store: %v, want ErrNoStoreID", err)
	}

	sock = filepath.Join(shortDir(t), "s.sock")
	daemonSaying(t, sock, id)
	s = NewSession(sock, nil).As(rpc.CLIName).ForStore(sock, storePath)
	if err := s.Connect(ctx); err != nil {
		t.Fatalf("the store's daemon: %v", err)
	}
	s.Close()
}
