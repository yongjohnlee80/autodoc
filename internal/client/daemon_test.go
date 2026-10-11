package client

import (
	"context"
	"fmt"
	"net"
	"os"
	"testing"

	golibrpc "github.com/yongjohnlee80/golib/server/rpc"
	"github.com/yongjohnlee80/golib/server/rpc/msgpackrpc"

	"github.com/yongjohnlee80/autodoc/rpc"
)

// otherDaemon is a daemon of protocol proto at sock: its hello refuses any other, admits proto, and
// answers a probe with its number; a refused session stays refused, as rpc's does (sessRefused);
// sys.shutdown, admitted, stops it.
func otherDaemon(t *testing.T, sock string, proto int64) (stopped chan struct{}) {
	t.Helper()
	ln, err := net.Listen("unix", sock)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	srv := golibrpc.New(msgpackrpc.New(nil), golibrpc.WithListener(ln))
	srv.Handle("sys.hello", func(_ context.Context, req *golibrpc.Request) (any, error) {
		reply := map[string]any{"protocol": proto, "version": fmt.Sprintf("v-p%d", proto), "pid": int64(os.Getpid())}
		m, _ := req.Params[0].(map[string]any)
		refused, _ := req.Session.Value("refused").(bool)
		switch p, declared := m["protocol"]; {
		case !declared:
			return reply, nil
		case p != proto || refused:
			req.Session.SetValue("refused", true)
			return nil, &golibrpc.Error{Code: rpc.CodeProtocolMismatch, Message: fmt.Sprintf("protocol mismatch: client %v, server %d", p, proto)}
		}
		req.Session.SetValue("hello", true)
		return reply, nil
	})
	srv.Handle("sys.shutdown", func(_ context.Context, req *golibrpc.Request) (any, error) {
		if ok, _ := req.Session.Value("hello").(bool); !ok {
			return nil, &golibrpc.Error{Code: rpc.CodeHandshakeRequired, Message: "handshake required"}
		}
		go cancel()
		return nil, nil
	})
	stopped = make(chan struct{})
	go func() { _ = srv.Run(ctx); close(stopped) }()
	t.Cleanup(func() { cancel(); <-stopped })
	return stopped
}
