package client

import (
	"context"
	"errors"
	"fmt"
	"net"
	"path/filepath"
	"strings"
	"testing"
	"time"

	golibrpc "github.com/yongjohnlee80/golib/server/rpc"
	"github.com/yongjohnlee80/golib/server/rpc/msgpackrpc"

	"github.com/yongjohnlee80/autodoc/rpc"
)

// TestAClientSurvivesAnOlderDaemon: a session that is not the TUI's, refused by a daemon one
// protocol older, says hello again at that daemon's protocol on a new connection (the refused one is
// spent) and is served; the TUI's is a mismatch, kept as Stale; a daemon older than this build serves
// is a mismatch for both.
func TestAClientSurvivesAnOlderDaemon(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	sock := filepath.Join(shortDir(t), "s.sock")
	stopped := otherDaemon(t, sock, rpc.Protocol-1)

	tui := NewSession(sock, nil)
	var mismatch *MismatchError
	if err := tui.Connect(ctx); !errors.As(err, &mismatch) || tui.Stale() == nil {
		t.Fatalf("the TUI's hello to an older daemon: %v, stale %v; want a mismatch kept as Stale", err, tui.Stale())
	}

	cli := NewSession(sock, nil).As(rpc.CLIName)
	if err := cli.Connect(ctx); err != nil {
		t.Fatalf("a client's hello to a daemon one protocol older: %v", err)
	}
	defer cli.Close()
	if cli.Version() != fmt.Sprintf("v-p%d", rpc.Protocol-1) || cli.Stale() != nil {
		t.Fatalf("connected to %q, stale %v", cli.Version(), cli.Stale())
	}
	if _, err := cli.Call(ctx, "sys.shutdown"); err != nil {
		t.Fatalf("an admitted session's call: %v", err)
	}
	<-stopped

	sock = filepath.Join(shortDir(t), "s.sock")
	otherDaemon(t, sock, rpc.MinProtocol-1)
	tooOld := NewSession(sock, nil).As(rpc.CLIName)
	if err := tooOld.Connect(ctx); !errors.As(err, &mismatch) || mismatch.Server != rpc.MinProtocol-1 {
		t.Fatalf("a daemon below MinProtocol: %v; want a mismatch naming its protocol", err)
	}
}

// refusingDaemon refuses every hello that declares a protocol; a probe is answered as protocol
// Protocol-1, or with an error when probeFails.
func refusingDaemon(t *testing.T, sock string, probeFails bool) {
	t.Helper()
	ln, err := net.Listen("unix", sock)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	srv := golibrpc.New(msgpackrpc.New(nil), golibrpc.WithListener(ln))
	srv.Handle("sys.hello", func(_ context.Context, req *golibrpc.Request) (any, error) {
		m, _ := req.Params[0].(map[string]any)
		if _, declared := m["protocol"]; declared {
			return nil, &golibrpc.Error{Code: rpc.CodeProtocolMismatch, Message: "protocol mismatch"}
		}
		if probeFails {
			return nil, &golibrpc.Error{Code: golibrpc.CodeInternal, Message: "no probe today"}
		}
		return map[string]any{"protocol": rpc.Protocol - 1, "version": "v-liar"}, nil
	})
	done := make(chan struct{})
	go func() { _ = srv.Run(ctx); close(done) }()
	t.Cleanup(func() { cancel(); <-done })
}

// TestAClientsSecondHelloCanFail: a client refused by a daemon whose probe then fails, or that
// refuses even the protocol it named, says which step failed.
func TestAClientsSecondHelloCanFail(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	for _, c := range []struct {
		probeFails bool
		says       string
	}{
		{true, "hello: protocol mismatch, and the probe failed"},
		{false, fmt.Sprintf("hello at protocol %d", rpc.Protocol-1)},
	} {
		sock := filepath.Join(shortDir(t), "s.sock")
		refusingDaemon(t, sock, c.probeFails)
		err := NewSession(sock, nil).As(rpc.CLIName).Connect(ctx)
		if err == nil || !strings.Contains(err.Error(), c.says) {
			t.Errorf("probe fails %v: %v; want it to say %q", c.probeFails, err, c.says)
		}
	}
}
