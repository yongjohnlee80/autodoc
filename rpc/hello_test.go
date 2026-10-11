package rpc

import (
	"bufio"
	"context"
	"net"
	"testing"
	"time"

	golibrpc "github.com/yongjohnlee80/golib/server/rpc"
	"github.com/yongjohnlee80/golib/server/rpc/msgpackrpc"
)

// pipe is a raw connection to a server: requests are written back to back, without waiting for
// any answer, as a client that pipelines does.
type pipe struct {
	t     *testing.T
	codec golibrpc.Codec
	r     *bufio.Reader
	w     *bufio.Writer
}

func rawServer(t *testing.T) *pipe {
	t.Helper()
	sock := shortSocket(t)
	ln, err := net.Listen("unix", sock)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	srv := New(Fixed(), "v1", WithListener(ln))
	done := make(chan struct{})
	go func() { _ = srv.Run(ctx); close(done) }()
	conn, err := net.Dial("unix", sock)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = conn.Close(); cancel(); <-done })
	return &pipe{t: t, codec: msgpackrpc.New(nil), r: bufio.NewReader(conn), w: bufio.NewWriter(conn)}
}

// send writes requests and flushes once, after the last.
func (p *pipe) send(ms ...*golibrpc.Message) {
	p.t.Helper()
	for _, m := range ms {
		m.Kind = golibrpc.KindRequest
		if err := p.codec.Write(p.w, m); err != nil {
			p.t.Fatal(err)
		}
	}
	if err := p.w.Flush(); err != nil {
		p.t.Fatal(err)
	}
}

// answers reads n responses, by request id.
func (p *pipe) answers(n int) map[uint32]*golibrpc.Message {
	p.t.Helper()
	out := map[uint32]*golibrpc.Message{}
	for len(out) < n {
		m, err := p.codec.Read(p.r)
		if err != nil {
			p.t.Fatal(err)
		}
		out[m.ID] = m
	}
	return out
}

func errCode(m *golibrpc.Message) int64 {
	if m.Err == nil {
		return 0
	}
	e, _ := m.Err.(map[string]any)
	switch c := e["code"].(type) {
	case int64:
		return c
	case uint64:
		return int64(c)
	}
	return -1
}

func hold(t *testing.T, d time.Duration) {
	t.Helper()
	beforeHello = func() { time.Sleep(d) }
	t.Cleanup(func() { beforeHello = nil })
}

// TestAVerbPipelinedBehindHelloIsServed: a verb written right behind sys.hello, before its answer,
// waits at the gate for the hello and is served, however long the hello takes within the bound.
func TestAVerbPipelinedBehindHelloIsServed(t *testing.T) {
	hold(t, 300*time.Millisecond)
	p := rawServer(t)
	p.send(&golibrpc.Message{ID: 1, Method: "sys.hello", Params: []any{map[string]any{"protocol": Protocol, "name": CLIName}}},
		&golibrpc.Message{ID: 2, Method: "sys.capabilities"})
	got := p.answers(2)
	if errCode(got[1]) != 0 || errCode(got[2]) != 0 {
		t.Fatalf("hello: %+v; the verb behind it: %+v; want both served", got[1].Err, got[2].Err)
	}
}

// TestTheGateWaitsOnlyForAHello: a hello that is refused, a probe (no protocol) and a hello with
// malformed params all answer, and the verb behind each is refused at once, not after the bound;
// a hello held past the bound leaves the verb behind it refused as before, never hung.
func TestTheGateWaitsOnlyForAHello(t *testing.T) {
	saved := helloWait
	helloWait = 2 * time.Second
	t.Cleanup(func() { helloWait = saved })
	for _, c := range []struct {
		name  string
		hello []any
		want  int64
	}{
		{"a probe", []any{map[string]any{}}, CodeHandshakeRequired},
		{"malformed params", []any{"not a map"}, CodeHandshakeRequired},
		{"a refused protocol", []any{map[string]any{"protocol": Protocol + 1, "name": CLIName}}, CodeProtocolMismatch},
	} {
		p := rawServer(t)
		start := time.Now()
		p.send(&golibrpc.Message{ID: 1, Method: "sys.hello", Params: c.hello}, &golibrpc.Message{ID: 2, Method: "sys.capabilities"})
		got := p.answers(2)
		if code := errCode(got[2]); code != c.want || time.Since(start) > time.Second {
			t.Errorf("%s: the verb behind it answered %d after %s; want %d at once", c.name, code, time.Since(start), c.want)
		}
	}
	helloWait = 100 * time.Millisecond
	hold(t, 600*time.Millisecond)
	p := rawServer(t)
	start := time.Now()
	p.send(&golibrpc.Message{ID: 1, Method: "sys.hello", Params: []any{map[string]any{"protocol": Protocol, "name": CLIName}}},
		&golibrpc.Message{ID: 2, Method: "sys.capabilities"})
	got := p.answers(2)
	if errCode(got[2]) != CodeHandshakeRequired || time.Since(start) < 100*time.Millisecond || errCode(got[1]) != 0 {
		t.Fatalf("past the bound: verb %+v, hello %+v after %s; want the verb refused, the hello served", got[2].Err, got[1].Err, time.Since(start))
	}
}
